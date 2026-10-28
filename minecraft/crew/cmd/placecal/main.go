// Command placecal calibrates block placement against a throwaway lab: it
// joins creative units, places every placeable creative item from a pruned
// matrix of clicks, reads back the state the server made of each, and writes
// the table internal/place embeds. It is lab-only.
//
// usage: placecal --lab lab --addr 127.0.0.1:19397 --compose lab.yaml [options]
//
// <lab> is the compose service of the lab, as for labcreative: only a service
// named `lab` passes unchecked, anything else needs --confirm-lab repeating
// it, and a target naming fwb is always refused. The lab address must be
// loopback. No cluster is ever commanded.
//
// It always stops at --max-duration, at --max-placements, or on an interrupt,
// breaks every block it placed, removes its ticking areas, and writes the
// table; a run that did not finish writes a table marked partial that lists
// what it did not reach.
//
// Exit status: 0 done, 1 failed, 2 refused or bad usage, 3 partial table.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sandertv/go-raknet"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcproto"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

const (
	defaultCompose     = "minecraft/crew/lab/compose.yaml"
	defaultOut         = "internal/place/table"
	defaultMaxDuration = 60 * time.Minute
	defaultMaxPlaced   = 120000
	maxDurationLimit   = 12 * time.Hour
	maxUnits           = 8
)

var unitName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)

type config struct {
	lab, confirmLab, compose, addr, units, out, only, serverVersion, mergeInto, pitches string
	maxDuration                                                                         time.Duration
	maxPlacements                                                                       int64
	maxItems                                                                            int
	floorY, originX, originZ                                                            int
	probe, probeOut                                                                     string
	force                                                                               bool
	skipDerived, verify, noInfer                                                        bool
	placeWait                                                                           time.Duration
	missRetries                                                                         int
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("placecal", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c config
	fs.StringVar(&c.lab, "lab", "", "lab compose service (`lab`), or another service with --confirm-lab")
	fs.StringVar(&c.confirmLab, "confirm-lab", "", "repeat --lab here to use a service not named lab")
	fs.StringVar(&c.compose, "compose", defaultCompose, "compose file of the lab")
	fs.StringVar(&c.addr, "addr", "", "the lab's RakNet address, host:port, loopback only")
	fs.StringVar(&c.units, "units", "PlcA,PlcB,PlcC,PlcD", "comma-separated in-game names of the units to join")
	fs.StringVar(&c.out, "out", defaultOut, "directory the table <server-version>.json.gz is written to")
	fs.StringVar(&c.only, "only", "", "comma-separated item names to calibrate instead of all")
	fs.StringVar(&c.serverVersion, "server-version", "", "server version for the file name, read from the lab when empty")
	fs.StringVar(&c.pitches, "pitches", "", "comma-separated pitch angles to try instead of 0,-60,60; the first must be 0")
	fs.StringVar(&c.mergeInto, "merge-into", "", "an earlier table to fold this run into: its blocks stay, this run's replace or add, and its not-covered list drops what this run covered")
	fs.DurationVar(&c.maxDuration, "max-duration", defaultMaxDuration, "stop after this long and write a partial table")
	fs.Int64Var(&c.maxPlacements, "max-placements", defaultMaxPlaced, "stop after this many clicks")
	fs.IntVar(&c.maxItems, "max-items", 0, "calibrate at most this many items (0 for all)")
	fs.IntVar(&c.floorY, "floor-y", 220, "height of the platforms' stone floor")
	fs.IntVar(&c.originX, "origin-x", 1000, "x of the first platform")
	fs.IntVar(&c.originZ, "origin-z", 1000, "z of the platforms")
	fs.BoolVar(&c.force, "force", false, "replace an existing table that holds more blocks than this run's")
	fs.BoolVar(&c.skipDerived, "skip-derived", false, "skip the neighbour pass")
	fs.BoolVar(&c.verify, "verify", false, "gap check: with --merge-into, run again (in full, no inference) the items whose blocks disagree with their family and the not-covered ones, then record what still disagrees")
	fs.BoolVar(&c.noInfer, "no-infer", false, "run every item's sweep in full instead of inferring from a sibling")
	fs.DurationVar(&c.placeWait, "place-wait", placeWait, "how long a click may take to show a block before it counts as a no-op")
	fs.IntVar(&c.missRetries, "miss-retries", missRetries, "how many more times a click that placed nothing is sent")
	fs.StringVar(&c.probe, "probe", "", "run kept experiments instead of a calibration: a comma list of "+probeUsage()+", or all")
	fs.StringVar(&c.probeOut, "probe-out", "", "file the probe report (markdown) is also written to")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	placeWait, missRetries = c.placeWait, c.missRetries
	if c.verify {
		c.noInfer = true
	}
	names, err := validate(c)
	if err != nil {
		fmt.Fprintln(stderr, "placecal: "+err.Error())
		return 2
	}
	if c.pitches != "" {
		if pitches, err = parsePitches(c.pitches); err != nil {
			fmt.Fprintln(stderr, "placecal: "+err.Error())
			return 2
		}
	}
	console, err := arena.NewLabConsole(c.lab, c.compose)
	if err != nil {
		fmt.Fprintln(stderr, "placecal: "+err.Error())
		return 2
	}
	if c.probe != "" {
		if _, err := probeNames(c.probe); err != nil {
			fmt.Fprintln(stderr, "placecal: "+err.Error())
			return 2
		}
		if err := executeProbe(ctx, c, names, &safeConsole{c: console}, stdout); err != nil {
			fmt.Fprintln(stderr, "placecal: "+err.Error())
			return 1
		}
		return 0
	}
	partial, err := execute(ctx, c, names, &safeConsole{c: console}, stdout)
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "placecal: "+err.Error())
		return 1
	case partial:
		return 3
	}
	return 0
}

