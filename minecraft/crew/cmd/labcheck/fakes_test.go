package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
)

// fakeClock is virtual time: Sleep advances it and never waits.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
	// block makes Sleep wait on the context, to run a check into its timeout.
	block bool
	// onSleep runs after the clock has advanced.
	onSleep func()
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if c.block {
		<-ctx.Done()
		return ctx.Err()
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	hook := c.onSleep
	c.mu.Unlock()
	if hook != nil {
		hook()
	}
	return ctx.Err()
}

type fakeMob struct {
	typ    string
	pos    [3]float64
	born   time.Time
	tagged bool
	named  bool
	dead   bool
	// event is the spawn event the summon carried.
	event string
}

// fakeLab is a console that behaves like the lab server just enough for the
// checks: players with poses, summoned mobs that can be tagged, moved and
// killed, and the replies Bedrock gives.
type fakeLab struct {
	clk *fakeClock

	online string
	units  map[string]*pose

	// tp is "apply" (the default), "ignore" (accepted and does nothing) or
	// "refuse".
	tp      string
	tpReply string

	summonReply string
	// noTag makes the tag command find nothing, as a mob that never appeared.
	noTag bool
	// path moves a mob type as time passes since its birth.
	path map[string]func(el time.Duration) [3]float64
	// phantomLife kills a phantom this long after it was summoned.
	lifespan map[string]time.Duration
	// stubborn is how many kill commands a mob survives.
	stubborn int
	// unreadable makes testfor answer with nothing readable.
	unreadable bool

	mobs []*fakeMob
	cmds []string
	// onCmd runs before each command is answered.
	onCmd func(cmd string)
}

func newLab() *fakeLab {
	return &fakeLab{
		clk:    newClock(),
		online: "Rusty, Slate",
		units: map[string]*pose{
			"Rusty": {X: 10.5, Y: 64, Z: 10.5, Yaw: 0},
			"Slate": {X: 30.5, Y: 64, Z: 10.5, Yaw: 180},
		},
	}
}

var (
	reSummonPlain = regexp.MustCompile(`^summon (\S+) "labcheck" (-?\d+) (-?\d+) (-?\d+)$`)
	reSummonEvent = regexp.MustCompile(`^summon (\S+) (-?\d+) (-?\d+) (-?\d+) 0 0 (\S+) "labcheck"$`)
	reTag         = regexp.MustCompile(`^execute positioned (-?\d+) (-?\d+) (-?\d+) run tag @e\[type=(\S+?),tag=!labcheck,c=1,r=4\] add labcheck$`)
	reQueryMob    = regexp.MustCompile(`^querytarget @e\[tag=labcheck,type=(\S+)\]$`)
	reQueryUnit   = regexp.MustCompile(`^querytarget @a\[name="([^"]+)"\]$`)
	reTP          = regexp.MustCompile(`^tp "([^"]+)" (\S+) (\S+) (\S+) (\S+) (\S+)$`)
)

func ints(m []string) [3]float64 {
	var p [3]float64
	for i := range p {
		v, _ := strconv.Atoi(m[i])
		p[i] = float64(v)
	}
	return p
}

func (l *fakeLab) alive(match func(*fakeMob) bool) []*fakeMob {
	var out []*fakeMob
	for _, m := range l.mobs {
		if l.lifespan[m.typ] > 0 && l.clk.Now().Sub(m.born) >= l.lifespan[m.typ] {
			m.dead = true
		}
		if !m.dead && match(m) {
			out = append(out, m)
		}
	}
	return out
}

func (m *fakeMob) at(l *fakeLab) [3]float64 {
	if f := l.path[m.typ]; f != nil {
		return f(l.clk.Now().Sub(m.born))
	}
	return m.pos
}

func queryJSON(ps ...pose) string {
	var parts []string
	for i, p := range ps {
		parts = append(parts, fmt.Sprintf(`{"dimension":0,"id":"%d","position":{"x":%v,"y":%v,"z":%v},"uniqueId":"-%d","yRot":%v}`, i, p.X, p.Y, p.Z, i, p.Yaw))
	}
	return "[2026-10-30 12:00:00:000 INFO] [" + strings.Join(parts, ",") + "]"
}

