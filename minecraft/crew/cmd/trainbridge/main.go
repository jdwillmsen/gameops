// Command trainbridge is the program the trainer's cmd evaluator runs for one
// fight: it reads {"profile":...,"scenario":...} on stdin, loads the profile
// onto the lab units, fights the scenario on the crew lab and prints the
// scorecards as JSON lines on stdout.
//
// usage: trainer run ... --evaluator "cmd:trainbridge --lab <lab> --layout <file>
//
//	--profile-dir <dir> --warriors A,B,C [--team-a A,B --team-b C,D --kits ...]"
//
// Instead of --profile-dir, --squad-url and --squad lab-<name> hand the
// profile to the lab squad's route with the token in CONTROL_TOKEN; a duel
// also needs --squad-b, since a squad has one profile.
//
// Mob scenarios come from the arena scenario book and are fought by the
// warriors; a duel scenario pits --team-a (the candidate) against --team-b
// (--opponent-profile, default the shipped one) for one round, and the b side
// of a mirrored pair starts the candidate on the other spawn line.
//
// With --status-url (a unit state endpoint with {name} for the warrior) mob
// scorecards carry the squad's damage taken, read from the units before and
// after the fight; without it they carry zero.
//
// It refuses (exit 2, no cards) unless training is enabled in the tacticsctl
// control directory and the target is the crew lab, and it re-checks the kill
// switch while fighting. A fight that the stop file, the kill switch or the
// per-fight timeout ends prints aborted cards and exits 0; a fault prints
// them and exits 1. The profile is withdrawn from the units on every exit.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	defaultCompose = "minecraft/crew/lab/compose.yaml"
	maxRequest     = 1 << 20
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := realMain(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func realMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	b, err := parse(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "trainbridge:", err)
		return 2
	}
	var req request
	if err := json.NewDecoder(io.LimitReader(stdin, maxRequest)).Decode(&req); err != nil {
		fmt.Fprintln(stderr, "trainbridge: reading the request:", err)
		return 2
	}
	cards, err := b.Run(ctx, req)
	enc := json.NewEncoder(stdout)
	for _, c := range cards {
		if encErr := enc.Encode(c); encErr != nil {
			fmt.Fprintln(stderr, "trainbridge:", encErr)
			return 1
		}
	}
	var ref refusal
	switch {
	case errors.As(err, &ref):
		fmt.Fprintln(stderr, "trainbridge: refused:", err)
		return 2
	case err != nil:
		fmt.Fprintln(stderr, "trainbridge:", err)
		return 1
	}
	return 0
}

