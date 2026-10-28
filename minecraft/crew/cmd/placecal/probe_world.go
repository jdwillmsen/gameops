package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// executeProbe joins one unit on a large stone platform and runs the named
// probes, printing the report. It leaves the lab as it found it, as a
// calibration run does.
func executeProbe(ctx context.Context, c config, names []string, con *safeConsole, out io.Writer) error {
	fns, err := probeNames(c.probe)
	if err != nil {
		return err
	}
	say := func(f string, a ...any) { fmt.Fprintf(out, "placecal: "+f+"\n", a...) }
	runCtx, cancel := context.WithTimeout(ctx, c.maxDuration)
	defer cancel()
	u, err := join(runCtx, c.addr, names[0], out)
	if err != nil {
		return err
	}
	defer u.close()
	if !u.ready(runCtx, 20*time.Second) {
		return errors.New("no position from the server")
	}

	b := bayAt(c.originX, c.floorY, c.originZ, 0)
	var (
		saved  map[string]string
		marked bool
		built  bool
		area   = areaName(0)
		clean  = context.WithoutCancel(runCtx)
	)
	defer func() {
		if marked {
			_ = clearMarker(clean, con, b)
		}
		if built {
			_, _ = con.Exec(fmt.Sprintf("fill %d %d %d %d %d %d air", b.ox-10, b.f, b.oz-10, b.ox+10, b.f+13, b.oz+10))
		}
		if saved != nil {
			if err := restoreGamerules(con, saved); err != nil {
				say("cleanup: gamerules: %v", err)
			}
		}
		_, _ = con.Exec("tickingarea remove " + area)
	}()
	removeStaleAreas(con)
	if saved, err = readGamerules(con); err != nil {
		saved = nil
		return err
	}
	if err := freezeGamerules(con); err != nil {
		return err
	}
	if err := addArea(con, b, area); err != nil {
		return err
	}
	if err := checkBayEmpty(runCtx, con, b); err != nil {
		return err
	}
	built = true
	if err := con.retry(runCtx, fmt.Sprintf("fill %d %d %d %d %d %d stone", b.ox-10, b.f, b.oz-10, b.ox+10, b.f, b.oz+10), "blocks filled"); err != nil {
		return err
	}
	if _, err := con.Exec(fmt.Sprintf("tp %q %.1f %d %.1f", u.name, float32(b.ox)+0.5, b.f+1, float32(b.oz)+4.5)); err != nil {
		return err
	}
	if _, err := con.Exec(fmt.Sprintf("gamemode creative %q", u.name)); err != nil {
		return err
	}
	if !u.waitFor(runCtx, 30*time.Second, func() bool {
		h, ok := u.hash(protocol.BlockPos{b.ox, b.f, b.oz})
		return ok && h != airHash && u.tracker.Creative()
	}) {
		return errors.New("platform not loaded or the unit not creative")
	}
	marked = true
	if err := checkMarker(runCtx, con, u, b); err != nil {
		return err
	}
	if err := clearMarker(runCtx, con, b); err != nil {
		return err
	}
	marked = false

	names2 := map[int32]string{}
	for _, e := range u.gd.Items {
		names2[int32(e.RuntimeID)] = strings.TrimPrefix(e.Name, "minecraft:")
	}
	rep := &report{out: out}
	p := &prober{
		ctx: runCtx, u: u, con: con, ox: b.ox, f: b.f, oz: b.oz, rep: rep, itemName: names2,
		w: &worker{r: &calRun{con: con, cfg: c}, u: u, bay: b, log: func(f string, a ...any) { say(u.name+": "+f, a...) }},
	}
	u.mu.Lock()
	u.tap = p.onPacket
	u.mu.Unlock()
	start := time.Now()
	for _, fn := range fns {
		if runCtx.Err() != nil {
			rep.say("\n(stopped before %s: %v)", fn.name, runCtx.Err())
			break
		}
		if fn.needsDrops {
			_, _ = con.Exec("gamerule dotiledrops true")
		}
		t := time.Now()
		func() {
			defer func() {
				if r := recover(); r != nil {
					rep.say("probe %s panicked: %v", fn.name, r)
				}
			}()
			fn.run(p)
		}()
		if fn.needsDrops {
			_, _ = con.Exec("gamerule dotiledrops false")
		}
		rep.say("(%s took %s)", fn.name, time.Since(t).Round(time.Second))
	}
	p.reset()
	rep.say("\nprobes finished in %s", time.Since(start).Round(time.Second))
	if c.probeOut != "" {
		return os.WriteFile(c.probeOut, []byte(strings.Join(rep.lines, "\n")+"\n"), 0o644)
	}
	return nil
}

