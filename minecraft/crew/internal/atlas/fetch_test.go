package atlas

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func makeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakeGitHub struct {
	*httptest.Server
	requests atomic.Int32
	tarballs atomic.Int32
}

func newFakeGitHub(t *testing.T, tarball []byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		switch {
		case r.URL.Path == "/repos/Mojang/bedrock-samples/tags":
			if r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte("[]"))
				return
			}
			_, _ = w.Write([]byte(`[{"name":"v1.26.60.29-preview"},{"name":"v1.26.50.4"},{"name":"v1.26.40.05"}]`))
		case r.URL.Path == "/repos/Mojang/bedrock-samples/tarball/refs/tags/v1.26.50.4":
			f.tarballs.Add(1)
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

var packFiles = map[string]string{
	"Mojang-bedrock-samples-abc/resource_pack/blocks.json":               "{}",
	"Mojang-bedrock-samples-abc/resource_pack/textures/blocks/stone.png": "png",
	"Mojang-bedrock-samples-abc/behavior_pack/entities/zombie.json":      "{}",
	"Mojang-bedrock-samples-abc/README.md":                               "readme",
	"Mojang-bedrock-samples-abc/resource_pack/../../escape.txt":          "evil",
}

func TestFetchDownloadsOnlyResourcePackAndCaches(t *testing.T) {
	gh := newFakeGitHub(t, makeTarball(t, packFiles))
	cache := t.TempDir()
	f := Fetcher{Client: gh.Client(), BaseURL: gh.URL}

	pack, err := f.Fetch(context.Background(), "1.26.52.3", cache)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Tag != "v1.26.50.4" {
		t.Fatalf("tag = %q", pack.Tag)
	}
	if want := filepath.Join(cache, "v1.26.50.4", "resource_pack"); pack.Dir != want {
		t.Fatalf("dir = %q, want %q", pack.Dir, want)
	}
	if pack.Bytes == 0 {
		t.Fatal("expected a non-zero download size")
	}
	if _, err := os.Stat(filepath.Join(pack.Dir, "textures", "blocks", "stone.png")); err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{
		filepath.Join(cache, "v1.26.50.4", "README.md"),
		filepath.Join(cache, "v1.26.50.4", "behavior_pack"),
		filepath.Join(cache, "escape.txt"),
		filepath.Join(filepath.Dir(cache), "escape.txt"),
	} {
		if _, err := os.Stat(unwanted); err == nil {
			t.Fatalf("%s should not have been extracted", unwanted)
		}
	}

	// A second call must not touch the network at all.
	gh.Close()
	again, err := f.Fetch(context.Background(), "1.26.52.3", cache)
	if err != nil {
		t.Fatalf("cached fetch: %v", err)
	}
	if again.Tag != pack.Tag || again.Dir != pack.Dir || again.Bytes != 0 {
		t.Fatalf("cached result = %+v", again)
	}
	if gh.tarballs.Load() != 1 {
		t.Fatalf("tarball downloads = %d, want 1", gh.tarballs.Load())
	}
}

func TestFetchNoTagAtOrBelow(t *testing.T) {
	gh := newFakeGitHub(t, nil)
	f := Fetcher{Client: gh.Client(), BaseURL: gh.URL}
	_, err := f.Fetch(context.Background(), "1.0.0.1", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no bedrock-samples tag") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchLeavesNoPartialCacheOnFailure(t *testing.T) {
	gh := newFakeGitHub(t, []byte("not a gzip stream"))
	cache := t.TempDir()
	f := Fetcher{Client: gh.Client(), BaseURL: gh.URL}
	if _, err := f.Fetch(context.Background(), "1.26.50.4", cache); err == nil {
		t.Fatal("expected an error for a corrupt tarball")
	}
	if _, err := os.Stat(filepath.Join(cache, "v1.26.50.4")); err == nil {
		t.Fatal("failed download left a cache entry behind")
	}
}
