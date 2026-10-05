// Package heads keeps the world map's player markers looking like the
// players. The game server sends every client each online player's skin;
// this crops a head from each and reports them to the map, which draws
// them where it would otherwise draw a dot.
package heads

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
)

const (
	// DefaultInterval is how often the whole list is sent again with
	// nothing changed. The map keeps heads in memory and forgets a report
	// after five minutes, so this is what brings them back after the map
	// restarts and keeps them while nobody joins or leaves.
	DefaultInterval = time.Minute

	// defaultSettle is how long a change waits for others before it is reported.
	// A connection opens with a burst of player-list packets, and one
	// report of the result is better than one per packet.
	defaultSettle = 500 * time.Millisecond

	// maxPatch bounds the JSON naming a skin's geometry, which is a line.
	maxPatch = 4 << 10
)

// Why a skin gave no head; logged with the skin's facts.
const (
	skipPersona  = "persona"
	skipGeometry = "geometry"
	skipImage    = "image"
)

// Skin is the part of a player's skin a head is taken from.
type Skin struct {
	Width, Height uint32
	// Data is RGBA, row-major.
	Data []byte
	// Persona marks a skin built in the character creator. Its image is
	// laid out for a model of its own, sent with it, and not as a classic
	// skin is; the face is not where this package would look for it.
	Persona bool
	// ResourcePatch is the JSON naming the model the skin is drawn on.
	ResourcePatch []byte
}

// Entry is one line of a player-list packet.
type Entry struct {
	UUID   string
	XUID   string
	Name   string
	Remove bool
	Skin   Skin
}

// Reporter is the map, as far as this package needs it.
type Reporter interface {
	ReportHeads(ctx context.Context, players []mapclient.PlayerHead) error
}

type player struct {
	xuid, name string
	head       []byte
}

// Watch holds the head of everyone in the current session's player list
// and reports them when they change.
//
// Only the process connected to the game server sees skins, and only it
// may report: a standby holds an empty list, and sending that would wipe
// the heads the live process just sent. So nothing is reported outside a
// session, except the one empty report that ends it.
type Watch struct {
	reporter Reporter
	log      *logging.Logger
	interval time.Duration
	settle   time.Duration

	mu      sync.Mutex
	active  bool
	closing bool
	players map[string]player
	changed chan struct{}
}

// New builds a Watch that reports to the map. interval is how often an
// unchanged list is sent again; zero uses DefaultInterval.
func New(reporter Reporter, log *logging.Logger, interval time.Duration) *Watch {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Watch{reporter: reporter, log: log, interval: interval, settle: defaultSettle, players: map[string]player{}, changed: make(chan struct{}, 1)}
}

func (w *Watch) touch() {
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

// Begin starts a session with nobody in it; the server's opening player
// list fills it. A nil Watch, which is an agent with no map, does nothing
// here or in any other method.
func (w *Watch) Begin() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.active, w.closing, w.players = true, false, map[string]player{}
	w.mu.Unlock()
}

// End closes the session. Nobody is being watched any more, and the map is
// told so once.
func (w *Watch) End() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.closing, w.active, w.players = w.active, false, map[string]player{}
	w.mu.Unlock()
	w.touch()
}

var xuidShape = regexp.MustCompile(`^[0-9]{1,20}$`)

// List applies one player-list packet.
func (w *Watch) List(entries []Entry) {
	if w == nil || len(entries) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		return
	}
	for _, e := range entries {
		if e.Remove {
			delete(w.players, e.UUID)
			continue
		}
		// The map knows players by XUID. An entry without one is not a
		// signed-in player and has no marker to dress.
		if !xuidShape.MatchString(e.XUID) || e.Name == "" {
			continue
		}
		w.players[e.UUID] = player{xuid: e.XUID, name: e.Name, head: w.head(e.XUID, e.Skin)}
	}
	w.touch()
}

// Reskin applies a skin a player changed to while online. The packet names
// them by the UUID their list entry carried.
func (w *Watch) Reskin(uuid string, s Skin) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.players[uuid]
	if !ok || !w.active {
		return
	}
	p.head = w.head(p.xuid, s)
	w.players[uuid] = p
	w.touch()
}

// head is the PNG of a skin's head, or nil for a skin there is no safe way
// to take one from. Every outcome is logged with what the skin was, since
// that record is the only way to learn what real clients send.
func (w *Watch) head(xuid string, s Skin) []byte {
	fields := logging.Fields{"xuid": xuid, "width": s.Width, "height": s.Height, "bytes": len(s.Data), "persona": s.Persona}
	skip := func(why string) []byte {
		fields["skipped"] = why
		w.log.Info("player_head", fields)
		return nil
	}
	if s.Persona {
		return skip(skipPersona)
	}
	geometry, known := modelOf(s.ResourcePatch)
	fields["geometry"] = geometry
	if !known {
		return skip(skipGeometry)
	}
	// The dimensions are checked against the sizes a skin can be before
	// they are converted, so a claim of billions of pixels is never an int.
	if s.Width > 1024 || s.Height > 1024 {
		return skip(skipImage)
	}
	img, err := skin.Head(int(s.Width), int(s.Height), s.Data)
	if err != nil {
		return skip(skipImage)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return skip(skipImage)
	}
	w.log.Info("player_head", fields)
	return buf.Bytes()
}

// modelOf names the model a skin is drawn on and reports whether it is the
// standard player, on which the face is where a classic skin puts it. A
// skin that names no model is drawn on the standard one. Any other model
// may put the head anywhere on the image, so its skin gives no head.
func modelOf(patch []byte) (name string, standard bool) {
	if len(bytes.TrimSpace(patch)) == 0 {
		return "", true
	}
	if len(patch) > maxPatch {
		return "", false
	}
	var parsed struct {
		Geometry struct {
			Default string `json:"default"`
		} `json:"geometry"`
	}
	if err := json.Unmarshal(patch, &parsed); err != nil {
		return "", false
	}
	name = parsed.Geometry.Default
	if len(name) > 128 {
		return "", false
	}
	return name, name == "geometry.humanoid" || strings.HasPrefix(name, "geometry.humanoid.custom")
}

func (w *Watch) snapshot() (players []mapclient.PlayerHead, send bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active && !w.closing {
		return nil, false
	}
	w.closing = false
	players = make([]mapclient.PlayerHead, 0, len(w.players))
	// One line per XUID: the map refuses a report that names a player
	// twice, and that would cost everyone their head.
	seen := map[string]bool{}
	for _, p := range w.players {
		if seen[p.xuid] {
			continue
		}
		seen[p.xuid] = true
		players = append(players, mapclient.PlayerHead{XUID: p.xuid, Gamertag: p.name, Head: p.head})
	}
	sort.Slice(players, func(i, j int) bool { return players[i].XUID < players[j].XUID })
	return players, true
}

// Run reports the list whenever it changes and every interval besides,
// until ctx ends. A report that fails is not retried by itself: the next
// change or the next interval sends the whole list again.
func (w *Watch) Run(ctx context.Context) {
	if w == nil {
		return
	}
	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-w.changed:
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.settle):
			}
			// Whatever arrived while settling is in the list already.
			select {
			case <-w.changed:
			default:
			}
		}
		players, send := w.snapshot()
		if !send {
			continue
		}
		if err := w.reporter.ReportHeads(ctx, players); err != nil && ctx.Err() == nil {
			w.log.Warn("player_heads_not_reported", logging.Fields{"players": len(players), "error": err.Error()})
		}
	}
}