func validate(c config) ([]string, error) {
	if c.lab == "" {
		return nil, errors.New("--lab is required: the server to command")
	}
	if err := arena.CheckLab(c.lab, c.confirmLab); err != nil {
		return nil, err
	}
	if strings.HasPrefix(c.lab, "k8s:") {
		return nil, fmt.Errorf("--lab %q is a cluster pod; placecal only drives a local compose lab", c.lab)
	}
	for _, v := range []string{c.lab, c.addr, c.compose} {
		if strings.Contains(strings.ToLower(v), "fwb") {
			return nil, fmt.Errorf("%q names the FWB server; this tool never touches it", v)
		}
	}
	host, _, err := net.SplitHostPort(c.addr)
	if err != nil {
		return nil, fmt.Errorf("--addr %q must be host:port", c.addr)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("--addr host %q is not loopback; the lab is local", host)
	}
	var names []string
	seen := map[string]bool{}
	for _, n := range strings.Split(c.units, ",") {
		if n = strings.TrimSpace(n); n == "" || seen[n] {
			continue
		}
		if !unitName.MatchString(n) {
			return nil, fmt.Errorf("unit name %q must match %s", n, unitName)
		}
		seen[n] = true
		names = append(names, n)
	}
	switch {
	case len(names) == 0 || len(names) > maxUnits:
		return nil, fmt.Errorf("--units must name 1 to %d units", maxUnits)
	case c.maxDuration <= 0 || c.maxDuration > maxDurationLimit:
		return nil, fmt.Errorf("--max-duration must be above 0 and at most %s", maxDurationLimit)
	case c.maxPlacements < 1:
		return nil, errors.New("--max-placements must be at least 1")
	}
	if err := checkOrigin(c.originX, c.originZ, c.floorY, len(names)); err != nil {
		return nil, err
	}
	if c.serverVersion != "" {
		if err := checkVersion(c.serverVersion); err != nil {
			return nil, err
		}
	}
	if err := checkCompose(c.compose, c.lab); err != nil {
		return nil, err
	}
	return names, nil
}

func parsePitches(s string) ([]int, error) {
	var out []int
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < -90 || n > 90 {
			return nil, fmt.Errorf("--pitches %q: %q is not an angle between -90 and 90", s, f)
		}
		out = append(out, n)
	}
	if len(out) == 0 || out[0] != 0 {
		return nil, errors.New("--pitches must start with 0")
	}
	return out, nil
}

// safeConsole serialises the lab console, which keeps a log cursor.
type safeConsole struct {
	mu sync.Mutex
	c  arena.Console
}

