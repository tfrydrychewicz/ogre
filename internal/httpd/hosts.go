package httpd

import (
	"net"
	"net/netip"
	"strings"
)

// hostList is the Host allow-list of plan section 3.8.
//
// A local Directory has no sign-in, and a hostile web page can make the
// browser send requests to any name that resolves to 127.0.0.1 (DNS
// rebinding). The browser still puts the hostile name in the Host header, so
// serving only the names we know stops that page from reading or writing
// links.
type hostList struct {
	allowed map[hostPort]bool
}

type hostPort struct{ host, port string }

// loopbackHosts are always accepted on the configured port. This check is
// about browsers: it stops a page of a hostile domain that resolves to this
// computer. It is not access control against other machines, which can send
// any Host they like; keeping those out is the job of listening on loopback
// only.
var loopbackHosts = []string{"localhost", "127.0.0.1", "::1"}

// newHostList builds the list from the listen address and the extra entries
// of --allow-host. An extra entry without a port is accepted on the listen
// port; with a port it is accepted on exactly that port, which is how a name
// served through a forwarder on port 80 is allowed ("go:80").
func newHostList(addr string, extra []string) hostList {
	listenHost, port := splitHostPort(strings.ToLower(addr))
	l := hostList{allowed: make(map[hostPort]bool)}
	for _, h := range loopbackHosts {
		l.allowed[hostPort{h, port}] = true
	}
	// A Directory told to listen on another loopback address, such as
	// 127.0.0.2, is reached under that address. It is as safe a name as
	// 127.0.0.1: a hostile page cannot make a browser send it as Host for
	// any other server. Other listen hosts are not added; they need
	// --allow-host, because only the operator knows which names are theirs.
	if ip, err := netip.ParseAddr(listenHost); err == nil && ip.Unmap().IsLoopback() {
		l.allowed[hostPort{ip.Unmap().String(), port}] = true
	}
	for _, e := range extra {
		h, p := splitHostPort(strings.ToLower(strings.TrimSpace(e)))
		if h == "" {
			continue
		}
		if p == "" {
			p = port
		}
		l.allowed[hostPort{h, p}] = true
	}
	return l
}

// allows reports whether a request's Host header names this Directory. The
// comparison ignores case. A Host without a port means the scheme's default
// port, as browsers omit it.
func (l hostList) allows(hostHeader string, tls bool) bool {
	host, port := splitHostPort(strings.ToLower(hostHeader))
	if host == "" {
		return false
	}
	if port == "" {
		port = "80"
		if tls {
			port = "443"
		}
	}
	return l.allowed[hostPort{host, port}]
}

// splitHostPort splits "host", "host:port", "[v6]" and "[v6]:port". The host
// comes back without brackets; the port is empty when there is none.
func splitHostPort(s string) (host, port string) {
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p
	}
	return strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"), ""
}
