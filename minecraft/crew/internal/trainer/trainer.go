// Package trainer searches for better tactics.Params per opponent family with
// an evolution strategy. It never touches the game: fights come from an
// Evaluator, so the loop is testable offline and the same code drives the lab.
//
// Every loop here is capped (generations, fights, wall clock, consecutive
// evaluator failures) and stops on a kill switch (a flag file, the tacticsctl
// training switch or a context).
package trainer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

// Scenario is one fight to score. Seed is shared by the champion and every
// challenger of a generation, and Side alternates so a positional advantage
// cancels in the paired comparison.
type Scenario struct {
	Name   string         `json:"name"`
	Kind   string         `json:"kind"`
	Family tactics.Family `json:"family"`
	Seed   int64          `json:"seed"`
	Side   string         `json:"side"`
}

// Evaluator runs one fight with a profile and returns its scorecards. It is
// the only thing that knows about the game.
type Evaluator interface {
	Evaluate(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error)
}

// Stop reasons reported in Result.StopReason.
const (
	StopGenerations = "generations"
	StopFights      = "fights"
	StopBudget      = "budget"
	StopFailures    = "failures"
	StopKilled      = "killed"
	StopCancelled   = "cancelled"
	// StopDisabled means the training switch is off or missing.
	StopDisabled = "disabled"
)

// minPairsFloor is the fewest paired fights a promotion may rest on. With
// fewer, a lucky streak is indistinguishable from an improvement.
const minPairsFloor = 5

// Config is the whole run. The four caps are mandatory: Validate rejects a
// config that leaves any of them unset, so no loop can be uncapped.
type Config struct {
	Family   tactics.Family
	Scenario string
	Kind     string // tactics.Scenario* kind; defaults to mob
	Base     tactics.Profile
	Store    *Store
	Eval     Evaluator
	// TacticOptions lists the tactic names that exist for a family. When set,
	// the loop also learns which tactics to use and in what order, drawing
	// only from this list; when nil it tunes numbers alone.
	TacticOptions func(tactics.Family) []string

	MaxGenerations int
	MaxFights      int
	Budget         time.Duration
	MaxFailures    int
	KillFile       string
	// ControlDir is the tacticsctl state directory. Training runs only while
	// its switch file says "on"; a missing file stops the run, so a wiped
	// volume can never start one.
	ControlDir string
	// MinPairs is the fewest paired fights a promotion may rest on; zero
	// means minPairsFloor, and nothing below it is accepted.
	MinPairs int

	Seed     int64
	Mu       int     // parents kept per generation
	Lambda   int     // challengers per generation
	Evals    int     // mirrored fights per candidate per generation
	Margin   float64 // minimum mean score gain to replace the champion
	Alpha    float64 // family-wise one-sided significance level
	Sigma    float64 // initial mutation size as a fraction of each bound
	MutateP  float64 // chance each parameter is mutated
	TacticP  float64 // chance a challenger also gets a tactic change
	RunID    string
	Now      func() time.Time
	Logf     func(format string, args ...any)
	OnGenEnd func(GenStat)
}

// GenStat summarises one finished generation.
type GenStat struct {
	Generation int     `json:"generation"`
	Fights     int     `json:"fights"`
	Sigma      float64 `json:"sigma"`
	Children   int     `json:"children"`
	BestGain   float64 `json:"bestGain"`
	Promoted   bool    `json:"promoted"`
	Champion   string  `json:"champion"`
}

// Result is the outcome of a run.
type Result struct {
	RunID       string         `json:"runId"`
	Family      tactics.Family `json:"family"`
	StopReason  string         `json:"stopReason"`
	Generations int            `json:"generations"`
	Fights      int            `json:"fights"`
	Failures    int            `json:"failures"`
	Promotions  int            `json:"promotions"`
	Sigma       float64        `json:"sigma"`
	Elapsed     string         `json:"elapsed"`
	Champion    Record         `json:"champion"`
}

