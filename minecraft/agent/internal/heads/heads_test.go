package heads

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

const (
	steveUUID, steveXUID = "00000000-0000-0000-0000-000000000001", "2535400000000001"
	alexUUID, alexXUID   = "00000000-0000-0000-0000-000000000002", "2535400000000002"
)

type fakeMap struct {
	mu      sync.Mutex
	reports [][]mapclient.PlayerHead
	err     error
	got     chan struct{}
}

func (f *fakeMap) ReportHeads(_ context.Context, players []mapclient.PlayerHead) error {
	f.mu.Lock()
	f.reports = append(f.reports, players)
	err := f.err
	f.mu.Unlock()
	f.got <- struct{}{}
	return err
}

func (f *fakeMap) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reports)
}

// next waits for one more report and returns it.
func (f *fakeMap) next(t *testing.T) map[string]mapclient.PlayerHead {
	t.Helper()
	select {
	case <-f.got:
	case <-time.After(5 * time.Second):
		t.Fatal("no report reached the map")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]mapclient.PlayerHead{}
	for _, p := range f.reports[len(f.reports)-1] {
		out[p.XUID] = p
	}
	return out
}

func (f *fakeMap) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-f.got:
		t.Fatal("a report reached the map when none was due")
	case <-time.After(d):
	}
}

func start(t *testing.T, interval time.Duration) (*Watch, *fakeMap) {
	t.Helper()
	m := &fakeMap{got: make(chan struct{}, 64)}
	w := New(m, logging.New("error"), interval)
	w.settle = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return w, m
}

// classic is a synthetic 64x64 skin whose face is one colour, with no hat.
func classic(face color.NRGBA) Skin {
	px := make([]byte, 64*64*4)
	for y := 8; y < 16; y++ {
		for x := 8; x < 16; x++ {
			copy(px[(y*64+x)*4:], []byte{face.R, face.G, face.B, 255})
		}
	}
	return Skin{Width: 64, Height: 64, Data: px, ResourcePatch: []byte(`{"geometry":{"default":"geometry.humanoid.custom"}}`)}
}

func faceOf(t *testing.T, head []byte) color.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(head))
	if err != nil {
		t.Fatalf("the head is not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
		t.Fatalf("the head is %dx%d, want 8x8", b.Dx(), b.Dy())
	}
	return color.NRGBAModel.Convert(img.At(4, 4)).(color.NRGBA)
}

var (
	red  = color.NRGBA{200, 0, 0, 255}
	blue = color.NRGBA{0, 0, 200, 255}
)

func TestEveryoneOnTheListIsReportedWithTheirHead(t *testing.T) {
	w, m := start(t, time.Hour)
	w.Begin()
	w.List([]Entry{
		{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)},
		{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: classic(blue)},
	})
	got := m.next(t)
	if len(got) != 2 || got[steveXUID].Gamertag != "Steve" || got[alexXUID].Gamertag != "Alex" {
		t.Fatalf("reported %v", got)
	}
	if c := faceOf(t, got[steveXUID].Head); c != red {
		t.Errorf("Steve's head is %v", c)
	}
	if c := faceOf(t, got[alexXUID].Head); c != blue {
		t.Errorf("Alex's head is %v", c)
	}
}

// A skin is whatever another player's client sent. One this package cannot
// safely take a face from costs that player their head and nobody else
// anything; they are still named, so the map knows the gamertag is in use.
func TestASkinThatCannotBeTrustedGivesNoHead(t *testing.T) {
	persona := classic(red)
	persona.Persona = true
	otherModel := classic(red)
	otherModel.ResourcePatch = []byte(`{"geometry":{"default":"geometry.some_pack.dragon"}}`)
	brokenPatch := classic(red)
	brokenPatch.ResourcePatch = []byte(`{"geometry":`)
	hugePatch := classic(red)
	hugePatch.ResourcePatch = append([]byte(`{"geometry":{"default":"geometry.humanoid.custom"},"pad":"`), append(bytes.Repeat([]byte("a"), maxPatch), `"}`...)...)
	short := classic(red)
	short.Data = short.Data[:len(short.Data)-4]
	oddSize := Skin{Width: 100, Height: 100, Data: make([]byte, 100*100*4)}
	enormous := Skin{Width: 1 << 31, Height: 1 << 31, Data: make([]byte, 16)}
	empty := Skin{}

	for name, bad := range map[string]Skin{
		"a persona skin":            persona,
		"a skin for another model":  otherModel,
		"an unreadable model name":  brokenPatch,
		"an over-long model name":   hugePatch,
		"fewer bytes than its size": short,
		"a size no skin has":        oddSize,
		"a size that overflows":     enormous,
		"no image at all":           empty,
	} {
		t.Run(name, func(t *testing.T) {
			w, m := start(t, time.Hour)
			w.Begin()
			w.List([]Entry{
				{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: bad},
				{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: classic(blue)},
			})
			got := m.next(t)
			if p, named := got[steveXUID]; !named || p.Gamertag != "Steve" {
				t.Fatalf("the player is not named: %v", got)
			}
			if len(got[steveXUID].Head) != 0 {
				t.Errorf("%s gave a head", name)
			}
			if c := faceOf(t, got[alexXUID].Head); c != blue {
				t.Errorf("the player beside them lost their head: %v", c)
			}
		})
	}
}

