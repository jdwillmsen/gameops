//go:build !unix

package tacticsctl

import (
	"errors"
	"os"
)

const noFollow = 0

// FileOwner is unknown off unix, so ownership checks refuse everything: the
// approval gate is lab tooling for the Linux devbox and must fail closed.
func FileOwner(os.FileInfo) (uint32, bool) { return 0, false }

// OpenPrivate refuses off unix, where ownership cannot be checked.
func OpenPrivate(path string) (*os.File, error) {
	return nil, errors.New("tacticsctl: private key files need a unix platform")
}
