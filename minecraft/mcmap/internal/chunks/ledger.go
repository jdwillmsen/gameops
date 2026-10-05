package chunks

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

var (
	// ErrNoCensus is acknowledging before there has been a count: there is
	// nothing yet to accept.
	ErrNoCensus = errors.New("no chunk count has been taken")
	// ErrStale is acknowledging a count that a newer one has replaced, which
	// may hold losses the operator has not seen.
	ErrStale = errors.New("a newer chunk count has replaced the one acknowledged")
)

// maxSample bounds how many lost chunks a report names. Enough to find the
// damage on the map; the counts carry the size of it.
const maxSample = 20

// Report is one count of the world compared with every chunk seen before it.
type Report struct {
	At      time.Time
	Present map[Dimension]int
	// Missing is the chunks seen before and absent from this count.
	Missing map[Dimension]int
	// Lost is every chunk found missing since the last acknowledgement,
	// including any that have since been generated again.
	Lost map[Dimension]int
	// Sample is the lowest-positioned lost chunks, at most maxSample.
	Sample []Pos
}

func total(m map[Dimension]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (r Report) TotalMissing() int { return total(r.Missing) }
func (r Report) TotalLost() int    { return total(r.Lost) }

// Ledger is every chunk the world has been seen to hold, and every one it
// has lost since an operator last accepted the world as it was.
//
// A loss is remembered rather than recomputed because a lost chunk does not
// stay missing: Bedrock generates it again from the seed as soon as a player
// comes near, with everything built on it gone. Only an acknowledgement
// clears it. The whole ledger is on disk, so neither a restart nor a pod
// moving can take the damage as the new normal.
type Ledger struct {
	path string
	// flush puts a directory's entries on the disk. It is syncDir outside
	// tests.
	flush func(dir string) error

	mu      sync.Mutex
	seen    Set
	missing Set
	lost    Set
	at      time.Time
}

// The file is the magic, the time of the last count in Unix nanoseconds, and
// then three sets, seen, missing and lost, each a count followed by its
// chunks as three little-endian int32s: dimension, x, z.
var magic = []byte("MCMAPCK1")

// OpenLedger reads the ledger at path. A missing file is an empty ledger; an
// unreadable one is an error.
func OpenLedger(path string) (*Ledger, error) {
	l := &Ledger{path: path, flush: syncDir, seen: Set{}, missing: Set{}, lost: Set{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	bad := func(why string) error { return fmt.Errorf("chunk ledger %s: %s", path, why) }
	if !bytes.HasPrefix(raw, magic) || len(raw) < len(magic)+8 {
		return nil, bad("not a ledger")
	}
	body := raw[len(magic):]
	if at := int64(binary.LittleEndian.Uint64(body)); at != 0 {
		l.at = time.Unix(0, at).UTC()
	}
	body = body[8:]
	for _, s := range []Set{l.seen, l.missing, l.lost} {
		if len(body) < 8 {
			return nil, bad("truncated")
		}
		n := binary.LittleEndian.Uint64(body)
		body = body[8:]
		if uint64(len(body)) < n*12 {
			return nil, bad("truncated")
		}
		for i := uint64(0); i < n; i++ {
			s[Pos{
				Dim: Dimension(int32(binary.LittleEndian.Uint32(body))),
				X:   int32(binary.LittleEndian.Uint32(body[4:])),
				Z:   int32(binary.LittleEndian.Uint32(body[8:])),
			}] = struct{}{}
			body = body[12:]
		}
	}
	if len(body) != 0 {
		return nil, bad("trailing bytes")
	}
	return l, nil
}

// Observe records a count taken at at. Nothing changes unless the record of
// it reaches the disk, so a failed write is retried in full by the next
// count rather than half remembered.
func (l *Ledger) Observe(current Set, at time.Time) (Report, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := maps.Clone(l.seen)
	maps.Copy(seen, current)
	missing := Set{}
	for p := range seen {
		if _, ok := current[p]; !ok {
			missing[p] = struct{}{}
		}
	}
	lost := maps.Clone(l.lost)
	maps.Copy(lost, missing)
	if err := l.commit(seen, missing, lost, at); err != nil {
		return Report{}, err
	}
	return l.report(), nil
}

// Acknowledge accepts the world as the count taken at checkedAt found it:
// what it had lost is forgotten, and the chunks still missing are no longer
// expected. For a restore that has been checked, or a deliberate rollback.
func (l *Ledger) Acknowledge(checkedAt time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at.IsZero() {
		return ErrNoCensus
	}
	if !checkedAt.Equal(l.at) {
		return ErrStale
	}
	seen := maps.Clone(l.seen)
	for p := range l.missing {
		delete(seen, p)
	}
	return l.commit(seen, Set{}, Set{}, l.at)
}

// Last is the most recent report, if there has been a count, including one
// recorded before this process started.
func (l *Ledger) Last() (Report, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at.IsZero() {
		return Report{}, false
	}
	return l.report(), true
}

func (l *Ledger) report() Report {
	present := counts(l.seen)
	missing := counts(l.missing)
	for d := range present {
		present[d] -= missing[d]
	}
	return Report{At: l.at, Present: present, Missing: missing, Lost: counts(l.lost), Sample: sample(l.lost)}
}

func (l *Ledger) commit(seen, missing, lost Set, at time.Time) error {
	if err := save(l.path, l.flush, seen, missing, lost, at); err != nil {
		return err
	}
	l.seen, l.missing, l.lost, l.at = seen, missing, lost, at
	return nil
}

func counts(s Set) map[Dimension]int {
	c := map[Dimension]int{}
	for _, d := range Dimensions {
		c[d] = 0
	}
	for p := range s {
		c[p.Dim]++
	}
	return c
}

func less(a, b Pos) int {
	return cmp.Or(cmp.Compare(a.Dim, b.Dim), cmp.Compare(a.X, b.X), cmp.Compare(a.Z, b.Z))
}

func sample(s Set) []Pos {
	if len(s) == 0 {
		return nil
	}
	all := slices.SortedFunc(maps.Keys(s), less)
	return all[:min(len(all), maxSample)]
}

// save writes the ledger whole or not at all: a torn file would be refused
// at the next start, which is safe but stops the service until someone
// deletes it.
func save(path string, flush func(dir string) error, seen, missing, lost Set, at time.Time) error {
	buf := append([]byte{}, magic...)
	buf = binary.LittleEndian.AppendUint64(buf, uint64(at.UnixNano()))
	for _, s := range []Set{seen, missing, lost} {
		buf = binary.LittleEndian.AppendUint64(buf, uint64(len(s)))
		for _, p := range slices.SortedFunc(maps.Keys(s), less) {
			buf = binary.LittleEndian.AppendUint32(buf, uint32(p.Dim))
			buf = binary.LittleEndian.AppendUint32(buf, uint32(p.X))
			buf = binary.LittleEndian.AppendUint32(buf, uint32(p.Z))
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ledger-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf); err != nil {
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
	// The rename is only an entry in the directory until the directory is
	// flushed. A node that stopped before then would come back with the
	// ledger before this one: an acknowledgement undone, or a chunk first
	// seen in this count and lost in the same stop never reported.
	return flush(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
