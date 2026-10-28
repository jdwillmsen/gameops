//go:build unix

package trainer

import (
	"errors"
	"os"
	"syscall"
)

var errWouldBlock = errors.New("lock held")

// noFollow stops the lock file from being a planted symlink to a file the
// trainer would then truncate.
const noFollow = syscall.O_NOFOLLOW

func flockExclusive(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return errWouldBlock
		}
		return err
	}
}
