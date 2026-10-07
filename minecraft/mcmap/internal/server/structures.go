package server

import (
	"net/http"
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
	// Spawn is the world spawn, with the overworld only.
	Spawn *spawnJSON `json:"spawn,omitempty"`
}

// handleStructures serves one dimension's structures from the last survey.
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
	out := structuresJSON{
		Recorded:   []structures.Structure{},
		Predicted:  []structures.Prediction{},
		Prediction: structures.SeedUnknown,
		Kinds:      map[structures.Kind]structures.KindCheck{},
	}
	if survey, ok := s.Structures.Last(); ok {
		layer := survey.Layers[dimension]
		out.Surveyed, out.At, out.Prediction = true, &survey.At, survey.Check.State
		// The survey keeps to this bound already. It is applied again
		// here because this is where a list becomes a response.
		out.Recorded, out.RecordedMore = clamp(layer.Recorded, layer.RecordedMore)
		out.Predicted, out.PredictedMore = clamp(layer.Predicted, layer.PredictedMore)
		for _, p := range structures.Predictors {
			if check, ok := survey.Check.Kinds[p.Kind()]; ok && p.Dimension() == dimension {
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
	writeJSON(w, http.StatusOK, out)
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

// maxKnownPlayers bounds how many players' ids are remembered. A server
// holds a few dozen players; past this the memory is started again.
const maxKnownPlayers = 4096

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
		if string(st.Kind) != q.Get("kind") || int64(midX) != x || int64(midZ) != z {
			continue
		}
		out := structureDetailJSON{At: survey.At, Structure: st}
		if i < len(layer.Details) {
			out.Detail = layer.Details[i]
		}
		if out.Detail != nil && out.Detail.Village != nil {
			out.Standing = s.standing(r, out.Detail.Village)
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	http.Error(w, "no such structure", http.StatusNotFound)
}

// standing is what a village thinks of the player asking. Who that is
// comes from the session and nowhere else: nothing in the request can name
// another player, and no other player's standing is in the answer.
func (s *Server) standing(r *http.Request, v *structures.VillageDetail) *standingJSON {
	if v.Met == 0 {
		return &standingJSON{State: standingNone}
	}
	player, ok := s.playerID(r)
	if !ok {
		return &standingJSON{State: standingUnknown}
	}
	value, met := v.Standing(player)
	if !met {
		return &standingJSON{State: standingNone}
	}
	return &standingJSON{State: standingKnown, Value: &value}
}

// playerID is the UniqueID the world's own records know the session's
// player by. The session holds an XUID and the world's records do not, so
// the two are joined where both are seen at once: the agent reports the
// gamertag each XUID is online under, and the live layer the id the game
// gives the player of that gamertag. It is remembered from then on, so a
// player need only have been in the game once since this service started.
func (s *Server) playerID(r *http.Request) (int64, bool) {
	id, ok := auth.FromContext(r.Context())
	if !ok || !auth.IsXUID(id.XUID) {
		return 0, false
	}
	if s.Heads != nil && s.Live != nil {
		if _, self := s.Heads.Listing(id.XUID); self != "" {
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
			if len(found) == 1 {
				s.mu.Lock()
				if s.players == nil || len(s.players) >= maxKnownPlayers {
					s.players = map[string]int64{}
				}
				s.players[id.XUID] = found[0]
				s.mu.Unlock()
				return found[0], true
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	player, known := s.players[id.XUID]
	return player, known
}
