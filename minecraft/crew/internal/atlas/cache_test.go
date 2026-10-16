package atlas

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixtureTarball(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir("testdata/pack", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("testdata/pack", p)
		data, err := os.ReadFile(p)
		files["Mojang-bedrock-samples-abc/resource_pack/"+filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return makeTarball(t, files)
}

func TestEnsureBuildsOnceAndReloadsFromCache(t *testing.T) {
	gh := newFakeGitHub(t, fixtureTarball(t))
	cache := t.TempDir()
	f := Fetcher{Client: gh.Client(), BaseURL: gh.URL}
	opts := Options{TileSize: 4}

	first, pack, err := Ensure(context.Background(), f, "1.26.52.3", cache, opts)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Tag != "v1.26.50.4" || len(first.Table().Blocks) == 0 {
		t.Fatalf("pack %+v, blocks %d", pack, len(first.Table().Blocks))
	}

	// Remove the source pack and the network: the built atlas alone must serve.
	gh.Close()
	if err := os.RemoveAll(pack.Dir); err != nil {
		t.Fatal(err)
	}
	second, _, err := Ensure(context.Background(), f, "1.26.52.3", cache, opts)
	if err != nil {
		t.Fatalf("cached ensure: %v", err)
	}
	if !bytes.Equal(first.PNG(), second.PNG()) {
		t.Error("cached PNG differs")
	}
	if !reflect.DeepEqual(first.Table(), second.Table()) {
		t.Error("cached table differs")
	}
	if got := second.TintColor(TintGrass, 1, 1); got != first.TintColor(TintGrass, 1, 1) {
		t.Errorf("cached colour maps differ: %v", got)
	}
}

func TestLoadRejectsStaleTableVersion(t *testing.T) {
	a := buildFixture(t)
	dir := t.TempDir()
	a.table.Version = tableVersion - 1
	if err := a.Save(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAtlas(dir); err == nil {
		t.Fatal("stale cache entry should not load")
	}
}
