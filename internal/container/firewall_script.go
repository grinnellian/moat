package container

import (
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/netrules"
)

// buildDockerFirewallScript renders the iptables/ip6tables script that
// SetupFirewall runs as root inside a Docker container. It is a pure function
// so the generated rules can be pinned by tests.
//
// With no tcpGrants the output is byte-identical to the script moat has always
// generated. Each grant adds exactly one IPv4 ACCEPT rule (tcp, one destination
// IP, one port) between the proxy-port rule and the final DROP. The IPv6 chain
// is never touched by grants: grants are IPv4 literals only.
func buildDockerFirewallScript(proxyPort int, tcpGrants []netrules.TCPGrant) (string, error) {
	// The grant block begins with its own leading newlines and has no trailing
	// newline, so an empty block leaves the surrounding script untouched.
	var grantBlock strings.Builder
	for i, g := range tcpGrants {
		// Never render an unvalidated grant into a root shell script.
		if err := g.Validate(); err != nil {
			return "", fmt.Errorf("invalid network.tcp grant %d: %w", i, err)
		}
		if i == 0 {
			grantBlock.WriteString("\n\n\t\t# Explicit raw-TCP egress grants (network.tcp): exactly this IPv4 address and port")
		}
		// A grant that fails to install must fail the script: the operator asked
		// for access the container would otherwise silently lack.
		fmt.Fprintf(&grantBlock, "\n\t\tiptables -w -A OUTPUT -p tcp -d %s --dport %d -j ACCEPT || { echo \"ERROR: failed to install network.tcp grant %s:%d\" >&2; exit 1; }", g.IP, g.Port, g.IP, g.Port)
	}

	script := fmt.Sprintf(`
		# Verify iptables is available
		if ! command -v iptables >/dev/null 2>&1; then
			echo "ERROR: iptables not found - container will not be firewalled" >&2
			exit 1
		fi

		# Flush existing rules (may fail if no rules exist, that's OK)
		iptables -w -F OUTPUT 2>/dev/null || true

		# Allow loopback
		iptables -w -A OUTPUT -o lo -j ACCEPT

		# Allow established/related connections (conntrack more reliable than state in containers)
		iptables -w -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

		# Allow DNS (UDP 53) - needed for initial hostname resolution
		iptables -w -A OUTPUT -p udp --dport 53 -j ACCEPT

		# Allow traffic to proxy port (destination IP not filtered - see function comment)
		iptables -w -A OUTPUT -p tcp --dport %d -j ACCEPT%s

		# Drop all other outbound traffic
		iptables -w -A OUTPUT -j DROP

		# Mirror rules for IPv6 to prevent bypass via AAAA records.
		# Prefer ip6tables-legacy for nf_tables compatibility on some
		# container kernels that lack nf_tables modules.
		# The DROP-all rule also blocks ICMPv6 Neighbor Solicitation, which
		# effectively disables IPv6 for the container — this is intentional;
		# fully blocked is better than partially open.
		if command -v ip6tables-legacy >/dev/null 2>&1; then
			IP6T=ip6tables-legacy
		elif command -v ip6tables >/dev/null 2>&1; then
			IP6T=ip6tables
		else
			echo "WARN: ip6tables not found - IPv6 traffic will not be firewalled" >&2
			IP6T=""
		fi
		if [ -n "$IP6T" ]; then
			# Use -w 5 (5-second timeout) instead of bare -w (wait forever).
			# On some CI hosts the ip6_tables kernel module is absent, causing
			# ip6tables to block indefinitely on the xtables lock.
			if $IP6T -w 5 -F OUTPUT 2>/dev/null &&
			   $IP6T -w 5 -A OUTPUT -o lo -j ACCEPT &&
			   $IP6T -w 5 -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT &&
			   $IP6T -w 5 -A OUTPUT -p udp --dport 53 -j ACCEPT &&
			   $IP6T -w 5 -A OUTPUT -p tcp --dport %d -j ACCEPT &&
			   $IP6T -w 5 -A OUTPUT -j DROP; then
				: # IPv6 firewall installed
			else
				# Flush partial rules so the container isn't left with an
				# incomplete policy (e.g. ACCEPT lo without a final DROP).
				$IP6T -w 5 -F OUTPUT 2>/dev/null || true
				echo "WARN: ip6tables rules failed — IPv6 traffic will not be firewalled" >&2
			fi
		fi
	`, proxyPort, grantBlock.String(), proxyPort)
	return script, nil
}
