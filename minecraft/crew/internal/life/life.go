// Package life is what a unit does when no skill is steering it: it stands
// and looks about, takes short walks, and fights or flees from what attacks
// it. A task's skill is paused for a fight and resumed after. Everything is
// deterministic apart from the idle walk's dice, and nothing logs a position.
package life

import (
	"math/rand"
	"sync"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/inventory"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
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
	// Vitals tunes the health and food reflex; its zero value keeps the
	// original thresholds.
	Vitals Vitals
	// Recover makes a unit that died walk back to where it fell and pick up
	// what it dropped.
	Recover bool
	// Inventory, when set, keeps the unit's pack in order: junk dropped,
	// surplus stored in a chest before the pack fills.
	Inventory *Inventory
	// Combat, when set, has a unit without a warrior role fight by the
	// combat baseline; nil leaves the plain reactive defence.
	Combat *CombatBaseline
	// NightShelter makes a unit spend the night safely: from dusk it goes to
	// the bed it set its respawn point on and sleeps, or with no bed walls
	// itself in where it stands, and at dawn it goes back to what it was
	// doing. It needs World.
	NightShelter bool
	// World is what the night skills read and click: the bed to tap and the
	// ground to wall in.
	World skill.ChestWorld
	// Home, when set, is where a unit goes on its own after a join or a death
	// has put it at the world spawn, before it resumes its work.
	Home *protocol.BlockPos
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
	// death outlives Reset, which the runner calls after a death: the next
	// life is what recovers from it. carried and dim are what the unit held and
	// where it was on its last tick, since by the time a death is announced the
	// inventory may already be gone.
	death   lastDeath
	carried int
	dim     int32

	// The rest belongs to the stepping goroutine.
	tick uint64

	attacker       uint64
	attackerUntil  uint64
	attackerPlayer bool
	ignore         map[uint64]uint64
	fightTicks     map[uint64]int
	// tacticErrAt is the tick each tactic's error was last logged at, so a
	// tactic that fails every tick says so once a minute and not 1200 times.
	tacticErrAt map[string]uint64

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
	rest restState
	// working is whether a task was running at the last Tending, for Tend.
	working bool
	kit     kitState
	rec     recoverState
	inv     invState
	// night is the night's bed or shelter, and home the walk back to base.
	night nightState
	home  homeState
	// warrior drives the squad's fighting when cfg.Role named a role, else
	// it is nil and the reactive defence decides everything.
	warrior *warriorState
	// profile is the warrior's tuning, swapped from outside the stepping
	// goroutine; nil is the default profile.
	profile atomic.Pointer[tactics.Profile]
	// pvp says which players the warrior may fight; nil is none, and is
	// swapped from outside the stepping goroutine like profile.
	pvp atomic.Pointer[PvPPolicy]
	// tel counts a warrior's swings, hits, damage taken and deaths.
	tel telemetry
	// dealt adds up the health other entities lost to the warrior's swings,
	// fed by watch from the read loop and the stepping goroutine.
	dealt DamageCounter
	watch *DamageWatcher
	// moves counts the sprint ticks and server corrections of the unit's
	// actuator, which the wiring points at it.
	moves *act.MoveCounters
	// makeKit replaces the withdraw a visit runs, for tests.
	makeKit func(weapon bool, food int) skill.Skill
	// makeRecover replaces the recovery walk, for tests.
	makeRecover func(lastDeath) skill.Skill
	// makeDeposit replaces one chest deposit of a trip, for tests.
	makeDeposit func(inventory.Move) skill.Skill
	// makeNight and makeHome replace the night visit and the walk home, for
	// tests.
	makeNight func(nightKind, protocol.BlockPos) skill.Skill
	makeHome  func() skill.Skill
}

// New builds a unit's life. The random source is the idle walk's only
// nondeterminism.
func New(cfg Config, world path.Blocks, self func() belief.Snapshot, log skill.Logger, rng *rand.Rand) *Life {
	if cfg.Radius < 1 {
		cfg.Radius = 1
	}
	l := &Life{cfg: cfg, world: world, body: path.NewSolid(world), self: self, log: log, rng: rng, ents: belief.NewEntities(), ignore: map[uint64]uint64{}, fightTicks: map[uint64]int{}, moves: &act.MoveCounters{}, home: newHomeState(cfg)}
	if role, ok := validRole(cfg.Role); ok {
		l.warrior = &warriorState{role: role, p: tactics.Defaults()}
		l.watch = NewDamageWatcher(&l.dealt)
	}
	return l
}

// Spawn starts a session for the unit with the runtime ID. It leaves the
// fight and walk state alone, which belongs to the goroutine that steps the
// life; Reset clears that.
func (l *Life) Spawn(runtimeID uint64) {
	l.ents.Spawn(runtimeID)
	l.tel.spawn()
	if l.watch != nil {
		l.watch.Spawn(runtimeID)
	}
	l.mu.Lock()
	l.selfID, l.hurt, l.healthSeen, l.dead, l.feetKnown = runtimeID, false, false, false, false
	// The drops of an earlier session are gone with its world.
	l.death = lastDeath{}
	l.mu.Unlock()
}

// Reset forgets the fight and the walk, as after a death or a lost session. Only
// the stepping goroutine may call it.
func (l *Life) Reset() {
	_, d := l.takeHurt(true)
	l.telemetryTick(d)
	l.tel.newLife()
	if l.watch != nil {
		l.watch.ClearSwings()
	}
	l.attacker, l.attackerUntil, l.attackerPlayer = 0, 0, false
	clear(l.ignore)
	clear(l.fightTicks)
	l.engaged, l.holdUntil, l.target = false, 0, belief.Entity{}
	l.retreating, l.endReason = 0, ""
	l.chase, l.flee = nil, nil
	l.idle, l.swim = idleState{}, swimState{}
	l.eat, l.kit, l.rec, l.rest = eatState{}, kitState{}, recoverState{}, restState{}
	l.inv = invState{}
	l.baselineReset()
	l.eat, l.kit, l.rec = eatState{}, kitState{}, recoverState{}
	// A new life starts at the world spawn or the join, wherever home is.
	l.night, l.home = nightState{}, newHomeState(l.cfg)
	if w := l.warrior; w != nil {
		w.reset()
	}
}

// EachEntity calls f for every entity the unit has been told about, quickly
// and without calling back into Life.
func (l *Life) EachEntity(f func(belief.Entity)) { l.ents.Each(f) }

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
		l.watch.Observe(pk)
		l.noteHealthSeen(pk)
		l.noteActorFlags(pk)
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
	l.noteDeathLocked()
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
