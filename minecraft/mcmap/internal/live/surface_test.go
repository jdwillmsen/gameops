package live

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A hill 70 high at the origin's chunk and the sea at 62 everywhere else.
func hills(_ string, x, z int32) (int32, bool) {
	switch {
	case x >= 0 && x < 16 && z >= 0 && z < 16:
		return 70, true
	case x < -1000:
		return 0, false
	}
	return 62, true
}

const surfaced = `{"gen":1,"kind":"mobs","dim":"overworld","part":0,"parts":1,"more":0,"items":[` +
	`{"i":"a","t":"zombie","x":5.9,"y":75,"z":3.2},` +
	`{"i":"b","t":"cow","x":-0.5,"y":60,"z":-0.5},` +
	`{"i":"c","t":"cow","x":-2000,"y":60,"z":0},` +
	`{"i":"d","t":"cow","x":1e9,"y":60,"z":0,"s":99}]}`

func TestLayerSaysTheSurfaceUnderEachEntityOfASample(t *testing.T) {
	l := New(10*time.Second, 1000, nil)
	l.Surface = hills
	l.Ingest(raw(1, t0, surfaced), t0)
	f := decode(t, string(l.Current("overworld", t0).Data))
	want := map[string]*int32{"a": ptr(70), "b": ptr(62), "c": nil, "d": nil}
	if len(f.Mobs) != len(want) {
		t.Fatalf("mobs = %+v", f.Mobs)
	}
	for _, m := range f.Mobs {
		got := m.Surface
		if (got == nil) != (want[m.ID] == nil) || (got != nil && *got != *want[m.ID]) {
			t.Errorf("mob %s: surface = %v, want %v", m.ID, deref(got), deref(want[m.ID]))
		}
	}
}

// -0.5 is in the block west and north of the origin, not in the origin's.
func TestLayerFindsTheBlockOfANegativePositionByFlooring(t *testing.T) {
	l := New(10*time.Second, 1000, nil)
	var asked [][2]int32
	l.Surface = func(_ string, x, z int32) (int32, bool) {
		asked = append(asked, [2]int32{x, z})
		return 0, false
	}
	l.Ingest(raw(1, t0, surfaced), t0)
	if asked[1] != [2]int32{-1, -1} || asked[0] != [2]int32{5, 3} {
		t.Errorf("columns asked of = %v", asked)
	}
}

func TestLayerLeavesTheSurfaceOutWhereItCannotBeToldAndNeverTakesThePacksOwn(t *testing.T) {
	l := New(10*time.Second, 1000, nil)
	l.Ingest(raw(1, t0, surfaced), t0)
	if s := string(l.Current("overworld", t0).Data); strings.Contains(s, `"s":`) {
		t.Errorf("a frame with no surface source or the pack's own claim carries one: %s", s)
	}
}

func BenchmarkAnnotateAThousandMobs(b *testing.B) {
	l := New(10*time.Second, 1000, nil)
	l.Surface = hills
	rec, _, err := Parse(mobsJSON(1, "overworld", 1000))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		l.annotate(&rec)
	}
}

func BenchmarkParseAThousandMobs(b *testing.B) {
	data := mobsJSON(1, "overworld", 1000)
	b.ResetTimer()
	for range b.N {
		if _, _, err := Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}

func ptr(v int32) *int32 { return &v }

func deref(p *int32) any {
	if p == nil {
		return nil
	}
	return *p
}

// herdLines is a thousand mobs as the pack prints them, in lines of forty.
func herdLines(gen int) []string {
	const part = 40
	var lines []string
	for i := 0; i < 1000; i += part {
		items := make([]Entity, 0, part)
		for j := i; j < i+part; j++ {
			items = append(items, Entity{ID: fmt.Sprint(j), Type: "zombie", X: float64(j*3%700 - 350), Y: 64, Z: float64(j*7%700 - 350)})
		}
		b, _ := json.Marshal(map[string]any{"gen": gen, "dim": "overworld", "kind": "mobs", "part": i / part, "parts": 1000 / part, "more": 0, "items": items})
		lines = append(lines, string(b))
	}
	return lines
}

// What taking in a sample of a thousand mobs costs, and what its frame
// costs to write out and weighs, with the surface on each and without.
func BenchmarkASampleAndFrameOfAThousandMobs(b *testing.B) {
	for name, surface := range map[string]func(string, int32, int32) (int32, bool){"without surfaces": nil, "with surfaces": hills} {
		b.Run(name, func(b *testing.B) {
			l := New(10*time.Second, 1000, nil)
			l.Surface = surface
			sample := [2][]string{herdLines(1), herdLines(2)}
			b.Run("ingest", func(b *testing.B) {
				for i := range b.N {
					// Two samples to and fro, each a newer generation than the last.
					for n, line := range sample[i%2] {
						l.Ingest(raw(int64(i*100+n), t0, strings.Replace(line, `"gen":`+fmt.Sprint(1+i%2), `"gen":`+fmt.Sprint(i+1), 1)), t0)
					}
				}
			})
			b.Run("frame", func(b *testing.B) {
				var size int
				for range b.N {
					size = len(l.Current("overworld", t0).Data)
				}
				b.ReportMetric(float64(size), "bytes/frame")
			})
		})
	}
}
