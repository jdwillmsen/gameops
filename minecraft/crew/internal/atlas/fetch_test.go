package atlas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testTag = "v1.26.50.4"

type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
}

func blob(p string) treeEntry { return treeEntry{Path: p, Mode: "100644", Type: "blob"} }

type fakeGitHub struct {
	*httptest.Server
	requests  atomic.Int32
	treeCalls atomic.Int32
	failFirst sync.Map // raw path -> remaining failures
	failAll   string   // raw path that always fails with 500
	mu        sync.Mutex
	rawHits   map[string]int
	tree      []treeEntry
	truncated bool
	files     map[string]string
}

func newFakeGitHub(t *testing.T, tree []treeEntry, files map[string]string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{tree: tree, files: files, rawHits: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		const rawPrefix = "/raw/Mojang/bedrock-samples/" + testTag + "/"
		switch {
		case r.URL.Path == "/repos/Mojang/bedrock-samples/tags":
			if r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte("[]"))
				return
			}
			_, _ = w.Write([]byte(`[{"name":"v1.26.60.29-preview"},{"name":"v1.26.50.4"},{"name":"v1.26.40.05"}]`))
		case r.URL.Path == "/repos/Mojang/bedrock-samples/git/trees/"+testTag:
			f.treeCalls.Add(1)
			if r.URL.Query().Get("recursive") != "1" {
				http.Error(w, "want recursive", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": f.tree, "truncated": f.truncated})
		case strings.HasPrefix(r.URL.Path, rawPrefix):
			p := strings.TrimPrefix(r.URL.Path, rawPrefix)
			f.mu.Lock()
			f.rawHits[p]++
			f.mu.Unlock()
			if p == f.failAll {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			if v, ok := f.failFirst.Load(p); ok {
				left := v.(*atomic.Int32)
				if left.Add(-1) >= 0 {
					http.Error(w, "flaky", http.StatusBadGateway)
					return
				}
			}
			body, ok := f.files[p]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) fetcher() Fetcher {
	return Fetcher{Client: f.Client(), BaseURL: f.URL, Backoff: time.Millisecond}
}

func (f *fakeGitHub) hits(p string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rawHits[p]
}

var packTree = []treeEntry{
	{Path: "resource_pack", Mode: "040000", Type: "tree"},
	blob("resource_pack/blocks.json"),
	blob("resource_pack/biomes_client.json"),
	blob("resource_pack/textures/terrain_texture.json"),
	blob("resource_pack/textures/blocks/stone.png"),
	blob("resource_pack/textures/blocks/sub/dirt.tga"),
	blob("resource_pack/textures/colormap/grass.png"),
	// Present in the pack but never read by Build.
	blob("resource_pack/textures/entity/zombie.png"),
	blob("resource_pack/sounds.json"),
	blob("behavior_pack/entities/zombie.json"),
	blob("README.md"),
	// Hostile or non-regular entries.
	blob("resource_pack/textures/blocks/../../../escape.txt"),
	blob("resource_pack/textures/blocks//double.png"),
	blob("/resource_pack/blocks.json"),
	{Path: "resource_pack/textures/blocks/link.png", Mode: "120000", Type: "blob"},
	{Path: "resource_pack/textures/blocks/submodule", Mode: "160000", Type: "commit"},
}

var packFiles = map[string]string{
	"resource_pack/blocks.json":                         "{}",
	"resource_pack/biomes_client.json":                  "{}",
	"resource_pack/textures/terrain_texture.json":       "{}",
	"resource_pack/textures/blocks/stone.png":           "png",
	"resource_pack/textures/blocks/sub/dirt.tga":        "tga",
	"resource_pack/textures/colormap/grass.png":         "grass",
	"resource_pack/textures/entity/zombie.png":          "zombie",
	"resource_pack/sounds.json":                         "{}",
	"resource_pack/textures/blocks/link.png":            "target",
	"resource_pack/textures/blocks/../../../escape.txt": "evil",
}

func TestFetchDownloadsOnlyWhatBuildReadsAndCaches(t *testing.T) {
	gh := newFakeGitHub(t, packTree, packFiles)
	cache := t.TempDir()
	f := gh.fetcher()
	var reported []Pack
	f.OnDownload = func(p Pack) { reported = append(reported, p) }

	pack, err := f.Fetch(context.Background(), "1.26.52.3", cache)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Tag != testTag {
		t.Fatalf("tag = %q", pack.Tag)
	}
	if want := filepath.Join(cache, testTag, "resource_pack"); pack.Dir != want {
		t.Fatalf("dir = %q, want %q", pack.Dir, want)
	}
	if pack.Bytes == 0 || pack.Files != 6 || pack.Requests == 0 || pack.Elapsed <= 0 {
		t.Fatalf("stats = %+v", pack)
	}
	if len(reported) != 1 || reported[0] != pack {
		t.Fatalf("OnDownload calls = %+v", reported)
	}
	// One tag-list page pair, one tree call and one request per file: no API call per file.
	if got := gh.treeCalls.Load(); got != 1 {
		t.Fatalf("tree calls = %d, want 1", got)
	}
	if want := int32(2 + 1 + 6); gh.requests.Load() != want || pack.Requests != int(want) {
		t.Fatalf("requests = %d (pack %d), want %d", gh.requests.Load(), pack.Requests, want)
	}

	var got []string
	root := filepath.Join(cache, testTag)
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	want := map[string]bool{
		".complete":                                   true,
		"resource_pack/blocks.json":                   true,
		"resource_pack/biomes_client.json":            true,
		"resource_pack/textures/terrain_texture.json": true,
		"resource_pack/textures/blocks/stone.png":     true,
		"resource_pack/textures/blocks/sub/dirt.tga":  true,
		"resource_pack/textures/colormap/grass.png":   true,
	}
	if len(got) != len(want) {
		t.Fatalf("cached files = %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected cached file %s (all: %v)", g, got)
		}
	}
	for _, p := range []string{"resource_pack/textures/entity/zombie.png", "resource_pack/sounds.json", "resource_pack/textures/blocks/link.png"} {
		if gh.hits(p) != 0 {
			t.Fatalf("%s should not have been requested", p)
		}
	}
	for _, unwanted := range []string{filepath.Join(cache, "escape.txt"), filepath.Join(filepath.Dir(cache), "escape.txt")} {
		if _, err := os.Stat(unwanted); err == nil {
			t.Fatalf("%s should not exist", unwanted)
		}
	}

	// A second call must not touch the network at all.
	gh.Close()
	again, err := f.Fetch(context.Background(), "1.26.52.3", cache)
	if err != nil {
		t.Fatalf("cached fetch: %v", err)
	}
	if again.Tag != pack.Tag || again.Dir != pack.Dir || again.Bytes != 0 || again.Requests != 0 {
		t.Fatalf("cached result = %+v", again)
	}
	if gh.treeCalls.Load() != 1 || len(reported) != 1 {
		t.Fatalf("tree calls = %d, reports = %d", gh.treeCalls.Load(), len(reported))
	}
}

func TestFetchKeepsOldFullPackCache(t *testing.T) {
	cache := t.TempDir()
	dir := filepath.Join(cache, testTag, "resource_pack", "textures", "entity")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, testTag, completeMark), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, indexFile), []byte(`{"1.26.52.3":"`+testTag+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f := Fetcher{Client: &http.Client{Transport: failTransport{}}}
	pack, err := f.Fetch(context.Background(), "1.26.52.3", cache)
	if err != nil || pack.Tag != testTag {
		t.Fatalf("pack %+v err %v", pack, err)
	}
}

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrHandlerTimeout
}

