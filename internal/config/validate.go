package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// ErrNotLoopback is returned by Validate when the listen address would expose
// a Directory without sign-in to other machines.
var ErrNotLoopback = errors.New("refusing to listen beyond this computer")

// Validate checks that the configuration is safe and usable to listen with.
//
// A Directory in local mode has no sign-in: whoever can reach it can re-point
// any go link. So unless AllowUnauthenticatedNetwork is set, the listen host
// must be one that only this computer can reach (DEP-3): "localhost", an
// address in 127.0.0.0/8, or ::1. Anything else is refused, including an
// empty host (":4655"), which means every network interface, and other host
// names, whose address cannot be known before listening.
//
// The port must be a fixed one, 1 to 65535. Port 0 ("any free port") is
// refused because the Host check and the extension both need to know the
// port in advance.
func (c Config) Validate() error {
	host, _, err := splitAddr(c.Addr)
	if err != nil {
		return err
	}
	if c.AllowUnauthenticatedNetwork || IsLoopbackHost(host) {
		return nil
	}
	what := fmt.Sprintf("%q is not a loopback address", host)
	if host == "" {
		what = "an address without a host listens on every network interface"
	}
	return fmt.Errorf("listen address %q: %w: %s, and ogre has no sign-in, so anyone who can reach it could change your links. "+
		"Use localhost, 127.0.0.1 or [::1], or pass --allow-unauthenticated-network to accept the risk",
		c.Addr, ErrNotLoopback, what)
}

// IsLoopbackHost reports whether host, as written in a listen address, can
// only be reached from this computer: "localhost" in any letter case, or an
// IP address in 127.0.0.0/8 or ::1.
//
// Other names are not looked up, not even ones that usually resolve to
// loopback such as "foo.localhost": the answer would depend on the resolver,
// and this is a safety check.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.Unmap().IsLoopback()
}

// splitAddr cuts a listen address into host and port and checks the port.
func splitAddr(addr string) (host string, port int, err error) {
	if addr == "" {
		return "", 0, errors.New("the listen address is empty; give host:port, such as " + DefaultAddr)
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		var addrErr *net.AddrError
		reason := err.Error()
		if errors.As(err, &addrErr) {
			reason = addrErr.Err
		}
		return "", 0, fmt.Errorf("listen address %q: %s; give host:port, such as %s or [::1]:4655", addr, reason, DefaultAddr)
	}
	port, convErr := strconv.Atoi(portText)
	if convErr != nil || port < 1 || port > 65535 || strings.TrimLeft(portText, "0123456789") != "" {
		return "", 0, fmt.Errorf("listen address %q: the port must be a number from 1 to 65535, not %q", addr, portText)
	}
	// Load writes the port in its one canonical form. A Config built some
	// other way with "04655" would listen on 4655 while the Host check
	// compared against "04655" and refused every request.
	if portText != strconv.Itoa(port) {
		return "", 0, fmt.Errorf("listen address %q: write the port as %d, without leading zeros", addr, port)
	}
	return host, port, nil
}
