package act

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type clipLog struct{ events []map[string]any }

func (l *clipLog) Info(event string, fields map[string]any) {
	if event == "movement_clip" {
		l.events = append(l.events, fields)
	}
}

func clips(reason string) float64 { return testutil.ToFloat64(metricClips.WithLabelValues(reason)) }

func TestAStayInsideABlockIsOneEvent(t *testing.T) {
	// A pose placed inside stone, as a block landing on the unit would.
	w := shaped{{0, 0, 0}: {"stone", nil}}
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0.1, 0.5}})
	log := &clipLog{}
	a.SetClipDiagnostics(log)
	before := clips(ClipPredictedInside)
	for i := 0; i < 5; i++ {
		_ = a.Tick(Input{})
	}
	if got := clips(ClipPredictedInside) - before; got != 1 {
		t.Errorf("predicted_inside_block counted %v times, want once for the stay", got)
	}
	if len(log.events) != 1 || log.events[0]["reason_code"] != ClipPredictedInside {
		t.Fatalf("events = %v, want one predicted_inside_block", log.events)
	}
	if _, has := log.events[0]["x"]; has {
		t.Error("a clip event must carry no coordinates")
	}
}

func TestWalkingAlongAWallIsNotAClip(t *testing.T) {
	w := shaped{{0, 0, 3}: {"stone", nil}, {0, 1, 3}: {"stone", nil}}
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})
	log := &clipLog{}
	a.SetClipDiagnostics(log)
	for i := 0; i < 60; i++ {
		_ = a.Tick(Input{Forward: 1, Jump: i%12 == 0})
	}
	if len(log.events) != 0 {
		t.Errorf("events = %v, want none for a unit held by a wall", log.events)
	}
}

func TestACorrectionIsSplitByWhereTheNamedTickStood(t *testing.T) {
	w := shaped{{0, 0, 0}: {"stone", nil}}
	a, s := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0.1, 0.5}})
	log := &clipLog{}
	a.SetClipDiagnostics(log)
	_ = a.Tick(Input{})
	inside := s.inputs(t)[0]
	a.pose = Pose{Feet: mgl32.Vec3{5.5, 0, 0.5}, OnGround: true}
	_ = a.Tick(Input{})
	clear := s.inputs(t)[1]

	insideBefore, clearBefore := clips(ClipCorrectedInside), clips(ClipCorrectedClear)
	a.Apply(&packet.CorrectPlayerMovePrediction{Position: inside.Position.Add(mgl32.Vec3{0, 0.6, 0}), Tick: inside.Tick})
	a.Apply(&packet.CorrectPlayerMovePrediction{Position: clear.Position.Add(mgl32.Vec3{0.3, 0, 0}), Tick: clear.Tick})
	if got := clips(ClipCorrectedInside) - insideBefore; got != 1 {
		t.Errorf("corrected_inside_block counted %v, want 1", got)
	}
	if got := clips(ClipCorrectedClear) - clearBefore; got != 1 {
		t.Errorf("corrected_in_free_space counted %v, want 1", got)
	}
}

func TestClipLogIsRateLimitedPerReasonAndCountsWhatItDropped(t *testing.T) {
	a, _ := newAct(flat(), Pose{OnGround: true})
	log := &clipLog{}
	a.SetClipDiagnostics(log)
	when := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return when }
	correction := &packet.CorrectPlayerMovePrediction{Position: mgl32.Vec3{0, eyeHeight, 0}, OnGround: true}
	for i := 0; i < 3; i++ {
		a.Apply(correction)
	}
	when = when.Add(clipLogEvery)
	a.Apply(correction)
	if len(log.events) != 2 || log.events[1]["suppressed"] != uint64(2) {
		t.Fatalf("events = %v, want two with 2 suppressed on the second", log.events)
	}
}

func TestOverlapDepthIsTheShallowestWayOut(t *testing.T) {
	w := shaped{{0, 0, 0}: {"stone", nil}}
	got := overlapDepth(w, mgl32.Vec3{0.5, 0.9, 0.5})
	near(t, "depth", float64(got), 0.1, 1e-4)
	if d := overlapDepth(w, mgl32.Vec3{2.5, 0, 0.5}); d > 0 {
		t.Errorf("depth = %v clear of the block", d)
	}
}

func TestReadmeNamesEveryClipReason(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ClipReasons {
		if !strings.Contains(string(b), "`"+r+"`") {
			t.Errorf("README does not describe clip reason %q", r)
		}
	}
}
