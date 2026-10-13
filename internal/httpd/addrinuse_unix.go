//go:build !windows

package httpd

import (
	"errors"
	"syscall"
)

// isAddrInUse reports whether a listen failed because the address is taken.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
