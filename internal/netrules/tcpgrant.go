package netrules

import "net/netip"

// TCPGrant is one explicit raw-TCP egress grant: outbound TCP to exactly one
// IPv4 address and one port, bypassing the HTTP proxy under a strict network
// policy.
type TCPGrant struct {
	IP   netip.Addr
	Port int
}

// ParseTCPGrant parses an "<IPv4-literal>:<port>" string.
func ParseTCPGrant(s string) (TCPGrant, error) {
	return TCPGrant{}, nil
}

// Validate reports whether g is a well-formed grant.
func (g TCPGrant) Validate() error { return nil }

// String renders g as "<ip>:<port>".
func (g TCPGrant) String() string { return "" }

// ParseTCPGrants parses and validates a list of grants, rejecting duplicates.
func ParseTCPGrants(entries []string) ([]TCPGrant, error) { return nil, nil }
