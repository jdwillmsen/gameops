package arena

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Hard caps on a duel run. They bound the loop whatever the flags say.
const (
	MaxDuelRounds       = 20
	MinDuelRoundTimeout = 10 * time.Second
	MaxDuelRoundTimeout = 10 * time.Minute
	MaxDuelWall         = 2 * time.Hour

	// duelDeathMisses is how many polls in a row a duelist must be missing
	// before it counts as dead: Bedrock can drop a target from a selector for
	// a moment without it having died.
	duelDeathMisses = 3

	// duelEdge is how far past the floor a standing duelist can be before it
	// counts as out: a quick respawn at the world spawn leaves no down poll,
	// only a position far from the arena.
	duelEdge = 10

	// duelMinSpacing keeps the two lines far enough apart not to start
	// inside each other's reach.
	duelMinSpacing = 4
)

// Round outcomes of a duel.
const (
	DuelDecided = "decided"
	DuelDraw    = "draw"
	DuelTimeout = "timeout"
	DuelAborted = "aborted"
)

var (
	ErrDuelHalted    = errors.New("arena: duel stopped by the kill switch")
	ErrDuelWallClock = errors.New("arena: duel hit its wall-clock cap")
	// ErrDuelistGone ends a round whose duelist vanished from the world for
	// several polls. A death is seen as a respawn far from the floor, so a
	// unit that is simply not there has disconnected, and scoring that as a
	// loss would teach the trainer a profile that lost to a network fault.
	ErrDuelistGone = errors.New("arena: a duelist left the world")
)

// DuelRound is the one-line result of a round. Winner is the spawn side that
// won; WinnerTeam is the entrant (the Red or Blue list as given) that did.
type DuelRound struct {
	Round      int      `json:"round"`
	Outcome    string   `json:"outcome"`
	Winner     string   `json:"winner,omitempty"`
	WinnerTeam string   `json:"winner_team,omitempty"`
	Seconds    float64  `json:"seconds"`
	RedSpawn   []string `json:"red_spawn"`
	BlueSpawn  []string `json:"blue_spawn"`
}

// Duel runs bot-vs-bot rounds on the Colosseum floor. Red and Blue are the two
// entrants, equal in size; round 1 puts Red on the red spawn line, and every
// following round swaps the lines so a spawn bias cancels over an even count.
//
// Kit parity. Kits is one list, indexed by slot, and both entrants read the
// same list: slot i of Red and slot i of Blue get the same kit, in both
// orders of the swap, because a kit follows its slot rather than its side.
// Only deterministic catalog kits (<role>/t<1-6>) are accepted, never the
// roll, elite or random ones. Each round clears the duelists' inventories
// before the kit and resets health and effects after it, so nothing left over
// from the last round skews the next. With no Kits, units fight in whatever
// they carry and parity is the operator's job.
//
// Only the allow-listed units may duel, and only the duelists are touched:
// teleported, cleared, kitted, tagged. A non-crew player online refuses the
// run, exactly as a boss test does.
type Duel struct {
	Console Console
	Layout  Layout

	Red, Blue               []string
	RedProfile, BlueProfile string
	Kits                    []string

	Rounds       int
	RoundTimeout time.Duration
	// MaxWall caps the whole run, rounds and setup together.
	MaxWall time.Duration

	// CrewNames are the players allowed online. They are not the duelists:
	// a duel clears inventories and teleports, so who may be put through it
	// is a separate, narrower list that is never derived from this one.
	CrewNames []string
	// Duelists are the only names a side may hold.
	Duelists []string

	Policy TargetPolicy
	Sink   ScoreSink
	RunID  string

	// Spacing is how far each line stands from the floor centre.
	Spacing   int
	Poll      time.Duration
	Settle    time.Duration
	KitGap    time.Duration
	Countdown time.Duration

	// Halted is polled every poll; true ends the run. It is where a kill
	// switch plugs in.
	Halted func() bool

	Out   io.Writer
	Now   func() time.Time
	Sleep func(time.Duration)
}

func (d *Duel) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Duel) sleep(dur time.Duration) {
	if dur <= 0 {
		return
	}
	if d.Sleep != nil {
		d.Sleep(dur)
		return
	}
	time.Sleep(dur)
}

