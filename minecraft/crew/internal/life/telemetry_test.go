package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

var zombieSide = mgl32.Vec3{22, 1, 20.5}

func (r *rig) hurtEntity(id uint64) {
	r.l.Apply(&packet.ActorEvent{EntityRuntimeID: id, EventType: packet.ActorEventHurt})
}

// events is the logged fields of one event, oldest first.
func (r *rig) events(event string) []map[string]any {
	var out []map[string]any
	for _, l := range r.log.lines {
		if l.event == event {
			out = append(out, l.fields)
		}
	}
	return out
}

func counterValue(t *testing.T, c prometheus.Collector) float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 1)
	c.Collect(ch)
	var m dto.Metric
	if err := (<-ch).Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

// meleeRig is a Vanguard with a zombie in reach and one swing already sent.
func meleeRig(t *testing.T) *rig {
	t.Helper()
	r := newWarrior(t, "vanguard", start)
	r.give(0, 303, 1)
	spawnZombie(r, 50, zombieSide)
	r.step()
	if n := len(r.out.attacks()); n != 1 {
		t.Fatalf("%d swings on the first tick, want 1", n)
	}
	return r
}

func TestHitLandedFollowsASwingAtTheTargetsHurtAnimation(t *testing.T) {
	hits0 := counterValue(t, metricHits)
	swings0 := counterValue(t, metricSwings)
	r := meleeRig(t)
	if n := len(r.events("hit_landed")); n != 0 {
		t.Fatalf("hit_landed before the target was hurt: %v", r.events("hit_landed"))
	}

	r.hurtEntity(50)
	r.step()
	got := r.events("hit_landed")
	if len(got) != 1 || got[0]["target"] != "zombie" || got[0]["count"] != uint64(1) {
		t.Fatalf("hit_landed = %v, want one for the zombie with count 1", got)
	}

	// The animation is answered once: a second one with no swing between is
	// not another hit.
	r.hurtEntity(50)
	r.step()
	if n := len(r.events("hit_landed")); n != 1 {
		t.Fatalf("%d hit_landed after a second animation with no swing", n)
	}

	for i := 0; i < attackEvery; i++ {
		r.step()
	}
	r.hurtEntity(50)
	r.step()
	if got := r.events("hit_landed"); len(got) != 2 || got[1]["count"] != uint64(2) {
		t.Fatalf("hit_landed = %v, want a running count of 2", got)
	}

	if d := counterValue(t, metricHits) - hits0; d != 2 {
		t.Errorf("crew_hits_landed_total rose %v, want 2", d)
	}
	if d := counterValue(t, metricSwings) - swings0; d != float64(len(r.out.attacks())) {
		t.Errorf("crew_swings_total rose %v for %d attacks sent", d, len(r.out.attacks()))
	}
}

func TestHitLandedIgnoresOtherEntitiesAndLateAnimations(t *testing.T) {
	r := meleeRig(t)
	spawnZombie(r, 51, mgl32.Vec3{30, 1, 30})
	r.hurtEntity(51)
	r.step()
	if n := len(r.events("hit_landed")); n != 0 {
		t.Fatalf("a hurt animation on a mob never swung at counted: %v", r.events("hit_landed"))
	}

	// Keep the swing from being renewed so only its own window can answer.
	r.l.lastAttack = r.l.tick + 1000
	for i := 0; i < meleeHitWindow+2; i++ {
		r.step()
	}
	r.hurtEntity(50)
	r.step()
	if n := len(r.events("hit_landed")); n != 0 {
		t.Fatalf("a hurt animation long after the swing counted: %v", r.events("hit_landed"))
	}
}

func TestAShotCountsAsHitWhenTheTargetIsHurtWhileTheArrowFlies(t *testing.T) {
	r := newWarrior(t, "ranger", start)
	r.give(0, 303, 1)
	r.give(1, 361, 1)
	r.give(20, 362, 32)
	spawnZombie(r, 50, mgl32.Vec3{28.5, 1, 20.5})
	for i := 0; r.out.releases(1) == 0; i++ {
		if i > 100 {
			t.Fatal("the Ranger never released an arrow")
		}
		r.step()
	}
	for i := 0; i < 20; i++ {
		r.step()
	}
	r.hurtEntity(50)
	r.step()
	got := r.events("hit_landed")
	if len(got) != 1 || got[0]["target"] != "zombie" {
		t.Fatalf("hit_landed = %v, want the arrow's hit on the zombie", got)
	}
}

func TestDamageTakenNamesTheAttackerTheAmountAndTheHealthLeft(t *testing.T) {
	dmg0 := counterValue(t, metricDamage)
	r := meleeRig(t)
	r.setHealth(20)
	r.step()

	r.hurt()
	r.setHealth(14)
	r.step()
	got := r.events("damage_taken")
	if len(got) != 1 {
		t.Fatalf("damage_taken = %v, want one", got)
	}
	if got[0]["attacker"] != "zombie" || got[0]["amount"] != 6.0 || got[0]["health_after"] != 14.0 {
		t.Fatalf("damage_taken = %v, want zombie, 6 and 14 left", got[0])
	}
	if d := counterValue(t, metricDamage) - dmg0; d != 6 {
		t.Errorf("crew_damage_taken_total rose %v, want 6", d)
	}
	r.run(5)
	if n := len(r.events("damage_taken")); n != 1 {
		t.Fatalf("%d damage_taken after one blow", n)
	}
}

