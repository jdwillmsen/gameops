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

func fixtureGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	var tree []treeEntry
	files := map[string]string{}
	err := filepath.WalkDir("testdata/pack", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("testdata/pack", p)
		data, err := os.ReadFile(p)
		repoPath := "resource_pack/" + filepath.ToSlash(rel)
		tree = append(tree, blob(repoPath))
		files[repoPath] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return newFakeGitHub(t, tree, files)
}

func TestEnsureBuildsOnceAndReloadsFromCache(t *testing.T) {
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	f := gh.fetcher()
	opts := Options{TileSize: 4}

	first, pack, err := Ensure(context.Background(), f, "1.26.52.3", cache, opts)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Tag != "v1.26.50.4" || pack.Dir != "" || len(first.Table().Blocks) == 0 {
		t.Fatalf("pack %+v, blocks %d", pack, len(first.Table().Blocks))
	}

	// Without the network the built atlas alone must serve.
	gh.Close()
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

func TestEnsureKeepsOnlyTheBuiltAtlasInTheCache(t *testing.T) {
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	if _, _, err := Ensure(context.Background(), gh.fetcher(), "1.26.52.3", cache, Options{TileSize: 4}); err != nil {
		t.Fatal(err)
	}
	var got []string
	err := filepath.WalkDir(cache, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(cache, p)
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"index.json", "v1.26.50.4/atlas/atlas.png", "v1.26.50.4/atlas/table.json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cache holds %v, want %v", got, want)
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
