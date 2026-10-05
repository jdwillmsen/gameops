package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/plugin"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/worlddamage"
)

var damageAt = time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)

func lostWorld() mapclient.World {
	return mapclient.World{
		Checked: true, CheckedAt: damageAt, LostTotal: 6460,
		Lost: map[string]int{"overworld": 4753, "nether": 1413, "end": 294},
		LostSample: []mapclient.Chunk{
			{Dimension: "overworld", X: 2560, Z: -16},
			{Dimension: "nether", X: 2576, Z: 32},
		},
	}
}

type fakeDamageWatch struct {
	world    mapclient.World
	active   bool
	clearErr error
	cleared  int
	// afterClear is what Current reports once Clear refused, standing in
	// for the refresh the real watch does.
	afterClear *mapclient.World
}

func (f *fakeDamageWatch) Current(context.Context) (mapclient.World, bool) { return f.world, f.active }

func (f *fakeDamageWatch) Clear(context.Context) (mapclient.World, error) {
	f.cleared++
	if !f.active {
		return mapclient.World{}, worlddamage.ErrNothingToClear
	}
	held := f.world
	if f.clearErr != nil {
		if f.afterClear != nil {
			f.world = *f.afterClear
		}
		return held, f.clearErr
	}
	f.active = false
	return held, nil
}

func permissionsOf(levels map[string]plugin.Permission) func(context.Context, string) plugin.Permission {
	return func(_ context.Context, xuid string) plugin.Permission { return levels[xuid] }
}

const (
	opXUID     = "op-1"
	playerXUID = "player-1"
)

func newDamage(w *fakeDamageWatch) *WorldDamage {
	return NewWorldDamage(w, permissionsOf(map[string]plugin.Permission{opXUID: plugin.PermissionOperator, playerXUID: plugin.PermissionVisitor}))
}

func runDamage(t *testing.T, p *WorldDamage, args ...string) (string, error) {
	t.Helper()
	cmds := p.Commands()
	if len(cmds) != 1 || cmds[0].Name != "worlddamage" {
		t.Fatalf("Commands() = %+v, want exactly !worlddamage", cmds)
	}
	return cmds[0].Run(context.Background(), &plugin.Context{}, plugin.Invocation{ActorXUID: opXUID, ActorPermission: plugin.PermissionOperator, Args: args})
}

func TestWorldDamage_CommandIsOperatorOnly(t *testing.T) {
	p := newDamage(&fakeDamageWatch{})
	if got := p.Commands()[0].Permission; got != plugin.PermissionOperator {
		t.Fatalf("permission = %v, want operator: it names where the loss is and forgets it", got)
	}
}

func TestWorldDamage_NoticeForAPlayerIsThePlainWarning(t *testing.T) {
	p := newDamage(&fakeDamageWatch{world: lostWorld(), active: true})
	lines := p.NoticeFor(context.Background(), playerXUID)
	if len(lines) != 1 {
		t.Fatalf("lines = %q, want one plain warning", lines)
	}
	for _, leak := range []string{"6460", "4753", "2560", "!worlddamage", "chunk"} {
		if strings.Contains(lines[0], leak) {
			t.Errorf("player notice %q exposes %q, which is for operators", lines[0], leak)
		}
	}
	for _, want := range []string{"damaged", "roll"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("player notice %q does not say %q", lines[0], want)
		}
	}
}

func TestWorldDamage_NoticeForAnOperatorHasTheCountAndTheCoordinates(t *testing.T) {
	p := newDamage(&fakeDamageWatch{world: lostWorld(), active: true})
	got := strings.Join(p.NoticeFor(context.Background(), opXUID), "\n")
	for _, want := range []string{"6460", "overworld 4753", "nether 1413", "end 294", "2026-10-03 04:00 UTC", "2560 -16", "nether 2576 32", "!worlddamage clear"} {
		if !strings.Contains(got, want) {
			t.Errorf("operator notice does not contain %q:\n%s", want, got)
		}
	}
}

// Whoever the resolver cannot place is a visitor, and the notice follows it:
// a failed permission lookup must never leak coordinates.
func TestWorldDamage_NoticeFailsClosedForAnUnresolvedPlayer(t *testing.T) {
	p := newDamage(&fakeDamageWatch{world: lostWorld(), active: true})
	got := strings.Join(p.NoticeFor(context.Background(), "stranger"), "\n")
	if strings.Contains(got, "2560") || strings.Contains(got, "6460") {
		t.Fatalf("notice for an unresolved player leaks operator detail:\n%s", got)
	}
	if got == "" {
		t.Fatal("an unresolved player still has to be warned")
	}
}

func TestWorldDamage_NoNoticeWhileNothingIsRecorded(t *testing.T) {
	p := newDamage(&fakeDamageWatch{world: lostWorld(), active: false})
	for _, xuid := range []string{opXUID, playerXUID} {
		if lines := p.NoticeFor(context.Background(), xuid); len(lines) != 0 {
			t.Errorf("%s got %q, want silence once cleared", xuid, lines)
		}
	}
}

