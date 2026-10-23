package arena

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// bossDistance is how far past the squad start the boss is summoned.
const bossDistance = 10

// bossSeenMisses is how many testfor misses before the boss has ever been
// found are tolerated; a summon that did not land must not read as a kill.
const bossSeenMisses = 3

// playerCheckEvery is how often a fight rereads the player list: a player who
// joins mid-fight must not stand next to a buffed boss for long.
const playerCheckEvery = 15 * time.Second

var mobPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(:[a-z][a-z0-9_]*)?$`)

// BossTest repeats one boss fight against the squad and reports each run. The
// director's own placement, rally-free death counting and ticking areas are
// reused; nothing here targets a player.
type BossTest struct {
	Console   Console
	Layout    Layout
	Mob       string
	Name      string
	Effects   []string
	Runs      int
	Timeout   time.Duration
	Warriors  []string
	CrewNames []string
	Poll      time.Duration

	// Settle is how long the arena's chunks get to load after the ticking
	// areas are added; Bedrock refuses a summon in an unloaded chunk.
	Settle time.Duration
	// PlaceWait lets the teleports land before the boss appears.
	PlaceWait time.Duration

	// Record receives each finished run before the next one starts.
	Record func(BossRecord) error

	Out   io.Writer
	Now   func() time.Time
	Sleep func(time.Duration)
}

// SplitEffects splits a comma-separated command list. Commas inside a
// selector's brackets belong to the command, so only top-level ones split.
func SplitEffects(list string) []string {
	var cmds []string
	depth, start := 0, 0
	add := func(end int) {
		if cmd := strings.TrimSpace(list[start:end]); cmd != "" {
			cmds = append(cmds, cmd)
		}
	}
	for i, r := range list {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				add(i)
				start = i + 1
			}
		}
	}
	add(len(list))
	return cmds
}

func (b *BossTest) validate() error {
	if b.Console == nil {
		return fmt.Errorf("arena: no console")
	}
	if !mobPattern.MatchString(b.Mob) {
		return fmt.Errorf("arena: %q is not a mob id", b.Mob)
	}
	if err := validateWarriorName(b.Name); err != nil {
		return fmt.Errorf("arena: boss name: %w", err)
	}
	if len(b.Warriors) == 0 {
		return fmt.Errorf("arena: a boss test needs at least one warrior")
	}
	for _, w := range b.Warriors {
		if err := validateWarriorName(w); err != nil {
			return err
		}
	}
	if b.Timeout <= 0 {
		return fmt.Errorf("arena: timeout must be positive")
	}
	for _, e := range b.Effects {
		if strings.TrimSpace(e) == "" || strings.ContainsFunc(e, unicode.IsControl) {
			return fmt.Errorf("arena: effect command %q is empty or has a control character", e)
		}
	}
	return nil
}

func (b *BossTest) director() *Director {
	return &Director{
		Console:       b.Console,
		Layout:        b.Layout,
		Warriors:      b.Warriors,
		WarriorMisses: 3,
		PlaceSquad:    true,
		Out:           b.Out,
		Now:           b.Now,
		Sleep:         b.Sleep,
	}
}

func (b *BossTest) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *BossTest) sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	if b.Sleep != nil {
		b.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (b *BossTest) bossSelector() string {
	return fmt.Sprintf("@e[name=%q,type=!player]", b.Name)
}

func (b *BossTest) killCommand() string { return "kill " + b.bossSelector() }

func (b *BossTest) summonCommand() string {
	p := squadStartPoint(b.Layout, 0)
	// Bedrock reads the name before the coordinates; the other order summons
	// an unnamed mob that the kill check can never find.
	return fmt.Sprintf("summon %s %q %d %d %d", b.Mob, b.Name, p.X, p.Y, p.Z+bossDistance)
}

// Run checks the server is crew-only, loads the arena, and fights Runs times.
// The player list is reread once the arena has loaded, before every run and
// during each fight; a player outside the crew ends the test with a
// NotAloneError, and a fight cut short that way is recorded as BossAborted.
// It returns the finished runs even when a later one fails, so a refusal
// between runs keeps what was already measured. The boss is killed and the
// areas removed on every exit path.
func (b *BossTest) Run(ctx context.Context) ([]BossRecord, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	runs := max(b.Runs, 1)
	if err := CheckAlone(b.Console, b.CrewNames); err != nil {
		return nil, err
	}
	d := b.director()
	if err := d.addTickingAreas(); err != nil {
		return nil, err
	}
	defer d.removeTickingAreas()
	b.sleep(b.Settle)

	var done []BossRecord
	for run := 1; run <= runs; run++ {
		if err := CheckAlone(b.Console, b.CrewNames); err != nil {
			return done, err
		}
		rec, err := b.fight(ctx, d, run)
		var busy *NotAloneError
		if err != nil && !errors.As(err, &busy) {
			return done, err
		}
		done = append(done, rec)
		if b.Record != nil {
			if err := b.Record(rec); err != nil {
				return done, errors.Join(err, busyErr(busy))
			}
		}
		if busy != nil {
			return done, busy
		}
	}
	return done, nil
}

// busyErr keeps a nil *NotAloneError from becoming a non-nil error.
func busyErr(busy *NotAloneError) error {
	if busy == nil {
		return nil
	}
	return busy
}

func (b *BossTest) killBoss() { _, _ = b.Console.Exec(b.killCommand()) }

func (b *BossTest) logf(format string, args ...any) {
	if b.Out != nil {
		fmt.Fprintf(b.Out, format+"\n", args...)
	}
}

// bossDown reads one testfor reply. Only a reply that reports no targets and
// does not list the boss counts: teleport and kill replies carry the same miss
// text for other reasons.
func bossDown(reply, name string) bool {
	return !strings.Contains(reply, "Found "+name) && strings.Contains(reply, "No targets matched")
}

// fight returns a BossAborted record together with the NotAloneError when a
// player outside the crew comes online during it.
func (b *BossTest) fight(ctx context.Context, d *Director, run int) (BossRecord, error) {
	d.lastPlaced = nil
	b.killBoss()
	// The boss goes before the caller unloads its chunks, on every way out.
	defer b.killBoss()

	for i, name := range b.Warriors {
		if err := d.placeWarrior(name, i); err != nil {
			return BossRecord{}, err
		}
	}
	b.sleep(b.PlaceWait)
	states, _, err := d.warriorBaseline()
	if err != nil {
		return BossRecord{}, err
	}

	startTS := b.now()
	out, err := d.exec(b.summonCommand())
	if err != nil {
		return BossRecord{}, err
	}
	if strings.Contains(out, "Unable to summon") {
		return BossRecord{}, fmt.Errorf("arena: summon %s failed: %s", b.Mob, strings.TrimSpace(out))
	}
	for _, e := range b.Effects {
		if _, err := d.exec(e); err != nil {
			return BossRecord{}, err
		}
	}

	var res Result
	var busy *NotAloneError
	seen, misses := false, 0
	outcome := ""
	checked := b.now()
	for outcome == "" {
		if err := ctx.Err(); err != nil {
			return BossRecord{}, err
		}
		b.sleep(b.pollEvery())
		if b.now().Sub(checked) >= playerCheckEvery {
			checked = b.now()
			if err := CheckAlone(b.Console, b.CrewNames); errors.As(err, &busy) {
				outcome = BossAborted
				break
			} else if err != nil {
				return BossRecord{}, err
			}
		}
		wiped, err := d.checkWarriors(states, &res)
		if err != nil {
			return BossRecord{}, err
		}
		reply, err := d.exec("testfor " + b.bossSelector())
		if err != nil {
			return BossRecord{}, err
		}
		switch {
		case strings.Contains(reply, "Found "+b.Name):
			seen = true
		case bossDown(reply, b.Name):
			if !seen {
				if misses++; misses >= bossSeenMisses {
					return BossRecord{}, fmt.Errorf("arena: %s %q was never found after summoning", b.Mob, b.Name)
				}
				continue
			}
			outcome = BossKilled
		}
		switch {
		case outcome != "":
		case wiped:
			outcome = BossWipe
		case b.now().Sub(startTS) >= b.Timeout:
			outcome = BossTimeout
		}
	}

	rec := BossRecord{
		TS:      startTS.UTC().Format(time.RFC3339),
		Boss:    b.Mob,
		Effects: append([]string{}, b.Effects...),
		Outcome: outcome,
		Seconds: roundTenth(b.now().Sub(startTS).Seconds()),
		Deaths:  map[string]int{"total": res.WarriorDeaths},
		Run:     run,
	}
	for _, name := range b.Warriors {
		rec.Deaths[name] = states[name].deaths
	}
	b.logf("arena: run %d: %s after %.1fs, %d deaths", run, outcome, rec.Seconds, res.WarriorDeaths)
	return rec, busyErr(busy)
}

func (b *BossTest) pollEvery() time.Duration {
	if b.Poll > 0 {
		return b.Poll
	}
	return 3 * time.Second
}

func roundTenth(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
