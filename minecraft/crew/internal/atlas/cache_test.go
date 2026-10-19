package atlas

import (
	"bytes"
	"context"
	"errors"
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

// An entry cached before the item sheet existed is rebuilt, but when the
// rebuild cannot fetch, its terrain still serves.
func TestEnsureFallsBackToAnEntryWithoutItems(t *testing.T) {
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	f := gh.fetcher()
	opts := Options{TileSize: 4}
	first, pack, err := Ensure(context.Background(), f, "1.26.52.3", cache, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cache, pack.Tag, atlasSubdir, itemsTableFile)); err != nil {
		t.Fatal(err)
	}
	gh.Close()
	got, _, err := Ensure(context.Background(), f, "1.26.52.3", cache, opts)
	if err != nil {
		t.Fatalf("no fallback: %v", err)
	}
	if !bytes.Equal(got.PNG(), first.PNG()) || got.ItemsErr() == nil {
		t.Errorf("fallback png equal %v, items err %v", bytes.Equal(got.PNG(), first.PNG()), got.ItemsErr())
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
	want := []string{"index.json", "v1.26.50.4/atlas/atlas.png", "v1.26.50.4/atlas/entities.failed", "v1.26.50.4/atlas/items.json", "v1.26.50.4/atlas/items.png", "v1.26.50.4/atlas/table.json"}
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

func TestSaveRemovesStaleEntityFilesWhenThereAreNone(t *testing.T) {
	dir := t.TempDir()
	a := &Atlas{png: []byte("p"), table: Table{Version: tableVersion}, entPNG: []byte("e"), entities: EntityTable{Version: entityTableVersion}}
	if err := a.Save(dir); err != nil {
		t.Fatal(err)
	}
	b := &Atlas{png: []byte("p"), table: Table{Version: tableVersion}, entitiesErr: errors.New("no models")}
	if err := b.Save(dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{entPNGFile, entTableFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s survived", f)
		}
	}
	got, err := LoadAtlas(dir)
	if err != nil || got.EntitiesErr() == nil || !got.entitiesFailed {
		t.Fatalf("load = %v, err %v, failed %v", got, err, got != nil && got.entitiesFailed)
	}
}

// A pack whose entity build fails is not downloaded again at every start.
func TestEnsureDoesNotRefetchForAFailedEntityBuildAndReportsFallbacks(t *testing.T) {
	gh := fixtureGitHub(t)
	cache := t.TempDir()
	f := gh.fetcher()
	opts := Options{TileSize: 4}
	first, _, err := Ensure(context.Background(), f, "1.26.52.3", cache, opts)
	if err != nil || first.EntitiesErr() == nil {
		t.Fatalf("first = %v, %v", first, err)
	}
	gh.Close()
	var fell []error
	opts.OnFallback = func(err error) { fell = append(fell, err) }
	second, _, err := Ensure(context.Background(), f, "1.26.52.3", cache, opts)
	if err != nil || len(fell) != 0 || second.EntitiesErr() == nil {
		t.Fatalf("second = %v, err %v, fallbacks %v", second, err, fell)
	}

	// An entry that lacks its items is rebuilt, and the failure is reported.
	if err := os.Remove(filepath.Join(cache, testTag, atlasSubdir, itemsTableFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(context.Background(), f, "1.26.52.3", cache, opts); err != nil || len(fell) != 1 {
		t.Fatalf("err %v, fallbacks %v", err, fell)
	}
}
