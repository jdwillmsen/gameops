package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/repair"
)

const layout = "../../lab/colosseum/layout.json"

// fixture writes a plan (a sand floor, a wall) and a scan in which the wall
// lost two blocks and a creeper left dirt on the floor.
func fixture(t *testing.T) (planDir, scan string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("clear.cmds", "fill -310 64 -770 -290 70 -750 air\n")
	write("build.cmds", "fill -310 63 -770 -290 63 -750 sand\nfill -310 64 -770 -310 66 -750 stone_bricks\n")
	scan = write("scan.cmds", "fill -310 63 -770 -290 63 -750 sand\nfill -310 64 -770 -310 66 -750 stone_bricks\n"+
		"fill -310 65 -760 -310 66 -760 air\nsetblock -300 64 -760 dirt\n")
	return dir, scan
}

func fakeDeps(c *arena.FakeConsole) deps {
	return deps{
		console: func(string, string) (arena.Console, error) { return c, nil },
		generate: func(string, string) error {
			panic("the generator should not run when --plan-dir is given")
		},
	}
}

func idle(f *arena.FakeConsole) {
	f.Respond = func(cmd string) (string, error) {
		switch {
		case cmd == "save query":
			return "Data saved. Files are now ready to be copied.\nworlds/crew-lab/db/CURRENT:2", nil
		case cmd == "list":
			return "There are 0/10 players online:\n", nil
		case strings.HasPrefix(cmd, "testfor"):
			return "No targets matched selector", nil
		}
		return "Successfully filled 1 block(s)", nil
	}
}

func tarOfCurrent(w io.Writer) error {
	tw := tar.NewWriter(w)
	if err := tw.WriteHeader(&tar.Header{Name: "db/CURRENT", Mode: 0o644, Size: 3, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte("x\ny")); err != nil {
		return err
	}
	return tw.Close()
}

// liveDeps serves a snapshot whose scan is the fixture file.
func liveDeps(t *testing.T, c *arena.FakeConsole, scan string) deps {
	d := fakeDeps(c)
	d.snapshot = func(c arena.Console, _, _, _ string) repair.Snapshotter {
		return repair.Snapshotter{Console: c, Sleep: func(time.Duration) {}, Start: func(_ context.Context, _ []string, w io.Writer) error { return tarOfCurrent(w) }}
	}
	d.world = func(string, []repair.Op) (repair.Reader, func() error, error) {
		f, err := os.Open(scan)
		if err != nil {
			return nil, nil, err
		}
		defer f.Close()
		ops, err := repair.ParseOps(f)
		return repair.OpsReader{Ops: ops}, func() error { return nil }, err
	}
	return d
}

func TestPlanReportsDamageAndSendsNothing(t *testing.T) {
	planDir, scan := fixture(t)
	c := &arena.FakeConsole{}
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"plan", "--layout", layout, "--plan-dir", planDir, "--scan", scan, "--json"}, &out, &errb, fakeDeps(c))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var rep repair.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if rep.Status != repair.StatusPlanned || rep.Damaged != 3 || rep.ByKind[repair.KindMissing] != 2 || rep.ByKind[repair.KindIntruder] != 1 {
		t.Errorf("report: %+v", rep)
	}
	if len(c.Cmds) != 0 {
		t.Errorf("plan touched the console: %v", c.Cmds)
	}
}

func TestPlanTextListsCommandsOnRequest(t *testing.T) {
	planDir, scan := fixture(t)
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"plan", "--layout", layout, "--plan-dir", planDir, "--scan", scan, "--commands"}, &out, &errb, fakeDeps(&arena.FakeConsole{})); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"repair report (planned)", "setblock -300 64 -760 air", "fill -310 65 -760 -310 66 -760 stone_bricks"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
}

func TestApplyIsADryRunByDefault(t *testing.T) {
	planDir, scan := fixture(t)
	c := &arena.FakeConsole{}
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"apply", "--lab", "lab", "--layout", layout, "--plan-dir", planDir, "--scan", scan}, &out, &errb, fakeDeps(c))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if len(c.Cmds) != 0 {
		t.Errorf("a dry run sent %v", c.Cmds)
	}
	if !strings.Contains(out.String(), "dry run, nothing sent") || !strings.Contains(errb.String(), "--execute") {
		t.Errorf("stdout:\n%s\nstderr: %s", out.String(), errb.String())
	}
}

