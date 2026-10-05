package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/generations"
)

type fakeChunks struct {
	report       chunks.Report
	checked      bool
	acknowledged []time.Time
	ackErr       error
}

func (f *fakeChunks) Last() (chunks.Report, bool) { return f.report, f.checked }
func (f *fakeChunks) Acknowledge(at time.Time) error {
	f.acknowledged = append(f.acknowledged, at)
	return f.ackErr
}

func damaged() *fakeChunks {
	return &fakeChunks{checked: true, report: chunks.Report{
		At:      time.Date(2026, 10, 2, 2, 4, 49, 0, time.UTC),
		Present: map[chunks.Dimension]int{chunks.Overworld: 123284, chunks.Nether: 18052, chunks.End: 5262},
		Missing: map[chunks.Dimension]int{chunks.Overworld: 4753, chunks.Nether: 1413, chunks.End: 294},
		Lost:    map[chunks.Dimension]int{chunks.Overworld: 4760, chunks.Nether: 1413, chunks.End: 294},
		Sample:  []chunks.Pos{{Dim: chunks.Overworld, X: 10, Z: -3}},
	}}
}

// What the agent reads to warn players as they join.
func TestWorld_ReportsWhatIsMissingToTheAgentOnly(t *testing.T) {
	s := withLogin(t)
	s.Chunks = damaged()
	bearer := []string{"Authorization", "Bearer " + internalToken}

	if rec := do(s.Handler(), "GET", "/internal/v1/world", "", nil, bearer...); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("world state on the public handler = %d", rec.Code)
	}
	internal := s.InternalHandler()
	if rec := do(internal, "GET", "/internal/v1/world", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d", rec.Code)
	}
	rec := do(internal, "GET", "/internal/v1/world", "", nil, bearer...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Checked      bool           `json:"checked"`
		CheckedAt    time.Time      `json:"checkedAt"`
		Chunks       map[string]int `json:"chunks"`
		Missing      map[string]int `json:"missing"`
		MissingTotal int            `json:"missingTotal"`
		Lost         map[string]int `json:"lost"`
		LostTotal    int            `json:"lostTotal"`
		Sample       []struct {
			Dimension string `json:"dimension"`
			X, Z      int
		} `json:"lostSample"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Checked || got.MissingTotal != 6460 || got.LostTotal != 6467 || got.Lost["overworld"] != 4760 || got.Missing["nether"] != 1413 || got.Chunks["end"] != 5262 || !got.CheckedAt.Equal(damaged().report.At) {
		t.Errorf("body = %s", rec.Body)
	}
	// Block coordinates: what a player can find in game or on the map.
	if len(got.Sample) != 1 || got.Sample[0].Dimension != "overworld" || got.Sample[0].X != 160 || got.Sample[0].Z != -48 {
		t.Errorf("sample = %+v", got.Sample)
	}
}

func TestWorld_BeforeTheFirstCensusSaysSo(t *testing.T) {
	s := withLogin(t)
	s.Chunks = &fakeChunks{}
	rec := do(s.InternalHandler(), "GET", "/internal/v1/world", "", nil, "Authorization", "Bearer "+internalToken)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"checked":false}` {
		t.Errorf("GET = %d %s", rec.Code, rec.Body)
	}
}

// The operator acknowledges the count they looked at, named by its time;
// the service refuses if a newer one has landed since.
func TestWorld_AcknowledgingTheLoss(t *testing.T) {
	bearer := []string{"Authorization", "Bearer " + internalToken}
	seen := `{"checkedAt":"2026-10-02T02:04:49Z"}`
	for name, c := range map[string]struct {
		err  error
		want int
	}{
		"accepted":           {nil, http.StatusNoContent},
		"nothing to clear":   {chunks.ErrNoCensus, http.StatusConflict},
		"a newer count":      {chunks.ErrStale, http.StatusConflict},
		"cannot be recorded": {errors.New("disk full"), http.StatusInternalServerError},
	} {
		s := withLogin(t)
		f := damaged()
		f.ackErr = c.err
		s.Chunks = f
		if rec := do(s.InternalHandler(), "POST", "/internal/v1/world/acknowledge", seen, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: no token = %d", name, rec.Code)
		}
		rec := do(s.InternalHandler(), "POST", "/internal/v1/world/acknowledge", seen, nil, bearer...)
		if rec.Code != c.want || len(f.acknowledged) != 1 || !f.acknowledged[0].Equal(damaged().report.At) {
			t.Errorf("%s: POST = %d (want %d), acknowledged %v", name, rec.Code, c.want, f.acknowledged)
		}
	}
}

