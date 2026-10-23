package life

import (
	"math"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

var (
	metricSwings = promauto.NewCounter(prometheus.CounterOpts{
		Name: "crew_swings_total",
		Help: "Melee swings and bow releases a warrior sent at a target.",
	})
	metricHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "crew_hits_landed_total",
		Help: "Swings and shots the target answered with a hurt animation or its death; an upper bound when several warriors fight one target.",
	})
	metricDamage = promauto.NewCounter(prometheus.CounterOpts{
		Name: "crew_damage_taken_total",
		Help: "Health points a warrior lost, as the health attribute reported.",
	})
	metricDeaths = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_deaths_total",
		Help: "Warrior deaths, by the entity type that last hurt it.",
	}, []string{"killer"})
)

const (
	// A melee hit shows as the target's hurt animation within a couple of
	// ticks of the swing, allowing for the round trip.
	meleeHitWindow = 8
	// An arrow is in flight for a while before it lands.
	shotHitWindow = 40
	// Swings waiting for their hurt animation; the oldest are dropped, as a
	// shot that missed never gets one.
	maxOpenSwings = 8
	// Hurt animations and deaths of other entities kept for the next tick: a
	// crowd hurting itself must not grow the sets without bound.
	maxSeenHurts = 64
	// A health drop and a hurt animation of the one blow can arrive ticks
	// apart; a lone hurt animation is given this long to be joined by a drop.
	// A second blow cannot land sooner, inside the invulnerability frames.
	damageJoin = attackEvery
	// An attribution this fresh belongs to the blow being reported.
	attributionFresh = 2

	killerUnknown = "unknown"
)

// FightStats is what a warrior's fights came to: swings sent, hits the
// target answered, health lost and deaths.
type FightStats struct {
	Swings      uint64  `json:"swings"`
	HitsLanded  uint64  `json:"hits_landed"`
	DamageTaken float64 `json:"damage_taken"`
	Deaths      uint64  `json:"deaths"`
}

// FightSummary is the unit's totals since it started and the fight now under
// way, or the last one when none is.
type FightSummary struct {
	Total   FightStats `json:"total"`
	Current FightStats `json:"current"`
}

// swing is one swing or shot waiting to be matched with a hurt animation.
type swing struct {
	tick   uint64
	target uint64
	typ    string
	ranged bool
}

// telemetry counts a warrior's fights. The read loop feeds it hurt animations
// and health changes; the stepping goroutine drains them once a tick and does
// the logging, so the log order follows the ticks.
type telemetry struct {
	mu sync.Mutex

	// Written by the read loop, drained by the stepping goroutine.
	selfHurt   bool
	drop       float32
	health     float32
	healthSeen bool
	died       bool
	// killer is what stood nearest when the unit died, or empty.
	killer    string
	seenHurts map[uint64]struct{}
	// Deaths of other entities, the killing blow's only sign for some.
	seenDeaths map[uint64]struct{}

	total, fight FightStats

	// The rest belongs to the stepping goroutine.
	open []swing
	// The last hit matched, so a death soon after its hurt is not a
	// second hit for the one blow.
	lastHit   uint64
	lastHitAt uint64

	hurtPending bool
	hurtSince   uint64
	// hurtBy is who the pending hurt was blamed on when it came.
	hurtBy     string
	lastDamage uint64
	damaged    bool

	attackerType string
	attackerAt   uint64
	attributed   bool
}

// spawn starts a session. What the last one left undrained is kept for the
// stepping goroutine, which reports it on its reset: a death the connection
// dropped with would otherwise never be counted.
func (t *telemetry) spawn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.healthSeen = false
	clear(t.seenHurts)
	clear(t.seenDeaths)
}

// newLife forgets the swings and blows of the life that ended. The clock
// stands still while the unit is dead, so an attribution kept would still
// look fresh to a death soon after the respawn.
func (t *telemetry) newLife() {
	t.open = t.open[:0]
	t.lastHit, t.lastHitAt = 0, 0
	t.hurtPending, t.damaged = false, false
	t.attackerType, t.attackerAt, t.attributed = "", 0, false
}

