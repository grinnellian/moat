package container

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/netrules"
)

func mustGrant(t *testing.T, s string) netrules.TCPGrant {
	t.Helper()
	g, err := netrules.ParseTCPGrant(s)
	if err != nil {
		t.Fatalf("ParseTCPGrant(%q): %v", s, err)
	}
	return g
}

// goldenNoGrants is the script moat generated before network.tcp existed,
// rendered for proxy port 3128 (extracted from the pre-change source).
func goldenNoGrants(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/docker_firewall_no_grants.golden")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildDockerFirewallScript_NoGrantsIsByteIdentical(t *testing.T) {
	want := goldenNoGrants(t)
	for name, grants := range map[string][]netrules.TCPGrant{"nil": nil, "empty": {}} {
		got, err := buildDockerFirewallScript(3128, grants)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s: script differs from pinned pre-change script:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}
}

func TestBuildDockerFirewallScript_GrantRule(t *testing.T) {
	got, err := buildDockerFirewallScript(3128, []netrules.TCPGrant{mustGrant(t, "10.1.2.3:22")})
	if err != nil {
		t.Fatal(err)
	}

	// The fail-closed helper (only invoked on a grant failure) contains its own
	// DROP; strip it so the placement checks look at the main rule sequence.
	got = stripFailClosedFunc(t, got)

	const wantRule = "iptables -w -A OUTPUT -p tcp -d 10.1.2.3 --dport 22 -j ACCEPT"
	if n := strings.Count(got, wantRule); n != 1 {
		t.Fatalf("want exactly one %q, found %d in:\n%s", wantRule, n, got)
	}

	// Placement: after the proxy-port ACCEPT, before the final DROP.
	proxyRule := strings.Index(got, "iptables -w -A OUTPUT -p tcp --dport 3128 -j ACCEPT")
	grantRule := strings.Index(got, wantRule)
	drop := strings.Index(got, "iptables -w -A OUTPUT -j DROP")
	if proxyRule < 0 || drop < 0 || grantRule < 0 || !(proxyRule < grantRule && grantRule < drop) {
		t.Errorf("rule order wrong: proxy=%d grant=%d drop=%d", proxyRule, grantRule, drop)
	}

	// No other new ACCEPT and nothing new on the IPv6 chain: every added line
	// is part of the single grant rule, its comment, or the fail-closed helper
	// (which only runs when a grant fails to install).
	golden := goldenNoGrants(t)
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(golden, line) {
			continue
		}
		if !strings.Contains(line, "10.1.2.3") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("unexpected new line in script: %q", line)
		}
	}
	if strings.Count(got, "-j ACCEPT") != strings.Count(golden, "-j ACCEPT")+1 {
		t.Errorf("want exactly one additional ACCEPT")
	}
	if strings.Contains(got, "$IP6T -w 5 -A OUTPUT -p tcp -d") {
		t.Errorf("grant leaked into the IPv6 chain")
	}
	// The whole ip6 section is byte-identical to the pre-change script.
	ip6 := func(s string) string { return s[strings.Index(s, "# Mirror rules for IPv6"):] }
	if ip6(got) != ip6(golden) {
		t.Errorf("IPv6 section changed")
	}
}

// stripFailClosedFunc removes the moat_fail_closed shell function definition,
// which is only ever invoked when a grant fails to install.
func stripFailClosedFunc(t *testing.T, script string) string {
	t.Helper()
	start := strings.Index(script, "\n\t\tmoat_fail_closed() {")
	if start < 0 {
		t.Fatal("moat_fail_closed definition not found")
	}
	end := strings.Index(script[start:], "\n\t\t}")
	if end < 0 {
		t.Fatal("moat_fail_closed definition not terminated")
	}
	return script[:start] + script[start+end+len("\n\t\t}"):]
}

func TestBuildDockerFirewallScript_MultipleGrantsKeepOrder(t *testing.T) {
	got, err := buildDockerFirewallScript(3128, []netrules.TCPGrant{
		mustGrant(t, "10.1.2.3:22"), mustGrant(t, "10.1.2.3:8080"), mustGrant(t, "192.0.2.9:22"),
	})
	if err != nil {
		t.Fatal(err)
	}
	a := strings.Index(got, "-d 10.1.2.3 --dport 22 ")
	b := strings.Index(got, "-d 10.1.2.3 --dport 8080 ")
	c := strings.Index(got, "-d 192.0.2.9 --dport 22 ")
	if a < 0 || b < 0 || c < 0 || !(a < b && b < c) {
		t.Errorf("grants missing or out of order: %d %d %d", a, b, c)
	}
	if n := strings.Count(got, "-p tcp -d "); n != 3 {
		t.Errorf("want 3 grant rules, got %d", n)
	}
}

func TestBuildDockerFirewallScript_RejectsInvalidGrant(t *testing.T) {
	// A zero-value grant must never be rendered into a shell script.
	if _, err := buildDockerFirewallScript(3128, []netrules.TCPGrant{{}}); err == nil {
		t.Fatal("expected error for zero-value grant")
	}
	bad := mustGrant(t, "10.1.2.3:22")
	bad.Port = 0
	if _, err := buildDockerFirewallScript(3128, []netrules.TCPGrant{bad}); err == nil {
		t.Fatal("expected error for port 0")
	}
}

// fakeFirewallRun is the outcome of running a firewall script against stubs.
type fakeFirewallRun struct {
	code   int
	v4Log  string // every iptables invocation, one per line, in order
	v6Log  string // every ip6tables-legacy invocation, one per line, in order
	stderr string
}

