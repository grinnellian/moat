package netrules

import (
	"strings"
	"testing"
)

func TestParseTCPGrant_Valid(t *testing.T) {
	tests := []struct {
		in       string
		wantIP   string
		wantPort int
	}{
		{"10.1.2.3:22", "10.1.2.3", 22},
		{"192.0.2.10:8080", "192.0.2.10", 8080},
		{"172.16.0.1:1", "172.16.0.1", 1},
		{"198.51.100.7:65535", "198.51.100.7", 65535},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			g, err := ParseTCPGrant(tt.in)
			if err != nil {
				t.Fatalf("ParseTCPGrant(%q): %v", tt.in, err)
			}
			if g.IP.String() != tt.wantIP || g.Port != tt.wantPort {
				t.Errorf("got %s:%d, want %s:%d", g.IP, g.Port, tt.wantIP, tt.wantPort)
			}
			if g.String() != tt.in {
				t.Errorf("String() = %q, want %q", g.String(), tt.in)
			}
			if err := g.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestParseTCPGrant_Invalid(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"hostname", "example.com:22"},
		{"localhost name", "localhost:22"},
		{"cidr", "10.0.0.0/8:22"},
		{"cidr no port", "10.0.0.0/8"},
		{"port range", "10.1.2.3:20-30"},
		{"port list", "10.1.2.3:22,80"},
		{"wildcard ip", "10.1.*:22"},
		{"wildcard star", "*:22"},
		{"wildcard port", "10.1.2.3:*"},
		{"unspecified", "0.0.0.0:22"},
		{"port zero", "10.1.2.3:0"},
		{"port too high", "10.1.2.3:65536"},
		{"port huge", "10.1.2.3:99999999999999999999"},
		{"port negative", "10.1.2.3:-1"},
		{"port plus", "10.1.2.3:+22"},
		{"port leading zero", "10.1.2.3:022"},
		{"missing port", "10.1.2.3"},
		{"empty port", "10.1.2.3:"},
		{"missing ip", ":22"},
		{"ipv6 bracketed", "[2001:db8::1]:22"},
		{"ipv6 bare", "2001:db8::1:22"},
		{"ipv4-mapped ipv6", "[::ffff:10.1.2.3]:22"},
		{"short ipv4", "10.1.2:22"},
		{"octet too big", "10.1.2.256:22"},
		{"leading zero octet", "010.1.2.3:22"},
		{"leading space", " 10.1.2.3:22"},
		{"trailing space", "10.1.2.3:22 "},
		{"inner space", "10.1.2.3: 22"},
		{"shell injection", "10.1.2.3;reboot:22"},
		{"newline", "10.1.2.3:22\n10.9.9.9:22"},
		{"scheme", "tcp://10.1.2.3:22"},
		{"zone", "10.1.2.3%eth0:22"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := ParseTCPGrant(tt.in)
			if err == nil {
				t.Fatalf("ParseTCPGrant(%q) = %v, want error", tt.in, g)
			}
		})
	}
}

func TestTCPGrantValidate_RejectsZeroValue(t *testing.T) {
	if err := (TCPGrant{}).Validate(); err == nil {
		t.Fatal("zero-value TCPGrant must not validate")
	}
}

func TestParseTCPGrants(t *testing.T) {
	got, err := ParseTCPGrants([]string{"10.1.2.3:22", "10.1.2.3:8080", "192.0.2.5:22"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[0].String() != "10.1.2.3:22" || got[1].String() != "10.1.2.3:8080" || got[2].String() != "192.0.2.5:22" {
		t.Fatalf("order/contents not preserved: %v", got)
	}

	if got, err := ParseTCPGrants(nil); err != nil || len(got) != 0 {
		t.Fatalf("nil input: got %v, %v", got, err)
	}

	_, err = ParseTCPGrants([]string{"10.1.2.3:22", "10.1.2.3:22"})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate: got %v, want duplicate error", err)
	}

	_, err = ParseTCPGrants([]string{"10.1.2.3:22", "example.com:22"})
	if err == nil || !strings.Contains(err.Error(), "network.tcp[1]") {
		t.Fatalf("bad entry: got %v, want error naming network.tcp[1]", err)
	}
}
