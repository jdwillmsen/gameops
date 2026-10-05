package server

import (
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
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
	}
	if survey, ok := s.Structures.Last(); ok {
		layer := survey.Layers[dimension]
		out.Surveyed, out.At, out.Prediction = true, &survey.At, survey.Check.State
		// The survey keeps to this bound already. It is applied again
		// here because this is where a list becomes a response.
		out.Recorded, out.RecordedMore = clamp(layer.Recorded, layer.RecordedMore)
		out.Predicted, out.PredictedMore = clamp(layer.Predicted, layer.PredictedMore)
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
