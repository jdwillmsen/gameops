// Command arenarepair compares the larger Colosseum with what the generator
// planned and restores the cells that differ. It is lab-only.
//
// usage: arenarepair plan  <source> [options]
//
//	arenarepair apply <source> --lab <lab> [--execute] [options]
//	arenarepair snapshot --lab <lab> --out <dir> [options]
//
// <source> is exactly one of
//
//	--world <dir>   a copy of the world's db directory (or its parent)
//	--scan <file>   fill/setblock lines stating what is in the world
//	--lab <lab>     fetch a fresh copy from the server (save hold/resume,
//	                a tar of db; no block is read or written through the console)
//
// <lab> is the compose service of the local lab, or k8s:<namespace>/<pod>.
// plan never writes anything. apply is a dry run until --execute is given, and
// even then refuses while a fight is running or a non-crew player is online.
// Both print a report (text, or one JSON object with --json).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/repair"
)

const (
	defaultLayout    = "minecraft/crew/lab/colosseum/layout.json"
	defaultGenerator = "minecraft/crew/lab/colosseum/colosseum.py"
	defaultCompose   = "minecraft/crew/lab/compose.yaml"
	defaultWorld     = "/data/worlds/crew-lab"
	// colosseumCentreX/Z is the larger arena. The older one at x 0 has no
	// generator plan, and repairing it against this plan would flatten it.
	colosseumCentreX = -300
	colosseumCentreZ = -758
)

// deps are the parts a test replaces.
type deps struct {
	console  func(lab, compose string) (arena.Console, error)
	snapshot func(c arena.Console, lab, compose, worldPath string) repair.Snapshotter
	generate func(generator, outDir string) error
	// world opens a copied db; tests swap in a fixed scan.
	world func(dir string, plan []repair.Op) (repair.Reader, func() error, error)
}

func main() {
	// An interrupt cancels the save-query poll and the tar stream; the
	// snapshot still sends save resume before the process exits.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, realDeps())
	stop()
	os.Exit(code)
}

func realDeps() deps {
	return deps{
		console:  newConsole,
		snapshot: newSnapshotter,
		world: func(dir string, plan []repair.Op) (repair.Reader, func() error, error) {
			w, err := repair.OpenWorld(dir, plan)
			if err != nil {
				return nil, nil, err
			}
			return w, w.Close, nil
		},
		generate: func(generator, outDir string) error {
			out, err := exec.Command("python3", generator, outDir).CombinedOutput()
			if err != nil {
				return fmt.Errorf("generator %s: %w: %s", generator, err, strings.TrimSpace(string(out)))
			}
			return nil
		},
	}
}

const usageText = `usage: arenarepair plan  (--world DIR | --scan FILE | --lab LAB) [--layout F] [--plan-dir D] [--json]
       arenarepair apply (--world DIR | --scan FILE | --lab LAB) --lab LAB [--execute] [--crew-names a,b] [--quiet 30s] [--json]
       arenarepair snapshot --lab LAB --out DIR`

