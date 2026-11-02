package dimension

import "testing"

func TestNameMapsKnownIdsAndFoldsTheRestIntoOther(t *testing.T) {
	for id, want := range map[int32]string{0: Overworld, 1: Nether, 2: End, 3: Other, -1: Other, 1 << 20: Other} {
		if got := Name(id); got != want {
			t.Errorf("Name(%d) = %q, want %q", id, got, want)
		}
	}
}

func TestEveryNameHasItsOwnGaugeValueAndUnknownReadsAsOther(t *testing.T) {
	seen := map[float64]string{}
	for i, n := range Names {
		g := Gauge(n)
		if g != float64(i) {
			t.Errorf("Gauge(%q) = %v, want %d", n, g, i)
		}
		if prev, dup := seen[g]; dup {
			t.Errorf("%q and %q share gauge %v", prev, n, g)
		}
		seen[g] = n
	}
	if Gauge("the_aether") != Gauge(Other) {
		t.Error("an unlisted name must read as other")
	}
	// The game's own ids are what an operator will compare against.
	for id := int32(0); id < 3; id++ {
		if Gauge(Name(id)) != float64(id) {
			t.Errorf("gauge of id %d = %v", id, Gauge(Name(id)))
		}
	}
}
