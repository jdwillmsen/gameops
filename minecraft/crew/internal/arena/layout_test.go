package arena

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalLayout = `{
  "center": {"x": 0, "z": -760},
  "ground_y": 63,
  "floor_radius": 45,
  "outer_radius": 70,
  "pens": [
    {"id": 1, "angle_deg": 15, "spawn": {"x": 12, "y": 64, "z": -712},
     "gate": {"x0": 18, "y0": 64, "z0": -713, "x1": 18, "y1": 66, "z1": -711},
     "open": "fill 18 64 -713 18 66 -711 air"}
  ],
  "arched_gates": [],
  "start_gate": {"name": "squad_start",
    "gate": {"x0": -3, "y0": 64, "z0": -709, "x1": 3, "y1": 67, "z1": -709},
    "open": "fill -3 64 -709 3 67 -709 air"},
  "boss": {"x": 0, "y": 65, "z": -760}
}`

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "arena.layout.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadLayout(t *testing.T) {
	l, err := LoadLayout(writeTemp(t, minimalLayout))
	if err != nil {
		t.Fatalf("LoadLayout: %v", err)
	}
	if len(l.Pens) != 1 || l.Pens[0].ID != 1 || l.Pens[0].Spawn.Y != 64 {
		t.Errorf("pens = %+v", l.Pens)
	}
	if l.Boss.X != 0 || l.Boss.Z != -760 {
		t.Errorf("boss = %+v", l.Boss)
	}
	if l.StartGate.Open == "" {
		t.Error("start gate lost its open command")
	}
	p, ok := l.Pen(1)
	if !ok || p.Gate.X0 != 18 {
		t.Errorf("Pen(1) = %+v, %v", p, ok)
	}
	if _, ok := l.Pen(9); ok {
		t.Error("Pen(9) exists in a one-pen layout")
	}
}

func TestLayoutRejectsBrokenDocuments(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{"no pens", func(s string) string {
			return strings.Replace(s, `"pens": [
    {"id": 1, "angle_deg": 15, "spawn": {"x": 12, "y": 64, "z": -712},
     "gate": {"x0": 18, "y0": 64, "z0": -713, "x1": 18, "y1": 66, "z1": -711},
     "open": "fill 18 64 -713 18 66 -711 air"}
  ]`, `"pens": []`, 1)
		}, "no pens"},
		{"no start gate", func(s string) string {
			return strings.Replace(s, `"open": "fill -3 64 -709 3 67 -709 air"`, `"open": ""`, 1)
		}, "start gate"},
		{"no boss", func(s string) string {
			return strings.Replace(s, `"boss": {"x": 0, "y": 65, "z": -760}`, `"boss": {"x": 0, "y": 0, "z": -760}`, 1)
		}, "boss point"},
		{"not json", func(string) string { return "{" }, "parse layout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadLayout(writeTemp(t, tt.mutate(minimalLayout)))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestBoxFill(t *testing.T) {
	b := Box{1, 64, -2, 3, 66, 0}
	if got, want := b.Fill("iron_bars"), "fill 1 64 -2 3 66 0 iron_bars"; got != want {
		t.Errorf("Fill = %q, want %q", got, want)
	}
}
