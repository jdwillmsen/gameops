// Package heads keeps the world map's player markers looking like the
// players. The game server sends every client each online player's skin;
// this crops a head from each and reports them to the map, which draws
// them where it would otherwise draw a dot.
package heads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
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

	// maxListed is how many of a skin's animations, or of the models its
	// patch names, one log line describes.
	maxListed = 8
)

// Why a skin gave no head of its own; logged with the skin's facts.
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
	// Geometry is the JSON of the models the skin brings with it. A skin
	// drawn on one of the game's own models brings none.
	Geometry []byte
	// Animations are the other images the skin brings. No head is taken
	// from one; they are described in the log beside the model.
	Animations []Animation
	// Tints are the colours a character-creator skin gives for its wearer.
	Tints skin.Tints
	// Made is the rest of what a character-creator skin says it is made
	// of, for the log.
	Made Made
}

// Made is what a skin says of how it was put together, without the
// identifiers of the parts.
type Made struct {
	// ID is the skin's name for itself.
	ID string
	// Pieces are the types of the parts it is assembled from, and Tinted
	// the types that come with colours.
	Pieces []uint32
	Tinted []string
	// Premium is a skin bought from the marketplace. Hashed is one that
	// carries the hash its appearance is filed under elsewhere.
	Premium, Hashed bool
}

// Animation is one of a skin's extra images, without its pixels.
type Animation struct {
	Type, Expression uint32
	Width, Height    uint32
	Frames           float32
	Bytes            int
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

// Which kind of head a player was reported with; logged as "head".
const (
	headReal      = "real"
	headGeometry  = "geometry"
	headGenerated = "generated"
)

// head is the PNG a player is drawn as: the head of their skin, or for a
// skin there is no safe way to take one from, an avatar made from who they
// are. Every outcome is logged with what the skin was, since that record
// is the only way to learn what real clients send.
func (w *Watch) head(xuid string, s Skin) []byte {
	fields := logging.Fields{"xuid": xuid, "width": s.Width, "height": s.Height, "bytes": len(s.Data), "persona": s.Persona}
	img, kind := w.face(xuid, s, fields)
	if img == nil {
		// Tints are only a character-creator skin's to give.
		tints := skin.Tints{}
		if s.Persona {
			tints = s.Tints
		}
		img, fields["tinted"] = skin.Avatar(xuid, tints)
		kind = headGenerated
	}
	fields["head"] = kind
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		fields["head"], fields["skipped"] = "", skipImage
		w.log.Info("player_head", fields)
		return nil
	}
	w.log.Info("player_head", fields)
	return buf.Bytes()
}

