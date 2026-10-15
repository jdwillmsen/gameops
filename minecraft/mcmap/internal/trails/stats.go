package trails

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// OnlineGap is the longest silence between two samples of a player that is
// still counted as time online. Samples come about a second apart, and the
// pack slows its pace on a loaded server, so a few missed ones are not a
// player leaving; a player who logs out is not listed at all, and the next
// sample after they return is further off than this. Time past it is
// counted as offline, so a stalled feed costs at most this much per stall.
const OnlineGap = 2 * time.Minute

// dateLayout names a day, for the per-day figures. Days are UTC: the server has no way to know which
// zone a viewer reads the page in.
const dateLayout = "2006-01-02"

// bucketWidth is how finely time online and distance are kept. The window
// they are summed over is cut at the second, by taking only the part of the
// oldest bucket that falls inside it.
const bucketWidth = 5 * 60

type bucket struct {
	start  int64
	online time.Duration
	blocks float64
	// dimensions is a bit for each of render.Dimensions the player was in.
	dimensions uint8
}

// account takes one sample of a player into the bucket it falls in. The
// time since the last sample is online time when it is short, and the way
// moved since is distance when the player walked it and did not arrive by
// a portal or a teleport, by the same rule that breaks a line.
func (t *trail) account(at time.Time, x, z float64, dimension uint8, begins bool) {
	start := at.Unix() / bucketWidth * bucketWidth
	i, found := slices.BinarySearchFunc(t.buckets, start, func(b bucket, s int64) int { return cmp.Compare(b.start, s) })
	if !found {
		t.buckets = slices.Insert(t.buckets, i, bucket{start: start})
	}
	b := &t.buckets[i]
	b.dimensions |= 1 << dimension
	if t.seenAt.IsZero() || !at.After(t.seenAt) {
		return
	}
	if gap := at.Sub(t.seenAt); gap <= OnlineGap {
		b.online += gap
	}
	if !begins {
		b.blocks += math.Hypot(x-t.seenX, z-t.seenZ)
	}
}

// forget lets go of the buckets that ended before cutoff.
func (t *trail) forget(cutoff time.Time) {
	t.buckets = slices.DeleteFunc(t.buckets, func(b bucket) bool { return b.start+bucketWidth <= cutoff.Unix() })
}

// DayStats is one day of a player's time and travel.
type DayStats struct {
	// Date is the day in UTC, as 2006-01-02.
	Date          string `json:"date"`
	OnlineSeconds int    `json:"onlineSeconds"`
	Blocks        int    `json:"blocks"`
}

// Stats is what the trails say a player did within the time they are kept.
type Stats struct {
	Name string `json:"name"`
	// WindowSeconds is how far back the figures go: the retention.
	WindowSeconds int `json:"windowSeconds"`
	// GapSeconds is the longest silence counted as time online.
	GapSeconds    int `json:"gapSeconds"`
	OnlineSeconds int `json:"onlineSeconds"`
	// Blocks is the distance walked, flown or sailed in the window, along
	// the ground and not counting a jump between places.
	Blocks int `json:"blocks"`
	// Days is oldest first.
	Days []DayStats `json:"days"`
	// Dimensions are those the player was seen in, in the map's order.
	Dimensions []string `json:"dimensions"`
	// LastSeen is in Unix seconds, in LastDimension.
	LastSeen      int64  `json:"lastSeen"`
	LastDimension string `json:"lastDimension"`
}

// Stats is the figures for the named player as of now, or false if no trail
// of theirs is held.
func (r *Recorder) Stats(player string, now time.Time) (Stats, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(now)
	t := r.trails[key(strings.TrimSpace(player))]
	if t == nil || t.seenAt.IsZero() {
		return Stats{}, false
	}
	out := Stats{
		Name:          t.name,
		WindowSeconds: int(r.MaxAge.Seconds()),
		GapSeconds:    int(OnlineGap.Seconds()),
		Days:          []DayStats{},
		Dimensions:    []string{},
		LastSeen:      t.seenAt.Unix(),
	}
	if int(t.dimension) < len(render.Dimensions) {
		out.LastDimension = render.Dimensions[t.dimension]
	}
	var online time.Duration
	var blocks float64
	var seen uint8
	cutoff := now.Add(-r.MaxAge).Unix()
	perDay := map[string]*[2]float64{}
	for _, b := range t.buckets {
		// The oldest bucket may straddle the start of the window.
		inside := 1.0
		if b.start < cutoff {
			inside = float64(b.start+bucketWidth-cutoff) / bucketWidth
		}
		seen |= b.dimensions
		secs, walked := b.online.Seconds()*inside, b.blocks*inside
		online += time.Duration(secs * float64(time.Second))
		blocks += walked
		date := time.Unix(b.start, 0).UTC().Format(dateLayout)
		if perDay[date] == nil {
			perDay[date] = &[2]float64{}
		}
		perDay[date][0] += secs
		perDay[date][1] += walked
	}
	for date, v := range perDay {
		out.Days = append(out.Days, DayStats{Date: date, OnlineSeconds: int(v[0]), Blocks: int(v[1])})
	}
	slices.SortFunc(out.Days, func(a, b DayStats) int { return strings.Compare(a.Date, b.Date) })
	out.OnlineSeconds, out.Blocks = int(online.Seconds()), int(blocks)
	for i, name := range render.Dimensions {
		if seen&(1<<i) != 0 {
			out.Dimensions = append(out.Dimensions, name)
		}
	}
	return out, true
}
