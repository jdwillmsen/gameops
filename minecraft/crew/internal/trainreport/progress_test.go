package trainreport

import "testing"

func TestProgressBucketsByTime(t *testing.T) {
	cards := fixture(
		// Two buckets ten minutes apart, one family per bucket, busiest
		// fights first; one card without a timestamp drops out cleanly.
		`{"ts":"2026-10-27T10:01:00Z","family":"zombie","profile":"alpha","outcome":"win","seconds":30}`,
		`{"ts":"2026-10-27T10:02:00Z","family":"zombie","profile":"alpha","outcome":"win","seconds":31}`,
		`{"ts":"2026-10-27T10:11:00Z","family":"zombie","profile":"alpha","outcome":"win","seconds":32}`,
		`{"ts":"2026-10-27T10:12:00Z","family":"skeleton","profile":"alpha","outcome":"win","seconds":33}`,
		`{"family":"zombie","profile":"alpha","outcome":"win","seconds":30}`,
	)
	var ps []Progress
	pipe := ProgressBuckets(cards, "alpha", 10*60*1000)
	for _, p := range pipe {
		if p.Family == "zombie" || p.Family == "skeleton" {
			ps = append(ps, p)
		}
	}
	if len(ps) != 2 {
		t.Fatalf("families %+v", ps)
	}
	if ps[0].Family != "zombie" || len(ps[0].Buckets) != 2 ||
		ps[0].Buckets[0].Start != "2026-10-27T10:00:00Z" || ps[0].Buckets[0].Fights != 2 ||
		ps[0].Buckets[1].Fights != 1 {
		t.Fatalf("zombie progress %+v", ps[0])
	}
	// With width 0 the whole log is one bucket per family.
	whole := ProgressBuckets(cards, "alpha", 0)
	if len(whole) != 2 || len(whole[0].Buckets) != 1 || whole[0].Buckets[0].Fights != 3 {
		t.Fatalf("whole-log progress %+v", whole)
	}
}

func TestProgressOnlyNamedProfileAndDropsAborted(t *testing.T) {
	cards := fixture(
		`{"ts":"2026-10-27T10:01:00Z","family":"zombie","profile":"alpha","outcome":"win","seconds":30}`,
		`{"ts":"2026-10-27T10:02:00Z","family":"zombie","profile":"alpha","outcome":"aborted","seconds":1}`,
		`{"ts":"2026-10-27T10:02:00Z","family":"zombie","profile":"beta","outcome":"win","seconds":30}`,
	)
	ps := ProgressBuckets(cards, "alpha", 0)
	if len(ps) != 1 || len(ps[0].Buckets) != 1 || ps[0].Buckets[0].Fights != 1 {
		t.Fatalf("progress %+v", ps)
	}
}

func TestBuildWiresBestProfileIntoProgress(t *testing.T) {
	cards := fixture(
		`{"ts":"2026-10-27T10:01:00Z","family":"zombie","profile":"alpha","outcome":"win","seconds":30}`,
		`{"ts":"2026-10-27T10:02:00Z","family":"zombie","profile":"beta","outcome":"loss","seconds":30}`,
	)
	rep := Build(cards, Options{BucketMs: 10 * 60 * 1000})
	if rep.BestProfile != "alpha" {
		t.Fatalf("best %q", rep.BestProfile)
	}
	if len(rep.Progress) != 1 || rep.Progress[0].Profile != "alpha" ||
		rep.Progress[0].Buckets[0].Fights != 1 {
		t.Fatalf("progress %+v", rep.Progress)
	}
}
