package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// A probe is a kept experiment: it sets a scene in the lab, clicks, and
// writes down what the server did, as lines of a report. Probes are how the
// calibration table's doubts and the placement planner's world rules were
// answered, so they are rerunnable on a new server version.

// describe is a block in one line: name and sorted states, or "air".
func describe(o outcome) string {
	if !o.Placed {
		return "air"
	}
	if o.Unmapped {
		return o.Name
	}
	if len(o.Props) == 0 {
		return o.Name
	}
	keys := make([]string, 0, len(o.Props))
	for k := range o.Props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", strings.TrimPrefix(k, "minecraft:"), o.Props[k]))
	}
	return o.Name + "[" + strings.Join(parts, ",") + "]"
}

// report collects the lines a probe run writes.
type report struct {
	mu    sync.Mutex
	lines []string
	out   io.Writer
}

func (r *report) head(title string) { r.add("\n## " + title) }

func (r *report) say(f string, a ...any) { r.add(fmt.Sprintf(f, a...)) }

func (r *report) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, s)
	if r.out != nil {
		fmt.Fprintln(r.out, s)
	}
}

// drop is an item entity the server spawned.
type drop struct {
	at   time.Time
	item string
	n    int32
}

// prober is one unit and a large empty platform to run scenes on. Cells are
// given relative to the platform's origin: x east, z south, y up from the
// floor (y 1 is the first cell above it).
type prober struct {
	ctx       context.Context
	u         *unit
	w         *worker
	con       *safeConsole
	ox, f, oz int32
	rep       *report
	itemName  map[int32]string

	mu    sync.Mutex
	drops []drop
	last  string
}

func (p *prober) cell(dx, dy, dz int32) protocol.BlockPos {
	return protocol.BlockPos{p.ox + dx, p.f + dy, p.oz + dz}
}

// rel names a cell the way cell() takes it.
func (p *prober) rel(c protocol.BlockPos) string {
	return fmt.Sprintf("(%d,%d,%d)", c.X()-p.ox, c.Y()-p.f, c.Z()-p.oz)
}

func (p *prober) get(c protocol.BlockPos) string { return describe(p.u.read(c)) }

func (p *prober) read(c protocol.BlockPos) outcome { return p.u.read(c) }

func (p *prober) exec(f string, a ...any) string {
	out, _ := p.con.Exec(fmt.Sprintf(f, a...))
	return strings.TrimSpace(out)
}

// set puts a block in with the console and waits until the unit sees the
// cell change.
func (p *prober) set(c protocol.BlockPos, block string) {
	before, _ := p.u.hash(c)
	p.exec("setblock %d %d %d %s", c.X(), c.Y(), c.Z(), block)
	p.u.waitFor(p.ctx, 1500*time.Millisecond, func() bool {
		h, _ := p.u.hash(c)
		return h != before
	})
}

// fill sets a box and waits until the last corner shows.
func (p *prober) fill(a, b protocol.BlockPos, block string) {
	before, _ := p.u.hash(b)
	p.exec("fill %d %d %d %d %d %d %s", a.X(), a.Y(), a.Z(), b.X(), b.Y(), b.Z(), block)
	p.u.waitFor(p.ctx, 1500*time.Millisecond, func() bool {
		h, _ := p.u.hash(b)
		return h != before
	})
}

// reset clears the platform's air space and the item entities, leaving the
// stone floor.
func (p *prober) reset() {
	probe := p.cell(0, 2, 0)
	p.set(probe, "stone")
	p.exec("fill %d %d %d %d %d %d air", p.ox-9, p.f+1, p.oz-9, p.ox+9, p.f+12, p.oz+9)
	p.exec("kill @e[type=item]")
	p.u.waitFor(p.ctx, 2*time.Second, func() bool { return p.u.isAir(probe) })
	time.Sleep(150 * time.Millisecond)
	p.mu.Lock()
	p.drops = nil
	p.mu.Unlock()
}