func TestADropArrivingAfterTheHurtAnimationIsOneBlow(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.step()
	r.hurt()
	r.step()
	r.setHealth(17)
	r.step()
	r.run(5)
	got := r.events("damage_taken")
	if len(got) != 1 || got[0]["amount"] != 3.0 {
		t.Fatalf("damage_taken = %v, want one blow of 3", got)
	}
}

func TestAHurtAnimationWithNoDropIsReportedWithoutAnAmount(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.step()
	r.hurt()
	r.run(damageJoin + 2)
	got := r.events("damage_taken")
	if len(got) != 1 {
		t.Fatalf("damage_taken = %v, want one", got)
	}
	if _, ok := got[0]["amount"]; ok {
		t.Errorf("a blow of unknown size reported an amount: %v", got[0])
	}
	if got[0]["attacker"] != "zombie" {
		t.Errorf("attacker = %v, want zombie", got[0]["attacker"])
	}
}

func TestDamageFromNothingNearbyHasAnUnknownAttacker(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.setHealth(20)
	r.step()
	r.hurt()
	r.setHealth(16)
	r.step()
	got := r.events("damage_taken")
	if len(got) != 1 || got[0]["attacker"] != "unknown" || got[0]["amount"] != 4.0 {
		t.Fatalf("damage_taken = %v, want an unknown attacker and 4 lost", got)
	}
}

func TestWarriorDiedNamesTheLastAttackerAndTheFightLength(t *testing.T) {
	deaths0 := counterValue(t, metricDeaths.WithLabelValues("zombie"))
	r := meleeRig(t)
	r.setHealth(20)
	r.run(30)
	r.hurt()
	r.setHealth(2)
	r.step()
	r.hurt()
	r.setHealth(0)
	r.step()

	got := r.events("warrior_died")
	if len(got) != 1 {
		t.Fatalf("warrior_died = %v, want one", got)
	}
	if got[0]["killer"] != "zombie" {
		t.Errorf("killer = %v, want zombie", got[0]["killer"])
	}
	if ticks, _ := got[0]["ticks"].(uint64); ticks < 30 {
		t.Errorf("ticks = %v, want the fight's length, at least 30", got[0]["ticks"])
	}
	if got[0]["deaths"] != uint64(1) || got[0]["swings"] == uint64(0) {
		t.Errorf("warrior_died = %v, want the fight's own numbers", got[0])
	}

	// Further packets and the reset that follows a death add nothing.
	r.setHealth(0)
	r.l.Reset()
	if n := len(r.events("warrior_died")); n != 1 {
		t.Fatalf("%d warrior_died for one death", n)
	}
	if d := counterValue(t, metricDeaths.WithLabelValues("zombie")) - deaths0; d != 1 {
		t.Errorf("crew_deaths_total{killer=zombie} rose %v, want 1", d)
	}
}

func TestADeathNoTickSawIsReportedWhenTheLifeIsReset(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.run(3)
	r.hurt()
	r.l.Apply(&packet.ActorEvent{EntityRuntimeID: selfID, EventType: packet.ActorEventDeath})
	r.l.Reset()
	if n := len(r.events("warrior_died")); n != 1 {
		t.Fatalf("warrior_died = %v, want one from the reset", r.events("warrior_died"))
	}
}

func TestADeathWithNothingToBlameIsAnUnknownKiller(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.setHealth(20)
	r.step()
	r.setHealth(0)
	r.step()
	got := r.events("warrior_died")
	if len(got) != 1 || got[0]["killer"] != "unknown" || got[0]["ticks"] != uint64(0) {
		t.Fatalf("warrior_died = %v, want an unknown killer and no fight", got)
	}
}

func TestDefendEndedCarriesTheFightSummary(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.hurtEntity(50)
	r.step()
	r.hurt()
	r.setHealth(15)
	r.step()
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	r.run(graceTicks + 5)

	ended := r.events("defend_ended")
	if len(ended) != 1 {
		t.Fatalf("defend_ended = %v, want one", ended)
	}
	f := ended[0]
	if f["swings"] != uint64(1) || f["hits"] != uint64(1) || f["damage_taken"] != 5.0 || f["deaths"] != uint64(0) {
		t.Fatalf("defend_ended = %v, want 1 swing, 1 hit, 5 damage and no deaths", f)
	}
	if f["reason"] != endGone {
		t.Errorf("reason = %v, want the existing %q", f["reason"], endGone)
	}

	spawnZombie(r, 52, zombieSide)
	r.step()
	if s, _ := r.l.Stats(); s.Current.Swings != 1 || s.Current.HitsLanded != 0 || s.Total.Swings != 2 || s.Total.HitsLanded != 1 {
		t.Fatalf("stats = %+v, want a fresh fight with the totals carried", s)
	}
}

