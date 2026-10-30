package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
)

const (
	crewLab    = "crew-lab"
	layoutFile = "../../lab/colosseum/layout.json"
)

// cli is one command-line run against a fake lab.
type cli struct {
	t        *testing.T
	lab      *fakeLab
	units    UnitSource
	duel     DuelRunner
	dir      string
	built    bool
	out, err bytes.Buffer
}

// newCLI puts a fake lab and a control dir with the switch on behind the
// command.
func newCLI(t *testing.T) *cli {
	t.Helper()
	c := &cli{t: t, lab: newLab(), dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(c.dir, "training"), []byte("on\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prevConsole, prevUnits, prevDuel, prevClock := newConsole, newUnits, newDuel, clock
	newConsole = func(string, string) (arena.Console, error) { c.built = true; return c.lab, nil }
	newUnits = func(string) (UnitSource, error) { return c.units, nil }
	newDuel = func(*config, arena.Console, func() string) (DuelRunner, error) { return c.duel, nil }
	clock = c.lab.clk
	t.Cleanup(func() { newConsole, newUnits, newDuel, clock = prevConsole, prevUnits, prevDuel, prevClock })
	return c
}

func (c *cli) run(args ...string) int {
	c.t.Helper()
	c.out.Reset()
	c.err.Reset()
	return realMain(context.Background(), args, &c.out, &c.err)
}

func (c *cli) base(extra ...string) []string {
	return append([]string{"--lab", crewLab, "--control-dir", c.dir}, extra...)
}

func TestTPRunsEndToEndAndPrintsPass(t *testing.T) {
	c := newCLI(t)
	if code := c.run(c.base("--checks", "tp")...); code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, c.err.String())
	}
	out := c.out.String()
	for _, w := range []string{"PASS", "tp", "cleanup      ok", "reply:"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestJSONReportCarriesEveryCheckWithItsEvidence(t *testing.T) {
	c := newCLI(t)
	c.lab.tp, c.lab.tpReply = "refuse", "Syntax error: Unexpected \"90\""
	code := c.run(c.base("--checks", "tp", "--json")...)
	if code != exitFail {
		t.Fatalf("exit = %d, want %d", code, exitFail)
	}
	var rep struct {
		Lab     string `json:"lab"`
		Results []struct {
			Check    string         `json:"check"`
			Status   string         `json:"status"`
			Evidence map[string]any `json:"evidence"`
		} `json:"results"`
		Cleanup struct {
			OK bool `json:"ok"`
		} `json:"cleanup"`
	}
	if err := json.Unmarshal(c.out.Bytes(), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, c.out.String())
	}
	if rep.Lab != crewLab || len(rep.Results) != 1 || rep.Results[0].Status != "FAIL" || rep.Results[0].Check != "tp" ||
		!strings.Contains(rep.Results[0].Evidence["reply"].(string), "Syntax error") || !rep.Cleanup.OK {
		t.Errorf("report = %+v", rep)
	}
}

func TestAllFiveChecksRunInOrder(t *testing.T) {
	c := newCLI(t)
	c.units = &fakeUnits{state: func(string, int) (UnitState, error) {
		return UnitState{SprintTicks: u64(1), Corrections: u64(0), Seen: seen(
			[]life.HealthSeen{{Seq: 1, Entity: "zombie"}}, []life.FlagSeen{{Seq: 2, Type: "creeper", Charged: true}})}, nil
	}}
	c.duel = duelThat(decided, nil)
	code := c.run(c.base("--status-url", "http://crew-control/units/{name}/v1/state", "--layout", layoutFile, "--json")...)
	var rep Report
	if err := json.Unmarshal(c.out.Bytes(), &rep); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	var names []string
	for _, r := range rep.Results {
		names = append(names, r.Check)
	}
	if strings.Join(names, ",") != "tp,zombie,creeper,phantom,sprint" {
		t.Errorf("checks = %v (exit %d)", names, code)
	}
}

// --- refusals ---------------------------------------------------------------

func TestRefusesATargetThatIsNotTheCrewLab(t *testing.T) {
	for name, lab := range map[string]string{
		"fwb pod":       "k8s:jdwillmsen-prd/jdwillmsen-fwb-minecraft-bedrock-0",
		"another pod":   "k8s:jdwillmsen-prd/jdwillmsen-minecraft-bedrock-0",
		"fwb compose":   "fwb",
		"no lab":        "",
		"prod word":     "k8s:ns/crew-lab-prod-0",
		"bare lab word": "k8s:ns/some-lab-0",
	} {
		t.Run(name, func(t *testing.T) {
			c := newCLI(t)
			if code := c.run("--lab", lab, "--control-dir", c.dir, "--checks", "tp"); code != exitRefused {
				t.Fatalf("exit = %d, want %d", code, exitRefused)
			}
			if c.built || len(c.lab.cmds) != 0 {
				t.Errorf("the server was reached for %q: built %v, cmds %v", lab, c.built, c.lab.cmds)
			}
		})
	}
}

func TestTheClusterLabPassesTheTargetGuard(t *testing.T) {
	c := newCLI(t)
	if code := c.run("--lab", arena.KnownLab, "--control-dir", c.dir, "--checks", "tp"); code == exitRefused {
		t.Fatalf("the real crew lab was refused at the guard (exit %d)", code)
	}
}

func TestRefusesWhileANonCrewPlayerIsOnline(t *testing.T) {
	c := newCLI(t)
	c.lab.online = "Rusty, Steve99"
	if code := c.run(c.base("--checks", "tp")...); code != exitRefused {
		t.Fatalf("exit = %d, want %d", code, exitRefused)
	}
	if !strings.Contains(c.err.String(), "Steve99") {
		t.Errorf("stderr = %q, want the stranger named", c.err.String())
	}
	for _, cmd := range c.lab.cmds {
		if cmd != "list" {
			t.Errorf("command %q sent with a stranger online", cmd)
		}
	}
}

func TestCrewNamesLetsExtraPlayersStayOnline(t *testing.T) {
	c := newCLI(t)
	c.lab.online = "Rusty, Steve99"
	if code := c.run(c.base("--checks", "tp", "--crew-names", "Rusty,Steve99")...); code != 0 {
		t.Fatalf("exit = %d, stderr %s", code, c.err.String())
	}
}

func TestCrewNamesDoesNotWidenWhoMayBeTouched(t *testing.T) {
	c := newCLI(t)
	c.lab.online = "Rusty, Steve99"
	c.lab.units["Steve99"] = &pose{X: 1, Y: 64, Z: 1}
	c.run(c.base("--checks", "tp", "--crew-names", "Rusty,Steve99")...)
	if c.lab.count(`tp "Steve99"`) != 0 || c.lab.count(`tp "Slate"`) != 0 {
		t.Errorf("a unit outside --units was teleported: %v", c.lab.cmds)
	}
}

func TestRefusesWhenTheTrainingSwitchIsOffBeforeTouchingTheServer(t *testing.T) {
	for name, content := range map[string]string{"off": "off\n", "missing": ""} {
		t.Run(name, func(t *testing.T) {
			c := newCLI(t)
			path := filepath.Join(c.dir, "training")
			if content == "" {
				os.Remove(path)
			} else if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if code := c.run(c.base("--checks", "tp")...); code != exitStopped {
				t.Fatalf("exit = %d, want %d", code, exitStopped)
			}
			if c.built || len(c.lab.cmds) != 0 {
				t.Errorf("the server was reached with training off: %v", c.lab.cmds)
			}
			if !strings.Contains(c.err.String(), "training switch is off") {
				t.Errorf("stderr = %q", c.err.String())
			}
		})
	}
}

func TestRefusesWhenTheStopFileExists(t *testing.T) {
	c := newCLI(t)
	stop := filepath.Join(t.TempDir(), "STOP")
	if err := os.WriteFile(stop, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := c.run(c.base("--checks", "tp", "--stop-file", stop)...); code != exitStopped {
		t.Fatalf("exit = %d, want %d", code, exitStopped)
	}
	if len(c.lab.cmds) != 0 {
		t.Errorf("commands sent with the stop file present: %v", c.lab.cmds)
	}
}

func TestTurningTheSwitchOffMidRunStopsIt(t *testing.T) {
	c := newCLI(t)
	c.units = &fakeUnits{state: func(string, int) (UnitState, error) { return UnitState{Seen: seen(nil, nil)}, nil }}
	c.lab.clk.onSleep = func() {
		if len(c.lab.mobs) > 0 {
			_ = os.WriteFile(filepath.Join(c.dir, "training"), []byte("off\n"), 0o600)
		}
	}
	code := c.run(c.base("--checks", "zombie,creeper", "--status-url", "http://u/{name}/state")...)
	if code != exitStopped {
		t.Fatalf("exit = %d, want %d\n%s%s", code, exitStopped, c.out.String(), c.err.String())
	}
	if c.lab.survivors() != 0 || !strings.Contains(c.out.String(), "STOPPED") || strings.Contains(c.out.String(), "creeper ") {
		t.Errorf("survivors %d, output:\n%s", c.lab.survivors(), c.out.String())
	}
}

// --- flags ------------------------------------------------------------------

func TestFlagValidation(t *testing.T) {
	c := newCLI(t)
	for name, tc := range map[string]struct {
		args []string
		msg  string
	}{
		"unknown check":         {[]string{"--checks", "tp,lava"}, "unknown check"},
		"no checks":             {[]string{"--checks", ","}, "names no check"},
		"state checks need url": {[]string{"--checks", "zombie"}, "--status-url"},
		"sprint needs layout":   {[]string{"--checks", "sprint", "--status-url", "http://u/{name}"}, "--layout"},
		"sprint needs two":      {[]string{"--checks", "sprint", "--status-url", "http://u/{name}", "--layout", layoutFile, "--units", "Rusty"}, "two names"},
		"bad unit name":         {[]string{"--checks", "tp", "--units", `Ru"sty`}, "not a unit name"},
		"no units":              {[]string{"--checks", "tp", "--units", ","}, "names no unit"},
		"timeout too long":      {[]string{"--checks", "tp", "--check-timeout", "2h"}, "--check-timeout"},
		"wall too long":         {[]string{"--checks", "tp", "--max-wall", "24h"}, "--max-wall"},
		"window too short":      {[]string{"--checks", "tp", "--window", "1s"}, "--window"},
		"duel timeout":          {[]string{"--checks", "tp", "--duel-timeout", "1s"}, "--duel-timeout"},
		"squads together":       {[]string{"--checks", "tp", "--red-squad", "lab-a"}, "go together"},
		"stray argument":        {[]string{"--checks", "tp", "extra"}, "unexpected argument"},
	} {
		t.Run(name, func(t *testing.T) {
			if code := c.run(append(c.base(), tc.args...)...); code != exitRefused {
				t.Fatalf("exit = %d, want %d", code, exitRefused)
			}
			if !strings.Contains(c.err.String(), tc.msg) {
				t.Errorf("stderr = %q, want %q", c.err.String(), tc.msg)
			}
			if c.built || len(c.lab.cmds) != 0 {
				t.Error("the server was reached with a malformed command line")
			}
		})
	}
}

func TestSquadsMustBeLabSquadsWithAToken(t *testing.T) {
	t.Setenv("CONTROL_TOKEN", "")
	if _, err := parseDuelSquads("http://cc", "lab-a", "lab-b"); err == nil || !strings.Contains(err.Error(), "CONTROL_TOKEN") {
		t.Errorf("err = %v, want the missing token refused", err)
	}
	t.Setenv("CONTROL_TOKEN", "x")
	for _, tc := range [][3]string{
		{"http://cc", "alpha", "lab-b"},
		{"http://cc", "lab-a", "lab-a"},
		{"ftp://cc", "lab-a", "lab-b"},
		{"http://cc", "lab-a", "lab-prod-b"},
	} {
		if _, err := parseDuelSquads(tc[0], tc[1], tc[2]); err == nil {
			t.Errorf("parseDuelSquads(%v) accepted", tc)
		}
	}
	if sq, err := parseDuelSquads("http://cc", "lab-a", "lab-b"); err != nil || sq.token != "x" {
		t.Errorf("sq = %+v, err = %v", sq, err)
	}
}

func TestRealDuelIsOneKitlessRoundOnTheFirstTwoUnits(t *testing.T) {
	c, err := parse([]string{"--lab", crewLab, "--control-dir", t.TempDir(), "--checks", "sprint", "--status-url", "http://u/{name}", "--layout", layoutFile}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	console := &arena.FakeConsole{}
	r, err := realDuel(c, console, func() string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	d := r.(*arena.Duel)
	if d.Rounds != 1 || len(d.Kits) != 0 || d.Red[0] != "Rusty" || d.Blue[0] != "Slate" || len(d.Duelists) != 2 {
		t.Errorf("duel = %+v", d)
	}
	if d.MaxWall > c.lim.CheckTimeout {
		t.Errorf("duel wall %s outlasts the check's %s", d.MaxWall, c.lim.CheckTimeout)
	}
}

// --- units -------------------------------------------------------------------

func TestHTTPUnitsReadsCountersAndSightings(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"tasks":{"fight":{"total":{"sprint_ticks":7,"movement_corrections":2}}},
			"seen":{"health":[{"seq":1,"entity":"zombie","health":20}],"flags":[{"seq":2,"type":"creeper","charged":true}]}}`))
	}))
	defer srv.Close()
	h, err := NewHTTPUnits(srv.URL + "/units/{name}/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	st, err := h.State(context.Background(), "Rusty")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/units/Rusty/v1/state" || *st.SprintTicks != 7 || *st.Corrections != 2 ||
		st.Seen.Health[0].Entity != "zombie" || !st.Seen.Flags[0].Charged {
		t.Errorf("state = %+v at %s", st, path)
	}
}

func TestHTTPUnitsSaysWhatAnOldBuildLacks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks":{"fight":{"total":{"swings":3}}}}`))
	}))
	defer srv.Close()
	h, _ := NewHTTPUnits(srv.URL + "/{name}")
	st, err := h.State(context.Background(), "Rusty")
	if err != nil || st.SprintTicks != nil || st.Corrections != nil || st.Seen != nil {
		t.Fatalf("state = %+v, err = %v: an old build's missing fields must read as absent, not zero", st, err)
	}
}

func TestHTTPUnitsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s: the unit proxy is read-only", r.Method)
		}
		http.Error(w, "no", http.StatusBadGateway)
	}))
	defer srv.Close()
	h, _ := NewHTTPUnits(srv.URL + "/{name}")
	if _, err := h.State(context.Background(), "Rusty"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v", err)
	}
	if _, err := NewHTTPUnits("http://u/state"); err == nil {
		t.Error("a template without {name} was accepted")
	}
}

func TestParsePoses(t *testing.T) {
	ps, err := parsePoses("[2026-10-30 12:00:00:123 INFO] [{\"position\":{\"x\":1.5,\"y\":64,\"z\":-2},\"yRot\":-90}]")
	if err != nil || len(ps) != 1 || ps[0] != (pose{1.5, 64, -2, -90}) {
		t.Errorf("poses = %+v, err = %v", ps, err)
	}
	if _, err := parsePoses("No targets matched selector"); err == nil {
		t.Error("a miss parsed")
	} else if _, ok := err.(errNoTargets); !ok {
		t.Errorf("err = %T, want errNoTargets", err)
	}
	if _, err := parsePoses("garbage"); err == nil {
		t.Error("garbage parsed")
	}
}

func TestYawHelpers(t *testing.T) {
	for _, tc := range []struct{ a, b, want float64 }{{0, 90, 90}, {170, -170, 20}, {359, 1, 2}, {180, -180, 0}} {
		if got := yawDiff(tc.a, tc.b); got < tc.want-1e-9 || got > tc.want+1e-9 {
			t.Errorf("yawDiff(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	if got := wrapYaw(270); got != -90 {
		t.Errorf("wrapYaw(270) = %v", got)
	}
}

func TestSystemClockSleepEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (systemClock{}).Sleep(ctx, time.Hour); err == nil {
		t.Error("a cancelled sleep ran its hour")
	}
	if err := (systemClock{}).Sleep(context.Background(), time.Millisecond); err != nil {
		t.Errorf("a short sleep failed: %v", err)
	}
	if (systemClock{}).Now().IsZero() {
		t.Error("no time")
	}
}
