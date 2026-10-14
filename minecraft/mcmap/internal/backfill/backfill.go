// Package backfill gives the map's history the days from before it was
// kept, out of the world's own archives: each archive is unpacked, drawn
// as the live world is drawn, and kept as the day it was taken on.
//
// With each day go the markers read from the same archive, and a day held
// without them, as every day from before they were kept is, is given them
// from its archive and is otherwise not touched.
//
// It is a job run by hand, beside a map that is running, and may be run
// again: a day already held with its markers is left alone. The map reads
// the days it holds when it starts, so it shows what was added here after
// its next restart.
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
var archiveName = regexp.MustCompile(`^.*-(\d{8}T\d{6}Z)\.tar\.gz$`)

const (
	stampLayout = "20060102T150405Z"
	scratchDir  = ".mcmap-backfill"
	// How many directories down from an archive's root its level is looked
	// for: server/worlds/<level> is two, and one more is room to spare.
	levelDepth = 3
)

// Renderer draws a world and says where it put the tiles.
type Renderer interface {
	Prepare(ctx context.Context) error
	Render(ctx context.Context, world, dimension, out string) error
	history.Source
}

// Job is one run over a directory of archives.
type Job struct {
	// Archives holds the archives, each with the level's directory at its
	// root or a few directories down.
	Archives string
	// Work is where one archive at a time is unpacked and drawn, in a
	// directory of the job's own inside it. It needs room for a whole
	// world and its tiles, and is left as it was found.
	Work  string
	Level string
	// Store is the history the days are added to. The job sets where its
	// tiles are read from, so it must not be a store something else is
	// capturing into.
	Store    *history.Store
	Renderer Renderer
	// Markers, if set, reads the markers of the world whose LevelDB is at
	// db, taken at at, as the map would serve them: one answer a
	// dimension. work is a directory for it alone to work in. Without it
	// days are kept, and left, without markers.
	Markers func(ctx context.Context, db, work string, at time.Time) (map[string][]byte, error)
	Logger  *slog.Logger
}

// Result is what a run did with the archives it found.
type Result struct {
	Kept int
	// Amended is days already held that were given the markers they had
	// been kept without.
	Amended int
	// Held is archives of a day the history already had whole.
	Held   int
	Failed int
	// Unmarked is archives whose markers could not be read. Their days are
	// held without them, and a later run tries again.
	Unmarked int
}

type dated struct {
	name string
	at   time.Time
}

// Run keeps every archive whose day is not yet held, oldest first, since a
// day is counted against the day before it, and gives a day held without
// its markers those of its archive. An archive that cannot be unpacked or
// drawn costs its own day, and one whose markers cannot be read costs its
// day those; both are reported at the end, and the rest are still kept.
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

	// Each day held, and whether it lacks markers this job could read.
	held := map[string]bool{}
	for _, d := range j.Store.Days() {
		held[d.Day] = j.Markers != nil && slices.ContainsFunc(d.Dimensions, func(dim history.DayDimension) bool { return !dim.Markers })
	}
	for _, a := range found {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		day := a.at.Format(time.DateOnly)
		lacks, has := held[day]
		started := time.Now()
		switch {
		case !has:
			kept, err := j.keep(ctx, a)
			if err != nil {
				result.Failed++
				j.Logger.Error("archive not kept as a day of history", "archive", a.name, "day", day, "error", err)
				continue
			}
			held[day] = j.Markers != nil && kept.Markers < len(render.Dimensions)
			if held[day] {
				result.Unmarked++
			}
			result.Kept++
			j.Logger.Info("archive kept as a day of history", "archive", a.name, "day", day, "tiles", kept.Tiles, "changed", kept.Changed, "markers", kept.Markers, "new_bytes", kept.NewBytes, "seconds", time.Since(started).Seconds())
		case lacks:
			added, err := j.amend(ctx, a, day)
			if err != nil {
				// Still lacking, so a later archive of the day is tried.
				result.Unmarked++
				j.Logger.Error("archive's markers not added to its day of history", "archive", a.name, "day", day, "error", err)
				continue
			}
			held[day] = false
			result.Amended++
			j.Logger.Info("archive's markers added to its day of history", "archive", a.name, "day", day, "markers", added, "seconds", time.Since(started).Seconds())
		default:
			result.Held++
		}
	}
	var errs []error
	if result.Failed > 0 {
		errs = append(errs, fmt.Errorf("%d of %d archives could not be kept", result.Failed, len(found)))
	}
	if result.Unmarked > 0 {
		errs = append(errs, fmt.Errorf("the markers of %d of %d archives could not be read", result.Unmarked, len(found)))
	}
	return result, errors.Join(errs...)
}

