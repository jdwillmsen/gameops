package trails

import (
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
)

// Five blocks a second is a walk slow enough that every second's point is
// kept at full detail, and close enough that older ones are thinned.
const slow = 5

func segmentsOf(t *testing.T, r *Recorder, dimension string, now time.Time) [][]Point {
	t.Helper()
	return only(t, r.Trails(dimension, "", time.Time{}, now)).Segments
}

func TestALongWalkKeepsItsStartPastTheOldLimit(t *testing.T) {
	r := New(24*time.Hour, 5000)
	beforeCount, beforeThinned := dropped("count"), dropped("thinned")
	const steps = 2 * 3600
	end := walkBy(r, "Steve", start, 0, steps, slow)

	segs := segmentsOf(t, r, "overworld", end)
	if len(segs) != 1 {
		t.Fatalf("%d lines, want 1", len(segs))
	}
	line := segs[0]
	if line[0] != (Point{start.Unix(), 0, 64, 0}) {
		t.Errorf("the walk starts at %+v, want where it began", line[0])
	}
	if len(line) > 5000 {
		t.Errorf("%d points held, want at most 5000", len(line))
	}
	if got := dropped("count") - beforeCount; got != 0 {
		t.Errorf("%v points dropped by count, want them thinned instead", got)
	}
	if got := dropped("thinned") - beforeThinned; got != float64(steps-len(line)) {
		t.Errorf("%v points counted as thinned, want %d", got, steps-len(line))
	}
	// Past the hour and the thinning pass after it, no two points are
	// closer than the step.
	cut := end.Add(-midAge - 2*thinEvery).Unix()
	for i := 1; i < len(line) && line[i].T < cut; i++ {
		dx := float64(line[i].X - line[i-1].X)
		if dx < midStep {
			t.Fatalf("points %d and %d of the old walk are %v blocks apart, want %d or more", i-1, i, dx, midStep)
		}
	}
	// The last hour is at full detail.
	recent := 0
	for _, p := range line {
		if p.T > end.Add(-midAge+2*thinEvery).Unix() {
			recent++
		}
	}
	if want := int((midAge - 2*thinEvery) / time.Second); recent < want-2 {
		t.Errorf("%d points in the last hour, want all %d", recent, want)
	}
}

// Thinning may only take points out of a line's middle.
func TestThinningKeepsWhereEveryLineBeganAndEnded(t *testing.T) {
	r := New(24*time.Hour, 50_000)
	const n = 1500
	at := start
	type ends struct{ first, last Point }
	var want []ends
	for line := range 3 {
		// Each line is a minute after the last, and the third is
		// five thousand blocks away from the second.
		x := float64(line * 5000)
		walkBy(r, "Alex", at, x, n, slow)
		want = append(want, ends{
			Point{at.Unix(), int32(x), 64, 0},
			Point{at.Unix() + n - 1, int32(x) + (n-1)*slow, 64, 0},
		})
		at = at.Add(n*time.Second + time.Minute)
	}
	// Hours later, so that all three are old.
	end := walkBy(r, "Alex", at.Add(3*time.Hour), 0, 100, slow)

	segs := segmentsOf(t, r, "overworld", end)
	if len(segs) != 4 {
		t.Fatalf("%d lines, want 4", len(segs))
	}
	for i, w := range want {
		s := segs[i]
		if s[0] != w.first || s[len(s)-1] != w.last {
			t.Errorf("line %d runs %+v to %+v, want %+v to %+v", i, s[0], s[len(s)-1], w.first, w.last)
		}
		if len(s) >= n {
			t.Errorf("line %d has %d points, want it thinned from %d", i, len(s), n)
		}
	}
}