func TestWorldDamage_StatusListsTheSample(t *testing.T) {
	p := newDamage(&fakeDamageWatch{world: lostWorld(), active: true})
	got, err := runDamage(t, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"6460", "overworld 2560 -16", "nether 2576 32", "!worlddamage clear"} {
		if !strings.Contains(got, want) {
			t.Errorf("status does not contain %q: %s", want, got)
		}
	}
}

func TestWorldDamage_StatusWithNothingRecorded(t *testing.T) {
	got, err := runDamage(t, newDamage(&fakeDamageWatch{}))
	if err != nil || !strings.Contains(got, "No world damage") {
		t.Fatalf("reply = %q, err = %v", got, err)
	}
}

func TestWorldDamage_ClearStopsTheNotice(t *testing.T) {
	w := &fakeDamageWatch{world: lostWorld(), active: true}
	p := newDamage(w)
	got, err := runDamage(t, p, "clear")
	if err != nil {
		t.Fatal(err)
	}
	if w.cleared != 1 {
		t.Errorf("Clear called %d times, want 1", w.cleared)
	}
	if !strings.Contains(got, "6460") || !strings.Contains(got, "2026-10-03 04:00 UTC") {
		t.Errorf("reply = %q, want it to say which count was accepted", got)
	}
	if lines := p.NoticeFor(context.Background(), playerXUID); len(lines) != 0 {
		t.Errorf("player still told %q after the clear", lines)
	}
}

func TestWorldDamage_ClearIsCaseInsensitive(t *testing.T) {
	w := &fakeDamageWatch{world: lostWorld(), active: true}
	if _, err := runDamage(t, newDamage(w), "CLEAR"); err != nil || w.cleared != 1 {
		t.Fatalf("err = %v, cleared = %d", err, w.cleared)
	}
}

func TestWorldDamage_ClearWithNothingRecorded(t *testing.T) {
	got, err := runDamage(t, newDamage(&fakeDamageWatch{}), "clear")
	if err != nil || !strings.Contains(got, "No world damage") {
		t.Fatalf("reply = %q, err = %v", got, err)
	}
}

// The refusal is the operator's to hear: it means a newer count exists whose
// losses nobody has read, and the notice must stay on.
func TestWorldDamage_ClearRefusedForANewerCountSaysSo(t *testing.T) {
	newer := lostWorld()
	newer.LostTotal = 6500
	w := &fakeDamageWatch{world: lostWorld(), active: true, clearErr: mapclient.ErrCountReplaced, afterClear: &newer}
	p := newDamage(w)

	got, err := runDamage(t, p, "clear")
	if err != nil {
		t.Fatalf("err = %v, want the refusal said to the operator, not swallowed into a generic failure", err)
	}
	for _, want := range []string{"Not cleared", "newer", "6500", "!worlddamage"} {
		if !strings.Contains(got, want) {
			t.Errorf("reply does not contain %q: %s", want, got)
		}
	}
	if lines := p.NoticeFor(context.Background(), playerXUID); len(lines) == 0 {
		t.Error("notice stopped though nothing was cleared")
	}
}

func TestWorldDamage_ClearFailureIsAnError(t *testing.T) {
	w := &fakeDamageWatch{world: lostWorld(), active: true, clearErr: errors.New("map: acknowledge world: status 500")}
	if _, err := runDamage(t, newDamage(w), "clear"); err == nil {
		t.Fatal("err = nil, want the failure so the dispatch is recorded as one")
	}
}

func TestWorldDamage_UnknownArgumentClearsNothing(t *testing.T) {
	for _, args := range [][]string{{"clea"}, {"clear", "now"}, {"forget"}} {
		w := &fakeDamageWatch{world: lostWorld(), active: true}
		got, err := runDamage(t, newDamage(w), args...)
		if err != nil || w.cleared != 0 || !strings.Contains(got, "!worlddamage clear") {
			t.Errorf("args %v: reply = %q, err = %v, cleared = %d; want usage and no clear", args, got, err, w.cleared)
		}
	}
}

func TestWorldDamage_ListsAtMostTheLinesAWhisperCanCarry(t *testing.T) {
	w := lostWorld()
	w.LostSample = nil
	for i := 0; i < 20; i++ {
		w.LostSample = append(w.LostSample, mapclient.Chunk{Dimension: "overworld", X: int64(i * 16), Z: -100000})
	}
	p := newDamage(&fakeDamageWatch{world: w, active: true})
	for _, line := range p.NoticeFor(context.Background(), opXUID) {
		if n := len([]rune(line)); n > 256 {
			t.Errorf("line is %d characters, want it short enough for one whisper: %s", n, line)
		}
	}
}
