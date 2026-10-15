package trails

import (
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

// dateLayout names a day. Days are UTC: the server has no way to know which
// zone a viewer reads the page in.
const dateLayout = "2006-01-02"

type day struct {
	date   string
	online time.Duration
	blocks float64
	// dimensions is a bit for each of render.Dimensions the player was in.
	dimensions uint8
}

// account takes one sample of a player into the day it was taken on. The
// time since the last sample is online time when it is short, and the way
// moved since is distance when the player walked it and did not arrive by
// a portal or a teleport, by the same rule that breaks a line.
func (t *trail) account(at time.Time, x, z float64, dimension uint8, begins bool) {
	date := at.UTC().Format(dateLayout)
	if n := len(t.days); n == 0 || t.days[n-1].date != date {
		// A clock set back lands in a day already held, and is added to it.
		if i := slices.IndexFunc(t.days, func(d day) bool { return d.date == date }); i >= 0 {
			t.days = append(t.days, t.days[i])
			t.days = slices.Delete(t.days, i, i+1)
		} else {
			t.days = append(t.days, day{date: date})
		}
	}
	d := &t.days[len(t.days)-1]
	d.dimensions |= 1 << dimension
	if t.seenAt.IsZero() || !at.After(t.seenAt) {
		return
	}
	if gap := at.Sub(t.seenAt); gap <= OnlineGap {
		d.online += gap
	}
	if !begins {
		d.blocks += math.Hypot(x-t.seenX, z-t.seenZ)
	}
}

// forgetDays lets go of the days that ended before cutoff.
func (t *trail) forgetDays(cutoff time.Time) {
	first := cutoff.UTC().Format(dateLayout)
	t.days = slices.DeleteFunc(t.days, func(d day) bool { return d.date < first })
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
	for _, d := range t.days {
		online += d.online
		blocks += d.blocks
		seen |= d.dimensions
		out.Days = append(out.Days, DayStats{Date: d.date, OnlineSeconds: int(d.online.Seconds()), Blocks: int(d.blocks)})
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