func TestApplyExecuteSendsTheRepair(t *testing.T) {
	planDir, scan := fixture(t)
	c := &arena.FakeConsole{}
	idle(c)
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"apply", "--execute", "--quiet", "1ms", "--load-wait", "1ms", "--lab", "lab", "--layout", layout, "--plan-dir", planDir}, &out, &errb, liveDeps(t, c, scan))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errb.String(), out.String())
	}
	if c.Count("setblock -300 64 -760 air") != 1 || c.Count("fill -310 65 -760 -310 66 -760 stone_bricks") != 1 || c.Count("tickingarea add") == 0 {
		t.Errorf("commands: %v", c.Cmds)
	}
	if !strings.Contains(out.String(), "repair report (applied)") || !strings.Contains(out.String(), "world copy taken") || !strings.Contains(errb.String(), "the scan is from") {
		t.Errorf("stdout:\n%s", out.String())
	}
}

func TestApplyRefusedDuringAFightExitsTwo(t *testing.T) {
	planDir, scan := fixture(t)
	c := &arena.FakeConsole{}
	idle(c)
	base := c.Respond
	c.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "testfor") && strings.Contains(cmd, "arena_wave") {
			return "Found 3 entities", nil
		}
		return base(cmd)
	}
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"apply", "--execute", "--quiet", "1ms", "--load-wait", "1ms", "--lab", "lab", "--layout", layout, "--plan-dir", planDir}, &out, &errb, liveDeps(t, c, scan))
	if code != 2 || c.Count("fill") != 0 || c.Count("setblock") != 0 {
		t.Fatalf("exit %d, commands %v", code, c.Cmds)
	}
	if !strings.Contains(out.String(), "REFUSED") {
		t.Errorf("stdout:\n%s", out.String())
	}
}

func TestExecuteNeedsTheLabAsItsOnlySource(t *testing.T) {
	planDir, scan := fixture(t)
	for _, src := range [][]string{{"--scan", scan}, {"--world", t.TempDir()}} {
		c := &arena.FakeConsole{}
		idle(c)
		args := append([]string{"apply", "--execute", "--lab", "lab", "--layout", layout, "--plan-dir", planDir}, src...)
		var errb bytes.Buffer
		if code := run(context.Background(), args, &bytes.Buffer{}, &errb, fakeDeps(c)); code != 2 || len(c.Cmds) != 0 {
			t.Errorf("%v: exit %d, commands %v (%s)", src, code, c.Cmds, errb.String())
		}
	}
}

func TestLabTargetsAreAllowlisted(t *testing.T) {
	planDir, scan := fixture(t)
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"--lab", "k8s:jdwillmsen-prd/jdwillmsen-minecraft-fwb-prd-minecraft-bedrock-0"}, 2},
		{[]string{"--lab", "k8s:other/pod", "--confirm-lab", "k8s:another/pod"}, 2},
		{[]string{"--lab", "k8s:other/pod", "--confirm-lab", "k8s:other/pod"}, 0},
		{[]string{"--lab", knownLab}, 0},
	} {
		c := &arena.FakeConsole{}
		idle(c)
		args := append([]string{"apply", "--layout", layout, "--plan-dir", planDir, "--scan", scan}, tc.args...)
		var errb bytes.Buffer
		if code := run(context.Background(), args, &bytes.Buffer{}, &errb, fakeDeps(c)); code != tc.code {
			t.Errorf("%v: exit %d, want %d (%s)", tc.args, code, tc.code, errb.String())
		}
	}
	var errb bytes.Buffer
	if code := run(context.Background(), []string{"snapshot", "--lab", "k8s:x/y", "--out", t.TempDir()}, &bytes.Buffer{}, &errb, fakeDeps(&arena.FakeConsole{})); code != 2 {
		t.Errorf("snapshot of an unknown lab: exit %d", code)
	}
}