func TestThinningNeverBridgesADimensionChange(t *testing.T) {
	r := New(24*time.Hour, 50_000)
	at := start
	const n = 600
	for i := range n {
		r.Record("overworld", at.Add(time.Duration(i)*time.Second), []live.Entity{player("Alex", float64(i*slow), 0)})
	}
	at = at.Add(n * time.Second)
	for i := range n {
		r.Record("nether", at.Add(time.Duration(i)*time.Second), []live.Entity{player("Alex", float64(i*slow), 0)})
	}
	at = at.Add(n * time.Second)
	for i := range n {
		r.Record("overworld", at.Add(time.Duration(i)*time.Second), []live.Entity{player("Alex", float64(n*slow+i*slow), 0)})
	}
	end := walkBy(r, "Alex", at.Add(3*time.Hour), 0, 10, slow)

	over := segmentsOf(t, r, "overworld", end)
	if len(over) != 3 {
		t.Fatalf("%d overworld lines, want 3 (before the nether, after it, and the last)", len(over))
	}
	if last := over[0][len(over[0])-1]; last.X != (n-1)*slow || last.T != start.Unix()+n-1 {
		t.Errorf("the line into the nether ends at %+v, want its last point", last)
	}
	if first := over[1][0]; first.X != n*slow || first.T != start.Unix()+2*n {
		t.Errorf("the line out of the nether starts at %+v, want its first point", first)
	}
	nether := segmentsOf(t, r, "nether", end)
	if len(nether) != 1 || nether[0][0].T != start.Unix()+n || nether[0][len(nether[0])-1].T != start.Unix()+2*n-1 {
		t.Errorf("nether = %d lines, want the one line from %d to %d", len(nether), start.Unix()+n, start.Unix()+2*n-1)
	}
	if len(over[0]) >= n || len(nether[0]) >= n {
		t.Errorf("%d and %d points held, want both thinned from %d", len(over[0]), len(nether[0]), n)
	}
}

func TestThinningIsIdempotentAndOnlyRemovesPoints(t *testing.T) {
	r := New(24*time.Hour, 50_000)
	// Wander, so that the line bends and some points are near others.
	var recorded []held
	for i := range 6000 {
		x, z := float64(i%400)*slow/2, float64(i/400)*3
		r.Record("overworld", start.Add(time.Duration(i)*time.Second), []live.Entity{player("Alex", x, z)})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	tr := r.trails["alex"]
	recorded = slices.Clone(tr.points)
	now := start.Add(5 * time.Hour)

	thin(tr, now)
	once := slices.Clone(tr.points)
	thin(tr, now)
	if !slices.Equal(tr.points, once) {
		t.Errorf("thinning twice left %d points, once left %d", len(tr.points), len(once))
	}
	if len(once) >= len(recorded) {
		t.Fatalf("%d of %d points left, want some thinned", len(once), len(recorded))
	}
	at := 0
	for _, p := range once {
		next := slices.Index(recorded[at:], p)
		if next < 0 {
			t.Fatalf("point %+v is not one that was recorded, or is out of order", p)
		}
		at += next + 1
	}
}

func TestTheReplyStatesTheDetailPointsAreKeptAt(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want []Detail
	}{
		{24 * time.Hour, []Detail{{0, 4}, {3600, 16}}},
		{168 * time.Hour, []Detail{{0, 4}, {3600, 16}, {86400, 64}}},
		{30 * time.Minute, []Detail{{0, 4}}},
	} {
		got := New(tc.age, 100).Trails("overworld", "", time.Time{}, start).Thinning
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("a retention of %s states %v, want %v", tc.age, got, tc.want)
		}
	}
	if got := New(time.Hour, 100).Trails("narnia", "", time.Time{}, start).Thinning; len(got) == 0 {
		t.Errorf("the reply to a dimension there is not states no detail")
	}
}

func TestThinnedPointsAreCountedAndTheGaugesAgree(t *testing.T) {
	r := New(24*time.Hour, 50_000)
	before := dropped("thinned")
	end := walkBy(r, "Steve", start, 0, 3*3600, slow)
	r.Prune(end)
	held := count(r.Trails("overworld", "", time.Time{}, end))
	if got := dropped("thinned") - before; got != float64(3*3600-held) || got == 0 {
		t.Errorf("%v counted as thinned, want %d", got, 3*3600-held)
	}
	if got := testutil.ToFloat64(metricPoints); got != float64(held) {
		t.Errorf("points gauge = %v, want %d", got, held)
	}
}

