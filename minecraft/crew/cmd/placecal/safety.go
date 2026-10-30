package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

// The helpers here are shared by every entry point that builds bays in the
// lab, so each one leaves the lab as it found it.

// Colosseum footprint, in blocks, that no bay may touch.
const (
	colosseumX1, colosseumX2 = -420, -181
	colosseumZ1, colosseumZ2 = -864, -653
)

const (
	bayPitch     = 24 // distance between the origins of neighbouring bays
	markerOffset = -12
	maxCoord     = 29_000_000
	areaPrefix   = "pcal"
)

// calGamerules are the rules a run turns off and must put back.
var calGamerules = []string{"domobspawning", "dodaylightcycle", "dotiledrops", "doweathercycle"}

var (
	versionRe       = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,4}$`)
	onlineModeOffRe = regexp.MustCompile(`ONLINE_MODE:\s*"?false`)
)

func checkVersion(v string) error {
	if !versionRe.MatchString(v) {
		return fmt.Errorf("server version %q must look like 1.26.51", v)
	}
	return nil
}

// bayAt is the i-th bay of a run.
func bayAt(originX, floorY, originZ, i int) bay {
	return bay{ox: int32(originX + bayPitch*i), f: int32(floorY), oz: int32(originZ)}
}

// bayExtent is the footprint of a bay and its marker cells.
func bayExtent(b bay) (x1, x2, z1, z2 int32) {
	return b.ox + bayWest, b.ox + bayEast, b.oz + markerOffset - 1, b.oz + baySouth
}

// checkOrigin refuses an origin whose bays reach the Colosseum, or whose
// numbers are not sane.
func checkOrigin(originX, originZ, floorY, n int) error {
	last := originX + bayPitch*(n-1)
	if originX < -maxCoord || last > maxCoord || originZ < -maxCoord || originZ > maxCoord {
		return fmt.Errorf("--origin-x/--origin-z out of range (at most %d from 0)", maxCoord)
	}
	if floorY < 5 || floorY > 300 {
		return errors.New("--floor-y must be between 5 and 300")
	}
	for i := 0; i < n; i++ {
		x1, x2, z1, z2 := bayExtent(bayAt(originX, floorY, originZ, i))
		if x2 >= colosseumX1 && x1 <= colosseumX2 && z2 >= colosseumZ1 && z1 <= colosseumZ2 {
			return fmt.Errorf("bay %d (x %d..%d, z %d..%d) intersects the Colosseum (x %d..%d, z %d..%d); pick another origin",
				i, x1, x2, z1, z2, colosseumX1, colosseumX2, colosseumZ1, colosseumZ2)
		}
	}
	return nil
}

// checkCompose requires the compose file to be a lab: it exists, defines the
// service and runs it offline.
func checkCompose(path, lab string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("--compose: %w", err)
	}
	svc := regexp.MustCompile(`(?m)^[ \t]+` + regexp.QuoteMeta(lab) + `:[ \t]*(#.*)?$`)
	if !svc.Match(data) {
		return fmt.Errorf("--compose %s defines no service %q", path, lab)
	}
	if !onlineModeOffRe.Match(data) {
		return fmt.Errorf("--compose %s does not set ONLINE_MODE false; that is not a lab", path)
	}
	return nil
}