// face is the head of the skin itself and which way it was found, or nil
// with the reason left in fields under "skipped".
func (w *Watch) face(xuid string, s Skin, fields logging.Fields) (*image.NRGBA, string) {
	skip := func(why string) (*image.NRGBA, string) {
		fields["skipped"] = why
		return nil, ""
	}
	geometry, standard := modelOf(s.ResourcePatch)
	fields["geometry"] = skin.MaskName(geometry)
	if s.Persona || !standard {
		return w.modelled(xuid, s, geometry, fields, skip)
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
	return img, headReal
}

// modelled is the head of a skin that is not a classic one, found where
// the model the skin brought with it says the head is drawn from.
//
// What the skin brought is logged whether or not it gave a head: a layout
// is only read once it has been seen, and this is where it is seen.
func (w *Watch) modelled(xuid string, s Skin, name string, fields logging.Fields, skip func(string) (*image.NRGBA, string)) (*image.NRGBA, string) {
	if skin.NoGeometry(s.Geometry) {
		w.log.Info("player_skin_model", described(xuid, s, nil))
		if s.Persona {
			return skip(skipPersona)
		}
		return skip(skipGeometry)
	}
	g, err := skin.ParseGeometry(s.Geometry)
	w.log.Info("player_skin_model", described(xuid, s, g))
	reason := func(err error) string {
		var why skin.GeometryError
		if errors.As(err, &why) {
			return string(why)
		}
		return skipImage
	}
	if err != nil {
		return skip(reason(err))
	}
	// An unreadable patch names nothing, and a model with no name in the
	// file is not the one it meant.
	if name == "" {
		return skip(skipGeometry)
	}
	if s.Width > 1024 || s.Height > 1024 {
		return skip(skipImage)
	}
	at, err := g.Head(name, int(s.Width), int(s.Height))
	if err != nil {
		return skip(reason(err))
	}
	img, err := skin.HeadAt(int(s.Width), int(s.Height), s.Data, at)
	if err != nil {
		return skip(reason(err))
	}
	fields["face"], fields["hat"] = at.Face.String(), at.Hat.String()
	return img, headGeometry
}

// described is the shape of what a skin brought, as it is logged: which
// models its patch names, what the geometry says of each one's head, the
// size of each extra image, and what it says it is made of. Names are cut
// to their kind, every list is cut to a length, and no pixel is included.
func described(xuid string, s Skin, g *skin.Geometry) logging.Fields {
	fields := logging.Fields{"xuid": xuid, "persona": s.Persona, "width": s.Width, "height": s.Height}
	if g != nil {
		fields["model"] = g.Facts()
	}
	if slots := slotsOf(s.ResourcePatch); len(slots) > 0 {
		fields["slots"] = slots
	}
	type animation struct {
		Type       uint32  `json:"type"`
		Expression uint32  `json:"expression"`
		Width      uint32  `json:"width"`
		Height     uint32  `json:"height"`
		Frames     float64 `json:"frames"`
		Bytes      int     `json:"bytes"`
	}
	listed := make([]animation, 0, min(len(s.Animations), maxListed))
	for _, a := range s.Animations[:cap(listed)] {
		frames := float64(a.Frames)
		// The count is a float on the wire, and one that is not a number
		// cannot be written to the log.
		if math.IsNaN(frames) || math.IsInf(frames, 0) {
			frames = -1
		}
		listed = append(listed, animation{a.Type, a.Expression, a.Width, a.Height, frames, a.Bytes})
	}
	fields["animations"], fields["animation_count"] = listed, len(s.Animations)

	tinted := make([]string, 0, min(len(s.Made.Tinted), maxListed))
	for _, t := range s.Made.Tinted[:cap(tinted)] {
		tinted = append(tinted, skin.MaskName(t))
	}
	fields["skin_id"], fields["premium"], fields["hashed"] = skin.MaskName(s.Made.ID), s.Made.Premium, s.Made.Hashed
	fields["pieces"], fields["piece_count"] = s.Made.Pieces[:min(len(s.Made.Pieces), 4*maxListed)], len(s.Made.Pieces)
	fields["tinted_pieces"], fields["skin_colour"], fields["hair_colour"] = tinted, hex(s.Tints.Skin), hex(s.Tints.Hair)
	return fields
}

// hex writes a colour as the four channels it arrived in, alpha last.
func hex(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

// slotsOf lists what a patch names a model for, with the kind of model
// each is: a character-creator skin names one for the body and others for
// the parts it animates.
func slotsOf(patch []byte) map[string]string {
	if len(patch) > maxPatch {
		return nil
	}
	var parsed struct {
		Geometry map[string]json.RawMessage `json:"geometry"`
	}
	if err := json.Unmarshal(patch, &parsed); err != nil {
		return nil
	}
	keys := make([]string, 0, len(parsed.Geometry))
	for k := range parsed.Geometry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	slots := map[string]string{}
	for _, k := range keys[:min(len(keys), maxListed)] {
		var name string
		if json.Unmarshal(parsed.Geometry[k], &name) != nil {
			name = "?"
		}
		slots[skin.MaskName(k)] = skin.MaskName(name)
	}
	return slots
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
