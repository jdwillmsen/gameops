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
	// Without this the rename can be lost on power failure, after callers
	// have already told a client the change was made.
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
