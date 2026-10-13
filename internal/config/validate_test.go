package config

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateLoopback is the refusal matrix of DEP-3: every address is tried
// without and with --allow-unauthenticated-network.
func TestValidateLoopback(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		loopback bool
	}{
		{"localhost", "localhost:4655", true},
		{"localhost in capitals", "LOCALHOST:4655", true},
		{"127.0.0.1", "127.0.0.1:4655", true},
		{"another address in 127.0.0.0/8", "127.1.2.3:4655", true},
		{"end of 127.0.0.0/8", "127.255.255.254:4655", true},
		{"IPv6 loopback", "[::1]:4655", true},
		{"IPv6 loopback written out", "[0:0:0:0:0:0:0:1]:4655", true},
		{"IPv4-mapped loopback", "[::ffff:127.0.0.1]:4655", true},
		{"another port", "localhost:8080", true},

		{"every IPv4 interface", "0.0.0.0:4655", false},
		{"every IPv6 interface", "[::]:4655", false},
		{"empty host", ":4655", false},
		{"LAN address", "192.168.1.20:4655", false},
		{"another private address", "10.0.0.5:4655", false},
		{"public address", "203.0.113.7:4655", false},
		{"just outside 127.0.0.0/8", "128.0.0.1:4655", false},
		{"IPv6 link-local", "[fe80::1]:4655", false},
		{"IPv6 unique local", "[fd00::1]:4655", false},
		{"IPv4-mapped LAN address", "[::ffff:192.168.1.20]:4655", false},
		{"host name", "myhost.example.com:4655", false},
		{"single-label host name", "go:4655", false},
		{"name that only looks local", "localhost.example.com:4655", false},
		{"subdomain of localhost", "foo.localhost:4655", false},
		{"localhost with a trailing dot", "localhost.:4655", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Config{Addr: tt.addr}.Validate()
			if tt.loopback {
				if err != nil {
					t.Errorf("Validate(%q) = %v, want it accepted", tt.addr, err)
				}
			} else {
				if !errors.Is(err, ErrNotLoopback) {
					t.Fatalf("Validate(%q) = %v, want ErrNotLoopback", tt.addr, err)
				}
				// The message has to tell the user both what is wrong and
				// how to proceed either way.
				for _, want := range []string{tt.addr, "no sign-in", "localhost", "--allow-unauthenticated-network"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not mention %q", err, want)
					}
				}
			}

			override := Config{Addr: tt.addr, AllowUnauthenticatedNetwork: true}
			if err := override.Validate(); err != nil {
				t.Errorf("Validate(%q) with the override = %v, want it accepted", tt.addr, err)
			}
		})
	}
}

// TestValidateBadAddress covers addresses that are wrong whatever the
// override says.
func TestValidateBadAddress(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string // must appear in the message
	}{
		{"empty", "", "empty"},
		{"no port", "localhost", "host:port"},
		{"no port, IPv4", "127.0.0.1", "host:port"},
		{"IPv6 without brackets", "::1:4655", "host:port"},
		{"empty port", "localhost:", "port"},
		{"port is a word", "localhost:http", "port"},
		{"port is not a number", "localhost:46x5", "port"},
		{"negative port", "localhost:-1", "port"},
		{"signed port", "localhost:+4655", "port"},
		{"port zero", "localhost:0", "port"},
		{"port zero, written long", "localhost:0000", "port"},
		// Not ambiguous, but not the one spelling the Host check compares
		// against; Load rewrites it before Validate sees it.
		{"port with a leading zero", "localhost:04655", "without leading zeros"},
		{"port with leading zeros, IPv4", "127.0.0.1:0080", "without leading zeros"},
		{"port too large", "localhost:65536", "port"},
		{"port far too large", "localhost:99999999999999999999", "port"},
		{"only a port number", "4655", "host:port"},
		{"a URL", "http://localhost:4655", "host:port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, override := range []bool{false, true} {
				err := Config{Addr: tt.addr, AllowUnauthenticatedNetwork: override}.Validate()
				if err == nil {
					t.Fatalf("Validate(%q), override %v: accepted", tt.addr, override)
				}
				if errors.Is(err, ErrNotLoopback) {
					t.Errorf("Validate(%q) = %v; a malformed address is not a loopback refusal", tt.addr, err)
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error %q does not mention %q", err, tt.want)
				}
			}
		})
	}
}

func TestValidateAcceptsPortRange(t *testing.T) {
	for _, addr := range []string{"localhost:1", "localhost:65535", "127.0.0.1:80"} {
		if err := (Config{Addr: addr}).Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want it accepted", addr, err)
		}
	}
}

// A port typed with leading zeros is the same port. Load gives it its one
// spelling, so what Validate, the Host check and the startup line see is the
// address the server really answers on.
func TestLoadedAddressWithLeadingZerosIsValid(t *testing.T) {
	tests := []struct{ typed, want string }{
		{"localhost:04655", "localhost:4655"},
		{"127.0.0.1:080", "127.0.0.1:80"},
		{"LOCALHOST:0004655", "localhost:4655"},
		{"[::1]:04655", "[::1]:4655"},
	}
	for _, tt := range tests {
		cfg, err := Load([]string{"--addr", tt.typed, "--db", "x.db"}, func(string) string { return "" })
		if err != nil {
			t.Fatalf("Load(--addr %s): %v", tt.typed, err)
		}
		if cfg.Addr != tt.want {
			t.Errorf("Load(--addr %s).Addr = %q, want %q", tt.typed, cfg.Addr, tt.want)
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate after Load(--addr %s) = %v, want it accepted", tt.typed, err)
		}
	}
	// Through the environment too.
	cfg, err := Load([]string{"--db", "x.db"}, func(name string) string {
		if name == EnvAddr {
			return "localhost:04655"
		}
		return ""
	})
	if err != nil || cfg.Addr != "localhost:4655" {
		t.Errorf("Load with %s=localhost:04655 = %q, %v; want localhost:4655", EnvAddr, cfg.Addr, err)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"Localhost", true},
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"::1", true},
		{"::ffff:127.0.0.1", true},
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"192.168.0.1", false},
		{"[::1]", false}, // brackets belong to host:port syntax, not to the host
		{"127.0.0.1.example.com", false},
		{"0x7f.0.0.1", false},
		{"2130706433", false}, // 127.0.0.1 as one number; not accepted in any form
	}
	for _, tt := range tests {
		if got := IsLoopbackHost(tt.host); got != tt.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestCleanHosts(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{name: "none", in: nil, want: nil},
		{name: "lowercased and trimmed", in: []string{" Go ", "LINKS.example.com:8080"}, want: []string{"go", "links.example.com:8080"}},
		{name: "repeats removed, order kept", in: []string{"b", "a", "B", "a"}, want: []string{"b", "a"}},
		{name: "blanks dropped", in: []string{"", "  ", "go"}, want: []string{"go"}},
		{name: "IPv6 with port", in: []string{"[2001:db8::1]:8080"}, want: []string{"[2001:db8::1]:8080"}},
		{name: "URL", in: []string{"https://go.example.com"}, wantErr: true},
		{name: "path", in: []string{"go/links"}, wantErr: true},
		{name: "space inside", in: []string{"go links"}, wantErr: true},
		{name: "user info", in: []string{"me@go"}, wantErr: true},
		{name: "wildcard", in: []string{"*"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cleanHosts(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("cleanHosts = %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("cleanHosts = %q, want %q", got, tt.want)
				}
			}
		})
	}
}