func TestASkinNamingNoModelIsAStandardOne(t *testing.T) {
	w, m := start(t, time.Hour)
	w.Begin()
	bare := classic(red)
	bare.ResourcePatch = nil
	slim := classic(blue)
	slim.ResourcePatch = []byte(`{"geometry":{"default":"geometry.humanoid.customSlim"}}`)
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: bare}, {UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: slim}})
	got := m.next(t)
	if faceOf(t, got[steveXUID].Head) != red || faceOf(t, got[alexXUID].Head) != blue {
		t.Error("a standard skin gave no head")
	}
}

func TestLeavingAndChangingSkinAreReported(t *testing.T) {
	w, m := start(t, time.Hour)
	w.Begin()
	w.List([]Entry{
		{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)},
		{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: classic(blue)},
	})
	m.next(t)

	w.Reskin(steveUUID, classic(blue))
	if got := m.next(t); faceOf(t, got[steveXUID].Head) != blue {
		t.Error("a changed skin did not change the head")
	}
	// Named by UUID alone, as a departure is.
	w.List([]Entry{{UUID: alexUUID, Remove: true}})
	if got := m.next(t); len(got) != 1 || got[steveXUID].Gamertag != "Steve" {
		t.Errorf("after Alex left, reported %v", got)
	}
	// A skin for nobody on the list is nobody's.
	w.Reskin(alexUUID, classic(red))
	m.quiet(t, 50*time.Millisecond)
}

func TestAnEntryThatIsNotASignedInPlayerIsLeftOut(t *testing.T) {
	w, m := start(t, time.Hour)
	w.Begin()
	w.List([]Entry{
		{UUID: steveUUID, XUID: "", Name: "Offline", Skin: classic(red)},
		{UUID: alexUUID, XUID: "not-a-number", Name: "Odd", Skin: classic(red)},
		{UUID: "00000000-0000-0000-0000-000000000003", XUID: alexXUID, Name: "", Skin: classic(red)},
	})
	if got := m.next(t); len(got) != 0 {
		t.Errorf("reported %v", got)
	}
}

// Two replicas run, and only the one in the game sees skins. The other
// holds an empty list, and reporting it would wipe what the first sent.
func TestNothingIsReportedOutsideASession(t *testing.T) {
	w, m := start(t, 10*time.Millisecond)
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)}})
	w.Reskin(steveUUID, classic(blue))
	m.quiet(t, 100*time.Millisecond)
	if n := m.count(); n != 0 {
		t.Errorf("%d reports from a process that is in no game", n)
	}
}

func TestTheEndOfASessionIsReportedOnceAndThenNothing(t *testing.T) {
	w, m := start(t, 20*time.Millisecond)
	w.Begin()
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)}})
	if got := m.next(t); len(got) != 1 {
		t.Fatalf("reported %v", got)
	}
	w.End()
	// Reports of the session itself may still be in flight; the last word
	// has to be that nobody is online.
	deadline := time.After(5 * time.Second)
	for {
		if got := m.next(t); len(got) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the map was never told the session ended")
		default:
		}
	}
	m.quiet(t, 150*time.Millisecond)

	// The next session starts from nobody, not from who was here before.
	w.Begin()
	w.List([]Entry{{UUID: alexUUID, XUID: alexXUID, Name: "Alex", Skin: classic(blue)}})
	if got := m.next(t); len(got) != 1 || got[alexXUID].Gamertag != "Alex" {
		t.Errorf("the next session reported %v", got)
	}
}

// The map keeps heads in memory. Repeating the list is what restores them
// after it restarts, and what gets past a report that failed.
func TestAnUnchangedListIsSentAgainAndAFailureIsNotTheEnd(t *testing.T) {
	w, m := start(t, 20*time.Millisecond)
	m.err = errors.New("map: report heads: connection refused")
	w.Begin()
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)}})
	for range 3 {
		if got := m.next(t); len(got) != 1 {
			t.Fatalf("reported %v", got)
		}
	}
}

func TestANilWatchIsAnAgentWithNoMap(t *testing.T) {
	var w *Watch
	w.Begin()
	w.List([]Entry{{UUID: steveUUID, XUID: steveXUID, Name: "Steve", Skin: classic(red)}})
	w.Reskin(steveUUID, classic(red))
	w.End()
	w.Run(context.Background())
}