func TestStatsReportTotalsAndTheCurrentFight(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.hurtEntity(50)
	r.hurt()
	r.setHealth(18)
	r.step()
	s, ok := r.l.Stats()
	if !ok {
		t.Fatal("a warrior reported no stats")
	}
	want := FightStats{Swings: 1, HitsLanded: 1, DamageTaken: 2}
	if s.Total != want || s.Current != want {
		t.Fatalf("stats = %+v, want both %+v", s, want)
	}
}

func TestOnlyAWarriorKeepsTelemetry(t *testing.T) {
	r := newRig(t, defaults, start)
	r.give(0, 303, 1)
	spawnZombie(r, 50, zombieSide)
	r.setHealth(20)
	r.step()
	r.hurtEntity(50)
	r.hurt()
	r.setHealth(10)
	r.step()
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	r.run(graceTicks + 5)

	for _, ev := range []string{"hit_landed", "damage_taken", "warrior_died"} {
		if n := len(r.events(ev)); n != 0 {
			t.Errorf("a reactive unit logged %s: %v", ev, r.events(ev))
		}
	}
	ended := r.events("defend_ended")
	if len(ended) != 1 || len(ended[0]) != 2 {
		t.Fatalf("defend_ended = %v, want only ticks and reason", ended)
	}
	if _, ok := r.l.Stats(); ok {
		t.Error("a reactive unit reported fight stats")
	}
}

func TestAHurtIsTakenWithItsTelemetryInOneStep(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.step()
	r.hurt()
	r.setHealth(14)
	hurt, d := r.l.takeHurt(true)
	if !hurt || !d.selfHurt || d.drop != 6 {
		t.Fatalf("takeHurt = %v, %+v, want the hurt and its drop of 6 together", hurt, d)
	}
}

func TestAHurtOnAQuietTickIsStillAttributed(t *testing.T) {
	r := meleeRig(t)
	// The hurt's telemetry half was drained a tick earlier, so this tick
	// has nothing else to report.
	r.l.mu.Lock()
	r.l.hurt = true
	r.l.mu.Unlock()
	r.step()
	if !r.l.tel.attributed || r.l.tel.attackerType != "zombie" || r.l.tel.attackerAt != r.l.tick {
		t.Fatalf("attribution = %v %q at %d, want the zombie at tick %d", r.l.tel.attributed, r.l.tel.attackerType, r.l.tel.attackerAt, r.l.tick)
	}
}

// die is the lethal blow as the read loop sees it, followed by the reset the
// runner does before any further tick: a dead unit is not stepped.
func (r *rig) die() {
	r.hurt()
	r.setHealth(0)
	r.l.Reset()
}

func TestAOneShotKillNamesWhatWasNearWhenHealthReachedZero(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.step()
	r.hurt()
	r.setHealth(14)
	r.step()
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	r.spawn(60, "minecraft:skeleton", mgl32.Vec3{20.5, 1, 31})
	r.run(3)

	r.die()
	got := r.events("warrior_died")
	if len(got) != 1 || got[0]["killer"] != "skeleton" {
		t.Fatalf("warrior_died = %v, want the skeleton's arrow, not the zombie's earlier blow", got)
	}
	dmg := r.events("damage_taken")
	if last := dmg[len(dmg)-1]; last["attacker"] != "skeleton" || last["amount"] != 14.0 {
		t.Fatalf("lethal damage_taken = %v, want 14 from the skeleton", last)
	}
}

func TestTheLethalHurtLogsNoHurtByAfterTheRespawn(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.step()
	r.die()
	n := len(r.events("hurt_by"))
	// The blow's own animation, late, while the unit lies dead.
	r.hurt()
	r.setHealth(20)
	r.step()
	if got := r.events("hurt_by"); len(got) != n {
		t.Fatalf("hurt_by after the respawn: %v", got[n:])
	}
}

func TestADeathInTheNextLifeIsNotBlamedOnTheLastOne(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.step()
	r.hurt()
	r.setHealth(14)
	r.step()
	r.die()

	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	r.setHealth(20)
	r.run(60)
	r.setHealth(0)
	r.l.Reset()
	got := r.events("warrior_died")
	if len(got) != 2 || got[1]["killer"] != "unknown" {
		t.Fatalf("warrior_died = %v, want the second death with nothing to blame", got)
	}
}

func TestASwingBeforeADeathIsNotAnsweredAfterTheRespawn(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.die()
	r.setHealth(20)
	r.hurtEntity(50)
	r.step()
	if got := r.events("hit_landed"); len(got) != 0 {
		t.Fatalf("hit_landed = %v, want none for a swing from the last life", got)
	}
}

func TestADeathPendingWhenTheSessionDropsIsStillCounted(t *testing.T) {
	r := meleeRig(t)
	r.setHealth(20)
	r.step()
	r.hurt()
	r.setHealth(0)
	// The connection drops and comes back before the stepping goroutine
	// resets the life.
	r.l.End()
	r.l.Spawn(selfID)
	r.l.Reset()
	got := r.events("warrior_died")
	if len(got) != 1 || got[0]["killer"] != "zombie" {
		t.Fatalf("warrior_died = %v, want the zombie's kill from the dropped session", got)
	}
}
