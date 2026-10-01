// Package render turns a world directory into map tiles.
package render

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Renderer is what the rest of the service needs from a map renderer, so the
// one in use can be replaced without touching its callers.
type Renderer interface {
	// Prepare makes sure rendering is possible at all. It is called before
	// the live world is touched, so a renderer that cannot run never costs
	// the server a pause.
	Prepare(ctx context.Context) error
	// Render brings the tiles under out up to date with one dimension of
	// world. out is reused between calls so unchanged areas are skipped.
	Render(ctx context.Context, world, dimension, out string) error
	// Info describes what a completed Render left in out.
	Info(out string) (Info, error)
	// RenderedAt is when out last began a render, if it ever has.
	RenderedAt(out string) (time.Time, bool)
	// TilePath is where the tile at (zoom, x, y) lives under out, whether or
	// not it exists.
	TilePath(out, format string, zoom, x, y int) string
}

// Info is the extent of a rendered dimension. A region is 512 blocks; zoom 0
// is one block per pixel and each step below it halves the scale.
type Info struct {
	MinZoom    int    `json:"minZoom"`
	MaxZoom    int    `json:"maxZoom"`
	MinRegionX int    `json:"minRegionX"`
	MinRegionZ int    `json:"minRegionZ"`
	MaxRegionX int    `json:"maxRegionX"`
	MaxRegionZ int    `json:"maxRegionZ"`
	Format     string `json:"format"`
}

// Dimensions are the ones a Bedrock world has, in display order.
var Dimensions = []string{"overworld", "nether", "end"}

const (
	binaryName     = "unmined-cli"
	propertiesFile = "unmined.map.properties.js"
	// A blank or failed tile is a few dozen bytes at most; a tile with any
	// terrain on it is thousands.
	minTileBytes = 256
)

// Unmined renders with the uNmINeD command line tool. Its licence allows
// free use but not redistribution, so it is never part of the image: it is
// downloaded on first use, checked against a pinned digest, and kept in Dir.
type Unmined struct {
	Dir    string
	URL    string
	SHA256 string
	HTTP   *http.Client
	Logger *slog.Logger

	ChunkProcessors int
	NetherTopY      int

	// run executes the binary; tests replace it.
	run func(ctx context.Context, bin string, args, env []string) ([]byte, error)

	mu sync.Mutex
	// refused remembers a download that failed its digest check, so the same
	// archive is not fetched again on every cycle only to be refused.
	refused      error
	refusedUntil time.Time
}

// refusalHold is how long a refused download is remembered. The pin only
// moves with a new release of this service, so there is nothing to gain from
// asking again sooner.
const refusalHold = 6 * time.Hour

func (u *Unmined) Prepare(ctx context.Context) error {
	_, err := u.Ensure(ctx)
	return err
}

