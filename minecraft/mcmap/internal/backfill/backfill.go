// Package backfill gives the map's history the days from before it was
// kept, out of the world's own archives: each archive is unpacked, drawn
// as the live world is drawn, and kept as the day it was taken on.
//
// It is a job run by hand, beside a map that is running, and may be run
// again: a day already held is left alone. The map reads the days it holds
// when it starts, so it shows the ones added here after its next restart.
package backfill

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// An archive is named for the moment it was taken, which is the only
// thing here that says which day it is: <anything>-20261001T040007Z.tar.gz.
// A leading dot is a copy still being written.
var archiveName = regexp.MustCompile(`^[^.].*-(\d{8}T\d{6}Z)\.tar\.gz$`)

const stampLayout = "20060102T150405Z"

// Renderer draws a world and says where it put the tiles.
type Renderer interface {
	Prepare(ctx context.Context) error
	Render(ctx context.Context, world, dimension, out string) error
	history.Source
}

// Job is one run over a directory of archives.
type Job struct {
	// Archives holds the archives, each the level's directory at its root.
	Archives string
	// Work is where one archive at a time is unpacked and drawn. It needs
	// room for a whole world and its tiles, and is left empty.
	Work  string
	Level string
	// Store is the history the days are added to. The job sets where its
	// tiles are read from, so it must not be a store something else is
	// capturing into.
	Store    *history.Store
	Renderer Renderer
	Logger   *slog.Logger
}

// Result is what a run did with the archives it found.
type Result struct {
	Kept int
	// Held is archives of a day the history already had.
	Held   int
	Failed int
}

type dated struct {
	name string
	at   time.Time
}

// Run keeps every archive whose day is not yet held, oldest first, since a
// day is counted against the day before it. An archive that cannot be
// unpacked or drawn costs its own day and is reported at the end; the rest
// are still kept.
func (j Job) Run(ctx context.Context) (Result, error) {
	var result Result
	// Before anything is unpacked: with no renderer no archive can be drawn.
	if err := j.Renderer.Prepare(ctx); err != nil {
		return result, fmt.Errorf("the renderer cannot run: %w", err)
	}
	entries, err := os.ReadDir(j.Archives)
	if err != nil {
		return result, err
	}
	var found []dated
	for _, e := range entries {
		m := archiveName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		at, err := time.Parse(stampLayout, m[1])
		if err != nil {
			continue
		}
		found = append(found, dated{e.Name(), at})
	}
	slices.SortFunc(found, func(a, b dated) int { return a.at.Compare(b.at) })

	held := map[string]bool{}
	for _, d := range j.Store.Days() {
		held[d.Day] = true
	}
	for _, a := range found {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		day := a.at.Format(time.DateOnly)
		if held[day] {
			result.Held++
			continue
		}
		started := time.Now()
		kept, err := j.keep(ctx, a)
		if err != nil {
			result.Failed++
			j.Logger.Error("archive not kept as a day of history", "archive", a.name, "day", day, "error", err)
			continue
		}
		held[day] = true
		result.Kept++
		j.Logger.Info("archive kept as a day of history", "archive", a.name, "day", day, "tiles", kept.Tiles, "changed", kept.Changed, "new_bytes", kept.NewBytes, "seconds", time.Since(started).Seconds())
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("%d of %d archives could not be kept", result.Failed, len(found))
	}
	return result, nil
}

func (j Job) keep(ctx context.Context, a dated) (history.Result, error) {
	worlds, maps := filepath.Join(j.Work, "world"), filepath.Join(j.Work, "maps")
	clear := func() {
		_ = os.RemoveAll(worlds)
		_ = os.RemoveAll(maps)
	}
	// What a killed run left; and nothing of this archive is left for the
	// next, whose tiles must all be its own.
	clear()
	defer clear()
	if err := unpack(filepath.Join(j.Archives, a.name), worlds); err != nil {
		return history.Result{}, fmt.Errorf("unpacking: %w", err)
	}
	world := filepath.Join(worlds, j.Level)
	if info, err := os.Stat(world); err != nil || !info.IsDir() {
		return history.Result{}, fmt.Errorf("the archive holds no %s", j.Level)
	}
	for _, dimension := range render.Dimensions {
		if err := j.Renderer.Render(ctx, world, dimension, filepath.Join(maps, dimension)); err != nil {
			return history.Result{}, err
		}
	}
	j.Store.MapsDir = maps
	return j.Store.Capture(ctx, a.at)
}

// unpack writes a .tar.gz's directories and files under dir, which it
// makes. Anything else in it, a link above all, is passed over, and a name
// that leads outside the archive refuses the whole of it.
func unpack(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(filepath.ToSlash(hdr.Name), "./")
		if name == "" || name == "." {
			continue
		}
		if !filepath.IsLocal(name) {
			return fmt.Errorf("the archive names a path outside itself: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			out, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(0o644))
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
}
