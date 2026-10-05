package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/generations"
)

// ChunkCensus is the part of the chunk census the internal API exposes.
type ChunkCensus interface {
	Last() (chunks.Report, bool)
	Acknowledge(checkedAt time.Time) error
}

// Generations is the part of the retained world copies the internal API
// exposes, so that whoever is deciding on a restore can see how far back
// each copy would take the world before they scale anything down.
type Generations interface {
	Look() generations.View
}

type blockPos struct {
	Dimension string `json:"dimension"`
	X         int64  `json:"x"`
	Z         int64  `json:"z"`
}

type worldJSON struct {
	Checked      bool           `json:"checked"`
	CheckedAt    *time.Time     `json:"checkedAt,omitempty"`
	Chunks       map[string]int `json:"chunks,omitempty"`
	Missing      map[string]int `json:"missing,omitempty"`
	MissingTotal *int           `json:"missingTotal,omitempty"`
	Lost         map[string]int `json:"lost,omitempty"`
	LostTotal    *int           `json:"lostTotal,omitempty"`
	Sample       []blockPos     `json:"lostSample,omitempty"`
	// Generations is independent of the count: copies are held, and worth
	// reporting, before the first census of a fresh volume.
	Generations *generationsJSON `json:"generations,omitempty"`
}

type generationJSON struct {
	// Name is the directory under the data volume's generations directory,
	// which is what a restore copies the world out of.
	Name    string    `json:"name"`
	TakenAt time.Time `json:"takenAt"`
	Files   int       `json:"files"`
	Bytes   int64     `json:"bytes"`
}

type generationsJSON struct {
	Current  *generationJSON `json:"current,omitempty"`
	Previous *generationJSON `json:"previous,omitempty"`
	Damaged  *generationJSON `json:"damaged,omitempty"`
}

func (s *Server) generations() *generationsJSON {
	if s.Generations == nil {
		return nil
	}
	v := s.Generations.Look()
	held := func(g *generations.Generation) *generationJSON {
		if g == nil {
			return nil
		}
		return &generationJSON{Name: g.Name, TakenAt: g.TakenAt, Files: g.Files, Bytes: g.Bytes}
	}
	return &generationsJSON{Current: held(v.Current), Previous: held(v.Previous), Damaged: held(v.Damaged)}
}

func byName(m map[chunks.Dimension]int) map[string]int {
	out := map[string]int{}
	for _, d := range chunks.Dimensions {
		out[d.Name()] = m[d]
	}
	return out
}

func (s *Server) handleWorld(w http.ResponseWriter, _ *http.Request) {
	kept := s.generations()
	r, ok := s.Chunks.Last()
	if !ok {
		writeJSON(w, http.StatusOK, worldJSON{Generations: kept})
		return
	}
	missing, lost := r.TotalMissing(), r.TotalLost()
	out := worldJSON{
		Checked: true, CheckedAt: &r.At,
		Chunks:  byName(r.Present),
		Missing: byName(r.Missing), MissingTotal: &missing,
		Lost: byName(r.Lost), LostTotal: &lost,
		Generations: kept,
	}
	for _, p := range r.Sample {
		out.Sample = append(out.Sample, blockPos{Dimension: p.Dim.Name(), X: int64(p.X) * 16, Z: int64(p.Z) * 16})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAcknowledge accepts the world as one count found it, named by the
// count's time so that a newer count, which may hold losses the operator has
// not seen, is never accepted in its place. What it forgets is logged,
// since this is the one call that clears the alert without the world being
// repaired.
func (s *Server) handleAcknowledge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CheckedAt time.Time `json:"checkedAt"`
	}
	if err := decodeStrict(w, r, &req); err != nil || req.CheckedAt.IsZero() {
		http.Error(w, "an acknowledgement names the count it accepts: {\"checkedAt\": ...} from GET /internal/v1/world", http.StatusBadRequest)
		return
	}
	before, _ := s.Chunks.Last()
	switch err := s.Chunks.Acknowledge(req.CheckedAt); {
	case errors.Is(err, chunks.ErrNoCensus), errors.Is(err, chunks.ErrStale):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		s.log().Error("chunk loss not acknowledged", "error", err.Error())
		http.Error(w, "could not record the acknowledgement", http.StatusInternalServerError)
	default:
		s.log().Warn("chunk loss acknowledged", "lost", before.TotalLost(), "missing", before.TotalMissing(), "checked_at", req.CheckedAt)
		w.WriteHeader(http.StatusNoContent)
	}
}
