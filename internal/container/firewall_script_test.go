package container

import (
	"context"
	"os"
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
	// is part of the single grant rule (and its comment).
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

func TestAppleSetupFirewall_RefusesTCPGrants(t *testing.T) {
	rt := &AppleRuntime{} // must refuse before touching the CLI
	err := rt.SetupFirewall(context.Background(), "cid", "moat-proxy", 3128, []netrules.TCPGrant{mustGrant(t, "10.1.2.3:22")})
	if err == nil || !strings.Contains(err.Error(), "network.tcp") {
		t.Fatalf("want an error naming network.tcp, got %v", err)
	}
}
