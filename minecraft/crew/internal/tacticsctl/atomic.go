package tacticsctl

import (
	"os"
	"path/filepath"
)

// renameFile is a variable so tests can fail the final step of a write and
// prove the destination is left untouched.
var renameFile = os.Rename

// writeAtomic replaces path with data in one step: readers see the old file or
// the new one, never a prefix. The temp file lives in the same directory so
// the rename cannot cross filesystems, and a crash leaves at worst a ".tmp-"
// file that nothing reads.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		return cleanup(err)
	}
	if err := f.Chmod(perm); err != nil {
		return cleanup(err)
	}
	if err := f.Sync(); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := renameFile(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