func (c *Config) applyDefaults() {
	if c.Kind == "" {
		c.Kind = tactics.ScenarioMob
	}
	if c.Base.Name == "" {
		c.Base = tactics.NewProfile("base")
	}
	if c.MinPairs == 0 {
		c.MinPairs = minPairsFloor
	}
	if c.Mu == 0 {
		c.Mu = 3
	}
	if c.Lambda == 0 {
		c.Lambda = 6
	}
	if c.Evals == 0 {
		c.Evals = 8
	}
	if c.Margin == 0 {
		c.Margin = 0.5
	}
	if c.Alpha == 0 {
		c.Alpha = 0.05
	}
	if c.Sigma == 0 {
		c.Sigma = 0.12
	}
	if c.MutateP == 0 {
		c.MutateP = 0.35
	}
	if c.TacticP == 0 {
		c.TacticP = 0.25
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
}

// Validate rejects a config with a missing cap or an unusable setting.
func (c Config) Validate() error {
	switch {
	case c.Store == nil:
		return errors.New("trainer: a store is required")
	case c.Eval == nil:
		return errors.New("trainer: an evaluator is required")
	case !nameRE.MatchString(string(c.Family)):
		return fmt.Errorf("trainer: family %q is not a safe name", c.Family)
	case c.Scenario == "":
		return errors.New("trainer: a scenario is required")
	case c.MaxGenerations <= 0:
		return errors.New("trainer: max generations must be positive")
	case c.MaxFights <= 0:
		return errors.New("trainer: max fights must be positive")
	case c.Budget <= 0:
		return errors.New("trainer: wall-clock budget must be positive")
	case c.MaxFailures <= 0:
		return errors.New("trainer: max consecutive failures must be positive")
	case c.ControlDir == "":
		return errors.New("trainer: a control directory is required for the training switch")
	case c.MinPairs < minPairsFloor:
		return fmt.Errorf("trainer: a promotion needs at least %d paired fights", minPairsFloor)
	case c.Mu < 1 || c.Lambda < 1:
		return errors.New("trainer: mu and lambda must be at least 1")
	case c.Evals < 2:
		return errors.New("trainer: at least 2 evaluations per candidate are needed for a paired test")
	case c.Alpha <= 0 || c.Alpha >= 1:
		return errors.New("trainer: alpha must be in (0,1)")
	case c.Sigma <= 0 || c.Sigma > 1:
		return errors.New("trainer: sigma must be in (0,1]")
	case c.MutateP <= 0 || c.MutateP > 1:
		return errors.New("trainer: mutation probability must be in (0,1]")
	case c.TacticP <= 0 || c.TacticP > 1:
		return errors.New("trainer: tactic mutation probability must be in (0,1]")
	case c.Margin < 0:
		return errors.New("trainer: margin must not be negative")
	}
	return nil
}

const (
	sigmaMin    = 0.01
	sigmaMax    = 0.4
	successP    = 0.2
	growSigma   = 1.22
	shrinkSigma = 0.95
)

// genome is everything the loop varies for the family being trained.
type genome struct {
	params  tactics.Params
	tactics []string
}

type candidate struct {
	genome
	scores []float64 // paired with the champion's scores by scenario index
	idx    []int
	cards  []tactics.Scorecard
}

type run struct {
	Config
	rng      *rand.Rand
	start    time.Time
	deadline time.Time
	fights   int
	failures int
}

// stopErr unwinds a generation when a cap or the kill switch trips.
type stopErr struct{ reason string }

func (e stopErr) Error() string { return "trainer: stopped: " + e.reason }

// Run trains until a cap trips, the kill switch fires, or ctx ends. It always
// returns the latest champion; an error means the run could not start or the
// store failed, not that a cap tripped.
func Run(ctx context.Context, cfg Config) (Result, error) {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	r := &run{Config: cfg, rng: rand.New(rand.NewSource(cfg.Seed)), start: cfg.Now()}
	if r.TacticOptions != nil {
		// A name the engine does not know would silently run the default
		// fight while the record claimed otherwise.
		setTacticList(&r.Base, r.Family, r.known(tacticList(r.Base, r.Family)))
	}
	r.deadline = r.start.Add(cfg.Budget)
	if r.RunID == "" {
		r.RunID = fmt.Sprintf("seed%d-%s", cfg.Seed, r.start.UTC().Format("20060102t150405z"))
	}
	// The context bound interrupts a fight in flight; r.deadline, read from
	// the injectable clock, is what the checks between fights use.
	ctx, cancel := context.WithTimeout(ctx, cfg.Budget)
	defer cancel()

	if !tacticsctl.TrainingEnabled(r.ControlDir) {
		return Result{RunID: r.RunID, Family: r.Family, StopReason: StopDisabled}, nil
	}
	release, err := r.Store.Lock(r.Family)
	if err != nil {
		return Result{}, err
	}
	defer release()

	champ, ok, err := r.Store.Champion(r.Family)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		base := r.Base
		base.Overrides = map[tactics.Family]tactics.Params{r.Family: base.For(r.Family).Clamp()}
		base.Parent = ""
		champ, err = r.Store.Append(Record{
			Profile: base, Family: r.Family, RunID: r.RunID, CreatedAt: r.stamp(),
		}, nil)
		if err != nil {
			return Result{}, err
		}
	}

	res := Result{RunID: r.RunID, Family: r.Family}
	sigma := r.Sigma
	pool := []genome{r.genomeOf(champ)}
	for gen := 1; ; gen++ {
		if reason := r.checkStop(ctx); reason != "" {
			res.StopReason = reason
			break
		}
		if gen > r.MaxGenerations {
			res.StopReason = StopGenerations
			break
		}
		stat, next, nextSigma, nextPool, err := r.generation(ctx, gen, champ, pool, sigma)
		var se stopErr
		if errors.As(err, &se) {
			res.StopReason = se.reason
			break
		}
		if err != nil {
			res.fill(r, champ, sigma)
			return res, err
		}
		if stat.Promoted {
			res.Promotions++
		}
		champ, sigma, pool = next, nextSigma, nextPool
		res.Generations = gen
		stat.Fights = r.fights
		r.Logf("gen %d: fights=%d sigma=%.3f gain=%+.2f promoted=%v champion=%s",
			gen, r.fights, sigma, stat.BestGain, stat.Promoted, champ.Profile.Name)
		if r.OnGenEnd != nil {
			r.OnGenEnd(stat)
		}
	}
	res.fill(r, champ, sigma)
	_ = r.Store.SaveRun(r.Family, r.RunID, res)
	return res, nil
}

