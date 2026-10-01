package run

import (
	"context"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/netrules"
)

// tcpGrants builds grants directly (not via the parser) so these tests do not
// depend on parser behaviour.
func tcpGrants(t *testing.T, ss ...string) []netrules.TCPGrant {
	t.Helper()
	var gs []netrules.TCPGrant
	for _, s := range ss {
		ap := netip.MustParseAddrPort(s)
		gs = append(gs, netrules.TCPGrant{IP: ap.Addr(), Port: int(ap.Port())})
	}
	return gs
}

func envValue(env []string, key string) (string, bool) {
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			return strings.TrimPrefix(e, key+"="), true
		}
	}
	return "", false
}

func TestAppendNoProxyGrants_NoGrantsLeavesEnvUntouched(t *testing.T) {
	for _, hostNet := range []bool{true, false} {
		env := buildProxyEnv("tok", 3128, hostNet)
		want := append([]string(nil), env...)
		got := appendNoProxyGrants(env, nil)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("hostNet=%v: env changed with no grants:\n got %v\nwant %v", hostNet, got, want)
		}
	}
}

func TestAppendNoProxyGrants_AppendsAndKeepsExisting(t *testing.T) {
	for _, hostNet := range []bool{true, false} {
		env := buildProxyEnv("tok", 3128, hostNet)
		oldUpper, _ := envValue(env, "NO_PROXY")
		oldLower, _ := envValue(env, "no_proxy")

		got := appendNoProxyGrants(env, tcpGrants(t, "10.1.2.3:22", "10.1.2.3:8080", "192.0.2.9:22"))

		for _, tc := range []struct{ key, old string }{{"NO_PROXY", oldUpper}, {"no_proxy", oldLower}} {
			v, ok := envValue(got, tc.key)
			if !ok {
				t.Fatalf("hostNet=%v: %s missing", hostNet, tc.key)
			}
			if !strings.HasPrefix(v, tc.old) {
				t.Errorf("hostNet=%v: %s = %q must keep existing %q as a prefix", hostNet, tc.key, v, tc.old)
			}
			if v != tc.old+",10.1.2.3,192.0.2.9" {
				t.Errorf("hostNet=%v: %s = %q, want existing + ,10.1.2.3,192.0.2.9 (deduped IPs, no ports)", hostNet, tc.key, v)
			}
			if n := strings.Count(strings.Join(got, "\n"), tc.key+"="); n != 1 {
				t.Errorf("%s appears %d times, want 1", tc.key, n)
			}
		}
		// Every other variable is untouched.
		if len(got) != len(env) {
			t.Errorf("env length changed: %d -> %d", len(env), len(got))
		}
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "MOAT_HOST_GATEWAY", "TERM"} {
			a, _ := envValue(env, k)
			b, _ := envValue(got, k)
			if a != b {
				t.Errorf("%s changed: %q -> %q", k, a, b)
			}
		}
	}
}

func TestAppendNoProxyGrants_DoesNotMutateInput(t *testing.T) {
	env := buildProxyEnv("tok", 3128, false)
	snapshot := append([]string(nil), env...)
	_ = appendNoProxyGrants(env, tcpGrants(t, "10.1.2.3:22"))
	if !reflect.DeepEqual(env, snapshot) {
		t.Errorf("input env mutated")
	}
}

func TestCheckTCPGrantsSupported(t *testing.T) {
	g := tcpGrants(t, "10.1.2.3:22")
	if err := checkTCPGrantsSupported(container.RuntimeDocker, g); err != nil {
		t.Errorf("docker must support network.tcp: %v", err)
	}
	if err := checkTCPGrantsSupported(container.RuntimeApple, nil); err != nil {
		t.Errorf("apple with no grants must be fine: %v", err)
	}
	err := checkTCPGrantsSupported(container.RuntimeApple, g)
	if err == nil || !strings.Contains(err.Error(), "network.tcp") || !strings.Contains(strings.ToLower(err.Error()), "apple") {
		t.Errorf("apple with grants must be refused with a clear error naming network.tcp and apple, got %v", err)
	}
}

// grantRecordingRuntime records the arguments SetupFirewall receives.
type grantRecordingRuntime struct {
	*stubRuntime
	called bool
	port   int
	grants []netrules.TCPGrant
}

func (g *grantRecordingRuntime) SetupFirewall(_ context.Context, _ string, _ string, port int, grants []netrules.TCPGrant) error {
	g.called, g.port, g.grants = true, port, grants
	return nil
}

func TestSetupFirewallPassesTCPGrantsToRuntime(t *testing.T) {
	rt := &grantRecordingRuntime{stubRuntime: &stubRuntime{}}
	m := mgrWithRuntime(rt)
	want := tcpGrants(t, "10.1.2.3:22", "10.1.2.3:8080")
	r := &Run{FirewallEnabled: true, ProxyHost: "moat-proxy", ProxyPort: 3128, ContainerID: "cid", TCPGrants: want}

	if err := m.setupFirewall(context.Background(), r); err != nil {
		t.Fatalf("setupFirewall: %v", err)
	}
	if !rt.called || rt.port != 3128 {
		t.Fatalf("runtime SetupFirewall not called as expected: called=%v port=%d", rt.called, rt.port)
	}
	if !reflect.DeepEqual(rt.grants, want) {
		t.Errorf("runtime got grants %v, want %v", rt.grants, want)
	}
}

func TestSetupFirewallWithoutGrantsPassesNone(t *testing.T) {
	rt := &grantRecordingRuntime{stubRuntime: &stubRuntime{}}
	m := mgrWithRuntime(rt)
	r := &Run{FirewallEnabled: true, ProxyHost: "moat-proxy", ProxyPort: 3128, ContainerID: "cid"}
	if err := m.setupFirewall(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if len(rt.grants) != 0 {
		t.Errorf("expected no grants, got %v", rt.grants)
	}
}