func gameruleValue(name, reply string) (string, bool) {
	m := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\s*=\s*(true|false)`).FindStringSubmatch(reply)
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}

// readGamerules records the current value of every rule a run changes. It
// fails when one cannot be read, since it could not be put back.
func readGamerules(con *safeConsole) (map[string]string, error) {
	saved := map[string]string{}
	for _, n := range calGamerules {
		out, err := con.Exec("gamerule " + n)
		if err != nil {
			return nil, err
		}
		v, ok := gameruleValue(n, out)
		if !ok {
			return nil, fmt.Errorf("cannot read gamerule %s (reply %q)", n, strings.TrimSpace(out))
		}
		saved[n] = v
	}
	return saved, nil
}

// freezeGamerules turns the rules off.
func freezeGamerules(con *safeConsole) error {
	for _, n := range calGamerules {
		if _, err := con.Exec("gamerule " + n + " false"); err != nil {
			return err
		}
	}
	return nil
}

// restoreGamerules puts the saved values back and reports every failure.
func restoreGamerules(con *safeConsole, saved map[string]string) error {
	var errs []error
	for _, n := range calGamerules {
		if v, ok := saved[n]; ok {
			if _, err := con.Exec("gamerule " + n + " " + v); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func areaName(i int) string { return fmt.Sprintf("%s%d", areaPrefix, i) }

// removeStaleAreas drops ticking areas a dead run left; a missing one is no
// error.
func removeStaleAreas(con *safeConsole) {
	for i := 0; i < maxUnits; i++ {
		_, _ = con.Exec("tickingarea remove " + areaName(i))
	}
}

var areaErrors = []string{"cannot", "can't", "failed", "unknown command", "exceed", "already", "error", "no such"}

// addArea adds the ticking area of bay b. Bedrock answers "Added ticking
// area"; a reply with an obvious error is refused, anything else unclear is
// let through since the later retries prove the chunks load.
func addArea(con *safeConsole, b bay, name string) error {
	out, err := con.Exec(fmt.Sprintf("tickingarea add circle %d %d %d 2 %s", b.ox, b.f, b.oz, name))
	if err != nil {
		return err
	}
	if strings.Contains(out, "Added ticking area") {
		return nil
	}
	low := strings.ToLower(out)
	for _, e := range areaErrors {
		if strings.Contains(low, e) {
			return fmt.Errorf("tickingarea add %s: %q", name, strings.TrimSpace(out))
		}
	}
	return nil
}

// sampleCells are a few cells across the bay's volume and its marker cells,
// which all must be air before a run builds there.
func sampleCells(b bay) []protocol.BlockPos {
	hi, lo := markerCells(b)
	cells := []protocol.BlockPos{hi, lo}
	for _, y := range []int32{0, 1, bayHeight} {
		for _, xz := range [][2]int32{{bayWest, bayNorth}, {bayEast, bayNorth}, {bayWest, baySouth}, {bayEast, baySouth}, {0, 0}, {3, 0}} {
			cells = append(cells, b.pos(xz[0], y, xz[1]))
		}
	}
	return cells
}

// The probe platform: a stone floor and a cleanup that reach probeRadius
// cells around the origin and probeHeight above the floor, well past the
// bay.
const (
	probeRadius = 10
	probeHeight = 13
)

// checkProbeOrigin refuses an origin whose probe platform reaches the
// Colosseum.
func checkProbeOrigin(b bay) error {
	x1, x2, z1, z2 := b.ox-probeRadius, b.ox+probeRadius, b.oz-probeRadius, b.oz+probeRadius
	if x2 >= colosseumX1 && x1 <= colosseumX2 && z2 >= colosseumZ1 && z1 <= colosseumZ2 {
		return fmt.Errorf("probe platform (x %d..%d, z %d..%d) intersects the Colosseum (x %d..%d, z %d..%d); pick another origin",
			x1, x2, z1, z2, colosseumX1, colosseumX2, colosseumZ1, colosseumZ2)
	}
	return nil
}

// probeCells is every cell the probe's floor fill and cleanup can write:
// the whole platform volume, floor row included. The cleanup blindly turns
// all of it to air, so the guard must cover each cell — a sample would let a
// thin lab structure between the sample points be destroyed.
func probeCells(b bay) []protocol.BlockPos {
	var cells []protocol.BlockPos
	for y := int32(0); y <= probeHeight; y++ {
		for x := int32(-probeRadius); x <= probeRadius; x++ {
			for z := int32(-probeRadius); z <= probeRadius; z++ {
				cells = append(cells, b.pos(x, y, z))
			}
		}
	}
	return cells
}

// checkProbeEmpty fails when any cell of the probe platform holds a block,
// so the platform's fills cannot destroy lab blocks outside the bay.
func checkProbeEmpty(ctx context.Context, con *safeConsole, b bay) error {
	return checkCellsEmpty(ctx, con, b, "probe platform", probeCells(b))
}

// checkBayEmpty fails when a sampled cell holds a block. A cell whose chunk
// has not loaded yet is asked again.
func checkBayEmpty(ctx context.Context, con *safeConsole, b bay) error {
	return checkCellsEmpty(ctx, con, b, "bay", sampleCells(b))
}

func checkCellsEmpty(ctx context.Context, con *safeConsole, b bay, what string, cells []protocol.BlockPos) error {
	for _, p := range cells {
		if err := ctx.Err(); err != nil {
			return err
		}
		cmd := fmt.Sprintf("testforblock %d %d %d air", p.X(), p.Y(), p.Z())
		var last string
		ok := false
		for try := 0; try < 40 && !ok; try++ {
			out, err := con.Exec(cmd)
			if err != nil {
				return err
			}
			last = strings.TrimSpace(out)
			switch {
			case strings.Contains(out, "Successfully found"):
				ok = true
			case strings.Contains(out, "expected"):
				return fmt.Errorf("%s at %d,%d,%d is not empty (block at %d,%d,%d): %s; clear it or pick another origin", what, b.ox, b.f, b.oz, p.X(), p.Y(), p.Z(), last)
			default:
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
		if !ok {
			return fmt.Errorf("%q never answered (last reply %q)", cmd, last)
		}
	}
	return nil
}

// clearBay fills the whole bay, floor included, back to air.
func clearBay(ctx context.Context, con *safeConsole, b bay) error {
	x1, x2 := b.ox+bayWest, b.ox+bayEast
	z1, z2 := b.oz+bayNorth, b.oz+baySouth
	return con.retry(ctx, fmt.Sprintf("fill %d %d %d %d %d %d air", x1, b.f, z1, x2, b.f+bayHeight, z2), "blocks filled")
}

var woolColours = [16]string{"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
	"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black"}

// markerCells are the two cells the nonce is written to, north of the bay.
func markerCells(b bay) (hi, lo protocol.BlockPos) {
	return b.pos(0, 1, markerOffset), b.pos(1, 1, markerOffset)
}

func woolName(nibble byte) string { return woolColours[nibble&15] + "_wool" }

// markerReader is the part of a unit the marker check reads with.
type markerReader interface {
	read(protocol.BlockPos) outcome
	waitFor(ctx context.Context, timeout time.Duration, cond func() bool) bool
}

func randomByte() (byte, error) {
	var b [1]byte
	_, err := rand.Read(b[:])
	return b[0], err
}

// checkMarker proves the console and the joined unit are on the same server:
// it writes a random byte as two wool blocks and requires the unit to read
// the same two. The unit must be within view of the bay. The marker is left
// for clearMarker.
func checkMarker(ctx context.Context, con *safeConsole, u markerReader, b bay) error {
	n, err := randomByte()
	if err != nil {
		return err
	}
	hi, lo := markerCells(b)
	for _, s := range []struct {
		p protocol.BlockPos
		w string
	}{{hi, woolName(n >> 4)}, {lo, woolName(n & 15)}} {
		if err := con.retry(ctx, fmt.Sprintf("setblock %d %d %d %s", s.p.X(), s.p.Y(), s.p.Z(), s.w), "Block placed"); err != nil {
			return err
		}
	}
	want := [2]string{woolName(n >> 4), woolName(n & 15)}
	name := func(p protocol.BlockPos) string { return strings.TrimPrefix(u.read(p).Name, "minecraft:") }
	if !u.waitFor(ctx, 15*time.Second, func() bool { return name(hi) == want[0] && name(lo) == want[1] }) {
		return fmt.Errorf("--addr is not the server the console commands: the unit read %q,%q where the console wrote %q,%q",
			name(hi), name(lo), want[0], want[1])
	}
	return nil
}

// clearMarker takes the marker out again.
func clearMarker(ctx context.Context, con *safeConsole, b bay) error {
	hi, lo := markerCells(b)
	for _, p := range []protocol.BlockPos{hi, lo} {
		if _, err := con.Exec(setAir(p)); err != nil {
			return err
		}
	}
	return nil
}

// writeTableAtomic writes t to path through a temp file in the same
// directory and a rename, and refuses to replace a table holding more blocks
// unless force is set.
func writeTableAtomic(path string, t *place.Table, force bool) error {
	if !force {
		if f, err := os.Open(path); err == nil {
			old, rerr := place.Read(f)
			f.Close()
			switch {
			case rerr != nil:
				return fmt.Errorf("%s exists and cannot be read (%v); use --force to replace it", path, rerr)
			case len(old.Blocks) > len(t.Blocks):
				return fmt.Errorf("%w: %s has %d blocks, this run %d; use --force to replace it", errShrinks, path, len(old.Blocks), len(t.Blocks))
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".placecal-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := t.Write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

var errShrinks = errors.New("the new table is smaller than the existing one")
