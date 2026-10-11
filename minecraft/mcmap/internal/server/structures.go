package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

// StructureSource is the last survey of the world's structures.
type StructureSource interface {
	Last() (structures.Survey, bool)
}

type spawnJSON struct {
	X int32 `json:"x"`
	Z int32 `json:"z"`
	// Y is left out for a world that has not resolved its spawn height.
	Y *int32 `json:"y,omitempty"`
}

// structuresJSON keeps what the world recorded apart from what the seed
// predicts, so the page can never draw one as the other.
type structuresJSON struct {
	// Surveyed is false until the first snapshot has been read.
	Surveyed bool       `json:"surveyed"`
	At       *time.Time `json:"at,omitempty"`
	// Recorded is never null, so the page need not tell an empty world
	// from a missing field.
	Recorded      []structures.Structure  `json:"recorded"`
	RecordedMore  int                     `json:"recordedMore"`
	Predicted     []structures.Prediction `json:"predicted"`
	PredictedMore int                     `json:"predictedMore"`
	// Prediction is how far the seed has been checked against the world:
	// structures.SeedVerified and its siblings. Only a verified seed
	// predicts anything. The seed itself is never sent.
	Prediction string `json:"prediction"`
	// Kinds is, for each kind this dimension has a rule for, how that
	// rule fared against the world's own structures of the kind. Only a
	// verified kind is among the predictions.
	Kinds map[structures.Kind]structures.KindCheck `json:"kinds"`
	// Catalog is every kind the map can show, in whichever dimension, with
	// where each can be and whether it is off until asked for. It is what
	// a page lists a dimension's kinds from, and is the same in every
	// answer.
	Catalog []structures.Info `json:"catalog"`
	// Spawn is the world spawn, with the overworld only.
	Spawn *spawnJSON `json:"spawn,omitempty"`
}

// structureCache is the answers about one survey, written out once each.
// The overworld's is a quarter of a megabyte and is asked for by every
// page every five minutes; between surveys it does not change.
type structureCache struct {
	at   time.Time
	held map[structureAsk]structureAnswer
}

type structureAsk struct {
	dimension chunks.Dimension
	every     bool
}

type structureAnswer struct {
	body []byte
	etag string
}

// holds reports whether the owner keeps a kind off the map.
func (s *Server) holds(kind structures.Kind) bool { return slices.Contains(s.Withheld, kind) }

// catalog is every kind the map can show to anybody.
func (s *Server) catalog() []structures.Info {
	out := make([]structures.Info, 0, len(structures.Catalog))
	for _, k := range structures.Catalog {
		if !s.holds(k.Kind) {
			out = append(out, k)
		}
	}
	return out
}

// handleStructures serves one dimension's structures from the last survey.
// The answer is the same for every session until the next survey, so the
// browser keeps its copy and asks only whether it is still current.
func (s *Server) handleStructures(w http.ResponseWriter, r *http.Request) {
	var dimension chunks.Dimension = -1
	for _, d := range chunks.Dimensions {
		if d.Name() == r.URL.Query().Get("dimension") {
			dimension = d
		}
	}
	if dimension < 0 {
		http.Error(w, "unknown dimension", http.StatusBadRequest)
		return
	}
	ask := structureAsk{dimension, r.URL.Query().Get("kinds") == "all"}
	survey, surveyed := s.Structures.Last()
	s.mu.Lock()
	if !surveyed || !s.listed.at.Equal(survey.At) || s.listed.held == nil {
		s.listed = structureCache{at: survey.At, held: map[structureAsk]structureAnswer{}}
	}
	answer, written := s.listed.held[ask]
	s.mu.Unlock()
	if !written {
		body, err := json.Marshal(s.structuresOf(survey, surveyed, ask))
		if err != nil {
			http.Error(w, "the structures could not be written out", http.StatusInternalServerError)
			return
		}
		sum := sha256.Sum256(body)
		answer = structureAnswer{body, `"` + hex.EncodeToString(sum[:12]) + `"`}
		// Nothing is kept of a world not surveyed yet: the next ask may
		// be after the first survey, which is another answer.
		if surveyed {
			s.mu.Lock()
			if s.listed.at.Equal(survey.At) {
				s.listed.held[ask] = answer
			}
			s.mu.Unlock()
		}
	}
	h := w.Header()
	// Private, because it is only for the logged-in browser that asked;
	// no-cache, so that every use of the copy is checked against the tag.
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", answer.etag)
	if r.Header.Get("If-None-Match") == answer.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	_, _ = w.Write(answer.body)
}

