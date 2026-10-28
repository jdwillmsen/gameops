//go:build unix

package tacticsctl

import (
	"fmt"
	"os"
	"syscall"
)

// noFollow makes opening a symlink fail instead of reading its target.
const noFollow = syscall.O_NOFOLLOW

// FileOwner reports the uid that owns fi. ok is false when the platform does
// not say, and callers must then treat the file as not trusted.
func FileOwner(fi os.FileInfo) (uid uint32, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, false
	}
	return st.Uid, true
}

// OpenPrivate opens a secret file that only the calling user may own and read.
// The checks run on the open descriptor, after refusing a symlink, so the file
// that is read is the file that was checked.
func OpenPrivate(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if uid, ok := FileOwner(fi); !ok || int(uid) != os.Getuid() {
		f.Close()
		return nil, fmt.Errorf("%s is not owned by the current user", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		f.Close()
		return nil, fmt.Errorf("%s is readable by others; chmod 600 it", path)
	}
	return f, nil
}