func (s *safeConsole) Exec(cmd string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c.Exec(cmd)
}

// retry runs a prep command until its reply shows it worked: the first
// commands after a ticking area is added fail until its chunks load.
func (s *safeConsole) retry(ctx context.Context, cmd, ok string) error {
	var last string
	for try := 0; try < 40; try++ {
		out, err := s.Exec(cmd)
		if err != nil {
			return err
		}
		if strings.Contains(out, ok) {
			return nil
		}
		last = strings.TrimSpace(out)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%q never reported %q (last reply %q)", cmd, ok, last)
}

// denied are items a click would turn into something other than a block in
// the bay: entities, liquids, projectiles, fire, spawners.
var denied = []string{
	"bucket", "spawn_egg", "boat", "minecart", "armor_stand", "end_crystal", "item_frame", "painting",
	"lead", "ender_pearl", "snowball", "potion", "experience_bottle", "wind_charge", "firework",
	"trident", "bow", "crossbow", "ender_eye", "flint_and_steel", "fire_charge", "name_tag", "spawner",
	"fishing_rod", "bundle", "banner_pattern", "totem", "elytra", "mace", "boat_with_chest", "balloon",
}

func skipItem(name string) bool {
	if name == "air" || name == "egg" || name == "blue_egg" || name == "brown_egg" {
		return true
	}
	for _, d := range denied {
		if strings.Contains(name, d) {
			return true
		}
	}
	return false
}

func execute(ctx context.Context, c config, names []string, con *safeConsole, out io.Writer) (partial bool, err error) {
	start := time.Now()
	say := func(f string, a ...any) { fmt.Fprintf(out, "placecal: "+f+"\n", a...) }
	vocab, nStates, vocabSource := loadVocabulary()
	say("vocabulary: %d blocks, %d states (%s)", len(vocab), nStates, vocabSource)

	if c.verify {
		if c.mergeInto == "" {
			return false, errors.New("--verify needs --merge-into, the table to check")
		}
		redo, gaps, err := verifyItems(c.mergeInto)
		if err != nil {
			return false, err
		}
		say("gap check: %d block(s) disagree with their family; %d item(s) to run again", len(gaps), len(redo))
		for _, g := range gaps {
			say("  gap %s", g)
		}
		if len(redo) == 0 {
			return false, nil
		}
		c.only = strings.Join(redo, ",")
	}

	pong, err := raknet.PingContext(ctx, c.addr)
	if err != nil {
		return false, fmt.Errorf("ping %s: %w", c.addr, err)
	}
	ad, err := mcproto.ParsePong(string(pong))
	if err != nil {
		return false, err
	}
	if c.serverVersion == "" {
		c.serverVersion = ad.Version
	}
	if err := checkVersion(c.serverVersion); err != nil {
		return false, err
	}
	say("server version %s, protocol %d", c.serverVersion, ad.Protocol)

	runCtx, cancel := context.WithTimeout(ctx, c.maxDuration)
	defer cancel()

	var units []*unit
	defer func() {
		for _, u := range units {
			u.close()
		}
	}()
	for _, n := range names {
		var u *unit
		for try := 0; try < 4; try++ {
			if u, err = join(runCtx, c.addr, n, out); err == nil {
				if u.ready(runCtx, 20*time.Second) {
					break
				}
				u.close()
				err = errors.New("no position from the server")
			}
			say("join %s: %v; retrying", n, err)
			time.Sleep(2 * time.Second)
		}
		if err != nil {
			return false, err
		}
		units = append(units, u)
	}
	say("%d unit(s) joined", len(units))

	r := &calRun{cfg: c, vocab: vocab, con: con, budget: c.maxPlacements, families: map[string]*family{}, derivedBy: map[string]map[string]bool{}}
	// Whatever happened, the lab keeps none of our areas, platforms, marker
	// or gamerule changes. Cleanup is best effort and reports what it missed.
	var (
		areas   []string
		built   []bay
		marked  []bay
		saved   map[string]string
		cleanCt = context.WithoutCancel(runCtx)
	)
	defer func() {
		for _, b := range marked {
			if err := clearMarker(cleanCt, con, b); err != nil {
				say("cleanup: marker: %v", err)
			}
		}
		for _, b := range built {
			if err := clearBay(cleanCt, con, b); err != nil {
				say("cleanup: bay at %d,%d,%d: %v", b.ox, b.f, b.oz, err)
			}
		}
		if saved != nil {
			if err := restoreGamerules(con, saved); err != nil {
				say("cleanup: gamerules: %v", err)
			}
		}
		for _, a := range areas {
			_, _ = con.Exec("tickingarea remove " + a)
		}
	}()
	removeStaleAreas(con)
	if saved, err = readGamerules(con); err != nil {
		saved = nil
		return false, err
	}
	if err := freezeGamerules(con); err != nil {
		return false, err
	}
	var workers []*worker
	for i, u := range units {
		b := bayAt(c.originX, c.floorY, c.originZ, i)
		area := areaName(i)
		areas = append(areas, area)
		if err := addArea(con, b, area); err != nil {
			return false, err
		}
		if err := checkBayEmpty(runCtx, con, b); err != nil {
			return false, err
		}
		built = append(built, b)
		w := &worker{r: r, u: u, bay: b, log: func(f string, a ...any) { say(u.name+": "+f, a...) }}
		if err := con.retry(runCtx, fmt.Sprintf("fill %d %d %d %d %d %d stone", b.ox+bayWest, b.f, b.oz+bayNorth, b.ox+bayEast, b.f, b.oz+baySouth), "blocks filled"); err != nil {
			return false, err
		}
		for _, cmd := range b.prepCommands()[1:] {
			ok := "blocks filled"
			if strings.HasPrefix(cmd, "setblock") {
				ok = "Block placed"
			}
			if err := con.retry(runCtx, cmd, ok); err != nil {
				return false, err
			}
		}
		if _, err := con.Exec(fmt.Sprintf("tp %q %.1f %d %.1f", u.name, b.stand().X(), b.f+1, b.stand().Z())); err != nil {
			return false, err
		}
		if _, err := con.Exec(fmt.Sprintf("gamemode creative %q", u.name)); err != nil {
			return false, err
		}
		if !u.teleportedTo(runCtx, b.stand(), 20*time.Second) {
			return false, fmt.Errorf("%s never arrived at its platform", u.name)
		}
		if !u.waitFor(runCtx, 30*time.Second, func() bool {
			h, ok := u.hash(b.s0())
			return ok && h != airHash && u.tracker.Creative()
		}) {
			return false, fmt.Errorf("%s: platform not loaded or not creative", u.name)
		}
		marked = append(marked, b)
		if err := checkMarker(runCtx, con, u, b); err != nil {
			return false, err
		}
		if err := clearMarker(runCtx, con, b); err != nil {
			return false, err
		}
		workers = append(workers, w)
		say("%s ready at its platform", u.name)
	}

	items, err := itemList(units[0], c)
	if err != nil {
		return false, err
	}
	say("%d creative items to calibrate", len(items))
	asked := map[string]bool{}
	for _, it := range items {
		asked[it.name] = true
	}

	control := sneakControl(runCtx, workers[0])
	say("sneak control: %s", control)

	queue := make(chan item, len(items))
	for _, it := range items {
		queue <- it
	}
	close(queue)

	var (
		mu       sync.Mutex
		finished = map[string]bool{}
		skipped  = map[string]string{}
		doneN    int
		wg       sync.WaitGroup
		panicMu  sync.Mutex
		panics   []string
	)
	for _, w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer w.restore(context.WithoutCancel(runCtx))
			defer func() {
				if p := recover(); p != nil {
					panicMu.Lock()
					panics = append(panics, fmt.Sprintf("%s: panic: %v", w.u.name, p))
					panicMu.Unlock()
				}
			}()
			for it := range queue {
				if runCtx.Err() != nil {
					return
				}
				ir, err := w.calibrate(runCtx, it)
				mu.Lock()
				switch {
				case err == nil:
					finished[it.name] = true
					r.mu.Lock()
					r.done = append(r.done, ir)
					r.mu.Unlock()
					doneN++
					if doneN%25 == 0 {
						say("%d/%d items, %d clicks, %s", doneN, len(items), r.placed.Load(), time.Since(start).Round(time.Second))
					}
				case isFatal(err) || !w.u.alive():
					skipped[it.name] = "run stopped: " + err.Error()
					mu.Unlock()
					return
				default:
					skipped[it.name] = err.Error()
					w.log("%s skipped: %v", it.name, err)
				}
				mu.Unlock()
				w.restore(context.WithoutCancel(runCtx))
			}
		}()
	}
	wg.Wait()
	for _, p := range panics {
		say("worker stopped: %s", p)
	}
	say("calibration stopped after %s: %d items done, %d clicks", time.Since(start).Round(time.Second), doneN, r.placed.Load())

	var results []*itemResult
	r.mu.Lock()
	results = append(results, r.done...)
	r.mu.Unlock()
	blocks, sum := buildBlocks(results, vocab)
	var notCovered []string
	for _, it := range items {
		if !finished[it.name] {
			reason := skipped[it.name]
			if reason == "" {
				reason = "not reached"
				if len(panics) > 0 {
					reason = "not reached: " + strings.Join(panics, "; ")
				}
			}
			notCovered = append(notCovered, it.name+": "+reason)
		}
	}
	sort.Strings(notCovered)
	hdr := place.Header{
		Format:        place.FormatVersion,
		ServerVersion: c.serverVersion,
		Image:         "itzg/minecraft-bedrock-server:2026.10.2",
		Protocol:      ad.Protocol,
		Vocabulary:    fmt.Sprintf("%s: %d states, %d blocks", vocabSource, nStates, len(vocab)),
		Matrix:        matrixHeader(control),
		Partial:       len(notCovered) > 0,
		NotCovered:    notCovered,
		Unplaceable:   sum.unplaceable,
		Unmapped:      unmappedCount(),
	}
	t := &place.Table{Header: hdr, Blocks: blocks}
	if c.mergeInto != "" {
		if t, err = mergeInto(c.mergeInto, t, asked); err != nil {
			return false, err
		}
		hdr = t.Header
		sum.blocks, sum.reachable, sum.unreachable, sum.derived, sum.inferred = tableCounts(t)
		sum.unplaceable = hdr.Unplaceable
	}
	if c.verify {
		gaps := gapCheck(t)
		t.Header.FamilyGaps = nil
		for _, g := range gaps {
			t.Header.FamilyGaps = append(t.Header.FamilyGaps, g.String())
		}
		t.Header.Matrix.Notes = append(t.Header.Matrix.Notes, fmt.Sprintf(
			"Gap check: blocks whose reachable states differ from their family's majority were run again in full (no inference), place wait %s, %d miss retries; %d still differ (family_gaps).",
			placeWait, missRetries, len(gaps)))
		say("after the re-run %d block(s) still differ from their family", len(gaps))
	}
	path := filepath.Join(c.out, c.serverVersion+".json.gz")
	if err := writeTableAtomic(path, t, c.force); err != nil {
		if errors.Is(err, errShrinks) {
			if werr := writeTableAtomic(path+".rejected", t, true); werr == nil {
				say("kept this run's table as %s.rejected", path)
			}
		}
		return false, err
	}
	say("wrote %s", path)
	say("blocks %d, states reachable %d, unreachable %d, blocks with derived keys %d, inferred %d, unplaceable items %d, truncated %d, unmapped hashes %d",
		sum.blocks, sum.reachable, sum.unreachable, sum.derived, sum.inferred, len(sum.unplaceable), len(sum.truncated), hdr.Unmapped)
	if hdr.Partial {
		say("PARTIAL: %d item(s) not covered", len(notCovered))
	}
	return hdr.Partial, nil
}

