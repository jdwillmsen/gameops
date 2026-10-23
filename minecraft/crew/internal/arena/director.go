// Package arena drives a Colosseum wave run from the server console. It only
// emits commands the arena mob-buffs spike proved on Bedrock: no behaviour
// pack, no players targeted, lab only.
package arena

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// Outcome is how a run ended.
const (
	OutcomeBossDown = "boss_down"
	OutcomeCleared  = "cleared"
	OutcomeWipe     = "wipe"
	OutcomeTimeout  = "timeout"
)

// Result is the one-line tuning record a run prints.
type Result struct {
	Tier            int     `json:"tier"`
	Outcome         string  `json:"outcome"`
	WavesCleared    int     `json:"waves_cleared"`
	WavesTotal      int     `json:"waves_total"`
	WarriorDeaths   int     `json:"warrior_deaths"`
	DurationSeconds float64 `json:"duration_s"`
}

// Status is what the arena currently holds, for `arena.sh status`.
type Status struct {
	ArenaWave int             `json:"arena_wave"`
	Boss      int             `json:"boss"`
	Warriors  map[string]bool `json:"warriors,omitempty"`
}

// Director runs waves over a Console. Its clock and sleep are replaceable so
// the whole run can be tested offline.
type Director struct {
	Console Console
	Layout  Layout
	Book    *WaveBook

	Tier          int
	MaxLive       int
	Pause         time.Duration
	Poll          time.Duration
	WaveTimeout   time.Duration
	RunTimeout    time.Duration
	Warriors      []string
	OpenStartGate bool

	Out   io.Writer
	Now   func() time.Time
	Sleep func(time.Duration)
}

func (d *Director) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Director) sleep(dur time.Duration) {
	if dur <= 0 {
		return
	}
	if d.Sleep != nil {
		d.Sleep(dur)
		return
	}
	time.Sleep(dur)
}

func (d *Director) logf(format string, args ...any) {
	out := d.Out
	if out == nil {
		out = io.Discard
	}
	fmt.Fprintf(out, format+"\n", args...)
}

func (d *Director) exec(cmd string) (string, error) {
	return d.Console.Exec(cmd)
}

func (d *Director) prepare() error {
	if d.Console == nil {
		return fmt.Errorf("arena: no console")
	}
	if d.Book == nil {
		return fmt.Errorf("arena: no wave book")
	}
	if d.Tier < 0 || d.Tier > 5 {
		return fmt.Errorf("arena: tier %d is outside 0..5", d.Tier)
	}
	if d.MaxLive <= 0 {
		d.MaxLive = d.Book.MaxLive
	}
	if d.Pause <= 0 {
		d.Pause = d.Book.Pause.Duration
	}
	if d.WaveTimeout <= 0 {
		d.WaveTimeout = d.Book.WaveTimeout.Duration
	}
	if d.Poll <= 0 {
		d.Poll = 2 * time.Second
	}
	if d.RunTimeout <= 0 {
		d.RunTimeout = 30 * time.Minute
	}
	if d.Out == nil {
		d.Out = io.Discard
	}
	return nil
}

// Run executes the selected waves and returns the tuning record. A wave may
// be cleared, or the run may end on the boss, a squad wipe or a timeout.
func (d *Director) Run() (Result, error) {
	if err := d.prepare(); err != nil {
		return Result{}, err
	}
	waves := d.Book.UpTo(d.Tier)
	if len(waves) == 0 {
		return Result{}, fmt.Errorf("arena: tier %d leaves no waves", d.Tier)
	}
	result := Result{Tier: d.Tier, WavesTotal: len(waves)}
	start := d.now()

	if err := d.validateWaves(waves); err != nil {
		return result, err
	}
	if err := d.closeAllPens(); err != nil {
		return result, err
	}
	if d.OpenStartGate {
		if d.Layout.StartGate.Open == "" {
			return result, fmt.Errorf("arena: layout has no start gate command")
		}
		if _, err := d.exec(d.Layout.StartGate.Open); err != nil {
			return result, err
		}
	}
	alive, deaths, err := d.warriorBaseline()
	if err != nil {
		return result, err
	}
	result.WarriorDeaths = deaths

	for wi, wave := range waves {
		waveStart := d.now()
		if d.expired(start, waveStart) != "" {
			result.Outcome = OutcomeTimeout
			result.DurationSeconds = d.now().Sub(start).Seconds()
			return result, nil
		}
		if err := d.closeWavePens(wave); err != nil {
			return result, err
		}
		pending, err := d.waveSpawns(wave)
		if err != nil {
			return result, err
		}
		d.logf("arena: wave %d/%d %s (%d mobs, tier %d)", wi+1, len(waves), wave.Name, len(pending), wave.MaxTier())

		outcome, cleared, err := d.runWave(wave, pending, start, waveStart, alive, &result)
		if err != nil {
			return result, err
		}
		if outcome != "" {
			if outcome == OutcomeBossDown {
				result.WavesCleared++
			}
			result.Outcome = outcome
			result.DurationSeconds = d.now().Sub(start).Seconds()
			return result, nil
		}
		if cleared {
			result.WavesCleared++
			result.DurationSeconds = d.now().Sub(start).Seconds()
			d.logf("arena: wave %d cleared (%.1fs)", wi+1, result.DurationSeconds)
		}
		if wi < len(waves)-1 {
			d.sleep(d.pauseAfter(wave))
		}
	}
	result.Outcome = OutcomeCleared
	result.DurationSeconds = d.now().Sub(start).Seconds()
	return result, nil
}

