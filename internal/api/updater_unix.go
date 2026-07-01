//go:build linux || darwin

package api

import (
	"errors"
	"syscall"
)

// isEROFS returns true if err is (or wraps) syscall.EROFS (read-only file system).
func isEROFS(err error) bool {
	return errors.Is(err, syscall.EROFS)
}