// verifyItems reads the table and names the items to run again: those of the
// blocks that disagree with their family and those it never covered.
func verifyItems(path string) ([]string, []gap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	t, err := place.Read(f)
	if err != nil {
		return nil, nil, err
	}
	gaps := gapCheck(t)
	seen := map[string]bool{}
	var out []string
	for _, n := range append(gapItems(gaps), notCoveredItems(t.Header.NotCovered)...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, gaps, nil
}

func notCoveredItems(entries []string) []string {
	var out []string
	for _, e := range entries {
		out = append(out, itemOf(e))
	}
	return out
}

// itemList turns the creative inventory into the items to calibrate.
func itemList(u *unit, c config) ([]item, error) {
	u.mu.Lock()
	cc := u.creative
	u.mu.Unlock()
	if cc == nil {
		return nil, errors.New("the server sent no creative inventory")
	}
	nameOf := map[int32]string{}
	for _, e := range u.gd.Items {
		nameOf[int32(e.RuntimeID)] = strings.TrimPrefix(e.Name, "minecraft:")
	}
	only := map[string]bool{}
	for _, n := range strings.Split(c.only, ",") {
		if n = strings.TrimPrefix(strings.TrimSpace(n), "minecraft:"); n != "" {
			only[n] = true
		}
	}
	has := map[string]bool{}
	for _, it := range cc.Items {
		n := nameOf[it.Item.NetworkID]
		if n == "" || skipItem(n) || (len(only) > 0 && !only[n]) {
			continue
		}
		has[n] = has[n] || it.Item.BlockRuntimeID != 0
	}
	var items []item
	for n, b := range has {
		items = append(items, item{name: n, hasBlock: b})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	if c.maxItems > 0 && len(items) > c.maxItems {
		items = items[:c.maxItems]
	}
	return items, nil
}

func matrixHeader(control string) place.Matrix {
	m := place.Matrix{
		Yaws:    yaws,
		Pitches: pitches,
		Sneak:   []bool{false, true},
	}
	for _, f := range faceNames {
		m.Faces = append(m.Faces, f)
	}
	m.Hits = append(append([]string{hMid}, sideHits...), flatHits...)
	m.Notes = []string{
		"First pass per item: each face at its centre, edge hits on the top face, height and side hits on the north face, then yaw, pitch and sneak around the first click that placed.",
		"Full pass: every combination of the inputs that changed a state, cheapest first, capped at 360 clicks.",
		"A family (same first-pass responses) is inferred from a sibling's sweep after 8 sampled clicks agree.",
		"Yaw 0 faces south, 90 west, 180 north, 270 east. Pitch is negative looking up. Hit is a fraction of the support block along x (east), y (up), z (south).",
		"The unit stands at a fixed spot south of the platform; position-derived facing is not varied.",
		"Neighbour pass: the block placed on bare floor with stone or itself on one or four horizontal sides; neighbours above and below are not varied.",
		"Sneak control (a crafting table as the support): " + control,
		fmt.Sprintf("Pitches tried in the run that wrote this header: %v.", pitches),
	}
	return m
}

// sneakControl proves the sneak flag reaches the server: stone clicked onto a
// crafting table opens its screen instead of placing, unless the unit sneaks.
func sneakControl(ctx context.Context, w *worker) string {
	lane := w.bay.lane()
	above := protocol.BlockPos{lane.X(), lane.Y() + 1, lane.Z()}
	defer func() {
		w.restore(context.WithoutCancel(ctx))
		_, _ = w.r.con.Exec(setAir(lane))
	}()
	try := func(sneak bool) (bool, error) {
		// The table is set fresh each time: restore sweeps the lane clean.
		if _, err := w.r.con.Exec(fmt.Sprintf("setblock %d %d %d crafting_table", lane.X(), lane.Y(), lane.Z())); err != nil {
			return false, err
		}
		w.u.waitFor(ctx, 2*time.Second, func() bool { return !w.u.isAir(lane) })
		w.u.setLook(ctx, click{Sneak: sneak})
		if err := w.click(ctx, lane, fTop, hitVec(fTop, hMid), "stone"); err != nil {
			return false, err
		}
		got := w.u.waitFor(ctx, placeWait, func() bool { return !w.u.isAir(above) })
		// A click that opens the table leaves its screen up, and the server
		// ignores clicks while one is open.
		if a := w.u.actuator(); a != nil {
			a.CloseContainer()
		}
		w.restore(context.WithoutCancel(ctx))
		return got, nil
	}
	plain, err := try(false)
	if err != nil {
		return "not run: " + err.Error()
	}
	sneaking, err := try(true)
	if err != nil {
		return "not run: " + err.Error()
	}
	w.u.setLook(ctx, click{})
	return fmt.Sprintf("placed without sneak=%v, with sneak=%v (works when false and true)", plain, sneaking)
}
