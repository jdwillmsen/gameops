package arena

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// bossDistance is how far past the squad start the boss is summoned.
const bossDistance = 10

// bossSeenMisses is how many testfor misses before the boss has ever been
// found are tolerated; a summon that did not land must not read as a kill.
const bossSeenMisses = 3

// bossConfirmWait separates the two testfor misses a kill needs. The console
// cannot tie a log line to the command that caused it, so one miss may be a
// late line from another command.
const bossConfirmWait = 2 * time.Second

// playerCheckEvery is how often a fight rereads the player list: a player who
// joins mid-fight must not stand next to a buffed boss for long.
const playerCheckEvery = 15 * time.Second

var mobPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(:[a-z][a-z0-9_]*)?$`)

// plainName is a boss name that needs no quotes inside a selector.
var plainName = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// effectTarget is the argument index of the target for each command an
// effect may use; event's first argument must be "entity".
var effectTarget = map[string]int{"effect": 1, "damage": 1, "event": 2}

// BossTest repeats one boss fight against the squad and reports each run. The
// director's own placement, rally-free death counting and ticking areas are
// reused. Besides placing and polling the squad, every command it sends
// targets only the boss; validate holds Effects to that.
type BossTest struct {
	Console Console
	Layout  Layout
	Mob     string
	Name    string
	// Effects are effect, damage or event commands whose only target is
	// the boss, as @e[name=<Name>...].
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
	// An effect selects the boss by name alone, so a player with that name
	// would be hit too; only crew can be online, so keep it off their names.
	if slices.Contains(b.CrewNames, b.Name) || slices.Contains(b.Warriors, b.Name) {
		return fmt.Errorf("arena: boss name %q is a crew name", b.Name)
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
		if err := b.checkEffect(e); err != nil {
			return err
		}
	}
	return nil
}

// checkEffect allows only effect, damage and event commands whose sole
// selector is the boss's. Anything else, execute included, could reach a
// player or the server itself.
func (b *BossTest) checkEffect(cmd string) error {
	args := splitArgs(cmd)
	at, ok := effectTarget[args[0]]
	if !ok {
		return fmt.Errorf("arena: effect %q: only effect, damage and event commands are allowed", cmd)
	}
	if args[0] == "event" && (len(args) < 2 || args[1] != "entity") {
		return fmt.Errorf("arena: effect %q: event must be event entity <boss> <event>", cmd)
	}
	if len(args) <= at+1 || !b.isBossSelector(args[at]) || strings.Count(cmd, "@") != 1 {
		return fmt.Errorf("arena: effect %q must target only the boss, as @e[name=%s]", cmd, b.Name)
	}
	return nil
}

// isBossSelector accepts @e[name=<Name>] with optional filters after the
// name. Further filters can only narrow the name match.
func (b *BossTest) isBossSelector(sel string) bool {
	forms := []string{"name=" + strconv.Quote(b.Name)}
	if plainName.MatchString(b.Name) {
		forms = append(forms, "name="+b.Name)
	}
	for _, f := range forms {
		rest, ok := strings.CutPrefix(sel, "@e["+f)
		if ok && (rest == "]" || strings.HasPrefix(rest, ",") && strings.HasSuffix(rest, "]")) {
			return true
		}
	}
	return false
}

// splitArgs splits a command on spaces outside selector brackets and quotes.
func splitArgs(cmd string) []string {
	var args []string
	var cur strings.Builder
	depth, quoted := 0, false
	for _, r := range strings.TrimSpace(cmd) {
		switch {
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		case unicode.IsSpace(r) && depth == 0:
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
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

// wait sleeps for d unless ctx ends first. An injected Sleep cannot be cut
// short, so ctx is checked once it returns.
func (b *BossTest) wait(ctx context.Context, d time.Duration) error {
	if b.Sleep != nil || d <= 0 {
		b.sleep(d)
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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
	if err := b.wait(ctx, b.Settle); err != nil {
		return nil, err
	}

	var done []BossRecord
	for run := 1; run <= runs; run++ {
		if err := ctx.Err(); err != nil {
			return done, err
		}
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
			b.sleep(bossConfirmWait)
			again, err := d.exec("testfor " + b.bossSelector())
			if err != nil {
				return BossRecord{}, err
			}
			if bossDown(again, b.Name) {
				outcome = BossKilled
			}
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
