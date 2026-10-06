package trails

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
)

var start = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func player(name string, x, z float64) live.Entity {
	return live.Entity{ID: "id-" + name, Name: name, X: x, Y: 64.5, Z: z}
}

// walk records one player moving ten blocks east a second, from x at at.
func walk(r *Recorder, name string, at time.Time, x float64, steps int) time.Time {
	for i := range steps {
		r.Record("overworld", at.Add(time.Duration(i)*time.Second), []live.Entity{player(name, x+float64(i)*10, 0)})
	}
	return at.Add(time.Duration(steps) * time.Second)
}

func only(t *testing.T, reply Reply) Trail {
	t.Helper()
	if len(reply.Players) != 1 {
		t.Fatalf("%d players in the answer, want 1: %+v", len(reply.Players), reply.Players)
	}
	return reply.Players[0]
}

func count(reply Reply) int {
	n := 0
	for _, p := range reply.Players {
		for _, s := range p.Segments {
			n += len(s)
		}
	}
	return n
}

func TestATrailIsWhereAPlayerWent(t *testing.T) {
	r := New(time.Hour, 100)
	end := walk(r, "Dotablaze", start, 100, 5)
	trail := only(t, r.Trails("overworld", "", time.Time{}, end))
	if trail.Name != "Dotablaze" || len(trail.Segments) != 1 || len(trail.Segments[0]) != 5 {
		t.Fatalf("trail = %+v", trail)
	}
	if first, last := trail.Segments[0][0], trail.Segments[0][4]; first != (Point{start.Unix(), 100, 64, 0}) || last != (Point{start.Unix() + 4, 140, 64, 0}) {
		t.Errorf("from %+v to %+v", first, last)
	}
	raw, err := json.Marshal(trail.Segments[0][0])
	if want := fmt.Sprintf("[%d,100,64,0]", start.Unix()); err != nil || string(raw) != want {
		t.Errorf("a point is sent as %s, want %s", raw, want)
	}
	reply := r.Trails("overworld", "", time.Time{}, end)
	if reply.MaxAgeSeconds != 3600 || reply.MaxPoints != 100 || reply.More != 0 {
		t.Errorf("limits in the answer = %+v", reply)
	}
	if got := r.Trails("nether", "", time.Time{}, end); len(got.Players) != 0 || got.Players == nil {
		t.Errorf("the nether = %+v, want an empty list", got.Players)
	}
	if got := r.Trails("narnia", "", time.Time{}, end); len(got.Players) != 0 {
		t.Errorf("a dimension there is not = %+v", got.Players)
	}
}

func TestAPlayerStandingStillLeavesOnePoint(t *testing.T) {
	r := New(time.Hour, 100)
	for i := range 60 {
		// Shuffling about within a block or two.
		r.Record("overworld", start.Add(time.Duration(i)*time.Second), []live.Entity{player("Steve", 10+float64(i%3), 5)})
	}
	if n := count(r.Trails("overworld", "", time.Time{}, start.Add(time.Minute))); n != 1 {
		t.Errorf("%d points, want 1", n)
	}
}

// A line is only drawn where the player went: not across the time they
// were away, a teleport, or the other side of a portal.
func TestATrailBreaksWhereThePlayerDidNotWalk(t *testing.T) {
	r := New(time.Hour, 100)
	at := walk(r, "Alex", start, 0, 3)
	// Gone for a minute, then back where they were.
	at = walk(r, "Alex", at.Add(time.Minute), 30, 3)
	// Teleported 5,000 blocks between two samples.
	at = walk(r, "Alex", at, 5000, 3)
	// Into the nether and out again.
	r.Record("nether", at, []live.Entity{player("Alex", 625, 0)})
	r.Record("nether", at.Add(time.Second), []live.Entity{player("Alex", 635, 0)})
	end := walk(r, "Alex", at.Add(2*time.Second), 5030, 2)

	over := only(t, r.Trails("overworld", "", time.Time{}, end))
	var lengths []int
	for _, s := range over.Segments {
		lengths = append(lengths, len(s))
	}
	if fmt.Sprint(lengths) != "[3 3 3 2]" {
		t.Errorf("overworld lines of %v points, want [3 3 3 2]", lengths)
	}
	nether := only(t, r.Trails("nether", "", time.Time{}, end))
	if len(nether.Segments) != 1 || len(nether.Segments[0]) != 2 || nether.Segments[0][0].X != 625 {
		t.Errorf("nether = %+v", nether)
	}
}

