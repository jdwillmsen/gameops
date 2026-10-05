// Package generations retains complete copies of the mirrored world, so a
// world damaged between two snapshots can be rolled back to minutes ago
// rather than to the last nightly archive.
//
// On 2026-10-01 the mirror held a complete, consistent copy of the world
// taken 115 seconds before the node froze. The next snapshot, after the
// server had repaired its database by dropping 6,460 chunks, overwrote it:
// the two-minute rollback was gone and the backup used instead cost about
// 42 hours of play. So the mirror is no longer the only copy, and a
// snapshot the chunk census did not find whole never replaces one it did.
package generations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// currentLink names the complete generation. It is a symlink because
	// moving it is one rename: at no instant does it name a directory that
	// is not a whole world.
	currentLink = "current"
	// damagedDir holds the first snapshot the census found short of chunks,
	// apart from the generations, for whoever measures the loss.
	damagedDir = "damaged"
	// markerName is written last and makes a directory a generation.
	markerName = "generation.json"

	buildingDir   = ".building"
	retiredPrefix = ".retired-"
	switchingLink = ".switching"
)

// slots are the two generation directories. The one currentLink does not
// name is the one the next capture is built into, so the newest complete
// generation is never the one being overwritten.
var slots = [2]string{"a", "b"}

// Health is what the chunk census said about the snapshot being retained.
type Health int

const (
	// Whole means the census compared the snapshot with every chunk the
	// world has ever held and found nothing gone.
	Whole Health = iota
	// Damaged means it found chunks lost. Bedrock regenerates a lost chunk
	// as empty terrain the moment a player walks near, so this does not
	// clear itself and only an operator's acknowledgement ends it.
	Damaged
)

// Outcome is what a capture did with the snapshot.
type Outcome string

const (
	Promoted    Outcome = "promoted"
	Quarantined Outcome = "quarantined"
	Skipped     Outcome = "skipped"
)

// Marker is what a generation records about itself.
type Marker struct {
	TakenAt time.Time `json:"takenAt"`
	Files   int       `json:"files"`
	Bytes   int64     `json:"bytes"`
}

// Generation is one retained copy of the world.
type Generation struct {
	Marker
	// Name is the directory under the store's root, which is what a restore
	// copies the world out of.
	Name string
	Path string
}

// View is every copy the store holds.
type View struct {
	// Current is the newest snapshot proved whole, Previous the one before
	// it. Either may be absent: Current before the first capture, Previous
	// before the second.
	Current  *Generation
	Previous *Generation
	// Damaged is the quarantined snapshot, if the world has lost chunks.
	Damaged *Generation
}

// Store keeps the generations under one directory of the map's data volume.
type Store struct {
	root   string
	logger *slog.Logger

	// link is os.Link outside tests. A generation is hard links to the
	// mirror's files, never copies: LevelDB never rewrites a table once it
	// is closed, and the mirror only ever creates, renames over or unlinks
	// a file, so a link made now holds that file's content for as long as
	// the link lives. Copying would cost a second full world on a volume
	// sized for one, every cycle, for no extra safety.
	link func(src, dst string) error

	// stage creates the file a marker is written to before it is renamed
	// into place. It is os.CreateTemp outside tests.
	stage func(dir string) (staged, error)

	// pause, where a test sets it, is where the process is killed to prove
	// what an interrupted capture leaves on the volume. Nothing sets it in
	// production, where it costs one comparison per step.
	pause func(step string)

	mu   sync.RWMutex
	view View
}

// staged is a marker on its way to the volume.
type staged interface {
	io.WriteCloser
	Sync() error
	Name() string
}

func stageMarker(dir string) (staged, error) {
	return os.CreateTemp(dir, ".marker-*")
}

func (s *Store) at(step string) {
	if s.pause != nil {
		s.pause(step)
	}
}

// Open prepares the store at root, discarding whatever an interrupted
// capture left behind.
func Open(root string, logger *slog.Logger) (*Store, error) {
	s := &Store{root: root, logger: logger, link: os.Link, stage: stageMarker}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if err := s.sweep(); err != nil {
		return nil, err
	}
	s.reload()
	return s, nil
}

// Look is what the store holds right now.
func (s *Store) Look() View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.view
}