// ---- gravity ---------------------------------------------------------------

func probeGravity(p *prober) {
	p.rep.head("Gravity blocks")
	for _, item := range []string{"sand", "red_sand", "gravel", "white_concrete_powder", "anvil", "dragon_egg"} {
		// Over air: clicked onto the side of a block floating five up.
		p.reset()
		p.set(p.cell(0, 6, 0), "stone")
		p.stand(1.5, 1, 1.5, 0, 0)
		t0 := time.Now()
		_ = p.put(item, p.cell(0, 6, 0), fEast, hitVec(fEast, hMid), click{})
		ev := p.watch(t0, 3500*time.Millisecond, p.cell(1, 6, 0), p.cell(1, 4, 0), p.cell(1, 2, 0), p.cell(1, 1, 0))
		p.rep.say("- %s placed into air beside a block 6 up: %s; item drops: %s", item, joinLines(ev), joinLines(p.dropsSince(t0)))

		// On a one-block scaffold, scaffold removed afterwards.
		p.reset()
		p.set(p.cell(3, 1, 0), "stone")
		p.stand(3.5, 1, 3.5, 0, 0)
		_ = p.putTop(item, p.cell(3, 2, 0), hMid, click{})
		placed := p.waitBlock(p.cell(3, 2, 0), time.Second)
		time.Sleep(700 * time.Millisecond)
		held := p.get(p.cell(3, 2, 0))
		t0 = time.Now()
		go p.u.breakAt(p.ctx, p.cell(3, 1, 0))
		ev = p.watch(t0, 3000*time.Millisecond, p.cell(3, 1, 0), p.cell(3, 2, 0))
		p.rep.say("- %s on a scaffold (placed=%v, 700ms later %s); scaffold broken: %s; drops: %s", item, placed, held, joinLines(ev), joinLines(p.dropsSince(t0)))
	}
	// Concrete powder beside water turns to concrete.
	p.reset()
	p.set(p.cell(5, 1, 0), "stone")
	p.set(p.cell(6, 2, 0), "water")
	p.stand(5.5, 1, 3.5, 0, 0)
	t0 := time.Now()
	_ = p.putTop("white_concrete_powder", p.cell(5, 2, 0), hMid, click{})
	ev := p.watch(t0, 2500*time.Millisecond, p.cell(5, 2, 0))
	p.rep.say("- white_concrete_powder placed beside a water source: %s", joinLines(ev))
	p.reset()
}

// ---- support loss ----------------------------------------------------------

func probeSupport(p *prober) {
	p.rep.head("Support loss (the block under, beside or over is broken by the unit)")
	type sc struct{ label, item, mode, scaffold string }
	cases := []sc{
		{"torch on top", "torch", "top", "stone"}, {"torch on a side", "torch", "side", "stone"},
		{"redstone wire", "redstone", "top", "stone"}, {"rail", "rail", "top", "stone"},
		{"carpet", "white_carpet", "top", "stone"}, {"pressure plate", "stone_pressure_plate", "top", "stone"},
		{"oak door (lower half's support)", "oak_door", "top", "stone"}, {"standing sign", "oak_sign", "top", "stone"},
		{"wall sign", "oak_sign", "side", "stone"}, {"ladder", "ladder", "side", "stone"},
		{"lantern hanging below a block", "lantern", "hang", "stone"}, {"lever on top", "lever", "top", "stone"},
		{"stone button on a side", "stone_button", "side", "stone"}, {"repeater", "repeater", "top", "stone"},
		{"sapling on dirt", "oak_sapling", "top", "dirt"}, {"snow layer", "snow_layer", "top", "stone"},
		{"candle", "candle", "top", "stone"}, {"white bed (foot support)", "white_bed", "top", "stone"},
		{"flower pot", "flower_pot", "top", "stone"},
	}
	for _, c := range cases {
		p.reset()
		var scaffold, against, target protocol.BlockPos
		var fc face
		switch c.mode {
		case "top":
			scaffold, fc, target = p.cell(0, 1, 0), fTop, p.cell(0, 2, 0)
			p.stand(0.5, 1, 3.5, 0, 0)
		case "side":
			scaffold, fc, target = p.cell(0, 1, 0), fNorth, p.cell(0, 1, -1)
			p.stand(0.5, 1, 2.5, 0, 0)
		default:
			scaffold, fc, target = p.cell(0, 4, 0), fBottom, p.cell(0, 3, 0)
			p.stand(0.5, 1, 2.5, 0, 0)
		}
		against = scaffold
		p.set(scaffold, c.scaffold)
		_ = p.put(c.item, against, fc, hitVec(fc, hMid), click{})
		if !p.waitBlock(target, 800*time.Millisecond) {
			p.rep.say("- %s: placed nothing", c.label)
			continue
		}
		time.Sleep(500 * time.Millisecond)
		upper := protocol.BlockPos{target.X(), target.Y() + 1, target.Z()}
		before := p.get(target)
		if !p.u.isAir(upper) {
			before += " + " + p.get(upper)
		}
		t0 := time.Now()
		go p.u.breakAt(p.ctx, scaffold)
		ev := p.watch(t0, 2500*time.Millisecond, scaffold, target, upper)
		p.rep.say("- %s: placed %s; support broken: %s; drops: %s", c.label, before, joinLines(ev), joinLines(p.dropsSince(t0)))
	}
	p.reset()
}

