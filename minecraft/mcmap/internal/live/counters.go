package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// The bounds of what is kept, so that nothing a world or a pack does can
	// make the file or the heap grow without end. A map that is full folds
	// further names into "other" and a full table of players counts no new
	// one.
	maxPlayers    = 512
	maxMobTypes   = 256
	maxCauses     = 64
	maxBlockTypes = 256
	otherType     = "other"

	// maxSources is how many runs of the pack are remembered, each with the
	// last line taken from it. The bridge holds minutes of lines, so a
	// handful of restarts is the most that can be in it at once.
	maxSources = 8

	countersVersion = 1
	saveEvery       = 10 * time.Second

	// maxCountersFile bounds what is read back: the table of players at its
	// bounds is several megabytes at the very most.
	maxCountersFile = 64 << 20
)

// Totals is everything counted of one player since Since. Since is when
// this map first took a count for them, not when they first played: the
// game kept nothing before that.
type Totals struct {
	Since         time.Time        `json:"since"`
	Deaths        map[string]int64 `json:"deaths,omitempty"`
	Kills         map[string]int64 `json:"kills,omitempty"`
	Broken        int64            `json:"broken"`
	BrokenTypes   map[string]int64 `json:"brokenTypes,omitempty"`
	Placed        int64            `json:"placed"`
	PlacedTypes   map[string]int64 `json:"placedTypes,omitempty"`
	Joins         int64            `json:"joins"`
	Leaves        int64            `json:"leaves"`
	OnlineSeconds int64            `json:"onlineSeconds"`
}

func (t Totals) clone() Totals {
	t.Deaths, t.Kills = maps.Clone(t.Deaths), maps.Clone(t.Kills)
	t.BrokenTypes, t.PlacedTypes = maps.Clone(t.BrokenTypes), maps.Clone(t.PlacedTypes)
	return t
}

// source is one run of the pack and the last line taken from it.
type source struct {
	Boot int64 `json:"boot"`
	Seq  int64 `json:"seq"`
}

type countersFile struct {
	Version int                `json:"version"`
	Sources []source           `json:"sources"`
	Players map[string]*Totals `json:"players"`
}

// Counters keeps running totals of what each player has done, from the
// deltas the pack prints. Only totals are kept, never the events, and they
// are written to disk whole and atomically.
//
// A delta is taken once. The bridge holds the latest lines and the map asks
// for the ones after the last it saw, so a line can come twice: after a
// failed poll, after the map restarts, or when the bridge restarts and
// answers with everything it holds. Each line carries the run of the pack
// that printed it and its number within that run, and the last number taken
// from each run is saved in the same file, in the same rename, as the totals
// it produced. A line is therefore either in the totals and refused when it
// comes again, or in neither and taken when it does.
type Counters struct {
	// Path is the file the totals are kept in. Empty keeps them in memory.
	Path   string
	Logger *slog.Logger

	mu      sync.Mutex
	sources []source
	players map[string]*Totals
	dirty   bool
}

func (c *Counters) log() *slog.Logger {
	if c.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return c.Logger
}

// Load reads the saved totals. A missing file is an empty start. One that
// cannot be read is set aside, so that it is kept for a person to look at
// and is not overwritten, and the totals start again.
func (c *Counters) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.players, c.sources, c.dirty = map[string]*Totals{}, nil, false
	if c.Path == "" {
		return nil
	}
	f, err := os.Open(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var file countersFile
	if err := json.NewDecoder(&limited{f: f, left: maxCountersFile}).Decode(&file); err != nil || file.Version != countersVersion {
		if err == nil {
			err = fmt.Errorf("version %d is not %d", file.Version, countersVersion)
		}
		aside := c.Path + ".unreadable"
		c.log().Error("player counters could not be read and were set aside; counting starts again", "error", err.Error(), "kept_as", aside)
		f.Close()
		return os.Rename(c.Path, aside)
	}
	for id, t := range file.Players {
		if t != nil && id != "" && len(id) <= maxText && len(c.players) < maxPlayers {
			c.players[id] = t
		}
	}
	c.sources = file.Sources
	if len(c.sources) > maxSources {
		c.sources = c.sources[len(c.sources)-maxSources:]
	}
	return nil
}

