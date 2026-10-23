package main

import (
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

func TestApplyRallyOverrides(t *testing.T) {
	bookAfter := arena.Duration{Duration: 20 * time.Second}
	bookEvery := arena.Duration{Duration: 25 * time.Second}
	tests := []struct {
		name         string
		after, every time.Duration
		wantAfter    time.Duration
		wantEvery    time.Duration
	}{
		{"flags unset keep the book", -1, -1, 20 * time.Second, 25 * time.Second},
		{"zero disables", 0, -1, 0, 25 * time.Second},
		{"both set", 5 * time.Second, 7 * time.Second, 5 * time.Second, 7 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			book := &arena.WaveBook{RallyAfter: &bookAfter, RallyEvery: &bookEvery}
			applyRallyOverrides(book, tt.after, tt.every)
			if book.RallyAfter.Duration != tt.wantAfter {
				t.Errorf("rally_after = %v, want %v", book.RallyAfter.Duration, tt.wantAfter)
			}
			if book.RallyEvery.Duration != tt.wantEvery {
				t.Errorf("rally_every = %v, want %v", book.RallyEvery.Duration, tt.wantEvery)
			}
		})
	}
}