func (res *Result) fill(r *run, champ Record, sigma float64) {
	res.Fights, res.Failures, res.Sigma = r.fights, r.failures, sigma
	res.Champion = champ
	res.Elapsed = r.Now().Sub(r.start).Round(time.Millisecond).String()
}

func (r *run) stamp() string { return r.Now().UTC().Format(time.RFC3339) }

// checkStop covers every cap except generations, and the kill switch. It runs
// before each generation and before each fight, so no cap can be overshot.
func (r *run) checkStop(ctx context.Context) string {
	if r.KillFile != "" {
		if _, err := os.Stat(r.KillFile); err == nil {
			return StopKilled
		}
	}
	if !tacticsctl.TrainingEnabled(r.ControlDir) {
		return StopDisabled
	}
	switch {
	case ctx.Err() != nil && !r.Now().Before(r.deadline):
		return StopBudget
	case ctx.Err() != nil:
		return StopCancelled
	case !r.Now().Before(r.deadline):
		return StopBudget
	case r.fights >= r.MaxFights:
		return StopFights
	case r.failures >= r.MaxFailures:
		return StopFailures
	}
	return ""
}

// fight spends one unit of the fight budget. A transient evaluator problem
// returns nil scores and no error so the caller can skip that sample.
func (r *run) fight(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, float64, bool, error) {
	if reason := r.checkStop(ctx); reason != "" {
		return nil, 0, false, stopErr{reason}
	}
	r.fights++
	cards, err := r.Eval.Evaluate(ctx, p, s)
	if err != nil {
		if reason := r.checkStop(ctx); reason == StopCancelled || reason == StopBudget || reason == StopKilled || reason == StopDisabled {
			return nil, 0, false, stopErr{reason}
		}
		r.failures++
		r.Logf("evaluator failure %d/%d: %v", r.failures, r.MaxFailures, err)
		return nil, 0, false, nil
	}
	cards = ownCards(cards, p.Name)
	var sum float64
	var n int
	for _, c := range cards {
		if c.Outcome == tactics.OutcomeAborted {
			continue
		}
		sum += c.Score()
		n++
	}
	if n == 0 {
		r.failures++
		r.Logf("evaluator failure %d/%d: no measured scorecards", r.failures, r.MaxFailures)
		return nil, 0, false, nil
	}
	r.failures = 0
	return cards, sum / float64(n), true, nil
}

