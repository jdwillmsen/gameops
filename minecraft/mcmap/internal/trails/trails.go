// Package trails remembers where players have been, for the page to draw
// behind them.
//
// It is the one thing the map holds that is about a person rather than the
// world, so it holds as little as will draw a trail, for a stated time, in
// memory only. Restarting the service forgets every trail, on purpose:
// writing them to the volume would turn a decoration into a record of who
// was where and when that outlives the process, on the same volume as the
// retained copies of the world, and what a restart costs is at most one
// retention's worth of lines that the players redraw by playing.
package trails

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

var (
	metricPoints = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_trails_points",
		Help: "Trail points held in memory, over every player.",
	})
	metricPlayers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_trails_players",
		Help: "Players with a trail in memory.",
	})
	metricOldest = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_trails_oldest_point_age_seconds",
		Help: "Age of the oldest trail point held when trails were last pruned. It stays under the age limit.",
	})
	metricLimit = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mcmap_trails_limit",
		Help: "The retention limits in force: age_seconds, points_per_player, players.",
	}, []string{"limit"})
	metricDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_trails_points_dropped_total",
		Help: "Trail points let go, by the limit that let them go: age, count (a player's own limit), players (the oldest trail making room for a new player).",
	}, []string{"reason"})
)

const (
	// MaxPlayers is how many players have a trail at once. The live list
	// is what the game server's console says, and a name is only text, so
	// the number of trails cannot be left to it.
	MaxPlayers = 64
	// MaxServed is the most points one answer carries, over every player
	// in it: about half a megabyte.
	MaxServed = 20_000

	// minStep is how far, in blocks across the map, a player has to be
	// from their last point before another is kept. Positions arrive every
	// second, and a player standing at a furnace is not a trail.
	minStep = 4
	// A point begins a new line, instead of joining the one before, when
	// the player was not seen for breakAfter or is breakJump blocks from
	// where they were last seen: they logged off, or went through a portal
	// and back, or were teleported, and did not walk the straight line
	// between.
	breakAfter = 30 * time.Second
	breakJump  = 256

	// maxCoordinate is past the edge of any Bedrock world.
	maxCoordinate = 32_000_000
)

// Point is a place a player was. It is sent as [t, x, y, z], t in Unix
// seconds: a trail is thousands of them.
type Point struct {
	T       int64
	X, Y, Z int32
}

func (p Point) MarshalJSON() ([]byte, error) {
	return json.Marshal([4]int64{p.T, int64(p.X), int64(p.Y), int64(p.Z)})
}

type held struct {
	Point
	dimension uint8
	// begins is set on a point that does not follow from the one before.
	begins bool
}

type trail struct {
	name   string
	points []held
	// Where and when the player was last seen, kept point or not.
	seenAt    time.Time
	seenX     float64
	seenZ     float64
	dimension uint8
}

// Recorder keeps each player's recent positions, within its limits.
type Recorder struct {
	// MaxAge is how long a point is kept.
	MaxAge time.Duration
	// MaxPoints is the most points kept for one player.
	MaxPoints int

	mu     sync.Mutex
	trails map[string]*trail
	// frames is the last frame of each dimension that was recorded.
	frames map[string]time.Time
}

// New is a Recorder that keeps at most maxPoints points a player, none of
// them older than maxAge.
func New(maxAge time.Duration, maxPoints int) *Recorder {
	metricLimit.WithLabelValues("age_seconds").Set(maxAge.Seconds())
	metricLimit.WithLabelValues("points_per_player").Set(float64(maxPoints))
	metricLimit.WithLabelValues("players").Set(MaxPlayers)
	return &Recorder{MaxAge: maxAge, MaxPoints: maxPoints, trails: map[string]*trail{}, frames: map[string]time.Time{}}
}

// Source is the live picture the positions are taken from.
type Source interface {
	Snapshot(dimension string, now time.Time) live.Frame
}

// Run records the players in each new live frame until ctx ends. Mobs are
// never recorded.
func (r *Recorder) Run(ctx context.Context, src Source, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			r.Sample(src, now)
		}
	}
}

// Sample records whatever is new in the live picture at now, and lets go
// of what has grown too old.
func (r *Recorder) Sample(src Source, now time.Time) {
	for _, dimension := range render.Dimensions {
		f := src.Snapshot(dimension, now)
		if f.Stale || f.At.IsZero() {
			continue
		}
		r.mu.Lock()
		fresh := !f.At.Equal(r.frames[dimension])
		r.frames[dimension] = f.At
		r.mu.Unlock()
		if fresh {
			r.Record(dimension, f.At, f.Players)
		}
	}
	r.Prune(now)
}

func key(name string) string { return strings.ToLower(name) }