// scratch is a directory of the job's own, and the only thing it ever
// deletes: Work given as a place that holds something else, the map's own
// data above all, which has a maps directory too, then loses nothing. It
// is cleared before an archive, of what a killed run left, and by the
// function returned after it, so nothing of one archive is left for the
// next, whose tiles must all be its own.
func (j Job) scratch() (dir string, clear func()) {
	dir = filepath.Join(j.Work, scratchDir)
	clear = func() { _ = os.RemoveAll(dir) }
	clear()
	return dir, clear
}

// open unpacks an archive under scratch and returns where its level is.
func (j Job) open(a dated, scratch string) (world string, err error) {
	unpacked := filepath.Join(scratch, "world")
	if err := unpack(filepath.Join(j.Archives, a.name), unpacked); err != nil {
		return "", fmt.Errorf("unpacking: %w", err)
	}
	return findLevel(unpacked, j.Level)
}

// findLevel is the level's directory in an unpacked archive: one of the
// level's name that holds a level.dat, at the root or up to levelDepth
// directories down, since archives are made by more than one thing and
// each puts the level where it keeps it. The shallowest is the level, and
// of several as shallow the first by path, so that the same archive is
// always read the same way. A directory of the name without a level.dat
// is something else's, and a link is never followed.
func findLevel(root, level string) (string, error) {
	dirs := []string{root}
	for depth := 0; depth <= levelDepth; depth++ {
		var found, below []string
		for _, dir := range dirs {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return "", err
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				path := filepath.Join(dir, e.Name())
				if info, err := os.Lstat(filepath.Join(path, "level.dat")); e.Name() == level && err == nil && info.Mode().IsRegular() {
					found = append(found, path)
				}
				below = append(below, path)
			}
		}
		if len(found) > 0 {
			return slices.Min(found), nil
		}
		dirs = below
	}
	return "", fmt.Errorf("the archive holds no %s with a level.dat, at its root or up to %d directories down", level, levelDepth)
}

// marks reads the markers of the world unpacked at world. The reader works
// on links to the world's files in a directory of its own, so the copy
// that is drawn afterwards is as it was unpacked.
func (j Job) marks(ctx context.Context, world, scratch string, at time.Time) (map[string][]byte, error) {
	return j.Markers(ctx, filepath.Join(world, "db"), filepath.Join(scratch, "markers"), at)
}

func (j Job) keep(ctx context.Context, a dated) (history.Result, error) {
	scratch, clear := j.scratch()
	defer clear()
	world, err := j.open(a, scratch)
	if err != nil {
		return history.Result{}, err
	}
	// The terrain is the day. Markers that cannot be read are added by a
	// later run, at the cost of unpacking and not of drawing again.
	var marks map[string][]byte
	if j.Markers != nil {
		if marks, err = j.marks(ctx, world, scratch, a.at); err != nil {
			j.Logger.Error("archive's markers not read; its day is kept without them", "archive", a.name, "error", err)
		}
	}
	maps := filepath.Join(scratch, "maps")
	for _, dimension := range render.Dimensions {
		if err := j.Renderer.Render(ctx, world, dimension, filepath.Join(maps, dimension)); err != nil {
			return history.Result{}, err
		}
	}
	j.Store.MapsDir = maps
	// Set for every archive, so that one whose markers were not read is
	// never kept with those of the archive before it.
	j.Store.Markers = func(dimension string) ([]byte, bool) {
		body, ok := marks[dimension]
		return body, ok
	}
	return j.Store.Capture(ctx, a.at)
}

// amend gives a day already held the markers of its archive, and nothing
// else: the archive is unpacked and read, and not drawn.
func (j Job) amend(ctx context.Context, a dated, day string) (added int, err error) {
	scratch, clear := j.scratch()
	defer clear()
	world, err := j.open(a, scratch)
	if err != nil {
		return 0, err
	}
	marks, err := j.marks(ctx, world, scratch, a.at)
	if err != nil {
		return 0, err
	}
	return j.Store.AddMarkers(day, marks)
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
