package icons

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
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

// Run against the published samples to see what a pin really gives: which
// names and pictures there are, what is missing, and what one fetch costs.
// It makes one listing request of the sixty an address is allowed an hour.
//
//	MCMAP_REAL_SOURCE=<tag or commit> go test -run RealSource -v ./minecraft/mcmap/internal/icons/
//
// MCMAP_REAL_SOURCE_OUT, if set, is a directory the pictures are written
// to, for looking at. It must not be inside the repository.
func TestRealSource(t *testing.T) {
	ref := os.Getenv("MCMAP_REAL_SOURCE")
	if ref == "" {
		t.Skip("MCMAP_REAL_SOURCE names no pin")
	}
	client := NewClient()
	count := &counting{inner: http.DefaultTransport}
	client.Transport = count
	source := &Source{Ref: ref, ListURL: DefaultListURL, RawURL: DefaultRawURL, HTTP: client}
	started := time.Now()
	set, err := source.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
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

	names := m.Names()
	t.Logf("%d mob icons, %d pictures, %d names from the language file, %d entity types defined", len(set.Mobs), len(set.Pictures), len(set.Lang), len(set.Entities))
	t.Logf("missing: %v", set.Missing)
	var unnamed, unpictured []string
	for _, kind := range set.Entities {
		if _, ok := set.Lang["entity."+kind+".name"]; !ok {
			unnamed = append(unnamed, kind+"="+names.Entity(kind))
		}
		if _, ok := set.Mobs[kind]; !ok {
			unpictured = append(unpictured, kind)
		}
	}
	t.Logf("entity types with a tidied name only (%d): %v", len(unnamed), unnamed)
	t.Logf("entity types with no icon (%d): %v", len(unpictured), unpictured)

	table := names.Table(nil)
	row := func(group, id, name, langKey, picture string) {
		_, real := set.Lang[langKey]
		_, drawn := set.Pictures[picture]
		t.Logf("  %-10s %-14s %-28q from the file: %-5v picture %-26s %v", group, id, name, real, picture, drawn)
	}
	for _, kind := range ContainerKinds {
		key := map[string]string{"chest": "tile.chest.name", "trapped_chest": "tile.trapped_chest.name", "barrel": "tile.barrel.name", "shulker": "tile.shulkerBox.name"}[kind]
		picture := "container/" + kind
		if kind == "shulker" {
			picture = "shulker/" + markers.Undyed
		}
		row("container", kind, table.Containers[kind], key, picture)
	}
	for _, colour := range markers.Colours {
		row("bed", colour, table.Beds[colour], "item.bed."+camel(legacyColour(colour), false)+".name", "bed/"+colour)
	}
	for _, colour := range markers.Colours {
		row("shulker", colour, table.Shulkers[colour], "tile.shulkerBox"+camel(legacyColour(colour), true)+".name", "shulker/"+colour)
	}
	for _, kind := range StructureKinds {
		feature := kind
		if other, ok := featureOf[kind]; ok {
			feature = other
		}
		row("structure", kind, table.Structures[kind], "feature."+feature, "structure/"+kind)
	}
	row("marker", "waypoint", "", "", "marker/waypoint")
	if strings.Join(slices.Sorted(slices.Values(keys(set.Pictures))), ",") != strings.Join(everyPicture(), ",") {
		t.Errorf("pictures = %v", slices.Sorted(slices.Values(keys(set.Pictures))))
	}

	if out := os.Getenv("MCMAP_REAL_SOURCE_OUT"); out != "" {
		for key, body := range set.Pictures {
			if err := os.WriteFile(filepath.Join(out, strings.ReplaceAll(key, "/", "_")+".png"), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
