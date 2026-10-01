// Package schedule decides when the map must leave the server's save state
// alone.
package schedule

import (
	"fmt"
	"strings"
	"time"
)

// window is a daily span in minutes since midnight UTC. end < start means it
// crosses midnight.
type window struct{ start, end int }

// Quiet is a set of daily UTC windows during which no snapshot is taken.
// They exist because other jobs pause world saving through a channel the
// bridge cannot see; resuming during one of those would unfreeze the world
// under that job's copy.
type Quiet []window

// ParseQuiet reads "HH:MM-HH:MM,HH:MM-HH:MM". An empty string is no windows.
func ParseQuiet(s string) (Quiet, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var q Quiet
	for _, part := range strings.Split(s, ",") {
		from, to, ok := strings.Cut(strings.TrimSpace(part), "-")
		if !ok {
			return nil, fmt.Errorf("quiet window %q is not HH:MM-HH:MM", part)
		}
		start, err := minutes(from)
		if err != nil {
			return nil, err
		}
		end, err := minutes(to)
		if err != nil {
			return nil, err
		}
		if start == end {
			return nil, fmt.Errorf("quiet window %q is empty", part)
		}
		q = append(q, window{start, end})
	}
	return q, nil
}

func minutes(hhmm string) (int, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil || len(hhmm) != 5 {
		return 0, fmt.Errorf("quiet window time %q is not HH:MM", hhmm)
	}
	return t.Hour()*60 + t.Minute(), nil
}

func (q Quiet) Contains(t time.Time) bool {
	t = t.UTC()
	m := t.Hour()*60 + t.Minute()
	for _, w := range q {
		if w.start < w.end && m >= w.start && m < w.end {
			return true
		}
		if w.start > w.end && (m >= w.start || m < w.end) {
			return true
		}
	}
	return false
}
