package trainer

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"math"
	"math/rand"
	"sync/atomic"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Fake is a deterministic Evaluator with a known optimum. A profile's fitness
// falls with its squared, bound-normalised distance from Optimum; a shared
// per-seed offset and an independent per-fight noise sit on top. Noise is a
// pure function of its inputs, so results do not depend on call order.
type Fake struct {
	Family   tactics.Family
	Optimum  tactics.Params
	Scale    float64 // score lost at a normalised mean-square distance of 1
	SeedSD   float64 // noise shared by every profile on the same seed
	NoiseSD  float64 // noise independent per fight
	SideBias float64 // flat score cost of fighting side b
	// Tactic, when set, is worth TacticReward score points to a profile that
	// leads with it for the family.
	Tactic       string
	TacticReward float64

	calls atomic.Int64
}

// NewFake returns a fake whose optimum sits well away from the defaults, with
// noise large enough that one fight cannot tell two nearby profiles apart.
func NewFake(family tactics.Family) *Fake {
	opt := tactics.Defaults()
	bounds := tactics.Bounds()
	for i, n := range tactics.Names() {
		b := bounds[n]
		frac := []float64{0.8, 0.25, 0.7, 0.3, 0.75, 0.2, 0.65, 0.35, 0.8, 0.3, 0.7, 0.25}[i]
		opt.Set(n, b.Min+frac*(b.Max-b.Min))
	}
	return &Fake{Family: family, Optimum: opt, Scale: 200, SeedSD: 3, NoiseSD: 4, SideBias: 2}
}

// Calls is the number of fights evaluated so far.
func (f *Fake) Calls() int { return int(f.calls.Load()) }

// Distance is the mean squared normalised distance of p from the optimum.
func (f *Fake) Distance(p tactics.Params) float64 {
	var sum float64
	bounds := tactics.Bounds()
	for _, n := range tactics.Names() {
		v, _ := p.Get(n)
		o, _ := f.Optimum.Get(n)
		b := bounds[n]
		z := (v - o) / (b.Max - b.Min)
		sum += z * z
	}
	return sum / float64(len(tactics.Names()))
}

// Fitness is the noiseless score of p.
func (f *Fake) Fitness(p tactics.Params) float64 { return 100 - f.Scale*f.Distance(p) }

func (f *Fake) Evaluate(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.calls.Add(1)
	params := p.For(f.Family)
	salt := hashParams(params)
	if s.Side == "b" {
		salt ^= 0x5bd1e995
	}
	score := f.Fitness(params) + f.SeedSD*gauss(s.Seed, 0) + f.NoiseSD*gauss(s.Seed, salt)
	if s.Side == "b" {
		score -= f.SideBias
	}
	if l := tacticList(p, f.Family); f.Tactic != "" && len(l) > 0 && l[0] == f.Tactic {
		score += f.TacticReward
	}
	// A win with no kills or deaths scores exactly 100 - 0.1*damageTaken, so
	// damage carries the synthetic score through the real Score().
	return []tactics.Scorecard{{
		Scenario: s.Name, Family: s.Family, Profile: p.Name, Side: s.Side,
		Outcome: tactics.OutcomeWin, DamageTaken: (100 - score) * 10, Seed: s.Seed,
	}}, nil
}

func hashParams(p tactics.Params) int64 {
	h := fnv.New64a()
	var b [8]byte
	for _, n := range tactics.Names() {
		v, _ := p.Get(n)
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
		h.Write(b[:])
	}
	return int64(h.Sum64())
}

func gauss(seed, salt int64) float64 {
	return rand.New(rand.NewSource(seed ^ salt)).NormFloat64()
}
