package arena

import "testing"

func TestParseCount(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want int
	}{
		{"single", "Found Zombie", 1},
		{"duplicates count separately", "Found Zombie, Zombie, Skeleton", 3},
		{"same name twice", "Found Vanguard, Vanguard", 2},
		{"prefixed log line", "[2026-10-23 04:00:00:000 INFO] Found Zombie, Skeleton", 2},
		{"multi line picks the reply", "noise\nFound Zombie\nmore noise", 1},
		{"zero is an error line", "ERROR: No targets matched selector", 0},
		{"trailing comma is not a mob", "Found Zombie, ", 1},
		{"empty output", "", -1},
		{"unrelated output", "Object successfully summoned", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCount(tt.out)
			if tt.want < 0 {
				if err == nil {
					t.Fatalf("ParseCount(%q) = %d, want an error", tt.out, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCount(%q): %v", tt.out, err)
			}
			if got != tt.want {
				t.Errorf("ParseCount(%q) = %d, want %d", tt.out, got, tt.want)
			}
		})
	}
}

func TestParseTargetPosition(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want TargetPosition
		ok   bool
	}{
		{"array", `[{"dimension":"overworld","id":1,"position":{"x":40,"y":65,"z":-760},"uniqueId":"x","yRot":90}]`, TargetPosition{40, 65, -760}, true},
		{"prefixed log line", `[2026-10-23 04:00:00:000 INFO] [{"position":{"x":1.5,"y":64.0,"z":-759.5}}]`, TargetPosition{1.5, 64, -759.5}, true},
		{"pretty printed", "[\n  {\n    \"position\": {\"x\": 0.5, \"y\": 65.0, \"z\": -760.25}\n  }\n]", TargetPosition{0.5, 65, -760.25}, true},
		{"trailing log line", `[{"position":{"x":2,"y":65,"z":-758}}] [INFO] sent`, TargetPosition{2, 65, -758}, true},
		{"first of several", `[{"position":{"x":2,"y":65,"z":-758}},{"position":{"x":9,"y":65,"z":-751}}]`, TargetPosition{2, 65, -758}, true},
		{"empty array", "[]", TargetPosition{}, false},
		{"no json", "No targets matched selector", TargetPosition{}, false},
		{"bad json", "[oops]", TargetPosition{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTargetPosition(tt.out)
			if !tt.ok {
				if err == nil {
					t.Fatalf("ParseTargetPosition(%q) = %+v, want an error", tt.out, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTargetPosition(%q): %v", tt.out, err)
			}
			if got != tt.want {
				t.Errorf("ParseTargetPosition(%q) = %+v, want %+v", tt.out, got, tt.want)
			}
		})
	}
}

func TestCountCommandsFilterItemDrops(t *testing.T) {
	if got, want := countCommand(), "testfor @e[tag=arena_wave,type=!item]"; got != want {
		t.Errorf("countCommand = %q, want %q", got, want)
	}
	if got, want := bossCountCommand(), "testfor @e[tag=arena_boss,type=!item]"; got != want {
		t.Errorf("bossCountCommand = %q, want %q", got, want)
	}
}
