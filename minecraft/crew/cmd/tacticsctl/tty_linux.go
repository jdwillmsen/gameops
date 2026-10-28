//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal asks the kernel whether f is a terminal. The mode bits are not
// enough: /dev/null is a character device too, and `approve < /dev/null`
// must not pass for a human.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