// stand teleports the unit to feet position (x, y, z) relative to the origin
// (y above the floor block's top: 1 is standing on the floor) and sets its
// look.
func (p *prober) stand(x, y, z float32, yaw, pitch int) {
	ax, ay, az := float32(p.ox)+x, float32(p.f)+y, float32(p.oz)+z
	p.exec("tp %q %.2f %.2f %.2f", p.u.name, ax, ay, az)
	a := p.u.actuator()
	p.u.waitFor(p.ctx, 3*time.Second, func() bool {
		f := a.Pose().Feet
		return abs32(f.X()-ax) < 0.7 && abs32(f.Z()-az) < 0.7 && abs32(f.Y()-ay) < 0.7
	})
	p.u.setLook(p.ctx, click{Yaw: yaw, Pitch: pitch})
	time.Sleep(120 * time.Millisecond)
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// put clicks the face of against with item, under the look and sneak of c.
func (p *prober) put(item string, against protocol.BlockPos, f face, hit [3]float32, c click) error {
	if item != p.last {
		if err := p.w.stockHotbar(p.ctx); err != nil {
			return err
		}
		p.w.slots++
		p.last = item
	}
	p.u.setLook(p.ctx, c)
	return p.w.click(p.ctx, against, f, hit, item)
}

// putTop clicks the top face of the block below cell at the point hit names,
// which places into cell.
func (p *prober) putTop(item string, cell protocol.BlockPos, hit string, c click) error {
	return p.put(item, protocol.BlockPos{cell.X(), cell.Y() - 1, cell.Z()}, fTop, hitVec(fTop, hit), c)
}

// waitBlock waits until the cell shows a block.
func (p *prober) waitBlock(c protocol.BlockPos, d time.Duration) bool {
	return p.u.waitFor(p.ctx, d, func() bool { return !p.u.isAir(c) })
}

// settleRead waits until the cell has not changed for a while, then reads it.
func (p *prober) settleRead(c protocol.BlockPos) outcome {
	return p.w.stableRead(p.ctx, c, 200*time.Millisecond)
}

// watch records every change of the cells for d, with the time since t0.
func (p *prober) watch(t0 time.Time, d time.Duration, cells ...protocol.BlockPos) []string {
	last := make([]string, len(cells))
	for i, c := range cells {
		last[i] = p.get(c)
	}
	var ev []string
	end := time.Now().Add(d)
	for time.Now().Before(end) && p.ctx.Err() == nil {
		for i, c := range cells {
			if cur := p.get(c); cur != last[i] {
				ev = append(ev, fmt.Sprintf("+%dms %s %s -> %s", time.Since(t0).Milliseconds(), p.rel(c), last[i], cur))
				last[i] = cur
			}
		}
		time.Sleep(8 * time.Millisecond)
	}
	return ev
}

func (p *prober) onPacket(pk packet.Packet) {
	if a, ok := pk.(*packet.AddItemActor); ok {
		p.mu.Lock()
		p.drops = append(p.drops, drop{at: time.Now(), item: p.itemName[a.Item.Stack.NetworkID], n: int32(a.Item.Stack.Count)})
		p.mu.Unlock()
	}
}

// dropsSince lists the item entities spawned after t0.
func (p *prober) dropsSince(t0 time.Time) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, d := range p.drops {
		if d.at.After(t0) {
			out = append(out, fmt.Sprintf("+%dms drop %dx %s", d.at.Sub(t0).Milliseconds(), d.n, d.item))
		}
	}
	return out
}

func joinLines(ev []string) string {
	if len(ev) == 0 {
		return "nothing changed"
	}
	return strings.Join(ev, "; ")
}

// probeFn is one experiment.
type probeFn struct {
	name string
	run  func(*prober)
	// needsDrops sets the lab's tile drops on: support loss pops items.
	needsDrops bool
}

func probeList() []probeFn {
	return []probeFn{
		{name: "hinge", run: probeHinge},
		{name: "pistons", run: probePistons},
		{name: "yaws", run: probeYaws},
		{name: "plates", run: probePlates},
		{name: "beds", run: probeBeds},
		{name: "gravity", run: probeGravity},
		{name: "support", run: probeSupport, needsDrops: true},
		{name: "twopart", run: probeTwoPart},
		{name: "multicount", run: probeMultiCount},
		{name: "liquids", run: probeLiquids},
		{name: "waterlog", run: probeWaterlog},
		{name: "neighbours", run: probeNeighbours},
		{name: "occupied", run: probeOccupied},
	}
}

// probeNames resolves the --probe value: a comma list, or all.
func probeNames(spec string) ([]probeFn, error) {
	all := probeList()
	if spec == "all" {
		return all, nil
	}
	byName := map[string]probeFn{}
	var names []string
	for _, f := range all {
		byName[f.name] = f
		names = append(names, f.name)
	}
	var out []probeFn
	for _, n := range strings.Split(spec, ",") {
		n = strings.TrimSpace(n)
		f, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("--probe %q: no such probe; have %s, or all", n, strings.Join(names, ", "))
		}
		out = append(out, f)
	}
	return out, nil
}

// roundYaws are the 32 yaw angles at 11.25 degree steps, as whole degrees,
// which the actuator sends: twice the resolution of a 16-way rotation.
func roundYaws() []int {
	out := make([]int, 32)
	for k := range out {
		out[k] = int(float64(k)*11.25 + 0.5)
	}
	return out
}

// sixteenYaws are the 16 yaw angles at 22.5 degree steps, rounded up.
func sixteenYaws() []int {
	out := make([]int, 16)
	for k := range out {
		out[k] = int(float64(k)*22.5 + 0.5)
	}
	return out
}

func probeUsage() string {
	var n []string
	for _, f := range probeList() {
		n = append(n, f.name)
	}
	return strings.Join(n, ", ")
}
