// Package worldview opens a Bedrock world's LevelDB for a reader. The open
// runs on links under a work directory, because even a read-only open
// creates a LOCK file and the world being read must be left exactly as the
// server wrote it.
package worldview

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/opt"
)

// Open opens the LevelDB at dbDir read-only, for a reader that goes over
// its records, and returns what closes it again.
//
// workDir belongs to one reader: a view left there by a run that was killed
// is removed on the next open, which would pull the files out from under a
// second reader sharing the directory.
func Open(dbDir, workDir string) (*leveldb.DB, func(), error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, nil, err
	}
	if stale, err := filepath.Glob(filepath.Join(workDir, "view-*")); err == nil {
		for _, dir := range stale {
			os.RemoveAll(dir)
		}
	}
	view, err := os.MkdirTemp(workDir, "view-")
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		os.RemoveAll(view)
		return nil, nil, err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if err := linkOrCopy(filepath.Join(dbDir, e.Name()), filepath.Join(view, e.Name())); err != nil {
			os.RemoveAll(view)
			return nil, nil, err
		}
	}
	db, err := leveldb.OpenFile(view, &opt.Options{ReadOnly: true, ErrorIfMissing: true})
	if err != nil {
		os.RemoveAll(view)
		return nil, nil, fmt.Errorf("open world: %w", err)
	}
	return db, func() {
		db.Close()
		os.RemoveAll(view)
	}, nil
}

// linkOrCopy falls back to copying where a link cannot be made, such as
// across filesystems.
func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return err
}
