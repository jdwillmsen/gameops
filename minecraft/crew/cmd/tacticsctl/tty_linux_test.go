//go:build linux

package main

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	var unlock, n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Skipf("ptsname: %v", e)
	}
	slave, err = os.OpenFile("/dev/pts/"+itoa(n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open pty slave: %v", err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestIsTerminalIsARealTerminalCheck(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if fi, _ := null.Stat(); fi.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("test premise: /dev/null should be a character device")
	}
	if isTerminal(null) {
		t.Error("/dev/null passed for a terminal")
	}
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("a pipe passed for a terminal")
	}
	file, _ := os.Open(os.Args[0])
	defer file.Close()
	if isTerminal(file) {
		t.Error("a regular file passed for a terminal")
	}
	_, slave := openPty(t)
	if !isTerminal(slave) {
		t.Error("a pty slave was not seen as a terminal")
	}
}
