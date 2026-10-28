package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func flag01(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "T"
		}
		return "F"
	case nil:
		return "-"
	}
	if s := fmt.Sprint(v); s == "1" {
		return "T"
	} else if s == "0" {
		return "F"
	}
	return fmt.Sprint(v)
}

// probeHinge places a door under every combination of hit point, yaw and
// neighbour and reads both halves: on Bedrock the hinge bit sits on the
// upper half, which calibration did not read.
func probeHinge(p *prober) {
	p.rep.head("Door hinge (hinge bit read on both halves)")
	lower, upper := p.cell(0, 1, 0), p.cell(0, 2, 0)
	type nb struct {
		name string
		set  func()
	}
	stone := func(dx, dz int32) func() {
		return func() {
			p.fill(p.cell(dx, 1, dz), p.cell(dx, 2, dz), "stone")
		}
	}
	neighbours := []nb{
		{"none", func() {}},
		{"stone+x", stone(1, 0)}, {"stone-x", stone(-1, 0)}, {"stone+z", stone(0, 1)}, {"stone-z", stone(0, -1)},
		{"door+x", func() {
			_ = p.putTop("oak_door", p.cell(1, 1, 0), hMid, click{})
			p.waitBlock(p.cell(1, 1, 0), time.Second)
		}},
		{"door-x", func() {
			_ = p.putTop("oak_door", p.cell(-1, 1, 0), hMid, click{})
			p.waitBlock(p.cell(-1, 1, 0), time.Second)
		}},
	}
	hits := []string{hMid, hXm, hXp, hZm, hZp}
	p.stand(0.5, 1, 4.5, 0, 0)
	seenTrue := map[string]bool{}
	for _, yaw := range []int{0, 90, 180, 270} {
		for _, n := range neighbours {
			var row []string
			for _, h := range hits {
				p.exec("fill %d %d %d %d %d %d air", p.ox-2, p.f+1, p.oz-2, p.ox+2, p.f+3, p.oz+2)
				p.u.waitFor(p.ctx, time.Second, func() bool { return p.u.isAir(lower) })
				p.stand(0.5, 1, 4.5, yaw, 0)
				n.set()
				_ = p.putTop("oak_door", lower, h, click{Yaw: yaw})
				if !p.waitBlock(lower, 700*time.Millisecond) {
					row = append(row, h+"=none")
					continue
				}
				time.Sleep(150 * time.Millisecond)
				lo, up := p.read(lower), p.read(upper)
				cell := fmt.Sprintf("%s=%s/%s", h, flag01(lo.Props["door_hinge_bit"]), flag01(up.Props["door_hinge_bit"]))
				row = append(row, cell)
				if flag01(up.Props["door_hinge_bit"]) == "T" || flag01(lo.Props["door_hinge_bit"]) == "T" {
					seenTrue[fmt.Sprintf("yaw %d, %s, hit %s (facing %v)", yaw, n.name, h, lo.Props["minecraft:cardinal_direction"])] = true
				}
			}
			p.rep.say("- yaw %3d, neighbour %-8s lower/upper hinge by hit: %s", yaw, n.name, strings.Join(row, " "))
		}
	}
	var keys []string
	for k := range seenTrue {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	p.rep.say("hinge true seen in %d of %d trials; first: %s", len(keys), 4*len(neighbours)*len(hits), firstN(keys, 6))
}

func firstN(s []string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.Join(s, "; ")
}

// probePistons tries the up and down facings with other pitches and with the
// unit standing above or below the cell it places into.
func probePistons(p *prober) {
	p.rep.head("Facing up/down of pistons and the like (pitch and stand position)")
	items := []string{"piston", "sticky_piston", "dropper", "dispenser", "observer", "barrel", "hopper", "crafter"}
	type scene struct {
		name  string
		prep  func() protocol.BlockPos // returns the cell to place into
		stand [3]float32
	}
	scenes := []scene{
		{"stand on floor 4 away, cell on floor", func() protocol.BlockPos { return p.cell(0, 1, 0) }, [3]float32{0.5, 1, 4.5}},
		{"stand 1 away, cell on floor (eye 1.6 above)", func() protocol.BlockPos { return p.cell(0, 1, 0) }, [3]float32{0.5, 1, 1.5}},
		{"stand on a 4-high pillar beside the cell (eye 5.6 above)", func() protocol.BlockPos {
			p.fill(p.cell(0, 1, 1), p.cell(0, 4, 1), "stone")
			return p.cell(0, 1, 0)
		}, [3]float32{0.5, 5, 1.5}},
		{"stand on floor, cell 3 up on a pillar (cell above eye)", func() protocol.BlockPos {
			p.fill(p.cell(0, 1, 0), p.cell(0, 3, 0), "stone")
			return p.cell(0, 4, 0)
		}, [3]float32{0.5, 1, 1.5}},
		{"stand on floor 4 away, cell 3 up on a pillar", func() protocol.BlockPos {
			p.fill(p.cell(0, 1, 0), p.cell(0, 3, 0), "stone")
			return p.cell(0, 4, 0)
		}, [3]float32{0.5, 1, 4.5}},
	}
	for _, item := range items {
		for _, sc := range scenes {
			var parts []string
			for _, pitch := range []int{0, -89, -90, 90, 89} {
				p.reset()
				target := sc.prep()
				p.stand(sc.stand[0], sc.stand[1], sc.stand[2], 0, pitch)
				_ = p.put(item, protocol.BlockPos{target.X(), target.Y() - 1, target.Z()}, fTop, hitVec(fTop, hMid), click{Pitch: pitch})
				if !p.waitBlock(target, 700*time.Millisecond) {
					parts = append(parts, fmt.Sprintf("pitch %d: none", pitch))
					continue
				}
				o := p.settleRead(target)
				parts = append(parts, fmt.Sprintf("pitch %d: %s", pitch, facingOf(o)))
			}
			p.rep.say("- %s | %s: %s", item, sc.name, strings.Join(parts, "; "))
		}
	}
	p.reset()
}

func facingOf(o outcome) string {
	var kv []string
	for _, k := range []string{"facing_direction", "minecraft:facing_direction", "minecraft:cardinal_direction", "orientation", "minecraft:block_face"} {
		if v, ok := o.Props[k]; ok {
			kv = append(kv, fmt.Sprintf("%s=%v", strings.TrimPrefix(k, "minecraft:"), v))
		}
	}
	if len(kv) == 0 {
		return describe(o)
	}
	return o.Name + " " + strings.Join(kv, ",")
}

// probeYaws turns the unit in steps finer than 90 degrees and records the
// rotation states of signs, banners and skulls.
func probeYaws(p *prober) {
	p.rep.head("Finer yaw steps (11.25 degree sweep)")
	cell := p.cell(0, 1, 0)
	p.stand(0.5, 1, 4.5, 0, 0)
	for _, item := range []string{"oak_sign", "oak_hanging_sign", "white_banner", "skeleton_skull", "creeper_head", "player_head"} {
		byVal := map[string][]int{}
		name := ""
		for _, yaw := range roundYaws() {
			p.reset()
			p.stand(0.5, 1, 4.5, yaw, 0)
			_ = p.putTop(item, cell, hMid, click{Yaw: yaw})
			if !p.waitBlock(cell, 600*time.Millisecond) {
				continue
			}
			o := p.settleRead(cell)
			name = o.Name
			var key []string
			for k, v := range o.Props {
				if strings.Contains(k, "direction") || strings.Contains(k, "rotation") {
					key = append(key, fmt.Sprintf("%s=%v", strings.TrimPrefix(k, "minecraft:"), v))
				}
			}
			sort.Strings(key)
			byVal[strings.Join(key, ",")] = append(byVal[strings.Join(key, ",")], yaw)
		}
		if name == "" {
			p.rep.say("- %s: placed nothing on the floor top", item)
			continue
		}
		var vals []string
		for v := range byVal {
			vals = append(vals, v)
		}
		sort.Strings(vals)
		p.rep.say("- %s (%s): %d distinct rotation states over 32 yaws", item, name, len(vals))
		for _, v := range vals {
			p.rep.say("    %s <- yaw %v", v, byVal[v])
		}
	}
	p.reset()
}

// probePlates asks whether a pressure plate's redstone_signal follows
// something standing on it.
func probePlates(p *prober) {
	p.rep.head("Pressure plate redstone_signal")
	cell := p.cell(0, 1, 0)
	for _, item := range []string{"stone_pressure_plate", "oak_pressure_plate", "light_weighted_pressure_plate", "heavy_weighted_pressure_plate", "polished_blackstone_pressure_plate"} {
		p.reset()
		p.stand(0.5, 1, 5.5, 0, 0)
		_ = p.putTop(item, cell, hMid, click{})
		t0 := time.Now()
		if !p.waitBlock(cell, 700*time.Millisecond) {
			p.rep.say("- %s: placed nothing", item)
			continue
		}
		var reads []string
		for _, d := range []time.Duration{60, 300, 1000, 2500} {
			time.Sleep(time.Until(t0.Add(d * time.Millisecond)))
			reads = append(reads, fmt.Sprintf("+%dms %s", d, flag01(p.read(cell).Props["redstone_signal"])+"/"+fmt.Sprint(p.read(cell).Props["redstone_signal"])))
		}
		p.stand(0.5, 1, 0.5, 0, 0) // onto the plate
		time.Sleep(900 * time.Millisecond)
		on := fmt.Sprint(p.read(cell).Props["redstone_signal"])
		p.stand(0.5, 1, 5.5, 0, 0)
		time.Sleep(1500 * time.Millisecond)
		off := fmt.Sprint(p.read(cell).Props["redstone_signal"])
		p.rep.say("- %s untouched, unit 5 away: %s; unit standing on it: %s; after stepping off: %s", item, strings.Join(reads, ", "), on, off)
	}
	p.reset()
}

// probeBeds places a bed facing each way and reads both halves.
func probeBeds(p *prober) {
	p.rep.head("Bed halves")
	foot := p.cell(0, 1, 0)
	dirs := []struct {
		yaw  int
		name string
		dx   int32
		dz   int32
	}{{0, "yaw 0 (south)", 0, 1}, {90, "yaw 90 (west)", -1, 0}, {180, "yaw 180 (north)", 0, -1}, {270, "yaw 270 (east)", 1, 0}}
	for _, d := range dirs {
		p.reset()
		// Stand so the head side is away from the unit; place on the cell.
		p.stand(0.5-float32(d.dx)*4, 1, 0.5-float32(d.dz)*4, d.yaw, 0)
		_ = p.putTop("red_bed", foot, hMid, click{Yaw: d.yaw})
		if !p.waitBlock(foot, 700*time.Millisecond) {
			p.rep.say("- %s: placed nothing", d.name)
			continue
		}
		time.Sleep(300 * time.Millisecond)
		var halves []string
		for _, c := range []protocol.BlockPos{foot, p.cell(1, 1, 0), p.cell(-1, 1, 0), p.cell(0, 1, 1), p.cell(0, 1, -1)} {
			if !p.u.isAir(c) {
				halves = append(halves, p.rel(c)+" "+p.get(c))
			}
		}
		p.rep.say("- %s, clicked %s: %s", d.name, p.rel(foot), strings.Join(halves, " | "))
	}
	p.reset()
}