// Capture retains the world mirrored under src as it stood at the snapshot
// taken at `at`. A snapshot the census found whole becomes the current
// generation and the one it replaces becomes the previous; a damaged one is
// quarantined instead and displaces neither.
func (s *Store) Capture(ctx context.Context, src string, at time.Time, health Health) (Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	target := s.spare()
	outcome := Promoted
	if health == Damaged {
		// Only the first damaged snapshot is kept. Every later one holds the
		// same loss plus whatever the world did afterwards, and keeping them
		// would pin the volume full while nobody can promote anything.
		if s.view.Damaged != nil {
			metricCaptures.WithLabelValues(string(Skipped)).Inc()
			s.logger.Warn("world still short of chunks; the retained generations and the quarantined snapshot are unchanged",
				"quarantined_at", s.view.Damaged.TakenAt)
			return Skipped, nil
		}
		target, outcome = damagedDir, Quarantined
	}
	// Everything below either changes the volume or tries to, and some of
	// it can fail halfway, so what is held is reread from the volume rather
	// than inferred from which step returned the error.
	defer s.reload()

	if err := s.build(ctx, src, at); err != nil {
		// A build that ran out of room or was cancelled gives back whatever
		// it took, and the retained generations were never touched.
		s.discard(buildingDir)
		metricCaptures.WithLabelValues(failedLabel).Inc()
		return "", err
	}
	s.at("built")
	if err := s.install(target); err != nil {
		s.discard(buildingDir)
		metricCaptures.WithLabelValues(failedLabel).Inc()
		return "", err
	}
	s.at("installed")
	if outcome == Promoted {
		if err := s.promote(target); err != nil {
			// The switch is the last step, so a failure here leaves the
			// generation before this one current: older than it could be,
			// whole.
			metricCaptures.WithLabelValues(failedLabel).Inc()
			return "", err
		}
	}
	metricCaptures.WithLabelValues(string(outcome)).Inc()
	return outcome, nil
}

// spare is the slot the current generation is not in, which is the only one
// a capture may overwrite.
//
// When the link naming the current generation is gone but a generation is
// still held, that one is all there is to restore from, so it is the one to
// keep: choosing by Current alone would pick its slot and put the only whole
// copy through the swap.
func (s *Store) spare() string {
	keep := s.view.Current
	if keep == nil {
		keep = s.view.Previous
	}
	if keep != nil && keep.Name == slots[0] {
		return slots[1]
	}
	return slots[0]
}

// build fills the staging directory with links to every file under src and
// finishes it with its marker.
func (s *Store) build(ctx context.Context, src string, at time.Time) error {
	dir := filepath.Join(s.root, buildingDir)
	// A capture that failed where it could not clean up after itself still
	// leaves nothing in the way of the next one.
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	var m Marker
	m.TakenAt = at.UTC()
	var dirs, files []string
	world := false
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// The mirror stages an arriving snapshot in a dot-directory at its
		// own root, and that is half a world by definition. Only the root is
		// treated this way: anything inside a world directory is the world.
		if strings.HasPrefix(rel, ".") && !strings.ContainsRune(rel, filepath.Separator) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			dirs = append(dirs, dst)
			return os.Mkdir(dst, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := s.link(p, dst); err != nil {
			return err
		}
		world = world || strings.HasSuffix(filepath.ToSlash(rel), "/db/CURRENT")
		files = append(files, dst)
		m.Files++
		m.Bytes += info.Size()
		return nil
	})
	if err != nil {
		return err
	}
	// Every Bedrock world has a db/CURRENT. Retaining a mirror without one
	// would offer a restore point that no server can open.
	if !world {
		return errors.New("the mirror holds no world database")
	}
	return s.settle(dir, dirs, files, m)
}

// settle puts the staged generation on the volume before anything names it.
// The files are links, so this also flushes the mirror's own writes, which
// is the point: a generation whose contents are still only in the page
// cache would be lost by exactly the event it exists for.
func (s *Store) settle(dir string, dirs, files []string, m Marker) error {
	for _, p := range files {
		if err := syncPath(p); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	marker, err := s.stage(dir)
	if err != nil {
		return err
	}
	if _, err = marker.Write(raw); err == nil {
		err = marker.Sync()
	}
	if cerr := marker.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(marker.Name(), filepath.Join(dir, markerName)); err != nil {
		return err
	}
	// Deepest first, so no directory is durable before its contents are.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncPath(dirs[i]); err != nil {
			return err
		}
	}
	return syncPath(dir)
}

