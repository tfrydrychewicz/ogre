package httpd

import (
	"net/http"
	"slices"
	"testing"
)

// The hosts a destination may not point at are the ones the Host guard
// serves, plus the listen address.
func TestOwnHosts(t *testing.T) {
	tests := []struct {
		name       string
		addr       string
		allowHosts []string
		want       []string
	}{
		{"default", "localhost:4655", nil,
			[]string{"127.0.0.1:4655", "[::1]:4655", "localhost:4655"}},
		{"allowed names, with and without a port", "localhost:4655", []string{"Go", "go:80", "fd00::1"},
			[]string{"127.0.0.1:4655", "[::1]:4655", "[fd00::1]:4655", "go:4655", "go:80", "localhost:4655"}},
		{"another loopback address", "127.0.0.2:8080", nil,
			[]string{"127.0.0.1:8080", "127.0.0.2:8080", "[::1]:8080", "localhost:8080"}},
		{"a LAN address", "192.168.1.10:4655", nil,
			[]string{"127.0.0.1:4655", "192.168.1.10:4655", "[::1]:4655", "localhost:4655"}},
		{"every interface names no host", "0.0.0.0:4655", nil,
			[]string{"127.0.0.1:4655", "[::1]:4655", "localhost:4655"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OwnHosts(tt.addr, tt.allowHosts)
			if !slices.Equal(got, tt.want) {
				t.Errorf("OwnHosts(%q, %q) = %q, want %q", tt.addr, tt.allowHosts, got, tt.want)
			}
			// Agreement with the guard: every name it serves is own.
			list := newHostList(tt.addr, tt.allowHosts)
			for _, h := range got {
				if !list.allows(h, false) && h != tt.addr {
					t.Errorf("%q is own but the Host guard refuses it", h)
				}
			}
		})
	}
}

func TestHostAllowList(t *testing.T) {
	tests := []struct {
		name       string
		allowHosts []string
		host       string
		want       bool
	}{
		// The built-in names, on the configured port.
		{"localhost", nil, "localhost:4655", true},
		{"localhost upper case", nil, "LOCALHOST:4655", true},
		{"IPv4 loopback", nil, "127.0.0.1:4655", true},
		{"IPv6 loopback", nil, "[::1]:4655", true},

		// DNS rebinding and friends.
		{"hostile name on our port", nil, "evil.example:4655", false},
		{"hostile name without port", nil, "evil.example", false},
		{"localhost on another port", nil, "localhost:9999", false},
		{"localhost without port", nil, "localhost", false},
		{"IPv4 loopback without port", nil, "127.0.0.1", false},
		{"IPv6 loopback without port", nil, "[::1]", false},
		{"localhost as a subdomain", nil, "localhost.evil.example:4655", false},
		{"our address as a prefix", nil, "localhost:4655.evil.example", false},
		{"trailing dot", nil, "localhost.:4655", false},
		{"userinfo", nil, "evil@localhost:4655", false},
		{"another loopback address", nil, "127.0.0.2:4655", false},
		{"unspecified address", nil, "0.0.0.0:4655", false},
		{"IPv6 loopback without brackets", nil, "::1:4655", false},
		{"empty port", nil, "localhost:", false},
		{"empty", nil, "-", false},

		// --allow-host without a port: the configured port only.
		{"allowed name", []string{"go"}, "go:4655", true},
		{"allowed name upper case", []string{"go"}, "GO:4655", true},
		{"entry upper case", []string{" Go "}, "go:4655", true},
		{"allowed name on another port", []string{"go"}, "go:8080", false},
		{"allowed name on the default port", []string{"go"}, "go", false},
		{"allowed name as a suffix", []string{"go"}, "notgo:4655", false},
		{"built-in names still work", []string{"go"}, "localhost:4655", true},
		{"empty entry allows nothing", []string{""}, "-", false},

		// --allow-host with a port: that port only.
		{"entry with port", []string{"directory.example:8080"}, "directory.example:8080", true},
		{"entry with port, upper case", []string{"Directory.Example:8080"}, "DIRECTORY.example:8080", true},
		{"entry with port, on our port", []string{"directory.example:8080"}, "directory.example:4655", false},
		{"entry on port 80, Host without port", []string{"go:80"}, "go", true},
		{"entry on port 80, Host with port", []string{"go:80"}, "go:80", true},
		{"entry on port 80, on our port", []string{"go:80"}, "go:4655", false},
		{"IPv6 entry with port", []string{"[fd00::1]:4655"}, "[fd00::1]:4655", true},
		{"IPv6 entry without port", []string{"fd00::1"}, "[fd00::1]:4655", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{}
			h := newTestServer(svc, func(o *Options) { o.AllowHosts = tt.allowHosts }).Handler()

			// The check comes before all routing: the health check, a go
			// link and a write are all refused alike.
			health := request{target: "/-/healthz", host: tt.host}.send(h)
			golink := request{target: "/roadmap", host: tt.host}.send(h)
			write := request{
				method: http.MethodPost, target: "/-/api/v1/links", host: tt.host,
				headers: map[string]string{"Content-Type": "application/json"}, body: validBody,
			}.send(h)

			if tt.want {
				if health.Code != http.StatusOK {
					t.Errorf("healthz status = %d, want 200", health.Code)
				}
				if golink.Code != http.StatusNotFound || len(svc.resolvedPaths()) != 1 {
					t.Errorf("go link status = %d, resolved %v; want 404 from the resolver", golink.Code, svc.resolvedPaths())
				}
				if write.Code != http.StatusCreated {
					t.Errorf("write status = %d, want 201", write.Code)
				}
				return
			}
			for what, rec := range map[string]int{"healthz": health.Code, "go link": golink.Code, "write": write.Code} {
				if rec != http.StatusForbidden {
					t.Errorf("%s status = %d, want 403", what, rec)
				}
			}
			if got := svc.resolvedPaths(); len(got) != 0 {
				t.Errorf("a refused host reached the resolver: %v", got)
			}
			if got := svc.createdDrafts(); len(got) != 0 {
				t.Errorf("a refused host created a link: %v", got)
			}
		})
	}
}

