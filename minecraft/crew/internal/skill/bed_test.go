package skill

import (
	"errors"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

var bedPos = protocol.BlockPos{7, 1, 7}

const (
	dayTicks   = 6000
	nightTicks = 16000
)

// bedRig is a flat world with a red bed at bedPos and a server that answers a
// tap on it as Bedrock does: a player SetSpawnPosition, and at night the
// sleeping flag.
type bedRig struct {
	*rig
	taps     int
	sleeping bool
	// monsters makes the server refuse sleep after setting the point.
	monsters bool
	// silent makes it ignore the tap altogether.
	silent  bool
	wakeAt  int
	stopped int
}

func newBedRig(t *testing.T, worldTime int64) *bedRig {
	t.Helper()
	w := flatWorld(12)
	w[bedPos] = "red_bed"
	br := &bedRig{rig: newRig(t, w, feetAt(1, 1, 2))}
	br.tracker.SetClock(worldTime, false, int32(worldTime))
	br.onBed = func(u *protocol.UseItemTransactionData) {
		br.taps++
		if hash, _ := br.world.BlockHash(u.BlockPosition); u.BlockRuntimeID != hash || br.silent {
			return
		}
		br.send(&packet.SetSpawnPosition{SpawnType: packet.SpawnTypePlayer, Position: u.BlockPosition})
		if worldTime == dayTicks || br.monsters {
			return
		}
		br.setSleeping(true)
		br.wakeAt = br.ticks + 120
	}
	br.onTick = func() {
		if br.sleeping && br.ticks >= br.wakeAt {
			br.setSleeping(false)
		}
	}
	br.onWrite = func(pk packet.Packet) bool {
		if p, ok := pk.(*packet.PlayerAction); ok && p.ActionType == protocol.PlayerActionStopSleeping {
			br.stopped++
		}
		return false
	}
	return br
}

func (br *bedRig) setSleeping(on bool) {
	br.sleeping = on
	var v int64
	if on {
		v = 1 << (protocol.EntityDataFlagSleeping - 64)
	}
	br.send(&packet.SetActorData{EntityRuntimeID: 1, EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyFlagsTwo: v}})
}

func (br *bedRig) skill(log Logger) Skill {
	return NewUseBed(br.world, br.self, bedPos, WithLogger(log))
}

func TestUseBedWalksToTheBedAndSetsTheSpawnByDay(t *testing.T) {
	br := newBedRig(t, dayTicks)
	log := &capture{}
	st, err := br.run(br.skill(log), 600)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	s := br.self()
	if !s.SpawnKnown || s.SpawnPoint != [3]int32{7, 1, 7} || br.taps != 1 {
		t.Errorf("spawn %v known %v after %d taps", s.SpawnPoint, s.SpawnKnown, br.taps)
	}
	if s.Sleeping {
		t.Error("the unit sleeps by day")
	}
	near(t, br.rig, bedPos, 5)
	if len(log.lines) != 1 || log.lines[0].event != "respawn_point_set" {
		t.Fatalf("log = %+v", log.lines)
	}
	for k := range log.lines[0].fields {
		if k != "sleeping" {
			t.Errorf("logged field %q: no coordinates belong here", k)
		}
	}
}