// A trail is thinned when a minute of it has gone by, not by each point, so
// a recorder at the top of its limits allocates a bounded amount a point:
// the regrowth of the array a trail drops its front from, not a copy of the
// trail.
func TestHoldingAWeekCostsLittlePerRecordedPoint(t *testing.T) {
	const limit = 20_000
	r := New(168*time.Hour, limit)
	at := start
	// One slice, so that what is measured is the recorder's.
	ps := []live.Entity{player("Steve", 0, 0)}
	record := func(n int) {
		for range n {
			at = at.Add(time.Second)
			ps[0].X = float64(at.Unix()-start.Unix()) * slow
			r.Record("overworld", at, ps)
		}
	}
	record(2 * limit)
	before := dropped("thinned")
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	const more = 10_000
	record(more)
	runtime.ReadMemStats(&m1)
	if dropped("thinned") == before {
		t.Errorf("nothing was thinned over %d more points", more)
	}
	if per := float64(m1.TotalAlloc-m0.TotalAlloc) / more; per > 256 {
		t.Errorf("%.1f bytes allocated a recorded point, want under 256", per)
	}
}

// Held bytes a point, array slack included, over trails that have grown by
// appending to the limit. The README's memory figure comes from it.
func TestAHeldPointCostsAtMostThisManyBytes(t *testing.T) {
	const players, each = 8, 20_000
	r := New(168*time.Hour, each)
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	at := start
	for i := range each + 500 {
		at = at.Add(time.Second)
		var ps []live.Entity
		for p := range players {
			// Past every step, so that nothing is thinned and each trail
			// grows to the limit.
			ps = append(ps, player(fmt.Sprintf("p%d", p), float64(i)*oldStep/4, 0))
		}
		r.Record("overworld", at, ps)
	}
	runtime.GC()
	runtime.ReadMemStats(&m1)
	r.mu.Lock()
	held := 0
	for _, tr := range r.trails {
		held += len(tr.points)
	}
	r.mu.Unlock()
	if held != players*each {
		t.Fatalf("%d points held, want %d", held, players*each)
	}
	per := float64(m1.HeapAlloc-m0.HeapAlloc) / float64(held)
	t.Logf("%d points held by %d players, %.1f bytes a point", held, players, per)
	if per > 48 {
		t.Errorf("%.1f bytes a held point, want at most 48", per)
	}
	runtime.KeepAlive(r)
}

// BenchmarkRecord is a frame of 64 players, each a second on from the last,
// each trail past an hour old and walked slowly. The cost of a point includes the
// thinning pass its trail is due.
func BenchmarkRecord(b *testing.B) {
	const limit = 20_000
	r := New(168*time.Hour, limit)
	at := start
	ps := make([]live.Entity, MaxPlayers)
	for p := range ps {
		ps[p] = player(fmt.Sprintf("p%02d", p), 0, float64(p))
	}
	frame := func(i int) {
		at = at.Add(time.Second)
		for p := range ps {
			ps[p].X = float64(i) * slow
		}
		r.Record("overworld", at, ps)
	}
	// Eight hours of walking: past both the first hour and a full trail,
	// so that thinning and the limit are both at work.
	const warm = 8 * 3600
	for i := range warm {
		frame(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		frame(warm + i)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/MaxPlayers, "ns/point")
}

// BenchmarkThin is one pass over a trail of a week at the recommended limit,
// which is paid for by the thinEvery seconds of points since the last.
func BenchmarkThin(b *testing.B) {
	r := New(168*time.Hour, 50_000)
	end := walkBy(r, "Steve", start, 0, 6*3600, slow)
	r.mu.Lock()
	defer r.mu.Unlock()
	tr := r.trails["steve"]
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		thin(tr, end)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(thinEvery/time.Second), "ns/point")
}
