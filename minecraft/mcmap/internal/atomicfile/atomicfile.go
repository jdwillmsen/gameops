// Package atomicfile writes a file so that a crash or power loss leaves the
// old one or the new one, never part of one.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write leaves either the old file or the new one at path: a crash
// mid-write must not leave something that stops the service starting
// until a person deletes it. The file is 0600, which is what a key needs.
// It returns nil once the rename has succeeded; the final directory sync is
// best effort, so an error always means the old file is still in place.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	// The rename is the commit point: the new file is already in place, so
	// a failure here must not be reported, or callers roll back state that
	// is in fact on disk. The sync only narrows the power-loss window.
	_ = syncDir(dir)
	return nil
}

// syncDir is a variable so a test can make the best-effort sync fail.
var syncDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
