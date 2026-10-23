package arena

import (
	"encoding/json"
	"fmt"
	"os"
)

// maxAreaChunks is Bedrock's chunk limit for one ticking area.
const maxAreaChunks = 100

// Layout is the document `labcourses arena-layout` prints: the pen spawn
// points, the iron-bar gate boxes the director opens, and the squad start gate.
type Layout struct {
	Center      Point     `json:"center"`
	GroundY     int       `json:"ground_y"`
	FloorRadius int       `json:"floor_radius"`
	OuterRadius int       `json:"outer_radius"`
	Areas       []Area    `json:"areas"`
	Pens        []Pen     `json:"pens"`
	StartGate   StartGate `json:"start_gate"`
	Boss        Point     `json:"boss"`
}

// Area is a chunk-aligned column the run must keep loaded.
type Area struct {
	X0 int `json:"x0"`
	Z0 int `json:"z0"`
	X1 int `json:"x1"`
	Z1 int `json:"z1"`
}

// chunks counts the whole chunk columns the area spans.
func (a Area) chunks() int {
	x0, x1 := a.X0, a.X1
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	z0, z1 := a.Z0, a.Z1
	if z0 > z1 {
		z0, z1 = z1, z0
	}
	return ((x1 >> 4) - (x0 >> 4) + 1) * ((z1 >> 4) - (z0 >> 4) + 1)
}

type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

// Box is an inclusive three-dimensional box.
type Box struct {
	X0 int `json:"x0"`
	Y0 int `json:"y0"`
	Z0 int `json:"z0"`
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	Z1 int `json:"z1"`
}

// Fill is the console command that replaces the box.
func (b Box) Fill(block string) string {
	return fmt.Sprintf("fill %d %d %d %d %d %d %s", b.X0, b.Y0, b.Z0, b.X1, b.Y1, b.Z1, block)
}

// volume counts the blocks the box covers, inclusive.
func (b Box) volume() int {
	return (axisLen(b.X0, b.X1)) * (axisLen(b.Y0, b.Y1)) * (axisLen(b.Z0, b.Z1))
}

func axisLen(a, b int) int {
	if a > b {
		return a - b + 1
	}
	return b - a + 1
}

// Pen is a spawn room and the gate that holds its mobs in.
type Pen struct {
	ID    int    `json:"id"`
	Spawn Point  `json:"spawn"`
	Gate  Box    `json:"gate"`
	Open  string `json:"open"`
}

type StartGate struct {
	Name string `json:"name"`
	Gate Box    `json:"gate"`
	Open string `json:"open"`
}

// LoadLayout reads the layout JSON.
func LoadLayout(path string) (Layout, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Layout{}, err
	}
	var l Layout
	if err := json.Unmarshal(data, &l); err != nil {
		return Layout{}, fmt.Errorf("parse layout: %w", err)
	}
	if err := l.validate(); err != nil {
		return Layout{}, err
	}
	return l, nil
}

func (l Layout) validate() error {
	for i, a := range l.Areas {
		if a.X1 < a.X0 || a.Z1 < a.Z0 {
			return fmt.Errorf("layout area %d has inverted bounds", i)
		}
		if n := a.chunks(); n > maxAreaChunks {
			return fmt.Errorf("layout area %d spans %d chunks, at most %d load at once", i, n, maxAreaChunks)
		}
	}
	if len(l.Pens) == 0 {
		return fmt.Errorf("layout has no pens")
	}
	for _, p := range l.Pens {
		if p.ID < 1 {
			return fmt.Errorf("layout has a pen with id %d", p.ID)
		}
		if p.Open == "" {
			return fmt.Errorf("layout pen %d has no open command", p.ID)
		}
		if p.Spawn.Y == 0 {
			return fmt.Errorf("layout pen %d has no spawn height", p.ID)
		}
	}
	if l.StartGate.Open == "" {
		return fmt.Errorf("layout has no squad start gate")
	}
	if l.Boss.Y == 0 {
		return fmt.Errorf("layout has no boss point")
	}
	return nil
}

// Pen returns pen id, the numbers being the JSON ids (1-based).
func (l Layout) Pen(id int) (Pen, bool) {
	for _, p := range l.Pens {
		if p.ID == id {
			return p, true
		}
	}
	return Pen{}, false
}