// wait sleeps unless ctx ends first. An injected Sleep cannot be cut short,
// so ctx is checked once it returns.
func (d *Duel) wait(ctx context.Context, dur time.Duration) error {
	if d.Sleep != nil || dur <= 0 {
		d.sleep(dur)
		return ctx.Err()
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (d *Duel) logf(format string, args ...any) {
	if d.Out != nil {
		fmt.Fprintf(d.Out, format+"\n", args...)
	}
}

func (d *Duel) pollEvery() time.Duration {
	if d.Poll > 0 {
		return d.Poll
	}
	return 3 * time.Second
}

// helper borrows the director's tested console routines: counting, position
// reads and ticking areas.
func (d *Duel) helper() *Director {
	return &Director{Console: d.Console, Layout: d.Layout, Out: d.Out, Now: d.Now, Sleep: d.Sleep}
}

func (d *Duel) validate() error {
	switch {
	case d.Console == nil:
		return fmt.Errorf("arena: no console")
	case d.Policy == nil:
		return fmt.Errorf("arena: a duel needs a target policy")
	case d.Sink == nil:
		return fmt.Errorf("arena: a duel needs a score sink")
	case len(d.Red) == 0 || len(d.Red) != len(d.Blue):
		return fmt.Errorf("arena: a duel needs equal, non-empty sides (red %d, blue %d)", len(d.Red), len(d.Blue))
	case len(d.Red) > len(squadOffsets):
		return fmt.Errorf("arena: at most %d warriors a side", len(squadOffsets))
	case d.Rounds < 1 || d.Rounds > MaxDuelRounds:
		return fmt.Errorf("arena: rounds must be 1..%d", MaxDuelRounds)
	case d.RoundTimeout < MinDuelRoundTimeout || d.RoundTimeout > MaxDuelRoundTimeout:
		return fmt.Errorf("arena: round timeout must be %s..%s", MinDuelRoundTimeout, MaxDuelRoundTimeout)
	case d.MaxWall <= 0 || d.MaxWall > MaxDuelWall:
		return fmt.Errorf("arena: wall-clock cap must be positive and at most %s", MaxDuelWall)
	}
	seen := map[string]bool{}
	for _, n := range append(slices.Clone(d.Red), d.Blue...) {
		if err := validateWarriorName(n); err != nil {
			return err
		}
		if seen[n] {
			return fmt.Errorf("arena: %q is on both sides or listed twice", n)
		}
		seen[n] = true
		if !slices.Contains(d.Duelists, n) {
			return fmt.Errorf("arena: %q is not an allowed duelist; a duel clears inventories and moves players", n)
		}
	}
	if err := ValidateWarriorKits(d.Red, d.Kits); err != nil {
		return err
	}
	if d.Layout.FloorRadius <= 0 {
		return fmt.Errorf("arena: the layout has no floor radius to place the sides on")
	}
	if d.Spacing < duelMinSpacing {
		return fmt.Errorf("arena: spacing must be at least %d", duelMinSpacing)
	}
	widest := 0
	for i := range d.Red {
		widest = max(widest, abs(squadOffsets[i%len(squadOffsets)]))
	}
	if reach := math.Hypot(float64(widest), float64(d.Spacing)); reach > float64(d.Layout.FloorRadius-2) {
		return fmt.Errorf("arena: spacing %d puts the sides %.0f from the centre, past the floor", d.Spacing, reach)
	}
	return nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// spawnPoint is the mirror pair: the blue point for slot i is the red point
// reflected through the floor centre, so both lines are the same distance
// from it and face each other.
func (d *Duel) spawnPoint(side string, slot int) Point {
	off := squadOffsets[slot%len(squadOffsets)]
	if side == SideRed {
		return Point{X: d.Layout.Center.X + off, Y: d.Layout.GroundY + 1, Z: d.Layout.Center.Z - d.Spacing}
	}
	return Point{X: d.Layout.Center.X - off, Y: d.Layout.GroundY + 1, Z: d.Layout.Center.Z + d.Spacing}
}

// placeCommand teleports facing the other line: yaw 0 looks south, 180 north.
func (d *Duel) placeCommand(name, side string, slot int) (string, error) {
	if err := validateWarriorName(name); err != nil {
		return "", err
	}
	p := d.spawnPoint(side, slot)
	yaw := 0
	if side == SideBlue {
		yaw = 180
	}
	return fmt.Sprintf("tp %q %d %d %d %d 0", name, p.X, p.Y, p.Z, yaw), nil
}

// Run checks the server is crew-only, loads the arena and fights Rounds
// rounds. It returns the finished rounds even when a later one fails. A round
// cut short after the start signal is returned with outcome aborted and
// aborted scorecards, which the trainer ignores. The target policy is
// disarmed and the ticking areas removed on every exit path.
func (d *Duel) Run(ctx context.Context) ([]DuelRound, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	start := d.now()
	if err := CheckAlone(d.Console, d.CrewNames); err != nil {
		return nil, err
	}
	h := d.helper()
	if err := h.addTickingAreas(); err != nil {
		return nil, err
	}
	defer h.removeTickingAreas()
	if err := d.wait(ctx, d.Settle); err != nil {
		return nil, err
	}

	var done []DuelRound
	for r := 1; r <= d.Rounds; r++ {
		if err := d.stopped(ctx, start); err != nil {
			return done, err
		}
		if err := CheckAlone(d.Console, d.CrewNames); err != nil {
			return done, err
		}
		rd, err := d.round(ctx, h, r, start)
		if rd.Round != 0 {
			done = append(done, rd)
		}
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// stopped says why the run must not go on, if it must not.
func (d *Duel) stopped(ctx context.Context, start time.Time) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case d.Halted != nil && d.Halted():
		return ErrDuelHalted
	case d.now().Sub(start) >= d.MaxWall:
		return ErrDuelWallClock
	}
	return nil
}

func (d *Duel) sides(round int) (red, blue duelSide) {
	red = duelSide{side: SideRed, profile: d.RedProfile, names: d.Red, dead: map[string]bool{}}
	blue = duelSide{side: SideBlue, profile: d.BlueProfile, names: d.Blue, dead: map[string]bool{}}
	if round%2 == 0 {
		red.profile, red.names, blue.profile, blue.names = blue.profile, blue.names, red.profile, red.names
	}
	return red, blue
}

// kitOf is the kit of a duelist by its slot in its own team, not by side.
func (d *Duel) kitOf(slot int) string {
	if len(d.Kits) == 0 {
		return ""
	}
	return d.Kits[slot]
}

// round sets one fight up, runs it and cleans up. A failure before the start
// signal measured nothing and returns a zero DuelRound.
func (d *Duel) round(ctx context.Context, h *Director, round int, runStart time.Time) (DuelRound, error) {
	red, blue := d.sides(round)
	roster := Roster{Red: red.names, Blue: blue.names}
	everyone := append(slices.Clone(red.names), blue.names...)
	defer d.endRound(everyone)

	for _, n := range everyone {
		cmd, err := warriorCommand(n)
		if err != nil {
			return DuelRound{}, err
		}
		up, err := h.countCmd(cmd, "duelist "+n)
		if err != nil {
			return DuelRound{}, err
		}
		if up == 0 {
			return DuelRound{}, fmt.Errorf("arena: duelist %s is not in the world", n)
		}
	}
	if err := d.setup(ctx, red, blue); err != nil {
		return DuelRound{}, err
	}
	if err := d.Policy.Arm(roster); err != nil {
		return DuelRound{}, err
	}
	if err := d.wait(ctx, d.Countdown); err != nil {
		return DuelRound{}, err
	}
	if err := CheckAlone(d.Console, d.CrewNames); err != nil {
		return DuelRound{}, err
	}
	if err := d.Policy.Go(); err != nil {
		return DuelRound{}, err
	}
	startTS := d.now()
	d.logf("arena: duel round %d: %v (red) against %v (blue)", round, red.names, blue.names)

	units := map[string]*duelUnit{}
	for _, n := range everyone {
		units[n] = &duelUnit{}
	}
	finish := func(outcome, winner string, err error) (DuelRound, error) {
		secs := roundTenth(d.now().Sub(startTS).Seconds())
		// A draw and a timeout both have no winner, and the trainer's
		// outcome set has no draw, so both score as a timeout.
		cardOutcome := func(self duelSide) string {
			switch {
			case outcome == DuelAborted:
				return tactics.OutcomeAborted
			case outcome != DuelDecided:
				return tactics.OutcomeTimeout
			case self.side == winner:
				return tactics.OutcomeWin
			}
			return tactics.OutcomeLoss
		}
		rd := DuelRound{
			Round: round, Outcome: outcome, Winner: winner, Seconds: secs,
			RedSpawn: red.names, BlueSpawn: blue.names,
		}
		if winner == SideRed {
			rd.WinnerTeam = d.teamOf(red.names)
		} else if winner == SideBlue {
			rd.WinnerTeam = d.teamOf(blue.names)
		}
		for _, pair := range [][2]duelSide{{red, blue}, {blue, red}} {
			card := pair[0].scorecard(d.RunID, startTS, secs, cardOutcome(pair[0]), pair[1])
			if rerr := d.Sink.Record(card); rerr != nil {
				err = errors.Join(err, rerr)
			}
		}
		d.logf("arena: duel round %d: %s after %.1fs", round, outcome, secs)
		return rd, err
	}

	checked := d.now()
	for {
		if err := d.wait(ctx, d.pollEvery()); err != nil {
			return finish(DuelAborted, "", err)
		}
		if err := d.stopped(ctx, runStart); err != nil {
			return finish(DuelAborted, "", err)
		}
		if d.now().Sub(checked) >= playerCheckEvery {
			checked = d.now()
			if err := CheckAlone(d.Console, d.CrewNames); err != nil {
				return finish(DuelAborted, "", err)
			}
		}
		if err := d.observe(h, units, &red, &blue); err != nil {
			return finish(DuelAborted, "", err)
		}
		redN, blueN := red.alive(), blue.alive()
		switch {
		case redN == 0 && blueN == 0:
			return finish(DuelDraw, "", nil)
		case blueN == 0:
			return finish(DuelDecided, SideRed, nil)
		case redN == 0:
			return finish(DuelDecided, SideBlue, nil)
		case d.now().Sub(startTS) >= d.RoundTimeout:
			return finish(DuelTimeout, "", nil)
		}
	}
}

func (d *Duel) teamOf(names []string) string {
	if slices.Equal(names, d.Red) {
		return "red"
	}
	return "blue"
}

// duelUnit is one duelist's standing. Death is final for the round: a unit
// that respawns at the world spawn does not rejoin. misses counts the polls
// in a row the unit was not in the world at all.
type duelUnit struct{ misses int }

// observe polls every living duelist once and latches the dead.
func (d *Duel) observe(h *Director, units map[string]*duelUnit, sides ...*duelSide) error {
	for _, s := range sides {
		for _, n := range s.names {
			if s.dead[n] {
				continue
			}
			cmd, err := warriorCommand(n)
			if err != nil {
				return err
			}
			up, err := h.countCmd(cmd, "duelist "+n)
			if err != nil {
				return err
			}
			u := units[n]
			if up > 0 {
				u.misses = 0
				if d.outOfArena(h, n) {
					s.dead[n] = true
				}
			} else if u.misses++; u.misses >= duelDeathMisses {
				return fmt.Errorf("%w: %s", ErrDuelistGone, n)
			}
			if s.dead[n] {
				d.logf("arena: duelist %s (%s) is out", n, s.side)
				if err := d.Policy.Eliminated(n); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// outOfArena reads a standing duelist's position. An unreadable position
// leaves it standing, as the director does.
func (d *Duel) outOfArena(h *Director, name string) bool {
	pos, err := h.rallyPosition(name)
	if err != nil {
		return false
	}
	dist := math.Hypot(pos.X-float64(d.Layout.Center.X), pos.Z-float64(d.Layout.Center.Z))
	return dist > float64(d.Layout.FloorRadius+duelEdge)
}

// setup puts both lines on their marks, then clears and kits the duelists
// slot by slot, red then blue, so the two sides' timing is the same.
func (d *Duel) setup(ctx context.Context, red, blue duelSide) error {
	for slot := range red.names {
		for _, s := range []duelSide{red, blue} {
			cmd, err := d.placeCommand(s.names[slot], s.side, slot)
			if err != nil {
				return err
			}
			if err := d.run(cmd, "place "+s.names[slot]); err != nil {
				return err
			}
		}
	}
	if len(d.Kits) > 0 {
		for _, s := range []duelSide{red, blue} {
			for _, n := range s.names {
				if err := d.run(fmt.Sprintf("clear %q", n), "clear "+n); err != nil {
					return err
				}
			}
		}
		for slot := range red.names {
			for _, s := range []duelSide{red, blue} {
				n := s.names[slot]
				kit := d.kitOf(d.slotInTeam(n))
				if err := d.run(fmt.Sprintf("execute as @a[name=%q] at @s run function kits/%s", n, kit), "kit "+n); err != nil {
					return err
				}
				d.logf("arena: kitted %s with %s", n, kit)
				if err := d.wait(ctx, d.KitGap); err != nil {
					return err
				}
			}
		}
	}
	for _, n := range append(slices.Clone(red.names), blue.names...) {
		if err := d.run(fmt.Sprintf("effect %q clear", n), "reset "+n); err != nil {
			return err
		}
		if err := d.run(fmt.Sprintf("effect %q instant_health 1 10 true", n), "heal "+n); err != nil {
			return err
		}
		if err := d.run(fmt.Sprintf("effect %q saturation 5 10 true", n), "feed "+n); err != nil {
			return err
		}
	}
	return nil
}

// slotInTeam is a duelist's index in its own entrant's list.
func (d *Duel) slotInTeam(name string) int {
	if i := slices.Index(d.Red, name); i >= 0 {
		return i
	}
	return slices.Index(d.Blue, name)
}

// run sends a setup command and refuses a reply that says the target was not
// there; a silent miss would leave one side unkitted and the duel unfair.
func (d *Duel) run(cmd, what string) error {
	out, err := d.Console.Exec(cmd)
	if err != nil {
		return err
	}
	if strings.Contains(out, "No targets matched") || strings.Contains(out, " not found.") {
		return fmt.Errorf("arena: %s failed: %s", what, strings.TrimSpace(out))
	}
	return nil
}

// endRound runs on every exit path of a round. It is best effort so the
// cleanup cannot hide why the round ended.
func (d *Duel) endRound(names []string) {
	if err := d.Policy.Disarm(); err != nil {
		d.logf("arena: disarm: %v", err)
	}
	for _, n := range names {
		_, _ = d.Console.Exec(fmt.Sprintf("effect %q clear", n))
	}
}