func TestCancelledSnapshotStillResumesSaving(t *testing.T) {
	_, scan := fixture(t)
	planDir, _ := fixture(t)
	c := &arena.FakeConsole{}
	idle(c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var errb bytes.Buffer
	code := run(ctx, []string{"plan", "--lab", "lab", "--layout", layout, "--plan-dir", planDir}, &bytes.Buffer{}, &errb, liveDeps(t, c, scan))
	if code != 1 || c.Count("save resume") != 1 || c.Count("save query") != 0 {
		t.Errorf("exit %d, commands %v (%s)", code, c.Cmds, errb.String())
	}
}

func TestOtherArenasAreRefused(t *testing.T) {
	planDir, scan := fixture(t)
	other := filepath.Join(t.TempDir(), "layout.json")
	raw, err := os.ReadFile(layout)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, bytes.Replace(raw, []byte(`"x": -300`), []byte(`"x": 0`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	code := run(context.Background(), []string{"plan", "--layout", other, "--plan-dir", planDir, "--scan", scan}, &bytes.Buffer{}, &errb, fakeDeps(&arena.FakeConsole{}))
	if code != 1 || !strings.Contains(errb.String(), "larger Colosseum") {
		t.Errorf("exit %d: %s", code, errb.String())
	}
}

func TestUsageErrors(t *testing.T) {
	planDir, scan := fixture(t)
	for name, args := range map[string][]string{
		"no command":        {},
		"unknown command":   {"nuke"},
		"no source":         {"plan", "--plan-dir", planDir},
		"two sources":       {"plan", "--plan-dir", planDir, "--scan", scan, "--world", "x"},
		"apply without lab": {"apply", "--plan-dir", planDir, "--scan", scan},
		"plan execute":      {"plan", "--execute", "--plan-dir", planDir, "--scan", scan},
		"snapshot no out":   {"snapshot", "--lab", "lab"},
	} {
		var errb bytes.Buffer
		if code := run(context.Background(), args, &bytes.Buffer{}, &errb, fakeDeps(&arena.FakeConsole{})); code != 2 {
			t.Errorf("%s: exit %d (%s)", name, code, errb.String())
		}
	}
}

func TestGeneratorRunsWhenNoPlanDir(t *testing.T) {
	_, scan := fixture(t)
	called := false
	d := fakeDeps(&arena.FakeConsole{})
	d.generate = func(gen, out string) error {
		called = true
		for name, text := range map[string]string{"clear.cmds": "fill -310 64 -770 -290 70 -750 air\n", "build.cmds": "fill -310 63 -770 -290 63 -750 sand\n"} {
			if err := os.WriteFile(filepath.Join(out, name), []byte(text), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	var errb bytes.Buffer
	if code := run(context.Background(), []string{"plan", "--layout", layout, "--scan", scan}, &bytes.Buffer{}, &errb, d); code != 0 || !called {
		t.Fatalf("exit %d called %v: %s", code, called, errb.String())
	}
}

func TestSnapshotCommandPrintsTheDBPath(t *testing.T) {
	c := &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == "save query" {
			return "Files are now ready to be copied\nworlds/crew-lab/db/CURRENT:2", nil
		}
		return "", nil
	}}
	d := fakeDeps(c)
	d.snapshot = func(c arena.Console, _, _, _ string) repair.Snapshotter {
		return repair.Snapshotter{Console: c, Sleep: func(time.Duration) {}, Start: func(_ context.Context, _ []string, w io.Writer) error {
			tw := tar.NewWriter(w)
			if err := tw.WriteHeader(&tar.Header{Name: "db/CURRENT", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg}); err != nil {
				return err
			}
			if _, err := tw.Write([]byte("x\n")); err != nil {
				return err
			}
			return tw.Close()
		}}
	}
	out := filepath.Join(t.TempDir(), "snap")
	var stdout, errb bytes.Buffer
	if code := run(context.Background(), []string{"snapshot", "--lab", "lab", "--out", out}, &stdout, &errb, d); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != filepath.Join(out, "db") {
		t.Errorf("printed %q", got)
	}
	if _, err := os.Stat(filepath.Join(out, "db", "CURRENT")); err != nil {
		t.Error(err)
	}
	if c.Count("save resume") != 1 {
		t.Errorf("commands %v", c.Cmds)
	}
}

func TestDBDirAcceptsEitherLevel(t *testing.T) {
	root := t.TempDir()
	if _, err := dbDir(root); err == nil {
		t.Fatal("an empty directory is not a world")
	}
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "db", "CURRENT"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{root, filepath.Join(root, "db")} {
		if got, err := dbDir(in); err != nil || got != filepath.Join(root, "db") {
			t.Errorf("dbDir(%s) = %q, %v", in, got, err)
		}
	}
}