func dropped(reason string) float64 {
	return testutil.ToFloat64(metricDropped.WithLabelValues(reason))
}

func TestAPlayerKeepsNoMoreThanTheLimitOfPoints(t *testing.T) {
	r := New(time.Hour, 5)
	before := dropped("count")
	end := walk(r, "Steve", start, 0, 40)
	trail := only(t, r.Trails("overworld", "", time.Time{}, end))
	if n := count(Reply{Players: []Trail{trail}}); n != 5 {
		t.Fatalf("%d points held, want 5", n)
	}
	// The newest five, as one line.
	if len(trail.Segments) != 1 || trail.Segments[0][0].X != 350 || trail.Segments[0][4].X != 390 {
		t.Errorf("kept %+v", trail.Segments)
	}
	if got := dropped("count") - before; got != 35 {
		t.Errorf("%v points counted as dropped, want 35", got)
	}
	if got := testutil.ToFloat64(metricPoints); got != 5 {
		t.Errorf("points gauge = %v, want 5", got)
	}
	if got := testutil.ToFloat64(metricLimit.WithLabelValues("points_per_player")); got != 5 {
		t.Errorf("limit gauge = %v, want 5", got)
	}
}

func TestPointsOlderThanTheLimitAreLetGo(t *testing.T) {
	r := New(10*time.Minute, 1000)
	before := dropped("age")
	walk(r, "Steve", start, 0, 10)
	walk(r, "Alex", start.Add(8*time.Minute), 0, 10)
	if got := testutil.ToFloat64(metricLimit.WithLabelValues("age_seconds")); got != 600 {
		t.Errorf("limit gauge = %v, want 600", got)
	}

	// Asking is enough: nothing has to be feeding the recorder.
	now := start.Add(10*time.Minute + 5*time.Second)
	reply := r.Trails("overworld", "", time.Time{}, now)
	if len(reply.Players) != 2 || count(reply) != 5+10 {
		t.Fatalf("after ten minutes: %d points over %d players, want 15 over 2", count(reply), len(reply.Players))
	}
	for _, p := range reply.Players {
		for _, s := range p.Segments {
			for _, point := range s {
				if age := now.Unix() - point.T; age > 600 {
					t.Errorf("%s has a point %d seconds old", p.Name, age)
				}
			}
		}
	}
	if got := dropped("age") - before; got != 5 {
		t.Errorf("%v points counted as aged out, want 5", got)
	}
	if got := testutil.ToFloat64(metricOldest); got > 600 || got < 590 {
		t.Errorf("oldest point gauge = %v, want just under 600", got)
	}

	r.Prune(start.Add(15 * time.Minute))
	if reply := r.Trails("overworld", "", time.Time{}, start.Add(15*time.Minute)); len(reply.Players) != 1 || reply.Players[0].Name != "Alex" {
		t.Errorf("after fifteen minutes: %+v", reply.Players)
	}
	r.Prune(start.Add(time.Hour))
	if got := testutil.ToFloat64(metricPlayers) + testutil.ToFloat64(metricPoints) + testutil.ToFloat64(metricOldest); got != 0 {
		t.Errorf("after an hour the gauges still add up to %v", got)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.trails) != 0 {
		t.Errorf("%d trails are still held with nothing in them", len(r.trails))
	}
}