// runScriptWithFakeFirewall runs the generated script under sh with stub
// iptables and ip6tables-legacy binaries on PATH. Both log their arguments;
// iptables fails (exit 1) whenever its arguments contain failOn (if non-empty).
func runScriptWithFakeFirewall(t *testing.T, script, failOn string) fakeFirewallRun {
	t.Helper()
	dir := t.TempDir()
	mk := func(name, failPattern string) {
		stub := "#!/bin/sh\necho \"$*\" >> \"$LOGDIR/" + name + ".log\"\n"
		if failPattern != "" {
			stub += "case \"$*\" in *" + failPattern + "*) echo \"stub: refusing $*\" >&2; exit 1;; esac\n"
		}
		stub += "exit 0\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("iptables", failOn)
	mk("ip6tables-legacy", "")

	var stderr strings.Builder
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "LOGDIR=" + dir}
	cmd.Stderr = &stderr
	res := fakeFirewallRun{}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("running script: %v", err)
		}
		res.code = ee.ExitCode()
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name+".log"))
		return string(b)
	}
	res.v4Log, res.v6Log, res.stderr = read("iptables"), read("ip6tables-legacy"), stderr.String()
	return res
}

// runScriptWithFakeIptables returns only the exit code of the script run.
func runScriptWithFakeIptables(t *testing.T, script, failOn string) int {
	t.Helper()
	return runScriptWithFakeFirewall(t, script, failOn).code
}

// A grant that fails to install must fail the script AND leave the container
// firewalled (fail closed): the final IPv4 DROP and the IPv6 lockdown are still
// installed, so egress is not left open if the caller then fails to stop the
// container.
func TestBuildDockerFirewallScript_GrantFailureFailsClosed(t *testing.T) {
	script, err := buildDockerFirewallScript(3128, []netrules.TCPGrant{mustGrant(t, "10.1.2.3:22"), mustGrant(t, "10.1.2.3:8080")})
	if err != nil {
		t.Fatal(err)
	}
	res := runScriptWithFakeFirewall(t, script, "-d 10.1.2.3 --dport 22 ")
	if res.code == 0 {
		t.Fatal("script exited 0 although a grant failed")
	}

	lines := strings.Split(strings.TrimSpace(res.v4Log), "\n")
	failedAt, dropAt := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "-d 10.1.2.3 --dport 22 ") {
			failedAt = i
		}
		if strings.Contains(l, "-A OUTPUT -j DROP") {
			dropAt = i
		}
	}
	if failedAt < 0 || dropAt < 0 || dropAt < failedAt {
		t.Errorf("IPv4 DROP not installed after the failed grant (failed=%d drop=%d); iptables log:\n%s", failedAt, dropAt, res.v4Log)
	}
	// The later grant never installs after a failure.
	if strings.Contains(res.v4Log, "--dport 8080 -j ACCEPT") {
		t.Errorf("a later grant was installed after the failure:\n%s", res.v4Log)
	}
	if !strings.Contains(res.v6Log, "-A OUTPUT -j DROP") {
		t.Errorf("IPv6 lockdown missing after the failed grant; ip6tables log:\n%q", res.v6Log)
	}
	if !strings.Contains(res.stderr, "network.tcp") {
		t.Errorf("stderr should name the failed grant, got %q", res.stderr)
	}
}

// On success exactly one IPv4 DROP is installed (the failure path adds none).
func TestBuildDockerFirewallScript_GrantSuccessInstallsSingleDrop(t *testing.T) {
	script, err := buildDockerFirewallScript(3128, []netrules.TCPGrant{mustGrant(t, "10.1.2.3:22")})
	if err != nil {
		t.Fatal(err)
	}
	res := runScriptWithFakeFirewall(t, script, "")
	if res.code != 0 {
		t.Fatalf("exit %d", res.code)
	}
	if n := strings.Count(res.v4Log, "-A OUTPUT -j DROP"); n != 1 {
		t.Errorf("want exactly one IPv4 DROP, got %d:\n%s", n, res.v4Log)
	}
}

func TestBuildDockerFirewallScript_GrantFailureFailsTheScript(t *testing.T) {
	script, err := buildDockerFirewallScript(3128, []netrules.TCPGrant{mustGrant(t, "10.1.2.3:22")})
	if err != nil {
		t.Fatal(err)
	}
	if code := runScriptWithFakeIptables(t, script, ""); code != 0 {
		t.Fatalf("script with a working iptables exited %d, want 0", code)
	}
	if code := runScriptWithFakeIptables(t, script, "-d 10.1.2.3"); code == 0 {
		t.Fatal("script exited 0 although the grant's iptables command failed")
	}
}

func TestBuildDockerFirewallScript_NoGrantsStillExitsZero(t *testing.T) {
	script, err := buildDockerFirewallScript(3128, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code := runScriptWithFakeIptables(t, script, ""); code != 0 {
		t.Fatalf("no-grants script exited %d, want 0", code)
	}
}

func TestAppleSetupFirewall_RefusesTCPGrants(t *testing.T) {
	rt := &AppleRuntime{} // must refuse before touching the CLI
	err := rt.SetupFirewall(context.Background(), "cid", "moat-proxy", 3128, []netrules.TCPGrant{mustGrant(t, "10.1.2.3:22")})
	if err == nil || !strings.Contains(err.Error(), "network.tcp") {
		t.Fatalf("want an error naming network.tcp, got %v", err)
	}
}