// limited fails a read past its bound instead of buffering it.
type limited struct {
	f    *os.File
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, errors.New("counters file is over the size limit")
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.f.Read(p)
	l.left -= int64(n)
	return n, err
}

// Apply takes the deltas of one record, received at at, unless it has been
// taken before, and says what became of it.
func (c *Counters) Apply(rec Record, at time.Time) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.players == nil {
		c.players = map[string]*Totals{}
	}

	i := 0
	for i < len(c.sources) && c.sources[i].Boot != rec.Boot {
		i++
	}
	if i == len(c.sources) {
		if len(c.sources) == maxSources {
			c.sources = c.sources[1:]
		}
		c.sources = append(c.sources, source{Boot: rec.Boot})
		i = len(c.sources) - 1
	} else if rec.Seq <= c.sources[i].Seq {
		return resultDropped
	} else if missed := rec.Seq - c.sources[i].Seq - 1; missed > 0 {
		metricCountsMissed.Add(float64(missed))
		c.log().Warn("counts lines were missed: the bridge dropped them before they were read", "missed", missed)
	}
	c.sources[i].Seq = rec.Seq
	c.dirty = true

	for _, d := range rec.Deltas {
		t := c.players[d.ID]
		if t == nil {
			if len(c.players) >= maxPlayers {
				metricCountsRefused.Inc()
				continue
			}
			t = &Totals{Since: at.UTC().Truncate(time.Second)}
			c.players[d.ID] = t
		}
		t.Broken += int64(d.Broken)
		t.Placed += int64(d.Placed)
		t.Joins += int64(d.Joins)
		t.Leaves += int64(d.Leaves)
		t.OnlineSeconds += int64(d.OnlineSeconds)
		t.Deaths = fold(t.Deaths, d.Deaths, maxCauses)
		t.Kills = fold(t.Kills, d.Kills, maxMobTypes)
		t.BrokenTypes = fold(t.BrokenTypes, d.BrokenTypes, maxBlockTypes)
		t.PlacedTypes = fold(t.PlacedTypes, d.PlacedTypes, maxBlockTypes)
	}
	return resultCounted
}

// fold adds delta into into, which gets at most limit names and one more,
// "other", for everything past them.
func fold(into map[string]int64, delta map[string]int, limit int) map[string]int64 {
	if len(delta) == 0 {
		return into
	}
	if into == nil {
		into = map[string]int64{}
	}
	for name, n := range delta {
		if _, known := into[name]; !known && len(into) >= limit {
			name = otherType
		}
		into[name] += int64(n)
	}
	return into
}

// Lookup is what has been counted of the player with this id.
func (c *Counters) Lookup(id string) (Totals, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.players[id]
	if !ok {
		return Totals{}, false
	}
	return t.clone(), true
}

// Save writes the totals to Path: to a temporary file in the same
// directory, which is synced and then renamed over the old one, so that a
// crash leaves either the whole of the old file or the whole of the new.
func (c *Counters) Save() error {
	if c.Path == "" {
		return nil
	}
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return nil
	}
	file := countersFile{Version: countersVersion, Sources: slicesClone(c.sources), Players: make(map[string]*Totals, len(c.players))}
	for id, t := range c.players {
		clone := t.clone()
		file.Players[id] = &clone
	}
	c.dirty = false
	c.mu.Unlock()

	err := writeAtomic(c.Path, file)
	if err != nil {
		c.mu.Lock()
		c.dirty = true
		c.mu.Unlock()
	}
	return err
}

func slicesClone(s []source) []source { return append([]source(nil), s...) }

func writeAtomic(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := json.NewEncoder(tmp).Encode(v); err != nil {
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
	return os.Rename(tmp.Name(), path)
}

// Run writes the totals every few seconds while they are changing, and
// once more when ctx ends.
func (c *Counters) Run(ctx context.Context) {
	tick := time.NewTicker(saveEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := c.Save(); err != nil {
				c.log().Error("player counters were not saved on the way out", "error", err.Error())
			}
			return
		case <-tick.C:
			if err := c.Save(); err != nil {
				c.log().Error("player counters were not saved", "error", err.Error())
			}
		}
	}
}
