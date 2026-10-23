// Package arena drives a Colosseum wave run from the server console. It only
// emits commands the arena mob-buffs spike proved on Bedrock: no behaviour
// pack, no players targeted, lab only.
package arena

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// Outcome is how a run ended.
const (
	OutcomeBossDown = "boss_down"
	OutcomeCleared  = "cleared"
	OutcomeWipe     = "wipe"
	OutcomeTimeout  = "timeout"
	OutcomeError    = "error"
)

// Result is the one-line tuning record a run prints.
type Result struct {
	Tier            int     `json:"tier"`
	Outcome         string  `json:"outcome"`
	WavesCleared    int     `json:"waves_cleared"`
	WavesTotal      int     `json:"waves_total"`
	WarriorDeaths   int     `json:"warrior_deaths"`
	Rallies         int     `json:"rallies"`
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

	// lastPlaced throttles recovery teleports: a failed respawn detector must
	// not bounce a warrior every poll.
	lastPlaced map[string]time.Time

	Tier          int
	MaxLive       int
	Pause         time.Duration
	Poll          time.Duration
	WaveTimeout   time.Duration
	RunTimeout    time.Duration
	Warriors      []string
	WarriorMisses int
	OpenStartGate bool
	PlaceSquad    bool

	Out   io.Writer
	Now   func() time.Time
	Sleep func(time.Duration)

	// Resolved in prepare from the wave book, which keeps an explicit zero
	// meaningful: zero disables rallies instead of falling back to a default.
	rallyAfter time.Duration
	rallyEvery time.Duration
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

// repairFloor patches blast craters; a fill that finds no air is not an error.
func (d *Director) repairFloor() error {
	for _, cmd := range floorRepairCommands(d.Layout) {
		out, err := d.exec(cmd)
		if err != nil {
			return err
		}
		if line, bad := fillErrorLine(out); bad {
			return fmt.Errorf("arena: floor repair failed: %s", line)
		}
	}
	return nil
}

// fillErrorLine reports the first error the console logged for a fill. Bedrock
// logs a rejected fill as an ERROR line while send-command still exits zero,
// so the reply is the only place the director can see it. A replace that finds
// no air is expected after a quiet wave and is reported as "0 blocks filled"
// or "No blocks were filled", which are not failures.
func fillErrorLine(out string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "ERROR") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "0 blocks filled") ||
			strings.Contains(lower, "no blocks were filled") ||
			strings.Contains(lower, "no blocks filled") {
			continue
		}
		return strings.TrimSpace(line), true
	}
	return "", false
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
	for _, b := range floorRepairBoxes(d.Layout) {
		if n := b.volume(); n > maxFill {
			return fmt.Errorf("arena: floor_radius %d produces a %d-block fill, over Bedrock's %d-block limit",
				d.Layout.FloorRadius, n, maxFill)
		}
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
	if d.WarriorMisses <= 0 {
		d.WarriorMisses = 3
	}
	if d.RunTimeout <= 0 {
		d.RunTimeout = 30 * time.Minute
	}
	d.rallyAfter, d.rallyEvery = 20*time.Second, 25*time.Second
	if d.Book.RallyAfter != nil {
		d.rallyAfter = d.Book.RallyAfter.Duration
	}
	if d.Book.RallyEvery != nil {
		d.rallyEvery = d.Book.RallyEvery.Duration
	}
	if d.rallyAfter < 0 {
		return fmt.Errorf("arena: rally_after must not be negative")
	}
	if d.rallyEvery < 0 {
		return fmt.Errorf("arena: rally_every must not be negative")
	}
	if d.rallyAfter > 0 && d.rallyEvery <= 0 {
		return fmt.Errorf("arena: rally_every must be positive when rallies are on")
	}
	if d.rallyAfter > 0 && len(d.Warriors) == 0 && d.selectedWaveRallies() {
		return fmt.Errorf("arena: rallies are on but no warriors were given")
	}
	if d.Out == nil {
		d.Out = io.Discard
	}
	return nil
}

