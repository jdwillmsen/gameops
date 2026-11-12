package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
)

func TestUnitSkinFollowsRoleAndOverride(t *testing.T) {
	log := logging.New("")
	dir := t.TempDir()
	cfg := config.Config{Username: "Agate", SkinRole: "miner", SkinDir: dir}

	if got, want := unitSkinFor(cfg, log), skin.ForPersona("Agate", skin.Persona{Role: "miner"}); got != want {
		t.Error("a miner without an override should wear the miner outfit")
	}
	cfg.Role = "ranger"
	if got, want := unitSkinFor(cfg, log), skin.ForPersona("Agate", skin.Persona{Role: "warrior", SquadRole: "ranger"}); got != want {
		t.Error("a warrior should wear its squad-role outfit")
	}

	custom := skin.ForPersona("Other", skin.Persona{Role: "shaft"})
	b, _ := custom.PNG()
	if err := os.WriteFile(filepath.Join(dir, "Agate.png"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := unitSkinFor(cfg, log); got.Data != custom.Data {
		t.Error("an override file should replace the generated outfit")
	}

	if err := os.WriteFile(filepath.Join(dir, "Agate.png"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := unitSkinFor(cfg, log); got.Data == "" {
		t.Error("a bad override must fall back to a generated skin, not no skin")
	}
}

func TestChopperUnitWearsChopperOutfitDeterministically(t *testing.T) {
	log := logging.New("")
	cfg := config.Config{Username: "Dotablaze7321", SkinRole: "chopper"}

	got := unitSkinFor(cfg, log)
	if want := skin.ForPersona("Dotablaze7321", skin.Persona{Role: "chopper"}); got != want {
		t.Error("a chopper should wear the chopper outfit")
	}
	if got != unitSkinFor(cfg, log) {
		t.Error("the same name and role must give the same skin")
	}
	if got == skin.For("Dotablaze7321") {
		t.Error("a chopper should not wear the plain name-hash skin")
	}
	if got == skin.ForPersona("Dotablaze7321", skin.Persona{Role: "miner"}) {
		t.Error("a chopper and a miner of the same name should differ")
	}
}
