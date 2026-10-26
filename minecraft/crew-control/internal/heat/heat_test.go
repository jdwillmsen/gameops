package heat

import (
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func total(g Grid) (n int) {
	for _, c := range g.Cells {
		n += int(c[2])
	}
	return
}

func TestPositionGridMatchesRawSamples(t *testing.T) {
	s := New()
	rng := rand.New(rand.NewSource(1))
	type raw struct{ x, z float64 }
	var raws []raw
	want := map[[2]int]int{}
	for i := 0; i < 1800; i++ { // 1 h at 2 s
		x, z := 300+rng.Float64()*160-0.5, -500+rng.Float64()*128
		if i%7 == 0 {
			x = -x // negative coordinates floor, not truncate
		}
		raws = append(raws, raw{x, z})
		s.Add("crew-01", t0.Add(time.Duration(i)*2*time.Second), x, z, false)
		want[[2]int{int(math.Floor(x / 4)), int(math.Floor(z / 4))}]++
	}
	s.Add("crew-02", t0, 1, 1, false)
	now := t0.Add(time.Hour - time.Second)
	g := s.Query("crew-01", Position, "1h", now)
	got := map[[2]int]int{}
	for _, c := range g.Cells {
		got[[2]int{int(c[0]), int(c[1])}] = int(c[2])
	}
	if len(got) != len(want) {
		t.Fatalf("cells %d want %d", len(got), len(want))
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("cell %v = %d want %d", k, got[k], n)
		}
	}
	if g.Samples != uint64(len(raws)) || total(g) != len(raws) {
		t.Fatalf("samples %d total %d", g.Samples, total(g))
	}
	if all := s.Query("", Position, "1h", now); total(all) != len(raws)+1 {
		t.Fatalf("fleet total %d", total(all))
	}
}

func TestWindowExcludesOlderSamples(t *testing.T) {
	s := New()
	s.Add("a", t0, 0, 0, false)
	s.Add("a", t0.Add(50*time.Minute), 100, 100, false)
	g := s.Query("a", Position, "15m", t0.Add(55*time.Minute))
	if len(g.Cells) != 1 || g.Cells[0][0] != 25 {
		t.Fatalf("%v", g.Cells)
	}
}

func TestRetentionDropsOldSlices(t *testing.T) {
	s := New()
	s.Add("a", t0, 0, 0, false)
	s.Add("a", t0.Add(Retention+2*time.Minute), 0, 0, false)
	if len(s.slices) != 1 {
		t.Fatalf("slices %d", len(s.slices))
	}
}

func TestMiningLayerOnlyCountsMiningSamples(t *testing.T) {
	s := New()
	s.Add("a", t0, 0, 0, false)
	s.Add("a", t0.Add(2*time.Second), 40, 0, true)
	g := s.Query("a", Mining, "15m", t0.Add(time.Minute))
	if len(g.Cells) != 1 || g.Cells[0][0] != 10 {
		t.Fatalf("%v", g.Cells)
	}
}

func TestTravelCoversPathOnceAndSkipsJumps(t *testing.T) {
	s := New()
	s.Add("a", t0, 0, 0, false)
	s.Add("a", t0.Add(2*time.Second), 20, 0, false)
	g := s.Query("a", Travel, "15m", t0.Add(time.Minute))
	if len(g.Cells) != 6 || g.Max != 1 {
		t.Fatalf("path cells %v", g.Cells)
	}
	s.Add("a", t0.Add(4*time.Second), 400, 0, false)  // teleport
	s.Add("a", t0.Add(30*time.Second), 410, 0, false) // gap
	if g2 := s.Query("a", Travel, "15m", t0.Add(time.Minute)); len(g2.Cells) != 6 {
		t.Fatalf("jump drew a path: %v", g2.Cells)
	}
}

func TestCellCapBoundsASlice(t *testing.T) {
	s := New()
	for i := 0; i < maxCellsPerSlice+50; i++ {
		s.Add("a", t0, float64(i*CellSize), 0, false)
	}
	g := s.Query("a", Position, "15m", t0)
	if len(g.Cells) != maxCellsPerSlice || g.Dropped != 50 {
		t.Fatalf("cells %d dropped %d", len(g.Cells), g.Dropped)
	}
}

type fakeSrc struct {
	pos []telemetry.Position
	st  []telemetry.Status
}

func (f fakeSrc) Positions() []telemetry.Position { return f.pos }
func (f fakeSrc) Snapshot() []telemetry.Status    { return f.st }

func TestSampleSkipsOfflineDeadAndOtherDimensions(t *testing.T) {
	s := New()
	s.Sample(fakeSrc{
		pos: []telemetry.Position{
			{ID: "ok", X: 8, Z: 8, Known: true},
			{ID: "off", X: 8, Z: 8, Known: true},
			{ID: "dead", X: 8, Z: 8, Known: true},
			{ID: "nether", X: 8, Z: 8, Known: true, Dim: 1},
			{ID: "unknown", Known: false},
		},
		st: []telemetry.Status{
			{ID: "ok", Online: true, Task: &telemetry.TaskSummary{Template: "gather_and_deposit"}},
			{ID: "off"}, {ID: "dead", Online: true, Dead: true}, {ID: "nether", Online: true}, {ID: "unknown", Online: true},
		},
	}, t0)
	if g := s.Query("", Position, "15m", t0); total(g) != 1 {
		t.Fatalf("%v", g.Cells)
	}
	if g := s.Query("", Mining, "15m", t0); total(g) != 1 {
		t.Fatalf("mining %v", g.Cells)
	}
}

type gate bool

func (g gate) Operator(*http.Request) (auth.Operator, bool) { return auth.Operator{}, bool(g) }

func serve(open bool, url string) *httptest.ResponseRecorder {
	s := New()
	s.Add("crew-01", time.Now(), 5, 5, false)
	mux := http.NewServeMux()
	s.MountOperator(mux, gate(open), func(id string) bool { return id == "crew-01" })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
	return rec
}

func TestEndpointRequiresSession(t *testing.T) {
	rec := serve(false, "/v1/heatmap")
	if rec.Code != 401 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if rec.Body.String() != "{\"error\":\"signed_out\"}\n" {
		t.Fatalf("body leaked: %s", rec.Body)
	}
}

func TestEndpointAnswersAndValidates(t *testing.T) {
	rec := serve(true, "/v1/heatmap?unit=crew-01&layer=position&window=1h")
	var g Grid
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil || rec.Code != 200 || total(g) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for url, code := range map[string]int{
		"/v1/heatmap?layer=x": 400, "/v1/heatmap?window=2d": 400, "/v1/heatmap?unit=nope": 404,
	} {
		if r := serve(true, url); r.Code != code {
			t.Errorf("%s: %d want %d", url, r.Code, code)
		}
	}
}