func TestHostDefaultPortFollowsScheme(t *testing.T) {
	tests := []struct {
		addr string
		host string
		tls  bool
		want bool
	}{
		{"localhost:80", "localhost", false, true},
		{"localhost:80", "localhost", true, false},
		{"localhost:443", "localhost", true, true},
		{"localhost:443", "localhost", false, false},
		{"localhost:80", "localhost:80", false, true},
		{"localhost:4655", "localhost", false, false},
		{"localhost:4655", "localhost", true, false},
	}
	for _, tt := range tests {
		if got := newHostList(tt.addr, nil).allows(tt.host, tt.tls); got != tt.want {
			t.Errorf("addr %q, Host %q, tls %v: allows = %v, want %v", tt.addr, tt.host, tt.tls, got, tt.want)
		}
	}
}

func TestSplitHostPort(t *testing.T) {
	tests := []struct{ in, host, port string }{
		{"localhost:4655", "localhost", "4655"},
		{"localhost", "localhost", ""},
		{"[::1]:4655", "::1", "4655"},
		{"[::1]", "::1", ""},
		{"::1", "::1", ""},
		{"", "", ""},
		{":4655", "", "4655"},
	}
	for _, tt := range tests {
		if host, port := splitHostPort(tt.in); host != tt.host || port != tt.port {
			t.Errorf("splitHostPort(%q) = %q, %q; want %q, %q", tt.in, host, port, tt.host, tt.port)
		}
	}
}

// A Directory listening on a loopback address of its own is reached under
// that address; any other listen host needs --allow-host.
func TestHostListFollowsALoopbackListenAddress(t *testing.T) {
	tests := []struct {
		addr string
		host string
		want bool
	}{
		{"127.0.0.2:4655", "127.0.0.2:4655", true},
		{"127.0.0.2:4655", "127.0.0.1:4655", true}, // the built-in names stay
		{"127.0.0.2:4655", "localhost:4655", true},
		{"127.0.0.2:4655", "127.0.0.3:4655", false},
		{"127.0.0.2:4655", "127.0.0.2:9999", false},
		{"127.0.0.2:4655", "127.0.0.2", false},
		{"127.1.2.3:8080", "127.1.2.3:8080", true},
		{"[::1]:4655", "[::1]:4655", true},
		{"[::ffff:127.0.0.2]:4655", "127.0.0.2:4655", true},
		{"localhost:4655", "127.0.0.2:4655", false},

		// Not loopback: nothing is added on the operator's behalf.
		{"0.0.0.0:4655", "0.0.0.0:4655", false},
		{"192.168.1.10:4655", "192.168.1.10:4655", false},
		{"[fd00::1]:4655", "[fd00::1]:4655", false},
		{"directory.example:4655", "directory.example:4655", false},
		{"0.0.0.0:4655", "localhost:4655", true},
	}
	for _, tt := range tests {
		if got := newHostList(tt.addr, nil).allows(tt.host, false); got != tt.want {
			t.Errorf("listening on %q, Host %q: allows = %v, want %v", tt.addr, tt.host, got, tt.want)
		}
	}
}