// observe is the read loop's half: it keeps only counters and flags, and the
// stepping goroutine reads them back. A dead unit's own hurts are not blows.
func (t *telemetry) observe(pk packet.Packet, selfID uint64, dead bool) {
	switch p := pk.(type) {
	case *packet.ActorEvent:
		if p.EventType != packet.ActorEventHurt && p.EventType != packet.ActorEventDeath {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		switch {
		case p.EntityRuntimeID == selfID:
			if !dead && p.EventType == packet.ActorEventHurt {
				t.selfHurt = true
			}
		case p.EventType == packet.ActorEventHurt:
			t.seenHurts = seen(t.seenHurts, p.EntityRuntimeID)
		default:
			t.seenDeaths = seen(t.seenDeaths, p.EntityRuntimeID)
		}
	case *packet.UpdateAttributes:
		if p.EntityRuntimeID != selfID {
			return
		}
		for _, a := range p.Attributes {
			if a.Name != "minecraft:health" {
				continue
			}
			t.mu.Lock()
			if t.healthSeen && a.Value < t.health {
				t.drop += t.health - a.Value
			}
			t.health, t.healthSeen = a.Value, true
			t.mu.Unlock()
		}
	}
}

// seen adds id to a set kept for the next tick, up to maxSeenHurts.
func seen(set map[uint64]struct{}, id uint64) map[uint64]struct{} {
	if set == nil {
		set = map[uint64]struct{}{}
	}
	if len(set) < maxSeenHurts {
		set[id] = struct{}{}
	}
	return set
}

// markDead notes the unit's death and what stood nearest when it came.
func (t *telemetry) markDead(killer string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.died, t.killer = true, killer
}

// drained is what the read loop gathered since the last tick.
type drained struct {
	selfHurt    bool
	drop        float32
	health      float32
	healthKnown bool
	died        bool
	killer      string
	hurts       []uint64
	deaths      []uint64
}

func (t *telemetry) drain() drained {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := drained{selfHurt: t.selfHurt, drop: t.drop, health: t.health, healthKnown: t.healthSeen, died: t.died, killer: t.killer}
	for id := range t.seenHurts {
		d.hurts = append(d.hurts, id)
	}
	for id := range t.seenDeaths {
		d.deaths = append(d.deaths, id)
	}
	clear(t.seenHurts)
	clear(t.seenDeaths)
	t.selfHurt, t.drop, t.died, t.killer = false, 0, false, ""
	return d
}

// empty says whether there is nothing to report, so a quiet tick returns
// early.
func (d drained) empty() bool {
	return !d.selfHurt && d.drop == 0 && !d.died && len(d.hurts) == 0 && len(d.deaths) == 0
}

// Stats returns the unit's totals and its current or last fight. The second
// result is false for a unit that does not fight as a warrior, which keeps no
// such numbers.
func (l *Life) Stats() (FightSummary, bool) {
	if l.warrior == nil {
		return FightSummary{}, false
	}
	l.tel.mu.Lock()
	defer l.tel.mu.Unlock()
	return FightSummary{Total: l.tel.total, Current: l.tel.fight}, true
}

// noteSwing records a melee swing or a bow release at e, to be matched with
// its hurt animation. Only a warrior keeps these counts.
func (l *Life) noteSwing(e belief.Entity, ranged bool) {
	if l.warrior == nil {
		return
	}
	t := &l.tel
	if len(t.open) >= maxOpenSwings {
		t.open = append(t.open[:0], t.open[1:]...)
	}
	t.open = append(t.open, swing{tick: l.tick, target: e.ID, typ: name(e.Type), ranged: ranged})
	t.mu.Lock()
	t.total.Swings++
	t.fight.Swings++
	t.mu.Unlock()
	metricSwings.Inc()
}

// fightStarted zeroes the per-fight numbers.
func (l *Life) fightStarted() {
	if l.warrior == nil {
		return
	}
	t := &l.tel
	t.mu.Lock()
	t.fight = FightStats{}
	t.mu.Unlock()
	t.open = t.open[:0]
}

// endFields adds the fight's summary to what defend_ended reports. A unit
// that is not a warrior keeps the event as it was.
func (l *Life) endFields(f map[string]any) map[string]any {
	if l.warrior == nil {
		return f
	}
	l.tel.mu.Lock()
	s := l.tel.fight
	l.tel.mu.Unlock()
	f["swings"], f["hits"], f["damage_taken"], f["deaths"] = s.Swings, s.HitsLanded, round2(s.DamageTaken), s.Deaths
	return f
}

// telemetryTick is the stepping goroutine's half, once a tick: it turns what
// the read loop saw, taken with the hurt marker, into log lines and counts.
// noteHurt has already run this tick, so a fresh attribution is the blow
// being reported.
func (l *Life) telemetryTick(d drained) {
	if l.warrior == nil {
		return
	}
	t := &l.tel
	l.pruneSwings()
	if d.empty() && !t.hurtPending {
		return
	}

	for _, id := range d.hurts {
		l.matchHit(id)
	}
	for _, id := range d.deaths {
		if t.lastHit != id || l.tick-t.lastHitAt > damageJoin {
			l.matchHit(id)
		}
	}
	if d.died && d.killer != "" {
		t.attackerType, t.attackerAt, t.attributed = d.killer, l.tick, true
	}
	l.reportDamage(d)
	if d.died {
		l.reportDeath()
	}
}

// pruneSwings drops swings too old to be answered.
func (l *Life) pruneSwings() {
	t := &l.tel
	kept := t.open[:0]
	for _, s := range t.open {
		if l.tick-s.tick <= swingWindow(s) {
			kept = append(kept, s)
		}
	}
	t.open = kept
}

func swingWindow(s swing) uint64 {
	if s.ranged {
		return shotHitWindow
	}
	return meleeHitWindow
}

// matchHit credits the oldest open swing at id with a hit. A swing is
// answered by one hurt animation or death at most, and the target is the one
// swung at, not whatever the unit's focus has moved on to since. The packets
// name no attacker, so a teammate's blow inside the window counts too: with
// several warriors on one target the hit count is an upper bound.
func (l *Life) matchHit(id uint64) {
	t := &l.tel
	for i, s := range t.open {
		if s.target != id {
			continue
		}
		t.open = append(t.open[:i], t.open[i+1:]...)
		t.lastHit, t.lastHitAt = id, l.tick
		t.mu.Lock()
		t.total.HitsLanded++
		t.fight.HitsLanded++
		n := t.fight.HitsLanded
		t.mu.Unlock()
		metricHits.Inc()
		l.logEvent("hit_landed", map[string]any{"target": s.typ, "count": n})
		return
	}
}

// reportDamage logs the blow the read loop saw. The health drop gives the
// amount; the hurt animation alone, without a drop soon after it, is a blow
// the amount of which is not known. A drop that joins a pending hurt is that
// hurt's blow, blamed as the hurt was: the drop's own attribution is ticks
// late. A lethal drop is blamed on the killer.
func (l *Life) reportDamage(d drained) {
	t := &l.tel
	switch {
	case d.drop > 0:
		by := l.blowAttacker()
		if t.hurtPending && !d.died {
			by = t.hurtBy
		}
		t.hurtPending = false
		t.damaged, t.lastDamage = true, l.tick
		l.damageEvent(float64(d.drop), d, by)
	case d.selfHurt && !t.hurtPending && (!t.damaged || l.tick-t.lastDamage > damageJoin):
		t.hurtPending, t.hurtSince, t.hurtBy = true, l.tick, l.blowAttacker()
	}
	if t.hurtPending && l.tick-t.hurtSince >= damageJoin {
		t.hurtPending = false
		l.damageEvent(0, d, t.hurtBy)
	}
}

func (l *Life) damageEvent(amount float64, d drained, by string) {
	t := &l.tel
	f := map[string]any{"attacker": by}
	if amount > 0 {
		f["amount"] = round2(amount)
	}
	if d.healthKnown {
		f["health_after"] = round2(float64(d.health))
	}
	if amount > 0 {
		t.mu.Lock()
		t.total.DamageTaken += amount
		t.fight.DamageTaken += amount
		t.mu.Unlock()
		metricDamage.Add(amount)
	}
	l.logEvent("damage_taken", f)
}

// blowAttacker is who hurt the unit just now, or unknown for a blow nothing
// nearby accounts for: a fall, fire, hunger.
func (l *Life) blowAttacker() string {
	t := &l.tel
	if t.attributed && l.tick-t.attackerAt <= attributionFresh {
		return t.attackerType
	}
	return killerUnknown
}

// reportDeath logs a death with the last attacker still on record. The fight
// ends with it, with no defend_ended to carry its summary, so the numbers
// ride here.
func (l *Life) reportDeath() {
	t := &l.tel
	killer := killerUnknown
	if t.attributed && l.tick-t.attackerAt <= attackerTicks {
		killer = t.attackerType
	}
	var ticks uint64
	if l.engaged {
		ticks = l.tick - l.engagedAt
	}
	t.mu.Lock()
	t.total.Deaths++
	t.fight.Deaths++
	s := t.fight
	t.mu.Unlock()
	metricDeaths.WithLabelValues(killer).Inc()
	l.logEvent("warrior_died", map[string]any{
		"killer": killer, "ticks": ticks,
		"swings": s.Swings, "hits": s.HitsLanded, "damage_taken": round2(s.DamageTaken), "deaths": s.Deaths,
	})
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
