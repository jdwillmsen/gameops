package atlas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	atlasPNGFile   = "atlas.png"
	atlasTableFile = "table.json"
	itemsPNGFile   = "items.png"
	itemsTableFile = "items.json"
	entPNGFile     = "entities.png"
	entTableFile   = "entities.json"
	entFailedFile  = "entities.failed"
	atlasSubdir    = "atlas"
)

// Save writes the atlas to dir as atlas.png and table.json.
func (a *Atlas) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	table, err := json.Marshal(a.table)
	if err != nil {
		return err
	}
	items, err := json.Marshal(a.items)
	if err != nil {
		return err
	}
	// Table last: its presence marks the entry as complete.
	if err := writeAtomic(filepath.Join(dir, atlasPNGFile), a.png); err != nil {
		return err
	}
	// An atlas without items leaves the item files out, so the next start
	// sees an incomplete entry and tries the build again.
	if a.itemsErr == nil {
		if err := writeAtomic(filepath.Join(dir, itemsPNGFile), a.itemPNG); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, itemsTableFile), items); err != nil {
			return err
		}
	}
	if a.entitiesErr == nil {
		ents, err := json.Marshal(a.entities)
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, entPNGFile), a.entPNG); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, entTableFile), ents); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(dir, entFailedFile))
	} else {
		// Files from an earlier build must not outlive it, or the cache would
		// serve models its table no longer describes.
		for _, f := range []string{entPNGFile, entTableFile} {
			if err := os.Remove(filepath.Join(dir, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		// The failure is remembered so the next start does not download the
		// whole pack again to fail the same way.
		if err := writeAtomic(filepath.Join(dir, entFailedFile), []byte(a.entitiesErr.Error())); err != nil {
			return err
		}
	}
	return writeAtomic(filepath.Join(dir, atlasTableFile), table)
}

// LoadAtlas reads an atlas written by Save. A table from an older layout is
// rejected so the caller rebuilds it. An entry without the item sheet or the
// entity models loads with ItemsErr or EntitiesErr set: the caller rebuilds
// it, but can still fall back to it.
func LoadAtlas(dir string) (*Atlas, error) {
	table, err := os.ReadFile(filepath.Join(dir, atlasTableFile))
	if err != nil {
		return nil, err
	}
	img, err := os.ReadFile(filepath.Join(dir, atlasPNGFile))
	if err != nil {
		return nil, err
	}
	var t Table
	if err := json.Unmarshal(table, &t); err != nil {
		return nil, fmt.Errorf("atlas: decode table: %w", err)
	}
	if t.Version != tableVersion {
		return nil, fmt.Errorf("atlas: cached table version %d, want %d", t.Version, tableVersion)
	}
	a := &Atlas{png: img, table: t}
	a.itemPNG, a.items, a.itemsErr = loadItems(dir)
	a.entPNG, a.entities, a.entitiesErr = loadEntities(dir)
	// A table from before armour still draws every model, so it is served
	// while the current one is rebuilt.
	a.entitiesOld = a.entitiesErr == nil && a.entities.Version < entityTableVersion
	if a.entitiesErr != nil {
		_, err := os.Stat(filepath.Join(dir, entFailedFile))
		a.entitiesFailed = err == nil
	}
	return a, nil
}

func loadEntities(dir string) ([]byte, EntityTable, error) {
	img, err := os.ReadFile(filepath.Join(dir, entPNGFile))
	if err != nil {
		return nil, EntityTable{}, fmt.Errorf("atlas: no cached entity sheet: %w", err)
	}
	doc, err := os.ReadFile(filepath.Join(dir, entTableFile))
	if err != nil {
		return nil, EntityTable{}, fmt.Errorf("atlas: no cached entity sheet: %w", err)
	}
	var t EntityTable
	if err := json.Unmarshal(doc, &t); err != nil {
		return nil, EntityTable{}, fmt.Errorf("atlas: decode entities: %w", err)
	}
	if t.Version < 1 || t.Version > entityTableVersion {
		return nil, EntityTable{}, fmt.Errorf("atlas: cached entity table version %d, want %d", t.Version, entityTableVersion)
	}
	return img, t, nil
}

func loadItems(dir string) ([]byte, ItemTable, error) {
	img, err := os.ReadFile(filepath.Join(dir, itemsPNGFile))
	if err != nil {
		return nil, ItemTable{}, fmt.Errorf("atlas: no cached item sheet: %w", err)
	}
	doc, err := os.ReadFile(filepath.Join(dir, itemsTableFile))
	if err != nil {
		return nil, ItemTable{}, fmt.Errorf("atlas: no cached item sheet: %w", err)
	}
	var items ItemTable
	if err := json.Unmarshal(doc, &items); err != nil {
		return nil, ItemTable{}, fmt.Errorf("atlas: decode items: %w", err)
	}
	return img, items, nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	return os.Rename(tmp.Name(), path)
}

// Ensure returns the atlas for a server version. It serves the cached atlas
// when the index already maps the version to a tag whose atlas is current;
// otherwise it fetches the pack and builds, overwriting that tag's atlas. A
// new server version therefore rebuilds once even if its tag was built before.
//
// Only the built atlas and the version index are kept in cacheDir. The pack's
// few thousand small files go to a scratch directory under os.TempDir and are
// removed after the build: on network storage each file costs a round trip,
// which made a cold build take minutes for a download of seconds. The
// returned Pack describes the download, so its Dir is always empty.
func Ensure(ctx context.Context, f Fetcher, serverVersion, cacheDir string, opts Options) (*Atlas, Pack, error) {
	// A cached atlas that lacks only its item sheet or entity models is
	// rebuilt, but kept to fall back on: losing the fetch should not also
	// lose the terrain.
	var fallback *Atlas
	var fallbackTag string
	if tag, ok := readIndex(cacheDir)[serverVersion]; ok {
		if a, err := LoadAtlas(filepath.Join(cacheDir, tag, atlasSubdir)); err == nil && a.table.TileSize == opts.tileSize() {
			if a.Complete() {
				return a, Pack{Tag: tag}, nil
			}
			fallback, fallbackTag = a, tag
		}
	}
	a, pack, err := build(ctx, f, serverVersion, cacheDir, opts)
	if err != nil && fallback != nil {
		if opts.OnFallback != nil {
			opts.OnFallback(err)
		}
		return fallback, Pack{Tag: fallbackTag}, nil
	}
	return a, pack, err
}

func build(ctx context.Context, f Fetcher, serverVersion, cacheDir string, opts Options) (*Atlas, Pack, error) {
	scratch, err := os.MkdirTemp("", "crew-atlas-")
	if err != nil {
		return nil, Pack{}, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	pack, err := f.Fetch(ctx, serverVersion, scratch)
	if err != nil {
		return nil, Pack{}, err
	}
	packDir := pack.Dir
	pack.Dir = ""
	a, err := BuildWithOptions(packDir, opts)
	if err != nil {
		return nil, pack, err
	}
	if err := a.Save(filepath.Join(cacheDir, pack.Tag, atlasSubdir)); err != nil {
		return nil, pack, err
	}
	if err := writeIndex(cacheDir, serverVersion, pack.Tag); err != nil {
		return nil, pack, err
	}
	return a, pack, nil
}
