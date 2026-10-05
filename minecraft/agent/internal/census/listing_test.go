package census

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func listingLines(t *testing.T, out string) (ListingHeader, []ListedEntity) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var header ListingHeader
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("header %q is not JSON: %v", lines[0], err)
	}
	var entities []ListedEntity
	for _, line := range lines[1:] {
		var e ListedEntity
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %q is not JSON: %v", line, err)
		}
		entities = append(entities, e)
	}
	return header, entities
}

func TestRenderListingKeepsOnlyTheRequestedTypes(t *testing.T) {
	entities := []Entity{
		{Identifier: "zombie", Dimension: Overworld, X: 10.5, Y: 64, Z: -3.25, Persistent: true},
		{Identifier: "cow", Dimension: Overworld, X: 1, Y: 70, Z: 1},
		{Identifier: "pillager", Dimension: Nether, X: 5, Y: 30, Z: 5, CustomName: "Bob"},
	}
	taken := time.Date(2026, 10, 5, 3, 16, 47, 0, time.UTC)

	out, err := RenderListing(entities, ScanStats{Orphaned: 7}, taken, KindSnapshot, []string{"zombie", "pillager"})
	if err != nil {
		t.Fatalf("RenderListing: %v", err)
	}
	header, listed := listingLines(t, out)

	if header.Entities != 2 || header.Orphaned != 7 || header.Source != KindSnapshot || header.WorldTakenAt != "2026-10-05T03:16:47Z" {
		t.Errorf("header = %+v", header)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d entities, want the zombie and the pillager\n%s", len(listed), out)
	}
	zombie, pillager := listed[0], listed[1]
	if zombie.Identifier != "zombie" || zombie.Dimension != "overworld" || zombie.X != 10.5 || zombie.Z != -3.25 || !zombie.Persistent {
		t.Errorf("zombie = %+v, want its saved position and persistence", zombie)
	}
	if pillager.Name != "Bob" || pillager.Dimension != "nether" {
		t.Errorf("pillager = %+v, want its name tag and dimension", pillager)
	}
}

func TestRenderListingWithNoTypesListsEverything(t *testing.T) {
	entities := []Entity{
		{Identifier: "zombie", Dimension: Overworld},
		{Identifier: "cow", Dimension: End},
	}
	out, err := RenderListing(entities, ScanStats{}, time.Time{}, KindArchive, nil)
	if err != nil {
		t.Fatalf("RenderListing: %v", err)
	}
	header, listed := listingLines(t, out)
	if header.Entities != 2 || len(listed) != 2 {
		t.Errorf("listed %d entities with a header claiming %d, want 2 and 2", len(listed), header.Entities)
	}
}

// Two listings of one world are compared with diff, and the scan hands back
// entities in database key order, which a compaction is free to change.
func TestRenderListingIsTheSameWhateverOrderTheScanReturned(t *testing.T) {
	a := Entity{Identifier: "zombie", Dimension: Overworld, X: 1, Y: 64, Z: 1, UniqueID: 2}
	b := Entity{Identifier: "zombie", Dimension: Overworld, X: 1, Y: 64, Z: 1, UniqueID: 1, Persistent: true}
	c := Entity{Identifier: "creeper", Dimension: Nether, X: -5, Y: 30, Z: 9}

	first, err := RenderListing([]Entity{a, b, c}, ScanStats{}, time.Time{}, KindArchive, nil)
	if err != nil {
		t.Fatalf("RenderListing: %v", err)
	}
	second, err := RenderListing([]Entity{c, b, a}, ScanStats{}, time.Time{}, KindArchive, nil)
	if err != nil {
		t.Fatalf("RenderListing: %v", err)
	}
	if first != second {
		t.Errorf("listing depends on input order\n--- first\n%s--- second\n%s", first, second)
	}
}

func TestRenderListingOmitsTheNameOfAnUnnamedEntity(t *testing.T) {
	out, err := RenderListing([]Entity{{Identifier: "zombie", Dimension: Overworld}}, ScanStats{}, time.Time{}, KindArchive, nil)
	if err != nil {
		t.Fatalf("RenderListing: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(strings.Split(out, "\n")[1]), &raw); err != nil {
		t.Fatalf("entity line is not JSON: %v", err)
	}
	if _, present := raw["name"]; present {
		t.Errorf("unnamed entity carries a name key: %v", raw)
	}
}
