package trainer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

func setSwitch(t *testing.T, dir, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTrainingSwitchOffStopsARunBeforeTheNextFight(t *testing.T) {
	var calls atomic.Int64
	inner := NewFake(tactics.FamilyZombie)
	cfg := baseConfig(t, nil)
	cfg.Eval = evalFunc(func(ctx context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
		if calls.Add(1) == 5 {
			setSwitch(t, cfg.ControlDir, "off\n")
		}
		return inner.Evaluate(ctx, p, s)
	})
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopDisabled || calls.Load() != 5 {
		t.Errorf("stop %s after %d fights, want disabled after 5", res.StopReason, calls.Load())
	}
}

func TestMissingTrainingSwitchStopsBeforeAnyFight(t *testing.T) {
	var calls atomic.Int64
	cfg := baseConfig(t, evalFunc(func(context.Context, tactics.Profile, Scenario) ([]tactics.Scorecard, error) {
		calls.Add(1)
		return nil, nil
	}))
	cfg.ControlDir = t.TempDir()
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopDisabled || calls.Load() != 0 {
		t.Errorf("stop %s after %d fights, want disabled with none", res.StopReason, calls.Load())
	}
	if _, ok, _ := cfg.Store.Champion(tactics.FamilyZombie); ok {
		t.Error("a disabled run still wrote a champion")
	}
	cfg.ControlDir = filepath.Join(cfg.ControlDir, "gone")
	if res, _ := Run(context.Background(), cfg); res.StopReason != StopDisabled {
		t.Errorf("missing control directory: stop %s", res.StopReason)
	}
}

func TestTrainingSwitchOnLetsARunGo(t *testing.T) {
	cfg := baseConfig(t, NewFake(tactics.FamilyZombie))
	cfg.MaxGenerations = 2
	res, err := Run(context.Background(), cfg)
	if err != nil || res.StopReason != StopGenerations || res.Fights == 0 {
		t.Errorf("switch on: stop %s, %d fights, %v", res.StopReason, res.Fights, err)
	}
}

func TestRunNeedsAControlDirectory(t *testing.T) {
	cfg := baseConfig(t, NewFake(tactics.FamilyZombie))
	cfg.ControlDir = ""
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Error("a run without a control directory started")
	}
}

// improving makes every challenger a little better than the champion, with a
// gain that differs from seed to seed so the paired test has variance.
func improving() Evaluator {
	return evalFunc(func(_ context.Context, p tactics.Profile, s Scenario) ([]tactics.Scorecard, error) {
		taken := 50 + float64(s.Seed%13)
		if strings.Contains(p.Name, "-g") {
			taken -= 20 + float64(s.Seed%3)
		}
		return []tactics.Scorecard{{Profile: p.Name, Outcome: tactics.OutcomeWin, Seconds: 30, DamageTaken: taken}}, nil
	})
}

func TestTooFewPairsNeverPromote(t *testing.T) {
	for _, evals := range []int{2, 3, 4} {
		cfg := baseConfig(t, improving())
		cfg.Evals, cfg.MaxGenerations = evals, 10
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if res.Promotions != 0 {
			t.Errorf("evals %d: %d promotions on fewer than %d pairs", evals, res.Promotions, minPairsFloor)
		}
	}
	cfg := baseConfig(t, improving())
	cfg.Evals, cfg.MaxGenerations = 8, 10
	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Promotions == 0 {
		t.Fatal("test premise: a clear improvement over 8 pairs should promote")
	}
}

func TestMinPairsCannotBeLoweredBelowTheFloor(t *testing.T) {
	cfg := baseConfig(t, NewFake(tactics.FamilyZombie))
	cfg.MinPairs = 2
	if _, err := Run(context.Background(), cfg); err == nil {
		t.Error("a run with MinPairs below the floor started")
	}
}