func TestUseBedStaysInBedUntilTheServerWakesIt(t *testing.T) {
	br := newBedRig(t, nightTicks)
	sk := br.skill(nil)
	st, err := br.run(sk, 1000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if br.sleeping || br.self().Sleeping {
		t.Error("the skill ended while the unit still slept")
	}
	if br.ticks < br.wakeAt {
		t.Errorf("done at input %d, before the wake at %d", br.ticks, br.wakeAt)
	}
	if !br.self().SpawnKnown {
		t.Error("no respawn point after a night in bed")
	}
	if sk.(Progresser).Progress() != 1 {
		t.Error("progress is not 1 after the point was set")
	}
}

// With monsters near the server sets the point and refuses sleep, which
// still completes the visit.
func TestUseBedFinishesWhenMonstersForbidSleep(t *testing.T) {
	br := newBedRig(t, nightTicks)
	br.monsters = true
	st, err := br.run(br.skill(nil), 600)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if !br.self().SpawnKnown || br.self().Sleeping {
		t.Errorf("spawn known %v, sleeping %v", br.self().SpawnKnown, br.self().Sleeping)
	}
}

func TestUseBedFailsWithoutABedAtTheTarget(t *testing.T) {
	for name, block := range map[string]string{"air": "air", "chest": "chest", "bedrock": "bedrock"} {
		t.Run(name, func(t *testing.T) {
			br := newBedRig(t, dayTicks)
			br.world[bedPos] = block
			st, err := br.run(br.skill(nil), 600)
			if st != Failed || !errors.Is(err, errNoBed) {
				t.Fatalf("status %v, err %v", st, err)
			}
			if br.taps != 0 || br.opens != 0 {
				t.Errorf("clicked a %s: %d taps, %d opens", name, br.taps, br.opens)
			}
		})
	}
}

func TestUseBedWaitsForAnUnloadedChunkThenFails(t *testing.T) {
	br := newBedRig(t, dayTicks)
	delete(br.world, bedPos)
	st, err := br.run(br.skill(nil), 600)
	if st != Failed || !errors.Is(err, errBedUnloaded) {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestUseBedFailsWhenTheBedCannotBeReached(t *testing.T) {
	w := simWorld{}
	w.platform(0, 5, 0, 5)
	w.platform(10, 14, 0, 5)
	w[bedPos] = "air"
	bed := protocol.BlockPos{12, 1, 2}
	w[bed] = "red_bed"
	r := newRig(t, w, feetAt(1, 1, 2))
	r.onBed = func(*protocol.UseItemTransactionData) { t.Error("tapped a bed it never reached") }
	st, err := r.run(NewUseBed(w, r.self, bed), 2000)
	if st != Failed || err == nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestUseBedTapsTwiceThenFailsWhenTheServerStaysSilent(t *testing.T) {
	br := newBedRig(t, dayTicks)
	br.silent = true
	st, err := br.run(br.skill(nil), 1000)
	if st != Failed || !errors.Is(err, errBedNotSet) {
		t.Fatalf("status %v, err %v", st, err)
	}
	if br.taps != 2 {
		t.Errorf("taps = %d, want the tap and one re-send", br.taps)
	}
}

func TestUseBedAnAlreadyKnownSpawnIsNotAnAnswer(t *testing.T) {
	br := newBedRig(t, dayTicks)
	br.silent = true
	br.send(&packet.SetSpawnPosition{SpawnType: packet.SpawnTypePlayer, Position: bedPos})
	st, _ := br.run(br.skill(nil), 1000)
	if st != Failed {
		t.Errorf("status %v: a point from before the tap counted as its answer", st)
	}
}

func TestUseBedStopWakesASleepingUnit(t *testing.T) {
	br := newBedRig(t, nightTicks)
	br.wakeAt = 1 << 30
	sk := br.skill(nil)
	for i := 0; i < 400 && !br.self().Sleeping; i++ {
		if st, err := sk.Step(t.Context(), br); st != Running {
			t.Fatalf("status %v, err %v before sleeping", st, err)
		}
		br.wakeAt = 1 << 30
	}
	if !br.self().Sleeping {
		t.Fatal("the unit never went to sleep")
	}
	sk.(Stopper).Stop(br)
	br.pump()
	if br.stopped != 1 {
		t.Errorf("stop-sleeping sent %d times, want 1", br.stopped)
	}
}

// After a fight the visit starts over; a server that does not repeat the spawn
// announcement for a bed that is already the respawn point has not failed.
func TestUseBedResumedAfterTheSpawnWasSetIsNotAFailure(t *testing.T) {
	br := newBedRig(t, nightTicks)
	br.monsters = true
	sk := br.skill(nil)
	for i := 0; i < 400 && sk.(Progresser).Progress() == 0; i++ {
		if st, err := sk.Step(t.Context(), br); st != Running {
			t.Fatalf("status %v, err %v before the spawn was set", st, err)
		}
	}
	if sk.(Progresser).Progress() != 1 {
		t.Fatal("the spawn was never set")
	}
	sk.(Resumer).Resume()
	br.silent = true
	if st, err := br.run(sk, 1000); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}
