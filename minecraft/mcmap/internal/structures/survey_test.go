package structures

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// testSeed is the low half of the seed the test worlds generate from; the
// high half is set too, and must make no difference.
const (
	testSeed  = 20261005
	levelSeed = int64(0x5eed<<32 | testSeed)
)

var surveyedAt = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

type world struct {
	t       *testing.T
	records map[string][]byte
	seed    *int64
}

func newWorld(t *testing.T) *world {
	seed := levelSeed
	return &world{t: t, records: map[string][]byte{}, seed: &seed}
}

func (w *world) put(p chunks.Pos, tag byte, value []byte) *world {
	w.records[string(chunks.RecordKey(p, tag))] = value
	return w
}

// generated marks a chunk as complete, which is also what gives the
// dimension an extent to predict over.
func (w *world) generated(d chunks.Dimension, x, z int32) *world {
	return w.put(chunks.Pos{Dim: d, X: x, Z: z}, TagFinalized, []byte{finalizedDone, 0, 0, 0})
}

// structure records box whole in the chunk its first corner is in, which is
// how a small structure sits; the tests' boxes are made to fit.
func (w *world) structure(d chunks.Dimension, kind byte, box Box) *world {
	p := chunks.Pos{Dim: d, X: box.MinX >> 4, Z: box.MinZ >> 4}
	key := string(chunks.RecordKey(p, TagSpawnAreas))
	var held []any
	if old, ok := w.records[key]; ok {
		for a := old[4:]; len(a) > 0; a = a[areaSize:] {
			at := func(i int) int32 { return int32(binary.LittleEndian.Uint32(a[i*4:])) }
			held = append(held, Box{at(0), at(1), at(2), at(3), at(4), at(5)}, a[24])
		}
	}
	w.records[key] = record(append(held, box, kind)...)
	return w
}