// ownCards keeps the scorecards of the profile under test. A duel emits one
// card per side, and scoring the opponent's mirror card would let a stronger
// opponent raise the candidate's mean.
func ownCards(cards []tactics.Scorecard, profile string) []tactics.Scorecard {
	return slices.DeleteFunc(slices.Clone(cards), func(c tactics.Scorecard) bool { return c.Profile != profile })
}

func (r *run) scenarios(n int) []Scenario {
	out := make([]Scenario, n)
	var seed int64
	for i := range out {
		// A seed is shared by an a-side and a b-side fight: mirrored.
		if i%2 == 0 {
			seed = r.rng.Int63()
		}
		side := "a"
		if i%2 == 1 {
			side = "b"
		}
		out[i] = Scenario{Name: r.Scenario, Kind: r.Kind, Family: r.Family, Seed: seed, Side: side}
	}
	return out
}

func (r *run) profileFor(name, parent string, g genome) tactics.Profile {
	pr := r.Base
	pr.Version = tactics.Version
	pr.Name, pr.Parent = name, parent
	pr.Overrides = map[tactics.Family]tactics.Params{r.Family: g.params.Clamp()}
	setTacticList(&pr, r.Family, g.tactics)
	pr.Default = pr.Default.Clamp()
	return pr
}

// genomeOf reads a record's trainable part. With tactic options configured,
// names no longer registered are dropped so they are never re-emitted.
func (r *run) genomeOf(rec Record) genome {
	list := tacticList(rec.Profile, r.Family)
	if r.TacticOptions != nil {
		list = r.known(list)
	}
	return genome{params: rec.Params(), tactics: list}
}

// known keeps the entries of list that are registered for the family, in
// order and without repeats.
func (r *run) known(list []string) []string {
	valid := map[string]bool{}
	for _, n := range r.TacticOptions(r.Family) {
		valid[n] = true
	}
	var out []string
	for _, n := range list {
		if valid[n] {
			valid[n] = false
			out = append(out, n)
		}
	}
	return out
}

const maxTactics = 4

// mutateTactics changes a tactic list by one step: swap a tactic for another
// option, add one, drop one, or reorder two. Every name comes from options.
func (r *run) mutateTactics(cur []string, options []string) []string {
	used := map[string]bool{}
	for _, n := range cur {
		used[n] = true
	}
	var unused []string
	for _, n := range options {
		if !used[n] {
			unused = append(unused, n)
		}
	}
	const (
		opReplace = iota
		opAdd
		opDrop
		opSwap
	)
	var ops []int
	if len(cur) > 0 && len(unused) > 0 {
		ops = append(ops, opReplace)
	}
	if len(cur) < maxTactics && len(unused) > 0 {
		ops = append(ops, opAdd)
	}
	if len(cur) > 0 {
		ops = append(ops, opDrop)
	}
	if len(cur) > 1 {
		ops = append(ops, opSwap)
	}
	if len(ops) == 0 {
		return cur
	}
	next := append([]string(nil), cur...)
	switch ops[r.rng.Intn(len(ops))] {
	case opReplace:
		next[r.rng.Intn(len(next))] = unused[r.rng.Intn(len(unused))]
	case opAdd:
		n := unused[r.rng.Intn(len(unused))]
		at := r.rng.Intn(len(next) + 1)
		next = append(next[:at], append([]string{n}, next[at:]...)...)
	case opDrop:
		at := r.rng.Intn(len(next))
		next = append(next[:at], next[at+1:]...)
	case opSwap:
		i := r.rng.Intn(len(next))
		j := r.rng.Intn(len(next) - 1)
		if j >= i {
			j++
		}
		next[i], next[j] = next[j], next[i]
	}
	return next
}

func (r *run) mutate(parent genome, sigma float64) genome {
	child := parent.params
	tacs := parent.tactics
	bounds := tactics.Bounds()
	names := tactics.Names()
	changed := false
	apply := func(n string) {
		v, _ := child.Get(n)
		b := bounds[n]
		child.Set(n, v+r.rng.NormFloat64()*sigma*(b.Max-b.Min))
		changed = true
	}
	for _, n := range names {
		if r.rng.Float64() < r.MutateP {
			apply(n)
		}
	}
	if !changed {
		apply(names[r.rng.Intn(len(names))])
	}
	out := genome{params: child.Clamp(), tactics: tacs}
	if r.TacticOptions != nil && r.rng.Float64() < r.TacticP {
		if options := r.TacticOptions(r.Family); len(options) > 0 {
			out.tactics = r.mutateTactics(r.known(tacs), options)
		}
	}
	return out
}