// Record takes the players seen in one dimension at at.
func (r *Recorder) Record(dimension string, at time.Time, players []live.Entity) {
	d := slices.Index(render.Dimensions, dimension)
	if d < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range players {
		k := key(p.Name)
		// Two entries with one name cannot be told apart, and a trail
		// zigzagging between two players is worse than the first one's.
		if k == "" || slices.ContainsFunc(players[:i], func(o live.Entity) bool { return key(o.Name) == k }) {
			continue
		}
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsNaN(p.Z) || math.Abs(p.X) > maxCoordinate || math.Abs(p.Y) > maxCoordinate || math.Abs(p.Z) > maxCoordinate {
			continue
		}
		t := r.trails[k]
		if t == nil {
			if len(r.trails) >= MaxPlayers {
				r.evict()
			}
			t = &trail{}
			r.trails[k] = t
		}
		t.name = p.Name
		begins := len(t.points) == 0 || t.dimension != uint8(d) || at.Sub(t.seenAt) > breakAfter ||
			math.Hypot(p.X-t.seenX, p.Z-t.seenZ) > breakJump
		t.seenAt, t.seenX, t.seenZ, t.dimension = at, p.X, p.Z, uint8(d)
		point := held{
			Point:     Point{T: at.Unix(), X: int32(math.Floor(p.X)), Y: int32(math.Floor(p.Y)), Z: int32(math.Floor(p.Z))},
			dimension: uint8(d), begins: begins,
		}
		if !begins {
			last := t.points[len(t.points)-1]
			if math.Hypot(float64(point.X-last.X), float64(point.Z-last.Z)) < minStep {
				continue
			}
		}
		t.points = append(t.points, point)
		if over := len(t.points) - r.MaxPoints; over > 0 {
			t.points = slices.Delete(t.points, 0, over)
			// What is now the oldest point no longer has the line that led
			// to it.
			t.points[0].begins = true
			metricDropped.WithLabelValues("count").Add(float64(over))
		}
	}
	r.export(time.Time{})
}

// evict lets go of the trail whose player was seen longest ago.
func (r *Recorder) evict() {
	oldest := ""
	for k, t := range r.trails {
		if oldest == "" || t.seenAt.Before(r.trails[oldest].seenAt) {
			oldest = k
		}
	}
	metricDropped.WithLabelValues("players").Add(float64(len(r.trails[oldest].points)))
	delete(r.trails, oldest)
}

// Prune lets go of every point older than MaxAge at now.
func (r *Recorder) Prune(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(now)
	r.export(now)
}

func (r *Recorder) prune(now time.Time) {
	cutoff := now.Add(-r.MaxAge).Unix()
	for k, t := range r.trails {
		old := 0
		for old < len(t.points) && t.points[old].T < cutoff {
			old++
		}
		if old > 0 {
			t.points = slices.Delete(t.points, 0, old)
			metricDropped.WithLabelValues("age").Add(float64(old))
		}
		switch {
		case len(t.points) == 0:
			delete(r.trails, k)
		case old > 0:
			t.points[0].begins = true
		}
	}
}

// export publishes what is held. The oldest point's age is only known
// against a clock, so it is left alone when now is zero.
func (r *Recorder) export(now time.Time) {
	points, oldest := 0, int64(math.MaxInt64)
	for _, t := range r.trails {
		points += len(t.points)
		if len(t.points) > 0 {
			oldest = min(oldest, t.points[0].T)
		}
	}
	metricPoints.Set(float64(points))
	metricPlayers.Set(float64(len(r.trails)))
	if now.IsZero() {
		return
	}
	age := 0.0
	if points > 0 {
		age = max(0, float64(now.Unix()-oldest))
	}
	metricOldest.Set(age)
}

// Trail is one player's lines in one dimension, oldest first. Each line is
// somewhere they went without a break; the gaps between lines are where
// they were not seen, or moved further than anyone walks in a second.
type Trail struct {
	Name     string    `json:"name"`
	Segments [][]Point `json:"segments"`
}

// Reply is the trails asked for and the limits they are kept within.
type Reply struct {
	Players []Trail `json:"players"`
	// More is how many points were left out to keep to MaxServed. Those
	// kept are each player's newest.
	More int `json:"more"`
	// MaxAgeSeconds and MaxPoints are the retention: no point is older
	// than the first, and no player has more than the second.
	MaxAgeSeconds int `json:"maxAgeSeconds"`
	MaxPoints     int `json:"maxPoints"`
}

// Trails is every player's trail in a dimension as of now, or only the
// named player's, and only the points after since if that is set.
func (r *Recorder) Trails(dimension, player string, since, now time.Time) Reply {
	reply := Reply{Players: []Trail{}, MaxAgeSeconds: int(r.MaxAge.Seconds()), MaxPoints: r.MaxPoints}
	d := slices.Index(render.Dimensions, dimension)
	if d < 0 {
		return reply
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// A recorder nothing is feeding still must not serve what has aged out.
	r.prune(now)
	r.export(now)

	// Counting first means only the points that are served are copied: at
	// the top of the limits, holding every matching point of every player
	// until it was trimmed cost on the order of 100 MB a request, with the
	// recorder locked.
	matching := func(p held) bool {
		return int(p.dimension) == d && (since.IsZero() || p.T > since.Unix())
	}
	type picked struct {
		name    string
		points  []held
		matched int
	}
	var found []picked
	for k, t := range r.trails {
		if player != "" && k != key(player) {
			continue
		}
		n := 0
		for _, p := range t.points {
			if matching(p) {
				n++
			}
		}
		if n > 0 {
			found = append(found, picked{t.name, t.points, n})
		}
	}
	slices.SortFunc(found, func(a, b picked) int { return cmp.Compare(key(a.name), key(b.name)) })
	share := MaxServed
	if len(found) > 0 {
		share = max(MaxServed/len(found), 1)
	}
	for _, f := range found {
		skip := max(f.matched-share, 0)
		reply.More += skip
		trail := Trail{Name: f.name}
		// One block holds every point served for the player; each segment
		// is a window onto it.
		block := make([]Point, 0, f.matched-skip)
		from := 0
		for _, p := range f.points {
			if !matching(p) {
				continue
			}
			if skip > 0 {
				skip--
				continue
			}
			if p.begins && len(block) > from {
				trail.Segments = append(trail.Segments, block[from:len(block):len(block)])
				from = len(block)
			}
			block = append(block, p.Point)
		}
		if len(block) > from {
			trail.Segments = append(trail.Segments, block[from:len(block):len(block)])
		}
		reply.Players = append(reply.Players, trail)
	}
	return reply
}
