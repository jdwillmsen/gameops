package insight

import (
	"testing"
	"time"
)

func TestTrackerLifecycle(t *testing.T) {
	tr := NewTracker()
	f := Finding{Rule: RuleZeroSuccess, Unit: "crew-16", Severity: SevCrit, Evidence: []int64{1, 2, 3}}
	if !tr.Apply(t0, []Finding{f}) {
		t.Fatal("first sighting should change state")
	}
	if tr.Apply(t0.Add(time.Second), []Finding{f}) {
		t.Fatal("same finding should dedupe")
	}
	l := tr.List()
	if len(l) != 1 || l[0].State != StateFiring || !l[0].FirstSeen.Equal(t0) {
		t.Fatalf("list = %+v", l)
	}
	f.Evidence = []int64{1, 2, 3, 4}
	if !tr.Apply(t0.Add(2*time.Second), []Finding{f}) || len(tr.List()[0].Evidence) != 4 {
		t.Fatal("new evidence should update in place")
	}
	if !tr.Apply(t0.Add(time.Minute), nil) {
		t.Fatal("clearing should change state")
	}
	l = tr.List()
	if len(l) != 1 || l[0].State != StateResolved || !l[0].ResolvedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("list = %+v", l)
	}
	tr.Apply(t0.Add(2*time.Minute), []Finding{f})
	l = tr.List()
	if len(l) != 2 || l[0].State != StateFiring || l[0].ID == l[1].ID {
		t.Fatalf("refire should be a new insight, got %+v", l)
	}
}