func TestOnlySoManyPlayersHaveATrail(t *testing.T) {
	r := New(time.Hour, 100)
	before := dropped("players")
	for i := range MaxPlayers + 10 {
		r.Record("overworld", start.Add(time.Duration(i)*time.Second), []live.Entity{player(fmt.Sprintf("p%03d", i), 0, 0)})
	}
	reply := r.Trails("overworld", "", time.Time{}, start.Add(time.Minute*2))
	if len(reply.Players) != MaxPlayers {
		t.Fatalf("%d trails, want %d", len(reply.Players), MaxPlayers)
	}
	// The ten seen longest ago made room.
	if reply.Players[0].Name != "p010" {
		t.Errorf("the oldest trail kept is %s, want p010", reply.Players[0].Name)
	}
	if got := dropped("players") - before; got != 10 {
		t.Errorf("%v points counted as dropped for room, want 10", got)
	}
}

func TestAnAnswerCarriesOnlySoManyPoints(t *testing.T) {
	r := New(24*time.Hour, 10_000)
	var end time.Time
	for _, name := range []string{"a", "b", "c"} {
		end = walk(r, name, start, 0, 10_000)
	}
	reply := r.Trails("overworld", "", time.Time{}, end)
	if n := count(reply); n > MaxServed || n != 3*(MaxServed/3) {
		t.Errorf("%d points served, want %d", n, 3*(MaxServed/3))
	}
	if reply.More != 30_000-3*(MaxServed/3) {
		t.Errorf("more = %d", reply.More)
	}
	// Each player's newest are the ones kept.
	for _, p := range reply.Players {
		if last := p.Segments[len(p.Segments)-1]; last[len(last)-1].X != 99_990 {
			t.Errorf("%s ends at x %d, want 99990", p.Name, last[len(last)-1].X)
		}
	}
}

func TestTrailsCanBeAskedForByPlayerAndSince(t *testing.T) {
	r := New(time.Hour, 100)
	end := walk(r, "Dotablaze", start, 0, 10)
	walk(r, "Steve", start, 500, 10)

	if got := only(t, r.Trails("overworld", "dotablaze", time.Time{}, end)); got.Name != "Dotablaze" {
		t.Errorf("asked for dotablaze, got %s", got.Name)
	}
	if got := r.Trails("overworld", "Herobrine", time.Time{}, end); len(got.Players) != 0 {
		t.Errorf("a player nobody has seen = %+v", got.Players)
	}
	recent := only(t, r.Trails("overworld", "Steve", start.Add(6*time.Second), end))
	if len(recent.Segments) != 1 || len(recent.Segments[0]) != 3 || recent.Segments[0][0].T != start.Unix()+7 {
		t.Errorf("since the sixth second = %+v", recent.Segments)
	}
}

func TestWhatIsNotAPlayersPositionIsNotRecorded(t *testing.T) {
	r := New(time.Hour, 100)
	r.Record("overworld", start, []live.Entity{
		{ID: "1", Name: "", X: 1, Z: 1},
		{ID: "2", Name: "Twin", X: 10, Z: 10},
		{ID: "3", Name: "twin", X: 900, Z: 900},
		{ID: "4", Name: "Far", X: 4e7, Z: 0},
		{ID: "5", Name: "High", X: 0, Y: 1e9, Z: 0},
	})
	r.Record("the_void", start, []live.Entity{player("Lost", 0, 0)})
	trail := only(t, r.Trails("overworld", "", time.Time{}, start))
	if trail.Name != "Twin" || trail.Segments[0][0].X != 10 {
		t.Errorf("recorded %+v", trail)
	}
}

type frames map[string]live.Frame

func (f frames) Snapshot(dimension string, _ time.Time) live.Frame {
	if frame, ok := f[dimension]; ok {
		return frame
	}
	return live.Frame{Stale: true}
}

