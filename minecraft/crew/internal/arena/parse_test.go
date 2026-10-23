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

func TestCountCommandsFilterItemDrops(t *testing.T) {
	if got, want := countCommand(), "testfor @e[tag=arena_wave,type=!item]"; got != want {
		t.Errorf("countCommand = %q, want %q", got, want)
	}
	if got, want := bossCountCommand(), "testfor @e[tag=arena_boss,type=!item]"; got != want {
		t.Errorf("bossCountCommand = %q, want %q", got, want)
	}
}
