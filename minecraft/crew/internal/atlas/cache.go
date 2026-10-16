package atlas

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	atlasPNGFile   = "atlas.png"
	atlasTableFile = "table.json"
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
	// Table last: its presence marks the entry as complete.
	if err := writeAtomic(filepath.Join(dir, atlasPNGFile), a.png); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, atlasTableFile), table)
}

// LoadAtlas reads an atlas written by Save. A table from an older layout is
// rejected so the caller rebuilds it.
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
	return &Atlas{png: img, table: t}, nil
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

// Ensure returns the atlas for a server version, fetching the pack and
// building it only when cacheDir/<tag>/atlas is missing or stale. The result
// is keyed by release tag, so server versions that share a tag share a build.
func Ensure(ctx context.Context, f Fetcher, serverVersion, cacheDir string, opts Options) (*Atlas, Pack, error) {
	pack, err := f.Fetch(ctx, serverVersion, cacheDir)
	if err != nil {
		return nil, Pack{}, err
	}
	dir := filepath.Join(cacheDir, pack.Tag, atlasSubdir)
	if a, err := LoadAtlas(dir); err == nil && a.table.TileSize == opts.tileSize() {
		return a, pack, nil
	}
	a, err := BuildWithOptions(pack.Dir, opts)
	if err != nil {
		return nil, pack, err
	}
	if err := a.Save(dir); err != nil {
		return nil, pack, err
	}
	return a, pack, nil
}