type options struct {
	layout, planDir, generator string
	world, scan, lab, compose  string
	worldPath, keep, out       string
	crew, confirmLab           string
	quiet, loadWait            time.Duration
	execute, asJSON, commands  bool
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, usageText)
		return 2
	}
	cmd := args[0]
	if cmd != "plan" && cmd != "apply" && cmd != "snapshot" {
		fmt.Fprintln(stderr, usageText)
		return 2
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.layout, "layout", defaultLayout, "layout JSON of the larger Colosseum")
	fs.StringVar(&o.planDir, "plan-dir", "", "directory holding the generator's clear.cmds and build.cmds (default: run the generator)")
	fs.StringVar(&o.generator, "generator", defaultGenerator, "generator script run when --plan-dir is empty")
	fs.StringVar(&o.world, "world", "", "copy of the world's db directory, or its parent")
	fs.StringVar(&o.scan, "scan", "", "scan file of fill/setblock lines")
	fs.StringVar(&o.lab, "lab", "", "lab compose service or k8s:<namespace>/<pod>")
	fs.StringVar(&o.confirmLab, "confirm-lab", "", "repeat --lab here to use a target other than the crew lab")
	fs.StringVar(&o.compose, "compose", defaultCompose, "compose file for a docker lab")
	fs.StringVar(&o.worldPath, "world-path", defaultWorld, "world directory inside the server container")
	fs.StringVar(&o.keep, "keep", "", "directory to keep the fetched world copy in (default: a temp dir, removed)")
	fs.StringVar(&o.out, "out", "", "snapshot: directory to write db/ into")
	fs.StringVar(&o.crew, "crew-names", strings.Join(arena.DefaultCrewNames, ","), "players allowed online while repairing")
	fs.DurationVar(&o.quiet, "quiet", repair.DefaultQuiet, "how long no tagged mob must show before repairing")
	fs.DurationVar(&o.loadWait, "load-wait", repair.DefaultLoadWait, "pause after adding ticking areas, before the first fill")
	fs.BoolVar(&o.execute, "execute", false, "apply: really send the repair (default is a dry run)")
	fs.BoolVar(&o.commands, "commands", false, "also print the repair commands (text report only)")
	fs.BoolVar(&o.asJSON, "json", false, "print the report as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if cmd == "snapshot" {
		return snapshotCmd(ctx, o, stderr, stdout, d)
	}
	if cmd == "plan" && o.execute {
		fmt.Fprintln(stderr, "arenarepair: plan never writes; --execute belongs to apply")
		return 2
	}
	if cmd == "apply" && o.lab == "" {
		fmt.Fprintln(stderr, "arenarepair: apply needs --lab, the server to repair")
		return 2
	}
	if err := checkLab(o); err != nil {
		fmt.Fprintln(stderr, "arenarepair: "+err.Error())
		return 2
	}
	if cmd == "apply" && o.execute && (o.world != "" || o.scan != "") {
		fmt.Fprintln(stderr, "arenarepair: apply --execute scans the server itself (--lab only); a --world or --scan copy may be stale, use it with plan or a dry run")
		return 2
	}
	sources := 0
	for _, s := range []string{o.world, o.scan} {
		if s != "" {
			sources++
		}
	}
	if sources > 1 || (sources == 1 && cmd == "plan" && o.lab != "") {
		fmt.Fprintln(stderr, "arenarepair: give exactly one of --world, --scan, --lab as the source")
		return 2
	}
	if sources == 0 && o.lab == "" {
		fmt.Fprintln(stderr, "arenarepair: no source: give --world, --scan or --lab")
		return 2
	}
	if err := execute(ctx, cmd, o, stdout, stderr, d); err != nil {
		var busy *arena.NotAloneError
		fmt.Fprintln(stderr, "arenarepair: "+err.Error())
		if errors.Is(err, repair.ErrFightActive) || errors.As(err, &busy) {
			return 2
		}
		return 1
	}
	return 0
}

