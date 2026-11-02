package telemetry

import "time"

const (
	// ReasonMovementCorrected is the closed code of a rubberbanding event.
	ReasonMovementCorrected = "movement_corrected"

	// A walking unit the server agrees with is corrected rarely; a handful of
	// corrections inside half a minute is the server pulling it back.
	rubberbandWindow = 30 * time.Second
	rubberbandAt     = 10
	// rubberbandClear is the window total at or under which the state clears.
	// It sits well below rubberbandAt so a unit hovering near the line is one
	// episode, not a stream of events.
	rubberbandClear = 2
)

type correctionSample struct {
	at time.Time
	n  uint64
}

// observeCorrections folds one poll's running correction count into the
// window and reports rubberbanding once as the window total reaches
// rubberbandAt, re-arming when it falls to rubberbandClear. A nil count is a
// unit that predates the field: it neither raises nor clears. The first
// reading, one after a gap and one after a restart (a count that went down)
// only set the baseline, because growth across them is not known. The text is
// fixed because the feed is public; the count rides in Attrs.
func (t *track) observeCorrections(cur *uint64, now time.Time) []Event {
	if cur == nil {
		t.resetCorrections()
		return nil
	}
	prev := t.corrections
	t.corrections = cur
	if prev == nil || *cur < *prev {
		t.correctionWindow = nil
		return nil
	}
	if d := *cur - *prev; d > 0 {
		t.correctionWindow = append(t.correctionWindow, correctionSample{at: now, n: d})
	}
	var total uint64
	keep := t.correctionWindow[:0]
	for _, s := range t.correctionWindow {
		if now.Sub(s.at) < rubberbandWindow {
			keep = append(keep, s)
			total += s.n
		}
	}
	t.correctionWindow = keep
	switch {
	case !t.rubberbanding && total >= rubberbandAt:
		t.rubberbanding = true
		return []Event{{
			At: now, Kind: "rubberbanding", Level: "warn",
			Text:       "the server keeps correcting its movement",
			ReasonCode: ReasonMovementCorrected,
			Attrs:      map[string]any{"corrections": total, "window_s": int(rubberbandWindow / time.Second)},
		}}
	case t.rubberbanding && total <= rubberbandClear:
		t.rubberbanding = false
	}
	return nil
}

func (t *track) resetCorrections() {
	t.corrections, t.correctionWindow, t.rubberbanding = nil, nil, false
}
