package generations

import (
	"cmp"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

var noon = time.Date(2026, 10, 1, 23, 43, 38, 0, time.UTC)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// mirror writes a world that looks like the one the map holds: a level
// directory with a LevelDB under it. Each table's content is its name plus
// the generation marker, so a test can tell one snapshot's files from
// another's.
func mirror(t *testing.T, dir, mark string, tables ...string) {
	t.Helper()
	db := filepath.Join(dir, "FWB", "db")
	if err := os.MkdirAll(db, 0o755); err != nil {
		t.Fatal(err)
	}
	// Through a temporary name, exactly as the mirror applies a snapshot.
	// Rewriting a file in place would change it inside every generation
	// holding a link to it, so the mirror never does and nor does this.
	write := func(p, body string) {
		t.Helper()
		tmp := p + ".writing"
		if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, p); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(db, "CURRENT"), "MANIFEST-000001\n")
	write(filepath.Join(db, "MANIFEST-000001"), "manifest "+mark)
	write(filepath.Join(dir, "FWB", "level.dat"), "level "+mark)
	write(filepath.Join(dir, "FWB", "levelname.txt"), "FWB")
	for _, name := range tables {
		write(filepath.Join(db, name), name+" "+mark)
	}
}

// contents is every file in a tree with its body, which is what makes two
// copies of a world comparable.
func contents(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func capture(t *testing.T, s *Store, src string, at time.Time, h Health) Outcome {
	t.Helper()
	got, err := s.Capture(context.Background(), src, at, h)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	return got
}

func open(t *testing.T, root string) *Store {
	t.Helper()
	s, err := Open(root, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Nothing is held before the first snapshot, and the first one has no
// generation to keep behind it.
func TestCapture_FirstSnapshotBecomesTheOnlyGeneration(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	mirror(t, src, "one", "000005.ldb")
	s := open(t, root)

	if v := s.Look(); v.Current != nil || v.Previous != nil || v.Damaged != nil {
		t.Fatalf("a fresh store holds %+v", v)
	}
	if got := capture(t, s, src, noon, Whole); got != Promoted {
		t.Fatalf("outcome = %q", got)
	}

	v := s.Look()
	if v.Current == nil || v.Previous != nil {
		t.Fatalf("after one snapshot: %+v", v)
	}
	if !v.Current.TakenAt.Equal(noon) || v.Current.Files != 5 {
		t.Errorf("current = %+v", v.Current)
	}
	want := contents(t, src)
	got := contents(t, v.Current.Path)
	delete(got, markerName)
	if !equal(want, got) {
		t.Errorf("the generation holds %v, the mirror %v", keys(got), keys(want))
	}
}

// The second snapshot becomes the restore point and the first is kept
// behind it; the third pushes the first out, never the one it is replacing.
func TestCapture_KeepsTheSnapshotBeforeTheCurrentOne(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	s := open(t, root)

	for i, mark := range []string{"one", "two", "three"} {
		mirror(t, src, mark, "000005.ldb")
		capture(t, s, src, noon.Add(time.Duration(i)*15*time.Minute), Whole)
	}

	v := s.Look()
	if v.Current == nil || v.Previous == nil {
		t.Fatalf("after three snapshots: %+v", v)
	}
	if !v.Current.TakenAt.Equal(noon.Add(30*time.Minute)) || !v.Previous.TakenAt.Equal(noon.Add(15*time.Minute)) {
		t.Fatalf("current %s, previous %s", v.Current.TakenAt, v.Previous.TakenAt)
	}
	if v.Current.Name == v.Previous.Name {
		t.Fatalf("both generations are %q", v.Current.Name)
	}
	if body := contents(t, v.Current.Path)["FWB/level.dat"]; body != "level three" {
		t.Errorf("current holds %q", body)
	}
	if body := contents(t, v.Previous.Path)["FWB/level.dat"]; body != "level two" {
		t.Errorf("previous holds %q", body)
	}
}

// The reason the feature exists: the mirror is overwritten by the damaged
// world, and the generation taken before it still holds what was lost.
func TestCapture_DamagingTheMirrorLeavesTheGenerationIntact(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	tables := []string{"000005.ldb", "000006.ldb", "000007.ldb"}
	mirror(t, src, "before", tables...)
	s := open(t, root)
	capture(t, s, src, noon, Whole)
	before := contents(t, src)

	// What the server's own repair does: drop the tables it can no longer
	// find, then carry on writing.
	for _, name := range tables[:2] {
		if err := os.Remove(filepath.Join(src, "FWB", "db", name)); err != nil {
			t.Fatal(err)
		}
	}
	mirror(t, src, "after")

	v := s.Look()
	got := contents(t, v.Current.Path)
	delete(got, markerName)
	if !equal(before, got) {
		t.Fatalf("the generation changed with the mirror: %v", keys(got))
	}
	// And the damaged mirror really is short, so the comparison means
	// something.
	if _, ok := contents(t, src)["FWB/db/000005.ldb"]; ok {
		t.Fatal("the mirror was not damaged")
	}
}

// A snapshot the census found short of chunks is held apart. Promoting it
// would overwrite the last whole world with the damaged one, which is the
// loss this exists to prevent.
func TestCapture_ADamagedSnapshotDoesNotDisplaceTheGenerations(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	s := open(t, root)
	mirror(t, src, "one", "000005.ldb")
	capture(t, s, src, noon, Whole)
	mirror(t, src, "two", "000005.ldb")
	capture(t, s, src, noon.Add(15*time.Minute), Whole)

	mirror(t, src, "damaged")
	if got := capture(t, s, src, noon.Add(30*time.Minute), Damaged); got != Quarantined {
		t.Fatalf("outcome = %q", got)
	}

	v := s.Look()
	if v.Damaged == nil || !v.Damaged.TakenAt.Equal(noon.Add(30*time.Minute)) {
		t.Fatalf("damaged = %+v", v.Damaged)
	}
	if contents(t, v.Current.Path)["FWB/level.dat"] != "level two" || contents(t, v.Previous.Path)["FWB/level.dat"] != "level one" {
		t.Fatalf("the generations moved: current %+v previous %+v", v.Current, v.Previous)
	}
	if !v.Current.TakenAt.Equal(noon.Add(15 * time.Minute)) {
		t.Errorf("current is now %s", v.Current.TakenAt)
	}

	// Every later cycle sees the same loss. Keeping each one would fill the
	// volume while no snapshot can be promoted.
	mirror(t, src, "damaged-later")
	if got := capture(t, s, src, noon.Add(45*time.Minute), Damaged); got != Skipped {
		t.Fatalf("second damaged snapshot = %q", got)
	}
	if body := contents(t, s.Look().Damaged.Path)["FWB/level.dat"]; body != "level damaged" {
		t.Errorf("the quarantined snapshot was replaced: %q", body)
	}
}

// Once the loss is acknowledged the census reports the world whole again,
// and promotion resumes from the next snapshot.
func TestCapture_PromotionResumesAfterTheWorldIsWholeAgain(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	s := open(t, root)
	mirror(t, src, "good", "000005.ldb")
	capture(t, s, src, noon, Whole)
	mirror(t, src, "damaged")
	capture(t, s, src, noon.Add(15*time.Minute), Damaged)
	mirror(t, src, "restored", "000005.ldb")

	if got := capture(t, s, src, noon.Add(30*time.Minute), Whole); got != Promoted {
		t.Fatalf("outcome = %q", got)
	}
	v := s.Look()
	if contents(t, v.Current.Path)["FWB/level.dat"] != "level restored" || contents(t, v.Previous.Path)["FWB/level.dat"] != "level good" {
		t.Errorf("current %+v, previous %+v", v.Current, v.Previous)
	}
	// The quarantined copy is evidence of a loss; nothing here throws it
	// away on the operator's behalf.
	if v.Damaged == nil {
		t.Error("the quarantined snapshot was discarded")
	}
}

// With no whole snapshot ever taken there is nothing to fall back on, and
// a damaged one is still not made the restore point: a copy that looks like
// one and is short of chunks is worse than none, since nothing says so.
func TestCapture_ADamagedSnapshotIsNotTheRestorePointOfLastResort(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	mirror(t, src, "damaged", "000005.ldb")
	s := open(t, root)

	if got := capture(t, s, src, noon, Damaged); got != Quarantined {
		t.Fatalf("outcome = %q", got)
	}
	v := s.Look()
	if v.Current != nil || v.Previous != nil {
		t.Fatalf("holds %+v", v)
	}
	if v.Damaged == nil {
		t.Fatal("the damaged snapshot was not kept at all")
	}
}

// A volume with no room left must cost the staged copy, never a retained
// one. The build is the only step that needs space, so a failure there is
// the whole exposure.
func TestCapture_RunningOutOfRoomLeavesTheRetainedGenerationsAlone(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	s := open(t, root)
	mirror(t, src, "one", "000005.ldb")
	capture(t, s, src, noon, Whole)
	mirror(t, src, "two", "000005.ldb")
	capture(t, s, src, noon.Add(15*time.Minute), Whole)
	before := s.Look()

	mirror(t, src, "three", "000005.ldb", "000006.ldb")
	links := 0
	s.link = func(from, to string) error {
		if links++; links > 2 {
			return &os.LinkError{Op: "link", Old: from, New: to, Err: syscall.ENOSPC}
		}
		return os.Link(from, to)
	}
	if _, err := s.Capture(context.Background(), src, noon.Add(30*time.Minute), Whole); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("err = %v, want ENOSPC", err)
	}

	after := s.Look()
	if after.Current == nil || !after.Current.TakenAt.Equal(before.Current.TakenAt) || after.Previous == nil {
		t.Fatalf("generations after a full volume: %+v", after)
	}
	if contents(t, after.Current.Path)["FWB/level.dat"] != "level two" {
		t.Error("the current generation changed")
	}
	// What the failed build took is given back, or the next cycle inherits
	// a volume that is still full.
	if entries, _ := os.ReadDir(root); slices.ContainsFunc(entries, func(e os.DirEntry) bool {
		return strings.HasPrefix(e.Name(), ".")
	}) {
		t.Errorf("a failed capture left staging behind: %v", names(entries))
	}
}

// The first capture is the one with nothing to fall back on, so a failure
// there must leave no half world that looks like a restore point.
func TestCapture_AFailedFirstCaptureLeavesNothingBehind(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	mirror(t, src, "one", "000005.ldb", "000006.ldb")
	s := open(t, root)
	s.link = func(string, string) error { return syscall.ENOSPC }

	if _, err := s.Capture(context.Background(), src, noon, Whole); err == nil {
		t.Fatal("capture succeeded with a full volume")
	}
	if v := s.Look(); v.Current != nil || v.Previous != nil {
		t.Fatalf("holds %+v", v)
	}
	if v := open(t, root).Look(); v.Current != nil {
		t.Fatalf("after reopening: %+v", v)
	}
}

// failing is a marker file whose write or flush fails the way a full or
// failing volume makes it fail: part of the marker has already landed.
type failing struct {
	*os.File
	write, sync error
}

func (f failing) Write(raw []byte) (int, error) {
	if f.write == nil {
		return f.File.Write(raw)
	}
	n, _ := f.File.Write(raw[:len(raw)/2])
	return n, f.write
}

func (f failing) Sync() error {
	if f.sync != nil {
		return f.sync
	}
	return f.File.Sync()
}

// The marker is what makes a directory a generation, and it is written when
// the volume is at its fullest. One that was cut short or never reached the
// disk must fail the capture: installed, it would take the place of the
// older whole generation and then be named the restore point, while saying
// nothing a restart could read.
func TestCapture_AMarkerThatWasNotWrittenLeavesTheRetainedGenerationsAlone(t *testing.T) {
	for name, broken := range map[string]failing{
		"write": {write: syscall.ENOSPC},
		"flush": {sync: syscall.EIO},
	} {
		t.Run(name, func(t *testing.T) {
			src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
			s := open(t, root)
			mirror(t, src, "one", "000005.ldb")
			capture(t, s, src, noon, Whole)
			mirror(t, src, "two", "000005.ldb")
			capture(t, s, src, noon.Add(15*time.Minute), Whole)
			before := s.Look()

			mirror(t, src, "three", "000005.ldb", "000006.ldb")
			s.stage = func(dir string) (staged, error) {
				f, err := os.CreateTemp(dir, ".marker-*")
				broken.File = f
				return broken, err
			}
			want := cmp.Or(broken.write, broken.sync)
			if _, err := s.Capture(context.Background(), src, noon.Add(30*time.Minute), Whole); !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}

			for when, v := range map[string]View{"at once": s.Look(), "after a restart": open(t, root).Look()} {
				if v.Current == nil || !v.Current.TakenAt.Equal(before.Current.TakenAt) {
					t.Fatalf("%s: current = %+v, want the snapshot of %v", when, v.Current, before.Current.TakenAt)
				}
				if v.Previous == nil || !v.Previous.TakenAt.Equal(before.Previous.TakenAt) {
					t.Fatalf("%s: previous = %+v, want the snapshot of %v", when, v.Previous, before.Previous.TakenAt)
				}
				if contents(t, v.Current.Path)["FWB/level.dat"] != "level two" {
					t.Errorf("%s: the current generation changed", when)
				}
			}
			if entries, _ := os.ReadDir(root); slices.ContainsFunc(entries, func(e os.DirEntry) bool {
				return strings.HasPrefix(e.Name(), ".")
			}) {
				t.Errorf("a failed capture left staging behind: %v", names(entries))
			}
		})
	}
}

// A mirror mid-repair, or one pruned down to nothing, is not a restore
// point: a server cannot open a world with no database.
func TestCapture_RefusesAMirrorThatIsNotAWorld(t *testing.T) {
	root := filepath.Join(t.TempDir(), "generations")
	s := open(t, root)
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Capture(context.Background(), src, noon, Whole); err == nil {
		t.Fatal("captured a directory with no world database")
	}
	if v := s.Look(); v.Current != nil {
		t.Fatalf("holds %+v", v)
	}
}

// The mirror stages an arriving snapshot in a dot-directory at its root.
// Half a world must not reach a generation.
func TestCapture_SkipsTheMirrorsStagingDirectory(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	mirror(t, src, "one", "000005.ldb")
	if err := os.MkdirAll(filepath.Join(src, ".incoming", "FWB", "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".incoming", "FWB", "db", "000009.ldb"), []byte("part"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := open(t, root)
	capture(t, s, src, noon, Whole)

	for name := range contents(t, s.Look().Current.Path) {
		if strings.HasPrefix(name, ".incoming") {
			t.Errorf("the generation holds %s", name)
		}
	}
}

// Generations are hard links, so two of them and the mirror share every
// file none of them has changed. Copies would cost a second and a third
// full world on a volume sized for one.
func TestCapture_GenerationsShareUnchangedFilesWithTheMirror(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	mirror(t, src, "one", "000005.ldb")
	s := open(t, root)
	capture(t, s, src, noon, Whole)

	table := filepath.Join(src, "FWB", "db", "000005.ldb")
	mirrored, err := os.Stat(table)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := os.Stat(filepath.Join(s.Look().Current.Path, "FWB", "db", "000005.ldb"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(mirrored, kept) {
		t.Error("the generation copied a table the mirror already held")
	}
}

// What an interrupted capture leaves is removed on the next start, and the
// generation that was current is still the one named.
func TestOpen_DiscardsWhatAnInterruptedCaptureLeft(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	mirror(t, src, "one", "000005.ldb")
	capture(t, open(t, root), src, noon, Whole)

	for _, leftover := range []string{buildingDir, retiredPrefix + "b"} {
		if err := os.MkdirAll(filepath.Join(root, leftover, "FWB", "db"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("b", filepath.Join(root, switchingLink)); err != nil {
		t.Fatal(err)
	}

	s := open(t, root)
	if v := s.Look(); v.Current == nil || v.Current.Name != "a" || v.Previous != nil {
		t.Fatalf("after reopening: %+v", v)
	}
	entries, _ := os.ReadDir(root)
	if got := names(entries); !slices.Equal(got, []string{"a", "current"}) {
		t.Errorf("root holds %v", got)
	}
	// And the next capture still works, into the slot that is not current.
	mirror(t, src, "two", "000005.ldb")
	capture(t, s, src, noon.Add(15*time.Minute), Whole)
	if v := s.Look(); v.Current.Name != "b" || v.Previous.Name != "a" {
		t.Errorf("after the next capture: current %q previous %q", v.Current.Name, v.Previous.Name)
	}
}

// A directory with no marker was never finished, whatever else is in it.
func TestOpen_ADirectoryWithoutAMarkerIsNotAGeneration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "generations")
	if err := os.MkdirAll(filepath.Join(root, "a", "FWB", "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a", filepath.Join(root, currentLink)); err != nil {
		t.Fatal(err)
	}
	if v := open(t, root).Look(); v.Current != nil || v.Previous != nil {
		t.Fatalf("holds %+v", v)
	}
}

// Restarting the map does not forget which copy is the restore point, nor
// mistake the older one for it.
func TestOpen_RemembersWhichGenerationIsCurrent(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	s := open(t, root)
	for i, mark := range []string{"one", "two"} {
		mirror(t, src, mark, "000005.ldb")
		capture(t, s, src, noon.Add(time.Duration(i)*15*time.Minute), Whole)
	}

	v := open(t, root).Look()
	if v.Current == nil || v.Previous == nil || contents(t, v.Current.Path)["FWB/level.dat"] != "level two" {
		t.Fatalf("after reopening: %+v", v)
	}
	if !v.Current.TakenAt.Equal(noon.Add(15 * time.Minute)) {
		t.Errorf("current taken at %s", v.Current.TakenAt)
	}
}

// The replaced copy is removed after the new generation is installed. If
// that removal fails the capture has still succeeded, and must go on to
// become the restore point rather than sit installed and unpromoted.
func TestCapture_AReplacedCopyThatWillNotGoAwayDoesNotFailTheCapture(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes files from a read-only directory, so the removal cannot be made to fail")
	}
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	s := open(t, root)
	for i, mark := range []string{"one", "two"} {
		mirror(t, src, mark, "000005.ldb")
		capture(t, s, src, noon.Add(time.Duration(i)*15*time.Minute), Whole)
	}
	stuck := filepath.Join(s.Look().Previous.Path, "FWB", "db")
	retired := filepath.Join(root, retiredPrefix+s.Look().Previous.Name, "FWB", "db")
	if err := os.Chmod(stuck, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(retired, 0o755); os.Chmod(stuck, 0o755) })

	mirror(t, src, "three", "000005.ldb")
	if got := capture(t, s, src, noon.Add(30*time.Minute), Whole); got != Promoted {
		t.Fatalf("outcome = %q", got)
	}
	v := s.Look()
	if v.Current == nil || contents(t, v.Current.Path)["FWB/level.dat"] != "level three" {
		t.Fatalf("the new generation is not the restore point: %+v", v.Current)
	}
	if v.Previous == nil || contents(t, v.Previous.Path)["FWB/level.dat"] != "level two" {
		t.Fatalf("the generation before it is not held: %+v", v.Previous)
	}
}

// A store that has lost the link naming its current generation still holds
// one whole copy. The next capture must build beside it, not over it: going
// through the swap is the one moment a crash could leave nothing behind.
func TestCapture_WithTheCurrentLinkGoneBuildsBesideTheSurvivor(t *testing.T) {
	src, root := t.TempDir(), filepath.Join(t.TempDir(), "generations")
	mirror(t, src, "one", "000005.ldb")
	capture(t, open(t, root), src, noon, Whole)
	if err := os.Remove(filepath.Join(root, currentLink)); err != nil {
		t.Fatal(err)
	}

	s := open(t, root)
	survivor := s.Look().Previous
	if s.Look().Current != nil || survivor == nil {
		t.Fatalf("with the link gone: %+v", s.Look())
	}
	var during []string
	s.pause = func(step string) {
		if step == "built" {
			entries, _ := os.ReadDir(root)
			during = names(entries)
		}
		if _, err := os.Stat(filepath.Join(survivor.Path, markerName)); err != nil {
			t.Errorf("at %q the surviving generation was not whole: %v", step, err)
		}
	}

	mirror(t, src, "two", "000005.ldb")
	if got := capture(t, s, src, noon.Add(15*time.Minute), Whole); got != Promoted {
		t.Fatalf("outcome = %q", got)
	}
	v := s.Look()
	if v.Current == nil || v.Current.Name == survivor.Name || v.Previous == nil || v.Previous.Name != survivor.Name {
		t.Fatalf("after the capture: current %+v, previous %+v (survivor was %q; during build %v)", v.Current, v.Previous, survivor.Name, during)
	}
	if contents(t, v.Previous.Path)["FWB/level.dat"] != "level one" {
		t.Errorf("the survivor was rewritten")
	}
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}
