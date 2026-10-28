//go:build !unix

package main

import (
	"errors"
	"os"
)

var errWouldBlock = errors.New("lock held")

const noFollow = 0

// Training is lab tooling that runs on the Linux devbox; refusing here is
// safer than fighting without the squad lock.
func tryFlock(*os.File) error {
	return errors.New("trainbridge: the squad lock needs a unix platform")
}