func (r *run) generation(ctx context.Context, gen int, champ Record, pool []genome, sigma float64) (GenStat, Record, float64, []genome, error) {
	stat := GenStat{Generation: gen, Sigma: sigma, Champion: champ.Profile.Name}
	// Shrink the field rather than overshoot the fight cap.
	children := r.Lambda
	if fit := (r.MaxFights-r.fights)/r.Evals - 1; fit < children {
		children = fit
	}
	if children < 1 {
		return stat, champ, sigma, pool, stopErr{StopFights}
	}
	stat.Children = children

	cands := make([]*candidate, children)
	for k := range cands {
		parent := pool[r.rng.Intn(len(pool))]
		cands[k] = &candidate{genome: r.mutate(parent, sigma)}
	}
	champProfile := r.profileFor(champ.Profile.Name, champ.Profile.Parent, r.genomeOf(champ))
	var champScores []float64
	var champCards []tactics.Scorecard

	for _, scn := range r.scenarios(r.Evals) {
		cards, score, ok, err := r.fight(ctx, champProfile, scn)
		if err != nil {
			return stat, champ, sigma, pool, err
		}
		if !ok {
			continue
		}
		champCards = append(champCards, cards...)
		champIdx := len(champScores)
		champScores = append(champScores, score)
		for k, c := range cands {
			name := fmt.Sprintf("%s-g%03d-c%d", r.Family, gen, k)
			cards, score, ok, err := r.fight(ctx, r.profileFor(name, champ.Profile.Name, c.genome), scn)
			if err != nil {
				return stat, champ, sigma, pool, err
			}
			if !ok {
				continue
			}
			c.cards = append(c.cards, cards...)
			c.scores = append(c.scores, score)
			c.idx = append(c.idx, champIdx)
		}
	}

	type result struct {
		c    *candidate
		test pairedTest
	}
	var results []result
	successes := 0
	for _, c := range cands {
		diffs := make([]float64, len(c.scores))
		for j, s := range c.scores {
			diffs[j] = s - champScores[c.idx[j]]
		}
		if len(diffs) < r.MinPairs {
			continue
		}
		t := newPairedTest(diffs)
		results = append(results, result{c, t})
		if t.Mean > 0 && t.P < successP {
			successes++
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].test.Mean > results[j].test.Mean })

	next, nextPool := champ, []genome{r.genomeOf(champ)}
	if len(results) > 0 {
		best := results[0]
		stat.BestGain = best.test.Mean
		// Bonferroni over the field: picking the best of lambda noisy
		// challengers would otherwise promote luck.
		alpha := r.Alpha / float64(children)
		if best.test.Mean > r.Margin && best.test.P < alpha {
			if !tacticsctl.TrainingEnabled(r.ControlDir) {
				return stat, champ, sigma, pool, stopErr{StopDisabled}
			}
			rec, err := r.promote(gen, champ, best.c, champCards, best.test)
			if err != nil {
				return stat, champ, sigma, pool, err
			}
			next, nextPool = rec, []genome{r.genomeOf(rec)}
			stat.Promoted, stat.Champion = true, rec.Profile.Name
			results = results[1:]
		}
		for _, res := range results {
			if len(nextPool) >= r.Mu {
				break
			}
			if res.test.Mean > 0 {
				nextPool = append(nextPool, res.c.genome)
			}
		}
	}

	if float64(successes)/float64(children) > successP {
		sigma = math.Min(sigmaMax, sigma*growSigma)
	} else {
		sigma = math.Max(sigmaMin, sigma*shrinkSigma)
	}
	return stat, next, sigma, nextPool, nil
}

func (r *run) promote(gen int, champ Record, c *candidate, champCards []tactics.Scorecard, t pairedTest) (Record, error) {
	cards := append(append([]tactics.Scorecard{}, champCards...), c.cards...)
	pr := r.profileFor("", champ.Profile.Name, c.genome)
	return r.Store.Append(Record{
		Profile: pr, Family: r.Family, Generation: gen, RunID: r.RunID,
		Pairs: t.N, Advantage: t.Mean, P: t.P, CreatedAt: r.stamp(),
	}, cards)
}
