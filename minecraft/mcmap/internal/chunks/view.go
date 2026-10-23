package chunks

import (
	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/pkg/worldview"
)

// OpenView opens the LevelDB at dbDir read-only, for a reader other than
// the chunk count, and returns what closes it again. The open itself is
// shared with the other world readers; see worldview.Open for what it does
// to the directory it reads.
func OpenView(dbDir, workDir string) (*leveldb.DB, func(), error) {
	return worldview.Open(dbDir, workDir)
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
