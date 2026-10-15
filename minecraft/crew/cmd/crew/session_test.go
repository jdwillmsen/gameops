package main

import (
	"strings"
	"testing"
)

func TestSessionLogFieldsCarryNoPosition(t *testing.T) {
	f := spawnedFields("2535400000000000", 8)
	for k := range f {
		switch strings.ToLower(k) {
		case "x", "y", "z", "pos", "position", "coords", "coordinates", "dimension":
			t.Errorf("spawn log carries %q; positions never go to logs", k)
		}
	}
	if f["xuid"] == nil || f["view_distance"] == nil {
		t.Errorf("fields = %v, want xuid and view_distance", f)
	}
}