// selectedWaveRallies reports whether any wave this tier can reach would try to
// pull its survivors to a warrior.
func (d *Director) selectedWaveRallies() bool {
	for _, w := range d.Book.UpTo(d.Tier) {
		if w.rallies() {
			return true
		}
	}
	return false
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
	if err := d.addTickingAreas(); err != nil {
		return result, err
	}
	defer d.cleanupRun()
	if err := d.repairFloor(); err != nil {
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
	if d.PlaceSquad {
		if d.Layout.FloorRadius <= 0 {
			d.logf("arena: layout has no floor; skipping squad placement")
		} else {
			for i, name := range d.Warriors {
				if err := d.placeWarrior(name, i); err != nil {
					return result, err
				}
			}
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
			result.Outcome = OutcomeError
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
			if err := d.repairFloor(); err != nil {
				return result, err
			}
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
func (d *Director) runWave(wave Wave, pending []spawn, runStart, waveStart time.Time, alive map[string]*warriorState, result *Result) (outcome string, cleared bool, err error) {
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
	spawnedBatch := false
	waveStarted := false
	var rallyAt time.Time
	rallyRound := 0
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
			spawnedBatch = true
			if !waveStarted {
				waveStarted = true
				if d.rallyAfter > 0 && wave.rallies() {
					rallyAt = d.now().Add(d.rallyAfter)
				}
			}
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
				d.logf("arena: %s is down", wave.Name)
				return OutcomeBossDown, false, nil
			}
		}

		wiped, err := d.checkWarriors(alive, result)
		if err != nil {
			return "", false, err
		}
		if wiped {
			d.logf("arena: squad wiped in %s", wave.Name)
			return OutcomeWipe, false, nil
		}

		live, err = d.countCmd(countCommand(), "wave")
		if err != nil {
			return "", false, err
		}
		if live == 0 {
			// A successful summon that leaves nothing to count means the map
			// was not loaded or the entity never appeared; reading that as a
			// clear would skip the rest of the run.
			if spawnedBatch {
				return "", false, fmt.Errorf("arena: wave %q summoned mobs but counted none", wave.Name)
			}
			if len(pending) > 0 {
				continue
			}
			return "", true, nil
		}
		spawnedBatch = false
		if !rallyAt.IsZero() && !d.now().Before(rallyAt) {
			issued, err := d.rally(alive, rallyRound)
			if err != nil {
				return "", false, err
			}
			if issued {
				result.Rallies++
				rallyRound++
				d.logf("arena: rally %d mobs", live)
			}
			// Schedule from now, not from the missed mark, so a slow console
			// cannot fire a burst of catch-up teleports.
			rallyAt = d.now().Add(d.rallyEvery)
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

// rally anchors on the first warrior the squad list still sees standing, so a
// dead leader sends the mobs to the next one instead of a corpse.
func (d *Director) rally(states map[string]*warriorState, round int) (bool, error) {
	anchor, ok := d.rallyAnchor(states)
	if !ok {
		return false, nil
	}
	pos, err := d.rallyPosition(anchor)
	if err != nil {
		// Never guess an offset: an unread anchor may be at the floor edge.
		d.logf("arena: rally skipped: cannot read %s position: %v", anchor, err)
		return false, nil
	}
	dest, dx, dz, relative := d.rallyDestination(pos, round)
	if relative {
		cmd, err := rallyCommand(anchor, dx, dz)
		if err != nil {
			return false, err
		}
		if _, err := d.exec(cmd); err != nil {
			return false, err
		}
		return true, nil
	}
	if _, err := d.exec(rallyAbsoluteCommand(dest)); err != nil {
		return false, err
	}
	return true, nil
}

func (d *Director) rallyAnchor(states map[string]*warriorState) (string, bool) {
	for _, name := range d.Warriors {
		if st := states[name]; st != nil && st.live {
			return name, true
		}
	}
	return "", false
}

func (d *Director) rallyPosition(name string) (TargetPosition, error) {
	cmd, err := rallyQueryCommand(name)
	if err != nil {
		return TargetPosition{}, err
	}
	out, err := d.exec(cmd)
	if err != nil {
		return TargetPosition{}, err
	}
	return ParseTargetPosition(out)
}

// rallyDestination walks the ring from this round's place. An offset is only
// usable when its destination, measured from the layout centre rather than the
// anchor, stays inside the floor; otherwise the survivors move towards the
// centre instead of into the stands.
func (d *Director) rallyDestination(anchor TargetPosition, round int) (dest TargetPosition, dx, dz int, relative bool) {
	for i := range rallyOffsets {
		o := rallyOffsets[(round+i)%len(rallyOffsets)]
		p := TargetPosition{X: anchor.X + float64(o[0]), Y: anchor.Y, Z: anchor.Z + float64(o[1])}
		if d.onFloor(p) {
			return p, o[0], o[1], true
		}
	}
	return d.rallyFallback(anchor), 0, 0, false
}

func (d *Director) onFloor(p TargetPosition) bool {
	if d.Layout.FloorRadius <= 0 {
		return true
	}
	safe := float64(d.Layout.FloorRadius - 2)
	return math.Hypot(p.X-float64(d.Layout.Center.X), p.Z-float64(d.Layout.Center.Z)) <= safe
}

func (d *Director) rallyFallback(anchor TargetPosition) TargetPosition {
	cx, cz := float64(d.Layout.Center.X), float64(d.Layout.Center.Z)
	dx, dz := cx-anchor.X, cz-anchor.Z
	dist := math.Hypot(dx, dz)
	x, z := anchor.X, anchor.Z
	if dist > 0 {
		x += 6 * dx / dist
		z += 6 * dz / dist
	}
	if d.Layout.FloorRadius > 0 {
		x, z = clampRadius(x, z, cx, cz, float64(d.Layout.FloorRadius-2))
	}
	return TargetPosition{X: x, Y: anchor.Y, Z: z}
}

// clampRadius keeps a point inside r of the centre. A negative r (a floor too
// small for any rally) collapses to the centre.
func clampRadius(x, z, cx, cz, r float64) (float64, float64) {
	if r < 0 {
		r = 0
	}
	dx, dz := x-cx, z-cz
	dist := math.Hypot(dx, dz)
	if dist <= r || dist == 0 {
		return x, z
	}
	return cx + dx/dist*r, cz + dz/dist*r
}

// addTickingAreas loads the arena's chunks: Bedrock refuses every summon and
// fill outside a ticking area, and the build script removes its own areas on
// exit. Failure here aborts before a single mob is spawned.
func (d *Director) addTickingAreas() error {
	for i, a := range d.Layout.Areas {
		if _, err := d.exec(tickingAddCommand(i, a)); err != nil {
			for j := i - 1; j >= 0; j-- {
				_, _ = d.exec(tickingRemoveCommand(j))
			}
			return err
		}
	}
	return nil
}

// removeTickingAreas is best-effort: cleanup must not mask the run's outcome.
func (d *Director) removeTickingAreas() {
	for i := range d.Layout.Areas {
		_, _ = d.exec(tickingRemoveCommand(i))
	}
}

// cleanupRun runs on every exit path, including timeout and a mid-wave error,
// so buffed mobs, open pens and ticking areas never outlive the run.
func (d *Director) cleanupRun() {
	_, _ = d.exec(killWaveCommand())
	_ = d.closeAllPens()
	d.removeTickingAreas()
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
		out, err := d.exec(cmd)
		if err != nil {
			return err
		}
		if strings.Contains(out, "Unable to summon") {
			return fmt.Errorf("arena: summon %s failed: %s", s.Type, strings.TrimSpace(out))
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

// squadReplaceInterval is the least time between recovery teleports for one
// warrior. The check runs every poll, so without it a position read that keeps
// failing would bounce the warrior off the floor each time.
const squadReplaceInterval = 5 * time.Second

func (d *Director) placeWarrior(name string, i int) error {
	_, err := d.squadPlace(name, i)
	return err
}

func (d *Director) squadPlace(name string, i int) (Point, error) {
	p := squadStartPoint(d.Layout, i)
	cmd, err := squadPlaceCommand(name, p)
	if err != nil {
		return Point{}, err
	}
	if _, err := d.exec(cmd); err != nil {
		return Point{}, err
	}
	d.logf("arena: placed %s", name)
	return p, nil
}

// replaceWarrior returns one warrior to its start cell unless it was already
// returned within squadReplaceInterval. The state's remembered position moves
// with the teleport so the next poll does not read it as a second stray.
func (d *Director) replaceWarrior(name string, i int, st *warriorState) error {
	now := d.now()
	if last, ok := d.lastPlaced[name]; ok && now.Sub(last) < squadReplaceInterval {
		return nil
	}
	p, err := d.squadPlace(name, i)
	if err != nil {
		return err
	}
	if d.lastPlaced == nil {
		d.lastPlaced = map[string]time.Time{}
	}
	d.lastPlaced[name] = now
	st.pos = TargetPosition{X: float64(p.X), Y: float64(p.Y), Z: float64(p.Z)}
	st.havePos = true
	// An asynchronous console may still report the pre-teleport position for
	// one poll; that is our own move, not a death.
	st.ignoreJump = true
	return nil
}

// observeWarrior polls a standing warrior's position. A jump larger than the
// floor itself is a respawn at the world spawn, so it counts as a death even
// when the miss grace hid the down: that is the only signal a quick respawn
// leaves. A warrior that just respawned, or that is beyond the floor's rally
// margin, is walked back to its start cell; the anchor may already be in the
// hub when the mobs are rallied.
func (d *Director) observeWarrior(name string, i int, st *warriorState, respawned bool, result *Result) error {
	pos, err := d.rallyPosition(name)
	readable := err == nil
	hasFloor := d.Layout.FloorRadius > 0
	jumped := false
	if readable {
		if hasFloor && st.havePos && !st.ignoreJump {
			moved := math.Hypot(pos.X-st.pos.X, pos.Z-st.pos.Z)
			jumped = moved > float64(d.Layout.FloorRadius*2)
		}
		if jumped && !st.deathCounted {
			result.WarriorDeaths++
			d.logf("arena: warrior %s died away from the squad", name)
		}
		st.pos, st.havePos = pos, true
		st.ignoreJump = false
	}
	if jumped || respawned {
		// The death this poll reports is now accounted for; the next down
		// starts a fresh count.
		st.deathCounted = false
	}
	// Without a floor there is no start cell to return to, and a jump threshold
	// of zero would read ordinary movement as a respawn at the world spawn.
	if !hasFloor || !d.PlaceSquad {
		return nil
	}
	if readable {
		dist := math.Hypot(pos.X-float64(d.Layout.Center.X), pos.Z-float64(d.Layout.Center.Z))
		if jumped || dist > float64(d.Layout.FloorRadius+10) {
			return d.replaceWarrior(name, i, st)
		}
	}
	if respawned {
		return d.replaceWarrior(name, i, st)
	}
	return nil
}

// warriorState is one warrior's standing across polls. A miss is held for a
// few polls before it counts as a death: Bedrock can drop a target from the
// selector while it respawns.
type warriorState struct {
	up           bool
	live         bool
	misses       int
	downSeen     bool
	havePos      bool
	ignoreJump   bool
	deathCounted bool
	pos          TargetPosition
}

// warriorBaseline remembers who is standing before the first wave. Deaths are
// counted from this set, so a name that never appears is a setup error rather
// than a death.
func (d *Director) warriorBaseline() (map[string]*warriorState, int, error) {
	states := map[string]*warriorState{}
	any := false
	for _, name := range d.Warriors {
		cmd, err := warriorCommand(name)
		if err != nil {
			return nil, 0, err
		}
		n, err := d.countCmd(cmd, "warrior "+name)
		if err != nil {
			return nil, 0, err
		}
		states[name] = &warriorState{up: n > 0, live: n > 0}
		if n > 0 {
			any = true
		}
	}
	if len(d.Warriors) > 0 && !any {
		return nil, 0, fmt.Errorf("arena: none of the warriors %v are in the world", d.Warriors)
	}
	return states, 0, nil
}

// checkWarriors rechecks every warrior on every poll and reports a wipe only
// when all of them are currently down. A down-and-respawned warrior counts as
// standing again and later deaths are counted again.
func (d *Director) checkWarriors(states map[string]*warriorState, result *Result) (bool, error) {
	if len(states) == 0 {
		return false, nil
	}
	standing := false
	for i, name := range d.Warriors {
		st := states[name]
		if st == nil {
			continue
		}
		cmd, err := warriorCommand(name)
		if err != nil {
			return false, err
		}
		n, err := d.countCmd(cmd, "warrior "+name)
		if err != nil {
			return false, err
		}
		if n > 0 {
			respawned := st.downSeen
			st.up, st.live, st.misses, st.downSeen = true, true, 0, false
			standing = true
			if err := d.observeWarrior(name, i, st, respawned, result); err != nil {
				return false, err
			}
			continue
		}
		st.live = false
		st.downSeen = true
		st.misses++
		if st.misses < d.WarriorMisses {
			// A short gap may be a respawn; keep a previously standing
			// warrior standing so it cannot flip the squad to a wipe.
			if st.up {
				standing = true
			}
			continue
		}
		if st.up {
			result.WarriorDeaths++
			st.deathCounted = true
			d.logf("arena: warrior %s is down", name)
		}
		st.up = false
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
			cmd, err := warriorCommand(name)
			if err != nil {
				return st, err
			}
			up, err := d.countCmd(cmd, "warrior "+name)
			if err != nil {
				return st, err
			}
			st.Warriors[name] = up > 0
		}
	}
	return st, nil
}
