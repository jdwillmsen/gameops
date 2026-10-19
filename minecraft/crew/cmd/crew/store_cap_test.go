package main

import "testing"

// The server never resends a chunk the client already had, so a store too
// small for the view loses blocks for good. Every column within the view
// distance, at full height, has to fit.
func TestStoreHoldsTheWholeView(t *testing.T) {
	for _, vd := range []uint8{1, 8, 16} {
		r := int(vd)
		columns := 0
		for x := -r; x <= r; x++ {
			for z := -r; z <= r; z++ {
				if x*x+z*z <= r*r {
					columns++
				}
			}
		}
		if got, need := storedSubChunks(vd), columns*sectionsPerColumn; got < need {
			t.Errorf("view %d: store holds %d sections, the view has %d", vd, got, need)
		}
	}
}