// install moves the staged generation into name. The directory it replaces
// is renamed aside first, so the move itself is one rename and a crash
// anywhere in here leaves the current generation whole.
func (s *Store) install(name string) error {
	dst := filepath.Join(s.root, name)
	retired := ""
	if _, err := os.Lstat(dst); err == nil {
		retired = filepath.Join(s.root, retiredPrefix+name)
		if err := os.RemoveAll(retired); err != nil {
			return err
		}
		if err := os.Rename(dst, retired); err != nil {
			return err
		}
		s.at("retired")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(filepath.Join(s.root, buildingDir), dst); err != nil {
		return err
	}
	if err := syncPath(s.root); err != nil {
		return err
	}
	if retired != "" {
		// The new generation is in place and durable by now, so a copy that
		// will not go away is leftover space, not a failed capture: failing
		// here would leave the new generation installed and never promoted.
		// The next capture into this slot, or the next start, removes it.
		if err := os.RemoveAll(retired); err != nil {
			s.logger.Error("could not remove the generation that was replaced", "entry", filepath.Base(retired), "error", err)
		}
	}
	return nil
}

// promote points the current link at name, which is the moment the new
// generation becomes the restore point.
func (s *Store) promote(name string) error {
	tmp := filepath.Join(s.root, switchingLink)
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(name, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.root, currentLink)); err != nil {
		return err
	}
	s.at("switched")
	return syncPath(s.root)
}

// sweep removes what an interrupted capture left: a part-built generation,
// a retired one whose removal did not finish, a half-made symlink. None of
// them is ever named by the current link, so none of them is a loss.
func (s *Store) sweep() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if name != buildingDir && name != switchingLink && !strings.HasPrefix(name, retiredPrefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.root, name)); err != nil {
			return err
		}
		s.logger.Warn("discarded what an interrupted capture left behind", "entry", name)
	}
	return nil
}

func (s *Store) discard(name string) {
	if err := os.RemoveAll(filepath.Join(s.root, name)); err != nil {
		s.logger.Error("could not remove a failed capture", "entry", name, "error", err)
	}
}

// reload rereads what is on the volume. The current link is the authority
// for which slot is the restore point; a marker is the authority for
// whether a directory is a whole generation at all.
func (s *Store) reload() {
	var v View
	current, err := os.Readlink(filepath.Join(s.root, currentLink))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Error("could not read which generation is current", "error", err)
	}
	for _, slot := range slots {
		g := s.read(slot)
		switch {
		case g == nil:
		case slot == current:
			v.Current = g
		default:
			v.Previous = g
		}
	}
	if v.Current == nil && v.Previous != nil {
		// A link naming a slot that holds nothing cannot be the restore
		// point, but the other slot is still a whole world.
		s.logger.Warn("the current generation is missing; the one before it is still held", "current", current)
	}
	v.Damaged = s.read(damagedDir)
	s.view = v

	count := 0
	for _, g := range []*Generation{v.Current, v.Previous} {
		if g != nil {
			count++
		}
	}
	metricRetained.Set(float64(count))
	metricDamaged.Set(boolGauge(v.Damaged != nil))
	for name, g := range map[string]*Generation{"current": v.Current, "previous": v.Previous, "damaged": v.Damaged} {
		bytes := 0.0
		if g != nil {
			bytes = float64(g.Bytes)
		}
		metricBytes.WithLabelValues(name).Set(bytes)
	}
	if v.Current != nil {
		metricCurrentAt.Set(float64(v.Current.TakenAt.Unix()))
	}
}

func (s *Store) read(name string) *Generation {
	path := filepath.Join(s.root, name)
	raw, err := os.ReadFile(filepath.Join(path, markerName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.logger.Error("could not read a generation's marker", "generation", name, "error", err)
		return nil
	}
	var m Marker
	if err := json.Unmarshal(raw, &m); err != nil || m.Files == 0 {
		s.logger.Error("a generation's marker does not describe a world", "generation", name, "error", err)
		return nil
	}
	return &Generation{Marker: m, Name: name, Path: path}
}

// syncPath flushes a file or directory. A directory is opened read-only,
// which is the only way it can be opened, and a file likewise: fsync does
// not need write access, and these are hard links to files the mirror owns.
func syncPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	return errors.Join(err, f.Close())
}

func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
