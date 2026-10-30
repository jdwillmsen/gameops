package main

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

func TestTheTwoKnownLabConstantsAgree(t *testing.T) {
	if tacticsctl.KnownLab != arena.KnownLab {
		t.Fatalf("tacticsctl.KnownLab %q != arena.KnownLab %q", tacticsctl.KnownLab, arena.KnownLab)
	}
	if err := tacticsctl.LabOnly(arena.KnownLab); err != nil {
		t.Fatalf("LabOnly refuses the lab arena trusts: %v", err)
	}
	if err := arena.ValidateLabTarget(arena.KnownLab); err != nil {
		t.Fatalf("ValidateLabTarget refuses it too: %v", err)
	}
}
