package main

import (
	"testing"
)

func placed(name string, kv ...any) outcome {
	p := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		p[kv[i].(string)] = kv[i+1]
	}
	return outcome{Placed: true, Name: name, Props: p}
}

func TestHitVecPutsTheHitOnTheClickedFace(t *testing.T) {
	cases := []struct {
		f    face
		hit  string
		want [3]float32
	}{
		{fTop, hMid, [3]float32{0.5, 1, 0.5}},
		{fBottom, hMid, [3]float32{0.5, 0, 0.5}},
		{fNorth, hHigh, [3]float32{0.5, 0.75, 0}},
		{fSouth, hLow, [3]float32{0.5, 0.25, 1}},
		{fEast, hLeft, [3]float32{1, 0.5, 0.1}},
		{fWest, hRight, [3]float32{0, 0.5, 0.9}},
		{fNorth, hRight, [3]float32{0.9, 0.5, 0}},
		{fTop, hXm, [3]float32{0.1, 1, 0.5}},
		{fBottom, hZp, [3]float32{0.5, 0, 0.9}},
	}
	for _, c := range cases {
		if got := hitVec(c.f, c.hit); got != c.want {
			t.Errorf("%s %s = %v, want %v", c.f, c.hit, got, c.want)
		}
	}
}

func TestOppositeFacesPairUp(t *testing.T) {
	for f := fBottom; f <= fEast; f++ {
		if f.opposite().opposite() != f || f.opposite() == f {
			t.Errorf("%s -> %s", f, f.opposite())
		}
		a, b := faceOff[f], faceOff[f.opposite()]
		if a[0] != -b[0] || a[1] != -b[1] || a[2] != -b[2] {
			t.Errorf("%s offsets %v %v", f, a, b)
		}
	}
}

// stairs-like responses: the half follows the face and the hit height, the
// facing follows the yaw.
func stairsFirstPass() map[string]outcome {
	res := map[string]outcome{}
	half := func(c click) bool {
		switch {
		case c.Face == fBottom:
			return true
		case c.Face.side() && (c.Hit == hHigh):
			return true
		}
		return false
	}
	for _, c := range append(probeClicks(false), lookClicks(click{Face: fTop, Hit: hMid})...) {
		res[c.key()] = placed("oak_stairs", "upside_down_bit", half(c), "facing", c.Yaw/90)
	}
	return res
}

func TestAnalyseFindsTheInputsThatMatter(t *testing.T) {
	res := stairsFirstPass()
	c0, ok := baseline(res)
	if !ok || c0.Face != fTop {
		t.Fatalf("baseline %v %v", c0, ok)
	}
	d := analyse(res, c0)
	if !d.Faces || !d.Yaw || d.Pitch || d.Sneak {
		t.Errorf("dims = %+v", d)
	}
	if len(d.SideHits) != 1 || d.SideHits[0] != hHigh {
		t.Errorf("side hits = %v, want [high]", d.SideHits)
	}
	if len(d.FlatHits) != 0 {
		t.Errorf("flat hits = %v", d.FlatHits)
	}
}

func TestGridCoversOnlyActiveInputsCheapestFirst(t *testing.T) {
	res := stairsFirstPass()
	c0, _ := baseline(res)
	d := analyse(res, c0)
	clicks, truncated := grid(d, res, c0)
	if truncated {
		t.Fatal("truncated")
	}
	// 6 faces; side faces have mid and high (4 x 2), top and bottom mid only;
	// all with 4 yaws: (4*2 + 2) * 4.
	if len(clicks) != 40 {
		t.Fatalf("%d clicks, want 40", len(clicks))
	}
	if clicks[0].Face != fTop || clicks[0].Yaw != 0 {
		t.Errorf("first = %+v", clicks[0])
	}
	for i := 1; i < len(clicks); i++ {
		if clicks[i-1].cost() > clicks[i].cost() {
			t.Fatalf("not ordered at %d", i)
		}
	}
	for _, c := range clicks {
		if c.Pitch != 0 || c.Sneak {
			t.Fatalf("inactive input varied: %+v", c)
		}
	}
}