func execute(ctx context.Context, cmd string, o options, stdout, stderr io.Writer, d deps) error {
	layout, err := arena.LoadLayout(o.layout)
	if err != nil {
		return err
	}
	if layout.Center.X != colosseumCentreX || layout.Center.Z != colosseumCentreZ {
		return fmt.Errorf("layout %s is centred %d,%d; this tool repairs only the larger Colosseum at %d,%d",
			o.layout, layout.Center.X, layout.Center.Z, colosseumCentreX, colosseumCentreZ)
	}
	plan, err := loadPlan(o, d)
	if err != nil {
		return err
	}

	var console arena.Console
	if o.lab != "" {
		if console, err = d.console(o.lab, o.compose); err != nil {
			return err
		}
	}
	var live repair.Reader
	var scanAt time.Time
	switch {
	case o.scan != "":
		f, err := os.Open(o.scan)
		if err != nil {
			return err
		}
		defer f.Close()
		ops, err := repair.ParseOps(f)
		if err != nil {
			return fmt.Errorf("scan %s: %w", o.scan, err)
		}
		live = repair.OpsReader{Ops: ops}
	default:
		dir := o.world
		if dir == "" {
			snap, err := fetchWorld(ctx, o, console, d, stderr)
			if err != nil {
				return err
			}
			dir, scanAt = snap.Dir, snap.At
			if o.keep == "" {
				defer os.RemoveAll(filepath.Dir(dir))
			}
		}
		if dir, err = dbDir(dir); err != nil {
			return err
		}
		w, closeWorld, err := d.world(dir, plan)
		if err != nil {
			return err
		}
		defer closeWorld()
		live = w
	}

	_, p, rep, err := repair.Compute(plan, live, layout)
	if err != nil {
		return err
	}
	rep.ScanAt = scanAt
	if !scanAt.IsZero() && cmd == "apply" && o.execute {
		fmt.Fprintf(stderr, "arenarepair: the scan is from %s; repairing now\n", scanAt.Format(time.RFC3339))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if cmd == "plan" {
		rep.Status = repair.StatusPlanned
		rep.Dry = true
		return printReport(stdout, rep, o, p.Repairs)
	}
	crew := splitNames(o.crew)
	ao := repair.Options{DryRun: !o.execute, Crew: crew, Quiet: o.quiet, LoadWait: o.loadWait, Stop: ctx.Err, Sleep: func(d time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}}
	if !o.execute {
		fmt.Fprintln(stderr, "arenarepair: dry run; pass --execute to send the repair")
	}
	aerr := repair.Apply(console, p, &rep, ao)
	if perr := printReport(stdout, rep, o, p.Repairs); perr != nil && aerr == nil {
		aerr = perr
	}
	return aerr
}

func loadPlan(o options, d deps) ([]repair.Op, error) {
	dir := o.planDir
	if dir == "" {
		tmp, err := os.MkdirTemp("", "arenarepair-plan-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		if err := d.generate(o.generator, tmp); err != nil {
			return nil, err
		}
		dir = tmp
	}
	var all []repair.Op
	for _, name := range []string{"clear.cmds", "build.cmds"} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		ops, err := repair.ParseOps(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		all = append(all, ops...)
	}
	return all, nil
}

func fetchWorld(ctx context.Context, o options, c arena.Console, d deps, stderr io.Writer) (repair.Snapshot, error) {
	dest := o.keep
	if dest == "" {
		var err error
		if dest, err = os.MkdirTemp("", "arenarepair-world-"); err != nil {
			return repair.Snapshot{}, err
		}
	} else if err := os.MkdirAll(dest, 0o755); err != nil {
		return repair.Snapshot{}, err
	}
	fmt.Fprintln(stderr, "arenarepair: copying the world db (save hold ... save resume)")
	snap, err := d.snapshot(c, o.lab, o.compose, o.worldPath).Fetch(ctx, dest)
	if err != nil {
		if o.keep == "" {
			os.RemoveAll(dest)
		}
		return repair.Snapshot{}, err
	}
	return snap, nil
}

const knownLab = arena.KnownLab

func checkLab(o options) error { return arena.CheckLab(o.lab, o.confirmLab) }

// dbDir accepts the db directory itself or the world directory above it.
func dbDir(dir string) (string, error) {
	for _, c := range []string{dir, filepath.Join(dir, "db")} {
		if _, err := os.Stat(filepath.Join(c, "CURRENT")); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("%s holds no LevelDB (no CURRENT file, here or in db/)", dir)
}

func snapshotCmd(ctx context.Context, o options, stderr, stdout io.Writer, d deps) int {
	if o.lab == "" || o.out == "" {
		fmt.Fprintln(stderr, "arenarepair: snapshot needs --lab and --out")
		return 2
	}
	if err := checkLab(o); err != nil {
		fmt.Fprintln(stderr, "arenarepair: "+err.Error())
		return 2
	}
	c, err := d.console(o.lab, o.compose)
	if err != nil {
		fmt.Fprintln(stderr, "arenarepair: "+err.Error())
		return 1
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		fmt.Fprintln(stderr, "arenarepair: "+err.Error())
		return 1
	}
	snap, err := d.snapshot(c, o.lab, o.compose, o.worldPath).Fetch(ctx, o.out)
	if err != nil {
		fmt.Fprintln(stderr, "arenarepair: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, snap.Dir)
	return 0
}

func printReport(w io.Writer, rep repair.Report, o options, cmds []string) error {
	if o.asJSON {
		return json.NewEncoder(w).Encode(rep)
	}
	rep.Write(w)
	if o.commands {
		for _, c := range cmds {
			fmt.Fprintln(w, c)
		}
	}
	return nil
}

func newConsole(lab, compose string) (arena.Console, error) { return arena.NewLabConsole(lab, compose) }

func newSnapshotter(c arena.Console, lab, compose, worldPath string) repair.Snapshotter {
	tar := []string{"tar", "-C", worldPath, "-cf", "-", "db"}
	var argv []string
	if target, ok := strings.CutPrefix(lab, "k8s:"); ok {
		ns, pod, _ := strings.Cut(target, "/")
		argv = append([]string{"kubectl", "-n", ns, "exec", pod, "--"}, tar...)
	} else {
		argv = append([]string{"docker", "compose", "-f", compose, "exec", "-T", lab}, tar...)
	}
	return repair.Snapshotter{Console: c, Tar: argv}
}

func splitNames(list string) []string {
	var names []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names
}
