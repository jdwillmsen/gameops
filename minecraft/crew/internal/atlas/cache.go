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
	if tag, ok := readIndex(cacheDir)[serverVersion]; ok {
		if a, err := LoadAtlas(filepath.Join(cacheDir, tag, atlasSubdir)); err == nil && a.table.TileSize == opts.tileSize() {
			return a, Pack{Tag: tag}, nil
		}
	}
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
