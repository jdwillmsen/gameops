package chunks

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/opt"
)

// OpenView opens the LevelDB at dbDir read-only, for a reader other than
// the chunk count, and returns what closes it again.
//
// As in Scan, the open runs on hard links under workDir, because even a
// read-only open creates a LOCK file and the mirror must hold only the
// server's files. workDir belongs to one reader: a view left there by a
// run that was killed is removed on the next open, which would pull the
// files out from under a second reader sharing the directory.
func OpenView(dbDir, workDir string) (*leveldb.DB, func(), error) {
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

// RecordOf reads a chunk record's key: which chunk it belongs to and the
// tag saying what kind of record it is. It refuses whatever Scan does not
// count as a chunk.
func RecordOf(k []byte) (Pos, byte, bool) {
	p, ok := chunkOf(k)
	if !ok {
		return Pos{}, 0, false
	}
	if len(k) >= 13 {
		return p, k[12], true
	}
	return p, k[8], true
}