func TestGridOfAnInertBlockIsOneClick(t *testing.T) {
	res := map[string]outcome{}
	for _, c := range append(probeClicks(false), lookClicks(click{Face: fTop, Hit: hMid})...) {
		res[c.key()] = placed("stone_like", "k", "v")
	}
	c0, _ := baseline(res)
	d := analyse(res, c0)
	clicks, _ := grid(d, res, c0)
	if len(clicks) != 1 || clicks[0] != c0 {
		t.Fatalf("clicks = %+v", clicks)
	}
}

func TestBaselineFallsBackToASideFace(t *testing.T) {
	res := map[string]outcome{
		click{Face: fNorth, Hit: hMid}.key(): placed("ladder", "facing", 2),
		click{Face: fEast, Hit: hMid}.key():  placed("ladder", "facing", 5),
	}
	c0, ok := baseline(res)
	if !ok || c0.Face != fNorth {
		t.Fatalf("c0 = %+v %v", c0, ok)
	}
	if _, ok := baseline(map[string]outcome{}); ok {
		t.Fatal("nothing placed, no baseline")
	}
}

func TestSignatureIgnoresTheBlockName(t *testing.T) {
	a, b := stairsFirstPass(), stairsFirstPass()
	for k, o := range b {
		o.Name = "birch_stairs"
		b[k] = o
	}
	c0 := click{Face: fTop, Hit: hMid}
	if signature(a, c0) != signature(b, c0) {
		t.Fatal("siblings must share a signature")
	}
	b[click{Face: fBottom, Hit: hMid}.key()] = placed("birch_stairs", "upside_down_bit", false, "facing", 0)
	if signature(a, c0) == signature(b, c0) {
		t.Fatal("different responses must differ")
	}
}

func TestSampleSpreadsAcrossTheGrid(t *testing.T) {
	var clicks []click
	for i := 0; i < 100; i++ {
		clicks = append(clicks, click{Yaw: i})
	}
	s := sample(clicks, 5)
	if len(s) != 5 || s[0].Yaw != 0 || s[4].Yaw != 80 {
		t.Fatalf("sample = %+v", s)
	}
	if len(sample(clicks[:3], 5)) != 3 {
		t.Fatal("a small grid is sampled whole")
	}
}

func TestLaneItemsAreProbedOnTheTopFaceOnly(t *testing.T) {
	c0 := click{Face: fTop, Hit: hMid, Lane: true}
	if got := probeClicks(true); len(got) != 5 || !got[0].Lane || got[0] != c0 {
		t.Fatalf("probes = %+v", got)
	}
	res := map[string]outcome{}
	for _, c := range append(probeClicks(true), lookClicks(c0)...) {
		res[c.key()] = placed("bed", "direction", c.Yaw/90)
	}
	d := analyse(res, c0)
	if d.Faces || !d.Yaw || len(d.SideHits) != 0 {
		t.Fatalf("dims = %+v", d)
	}
	clicks, _ := grid(d, res, c0)
	if len(clicks) != 4 {
		t.Fatalf("%d clicks, want one per yaw", len(clicks))
	}
	for _, c := range clicks {
		if !c.Lane || c.Face != fTop {
			t.Fatalf("click left the lane: %+v", c)
		}
	}
}

func TestSiteForALaneClickIsTheFloorUnderTheLane(t *testing.T) {
	b := bay{ox: 0, f: 10, oz: 0}
	against, target := b.site(click{Face: fTop, Lane: true})
	if target != b.lane() || against != floorUnder(b.lane()) {
		t.Fatalf("against %v target %v", against, target)
	}
	a2, t2 := b.site(click{Face: fBottom})
	if a2 != b.s1() || t2 != b.pos(3, 2, 0) {
		t.Fatalf("bottom: against %v target %v", a2, t2)
	}
}
