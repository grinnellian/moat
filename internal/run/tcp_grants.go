package run

import (
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/netrules"
)

// appendNoProxyGrants returns a copy of env with the granted IPs appended to
// the NO_PROXY and no_proxy entries, so tools that honor the proxy variables
// connect to a granted IP:port directly instead of through the proxy (where
// the raw TCP the grant exists for, such as SSH, could not flow anyway).
// Existing entries are never dropped, IPs are deduplicated, and with no grants
// env is returned unchanged.
func appendNoProxyGrants(env []string, grants []netrules.TCPGrant) []string {
	if len(grants) == 0 {
		return env
	}
	var ips []string
	seen := make(map[string]bool, len(grants))
	for _, g := range grants {
		ip := g.IP.String()
		if !seen[ip] {
			seen[ip] = true
			ips = append(ips, ip)
		}
	}
	suffix := "," + strings.Join(ips, ",")

	out := make([]string, len(env))
	for i, e := range env {
		switch {
		case strings.HasPrefix(e, "NO_PROXY="), strings.HasPrefix(e, "no_proxy="):
			// Append (never replace); skip a leading comma on an empty list.
			if strings.HasSuffix(e, "=") {
				out[i] = e + strings.TrimPrefix(suffix, ",")
			} else {
				out[i] = e + suffix
			}
		default:
			out[i] = e
		}
	}
	return out
}

// checkTCPGrantsSupported refuses network.tcp on runtimes that cannot enforce
// it. Only the Docker runtime (which includes Podman) implements the grant
// rules; refusing up front avoids starting a container that silently lacks the
// access the operator asked for.
func checkTCPGrantsSupported(rt container.RuntimeType, grants []netrules.TCPGrant) error {
	if len(grants) > 0 && rt == container.RuntimeApple {
		return fmt.Errorf("network.tcp is not supported on the Apple container runtime (Docker/Podman only); remove it or run with --runtime docker")
	}
	return nil
}
