package main

import (
	"regexp"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricCorruptionDetected = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mc_console_bridge_world_corruption_detected",
		Help: "1 once the server has reported its world corrupt since this bridge started. A repair that follows does not clear it: the server repairs by dropping what it cannot find.",
	})
	metricCorruptionLines = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mc_console_bridge_world_corruption_lines_total",
		Help: "Distinct server log lines reporting world corruption. A line replayed from history after a reconnect is not counted again.",
	})
	metricCorruptionSeenAt = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mc_console_bridge_world_corruption_last_seen_timestamp_seconds",
		Help: "The time on the latest server log line reporting world corruption.",
	})
)

// corruptionRe matches the server's own reports that the world database is
// damaged: any LevelDB status other than OK at world open, which the server
// follows with "Trying repair", and the run-time check that shuts it down.
// Anchored on the log-level tag so a player typing the words in chat cannot
// raise it.
var corruptionRe = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}):\d+ (?:INFO|WARN|ERROR)\] (?:LevelDB .* status NOT OK\(|Level corruption detected)`)

// The server stamps its log in the container's zone, which is UTC.
const serverTimeLayout = "2006-01-02 15:04:05"

// mc-server-runner replays recent history on every connect, so the same line
// arrives again after each redial; it is still one report. Remembering the
// lines already counted is enough, since the server prints one at a time and
// a world has few of them in its life.
var corruptionCounted = struct {
	sync.Mutex
	lines  map[string]struct{}
	latest time.Time
}{lines: map[string]struct{}{}}

const maxCountedCorruptionLines = 256

func noteCorruption(line string, receivedAt time.Time) {
	m := corruptionRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	metricCorruptionDetected.Set(1)

	at, err := time.ParseInLocation(serverTimeLayout, m[1], time.UTC)
	if err != nil {
		at = receivedAt
	}
	corruptionCounted.Lock()
	defer corruptionCounted.Unlock()
	if _, seen := corruptionCounted.lines[line]; seen {
		return
	}
	if len(corruptionCounted.lines) >= maxCountedCorruptionLines {
		clear(corruptionCounted.lines)
	}
	corruptionCounted.lines[line] = struct{}{}
	metricCorruptionLines.Inc()
	if at.After(corruptionCounted.latest) {
		corruptionCounted.latest = at
		metricCorruptionSeenAt.Set(float64(at.Unix()))
	}
}
