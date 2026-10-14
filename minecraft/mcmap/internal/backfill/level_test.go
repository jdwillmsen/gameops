package backfill

import (
	"context"
	"strings"
	"testing"
)

func level(dir, said string) []file {
	return []file{{dir + "/level.dat", said}, {dir + "/db/CURRENT", "MANIFEST-000001\n"}}
}

func together(sets ...[]file) []file {
	var out []file
	for _, set := range sets {
		out = append(out, set...)
	}
	return out
}

// Archives are made by more than one thing, and each holds the level where
// its maker keeps it: at the root, under worlds/ as the server does, or
// under the server's own directory above that.
func TestAnArchiveHoldsTheLevelAtItsRootOrAFewDirectoriesDown(t *testing.T) {
	for name, dir := range map[string]string{
		"at the root":         "./FWB",
		"under worlds":        "./worlds/FWB",
		"under server/worlds": "server/worlds/FWB",
		"three down":          "a/b/c/FWB",
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t)
			withScanner(t, s)
			archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", together(level(dir, name), []file{{"server.properties", "x"}, {"server/worlds/Other/level.dat", "another level"}})...)
			r, err := s.job.Run(context.Background())
			if err != nil || r.Kept != 1 {
				t.Fatalf("result = %+v, err = %v", r, err)
			}
			if got := tileOf(t, s.store, "2026-10-01"); got != "overworld of "+name {
				t.Errorf("the day's tile = %q", got)
			}
			if got, _ := markersOf(t, s.store, "2026-10-01", "overworld"); got != "overworld markers of "+name+" at 2026-10-01T04:00:07Z" {
				t.Errorf("the day's markers = %q", got)
			}
		})
	}
}

func TestOfSeveralLevelsInAnArchiveTheShallowestIsTheDayAndThenTheFirstByPath(t *testing.T) {
	for name, tc := range map[string]struct {
		files []file
		want  string
	}{
		"the root over one below it": {together(level("server/worlds/FWB", "deep"), level("worlds/FWB", "under worlds"), level("FWB", "at the root")), "at the root"},
		"one down over two down":     {together(level("server/worlds/FWB", "deep"), level("worlds/FWB", "under worlds")), "under worlds"},
		"a level inside the level":   {together(level("FWB/FWB", "inside"), level("FWB", "outside")), "outside"},
		"as deep, the first by path": {together(level("worlds/FWB", "worlds"), level("backup/FWB", "backup"), level("old/FWB", "old")), "backup"},
		// By the whole path and not a directory at a time, which differ
		// where one name is the start of another.
		"as deep, by the whole path": {together(level("a/FWB", "a"), level("a-b/FWB", "a-b")), "a-b"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t)
			archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", tc.files...)
			if _, err := s.job.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := tileOf(t, s.store, "2026-10-01"); got != "overworld of "+tc.want {
				t.Errorf("the day's tile = %q, want the level %s", got, tc.want)
			}
		})
	}
}

// A directory that only has the level's name is not the level: a server's
// backup has one for the level's packs beside the level itself.
func TestADirectoryOfTheLevelsNameWithoutALevelDatIsNotTheLevel(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", together([]file{{"FWB/notes.txt", "x"}, {"packs/FWB/level.dat/inside", "a directory, not the file"}}, level("server/worlds/FWB", "the level"))...)
	archive(t, s.archives, "fwb-20261002T040009Z.tar.gz", file{"FWB/db/CURRENT", "x"}, file{"worlds/FWB/readme", "x"})
	archive(t, s.archives, "fwb-20261003T040012Z.tar.gz", level("a/b/c/d/FWB", "too deep")...)
	archive(t, s.archives, "fwb-20261004T040012Z.tar.gz", level("worlds/fwb", "another name")...)

	r, err := s.job.Run(context.Background())
	if err == nil || r.Kept != 1 || r.Failed != 3 {
		t.Fatalf("result = %+v, err = %v; want the one with a level kept and the three without refused", r, err)
	}
	if got := tileOf(t, s.store, "2026-10-01"); got != "overworld of the level" {
		t.Errorf("the day's tile = %q", got)
	}
	for _, name := range []string{"fwb-20261002T040009Z.tar.gz", "fwb-20261003T040012Z.tar.gz", "fwb-20261004T040012Z.tar.gz"} {
		if !strings.Contains(s.log.String(), name) {
			t.Errorf("the log does not name %s", name)
		}
	}
	if !strings.Contains(s.log.String(), "holds no FWB with a level.dat") {
		t.Errorf("the log does not say what the archives lacked:\n%s", s.log.String())
	}
}
