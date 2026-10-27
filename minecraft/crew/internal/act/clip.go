package act

import (
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The closed set of clipping reasons. A consumer keys on the code, never on
// the log text; an unknown code reads as other.
const (
	// ClipPredictedInside: the pose the unit predicted for a tick put its
	// body inside a block's collision shape.
	ClipPredictedInside = "predicted_inside_block"
	// ClipCorrectedInside: the server corrected a tick whose predicted body
	// was inside a block, so the prediction ran through something solid.
	ClipCorrectedInside = "corrected_inside_block"
	// ClipCorrectedClear: the server corrected a tick whose predicted body
	// was in free space, which is a shape, speed or entity mismatch rather
	// than a clip.
	ClipCorrectedClear = "corrected_in_free_space"
)

// ClipReasons lists every code, for the docs test and for consumers that
// validate what a unit reported.
var ClipReasons = []string{ClipPredictedInside, ClipCorrectedInside, ClipCorrectedClear}

// clipDepth is how far into a box the body must sit to count as a clip. It
// clears the settle band, inside which the unit is held, not clipped.
const clipDepth = 0.02

// clipLogEvery bounds the log, not the counter, per reason.
const clipLogEvery = 10 * time.Second

var metricClips = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "crew_movement_clips_total",
	Help: "Ticks whose predicted body was inside a block, and server corrections, by reason code.",
}, []string{"reason"})

// overlapDepth is how far the body at feet sits inside the deepest block box
// it overlaps, or zero when it overlaps none.
func overlapDepth(w Solid, feet mgl32.Vec3) float32 {
	var deepest float32
	for _, o := range obstacles(w, feet.Sub(bodyLo), feet.Add(bodyHi)) {
		d := float32(math.MaxFloat32)
		for a := 0; a < 3; a++ {
			lo, hi := feet[a]-bodyLo[a], feet[a]+bodyHi[a]
			d = min(d, min(hi, o.hi[a])-max(lo, o.lo[a]))
		}
		deepest = max(deepest, d)
	}
	return deepest
}

// SetClipDiagnostics sends a `movement_clip` event, with the reason code and
// block names around the feet but no position, to log.
func (a *Actuator) SetClipDiagnostics(log CorrectionLogger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clipLogger = log
}

// noteClip counts a clip and, at most once per reason per interval, logs it.
func (a *Actuator) noteClip(reason string, depth float32, at sentTick) {
	metricClips.WithLabelValues(reason).Inc()
	if a.clipLogger == nil {
		return
	}
	now := a.now()
	if last, ok := a.clipLogged[reason]; ok && now.Sub(last) < clipLogEvery {
		a.clipSuppressed[reason]++
		return
	}
	if a.clipLogged == nil {
		a.clipLogged, a.clipSuppressed = map[string]time.Time{}, map[string]uint64{}
	}
	feet := blockPos(at.pose.Feet)
	a.clipLogger.Info("movement_clip", map[string]any{
		"reason_code": reason, "depth": round2(depth),
		"skill": skillOrIdle(at.skill), "on_ground": at.pose.OnGround,
		"feet": a.blockName(feet), "below": a.blockName(protocol.BlockPos{feet[0], feet[1] - 1, feet[2]}),
		"suppressed": a.clipSuppressed[reason],
	})
	a.clipLogged[reason], a.clipSuppressed[reason] = now, 0
}
