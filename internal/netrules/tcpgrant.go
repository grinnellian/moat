package netrules

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// TCPGrant is one explicit raw-TCP egress grant: outbound TCP to exactly one
// IPv4 address and one port, bypassing the HTTP proxy under a strict network
// policy. Grants are rendered into a root shell script that configures the
// container firewall, so parsing is deliberately strict: only an IPv4 literal
// and a canonical decimal port are accepted.
type TCPGrant struct {
	IP   netip.Addr
	Port int
}

// ParseTCPGrant parses an "<IPv4-literal>:<port>" string. It refuses
// hostnames, CIDRs, ranges, wildcards, non-unicast addresses (0.0.0.0/8,
// loopback, link-local, multicast, 240.0.0.0/4), IPv6, a missing or
// out-of-range port (1-65535), and anything with surrounding whitespace.
func ParseTCPGrant(s string) (TCPGrant, error) {
	// Exactly one colon: refuses bare and bracketed IPv6 outright.
	if strings.Count(s, ":") != 1 {
		return TCPGrant{}, fmt.Errorf("%q: want \"<IPv4-literal>:<port>\" (IPv6 is not supported)", s)
	}
	ipPart, portPart, _ := strings.Cut(s, ":")

	ip, err := netip.ParseAddr(ipPart)
	if err != nil || !ip.Is4() || ip.String() != ipPart {
		return TCPGrant{}, fmt.Errorf("%q: %q is not an IPv4 literal (hostnames, CIDRs and wildcards are not allowed)", s, ipPart)
	}

	if portPart == "" {
		return TCPGrant{}, fmt.Errorf("%q: missing port", s)
	}
	for _, c := range portPart {
		if c < '0' || c > '9' {
			return TCPGrant{}, fmt.Errorf("%q: port %q must be a plain decimal number (ranges and wildcards are not allowed)", s, portPart)
		}
	}
	port, err := strconv.Atoi(portPart)
	if err != nil || strconv.Itoa(port) != portPart {
		return TCPGrant{}, fmt.Errorf("%q: port %q must be a plain decimal number in 1-65535", s, portPart)
	}

	g := TCPGrant{IP: ip, Port: port}
	if err := g.Validate(); err != nil {
		return TCPGrant{}, fmt.Errorf("%q: %w", s, err)
	}
	return g, nil
}

// Validate reports whether g is a well-formed grant. It is the gate every
// consumer must pass before rendering a grant into a script, so a zero-value or
// hand-built TCPGrant can never reach the firewall.
func (g TCPGrant) Validate() error {
	if !g.IP.IsValid() || !g.IP.Is4() {
		return fmt.Errorf("address must be an IPv4 literal")
	}
	if !isOrdinaryUnicast(g.IP) {
		return fmt.Errorf("address %s is not an ordinary unicast host (0.0.0.0/8, loopback, link-local, multicast and 240.0.0.0/4 reserved/broadcast addresses are not allowed)", g.IP)
	}
	if g.Port < 1 || g.Port > 65535 {
		return fmt.Errorf("port %d is out of range (1-65535)", g.Port)
	}
	return nil
}

// Ranges a grant may never name: "this network" (0.0.0.0/8, includes 0.0.0.0)
// and the reserved block 240.0.0.0/4 (includes 255.255.255.255 broadcast).
var (
	thisNetwork = netip.MustParsePrefix("0.0.0.0/8")
	reserved    = netip.MustParsePrefix("240.0.0.0/4")
)

// isOrdinaryUnicast reports whether ip is a plain unicast host address: not
// 0.0.0.0/8, loopback (127/8), link-local (169.254/16, which holds the cloud
// metadata address), multicast (224/4), or the reserved 240/4 block.
func isOrdinaryUnicast(ip netip.Addr) bool {
	return !thisNetwork.Contains(ip) &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsMulticast() &&
		!reserved.Contains(ip)
}

// String renders g as "<ip>:<port>".
func (g TCPGrant) String() string {
	return g.IP.String() + ":" + strconv.Itoa(g.Port)
}

// ParseTCPGrants parses and validates a list of network.tcp entries,
// preserving order and rejecting duplicates. Errors name the offending entry
// as network.tcp[i].
func ParseTCPGrants(entries []string) ([]TCPGrant, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	grants := make([]TCPGrant, 0, len(entries))
	seen := make(map[TCPGrant]int, len(entries))
	for i, e := range entries {
		g, err := ParseTCPGrant(e)
		if err != nil {
			return nil, fmt.Errorf("network.tcp[%d]: %w", i, err)
		}
		if first, dup := seen[g]; dup {
			return nil, fmt.Errorf("network.tcp[%d]: duplicate of network.tcp[%d] (%s)", i, first, g)
		}
		seen[g] = i
		grants = append(grants, g)
	}
	return grants, nil
}