// structuresOf is the answer about one dimension of a survey.
func (s *Server) structuresOf(survey structures.Survey, surveyed bool, ask structureAsk) structuresJSON {
	dimension, every := ask.dimension, ask.every
	out := structuresJSON{
		Recorded:   []structures.Structure{},
		Predicted:  []structures.Prediction{},
		Prediction: structures.SeedUnknown,
		Kinds:      map[structures.Kind]structures.KindCheck{},
		Catalog:    s.catalog(),
	}
	if surveyed {
		layer := survey.Layers[dimension]
		out.Surveyed, out.At, out.Prediction = true, &survey.At, survey.Check.State
		// A page from before the catalog has no row to put a later kind
		// away with, and would draw every one of them, the ones that are
		// off until asked for among them. It is sent the kinds it knows,
		// and those are picked out before the bound is applied, so that a
		// later kind takes none of the room there is for them. A kind the
		// owner withholds is sent to nobody; the survey leaves it out
		// already, and it is left out again here, where a list becomes an
		// answer.
		sent := func(kind structures.Kind) bool { return !s.holds(kind) && (every || firstKinds[kind]) }
		recorded := slices.DeleteFunc(slices.Clone(layer.Recorded), func(st structures.Structure) bool { return !sent(st.Kind) })
		predicted := slices.DeleteFunc(slices.Clone(layer.Predicted), func(p structures.Prediction) bool { return !sent(p.Kind) })
		// The survey keeps to this bound already. It is applied again
		// here because this is where a list becomes a response.
		out.Recorded, out.RecordedMore = clamp(recorded, layer.RecordedMore)
		out.Predicted, out.PredictedMore = clamp(predicted, layer.PredictedMore)
		for _, p := range structures.Predictors {
			if check, ok := survey.Check.Kinds[structures.Rule{Kind: p.Kind(), Dimension: dimension}]; ok && p.Dimension() == dimension && sent(p.Kind()) {
				out.Kinds[p.Kind()] = check
			}
		}
		if survey.HasLevel && dimension == chunks.Overworld {
			out.Spawn = &spawnJSON{X: survey.Level.SpawnX, Z: survey.Level.SpawnZ}
			if survey.Level.SpawnYKnown {
				out.Spawn.Y = &survey.Level.SpawnY
			}
		}
	}
	return out
}

// firstKinds is the kinds a page from before the catalog knows.
var firstKinds = map[structures.Kind]bool{
	structures.Fortress: true, structures.Monument: true, structures.Outpost: true, structures.Village: true,
	structures.WitchHut: true, structures.Stronghold: true, structures.TrialChamber: true,
}

func clamp[T any](list []T, more int) ([]T, int) {
	if len(list) > structures.MaxPerLayer {
		more += len(list) - structures.MaxPerLayer
		list = list[:structures.MaxPerLayer]
	}
	if list == nil {
		list = []T{}
	}
	return list, more
}

// How a village's standing for the player asking is answered.
const (
	// standingKnown: the village has met this player, and Value is what it
	// thinks of them.
	standingKnown = "known"
	// standingNone: the village has no standing for this player.
	standingNone = "none"
	// standingUnknown: the village has standings, and nothing says which
	// of the world's players the session is.
	standingUnknown = "unknown"
	// standingPending: the session's player has been seen in the game, but
	// only since the snapshot the standings were read from. Which record
	// is theirs is not said until a snapshot taken after they were seen.
	standingPending = "pending"
)

type standingJSON struct {
	State string `json:"state"`
	Value *int32 `json:"value,omitempty"`
}

// structureDetailJSON is one recorded structure and what the save holds in
// it, as of the survey taken at At.
type structureDetailJSON struct {
	At time.Time `json:"at"`
	structures.Structure
	// Detail is left out for a survey that could not work it out.
	Detail *structures.Detail `json:"detail,omitempty"`
	// Standing is with a village the game has counted, and is only ever
	// the standing of the player asking.
	Standing *standingJSON `json:"standing,omitempty"`
}

const (
	// maxKnownPlayers bounds how many players' ids are remembered. A
	// server holds a few dozen players; past this the memory is started
	// again.
	maxKnownPlayers = 4096
	// playerMemory is how long a player's id is believed after the live
	// layer last showed them under their gamertag.
	playerMemory = 30 * time.Minute
)

// knownPlayer is the id the world knows one session's player by, and when
// the live layer first and last bore that out.
type knownPlayer struct {
	id          int64
	first, last time.Time
}

// worldMark is what tells one world from another, and a world from an
// earlier copy of itself: an id means something in one world only.
type worldMark struct {
	known bool
	seed  int64
	tick  int64
}

func markOf(survey structures.Survey) worldMark {
	return worldMark{known: survey.HasLevel, seed: survey.Level.Seed, tick: survey.Level.Tick}
}

// follows reports whether m is the same world as before, no earlier in
// its own time. A world that cannot be told is taken for another.
func (m worldMark) follows(before worldMark) bool {
	return m.known && before.known && m.seed == before.seed && m.tick >= before.tick
}