func TestSampleRecordsPlayersAndNeverMobs(t *testing.T) {
	r := New(time.Hour, 100)
	src := frames{"overworld": {
		At:      start,
		Players: []live.Entity{player("Steve", 0, 0)},
		Mobs:    []live.Entity{{ID: "m", Name: "Bessie", Type: "cow", X: 50, Z: 50}},
	}}
	r.Sample(src, start)
	// The same frame again is not a second sighting.
	r.Sample(src, start.Add(time.Second))
	src["overworld"] = live.Frame{At: start.Add(2 * time.Second), Players: []live.Entity{player("Steve", 20, 0)}, Mobs: src["overworld"].Mobs}
	r.Sample(src, start.Add(2*time.Second))
	// A stale frame's players are no longer where it says.
	src["overworld"] = live.Frame{At: start.Add(3 * time.Second), Players: []live.Entity{player("Steve", 90, 0)}, Stale: true}
	r.Sample(src, start.Add(3*time.Second))

	trail := only(t, r.Trails("overworld", "", time.Time{}, start.Add(3*time.Second)))
	if trail.Name != "Steve" || len(trail.Segments) != 1 || len(trail.Segments[0]) != 2 || trail.Segments[0][1].X != 20 {
		t.Errorf("trail = %+v", trail)
	}
}

func TestRecordingAndReadingAtOnce(t *testing.T) {
	r := New(time.Hour, 50)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 200 {
				at := start.Add(time.Duration(i) * time.Second)
				r.Record("overworld", at, []live.Entity{player(fmt.Sprintf("p%d", w), float64(i*10), 0)})
				r.Trails("overworld", "", time.Time{}, at)
				r.Prune(at)
			}
		})
	}
	wg.Wait()
	if n := count(r.Trails("overworld", "", time.Time{}, start.Add(200*time.Second))); n != 200 {
		t.Errorf("%d points held, want 4 players at their limit of 50", n)
	}
}

// The expectation is worked out from what was recorded, not from the
// recorder: x = 10*i, and a line breaks at each multiple of 1500 because
// the player was not seen for a minute before it.
func TestAReplyForALongTrailIsTheNewestPointsAndDoesNotCopyTheRest(t *testing.T) {
	const held, line = 50_000, 1500
	r := New(24*time.Hour, held)
	at := func(i int) time.Time { return start.Add(time.Duration(i) * time.Second) }
	for i := range held {
		stamp := at(i)
		if i%line == 0 {
			stamp = stamp.Add(time.Duration(i/line) * time.Minute)
		}
		r.Record("overworld", stamp, []live.Entity{player("Dotablaze", float64(10*i), 0)})
	}
	end := at(held).Add(held / line * time.Minute)

	// The expectation is built from what Record was given.
	stamp := func(i int) int64 {
		s := at(i)
		if i%line == 0 {
			s = s.Add(time.Duration(i/line) * time.Minute)
		}
		return s.Unix()
	}
	expect := func(first int) [][]Point {
		var lines [][]Point
		for i := first; i < held; i++ {
			if i == first || i%line == 0 {
				lines = append(lines, nil)
			}
			lines[len(lines)-1] = append(lines[len(lines)-1], Point{T: stamp(i), X: int32(10 * i), Y: 64, Z: 0})
		}
		return lines
	}

	for name, since := range map[string]time.Time{"all": {}, "since": at(5000)} {
		first := held - MaxServed
		matching := held
		if !since.IsZero() {
			matching = held - 5001
		}
		reply := r.Trails("overworld", "", since, end)
		if reply.More != matching-MaxServed {
			t.Errorf("%s: more = %d, want %d", name, reply.More, matching-MaxServed)
		}
		if got := only(t, reply).Segments; !reflect.DeepEqual(got, expect(first)) {
			t.Errorf("%s: the segments differ: %d lines of %d points, want %d lines of %d points", name, len(got), count(reply), len(expect(first)), MaxServed)
		}
	}

	// Serving 20,000 of 50,000 held points must cost about the 20,000, not
	// a copy of everything that matched.
	r.Trails("overworld", "", time.Time{}, end)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	reply := r.Trails("overworld", "", time.Time{}, end)
	runtime.ReadMemStats(&after)
	served := count(reply)
	if bytes := after.TotalAlloc - before.TotalAlloc; bytes > uint64(served)*24*3/2 {
		t.Errorf("a reply of %d points allocated %d bytes, want under %d", served, bytes, uint64(served)*24*3/2)
	}
}
