//go:build !unix

package trainer

import (
	"errors"
	"os"
)

var errWouldBlock = errors.New("lock held")

// Training is lab tooling that runs on the Linux devbox; refusing here is
// safer than training without the lock.
func flockExclusive(*os.File) error {
	return errors.New("trainer: the training lock needs a unix platform")
}
