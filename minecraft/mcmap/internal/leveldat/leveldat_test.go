package leveldat

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/level.dat is the FWB world's own file as the server wrote it on
// 2026-10-05 (storage version 10, game 1.26.52.3), with one change: the
// eight bytes of RandomSeed are replaced by fixtureSeed. The world's real
// seed is not something to publish.
const fixtureSeed = -4172144997902289642

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "level.dat"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// withHeader wraps an NBT body the way the game does.
func withHeader(body []byte) []byte {
	out := binary.LittleEndian.AppendUint32(nil, 10)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

func named(kind byte, name string, payload ...byte) []byte {
	out := []byte{kind}
	out = binary.LittleEndian.AppendUint16(out, uint16(len(name)))
	return append(append(out, name...), payload...)
}

func root(tags ...[]byte) []byte {
	body := named(tagCompound, "")
	for _, tag := range tags {
		body = append(body, tag...)
	}
	return withHeader(append(body, tagEnd))
}

var (
	seedTag = named(tagLong, "RandomSeed", 1, 0, 0, 0, 0, 0, 0, 0)
	xTag    = named(tagInt, "SpawnX", 0xf0, 0xff, 0xff, 0xff)
	yTag    = named(tagInt, "SpawnY", 64, 0, 0, 0)
	zTag    = named(tagInt, "SpawnZ", 200, 0, 0, 0)
)

func TestParse_RealLevelDat(t *testing.T) {
	got, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	// This world never resolved its spawn height: it stores 32767.
	want := Level{Seed: fixtureSeed, SpawnX: 0, SpawnZ: 0, SpawnY: 32767, SpawnYKnown: false}
	if got != want {
		t.Errorf("Parse = %+v, want %+v", got, want)
	}
}

func TestParse_ReadsNegativeCoordinatesAndAResolvedHeight(t *testing.T) {
	got, err := Parse(root(xTag, named(tagString, "LevelName", 3, 0, 'F', 'W', 'B'), yTag, zTag, seedTag))
	if err != nil {
		t.Fatal(err)
	}
	want := Level{Seed: 1, SpawnX: -16, SpawnY: 64, SpawnZ: 200, SpawnYKnown: true}
	if got != want {
		t.Errorf("Parse = %+v, want %+v", got, want)
	}
}

// Zero is a valid seed and the origin a valid spawn, so a file without them
// must be refused, not read as a world that has those.
func TestParse_RefusesAFileWithoutTheSeedOrTheSpawn(t *testing.T) {
	for name, data := range map[string][]byte{
		"no seed":    root(xTag, yTag, zTag),
		"no spawn x": root(seedTag, yTag, zTag),
		"no spawn z": root(seedTag, xTag, yTag),
		// The right name with the wrong type is not the seed.
		"seed as int": root(named(tagInt, "RandomSeed", 1, 0, 0, 0), xTag, zTag),
	} {
		if got, err := Parse(data); err == nil {
			t.Errorf("%s: Parse = %+v, want an error", name, got)
		}
	}
}

// The server replaces level.dat while it runs. A copy caught part-way has a
// header promising more than arrived, and the tags that did arrive are not
// the world's settings.
func TestParse_RefusesAFileShorterThanItsHeaderSays(t *testing.T) {
	data := fixture(t)
	for _, n := range []int{0, 4, 8, 9, len(data) / 2, len(data) - 1} {
		if got, err := Parse(data[:n]); err == nil {
			t.Errorf("Parse of the first %d bytes = %+v, want an error", n, got)
		}
	}
	if _, err := Parse(append(bytes.Clone(data), 0)); err == nil {
		t.Error("Parse accepted a file longer than its header says")
	}
}

// Every way of cutting the real file short, with a header that agrees with
// the cut so that the tag reader itself is what has to notice.
func TestParse_NeverReadsPastTheEnd(t *testing.T) {
	body := fixture(t)[headerSize:]
	for n := range len(body) {
		if got, err := Parse(withHeader(body[:n])); err == nil {
			t.Fatalf("Parse of a body cut to %d bytes = %+v, want an error", n, got)
		}
	}
}

func TestParse_RefusesLengthsTheFileCannotHold(t *testing.T) {
	huge := []byte{0xff, 0xff, 0xff, 0x7f}
	deep := named(tagList, "deep")
	for range maxDepth + 2 {
		deep = append(deep, tagList, 1, 0, 0, 0)
	}
	deep = append(deep, tagByte, 1, 0, 0, 0, 7)
	for name, tag := range map[string][]byte{
		"byte array":      named(tagByteArray, "a", huge...),
		"long array":      named(tagLongArray, "a", huge...),
		"negative array":  named(tagIntArray, "a", 0xff, 0xff, 0xff, 0xff),
		"list":            named(tagList, "a", append([]byte{tagCompound}, huge...)...),
		"list of nothing": named(tagList, "a", append([]byte{tagEnd}, 1, 0, 0, 0)...),
		"unknown type":    named(13, "a", 0, 0, 0, 0),
		"nested too deep": deep,
	} {
		if got, err := Parse(root(seedTag, xTag, zTag, tag)); err == nil {
			t.Errorf("%s: Parse = %+v, want an error", name, got)
		}
	}
	// What those cases wrap is fine without them.
	if _, err := Parse(root(seedTag, xTag, zTag, named(tagList, "empty", tagEnd, 0, 0, 0, 0))); err != nil {
		t.Errorf("an empty list was refused: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	data, err := os.ReadFile(filepath.Join("testdata", "level.dat"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add(root(seedTag, xTag, yTag, zTag))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}

// The map reads the world; it never writes it. The file and its directory
// are made unwritable, so a reader that opened for writing, or left a lock
// or a temporary file beside it, fails here.
func TestRead_OpensTheFileReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes, so this cannot tell a read from a write")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "level.dat")
	if err := os.WriteFile(path, fixture(t), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seed != fixtureSeed {
		t.Errorf("Seed = %d, want %d", got.Seed, int64(fixtureSeed))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d entries after a read, want only level.dat", len(entries))
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, fixture(t)) {
		t.Errorf("level.dat changed under a read (%v)", err)
	}
}

func TestRead_RefusesAnOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "level.dat")
	if err := os.WriteFile(path, make([]byte, maxSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "over") {
		t.Errorf("Read of an oversized file: %v, want a size refusal", err)
	}
}
