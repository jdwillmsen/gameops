// Package mirror keeps a local copy of the running server's world, fetched
// from the console bridge's /snapshot endpoint.
package mirror

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ErrBusy means the bridge declined this once: another snapshot is running
// or saving is paused by something else. The mirror is unchanged and the
// next cycle should simply try again.
var ErrBusy = errors.New("the bridge refused a snapshot right now")

const (
	manifestName = "snapshot.json"
	completeName = "snapshot.ok"
	// stagingDir holds a snapshot until it is known to be complete. It sits
	// inside Root so moving files into place is a rename, never a copy.
	stagingDir = ".incoming"
)

type Mirror struct {
	// Root is the directory the world directories live under.
	Root   string
	URL    string
	Token  string
	HTTP   *http.Client
	Logger *slog.Logger
}

type Stats struct {
	Files    int
	Fetched  int
	Removed  int
	Bytes    int64
	Duration time.Duration
}

type file struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Sync brings the mirror to the server's latest saved state. A snapshot that
// cannot be completed changes nothing. Once one is complete its files are
// moved in one by one, so a crash in the middle of that leaves a mix; the
// next Sync repairs it, and nothing reads the mirror until a Sync succeeds.
//
// A file here is only ever created, replaced by rename, or unlinked, never
// written in place. The retained generations hold hard links to these
// files, so rewriting one would silently change every copy of the world
// that was taken before it.
func (m *Mirror) Sync(ctx context.Context) (Stats, error) {
	started := time.Now()
	staging := filepath.Join(m.Root, stagingDir)
	if err := os.RemoveAll(staging); err != nil {
		return Stats{}, err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return Stats{}, err
	}
	defer os.RemoveAll(staging)

	have, err := m.tables()
	if err != nil {
		return Stats{}, err
	}
	files, staged, bytesIn, err := m.fetch(ctx, have, staging)
	if err != nil {
		return Stats{}, err
	}

	// Everything is checked before anything moves, so a snapshot that cannot
	// be completed never leaves the mirror between two points in time.
	want := make(map[string]int64, len(files))
	for _, f := range files {
		want[f.Name] = f.Size
		if _, ok := staged[f.Name]; ok {
			continue
		}
		info, err := os.Stat(m.local(f.Name))
		if err != nil || info.Size() != f.Size {
			return Stats{}, fmt.Errorf("snapshot skipped %s, which the mirror does not hold at %d bytes", f.Name, f.Size)
		}
	}

	for name := range staged {
		dst := m.local(name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Stats{}, err
		}
		if err := os.Rename(filepath.Join(staging, filepath.FromSlash(name)), dst); err != nil {
			return Stats{}, err
		}
	}
	removed, err := m.prune(want)
	if err != nil {
		return Stats{}, err
	}
	return Stats{Files: len(files), Fetched: len(staged), Removed: removed, Bytes: bytesIn, Duration: time.Since(started)}, nil
}

func (m *Mirror) local(name string) string {
	return filepath.Join(m.Root, filepath.FromSlash(name))
}

// tables lists the table files the mirror holds. Only those are worth
// offering: the bridge re-sends every other file regardless.
func (m *Mirror) tables() (map[string]int64, error) {
	have := map[string]int64{}
	err := m.walk(func(name string, info fs.FileInfo) error {
		if strings.HasSuffix(name, ".ldb") {
			have[name] = info.Size()
		}
		return nil
	})
	return have, err
}

func (m *Mirror) walk(fn func(name string, info fs.FileInfo) error) error {
	return filepath.WalkDir(m.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == stagingDir && filepath.Dir(p) == m.Root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(m.Root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), info)
	})
}

func (m *Mirror) fetch(ctx context.Context, have map[string]int64, staging string) (files []file, staged map[string]struct{}, n int64, err error) {
	body, err := json.Marshal(map[string]any{"have": have})
	if err != nil {
		return nil, nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.URL, "/")+"/snapshot", bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return nil, nil, 0, ErrBusy
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, nil, 0, fmt.Errorf("bridge /snapshot: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	tr := tar.NewReader(resp.Body)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != manifestName {
		return nil, nil, 0, fmt.Errorf("snapshot does not start with %s", manifestName)
	}
	var manifest struct {
		Files []file `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(tr, 16<<20)).Decode(&manifest); err != nil {
		return nil, nil, 0, fmt.Errorf("snapshot manifest: %w", err)
	}
	sizes := make(map[string]int64, len(manifest.Files))
	hasDatabase := false
	for _, f := range manifest.Files {
		if err := checkName(f.Name); err != nil {
			return nil, nil, 0, err
		}
		sizes[f.Name] = f.Size
		hasDatabase = hasDatabase || strings.HasSuffix(f.Name, "/db/CURRENT")
	}
	// Every LevelDB has a CURRENT file. A manifest without one is not a
	// world, and applying it would prune the whole mirror.
	if !hasDatabase {
		return nil, nil, 0, errors.New("snapshot manifest lists no world database")
	}

	// Writes go through a root handle so that no entry name, whatever it
	// contains, can reach a path outside the staging directory.
	root, err := os.OpenRoot(staging)
	if err != nil {
		return nil, nil, 0, err
	}
	defer root.Close()

	staged = map[string]struct{}{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			// Including a clean end of stream: the bridge ends every good
			// snapshot with the completion marker, handled below.
			return nil, nil, 0, fmt.Errorf("snapshot ended without %s: %w", completeName, err)
		}
		if hdr.Name == completeName {
			return manifest.Files, staged, n, nil
		}
		size, listed := sizes[hdr.Name]
		if !listed || hdr.Size != size {
			return nil, nil, 0, fmt.Errorf("snapshot sent %s (%d bytes), which its manifest does not list at that size", hdr.Name, hdr.Size)
		}
		rel := filepath.FromSlash(hdr.Name)
		if !filepath.IsLocal(rel) {
			return nil, nil, 0, fmt.Errorf("snapshot names a path outside a world directory: %q", hdr.Name)
		}
		if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			return nil, nil, 0, err
		}
		out, err := root.Create(rel)
		if err != nil {
			return nil, nil, 0, err
		}
		written, err := io.Copy(out, tr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, nil, 0, fmt.Errorf("receiving %s: %w", hdr.Name, err)
		}
		staged[hdr.Name] = struct{}{}
		n += written
	}
}

// checkName accepts only a clean relative path inside a world directory.
// The bridge applies the same rule, but these names become file paths here,
// so this end does not rely on it.
func checkName(name string) error {
	if name == "" || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || path.Clean(name) != name ||
		name == ".." || strings.HasPrefix(name, "../") || !strings.Contains(name, "/") ||
		strings.HasPrefix(name, stagingDir+"/") {
		return fmt.Errorf("snapshot names a path outside a world directory: %q", name)
	}
	return nil
}

// prune removes whatever the server no longer has: tables merged away by
// compaction, superseded manifests, rotated logs.
func (m *Mirror) prune(want map[string]int64) (int, error) {
	var stale []string
	err := m.walk(func(name string, _ fs.FileInfo) error {
		if _, ok := want[name]; !ok {
			stale = append(stale, name)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, name := range stale {
		if err := os.Remove(m.local(name)); err != nil {
			return 0, err
		}
	}
	return len(stale), nil
}
