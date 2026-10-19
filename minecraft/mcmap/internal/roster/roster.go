// Package roster remembers every player the live feed has shown: the id the
// game gives them, the gamertag they were last seen under and when. The
// world save holds an offline player's details under that id but not their
// name, so without this nothing could say whose details they are.
package roster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/atomicfile"
)

const (
	// MaxPlayers bounds the file and the heap. Past it the player seen
	// longest ago makes room, so a full roster still takes newcomers.
	MaxPlayers = 1024
	// maxEarlier is how many earlier gamertags are kept for each player.
	maxEarlier = 5
	// maxGamertag is longer than any gamertag.
	maxGamertag = 64
	// maxFile bounds what is read back: a full roster is well under it.
	maxFile = 4 << 20

	fileVersion = 1
	saveEvery   = 10 * time.Second
	// lastSeenStep is how far a player's last sighting has to move before
	// it is worth a write: the pack samples every second, and a last-seen
	// is never shown finer than a minute.
	lastSeenStep = time.Minute
)

// The live feed's id for a player: a signed 64-bit number written out.
var playerID = regexp.MustCompile(`^-?[0-9]{1,20}$`)

// Entry is one player. XUID is set once they have logged in to the map,
// which is the only place an XUID and the game's id meet a name.
type Entry struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Earlier []string  `json:"earlier,omitempty"`
	XUID    string    `json:"xuid,omitempty"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
}

type file struct {
	Version int      `json:"version"`
	Players []*Entry `json:"players"`
}

// Roster is the players, kept in memory and written whole and atomically to
// Path. Empty Path keeps it in memory only.
type Roster struct {
	Path   string
	Logger *slog.Logger

	mu      sync.Mutex
	players map[string]*Entry
	// xuids is the XUID of each gamertag, folded, that has logged in. It is
	// kept so that a login reported before the player's first sample still
	// links them once the sample arrives. It is not written: the next login
	// sets it again.
	xuids map[string]string
	dirty bool
}

func (r *Roster) log() *slog.Logger {
	if r.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Logger
}

func (r *Roster) init() {
	if r.players == nil {
		r.players, r.xuids = map[string]*Entry{}, map[string]string{}
	}
}

// Load reads the saved roster. A missing file is an empty one. A file that
// cannot be read is set aside for a person to look at, and the roster
// starts again rather than keeping the map from starting.
func (r *Roster) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.players, r.xuids, r.dirty = nil, nil, false
	r.init()
	if r.Path == "" {
		return nil
	}
	raw, err := readLimited(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil && !errors.Is(err, errTooBig) {
		return err
	}
	var f file
	if err == nil {
		err = json.Unmarshal(raw, &f)
	}
	if err == nil && f.Version != fileVersion {
		err = fmt.Errorf("version %d is not %d", f.Version, fileVersion)
	}
	if err != nil {
		aside := r.Path + ".unreadable"
		r.log().Error("player roster could not be read and was set aside; it starts again", "error", err.Error(), "kept_as", aside)
		return os.Rename(r.Path, aside)
	}
	for _, e := range f.Players {
		if e == nil || !playerID.MatchString(e.ID) || !validName(e.Name) {
			continue
		}
		r.players[e.ID] = e
	}
	r.trim()
	return nil
}

var errTooBig = errors.New("roster file is over the size limit")

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err == nil && len(raw) > maxFile {
		err = errTooBig
	}
	return raw, err
}

func validName(name string) bool {
	return name != "" && len(name) <= maxGamertag && !strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// Seen notes that the live feed showed the player with this id under this
// name at at. It is called for every player of every sample, so it only
// marks the roster for writing when something shown would change.
func (r *Roster) Seen(id, name string, at time.Time) {
	if !playerID.MatchString(id) || !validName(name) {
		return
	}
	at = at.UTC().Truncate(time.Second)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	e := r.players[id]
	if e == nil {
		e = &Entry{ID: id, Name: name, First: at, Last: at}
		r.players[id] = e
		r.dirty = true
		r.trim()
	}
	if e.Name != name {
		e.Earlier = slices.DeleteFunc(e.Earlier, func(n string) bool { return strings.EqualFold(n, name) })
		e.Earlier = append([]string{e.Name}, e.Earlier...)
		if len(e.Earlier) > maxEarlier {
			e.Earlier = e.Earlier[:maxEarlier]
		}
		// The old gamertag is free for another player to take, who must
		// not be given this one's XUID by it.
		if old := strings.ToLower(e.Name); e.XUID != "" && r.xuids[old] == e.XUID {
			delete(r.xuids, old)
		}
		e.Name = name
		// Nor is the XUID carried to the new name unchecked: it may be
		// another player's old one.
		e.XUID = ""
		r.dirty = true
	}
	if x, ok := r.xuids[strings.ToLower(name)]; ok && e.XUID != x {
		e.XUID = x
		r.dirty = true
	}
	if at.Sub(e.Last) >= lastSeenStep {
		r.dirty = true
	}
	if at.After(e.Last) {
		e.Last = at
	}
}

// Link notes the XUID of the player who logged in to the map under this
// gamertag, which the game or the Xbox account vouched for.
func (r *Roster) Link(gamertag, xuid string) {
	if !validName(gamertag) || xuid == "" || len(xuid) > 20 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	folded := strings.ToLower(gamertag)
	if len(r.xuids) >= MaxPlayers {
		clear(r.xuids)
	}
	r.xuids[folded] = xuid
	for _, e := range r.players {
		switch {
		case strings.EqualFold(e.Name, gamertag) && e.XUID != xuid:
			e.XUID = xuid
			r.dirty = true
		case e.XUID == xuid && !strings.EqualFold(e.Name, gamertag):
			// The same account under a name the feed has not shown yet:
			// the old entry's link would otherwise outlive the name.
			e.XUID = ""
			r.dirty = true
		}
	}
}

// trim drops the players seen longest ago until the roster is in bounds.
func (r *Roster) trim() {
	for len(r.players) > MaxPlayers {
		var oldest *Entry
		for _, e := range r.players {
			if oldest == nil || e.Last.Before(oldest.Last) || (e.Last.Equal(oldest.Last) && e.ID < oldest.ID) {
				oldest = e
			}
		}
		delete(r.players, oldest.ID)
		r.dirty = true
	}
}

// List is every player, most recently seen first.
func (r *Roster) List() []Entry {
	r.mu.Lock()
	out := make([]Entry, 0, len(r.players))
	for _, e := range r.players {
		c := *e
		c.Earlier = slices.Clone(e.Earlier)
		out = append(out, c)
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b Entry) int {
		if c := b.Last.Compare(a.Last); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// Save writes the roster if it has changed since the last write.
func (r *Roster) Save() error {
	if r.Path == "" {
		return nil
	}
	r.mu.Lock()
	if !r.dirty {
		r.mu.Unlock()
		return nil
	}
	f := file{Version: fileVersion, Players: make([]*Entry, 0, len(r.players))}
	for _, e := range r.players {
		c := *e
		c.Earlier = slices.Clone(e.Earlier)
		f.Players = append(f.Players, &c)
	}
	r.dirty = false
	r.mu.Unlock()
	slices.SortFunc(f.Players, func(a, b *Entry) int { return strings.Compare(a.ID, b.ID) })

	raw, err := json.Marshal(f)
	if err == nil {
		err = atomicfile.Write(r.Path, raw)
	}
	if err != nil {
		r.mu.Lock()
		r.dirty = true
		r.mu.Unlock()
	}
	return err
}

// Run writes the roster every few seconds while it changes, and once more
// when ctx ends.
func (r *Roster) Run(ctx context.Context) {
	tick := time.NewTicker(saveEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := r.Save(); err != nil {
				r.log().Error("player roster was not saved on the way out", "error", err.Error())
			}
			return
		case <-tick.C:
			if err := r.Save(); err != nil {
				r.log().Error("player roster was not saved", "error", err.Error())
			}
		}
	}
}
