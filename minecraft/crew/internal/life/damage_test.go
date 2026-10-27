package life

import (
	"sync"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	selfRID = 7
	mobRID  = 50
)

func addMob(w *DamageWatcher, id uint64, hp float32) {
	w.Observe(&packet.AddActor{
		EntityRuntimeID: id,
		Attributes:      []protocol.AttributeValue{{Name: healthName, Value: hp, Max: hp}},
	})
}

func setHealth(w *DamageWatcher, id uint64, hp float32) {
	w.Observe(&packet.UpdateAttributes{
		EntityRuntimeID: id,
		Attributes:      []protocol.Attribute{{AttributeValue: protocol.AttributeValue{Name: healthName, Value: hp}}},
	})
}

func newWatcher() (*DamageWatcher, *DamageCounter) {
	c := &DamageCounter{}
	w := NewDamageWatcher(c)
	w.Spawn(selfRID)
	return w, c
}

func wantCount(t *testing.T, got, want DamageCount) {
	t.Helper()
	if got != want {
		t.Fatalf("count = %+v, want %+v", got, want)
	}
}

func TestDropAfterOwnSwingIsDamageDealt(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	w.Swing(100, mobRID, false)
	setHealth(w, mobRID, 16)
	w.Tick(101)
	wantCount(t, c.Summary().Fight, DamageCount{Dealt: 4})
}

func TestDropWithNoSwingIsUnattributed(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	setHealth(w, mobRID, 15)
	w.Tick(101)
	wantCount(t, c.Summary().Fight, DamageCount{Unattributed: 5})
}

func TestSwingAtAnotherTargetDoesNotClaimTheDrop(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	w.Swing(100, mobRID+1, false)
	setHealth(w, mobRID, 18)
	w.Tick(101)
	wantCount(t, c.Summary().Fight, DamageCount{Unattributed: 2})
}

func TestSwingExpiresAfterItsWindow(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	w.Swing(100, mobRID, false)
	w.Tick(100 + meleeHitWindow + 1)
	setHealth(w, mobRID, 18)
	w.Tick(100 + meleeHitWindow + 2)
	wantCount(t, c.Summary().Fight, DamageCount{Unattributed: 2})
}

func TestArrowSwingStaysOpenLongerThanAMeleeOne(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	w.Swing(100, mobRID, true)
	w.Tick(100 + meleeHitWindow + 5)
	setHealth(w, mobRID, 17)
	w.Tick(100 + meleeHitWindow + 6)
	wantCount(t, c.Summary().Fight, DamageCount{Dealt: 3})
}

func TestOneSwingClaimsOneDrop(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	addMob(w, mobRID+1, 20)
	w.Swing(100, mobRID, false)
	setHealth(w, mobRID, 16)
	w.Tick(101)
	setHealth(w, mobRID, 12)
	w.Tick(102)
	wantCount(t, c.Summary().Fight, DamageCount{Dealt: 4, Unattributed: 4})
}

func TestDropsInOneTickAreSummed(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	w.Swing(100, mobRID, false)
	setHealth(w, mobRID, 17)
	setHealth(w, mobRID, 14)
	w.Tick(101)
	wantCount(t, c.Summary().Fight, DamageCount{Dealt: 6})
}

func TestFirstUpdateWithoutABaselineIsNotADrop(t *testing.T) {
	w, c := newWatcher()
	w.Swing(100, mobRID, false)
	setHealth(w, mobRID, 8)
	w.Tick(101)
	wantCount(t, c.Summary().Total, DamageCount{})
	setHealth(w, mobRID, 4)
	w.Tick(102)
	if got := c.Summary().Total.Unattributed + c.Summary().Total.Dealt; got != 4 {
		t.Fatalf("damage after the baseline = %v, want 4", got)
	}
}

func TestHealingAndOtherAttributesAreNotDamage(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 10)
	setHealth(w, mobRID, 14)
	w.Observe(&packet.UpdateAttributes{
		EntityRuntimeID: mobRID,
		Attributes:      []protocol.Attribute{{AttributeValue: protocol.AttributeValue{Name: "minecraft:absorption", Value: 0}}},
	})
	w.Tick(101)
	wantCount(t, c.Summary().Total, DamageCount{})
}

