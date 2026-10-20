package atlas

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A cache written before armour has an entity table at version 1. It still
// draws every model, so it is loaded and served while the build is retried,
// not discarded for stand-in boxes.
func TestEntityTableFromBeforeArmourLoadsButIsNotComplete(t *testing.T) {
	dir := t.TempDir()
	a := &Atlas{png: []byte("p"), table: Table{Version: tableVersion}, itemPNG: []byte("i"), items: ItemTable{},
		entPNG: []byte("e"), entities: EntityTable{Version: 1, Width: 1, Height: 1, Models: map[string]EntityModel{"minecraft:x": {Height: 1}}}}
	if err := a.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAtlas(dir)
	if err != nil || got.EntitiesErr() != nil {
		t.Fatalf("load = %v, %v", err, got.EntitiesErr())
	}
	if len(got.Entities().Models) != 1 || got.Complete() {
		t.Errorf("models %d complete %v; want the old models served and a rebuild wanted", len(got.Entities().Models), got.Complete())
	}

	// The current version is complete.
	raw, err := os.ReadFile(filepath.Join(dir, entTableFile))
	if err != nil {
		t.Fatal(err)
	}
	var tbl EntityTable
	if err := json.Unmarshal(raw, &tbl); err != nil {
		t.Fatal(err)
	}
	tbl.Version = entityTableVersion
	raw, _ = json.Marshal(tbl)
	if err := os.WriteFile(filepath.Join(dir, entTableFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadAtlas(dir); err != nil || !got.Complete() {
		t.Errorf("current table: %v complete %v", err, got != nil && got.Complete())
	}

	// A version from the future is refused.
	tbl.Version = entityTableVersion + 1
	raw, _ = json.Marshal(tbl)
	if err := os.WriteFile(filepath.Join(dir, entTableFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadAtlas(dir); err != nil || got.EntitiesErr() == nil {
		t.Errorf("future table accepted: %v", err)
	}
}
