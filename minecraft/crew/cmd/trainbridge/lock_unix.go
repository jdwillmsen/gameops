//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

var errWouldBlock = errors.New("lock held")

func tryFlock(f *os.File) error {
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

// noFollow keeps a lock file that was swapped for a symlink from steering
// the open elsewhere.
const noFollow = syscall.O_NOFOLLOW
