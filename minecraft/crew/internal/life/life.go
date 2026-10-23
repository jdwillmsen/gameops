// Package life is what a unit does when no skill is steering it: it stands
// and looks about, takes short walks, and fights or flees from what attacks
// it. A task's skill is paused for a fight and resumed after. Everything is
// deterministic apart from the idle walk's dice, and nothing logs a position.
package life

import (
	"math/rand"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// Distances are in blocks and durations in ticks of 50 ms.
const (
	// A hostile mob this close is fought.
	threatRange = 6.0
	// What hurt the unit is looked for this far for a shooter, and this far
	// for anything else.
	shooterRange     = 16.0
	attributionRange = 5.0
	// A creeper this close is backed away from.
	creeperRange = 5.0
	// Horizontal reach of a hit, inside the server's own limit.
	meleeReach  = 2.8
	reachHeight = 2.0
	// A walk toward a mob stops this near its cell.
	chaseWithin = 1.8
	// Heights a mob can be at and still count as near: a floor away is no threat.
	threatHeight = 4.0

	// Bedrock gives a hit target invulnerability frames for about this long, so
	// hitting faster only wastes swings.
	attackEvery = 10
	lowHealth   = 6
	// How long a fight lingers after the last threat vanished, so a mob that
	// steps out of range and back does not stop and restart the task each time.
	graceTicks = 20
	// How long what hurt the unit stays a suspect.
	attackerTicks = 100
	stepBackTicks = 15
	// Retreating this long without shaking the threat means it cannot be
	// outrun, so the unit turns and fights.
	retreatLimit = 200
	// A fight with one mob this long is not going anywhere; leave it be.
	fightLimit  = 600
	ignoreTicks = 200
	chaseEvery  = 10
	fleeEvery   = 20
	fleeNodes   = 600
	maxIgnored  = 32
)

// Config selects what the unit does on its own.
type Config struct {
	// Wander makes an idle unit look about and take short walks.
	Wander bool
	// Radius bounds those walks around where the unit went idle.
	Radius int
	// Defend makes the unit fight hostile mobs and avoid players' blows.
	Defend bool
	// Eat makes the unit eat when hungry and food is on its hotbar.
	Eat bool
	// Role opts the unit into the warrior engine when it names a warrior
	// role — vanguard, striker or ranger. Any other value leaves the
	// reactive defence in charge.
	Role string
	// Kit, when set, is a chest the unit rearms from.
	Kit *Kit
}

type mode uint8

const (
	modeFight mode = iota
	modeRetreat
	modeStepBack
	// modeRegroup is the warrior's heal fallback: walk to the leader.
	modeRegroup
)

// Life is driven by the runner once per tick, and fed every packet by the
// read loop.
type Life struct {
	cfg   Config
	world path.Blocks
	self  func() belief.Snapshot
	log   skill.Logger
	rng   *rand.Rand
	ents  *belief.Entities

	// body is the world as the unit's body meets it, which has water to swim in.
	body act.Solid

	// mu guards what the read loop writes: the hurt marker and the health the
	// last attribute update reported, and the feet the last tick stood on,
	// where the read loop looks for what dealt a lethal blow.
	mu         sync.Mutex
	selfID     uint64
	hurt       bool
	health     float32
	healthSeen bool
	dead       bool
	feet       mgl32.Vec3
	feetKnown  bool

	// The rest belongs to the stepping goroutine.
	tick uint64

	attacker       uint64
	attackerUntil  uint64
	attackerPlayer bool
	ignore         map[uint64]uint64
	fightTicks     map[uint64]int

	engaged    bool
	engagedAt  uint64
	holdUntil  uint64
	target     belief.Entity
	mode       mode
	retreating int
	lastAttack uint64
	endReason  string

	chase     skill.Skill
	chaseGoal [3]int32
	chaseAt   uint64
	flee      skill.Skill
	fleeAt    uint64

	idle idleState
	swim swimState
	eat  eatState
	kit  kitState
	// warrior drives the squad's fighting when cfg.Role named a role, else
	// it is nil and the reactive defence decides everything.
	warrior *warriorState
	// tel counts a warrior's swings, hits, damage taken and deaths.
	tel telemetry
	// makeKit replaces the withdraw a visit runs, for tests.
	makeKit func(weapon bool, food int) skill.Skill
}

// New builds a unit's life. The random source is the idle walk's only
// nondeterminism.
func New(cfg Config, world path.Blocks, self func() belief.Snapshot, log skill.Logger, rng *rand.Rand) *Life {
	if cfg.Radius < 1 {
		cfg.Radius = 1
	}
	l := &Life{cfg: cfg, world: world, body: path.NewSolid(world), self: self, log: log, rng: rng, ents: belief.NewEntities(), ignore: map[uint64]uint64{}, fightTicks: map[uint64]int{}}
	if role, ok := validRole(cfg.Role); ok {
		l.warrior = &warriorState{role: role}
	}
	return l
}

// Spawn starts a session for the unit with the runtime ID. It leaves the
// fight and walk state alone, which belongs to the goroutine that steps the
// life; Reset clears that.
func (l *Life) Spawn(runtimeID uint64) {
	l.ents.Spawn(runtimeID)
	l.tel.spawn()
	l.mu.Lock()
	l.selfID, l.hurt, l.healthSeen, l.dead, l.feetKnown = runtimeID, false, false, false, false
	l.mu.Unlock()
}

// Reset forgets the fight and the walk, as after a death or a lost session. Only
// the stepping goroutine may call it.
func (l *Life) Reset() {
	_, d := l.takeHurt(true)
	l.telemetryTick(d)
	l.tel.newLife()
	l.attacker, l.attackerUntil, l.attackerPlayer = 0, 0, false
	clear(l.ignore)
	clear(l.fightTicks)
	l.engaged, l.holdUntil, l.target = false, 0, belief.Entity{}
	l.retreating, l.endReason = 0, ""
	l.chase, l.flee = nil, nil
	l.idle, l.swim = idleState{}, swimState{}
	l.eat, l.kit = eatState{}, kitState{}
	if w := l.warrior; w != nil {
		w.reset()
	}
}

// End drops the session's entities. Like Spawn it is for the read loop.
func (l *Life) End() {
	l.ents.Reset()
	l.mu.Lock()
	l.selfID, l.hurt = 0, false
	l.mu.Unlock()
}

// Apply feeds the read loop's packets in: who is around, and whether the unit
// was hurt.
func (l *Life) Apply(pk packet.Packet) {
	l.ents.Apply(pk)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.warrior != nil {
		l.tel.observe(pk, l.selfID, l.dead)
	}
	switch p := pk.(type) {
	case *packet.ActorEvent:
		if p.EntityRuntimeID != l.selfID {
			return
		}
		switch {
		case p.EventType == packet.ActorEventDeath:
			l.diedLocked()
		case p.EventType == packet.ActorEventHurt && !l.dead:
			l.hurt = true
		}
	case *packet.UpdateAttributes:
		if p.EntityRuntimeID != l.selfID {
			return
		}
		for _, a := range p.Attributes {
			if a.Name != "minecraft:health" {
				continue
			}
			if l.healthSeen && a.Value < l.health {
				l.hurt = true
			}
			l.health, l.healthSeen = a.Value, true
			switch {
			case a.Value <= 0:
				l.diedLocked()
			case l.dead:
				l.dead = false
			}
		}
	}
}

// diedLocked notes a death once, however many packets announce it. A dead
// unit is not stepped, so the lethal blow is attributed here, from where the
// unit last stood, before its killer can move off or, as a creeper does,
// vanish; and its hurt marker is dropped, or the next life would start by
// blaming whatever stands at the respawn point.
func (l *Life) diedLocked() {
	if l.dead {
		return
	}
	l.dead, l.hurt = true, false
	if l.warrior == nil {
		return
	}
	killer := ""
	if l.feetKnown {
		if e, player, ok := l.suspect(act.Pose{Feet: l.feet}); ok {
			killer = attackerName(e, player)
		}
	}
	l.tel.markDead(killer)
}

// takeHurt takes the hurt marker and, when drain is set, the telemetry the
// read loop gathered, under the one lock Apply holds: a packet landing between
// the two would be counted with no attribution and attributed with nothing to
// count.
func (l *Life) takeHurt(drain bool) (bool, drained) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.takeHurtLocked(drain)
}

// sense is takeHurt for a tick, which also records where the unit stands.
func (l *Life) sense(p act.Pose, drain bool) (bool, drained) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.feet, l.feetKnown = p.Feet, true
	return l.takeHurtLocked(drain)
}

func (l *Life) takeHurtLocked(drain bool) (bool, drained) {
	h := l.hurt
	l.hurt = false
	var d drained
	if drain && l.warrior != nil {
		d = l.tel.drain()
	}
	return h, d
}

// Cancel drops the idle walk, for a task that is about to take the unit over.
// The next idle spell anchors where the task leaves the unit.
func (l *Life) Cancel() { l.idle = idleState{} }

func (l *Life) logEvent(event string, f map[string]any) {
	if l.log != nil {
		l.log.Info(event, f)
	}
}

// still sends a tick that stands in place, facing where the unit already faces.
func (l *Life) still(a skill.Actuator) error {
	p := a.Pose()
	return a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch})
}
