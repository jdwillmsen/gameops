package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

// face is a protocol block face index.
type face int

const (
	fBottom face = iota
	fTop
	fNorth
	fSouth
	fWest
	fEast
)

var faceNames = [...]string{"bottom", "top", "north", "south", "west", "east"}

// faceOff is the cell a click on the face places into, relative to the
// clicked block.
var faceOff = [...][3]int32{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

func (f face) String() string { return faceNames[f] }

// opposite is the side of the placed cell the support lies on.
func (f face) opposite() face {
	return [...]face{fTop, fBottom, fSouth, fNorth, fEast, fWest}[f]
}

func (f face) side() bool { return f >= fNorth }

// Named hit points. A side face has low and high (height) and left and right
// (along the face); top and bottom have the four edges. mid is the centre.
const (
	hMid   = "mid"
	hLow   = "low"
	hHigh  = "high"
	hLeft  = "left"
	hRight = "right"
	hXm    = "xm"
	hXp    = "xp"
	hZm    = "zm"
	hZp    = "zp"
)

var (
	sideHits = []string{hLow, hHigh, hLeft, hRight}
	flatHits = []string{hXm, hXp, hZm, hZp}
	yaws     = []int{0, 90, 180, 270}
	pitches  = []int{0, -60, 60}
)

const (
	hitLowY  = 0.25
	hitHighY = 0.75
	hitNear  = 0.1
	hitFar   = 0.9
)

// hitVec is the point on the clicked block a hit name stands for, as a
// fraction of the block along x, y and z.
func hitVec(f face, hit string) [3]float32 {
	v := [3]float32{0.5, 0.5, 0.5}
	switch f {
	case fBottom:
		v[1] = 0
	case fTop:
		v[1] = 1
	case fNorth:
		v[2] = 0
	case fSouth:
		v[2] = 1
	case fWest:
		v[0] = 0
	case fEast:
		v[0] = 1
	}
	along := 0 // the axis "left" and "right" run along on a side face
	if f == fWest || f == fEast {
		along = 2
	}
	switch hit {
	case hLow:
		v[1] = hitLowY
	case hHigh:
		v[1] = hitHighY
	case hLeft:
		v[along] = hitNear
	case hRight:
		v[along] = hitFar
	case hXm:
		v[0] = hitNear
	case hXp:
		v[0] = hitFar
	case hZm:
		v[2] = hitNear
	case hZp:
		v[2] = hitFar
	}
	return v
}

// click is one cell of the matrix.
type click struct {
	Face  face
	Hit   string
	Yaw   int
	Pitch int
	Sneak bool
	// Lane says the click lands on the bare floor of the lane, where there
	// is flat ground on every side, instead of on a fixture.
	Lane bool
}

func (c click) key() string {
	return fmt.Sprintf("%s/%s/%d/%d/%t/%t", c.Face, c.Hit, c.Yaw, c.Pitch, c.Sneak, c.Lane)
}

func (c click) recipe() place.Recipe {
	return place.Recipe{
		Support: c.Face.opposite().String(),
		Face:    c.Face.String(),
		Hit:     hitVec(c.Face, c.Hit),
		Yaw:     c.Yaw,
		Pitch:   c.Pitch,
		Sneak:   c.Sneak,
	}
}

func (c click) cost() float64 { return c.recipe().Cost() }

// outcome is what the server made of one click.
type outcome struct {
	Placed bool
	Name   string
	Props  map[string]any
	// Unmapped says a block appeared whose hash the vocabulary lacks.
	Unmapped bool
}

// key names the result: the block and its states, or "none".
func (o outcome) key() string {
	if !o.Placed {
		return "none"
	}
	if o.Unmapped {
		return "unmapped"
	}
	return o.Name + "|" + place.Key(o.Props, nil)
}

// states is the key without the block name, so siblings compare equal.
func (o outcome) states() string {
	if !o.Placed {
		return "none"
	}
	return place.Key(o.Props, nil)
}

// faceOrder is the order the baseline click is looked for: the plain top
// click first, since it is the cheapest.
var faceOrder = []face{fTop, fNorth, fSouth, fWest, fEast, fBottom}

// probeClicks is the first, small pass: each face at its centre, the edge
// hits on the top face and the height and side hits on the north face.
func probeClicks(lane bool) []click {
	var out []click
	if lane {
		out = append(out, click{Face: fTop, Hit: hMid, Lane: true})
		for _, h := range flatHits {
			out = append(out, click{Face: fTop, Hit: h, Lane: true})
		}
		return out
	}
	for _, f := range faceOrder {
		out = append(out, click{Face: f, Hit: hMid})
	}
	for _, h := range flatHits {
		out = append(out, click{Face: fTop, Hit: h})
	}
	for _, h := range sideHits {
		out = append(out, click{Face: fNorth, Hit: h})
	}
	return out
}

// lookClicks varies the unit's look and sneak around a baseline click.
func lookClicks(c0 click) []click {
	var out []click
	for _, y := range yaws[1:] {
		o := c0
		o.Yaw = y
		out = append(out, o)
	}
	for _, p := range pitches[1:] {
		o := c0
		o.Pitch = p
		out = append(out, o)
	}
	o := c0
	o.Sneak = true
	return append(out, o)
}

// dims is which inputs changed a block's state in the first pass.
type dims struct {
	Faces              bool
	FlatHits, SideHits []string
	Yaw, Pitch, Sneak  bool
}

// baseline is the first click that placed a block, in faceOrder.
func baseline(res map[string]outcome) (click, bool) {
	for _, f := range faceOrder {
		c := click{Face: f, Hit: hMid}
		if res[c.key()].Placed {
			return c, true
		}
	}
	return click{}, false
}

// analyse reads the first pass: an input is active when changing it alone
// gave a different block or state than its baseline.
func analyse(res map[string]outcome, c0 click) dims {
	var d dims
	lane := c0.Lane
	seen := map[string]bool{}
	for _, f := range faceOrder {
		if o := res[click{Face: f, Hit: hMid, Lane: lane}.key()]; o.Placed {
			seen[o.key()] = true
		}
	}
	d.Faces = !lane && len(seen) > 1
	differs := func(base, c click) bool {
		b, o := res[base.key()], res[c.key()]
		return o.Placed && b.Placed && o.key() != b.key()
	}
	topMid := click{Face: fTop, Hit: hMid, Lane: lane}
	for _, h := range flatHits {
		if differs(topMid, click{Face: fTop, Hit: h, Lane: lane}) {
			d.FlatHits = append(d.FlatHits, h)
		}
	}
	northMid := click{Face: fNorth, Hit: hMid}
	for _, h := range sideHits {
		if !lane && differs(northMid, click{Face: fNorth, Hit: h}) {
			d.SideHits = append(d.SideHits, h)
		}
	}
	for _, c := range lookClicks(c0) {
		if !differs(c0, c) {
			continue
		}
		switch {
		case c.Sneak:
			d.Sneak = true
		case c.Yaw != 0:
			d.Yaw = true
		case c.Pitch != 0:
			d.Pitch = true
		}
	}
	return d
}

// gridCap bounds one block's full pass; a grid past it is cut to its cheapest
// clicks and the block is reported truncated.
const gridCap = 360

// grid is the full pass: every combination of the active inputs, cheapest
// first so a cut keeps the clicks a planner would pick.
func grid(d dims, res map[string]outcome, c0 click) (clicks []click, truncated bool) {
	faces := []face{c0.Face}
	if d.Faces {
		faces = faces[:0]
		for _, f := range faceOrder {
			if res[click{Face: f, Hit: hMid}.key()].Placed {
				faces = append(faces, f)
			}
		}
	}
	ys, ps, ss := []int{0}, []int{0}, []bool{false}
	if d.Yaw {
		ys = yaws
	}
	if d.Pitch {
		ps = pitches
	}
	if d.Sneak {
		ss = []bool{false, true}
	}
	for _, f := range faces {
		hits := []string{hMid}
		if f.side() {
			hits = append(hits, d.SideHits...)
		} else {
			hits = append(hits, d.FlatHits...)
		}
		for _, h := range hits {
			for _, y := range ys {
				for _, p := range ps {
					for _, s := range ss {
						clicks = append(clicks, click{Face: f, Hit: h, Yaw: y, Pitch: p, Sneak: s, Lane: c0.Lane})
					}
				}
			}
		}
	}
	sort.SliceStable(clicks, func(i, j int) bool {
		ci, cj := clicks[i].cost(), clicks[j].cost()
		if ci != cj {
			return ci < cj
		}
		return clicks[i].key() < clicks[j].key()
	})
	if len(clicks) > gridCap {
		return clicks[:gridCap], true
	}
	return clicks, false
}

// signature fingerprints the first pass by the states it gave, not the block
// names, so every stair or slab of a family gets the same one.
func signature(res map[string]outcome, c0 click) string {
	var sb strings.Builder
	for _, c := range probeClicks(c0.Lane) {
		sb.WriteString(res[c.key()].states())
		sb.WriteByte('\n')
	}
	for _, c := range lookClicks(c0) {
		sb.WriteString(res[c.key()].states())
		sb.WriteByte('\n')
	}
	fmt.Fprintf(&sb, "c0=%s", c0.key())
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:8])
}

// sample picks up to n clicks of the grid to confirm an inferred family,
// spread evenly and without the random choice a rerun would not repeat.
func sample(clicks []click, n int) []click {
	if len(clicks) <= n {
		return clicks
	}
	out := make([]click, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, clicks[i*len(clicks)/n])
	}
	return out
}