func defaultControlDir() string {
	if d := os.Getenv("TACTICSCTL_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "crew-tactics")
}

func parse(args []string, errOut io.Writer) (*bridge, error) {
	fs := flag.NewFlagSet("trainbridge", flag.ContinueOnError)
	fs.SetOutput(errOut)
	lab := fs.String("lab", "", "crew lab compose service or k8s:<namespace>/<pod>")
	compose := fs.String("compose", defaultCompose, "compose file for a docker lab")
	layoutPath := fs.String("layout", "", "layout JSON from labcourses arena-layout")
	scenarios := fs.String("scenarios", "", "scenario book YAML (default embedded)")
	control := fs.String("control-dir", defaultControlDir(), "tacticsctl state directory holding the training switch")
	profileDir := fs.String("profile-dir", "", "directory the lab units read their candidate profile from")
	squadURL := fs.String("squad-url", "", "crew-control base URL; hands profiles to the lab squad's route instead of --profile-dir (token from CONTROL_TOKEN)")
	squad := fs.String("squad", "", "lab squad id the units fight in, lab- prefixed; with --squad-url")
	squadB := fs.String("squad-b", "", "lab squad id of the --team-b units, when it differs from --squad; a duel needs two squads")
	opponent := fs.String("opponent-profile", "", "profile JSON the duel opponent uses (default the shipped behaviour)")
	warriors := fs.String("warriors", "Rusty,Slate,Tinder", "comma-separated warriors for mob scenarios")
	warriorKits := fs.String("warrior-kits", "", "comma-separated catalog kits, one per --warriors name")
	crew := fs.String("crew-names", strings.Join(arena.DefaultCrewNames, ","), "players allowed online during a fight")
	teamA := fs.String("team-a", "", "duel team that fights with the candidate profile")
	teamB := fs.String("team-b", "", "duel team that fights with the opponent profile; as many as --team-a")
	kits := fs.String("kits", "", "comma-separated catalog kits for a duel, one per slot, handed to both teams")
	stopFile := fs.String("stop-file", "", "end the fight when this file exists")
	fightTimeout := fs.Duration("fight-timeout", 10*time.Minute, "hard limit for one fight, set-up included")
	roundTimeout := fs.Duration("round-timeout", 2*time.Minute, "limit for the duel round itself")
	poll := fs.Duration("poll", 3*time.Second, "delay between standing checks")
	settle := fs.Duration("settle", 25*time.Second, "wait for the arena chunks to load")
	placeWait := fs.Duration("place-wait", 3*time.Second, "wait after placing the squad")
	countdown := fs.Duration("countdown", 5*time.Second, "duel wait between the last set-up step and the start")
	kitGap := fs.Duration("kit-gap", 3*time.Second, "duel wait after each kit")
	spacing := fs.Int("spacing", 12, "duel distance of each line from the floor centre")
	var damage arena.DamageSource
	fs.Func("status-url", "unit state url with {name} for the warrior, e.g. http://crew-control/units/{name}/v1/state; fills damage taken on mob scorecards (off by default)", func(v string) error {
		src, err := arena.NewHTTPDamage(v)
		if err != nil {
			return err
		}
		damage = src
		return nil
	})
	emit := fs.String("emit", "all", "duel cards to print: all, or candidate for the candidate's alone")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *lab == "" {
		return nil, errors.New("--lab is required")
	}
	if *layoutPath == "" {
		return nil, errors.New("--layout is required")
	}
	sink, err := newSink(*profileDir, *squadURL, *squad, *squadB, splitNames(*teamA), splitNames(*teamB))
	if err != nil {
		return nil, err
	}
	if *emit != "all" && *emit != "candidate" {
		return nil, fmt.Errorf("--emit must be all or candidate, got %q", *emit)
	}
	if *fightTimeout <= 0 || *fightTimeout > arena.MaxDuelWall {
		return nil, fmt.Errorf("--fight-timeout must be positive and at most %s", arena.MaxDuelWall)
	}
	layout, err := arena.LoadLayout(*layoutPath)
	if err != nil {
		return nil, err
	}
	book, err := loadBook(*scenarios)
	if err != nil {
		return nil, err
	}
	console, err := newConsole(*lab, *compose)
	if err != nil {
		return nil, err
	}
	opp := tactics.Profile{}
	if *opponent != "" {
		raw, err := os.ReadFile(*opponent)
		if err != nil {
			return nil, err
		}
		if opp, err = tactics.Parse(raw); err != nil {
			return nil, fmt.Errorf("opponent profile: %w", err)
		}
	}
	return &bridge{
		ControlDir: *control, Lab: *lab, StopFile: *stopFile,
		Console: console, Layout: layout, Book: book, Sink: sink, Opponent: opp,
		Warriors: splitNames(*warriors), WarriorKits: splitNames(*warriorKits), Crew: splitNames(*crew),
		TeamA: splitNames(*teamA), TeamB: splitNames(*teamB), DuelKits: splitNames(*kits),
		FightTimeout: *fightTimeout, RoundTimeout: *roundTimeout, Poll: *poll, Settle: *settle,
		PlaceWait: *placeWait, Countdown: *countdown, KitGap: *kitGap, Spacing: *spacing,
		Emit: *emit, Out: errOut, Damage: damage,
	}, nil
}

func loadBook(path string) (*arena.ScenarioBook, error) {
	if path == "" {
		return arena.DefaultScenarios()
	}
	waves, err := arena.DefaultWaves()
	if err != nil {
		return nil, err
	}
	return arena.LoadScenarioFile(path, waves.Tiers)
}

func newConsole(lab, compose string) (arena.Console, error) {
	if target, ok := strings.CutPrefix(lab, "k8s:"); ok {
		namespace, pod, ok := strings.Cut(target, "/")
		if !ok || namespace == "" || pod == "" {
			return nil, fmt.Errorf("%q is not k8s:<namespace>/<pod>", lab)
		}
		return arena.NewKubectlConsole(namespace, pod), nil
	}
	return arena.NewDockerConsole(compose, lab), nil
}

func splitNames(list string) []string {
	var names []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// newSink picks where candidate profiles go: a directory, or the squad route.
// The token comes from the environment only, so it appears in no argument
// list, log or message.
func newSink(profileDir, squadURL, squad, squadB string, teamA, teamB []string) (ProfileSink, error) {
	if squadURL == "" {
		if profileDir == "" {
			return nil, errors.New("--profile-dir or --squad-url is required")
		}
		return FileSink{Dir: profileDir}, nil
	}
	if profileDir != "" {
		return nil, errors.New("--profile-dir and --squad-url are alternatives")
	}
	base, err := url.Parse(squadURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("--squad-url: want an http or https URL with a host")
	}
	token := strings.TrimSpace(os.Getenv("CONTROL_TOKEN"))
	if token == "" {
		return nil, errors.New("CONTROL_TOKEN is required with --squad-url")
	}
	if !strings.HasPrefix(squad, labSquadPrefix) {
		return nil, fmt.Errorf("--squad: %q is not a lab squad (want a %s prefix)", squad, labSquadPrefix)
	}
	sink := SquadSink{Base: base, Default: squad, Token: token, Client: &http.Client{}}
	if len(teamB) > 0 {
		if !strings.HasPrefix(squadB, labSquadPrefix) {
			return nil, fmt.Errorf("--squad-b: %q is not a lab squad (want a %s prefix)", squadB, labSquadPrefix)
		}
		// One squad has one profile, so the two teams of a duel cannot share.
		if len(teamA) > 0 && squadB == squad {
			return nil, errors.New("--squad-b must differ from --squad for a duel")
		}
		sink.Squads = map[string]string{}
		for _, u := range teamB {
			sink.Squads[u] = squadB
		}
	}
	return sink, nil
}