func TestFetchRetriesAFlakyFileOnce(t *testing.T) {
	gh := newFakeGitHub(t, packTree, packFiles)
	n := &atomic.Int32{}
	n.Store(1)
	gh.failFirst.Store("resource_pack/blocks.json", n)
	pack, err := gh.fetcher().Fetch(context.Background(), "1.26.52.3", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if gh.hits("resource_pack/blocks.json") != 2 || pack.Files != 6 {
		t.Fatalf("hits = %d, pack = %+v", gh.hits("resource_pack/blocks.json"), pack)
	}
}

func TestFetchGivesUpAfterOneRetryAndLeavesNoCache(t *testing.T) {
	gh := newFakeGitHub(t, packTree, packFiles)
	gh.failAll = "resource_pack/blocks.json"
	cache := t.TempDir()
	if _, err := gh.fetcher().Fetch(context.Background(), "1.26.52.3", cache); err == nil {
		t.Fatal("expected an error")
	}
	if got := gh.hits("resource_pack/blocks.json"); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(cache, testTag)); err == nil {
		t.Fatal("failed download left a cache entry behind")
	}
	if _, err := os.Stat(filepath.Join(cache, indexFile)); err == nil {
		t.Fatal("failed download wrote the version index")
	}
}

func TestFetchDoesNotRetryNotFound(t *testing.T) {
	files := map[string]string{}
	for k, v := range packFiles {
		files[k] = v
	}
	delete(files, "resource_pack/blocks.json")
	gh := newFakeGitHub(t, packTree, files)
	if _, err := gh.fetcher().Fetch(context.Background(), "1.26.52.3", t.TempDir()); err == nil {
		t.Fatal("expected an error")
	}
	if got := gh.hits("resource_pack/blocks.json"); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestFetchRejectsTruncatedTree(t *testing.T) {
	gh := newFakeGitHub(t, packTree, packFiles)
	gh.truncated = true
	_, err := gh.fetcher().Fetch(context.Background(), "1.26.52.3", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchTreeWithoutWantedFilesFails(t *testing.T) {
	gh := newFakeGitHub(t, []treeEntry{blob("README.md")}, nil)
	_, err := gh.fetcher().Fetch(context.Background(), "1.26.52.3", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "resource_pack") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchNoTagAtOrBelow(t *testing.T) {
	gh := newFakeGitHub(t, nil, nil)
	_, err := gh.fetcher().Fetch(context.Background(), "1.0.0.1", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no bedrock-samples tag") {
		t.Fatalf("err = %v", err)
	}
}

func TestWantedPaths(t *testing.T) {
	for p, want := range map[string]bool{
		"resource_pack/blocks.json":                     true,
		"resource_pack/biomes_client.json":              true,
		"resource_pack/textures/terrain_texture.json":   true,
		"resource_pack/textures/blocks/a/b.png":         true,
		"resource_pack/textures/colormap/foliage.png":   true,
		"resource_pack/textures/blocks":                 false,
		"resource_pack/textures/blocks/../entity/a.png": false,
		"resource_pack/textures/item_texture.json":      false,
		"resource_pack/blocks.json/":                    false,
		"resource_pack//blocks.json":                    false,
		"./resource_pack/blocks.json":                   false,
		"behavior_pack/blocks.json":                     false,
	} {
		if got := wanted(p); got != want {
			t.Errorf("wanted(%q) = %v, want %v", p, got, want)
		}
	}
}
