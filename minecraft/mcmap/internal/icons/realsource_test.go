package icons

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type counting struct {
	inner           http.RoundTripper
	requests, bytes atomic.Int64
	listings        atomic.Int64
}

type countedBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (b countedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.Add(int64(n))
	return n, err
}

func (c *counting) RoundTrip(r *http.Request) (*http.Response, error) {
	c.requests.Add(1)
	if r.URL.Host == "api.github.com" {
		c.listings.Add(1)
	}
	resp, err := c.inner.RoundTrip(r)
	if err == nil {
		resp.Body = countedBody{resp.Body, &c.bytes}
	}
	return resp, err
}

// Run against the published samples to see what a pin really gives and
// what one fetch costs. It prints counts and nothing of the samples'
// own, and makes one listing request of the sixty an address is allowed
// an hour.
//
//	MCMAP_REAL_SOURCE=<tag or commit> go test -run RealSource -v ./minecraft/mcmap/internal/icons/
//
// MCMAP_REAL_SOURCE_OUT, if set, is a directory the pictures are written
// to, for looking at, with why each one not made was not. It must not be
// inside the repository. MCMAP_REAL_SOURCE_LIST and MCMAP_REAL_SOURCE_RAW
// point it at a copy of the samples kept somewhere else.
func TestRealSource(t *testing.T) {
	ref := os.Getenv("MCMAP_REAL_SOURCE")
	if ref == "" {
		t.Skip("MCMAP_REAL_SOURCE names no pin")
	}
	client := NewClient()
	count := &counting{inner: http.DefaultTransport}
	client.Transport = count
	source := &Source{Ref: ref, ListURL: cmp.Or(os.Getenv("MCMAP_REAL_SOURCE_LIST"), DefaultListURL), RawURL: cmp.Or(os.Getenv("MCMAP_REAL_SOURCE_RAW"), DefaultRawURL), HTTP: client}
	// The most the heap holds at any moment of the fetch, sampled.
	var peak atomic.Uint64
	stop := make(chan struct{})
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	go func() {
		for {
			var now runtime.MemStats
			runtime.ReadMemStats(&now)
			if now.HeapInuse > peak.Load() {
				peak.Store(now.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	started := time.Now()
	set, err := source.Fetch(t.Context())
	close(stop)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("heap in use: %d KB before, %d KB at most during the fetch", before.HeapInuse>>10, peak.Load()>>10)
	t.Logf("fetched in %s: %d requests (%d to the listing API), %d bytes", time.Since(started).Round(time.Millisecond), count.requests.Load(), count.listings.Load(), count.bytes.Load())

	dir := t.TempDir()
	m := &Mobs{Dir: dir, Ref: ref, Logger: quiet(), Fetch: func(context.Context) (Set, error) { return set, nil }}
	m.Run(t.Context())
	var files, size int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, _ error) error {
		if info, err := d.Info(); err == nil && !d.IsDir() {
			files++
			size += info.Size()
		}
		return nil
	})
	t.Logf("on the volume: %d files, %d bytes", files, size)

	groups := map[string]int{}
	for key := range set.Pictures {
		group, _, _ := strings.Cut(key, "/")
		groups[group]++
	}
	t.Logf("%d mob icons, %d names from the language file, %d entity types defined, %d recipes", len(set.Mobs), len(set.Lang), len(set.Entities), len(set.Recipes))
	t.Logf("pictures by group: %v", groups)
	t.Logf("missing: %v", set.Missing)
	var faceless []string
	for key := range set.Rejected {
		if kind, ok := strings.CutPrefix(key, "face/"); ok {
			faceless = append(faceless, kind)
		}
	}
	slices.Sort(faceless)
	t.Logf("not made (%d), of which mobs left as their spawn egg (%d): %v", len(set.Rejected), len(faceless), faceless)
	if len(set.Missing) > 0 {
		t.Errorf("the pin left %d things out", len(set.Missing))
	}
	for _, key := range everyPicture() {
		if _, ok := set.Pictures[key]; !ok {
			t.Errorf("no picture for %s", key)
		}
	}

	if out := os.Getenv("MCMAP_REAL_SOURCE_OUT"); out != "" {
		for key, body := range set.Pictures {
			if err := os.WriteFile(filepath.Join(out, strings.ReplaceAll(key, "/", "__")+".png"), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for kind, body := range set.Mobs {
			if err := os.WriteFile(filepath.Join(out, "egg__"+kind+".png"), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		why, _ := json.MarshalIndent(set.Rejected, "", " ")
		if err := os.WriteFile(filepath.Join(out, "rejected.json"), why, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