func (l *fakeLab) Exec(cmd string) (string, error) {
	l.cmds = append(l.cmds, cmd)
	if l.onCmd != nil {
		l.onCmd(cmd)
	}
	switch {
	case cmd == "list":
		n := len(strings.Split(l.online, ","))
		if strings.TrimSpace(l.online) == "" {
			n = 0
		}
		return fmt.Sprintf("There are %d/10 players online:\n%s", n, l.online), nil
	case reQueryUnit.MatchString(cmd):
		name := reQueryUnit.FindStringSubmatch(cmd)[1]
		p, ok := l.units[name]
		if !ok {
			return "No targets matched selector", nil
		}
		return queryJSON(*p), nil
	case reTP.MatchString(cmd):
		return l.teleport(reTP.FindStringSubmatch(cmd)), nil
	case reSummonPlain.MatchString(cmd):
		m := reSummonPlain.FindStringSubmatch(cmd)
		return l.summon(m[1], "", ints(m[2:5]))
	case reSummonEvent.MatchString(cmd):
		m := reSummonEvent.FindStringSubmatch(cmd)
		return l.summon(m[1], m[5], ints(m[2:5]))
	case reTag.MatchString(cmd):
		m := reTag.FindStringSubmatch(cmd)
		if l.noTag {
			return "No targets matched selector", nil
		}
		for _, mob := range l.mobs {
			if !mob.dead && !mob.tagged && mob.typ == m[4] {
				mob.tagged = true
				return "Added tag 'labcheck' to 1 entities", nil
			}
		}
		return "No targets matched selector", nil
	case strings.HasPrefix(cmd, "effect @e[tag=labcheck,type="):
		return "Gave effect", nil
	case reQueryMob.MatchString(cmd):
		typ := reQueryMob.FindStringSubmatch(cmd)[1]
		ms := l.alive(func(m *fakeMob) bool { return m.tagged && m.typ == typ })
		if len(ms) == 0 {
			return "No targets matched selector", nil
		}
		ps := make([]pose, len(ms))
		for i, m := range ms {
			at := m.at(l)
			ps[i] = pose{X: at[0], Y: at[1], Z: at[2]}
		}
		return queryJSON(ps...), nil
	case strings.HasPrefix(cmd, "kill @e["):
		byTag := strings.Contains(cmd, "tag=labcheck")
		for _, m := range l.alive(func(m *fakeMob) bool { return (byTag && m.tagged) || (!byTag && m.named) }) {
			if l.stubborn > 0 {
				l.stubborn--
				continue
			}
			m.dead = true
		}
		return "Killed", nil
	case strings.HasPrefix(cmd, "testfor @e["):
		if l.unreadable {
			return "", nil
		}
		byTag := strings.Contains(cmd, "tag=labcheck")
		ms := l.alive(func(m *fakeMob) bool { return (byTag && m.tagged) || (!byTag && m.named) })
		if len(ms) == 0 {
			return "No targets matched selector", nil
		}
		names := make([]string, len(ms))
		for i, m := range ms {
			names[i] = m.typ
		}
		return "Found " + strings.Join(names, ", "), nil
	}
	return "", fmt.Errorf("fake lab: unexpected command %q", cmd)
}

func (l *fakeLab) teleport(m []string) string {
	name := m[1]
	p, ok := l.units[name]
	if !ok {
		return "No targets matched selector"
	}
	switch l.tp {
	case "refuse":
		return l.tpReply
	case "ignore":
		return "Teleported " + name
	}
	p.X, _ = strconv.ParseFloat(m[2], 64)
	p.Y, _ = strconv.ParseFloat(m[3], 64)
	p.Z, _ = strconv.ParseFloat(m[4], 64)
	p.Yaw, _ = strconv.ParseFloat(m[5], 64)
	return "Teleported " + name + " to " + m[2] + ", " + m[3] + ", " + m[4]
}

func (l *fakeLab) summon(typ, event string, at [3]float64) (string, error) {
	if l.summonReply != "" {
		return l.summonReply, nil
	}
	l.mobs = append(l.mobs, &fakeMob{typ: typ, pos: at, born: l.clk.Now(), named: true, event: event})
	return "Object successfully summoned", nil
}

func (l *fakeLab) count(sub string) int {
	n := 0
	for _, c := range l.cmds {
		if strings.Contains(c, sub) {
			n++
		}
	}
	return n
}

func (l *fakeLab) survivors() int {
	return len(l.alive(func(m *fakeMob) bool { return m.tagged || m.named }))
}

// fakeDuel stands in for the duel runner; run is what the duel does to the
// world while it "runs".
type fakeDuel struct {
	run   func(ctx context.Context) ([]arena.DuelRound, error)
	calls int
}

func (f *fakeDuel) Run(ctx context.Context) ([]arena.DuelRound, error) {
	f.calls++
	return f.run(ctx)
}

// fakeUnits answers /v1/state from a script.
type fakeUnits struct {
	state func(unit string, call int) (UnitState, error)
	calls map[string]int
}

func (f *fakeUnits) State(_ context.Context, unit string) (UnitState, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[unit]++
	return f.state(unit, f.calls[unit])
}

func seen(h []life.HealthSeen, f []life.FlagSeen) *life.Sightings {
	return &life.Sightings{Health: h, Flags: f}
}

func u64(v uint64) *uint64 { return &v }

// testEnv wires a lab, its clock and the default options.
func testEnv(l *fakeLab, units UnitSource) *Env {
	return &Env{
		Console: l, Units: units, Clock: l.clk, Out: nil,
		Opts: Options{
			Units: []string{"Rusty", "Slate"}, CrewNames: arena.DefaultCrewNames,
			Window: 20 * time.Second, Poll: 2 * time.Second, Settle: 2 * time.Second,
			PhantomMove: 6, PhantomClose: 3, MaxCorrections: 10,
		},
	}
}

var testLimits = Limits{CheckTimeout: time.Minute, MaxWall: 10 * time.Minute}

// runOne runs one check through Run, which is how the command runs it.
func runOne(e *Env, name string) (Result, Report) {
	rep := Run(context.Background(), e, []string{name}, testLimits)
	if len(rep.Results) != 1 {
		panic(fmt.Sprintf("run %s: %d results, stopped %q", name, len(rep.Results), rep.Stopped))
	}
	return rep.Results[0], rep
}