// runWave spawns in batches up to the live cap and polls until the wave is
// empty, the boss falls, the squad is gone or a timeout fires.
func (d *Director) runWave(wave Wave, pending []spawn, runStart, waveStart time.Time, alive map[string]bool, result *Result) (outcome string, cleared bool, err error) {
	pens := map[int]bool{}
	for _, m := range wave.Mobs {
		if m.Pen > 0 {
			pens[m.Pen] = true
		}
	}
	opened := false
	bossSpawned := false
	bossSeen := false
	live := 0
	phases := append([]Phase(nil), wave.Phases...)
	phaseAt := 0

	for {
		if len(pending) > 0 && live < d.MaxLive {
			n := d.MaxLive - live
			if n > len(pending) {
				n = len(pending)
			}
			for _, sp := range pending[:n] {
				if err := d.spawnWaveMob(sp); err != nil {
					return "", false, err
				}
				if sp.Boss {
					bossSpawned = true
				}
			}
			pending = pending[n:]
			if !opened {
				if err := d.openPens(pens); err != nil {
					return "", false, err
				}
				opened = true
			}
		}

		if why := d.expired(runStart, waveStart); why != "" {
			d.logf("arena: %s timeout in %s", wave.Name, why)
			return OutcomeTimeout, false, nil
		}

		if wave.Boss {
			for phaseAt < len(phases) && phases[phaseAt].At.Duration <= d.now().Sub(waveStart) {
				p := phases[phaseAt]
				phaseAt++
				if p.Callout != "" {
					d.logf("arena: %s: %s", wave.Name, p.Callout)
				}
				for _, cmd := range p.Commands {
					if _, err := d.exec(cmd); err != nil {
						return "", false, err
					}
				}
			}
		}

		if bossSpawned {
			n, err := d.countCmd(bossCountCommand(), "boss")
			if err != nil {
				return "", false, err
			}
			if n > 0 {
				bossSeen = true
			}
			// Only trust the miss after a count that found the boss; a summon
			// that failed must not read as an instant win.
			if bossSeen && n == 0 {
				if _, err := d.exec(killWaveCommand()); err != nil {
					return "", false, err
				}
				d.logf("arena: %s is down", wave.Name)
				return OutcomeBossDown, false, nil
			}
		}

		wiped, err := d.checkWarriors(alive, result)
		if err != nil {
			return "", false, err
		}
		if wiped {
			if _, err := d.exec(killWaveCommand()); err != nil {
				return "", false, err
			}
			d.logf("arena: squad wiped in %s", wave.Name)
			return OutcomeWipe, false, nil
		}

		live, err = d.countCmd(countCommand(), "wave")
		if err != nil {
			return "", false, err
		}
		if live == 0 {
			if len(pending) > 0 {
				continue
			}
			return "", true, nil
		}
		d.sleep(d.Poll)
	}
}

// countCmd retries a count whose reply had not reached the log yet. The
// console is asynchronous, so one read can be empty even though the testfor
// ran; the next read carries its result and the parse succeeds.
func (d *Director) countCmd(cmd, what string) (int, error) {
	var lastErr error
	for range 3 {
		out, err := d.exec(cmd)
		if err != nil {
			return 0, err
		}
		n, err := ParseCount(out)
		if err == nil {
			return n, nil
		}
		lastErr = err
	}
	return 0, fmt.Errorf("arena: count %s: %w", what, lastErr)
}

// expired names the limit that has passed: the whole run or the current wave.
func (d *Director) expired(runStart, waveStart time.Time) string {
	now := d.now()
	if d.RunTimeout > 0 && now.Sub(runStart) > d.RunTimeout {
		return "run"
	}
	if d.WaveTimeout > 0 && now.Sub(waveStart) > d.WaveTimeout {
		return "wave"
	}
	return ""
}

func (d *Director) pauseAfter(w Wave) time.Duration {
	if w.Pause != nil {
		return w.Pause.Duration
	}
	return d.Pause
}

func (d *Director) closeAllPens() error {
	for _, p := range d.Layout.Pens {
		if _, err := d.exec(penCloseCommand(p)); err != nil {
			return err
		}
	}
	return nil
}

