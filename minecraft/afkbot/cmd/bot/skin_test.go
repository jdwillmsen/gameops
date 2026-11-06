package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/afkbot/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/afkbot/internal/logging"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
)

func TestBotSkinPrefersOverrideThenTheme(t *testing.T) {
	log := logging.New("error")
	dir := t.TempDir()
	cfg := config.Config{Username: "bot1", SkinTheme: "dotablaze", SkinDir: dir}

	themed := botSkinFor(cfg, log)
	if want, _ := skin.ForTheme("bot1", "dotablaze"); themed != want {
		t.Fatal("without a file the themed skin should be used")
	}

	custom, _ := skin.ForTheme("bot1", "neon")
	b, err := custom.PNG()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bot1.png"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := botSkinFor(cfg, log); got.Data != custom.Data {
		t.Fatal("a file in SKIN_DIR should win over the theme")
	}

	cfg.SkinTheme = "bogus"
	cfg.SkinDir = ""
	if got := botSkinFor(cfg, log); got != skin.For("bot1") {
		t.Fatal("an unknown theme should fall back to the plain skin")
	}
}