// handleStructureDetail serves what the save holds inside one recorded
// structure, named the way the page's address names it: by its kind and
// the middle of its box.
func (s *Server) handleStructureDetail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var dimension chunks.Dimension = -1
	for _, d := range chunks.Dimensions {
		if d.Name() == q.Get("dimension") {
			dimension = d
		}
	}
	x, errX := strconv.ParseInt(q.Get("x"), 10, 32)
	z, errZ := strconv.ParseInt(q.Get("z"), 10, 32)
	if dimension < 0 || errX != nil || errZ != nil || q.Get("kind") == "" {
		http.Error(w, "a structure is named by its dimension, kind, x and z", http.StatusBadRequest)
		return
	}
	survey, ok := s.Structures.Last()
	if !ok {
		http.Error(w, "no survey yet", http.StatusNotFound)
		return
	}
	layer := survey.Layers[dimension]
	// Only what the list would have sent can be asked about.
	recorded, _ := clamp(layer.Recorded, 0)
	for i, st := range recorded {
		midX, midZ := st.MinX+(st.MaxX-st.MinX)/2, st.MinZ+(st.MaxZ-st.MinZ)/2
		if string(st.Kind) != q.Get("kind") || int64(midX) != x || int64(midZ) != z || s.holds(st.Kind) {
			continue
		}
		out := structureDetailJSON{At: survey.At, Structure: st}
		if i < len(layer.Details) {
			out.Detail = layer.Details[i]
		}
		// A standing is one player's. A service session is no player,
		// so its answer has none, not even one that says it is unknown.
		if out.Detail != nil && out.Detail.Village != nil && !auth.IsService(r.Context()) {
			out.Standing = s.standing(r, out.Detail.Village, survey)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	http.Error(w, "no such structure", http.StatusNotFound)
}

// standing is what a village thinks of the player asking. Who that is
// comes from the session and nowhere else: nothing in the request can name
// another player, and no other player's standing is in the answer.
func (s *Server) standing(r *http.Request, v *structures.VillageDetail, survey structures.Survey) *standingJSON {
	if v.Met == 0 {
		return &standingJSON{State: standingNone}
	}
	player, state := s.playerID(r, survey)
	if state != standingKnown {
		return &standingJSON{State: state}
	}
	value, met := v.Standing(player)
	if !met {
		return &standingJSON{State: standingNone}
	}
	return &standingJSON{State: standingKnown, Value: &value}
}

// now is the clock the memory of players is kept by.
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// seenLive is the id the game gives the session's player, if the live
// layer shows them now. The session holds an XUID and the world's records
// do not, so the two are joined where both are seen at once: the agent
// reports the gamertag each XUID is online under, and the live layer the
// id of the player of that gamertag.
func (s *Server) seenLive(xuid string) (int64, bool) {
	if s.Heads == nil || s.Live == nil {
		return 0, false
	}
	_, self := s.Heads.Listing(xuid)
	if self == "" {
		return 0, false
	}
	var found []int64
	now := time.Now()
	for _, dimension := range render.Dimensions {
		for _, p := range s.Live.Store.Snapshot(dimension, now).Players {
			if icons.Fold(p.Name) != self {
				continue
			}
			if n, err := strconv.ParseInt(p.ID, 10, 64); err == nil {
				found = append(found, n)
			}
		}
	}
	// Two players under one name cannot be told apart by it.
	if len(found) != 1 {
		return 0, false
	}
	return found[0], true
}

// playerID is the UniqueID the world's records know the session's player
// by, with standingKnown; or why it cannot be said.
//
// An id is one world's. It is remembered only while the surveys go on
// being of the same world and no earlier in it, so a world put in place of
// another, or put back to an earlier copy, starts the memory again: the
// same id may be another player's there. It is believed for playerMemory
// after the live layer last bore it out. And it is used only against a
// snapshot taken after it was first borne out, since a snapshot from
// before may be of the world before this one, which nothing can tell until
// the next snapshot is read.
func (s *Server) playerID(r *http.Request, survey structures.Survey) (int64, string) {
	id, ok := auth.FromContext(r.Context())
	if !ok || auth.IsService(r.Context()) || !auth.IsXUID(id.XUID) {
		return 0, standingUnknown
	}
	live, seen := s.seenLive(id.XUID)
	now, world := s.now(), markOf(survey)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !world.follows(s.playersWorld) || len(s.players) >= maxKnownPlayers {
		s.players = nil
	}
	s.playersWorld = world
	known, remembered := s.players[id.XUID]
	if remembered && now.Sub(known.last) > playerMemory {
		delete(s.players, id.XUID)
		remembered = false
	}
	if seen {
		if !remembered || known.id != live {
			known = knownPlayer{id: live, first: now}
		}
		known.last = now
		if s.players == nil {
			s.players = map[string]knownPlayer{}
		}
		s.players[id.XUID] = known
		remembered = true
	}
	switch {
	case !remembered:
		return 0, standingUnknown
	case known.first.After(survey.At):
		return 0, standingPending
	}
	return known.id, standingKnown
}