func (d *Director) closeWavePens(w Wave) error {
	for _, id := range wavePens(w) {
		p, ok := d.Layout.Pen(id)
		if !ok {
			return fmt.Errorf("arena: wave %q uses unknown pen %d", w.Name, id)
		}
		if _, err := d.exec(penCloseCommand(p)); err != nil {
			return err
		}
	}
	return nil
}

func (d *Director) openPens(pens map[int]bool) error {
	ids := make([]int, 0, len(pens))
	for id := range pens {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		p, ok := d.Layout.Pen(id)
		if !ok {
			return fmt.Errorf("arena: unknown pen %d", id)
		}
		if _, err := d.exec(p.Open); err != nil {
			return err
		}
	}
	return nil
}

func wavePens(w Wave) []int {
	seen := map[int]bool{}
	var ids []int
	for _, m := range w.Mobs {
		if m.Pen > 0 && !seen[m.Pen] {
			seen[m.Pen] = true
			ids = append(ids, m.Pen)
		}
	}
	sort.Ints(ids)
	return ids
}

// waveSpawns expands a wave's counts into one spawn per mob. Pen 0 means the
// dais, which is how a boss appears at the arena centre.
func (d *Director) waveSpawns(w Wave) ([]spawn, error) {
	var out []spawn
	for _, m := range w.Mobs {
		at := d.Layout.Boss
		if m.Pen > 0 {
			p, ok := d.Layout.Pen(m.Pen)
			if !ok {
				return nil, fmt.Errorf("arena: wave %q uses unknown pen %d", w.Name, m.Pen)
			}
			at = p.Spawn
		}
		for range m.Count {
			out = append(out, spawn{
				Type:    m.Mob,
				Name:    m.Name,
				Variant: m.Variant,
				Tier:    m.Tier,
				Boss:    w.Boss && isBossMob(m.Mob),
				Point:   at,
			})
		}
	}
	return out, nil
}

func (d *Director) spawnWaveMob(s spawn) error {
	recipe, ok := d.Book.Tiers[s.Tier]
	if !ok {
		return fmt.Errorf("arena: no recipe for tier %d", s.Tier)
	}
	for _, cmd := range s.commands(recipe) {
		if _, err := d.exec(cmd); err != nil {
			return err
		}
	}
	return nil
}

func (d *Director) validateWaves(waves []Wave) error {
	for _, w := range waves {
		for _, id := range wavePens(w) {
			if _, ok := d.Layout.Pen(id); !ok {
				return fmt.Errorf("arena: wave %q uses unknown pen %d", w.Name, id)
			}
		}
	}
	return nil
}

// warriorBaseline remembers who is standing before the first wave. Deaths are
// counted from this set, so a name that never appears is a setup error rather
// than a death.
func (d *Director) warriorBaseline() (map[string]bool, int, error) {
	alive := map[string]bool{}
	any := false
	for _, name := range d.Warriors {
		n, err := d.countCmd(warriorCommand(name), "warrior "+name)
		if err != nil {
			return nil, 0, err
		}
		alive[name] = n > 0
		if n > 0 {
			any = true
		}
	}
	if len(d.Warriors) > 0 && !any {
		return nil, 0, fmt.Errorf("arena: none of the warriors %v are in the world", d.Warriors)
	}
	return alive, 0, nil
}

// checkWarriors updates the death count and reports a full wipe.
func (d *Director) checkWarriors(alive map[string]bool, result *Result) (bool, error) {
	if len(alive) == 0 {
		return false, nil
	}
	standing := false
	for name, up := range alive {
		if !up {
			continue
		}
		n, err := d.countCmd(warriorCommand(name), "warrior "+name)
		if err != nil {
			return false, err
		}
		if n == 0 {
			alive[name] = false
			result.WarriorDeaths++
			d.logf("arena: warrior %s is down", name)
			continue
		}
		standing = true
	}
	return !standing, nil
}

// Reset clears only this director's mobs; warriors and players are untouched.
func (d *Director) Reset() error {
	if d.Console == nil {
		return fmt.Errorf("arena: no console")
	}
	_, err := d.exec(killWaveCommand())
	return err
}

// Status reports the live wave population and which warriors are standing.
func (d *Director) Status() (Status, error) {
	if d.Console == nil {
		return Status{}, fmt.Errorf("arena: no console")
	}
	var st Status
	n, err := d.countCmd(countCommand(), "wave")
	if err != nil {
		return st, err
	}
	st.ArenaWave = n
	n, err = d.countCmd(bossCountCommand(), "boss")
	if err != nil {
		return st, err
	}
	st.Boss = n
	if len(d.Warriors) > 0 {
		st.Warriors = map[string]bool{}
		for _, name := range d.Warriors {
			up, err := d.countCmd(warriorCommand(name), "warrior "+name)
			if err != nil {
				return st, err
			}
			st.Warriors[name] = up > 0
		}
	}
	return st, nil
}