// write puts the world on disk the way a mirrored one sits: a db directory
// with no LOCK file, and level.dat beside it.
func (w *world) write() string {
	w.t.Helper()
	dir := w.t.TempDir()
	db, err := leveldb.OpenFile(filepath.Join(dir, "db"), nil)
	if err != nil {
		w.t.Fatal(err)
	}
	for k, v := range w.records {
		if err := db.Put([]byte(k), v, nil); err != nil {
			w.t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		w.t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "db", "LOCK")); err != nil {
		w.t.Fatal(err)
	}
	if w.seed != nil {
		if err := os.WriteFile(filepath.Join(dir, "level.dat"), levelDat(*w.seed, 40, -72), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
	return dir
}

func levelDat(seed int64, spawnX, spawnZ int32) []byte {
	tag := func(kind byte, name string, payload []byte) []byte {
		out := binary.LittleEndian.AppendUint16([]byte{kind}, uint16(len(name)))
		return append(append(out, name...), payload...)
	}
	body := tag(10, "", nil)
	body = append(body, tag(4, "RandomSeed", binary.LittleEndian.AppendUint64(nil, uint64(seed)))...)
	body = append(body, tag(3, "SpawnX", binary.LittleEndian.AppendUint32(nil, uint32(spawnX)))...)
	body = append(body, tag(3, "SpawnZ", binary.LittleEndian.AppendUint32(nil, uint32(spawnZ)))...)
	body = append(body, 0)
	out := binary.LittleEndian.AppendUint32(nil, 10)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

func surveyor(t *testing.T, log *bytes.Buffer) *Surveyor {
	if log == nil {
		log = &bytes.Buffer{}
	}
	return &Surveyor{WorkDir: t.TempDir(), Predictors: Predictors, Logger: slog.New(slog.NewTextHandler(log, nil))}
}

// monumentAt is the part of a monument inside its site's own chunk, which
// is enough for the site to explain it.
func monumentAt(s Site) Box {
	return Box{s.ChunkX * 16, 39, s.ChunkZ * 16, s.ChunkX*16 + 15, 61, s.ChunkZ*16 + 15}
}

// evidence is a world with three monuments exactly where testSeed puts
// them, in an overworld and a nether 128 chunks across.
func evidence(t *testing.T) *world {
	w := newWorld(t)
	for _, d := range []chunks.Dimension{chunks.Overworld, chunks.Nether} {
		w.generated(d, 0, 0).generated(d, 63, 63)
	}
	for _, region := range [][2]int32{{0, 0}, {1, 0}, {0, 1}} {
		site, _ := monumentSpread.site(testSeed, region[0], region[1])
		w.structure(chunks.Overworld, monumentByte, monumentAt(site))
	}
	return w
}

// evidenceArea is the window a survey of evidence looks in: its chunks and
// the margin round them.
var evidenceArea = Area{-64, -64, 127, 127}

// fortressAt is a fragment of a fortress in its site's own chunk.
func fortressAt(s Site) Box {
	return Box{s.ChunkX*16 + 2, 48, s.ChunkZ*16 + 3, s.ChunkX*16 + 12, 57, s.ChunkZ*16 + 9}
}

// fortresses records one at each of the last three fortress sites beside
// the world, which is what it takes for fortresses to be predicted, and
// returns the sites it left alone.
func (w *world) fortresses() (free []Site) {
	sites, _ := fortress{}.Sites(testSeed, evidenceArea, 100)
	if len(sites) < 7 {
		w.t.Fatalf("only %d fortress sites to test with", len(sites))
	}
	for _, site := range sites[len(sites)-3:] {
		w.structure(chunks.Nether, fortressByte, fortressAt(site))
	}
	return sites[:len(sites)-3]
}

func listing(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(p string, _ os.DirEntry, err error) error {
		names = append(names, p)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestTake_ReadsRecordedStructuresByDimension(t *testing.T) {
	dir := newWorld(t).
		put(fortressChunk, TagSpawnAreas, fortressRecord).
		put(hutChunk, TagSpawnAreas, hutRecord).
		// Records that are not spawn areas, and one that only looks like a
		// chunk's.
		put(hutChunk, 0x2c, []byte{41}).
		put(chunks.Pos{Dim: chunks.End, X: 1, Z: 1}, 0x2f, []byte("subchunk")).
		write()
	before := listing(t, dir)

	s := surveyor(t, nil)
	got, err := s.Take(context.Background(), dir, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Areas != 9 || got.Malformed != 0 || got.Unknown != 0 || got.OverLimit != 0 {
		t.Errorf("areas %d, malformed %d, unknown %d, over limit %d", got.Areas, got.Malformed, got.Unknown, got.OverLimit)
	}
	wantNether := []Structure{{Kind: Fortress, Box: fortressBox, Areas: 8}}
	if nether := got.Layers[chunks.Nether].Recorded; !slices.Equal(nether, wantNether) {
		t.Errorf("nether = %+v, want %+v", nether, wantNether)
	}
	wantOverworld := []Structure{{Kind: WitchHut, Box: hutBox, Areas: 1}}
	if overworld := got.Layers[chunks.Overworld].Recorded; !slices.Equal(overworld, wantOverworld) {
		t.Errorf("overworld = %+v, want %+v", overworld, wantOverworld)
	}
	if end := got.Layers[chunks.End]; len(end.Recorded) != 0 {
		t.Errorf("end = %+v, want nothing", end.Recorded)
	}
	if !got.HasLevel || got.Level.Seed != levelSeed || got.Level.SpawnX != 40 || got.Level.SpawnZ != -72 {
		t.Errorf("level = %+v (read %v)", got.Level, got.HasLevel)
	}
	if last, ok := s.Last(); !ok || !last.At.Equal(surveyedAt) {
		t.Errorf("Last = %v, %v", last.At, ok)
	}

	// The mirror must hold only the server's files: a survey that opened
	// it directly would leave a LOCK file, and one that wrote would do worse.
	if after := listing(t, dir); !slices.Equal(before, after) {
		t.Errorf("the world changed under a survey:\n before %v\n after  %v", before, after)
	}
	if left, _ := os.ReadDir(s.WorkDir); len(left) != 0 {
		t.Errorf("the survey left %d entries in its work directory", len(left))
	}
}

func TestTake_OffersPredictionsOnceTheSeedExplainsTheWorld(t *testing.T) {
	w := evidence(t)
	sites := w.fortresses()
	built, empty := sites[0], sites[1]
	log := &bytes.Buffer{}
	dir := w.
		// One fortress the world already has, recorded near its site.
		structure(chunks.Nether, fortressByte, Box{built.ChunkX*16 + 20, 48, built.ChunkZ*16 + 3, built.ChunkX*16 + 30, 57, built.ChunkZ*16 + 9}).
		// And one site whose chunk is complete with nothing recorded.
		generated(chunks.Nether, empty.ChunkX, empty.ChunkZ).
		write()

	s := surveyor(t, log)
	got, err := s.Take(context.Background(), dir, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedVerified || got.Check.Agree != 3 || got.Check.Disagree != 0 {
		t.Fatalf("check = %+v, want the seed verified by 3", got.Check)
	}
	isSite := map[Prediction]bool{}
	for _, site := range sites {
		isSite[Prediction{Kind: Fortress, X: site.ChunkX*16 + 8, Z: site.ChunkZ*16 + 8}] = true
	}
	nether := got.Layers[chunks.Nether].Predicted
	if len(nether) < 3 {
		t.Fatalf("only %d predictions: %+v", len(nether), nether)
	}
	sawEmpty := false
	for _, p := range nether {
		at := Site{(p.X - 8) / 16, (p.Z - 8) / 16}
		switch {
		case at == built:
			t.Errorf("%+v is predicted where the world already recorded a fortress", p)
		case at == empty:
			sawEmpty = true
			if !p.Generated {
				t.Errorf("%+v is in a complete chunk and not marked so", p)
			}
		case p.Generated:
			t.Errorf("%+v is marked generated in a chunk the world does not hold", p)
		}
		// Bastion regions are in the same area and must not be offered.
		if p.Generated = false; !isSite[p] && at.ChunkX >= -64 && at.ChunkX <= 127 && at.ChunkZ >= -64 && at.ChunkZ <= 127 {
			t.Errorf("%+v is not a fortress site of this seed", p)
		}
	}
	if !sawEmpty {
		t.Errorf("the site in a complete chunk is missing from %+v", nether)
	}
	// Monument sites are where the generator tries; whether it builds
	// depends on a biome nothing here knows, so each is a candidate. The
	// world has recorded none of the other kinds to check their rules by.
	overworld := got.Layers[chunks.Overworld].Predicted
	if len(overworld) == 0 {
		t.Error("no monument sites offered by a world that bears out three")
	}
	for _, p := range overworld {
		if p.Kind != Monument || !p.Candidate || p.Generated {
			t.Errorf("overworld prediction %+v, want a monument and a candidate", p)
		}
	}
	for kind, want := range map[Kind]string{Fortress: SeedVerified, Monument: SeedVerified, Outpost: SeedUnverified, Village: SeedUnverified, WitchHut: SeedUnverified} {
		if got := got.Check.Kinds[kind].State; got != want {
			t.Errorf("%s is %s, want %s", kind, got, want)
		}
	}

	// The site that came to nothing is a finding, said once.
	finding := "fortress predicted at nether"
	if got.Check.Total != 1 || len(got.Check.Findings) != 1 || !strings.Contains(got.Check.Findings[0], finding) {
		t.Errorf("findings = %v (total %d)", got.Check.Findings, got.Check.Total)
	}
	if n := strings.Count(log.String(), finding); n != 1 {
		t.Errorf("the finding was logged %d times, want 1:\n%s", n, log)
	}
	if _, err := s.Take(context.Background(), dir, surveyedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(log.String(), finding); n != 1 {
		t.Errorf("an unchanged finding was logged again (%d times)", n)
	}
	if strings.Contains(log.String(), "20261005") || strings.Contains(log.String(), "seed=") {
		t.Errorf("the log names the seed:\n%s", log)
	}
}

// A seed that does not put the world's own structures where they are is not
// one to predict from: every prediction would be a guess.
func TestTake_WithholdsPredictionsFromASeedTheWorldContradicts(t *testing.T) {
	w := evidence(t)
	wrong := levelSeed + 1
	w.seed = &wrong
	log := &bytes.Buffer{}
	got, err := surveyor(t, log).Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedRefuted || got.Check.Agree != 0 || got.Check.Disagree != 3 {
		t.Errorf("check = %+v, want the seed refuted by 3", got.Check)
	}
	for d, layer := range got.Layers {
		if len(layer.Predicted) != 0 || layer.PredictedMore != 0 {
			t.Errorf("%s: %d predictions offered from a refuted seed", d.Name(), len(layer.Predicted))
		}
	}
	if len(got.Layers[chunks.Overworld].Recorded) != 3 {
		t.Errorf("recorded structures = %+v, want all 3 whatever the seed", got.Layers[chunks.Overworld].Recorded)
	}
	if got.Check.Total != 3 || len(got.Check.Findings) != 3 || !strings.Contains(got.Check.Findings[0], "the seed puts none there") {
		t.Errorf("findings = %v (total %d), want the 3 monuments", got.Check.Findings, got.Check.Total)
	}
	// One line for the cause, not one per structure it shows up in.
	if n := strings.Count(log.String(), "nothing is predicted"); n != 1 || strings.Contains(log.String(), "finding=") {
		t.Errorf("the refusal was logged %d times, want once and no findings:\n%s", n, log)
	}
}

func TestTake_PredictsNothingUntilThereIsEnoughToCheckTheSeedAgainst(t *testing.T) {
	w := newWorld(t).generated(chunks.Overworld, 0, 0).generated(chunks.Nether, 0, 0).generated(chunks.Nether, 63, 63)
	for _, region := range [][2]int32{{0, 0}, {1, 0}} {
		site, _ := monumentSpread.site(testSeed, region[0], region[1])
		w.structure(chunks.Overworld, monumentByte, monumentAt(site))
	}
	got, err := surveyor(t, nil).Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedUnverified || got.Check.Agree != 2 {
		t.Errorf("check = %+v, want unverified with 2", got.Check)
	}
	if n := len(got.Layers[chunks.Nether].Predicted); n != 0 {
		t.Errorf("%d predictions offered on two structures' evidence", n)
	}
}

// One chunk a long way off, which a glitch or a long trip leaves behind,
// must not move the search away from where the world is.
func TestTake_AFarOffChunkDoesNotMoveTheSearch(t *testing.T) {
	w := evidence(t).
		generated(chunks.Overworld, -(1<<21), 1<<21).
		generated(chunks.Nether, 1<<21, -(1<<21)).
		// A monument out there is not at a site anyone looked for, and
		// says nothing about the seed.
		structure(chunks.Overworld, monumentByte, Box{-(1 << 25), 39, 1 << 25, -(1 << 25) + 15, 61, 1<<25 + 15})
	near := w.fortresses()

	got, err := surveyor(t, nil).Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedVerified || got.Check.Agree != 3 || got.Check.Disagree != 0 {
		t.Fatalf("check = %+v, want the seed verified by the 3 monuments near the world", got.Check)
	}
	predicted := map[Site]bool{}
	for _, p := range got.Layers[chunks.Nether].Predicted {
		predicted[Site{(p.X - 8) / 16, (p.Z - 8) / 16}] = true
	}
	for _, site := range near {
		if !predicted[site] {
			t.Errorf("fortress site %+v beside the world is not predicted", site)
		}
	}
	if len(got.Layers[chunks.Overworld].Recorded) != 4 {
		t.Errorf("recorded = %+v, want the far monument kept as recorded", got.Layers[chunks.Overworld].Recorded)
	}
}

func TestTake_UsesTheOperatorsSeedInPlaceOfLevelDat(t *testing.T) {
	w := evidence(t)
	w.fortresses()
	wrong := levelSeed + 1
	w.seed = &wrong
	s := surveyor(t, nil)
	seed := uint32(testSeed)
	s.StructureSeed = &seed
	got, err := s.Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedVerified {
		t.Errorf("check = %+v, want verified", got.Check)
	}
	if len(got.Layers[chunks.Nether].Predicted) == 0 {
		t.Error("no predictions from a verified seed")
	}
}

// Recorded structures need no seed, so a level.dat that cannot be read
// costs the predictions and nothing else.
func TestTake_WithoutLevelDatStillReadsWhatIsRecorded(t *testing.T) {
	w := evidence(t)
	w.seed = nil
	got, err := surveyor(t, nil).Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.HasLevel || got.Check.State != SeedUnknown {
		t.Errorf("level read %v, check %+v", got.HasLevel, got.Check)
	}
	if len(got.Layers[chunks.Overworld].Recorded) != 3 || len(got.Layers[chunks.Nether].Predicted) != 0 {
		t.Errorf("layers = %+v", got.Layers)
	}
}

func TestTake_CountsWhatItCannotReadAndKeepsTheRest(t *testing.T) {
	good := Box{160, 64, 160, 175, 85, 175}
	whole := fortressRecord
	dir := newWorld(t).
		structure(chunks.Overworld, outpostByte, good).
		put(fortressChunk, TagSpawnAreas, whole[:len(whole)-3]).
		put(chunks.Pos{X: 20, Z: 20}, TagSpawnAreas, record(Box{320, 64, 320, 335, 85, 335}, byte(9), Box{335, 64, 320, 320, 85, 335}, outpostByte)).
		write()
	got, err := surveyor(t, nil).Take(context.Background(), dir, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Areas != 1 || got.Malformed != 2 || got.Unknown != 1 {
		t.Errorf("areas %d, malformed %d, unknown %d; want 1, 2, 1", got.Areas, got.Malformed, got.Unknown)
	}
	if want := []Structure{{Kind: Outpost, Box: good, Areas: 1}}; !slices.Equal(got.Layers[chunks.Overworld].Recorded, want) {
		t.Errorf("recorded = %+v, want %+v", got.Layers[chunks.Overworld].Recorded, want)
	}
	if n := len(got.Layers[chunks.Nether].Recorded); n != 0 {
		t.Errorf("%d structures taken from a record that was cut short", n)
	}
}

func TestTake_BoundsWhatItKeeps(t *testing.T) {
	w := evidence(t)
	w.fortresses()
	// Ten fortress fragments, too far apart to be one, and outside the
	// area the fortress rule is checked in.
	for i := range int32(10) {
		w.structure(chunks.Nether, fortressByte, Box{-4000 + i*64, 64, -4000, -3995 + i*64, 70, -3995})
	}
	dir := w.write()

	s := surveyor(t, nil)
	s.layerLimit = 4
	got, err := s.Take(context.Background(), dir, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	nether := got.Layers[chunks.Nether]
	if len(nether.Recorded) != 4 || nether.RecordedMore != 9 {
		t.Errorf("recorded %d (+%d), want 4 (+9)", len(nether.Recorded), nether.RecordedMore)
	}
	if len(nether.Predicted) != 4 || nether.PredictedMore < 1 {
		t.Errorf("predicted %d (+%d), want 4 and the rest counted", len(nether.Predicted), nether.PredictedMore)
	}
	// The seed is checked against every site, however few are shown.
	if got.Check.State != SeedVerified {
		t.Errorf("check = %+v: the layer's limit cost the seed its evidence", got.Check)
	}

	s = surveyor(t, nil)
	s.areaLimit = 5
	got, err = s.Take(context.Background(), dir, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Areas != 5 || got.OverLimit != 11 {
		t.Errorf("areas %d, over limit %d; want 5 and 11", got.Areas, got.OverLimit)
	}
}

func TestTake_AFailureKeepsTheLastSurvey(t *testing.T) {
	s := surveyor(t, nil)
	if _, ok := s.Last(); ok {
		t.Fatal("a survey before the first")
	}
	if _, err := s.Take(context.Background(), evidence(t).write(), surveyedAt); err != nil {
		t.Fatal(err)
	}
	// Not a world: opening it must fail, not make an empty one.
	empty := t.TempDir()
	if err := os.Mkdir(filepath.Join(empty, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Take(context.Background(), empty, surveyedAt.Add(time.Hour)); err == nil {
		t.Fatal("a directory that is not a world was surveyed")
	}
	if entries, _ := os.ReadDir(filepath.Join(empty, "db")); len(entries) != 0 {
		t.Errorf("the failed survey made %d files where there was no world", len(entries))
	}
	last, ok := s.Last()
	if !ok || !last.At.Equal(surveyedAt) || len(last.Layers[chunks.Overworld].Recorded) != 3 {
		t.Errorf("Last after a failure = %v, %v", last.At, ok)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Take(ctx, evidence(t).write(), surveyedAt); err == nil {
		t.Error("a cancelled survey reported success")
	}
}

// A survey that panics part way is caught by whoever runs it. What it must
// not do is take the last survey with it, or leave the surveyor unable to
// take another.
func TestTake_APanicKeepsTheLastSurveyAndTheSurveyorUsable(t *testing.T) {
	s := surveyor(t, nil)
	w := evidence(t)
	first := take(t, s, w)
	s.Biomes = func(chunks.Dimension, int32, int32) (uint32, bool) { panic("made up") }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the survey did not panic, so this test shows nothing")
			}
		}()
		_, _ = s.Take(context.Background(), w.write(), surveyedAt.Add(time.Hour))
	}()
	if last, ok := s.Last(); !ok || !last.At.Equal(first.At) {
		t.Errorf("after a panic the last survey is of %v (%v), want the one before it", last.At, ok)
	}
	if left, _ := os.ReadDir(s.WorkDir); len(left) != 0 {
		t.Errorf("the survey that panicked left %d entries in its work directory", len(left))
	}
	s.Biomes = nil
	if again := take(t, s, w); !again.At.Equal(surveyedAt) {
		t.Errorf("the next survey = %v", again.At)
	}
}
