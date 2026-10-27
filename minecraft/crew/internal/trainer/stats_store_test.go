package trainer

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func TestStudentCDFMatchesTables(t *testing.T) {
	// One-sided critical values from a t table.
	for _, c := range []struct{ t, df, p float64 }{
		{1.895, 7, 0.05}, {2.262, 9, 0.025}, {1.645, 1e6, 0.05}, {3.365, 5, 0.01}, {0, 4, 0.5},
	} {
		if got := 1 - studentCDF(c.t, c.df); math.Abs(got-c.p) > 0.002 {
			t.Errorf("1-cdf(%v, df %v) = %.4f, want %.3f", c.t, c.df, got, c.p)
		}
	}
}

func TestPairedTest(t *testing.T) {
	if p := newPairedTest([]float64{5, 6, 4, 5, 7, 5}); p.P > 0.001 {
		t.Errorf("clear gain p = %v", p.P)
	}
	if p := newPairedTest([]float64{5, -6, 4, -5, 1, -2}); p.P < 0.2 {
		t.Errorf("noise p = %v", p.P)
	}
	if p := newPairedTest([]float64{3, 3, 3}); p.P != 0 {
		t.Errorf("constant gain p = %v", p.P)
	}
	if p := newPairedTest([]float64{0, 0, 0}); p.P != 1 {
		t.Errorf("constant tie p = %v", p.P)
	}
	if p := newPairedTest([]float64{9}); p.P != 1 {
		t.Errorf("single sample p = %v", p.P)
	}
}

func TestStoreHistoryRollbackAndAtomicity(t *testing.T) {
	s := newStore(t)
	f := tactics.FamilyZombie
	var parent string
	for i := 0; i < 3; i++ {
		p := tactics.Defaults()
		p.KiteBackoff = 4 + float64(i)
		pr := tactics.NewProfile("")
		pr.Overrides = map[tactics.Family]tactics.Params{f: p}
		pr.Parent = parent
		rec, err := s.Append(Record{Profile: pr, Family: f, Generation: i}, []tactics.Scorecard{{Outcome: tactics.OutcomeWin}})
		if err != nil {
			t.Fatal(err)
		}
		parent = rec.Profile.Name
	}
	hist, _ := s.History(f)
	if len(hist) != 3 || hist[2].Profile.Parent != hist[1].Profile.Name || hist[2].Profile.Name != "zombie-v002" {
		t.Fatalf("history = %+v", hist)
	}

	rec, err := s.Rollback(f, "zombie-v000")
	if err != nil || rec.Params().KiteBackoff != 4 {
		t.Fatalf("rollback = %+v, %v", rec, err)
	}
	cur, ok, _ := s.Champion(f)
	if !ok || cur.Profile.Name != "zombie-v000" {
		t.Errorf("champion after rollback = %s", cur.Profile.Name)
	}
	if h, _ := s.History(f); len(h) != 3 {
		t.Errorf("rollback changed history to %d records", len(h))
	}
	next, _ := s.Append(Record{Profile: tactics.NewProfile(""), Family: f}, nil)
	if next.Profile.Name != "zombie-v003" {
		t.Errorf("name after rollback = %s, want a fresh one", next.Profile.Name)
	}
	if _, err := s.Rollback(f, "zombie-v099"); err == nil {
		t.Error("rollback to an unknown champion succeeded")
	}

	var tmps []string
	filepath.WalkDir(s.Dir(), func(p string, d os.DirEntry, _ error) error {
		if strings.HasPrefix(d.Name(), ".tmp-") {
			tmps = append(tmps, p)
		}
		return nil
	})
	if len(tmps) > 0 {
		t.Errorf("temp files left behind: %v", tmps)
	}
}

func TestStoreClampsAndRejectsHostileInput(t *testing.T) {
	s := newStore(t)
	dir := filepath.Join(s.Dir(), "zombie")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "champion.json"),
		[]byte(`{"profile":{"version":1,"name":"x","default":{},"overrides":{"zombie":{"healSafe":500}}},"family":"zombie"}`), 0o644)
	rec, ok, err := s.Champion(tactics.FamilyZombie)
	if err != nil || !ok || rec.Params().HealSafe != 8 {
		t.Errorf("healSafe = %v, ok %v, err %v, want clamped to 8", rec.Params().HealSafe, ok, err)
	}
	if _, _, err := s.Champion("../etc"); err == nil {
		t.Error("path traversal family accepted")
	}
	os.WriteFile(filepath.Join(dir, "champion.json"), []byte(`{"profile":{"version":9}}`), 0o644)
	if _, _, err := s.Champion(tactics.FamilyZombie); err == nil {
		t.Error("wrong-version champion accepted")
	}
}

func TestCmdEvaluator(t *testing.T) {
	t.Setenv("TRAINER_TEST_HELPER", "1")
	ev := CmdEvaluator{Command: os.Args[0] + " -test.run=TestHelperProcess"}
	scn := Scenario{Name: "pack", Kind: "mob", Family: tactics.FamilyZombie, Seed: 5, Side: "b"}
	cards, err := ev.Evaluate(context.Background(), tactics.NewProfile("p1"), scn)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Profile != "p1" || cards[0].Side != "b" || cards[0].Seed != 5 {
		t.Errorf("cards = %+v", cards)
	}

	t.Setenv("TRAINER_TEST_MODE", "fail")
	if _, err := ev.Evaluate(context.Background(), tactics.NewProfile("p1"), scn); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing command err = %v", err)
	}
	t.Setenv("TRAINER_TEST_MODE", "garbage")
	if _, err := ev.Evaluate(context.Background(), tactics.NewProfile("p1"), scn); err == nil {
		t.Error("garbage output accepted")
	}
	if _, err := (CmdEvaluator{}).Evaluate(context.Background(), tactics.NewProfile("p1"), scn); err == nil {
		t.Error("empty command accepted")
	}
}

// TestHelperProcess is the evaluator command CmdEvaluator runs in tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("TRAINER_TEST_HELPER") != "1" {
		return
	}
	switch os.Getenv("TRAINER_TEST_MODE") {
	case "fail":
		os.Stderr.WriteString("boom")
		os.Exit(3)
	case "garbage":
		os.Stdout.WriteString("not json\n")
	default:
		var req cmdRequest
		if err := jsonDecode(os.Stdin, &req); err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(4)
		}
		os.Stdout.WriteString(`{"profile":"` + req.Profile.Name + `","side":"` + os.Getenv("TRAINER_SIDE") +
			`","seed":` + os.Getenv("TRAINER_SEED") + `,"outcome":"win"}` + "\n\n" +
			`{"profile":"rival","outcome":"loss"}` + "\n")
	}
	os.Exit(0)
}