func TestWorld_AcknowledgingNeedsTheCountItAccepts(t *testing.T) {
	s := withLogin(t)
	f := damaged()
	s.Chunks = f
	for _, body := range []string{``, `{}`, `{"checkedAt":"yesterday"}`, `{"checkedAt":"2026-10-02T02:04:49Z","extra":1}`} {
		rec := do(s.InternalHandler(), "POST", "/internal/v1/world/acknowledge", body, nil, "Authorization", "Bearer "+internalToken)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q = %d, want 400", body, rec.Code)
		}
	}
	if len(f.acknowledged) != 0 {
		t.Errorf("acknowledged %v", f.acknowledged)
	}
}

type fakeGenerations struct{ view generations.View }

func (f *fakeGenerations) Look() generations.View { return f.view }

// Whoever is weighing a restore needs to know how far back each copy would
// take the world, and that is the one thing the metrics do not spell out
// per copy. It is reported whether or not a count has run.
func TestWorld_ReportsTheRetainedGenerations(t *testing.T) {
	taken := time.Date(2026, 10, 1, 23, 43, 38, 0, time.UTC)
	s := withLogin(t)
	s.Chunks = damaged()
	s.Generations = &fakeGenerations{view: generations.View{
		Current:  &generations.Generation{Name: "a", Marker: generations.Marker{TakenAt: taken, Files: 412, Bytes: 780906719}},
		Previous: &generations.Generation{Name: "b", Marker: generations.Marker{TakenAt: taken.Add(-15 * time.Minute), Files: 411}},
		Damaged:  &generations.Generation{Name: "damaged", Marker: generations.Marker{TakenAt: taken.Add(2*time.Hour + 21*time.Minute), Files: 405}},
	}}

	var got struct {
		Generations struct {
			Current, Previous, Damaged struct {
				Name    string    `json:"name"`
				TakenAt time.Time `json:"takenAt"`
				Files   int       `json:"files"`
				Bytes   int64     `json:"bytes"`
			}
		} `json:"generations"`
	}
	rec := do(s.InternalHandler(), "GET", "/internal/v1/world", "", nil, "Authorization", "Bearer "+internalToken)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Generations.Current.Name != "a" || !got.Generations.Current.TakenAt.Equal(taken) || got.Generations.Current.Bytes != 780906719 {
		t.Errorf("current = %+v", got.Generations.Current)
	}
	if got.Generations.Previous.Files != 411 || got.Generations.Damaged.Name != "damaged" {
		t.Errorf("previous %+v, damaged %+v", got.Generations.Previous, got.Generations.Damaged)
	}

	s.Chunks = &fakeChunks{}
	rec = do(s.InternalHandler(), "GET", "/internal/v1/world", "", nil, "Authorization", "Bearer "+internalToken)
	if !strings.Contains(rec.Body.String(), `"name":"a"`) || !strings.Contains(rec.Body.String(), `"checked":false`) {
		t.Errorf("before the first count = %s", rec.Body)
	}
}

// A fresh volume holds nothing yet, and says so by leaving them out rather
// than reporting a copy that is not there.
func TestWorld_NoGenerationsYetIsNotAnEmptyOne(t *testing.T) {
	s := withLogin(t)
	s.Chunks = &fakeChunks{}
	s.Generations = &fakeGenerations{}
	rec := do(s.InternalHandler(), "GET", "/internal/v1/world", "", nil, "Authorization", "Bearer "+internalToken)
	if body := strings.TrimSpace(rec.Body.String()); body != `{"checked":false,"generations":{}}` {
		t.Errorf("GET = %s", body)
	}
}