// ---- two-part blocks -------------------------------------------------------

func probeTwoPart(p *prober) {
	p.rep.head("Two-part blocks")
	lo, up := p.cell(0, 1, 0), p.cell(0, 2, 0)
	both := func(a, b protocol.BlockPos) string { return fmt.Sprintf("%s | %s", p.get(a), p.get(b)) }
	// Doors.
	p.reset()
	p.stand(0.5, 1, 4.5, 0, 0)
	_ = p.putTop("oak_door", lo, hMid, click{})
	p.waitBlock(lo, time.Second)
	time.Sleep(300 * time.Millisecond)
	p.rep.say("- door, one click on free floor: %s", both(lo, up))
	p.reset()
	p.set(up, "stone")
	_ = p.putTop("oak_door", lo, hMid, click{})
	p.waitBlock(lo, 700*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	p.rep.say("- door, upper cell holds stone: lower %s, upper %s", p.get(lo), p.get(up))
	p.reset()
	p.set(up, "glass")
	_ = p.putTop("oak_door", lo, hMid, click{})
	p.waitBlock(lo, 700*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	p.rep.say("- door, upper cell holds glass: lower %s, upper %s", p.get(lo), p.get(up))
	// Beds.
	for _, sc := range []struct{ label, setup string }{
		{"free ground", ""}, {"head cell holds stone", "head-stone"}, {"nothing under the head cell", "hole"},
	} {
		p.reset()
		p.stand(0.5, 1, -3.5, 180, 0) // unit north of the cell, facing south: head goes +z
		p.u.setLook(p.ctx, click{Yaw: 0})
		foot := p.cell(0, 1, 0)
		switch sc.setup {
		case "head-stone":
			p.set(p.cell(0, 1, 1), "stone")
		case "hole":
			p.set(p.cell(0, 1, 0), "stone")
			foot = p.cell(0, 2, 0)
		}
		_ = p.put("red_bed", protocol.BlockPos{foot.X(), foot.Y() - 1, foot.Z()}, fTop, hitVec(fTop, hMid), click{Yaw: 0})
		p.waitBlock(foot, 800*time.Millisecond)
		time.Sleep(300 * time.Millisecond)
		var cells []string
		for _, c := range []protocol.BlockPos{foot, {foot.X(), foot.Y(), foot.Z() + 1}, {foot.X(), foot.Y(), foot.Z() - 1}} {
			if !p.u.isAir(c) {
				cells = append(cells, p.rel(c)+" "+p.get(c))
			}
		}
		p.rep.say("- bed, %s: %s", sc.label, joinOr(cells, "placed nothing"))
	}
	// Tall plants.
	for _, item := range []string{"tall_grass", "large_fern", "sunflower", "rose_bush", "peony", "lilac", "pitcher_plant", "double_plant"} {
		for _, free := range []bool{true, false} {
			p.reset()
			p.set(lo, "grass_block")
			p.stand(0.5, 1, 3.5, 0, 0)
			if !free {
				p.set(p.cell(0, 3, 0), "stone")
			}
			_ = p.put(item, lo, fTop, hitVec(fTop, hMid), click{})
			p.waitBlock(up, 700*time.Millisecond)
			time.Sleep(250 * time.Millisecond)
			p.rep.say("- %s on grass, upper space free=%v: %s | %s", item, free, p.get(up), p.get(p.cell(0, 3, 0)))
		}
	}
	// Double slabs.
	for _, sc := range []struct {
		label string
		f     face
		hit   string
	}{{"top face", fTop, hMid}, {"north face, low hit", fNorth, hLow}, {"north face, high hit", fNorth, hHigh}} {
		p.reset()
		p.stand(0.5, 1, 4.5, 0, 0)
		_ = p.putTop("oak_slab", lo, hMid, click{})
		p.waitBlock(lo, time.Second)
		time.Sleep(200 * time.Millisecond)
		first := p.get(lo)
		_ = p.put("oak_slab", lo, sc.f, hitVec(sc.f, sc.hit), click{})
		time.Sleep(500 * time.Millisecond)
		p.rep.say("- slab on the floor (%s), then a second slab clicked on that slab's %s: cell now %s; cell above %s; north cell %s", first, sc.label, p.get(lo), p.get(up), p.get(p.cell(0, 1, -1)))
	}
	// A slab of another kind.
	p.reset()
	p.stand(0.5, 1, 4.5, 0, 0)
	_ = p.putTop("oak_slab", lo, hMid, click{})
	p.waitBlock(lo, time.Second)
	time.Sleep(200 * time.Millisecond)
	_ = p.put("birch_slab", lo, fTop, hitVec(fTop, hMid), click{})
	time.Sleep(500 * time.Millisecond)
	p.rep.say("- oak slab then a birch slab clicked on its top: cell %s; above %s", p.get(lo), p.get(up))
	p.reset()
}

func joinOr(s []string, empty string) string {
	if len(s) == 0 {
		return empty
	}
	return strings.Join(s, " | ")
}

// ---- multi-count blocks ----------------------------------------------------

func probeMultiCount(p *prober) {
	p.rep.head("Multi-count blocks (clicking the same cell again)")
	cell := p.cell(0, 1, 0)
	type k struct {
		item  string
		prop  string
		sneak bool
		f     face
	}
	for _, sneak := range []bool{false, true} {
		for _, item := range []string{"candle", "white_candle", "sea_pickle", "turtle_egg", "snow_layer", "pink_petals", "wildflowers", "leaf_litter"} {
			for _, fc := range []face{fTop, fNorth} {
				p.reset()
				p.stand(0.5, 1, 3.5, 0, 0)
				_ = p.putTop(item, cell, hMid, click{Sneak: sneak})
				if !p.waitBlock(cell, 800*time.Millisecond) {
					p.rep.say("- %s: placed nothing on the floor", item)
					break
				}
				var seq []string
				seq = append(seq, p.get(cell))
				for i := 0; i < 5; i++ {
					time.Sleep(250 * time.Millisecond)
					if fc == fTop {
						_ = p.put(item, cell, fTop, hitVec(fTop, hMid), click{Sneak: sneak})
					} else {
						_ = p.put(item, cell, fNorth, hitVec(fNorth, hMid), click{Sneak: sneak, Yaw: 0})
					}
					time.Sleep(350 * time.Millisecond)
					seq = append(seq, p.get(cell))
				}
				extra := ""
				if fc == fTop && !p.u.isAir(p.cell(0, 2, 0)) {
					extra = "; cell above: " + p.get(p.cell(0, 2, 0))
				}
				if fc == fNorth && !p.u.isAir(p.cell(0, 1, -1)) {
					extra = "; north cell: " + p.get(p.cell(0, 1, -1))
				}
				p.rep.say("- %s, clicked on its %s face, sneak=%v, five more clicks: %s%s", item, fc, sneak, strings.Join(dedupe(seq), " -> "), extra)
			}
		}
	}
	p.reset()
}

func dedupe(s []string) []string {
	var out []string
	for _, x := range s {
		if len(out) == 0 || out[len(out)-1] != x {
			out = append(out, x)
		}
	}
	return out
}

// ---- liquids ---------------------------------------------------------------

// pit makes a one-cell pit at (0,1,0): stone walls round it, the stone floor
// under it.
func (p *prober) pit() {
	p.fill(p.cell(-1, 1, -1), p.cell(1, 2, 1), "stone")
	p.set(p.cell(0, 1, 0), "air")
	p.set(p.cell(0, 2, 0), "air")
}

func probeLiquids(p *prober) {
	p.rep.head("Liquids (bucket items)")
	pit := p.cell(0, 1, 0)
	floor := p.cell(0, 0, 0)
	for _, bucket := range []string{"water_bucket", "lava_bucket"} {
		// Shape 1: use-item on the floor block's top face.
		p.reset()
		p.pit()
		p.stand(0.5, 1, 4.5, 0, 0)
		t0 := time.Now()
		err := p.put(bucket, floor, fTop, hitVec(fTop, hMid), click{})
		ev := p.watch(t0, 1500*time.Millisecond, pit, p.cell(0, 2, 0))
		p.rep.say("- %s, click-block on the pit floor's top (err=%v): %s", bucket, err, joinLines(ev))
		// Shape 2: use-item on air with the look down at the pit.
		p.reset()
		p.pit()
		p.stand(0.5, 1, 2.5, 0, 60)
		p.u.setLook(p.ctx, click{Pitch: 60})
		t0 = time.Now()
		if a := p.u.actuator(); a != nil {
			err = a.ClickAir(bucket)
		}
		ev = p.watch(t0, 1500*time.Millisecond, pit, p.cell(0, 2, 0))
		p.rep.say("- %s, click-air looking 60 degrees down from 2 cells away (err=%v): %s", bucket, err, joinLines(ev))
		// Shape 3: click-block on the side face of a wall, into the pit side.
		p.reset()
		p.pit()
		p.stand(0.5, 1, 4.5, 0, 0)
		t0 = time.Now()
		err = p.put(bucket, p.cell(0, 1, 1), fNorth, hitVec(fNorth, hMid), click{})
		ev = p.watch(t0, 1500*time.Millisecond, pit)
		p.rep.say("- %s, click-block on the north face of the stone south of the pit... (err=%v): %s", bucket, err, joinLines(ev))
	}
	// Placing liquids onto things.
	type occ struct{ label, setup string }
	for _, o := range []occ{
		{"a stone block (target cell occupied)", "stone"}, {"short grass", "short_grass"}, {"a snow layer", "snow_layer"},
		{"a torch", "torch"}, {"a water source (lava onto water)", "water"}, {"a lava source (water onto lava)", "lava"},
		{"a slab", "stone_slab"}, {"a fence", "oak_fence"},
	} {
		for _, bucket := range []string{"water_bucket", "lava_bucket"} {
			if o.setup == "water" && bucket == "water_bucket" || o.setup == "lava" && bucket == "lava_bucket" {
				continue
			}
			p.reset()
			p.pit()
			p.set(pit, o.setup)
			p.stand(0.5, 1, 4.5, 0, 0)
			t0 := time.Now()
			err := p.put(bucket, floor, fTop, hitVec(fTop, hMid), click{})
			ev := p.watch(t0, 1800*time.Millisecond, pit, p.cell(0, 2, 0))
			p.rep.say("- %s onto the pit floor with %s in the cell (err=%v): %s", bucket, o.label, err, joinLines(ev))
		}
	}
	p.reset()
}

// ---- waterlogging ----------------------------------------------------------

func probeWaterlog(p *prober) {
	p.rep.head("Waterlogging (a water source in a closed pit, item placed into it, then broken)")
	pit := p.cell(0, 1, 0)
	for _, item := range []string{"oak_slab", "oak_stairs", "oak_fence", "lantern", "chest", "oak_sign", "torch", "candle", "sea_pickle", "oak_trapdoor", "ladder", "oak_door", "glass_pane", "stone", "white_carpet", "redstone", "rail", "lever", "iron_bars", "cobblestone_wall", "oak_button", "stone_pressure_plate", "repeater", "white_bed"} {
		p.reset()
		p.pit()
		p.set(pit, "water")
		p.stand(0.5, 1, 4.5, 0, 0)
		_ = p.put(item, p.cell(0, 0, 0), fTop, hitVec(fTop, hMid), click{})
		if !p.waitBlock(pit, 800*time.Millisecond) || p.get(pit) == "water" || strings.HasPrefix(p.get(pit), "water[") {
			time.Sleep(500 * time.Millisecond)
		}
		placed := p.get(pit)
		time.Sleep(400 * time.Millisecond)
		placed2 := p.get(pit)
		ok := p.u.breakAt(p.ctx, pit)
		time.Sleep(900 * time.Millisecond)
		p.rep.say("- %s into a water source: cell reads %s (later %s); after breaking it (ok=%v) the cell reads %s", item, placed, placed2, ok, p.get(pit))
	}
	p.reset()
}

// ---- neighbour-derived shapes above and below -------------------------------

func probeNeighbours(p *prober) {
	p.rep.head("Neighbour-derived shapes with neighbours above and below")
	cell := p.cell(0, 1, 0)
	above := p.cell(0, 2, 0)
	for _, s := range []struct{ item, block string }{
		{"oak_fence", "oak_fence"}, {"cobblestone_wall", "cobblestone_wall"}, {"glass_pane", "glass_pane"}, {"iron_bars", "iron_bars"}, {"oak_stairs", "oak_stairs"},
	} {
		p.reset()
		p.stand(0.5, 1, 4.5, 0, 0)
		_ = p.putTop(s.item, cell, hMid, click{})
		p.waitBlock(cell, 800*time.Millisecond)
		bare := p.settleRead(cell)
		p.set(above, "stone")
		time.Sleep(500 * time.Millisecond)
		withStone := p.get(cell)
		p.set(above, s.block)
		time.Sleep(500 * time.Millisecond)
		withSame, aboveState := p.get(cell), p.get(above)
		p.set(above, "air")
		p.set(p.cell(1, 1, 0), "stone")
		time.Sleep(400 * time.Millisecond)
		sideOnly := p.get(cell)
		p.set(above, "stone")
		time.Sleep(500 * time.Millisecond)
		sideAndAbove := p.get(cell)
		p.rep.say("- %s alone: %s", s.item, describe(bare))
		p.rep.say("    stone above: %s", withStone)
		p.rep.say("    same block above: %s; the one above reads %s", withSame, aboveState)
		p.rep.say("    stone east only: %s; stone east and above: %s", sideOnly, sideAndAbove)

		// A block placed on top of an existing one.
		p.reset()
		p.set(cell, s.block)
		p.stand(0.5, 1, 3.5, 0, 0)
		_ = p.put(s.item, cell, fTop, hitVec(fTop, hMid), click{})
		p.waitBlock(above, 800*time.Millisecond)
		time.Sleep(500 * time.Millisecond)
		p.rep.say("    placed on top of the same block: lower %s; upper %s", p.get(cell), p.get(above))
		// A block placed below an existing one (clicking the bottom face of a ceiling).
		p.reset()
		p.set(p.cell(0, 3, 0), s.block)
		p.stand(0.5, 1, 2.5, 0, 0)
		_ = p.put(s.item, p.cell(0, 3, 0), fBottom, hitVec(fBottom, hMid), click{})
		p.waitBlock(above, 800*time.Millisecond)
		time.Sleep(500 * time.Millisecond)
		p.rep.say("    placed under the same block (clicked its underside): upper %s; the new one %s", p.get(p.cell(0, 3, 0)), p.get(above))
	}
	// Redstone wire and vines power/side bits.
	p.reset()
	p.stand(0.5, 1, 4.5, 0, 0)
	_ = p.putTop("redstone", cell, hMid, click{})
	p.waitBlock(cell, 800*time.Millisecond)
	p.set(above, "stone")
	time.Sleep(400 * time.Millisecond)
	p.rep.say("- redstone wire with stone above: %s", p.get(cell))
	p.reset()
}

// ---- occupied target cells -------------------------------------------------

func probeOccupied(p *prober) {
	p.rep.head("Placing into a cell that is already occupied")
	pit := p.cell(0, 1, 0)
	floor := p.cell(0, 0, 0)
	for _, o := range []struct{ label, block string }{
		{"water source", "water"}, {"lava source", "lava"}, {"short grass", "short_grass"}, {"tall grass lower half", "tall_grass"},
		{"snow layer (1)", "snow_layer"}, {"snow layer (3)", `snow_layer ["height"=2]`}, {"fire", "fire"}, {"vine", "vine"},
		{"dead bush", "deadbush"}, {"seagrass", "seagrass"}, {"torch", "torch"}, {"redstone wire", "redstone_wire"}, {"carpet", "white_carpet"},
		{"stone slab", "stone_slab"}, {"candle", "candle"}, {"air (control)", "air"}, {"stone", "stone"},
	} {
		for _, item := range []string{"stone", "oak_planks"} {
			if item == "oak_planks" && o.block != "water" && o.block != "short_grass" {
				continue
			}
			p.reset()
			p.pit()
			p.set(pit, o.block)
			time.Sleep(250 * time.Millisecond)
			before := p.get(pit)
			p.stand(0.5, 1, 4.5, 0, 0)
			_ = p.put(item, floor, fTop, hitVec(fTop, hMid), click{})
			time.Sleep(900 * time.Millisecond)
			p.rep.say("- %s onto the pit floor, the cell holds %s (%s): now %s; the cell above %s", item, o.label, before, p.get(pit), p.get(p.cell(0, 2, 0)))
		}
	}
	p.reset()
}
