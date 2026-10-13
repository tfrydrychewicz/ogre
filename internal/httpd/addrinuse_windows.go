//go:build windows

package httpd

import (
	"errors"
	"syscall"
)

// wsaeaddrinuse is what Windows sockets report for a taken address. Package
// syscall also has an EADDRINUSE on Windows, but that is an invented number
// no system call returns, and Errno.Is does not map one to the other.
const wsaeaddrinuse = syscall.Errno(10048)

// isAddrInUse reports whether a listen failed because the address is taken.
func isAddrInUse(err error) bool {
	return errors.Is(err, wsaeaddrinuse) || errors.Is(err, syscall.EADDRINUSE)
}
