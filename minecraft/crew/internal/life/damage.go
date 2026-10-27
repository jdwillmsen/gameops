package life

import (
	"sync"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	healthName = "minecraft:health"
	// Other entities seen with a health baseline; past it the table starts
	// over, costing only the first blow on each mob met afterwards.
	maxTrackedHealth = 256
)

// DamageSink receives the health the fight cost each side, as the unit's own
// client saw it. The Bedrock console reports no damage, so these counts are
// the only source for a scorecard's damage fields.
type DamageSink interface {
	// FightStart zeroes the per-fight counts and keeps the totals.
	FightStart()
	// Taken is health the unit itself lost.
	Taken(amount float64)
	// Dealt is health target lost soon after the unit swung or shot at it.
	Dealt(target uint64, amount float64)
	// Unattributed is health another entity lost with no swing of the unit's
	// to account for it: a teammate's blow, fire, a fall.
	Unattributed(target uint64, amount float64)
}

// DamageCount is health points, summed.
type DamageCount struct {
	Dealt        float64 `json:"dealt"`
	Taken        float64 `json:"taken"`
	Unattributed float64 `json:"unattributed"`
}

// DamageSummary is the totals since the unit started and the fight now under
// way, or the last one when none is. A scorecard for a round that spans
// several engagements wants the difference of two Totals, not Fight.
type DamageSummary struct {
	Total DamageCount `json:"total"`
	Fight DamageCount `json:"fight"`
}

// DamageCounter is the plain DamageSink: it adds up what it is told.
type DamageCounter struct {
	mu           sync.Mutex
	total, fight DamageCount
}

func (c *DamageCounter) FightStart() {
	c.mu.Lock()
	c.fight = DamageCount{}
	c.mu.Unlock()
}

func (c *DamageCounter) Taken(amount float64) {
	c.add(func(d *DamageCount) { d.Taken += amount })
}

func (c *DamageCounter) Dealt(_ uint64, amount float64) {
	c.add(func(d *DamageCount) { d.Dealt += amount })
}

func (c *DamageCounter) Unattributed(_ uint64, amount float64) {
	c.add(func(d *DamageCount) { d.Unattributed += amount })
}

func (c *DamageCounter) add(f func(*DamageCount)) {
	c.mu.Lock()
	f(&c.total)
	f(&c.fight)
	c.mu.Unlock()
}

// Summary returns the totals and the current or last fight.
func (c *DamageCounter) Summary() DamageSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	return DamageSummary{Total: c.total, Fight: c.fight}
}

// DamageWatcher turns the health attribute packets of the unit and of the
// entities around it into DamageSink calls. The read loop feeds Observe; the
// stepping goroutine feeds Swing and Tick, and every sink call happens in
// Tick so the sink needs no thought about threads.
//
// Packets name no attacker, so a drop on another entity counts as the unit's
// only while it has a swing at that entity still open. With several warriors
// on one target the unit's Dealt is an upper bound, as its hit count is.
// Absorption is a separate attribute that also expires on its own, so it is
// left out: an absorbing target's damage dealt reads low.
type DamageWatcher struct {
	sink DamageSink

	mu       sync.Mutex
	self     uint64
	health   map[uint64]float32
	selfSeen bool
	selfLast float32
	// Drops waiting for the next Tick, by entity; selfDrop is the unit's own.
	drops    map[uint64]float64
	selfDrop float64

	open []swing
}

// NewDamageWatcher reports to sink. It is not safe to share one sink between
// units whose fights are not the same.
func NewDamageWatcher(sink DamageSink) *DamageWatcher {
	return &DamageWatcher{sink: sink, health: map[uint64]float32{}, drops: map[uint64]float64{}}
}

// Spawn starts a session: no health is known yet, and a drop across a
// reconnect or respawn is not a blow.
func (w *DamageWatcher) Spawn(self uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.self = self
	w.selfSeen = false
	clear(w.health)
	clear(w.drops)
	w.selfDrop = 0
	w.open = w.open[:0]
}

// FightStart begins a fight: the sink's per-fight counts and the open swings
// start over, and damage seen but not yet reported belongs to no fight.
func (w *DamageWatcher) FightStart() {
	w.mu.Lock()
	w.open = w.open[:0]
	clear(w.drops)
	w.selfDrop = 0
	w.mu.Unlock()
	w.sink.FightStart()
}

// Observe is the read loop's half. It keeps sums only.
func (w *DamageWatcher) Observe(pk packet.Packet) {
	switch p := pk.(type) {
	case *packet.AddActor:
		for _, a := range p.Attributes {
			if a.Name == healthName {
				w.baseline(p.EntityRuntimeID, a.Value)
			}
		}
	case *packet.UpdateAttributes:
		for _, a := range p.Attributes {
			if a.Name == healthName {
				w.update(p.EntityRuntimeID, a.Value)
			}
		}
	case *packet.ChangeDimension:
		w.mu.Lock()
		clear(w.health)
		clear(w.drops)
		w.mu.Unlock()
	}
}

func (w *DamageWatcher) baseline(id uint64, v float32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if id == w.self {
		return
	}
	w.setLocked(id, v)
}

func (w *DamageWatcher) setLocked(id uint64, v float32) {
	if _, ok := w.health[id]; !ok && len(w.health) >= maxTrackedHealth {
		clear(w.health)
	}
	w.health[id] = v
}

func (w *DamageWatcher) update(id uint64, v float32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if id == w.self {
		if w.selfSeen && v < w.selfLast {
			w.selfDrop += float64(w.selfLast - v)
		}
		w.selfLast, w.selfSeen = v, true
		return
	}
	prev, known := w.health[id]
	w.setLocked(id, v)
	if known && v < prev {
		w.drops[id] += float64(prev - v)
	}
}

// Swing notes a melee swing or a bow release at target at tick.
func (w *DamageWatcher) Swing(tick, target uint64, ranged bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.open) >= maxOpenSwings {
		w.open = append(w.open[:0], w.open[1:]...)
	}
	w.open = append(w.open, swing{tick: tick, target: target, ranged: ranged})
}

// Tick reports what Observe gathered since the last one to the sink, crediting
// each drop on another entity to the oldest open swing at it.
func (w *DamageWatcher) Tick(tick uint64) {
	w.mu.Lock()
	kept := w.open[:0]
	for _, s := range w.open {
		if tick-s.tick <= swingWindow(s) {
			kept = append(kept, s)
		}
	}
	w.open = kept
	selfDrop := w.selfDrop
	w.selfDrop = 0
	type blow struct {
		target uint64
		amount float64
		own    bool
	}
	var blows []blow
	for id, amount := range w.drops {
		blows = append(blows, blow{id, amount, w.claimLocked(id)})
		if w.health[id] <= 0 {
			delete(w.health, id)
		}
	}
	clear(w.drops)
	w.mu.Unlock()

	if selfDrop > 0 {
		w.sink.Taken(selfDrop)
	}
	for _, b := range blows {
		if b.own {
			w.sink.Dealt(b.target, b.amount)
		} else {
			w.sink.Unattributed(b.target, b.amount)
		}
	}
}

// claimLocked consumes the oldest open swing at id, one per drop.
func (w *DamageWatcher) claimLocked(id uint64) bool {
	for i, s := range w.open {
		if s.target == id {
			w.open = append(w.open[:i], w.open[i+1:]...)
			return true
		}
	}
	return false
}