// Ensure returns the path of the installed binary, downloading it first if
// this digest has never been installed here.
func (u *Unmined) Ensure(ctx context.Context) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	home := filepath.Join(u.Dir, u.SHA256)
	bin := filepath.Join(home, binaryName)
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	if u.refused != nil && time.Now().Before(u.refusedUntil) {
		return "", u.refused
	}
	if err := os.MkdirAll(u.Dir, 0o755); err != nil {
		return "", err
	}
	// Whatever a killed install left half-done; nothing else writes here.
	for _, pattern := range []string{"download-*", "install-*"} {
		leftovers, _ := filepath.Glob(filepath.Join(u.Dir, pattern))
		for _, p := range leftovers {
			_ = os.RemoveAll(p)
		}
	}

	archive, err := os.CreateTemp(u.Dir, "download-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading the renderer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading the renderer: %s", resp.Status)
	}
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(archive, sum), resp.Body); err != nil {
		return "", fmt.Errorf("downloading the renderer: %w", err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != u.SHA256 {
		u.refused = fmt.Errorf("the renderer download has sha256 %s, not the pinned %s; a new build was probably published, and the pin moves only after that build is reviewed", got, u.SHA256)
		u.refusedUntil = time.Now().Add(refusalHold)
		return "", u.refused
	}

	staging, err := os.MkdirTemp(u.Dir, "install-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if err := extract(archive, staging); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(staging, binaryName)); err != nil {
		return "", fmt.Errorf("the renderer archive has no %s in it", binaryName)
	}
	if err := os.Rename(staging, home); err != nil {
		return "", err
	}
	u.Logger.Info("renderer installed", "sha256", u.SHA256, "path", bin)
	return bin, nil
}

// extract unpacks a .tar.gz into dir without its single top-level directory.
func extract(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
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
		_, rel, ok := strings.Cut(hdr.Name, "/")
		if !ok || rel == "" {
			continue
		}
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("the renderer archive names a path outside itself: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
				return err
			}
			f, err := root.OpenFile(rel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(hdr.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
}

func (u *Unmined) Render(ctx context.Context, world, dimension, out string) error {
	args := []string{
		"web", "render",
		"--world=" + world,
		"--dimension=" + dimension,
		"--output=" + out,
		// The default, JPEG, crashes this build after writing blank tiles
		// and still exits 0.
		"--imageformat=webp",
		"--chunkprocessors=" + strconv.Itoa(max(u.ChunkProcessors, 1)),
		// One unreadable chunk should cost that chunk, not the whole map.
		"-c",
	}
	switch dimension {
	case "overworld", "end":
	case "nether":
		args = append(args, "--topY="+strconv.Itoa(u.NetherTopY))
	default:
		return fmt.Errorf("unknown dimension %q", dimension)
	}

	bin, err := u.Ensure(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	env := []string{
		// The runtime images carry no ICU libraries; without this the
		// binary aborts at startup.
		"DOTNET_SYSTEM_GLOBALIZATION_INVARIANT=1",
		"HOME=" + os.TempDir(),
		"DOTNET_BUNDLE_EXTRACT_BASE_DIR=" + os.TempDir(),
		"TMPDIR=" + os.TempDir(),
	}
	run := u.run
	if run == nil {
		run = runBinary
	}
	started := time.Now()
	output, err := run(ctx, bin, args, env)
	if err != nil {
		return fmt.Errorf("rendering %s: %w: %s", dimension, err, tail(output, 30))
	}
	return verify(out, started)
}

func runBinary(ctx context.Context, bin string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = filepath.Dir(bin)
	// A first render logs a line per region for minutes; only the end of it
	// is ever reported.
	output := &tailBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	return []byte(output.String()), err
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

func tail(output []byte, lines int) string {
	all := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// clockSlack allows for file timestamps coarser than the clock.
const clockSlack = 2 * time.Second

// verify checks that out holds a map this render produced, and not the
// leftovers of one that claimed success. The renderer rewrites its
// properties file at the start of every run, so that file being older than
// the run means the run did nothing; and a real map has at least one
// full-scale tile with something drawn on it.
func verify(out string, started time.Time) error {
	props, err := os.Stat(filepath.Join(out, propertiesFile))
	if err != nil {
		return fmt.Errorf("the render left no %s", propertiesFile)
	}
	if props.ModTime().Before(started.Add(-clockSlack)) {
		return fmt.Errorf("the render did not rewrite %s; what is on disk is from an earlier run", propertiesFile)
	}
	found := errors.New("found")
	err = filepath.WalkDir(filepath.Join(out, "tiles", "zoom.0"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasPrefix(d.Name(), "tile.") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() >= minTileBytes {
			return found
		}
		return nil
	})
	if errors.Is(err, found) {
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return errors.New("the render produced no full-scale tile with anything on it")
}

// RenderedAt reports when out last finished rendering, from what the
// renderer left on disk, so the time survives a restart of this service.
func (u *Unmined) RenderedAt(out string) (time.Time, bool) {
	info, err := os.Stat(filepath.Join(out, propertiesFile))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

var property = regexp.MustCompile(`(?m)^\s*(\w+):\s*(?:(-?\d+)|"([^"]*)")\s*,?\s*$`)

func (u *Unmined) Info(out string) (Info, error) {
	raw, err := os.ReadFile(filepath.Join(out, propertiesFile))
	if err != nil {
		return Info{}, err
	}
	var info Info
	ints := map[string]*int{
		"minZoom": &info.MinZoom, "maxZoom": &info.MaxZoom,
		"minRegionX": &info.MinRegionX, "minRegionZ": &info.MinRegionZ,
		"maxRegionX": &info.MaxRegionX, "maxRegionZ": &info.MaxRegionZ,
	}
	seen := 0
	for _, m := range property.FindAllStringSubmatch(string(raw), -1) {
		if dst, ok := ints[m[1]]; ok && m[2] != "" {
			*dst, _ = strconv.Atoi(m[2])
			seen++
		}
		if m[1] == "imageFormat" {
			info.Format = m[3]
		}
	}
	if seen != len(ints) || info.Format == "" {
		return Info{}, fmt.Errorf("%s does not describe a map", propertiesFile)
	}
	return info, nil
}

func (u *Unmined) TilePath(out, format string, zoom, x, y int) string {
	return filepath.Join(out, "tiles",
		"zoom."+strconv.Itoa(zoom),
		strconv.Itoa(floorDiv(x, 10)), strconv.Itoa(floorDiv(y, 10)),
		fmt.Sprintf("tile.%d.%d.%s", x, y, format))
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