func TestKillingBlowCountsTheHealthLeft(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 3)
	w.Swing(100, mobRID, false)
	setHealth(w, mobRID, 0)
	w.Tick(101)
	wantCount(t, c.Summary().Fight, DamageCount{Dealt: 3})
	if _, ok := w.health[mobRID]; ok {
		t.Fatal("a dead mob keeps its health entry")
	}
}

func TestOwnHealthDropIsDamageTaken(t *testing.T) {
	w, c := newWatcher()
	setHealth(w, selfRID, 20)
	setHealth(w, selfRID, 15)
	setHealth(w, selfRID, 18)
	setHealth(w, selfRID, 17)
	w.Tick(101)
	wantCount(t, c.Summary().Fight, DamageCount{Taken: 6})
}

func TestSpawnForgetsBaselinesSoARespawnIsNotABlow(t *testing.T) {
	w, c := newWatcher()
	setHealth(w, selfRID, 3)
	w.Spawn(selfRID)
	setHealth(w, selfRID, 20)
	w.Tick(101)
	wantCount(t, c.Summary().Total, DamageCount{})
}

func TestChangeDimensionForgetsMobBaselines(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	w.Observe(&packet.ChangeDimension{})
	setHealth(w, mobRID, 5)
	w.Tick(101)
	wantCount(t, c.Summary().Total, DamageCount{})
}

func TestFightStartResetsFightButKeepsTotals(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	setHealth(w, selfRID, 20)
	w.Swing(100, mobRID, false)
	setHealth(w, mobRID, 16)
	setHealth(w, selfRID, 18)
	w.Tick(101)
	wantCount(t, c.Summary().Fight, DamageCount{Dealt: 4, Taken: 2})

	w.FightStart()
	wantCount(t, c.Summary().Fight, DamageCount{})
	wantCount(t, c.Summary().Total, DamageCount{Dealt: 4, Taken: 2})

	setHealth(w, mobRID, 13)
	w.Tick(102)
	wantCount(t, c.Summary().Fight, DamageCount{Unattributed: 3})
}

func TestFightStartDropsUnreportedDamageAndOpenSwings(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 20)
	w.Swing(100, mobRID, false)
	setHealth(w, mobRID, 16)
	w.FightStart()
	w.Tick(101)
	wantCount(t, c.Summary().Total, DamageCount{})
}

func TestOpenSwingsAreCapped(t *testing.T) {
	w, _ := newWatcher()
	for i := uint64(0); i < 3*maxOpenSwings; i++ {
		w.Swing(100, mobRID+i, false)
	}
	if len(w.open) != maxOpenSwings {
		t.Fatalf("%d open swings, want %d", len(w.open), maxOpenSwings)
	}
	if w.open[0].target != mobRID+2*maxOpenSwings {
		t.Fatalf("oldest kept swing is at %d, want the newest %d", w.open[0].target, mobRID+2*maxOpenSwings)
	}
}

func TestTrackedHealthIsCapped(t *testing.T) {
	w, _ := newWatcher()
	for i := uint64(0); i < 3*maxTrackedHealth; i++ {
		addMob(w, 1000+i, 20)
	}
	if len(w.health) > maxTrackedHealth {
		t.Fatalf("%d tracked, cap %d", len(w.health), maxTrackedHealth)
	}
}

func TestCounterIsSafeForConcurrentUse(t *testing.T) {
	c := &DamageCounter{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				c.Taken(1)
				c.Dealt(1, 1)
				c.Unattributed(1, 1)
				c.FightStart()
			}
		}()
	}
	wg.Wait()
	wantCount(t, c.Summary().Total, DamageCount{Dealt: 800, Taken: 800, Unattributed: 800})
}

func TestWatcherIsSafeForConcurrentReadLoopAndTicks(t *testing.T) {
	w, c := newWatcher()
	addMob(w, mobRID, 1_000_000)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 500 {
			setHealth(w, mobRID, float32(1_000_000-i-1))
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 500 {
			w.Swing(uint64(i), mobRID, false)
			w.Tick(uint64(i))
		}
	}()
	wg.Wait()
	w.Tick(1000)
	s := c.Summary().Total
	if got := s.Dealt + s.Unattributed; got != 500 {
		t.Fatalf("damage seen = %v, want 500", got)
	}
}
