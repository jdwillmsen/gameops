package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

// A server with a live layer holding zombies, markers holding a named
// happy ghast, and whatever the fetch gives. Nothing here is the game's
// own text or artwork.
func withArt(t *testing.T, fetch func(context.Context) (icons.Set, error)) (*Server, *icons.Mobs) {
	t.Helper()
	s := withLive(t)
	feed(s, 1, "overworld", 0, 2)
	store := markers.NewStore()
	store.Set(time.Now(), markers.World{chunks.End: {Mobs: []markers.Marker{{Kind: "happy_ghast", Name: "Casper"}, {Kind: "villager_v2", Name: "Tesch"}}}})
	s.Markers = store
	mobs := mobIcons(t, fetch)
	s.MobIcons, s.Art, s.Heads = mobs, mobs, &icons.Heads{}
	return s, mobs
}

func fetched(t *testing.T) icons.Set {
	return icons.Set{
		Mobs: map[string][]byte{"cow": picture(t, 16, red)},
		Pictures: map[string][]byte{
			"container/chest": picture(t, 16, red), "bed/red": picture(t, 16, blue),
			"shulker/undyed": picture(t, 16, red), "structure/monument": picture(t, 16, blue), "marker/waypoint": picture(t, 16, red),
		},
		Lang: map[string]string{
			"entity.cow.name": "Synthetic Cow", "entity.villager_v2.name": "Synthetic Villager",
			"tile.chest.name": "Synthetic Chest", "feature.monument": "Synthetic Monument",
		},
		Entities: []string{"cow", "pig", "villager_v2"},
	}
}

func withFetchedArt(t *testing.T) *Server {
	t.Helper()
	s, mobs := withArt(t, func(context.Context) (icons.Set, error) { return fetched(t), nil })
	mobs.Run(t.Context())
	return s
}

func namesOf(t *testing.T, s *Server, c *http.Cookie) namesJSON {
	t.Helper()
	rec := do(s.Handler(), "GET", "/api/names", "", []*http.Cookie{c})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/names = %d %s", rec.Code, rec.Body)
	}
	var out namesJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNamesAndPictures_NeedASession(t *testing.T) {
	s := withFetchedArt(t)
	h := s.Handler()
	paths := []string{"/api/names", "/api/icons/picture/container/chest?v=x", "/api/icons/picture/structure/monument", "/api/icons"}
	for _, path := range paths {
		rec := do(h, "GET", path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, rec.Code)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("PNG")) || strings.Contains(rec.Body.String(), "Synthetic") || strings.Contains(rec.Body.String(), "happy_ghast") {
			t.Errorf("GET %s without a session leaked %q", path, rec.Body)
		}
	}
	c := []*http.Cookie{session(s, alex)}
	for _, path := range paths {
		if rec := do(h, "GET", path, "", c); rec.Code != http.StatusOK {
			t.Errorf("GET %s with a session = %d, want 200", path, rec.Code)
		}
	}
}

func TestPictures_AreListedOnceAndKeptUntilTheirVersionChanges(t *testing.T) {
	s := withFetchedArt(t)
	c := []*http.Cookie{session(s, alex)}
	list := listing(t, s, c[0])
	want := []string{"bed/red", "container/chest", "marker/waypoint", "shulker/undyed", "structure/monument"}
	if list.Pictures.Version == "" || !slices.Equal(list.Pictures.Keys, want) {
		t.Fatalf("pictures listed as %+v, want %v", list.Pictures, want)
	}
	h := s.Handler()
	for _, key := range list.Pictures.Keys {
		path := "/api/icons/picture/" + key + "?v="
		rec := do(h, "GET", path+list.Pictures.Version, "", c)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if _, err := png.Decode(rec.Body); err != nil {
			t.Errorf("GET %s is not a PNG: %v", path, err)
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
			t.Errorf("GET %s at its own version is cached as %q", path, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s may be sniffed: %q", path, got)
		}
		stale := do(h, "GET", path+"an-older-one", "", c)
		if got := stale.Header().Get("Cache-Control"); stale.Code != http.StatusOK || got != "private, no-cache" {
			t.Errorf("GET %s at another version = %d, cached as %q", path, stale.Code, got)
		}
	}
	// What the listing does not hold is not there to ask for.
	for _, path := range []string{"/api/icons/picture/bed/blue", "/api/icons/picture/container/unicorn", "/api/icons/picture/bed", "/api/icons/picture/bed/red/extra", "/api/icons/picture/..%2f..%2fetc/passwd"} {
		rec := do(h, "GET", path+"?v="+list.Pictures.Version, "", c)
		if rec.Code != http.StatusNotFound || bytes.Contains(rec.Body.Bytes(), []byte("PNG")) {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestNames_AreTheLanguageFilesWhereItHasOneAndTidiedIDsWhereNot(t *testing.T) {
	s := withFetchedArt(t)
	c := session(s, alex)
	names := namesOf(t, s, c)
	for got, want := range map[string]string{
		names.Entities["cow"]:         "Synthetic Cow",
		names.Entities["villager_v2"]: "Synthetic Villager",
		names.Entities["pig"]:         "Pig",         // defined by the samples, not named by the file
		names.Entities["zombie"]:      "Zombie",      // only on the live layer
		names.Entities["happy_ghast"]: "Happy Ghast", // only among the markers
		names.Containers["chest"]:     "Synthetic Chest",
		names.Containers["shulker"]:   "Shulker Box",
		names.Beds["light_gray"]:      "Light Gray Bed",
		names.Beds["default"]:         "Bed",
		names.Shulkers["undyed"]:      "Shulker Box",
		names.Structures["monument"]:  "Synthetic Monument",
		names.Structures["witch_hut"]: "Witch Hut",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}

	// One request tells the page whether to ask for the names again.
	if list := listing(t, s, c); names.Version == "" || list.Names.Version != names.Version {
		t.Errorf("/api/icons gives the names version %q, /api/names %q", list.Names.Version, names.Version)
	}
	h := s.Handler()
	first := do(h, "GET", "/api/names", "", []*http.Cookie{c})
	if got := first.Header().Get("Cache-Control"); got != "private, no-cache" || first.Header().Get("ETag") != `"`+names.Version+`"` {
		t.Errorf("names are cached as %q under the tag %s", got, first.Header().Get("ETag"))
	}
	again := do(h, "GET", "/api/names", "", []*http.Cookie{c}, "If-None-Match", first.Header().Get("ETag"))
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Errorf("an unchanged table = %d with %d bytes, want an empty 304", again.Code, again.Body.Len())
	}

	// A type of mob that turns up is named from then on, and the version
	// says the table changed.
	now := time.Now()
	s.Live.Ingest(liveMobs(now, "nether", "zombie_villager_v2"), now)
	after := namesOf(t, s, c)
	if after.Entities["zombie_villager_v2"] != "Zombie Villager" || after.Version == names.Version {
		t.Errorf("a new type is named %q at version %q (was %q)", after.Entities["zombie_villager_v2"], after.Version, names.Version)
	}
}

// The source being out of reach is the ordinary case on a first start with
// no route out. The map is then the map it was before there were names or
// pictures: every route answers, no picture is listed so the page keeps its
// rings and letters, and every name is its id tidied into words.
func TestNamesAndPictures_WithTheSourceUnreachableTheMapWorksOnTidiedIDs(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	asked := make(chan struct{}, 1)
	s, mobs := withArt(t, func(ctx context.Context) (icons.Set, error) {
		select {
		case asked <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return icons.Set{}, errors.New("dial tcp: i/o timeout")
	})
	done := make(chan struct{})
	go func() { mobs.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	<-asked

	c := []*http.Cookie{session(s, steve)}
	h := s.Handler()
	for _, path := range []string{"/api/map", "/tiles/overworld/0/37/-4.webp", "/api/me", "/", "/api/markers?dimension=end", "/api/icons", "/api/names"} {
		if rec := do(h, "GET", path, "", c); rec.Code != http.StatusOK {
			t.Errorf("GET %s with the source unreachable = %d, want 200", path, rec.Code)
		}
	}
	list := listing(t, s, c[0])
	if list.Pictures.Version != "" || len(list.Pictures.Keys) != 0 || list.Pictures.Keys == nil {
		t.Errorf("pictures are listed with none fetched: %+v", list.Pictures)
	}
	if rec := do(h, "GET", "/api/icons/picture/container/chest", "", c); rec.Code != http.StatusNotFound {
		t.Errorf("a picture that was never fetched = %d, want 404", rec.Code)
	}
	names := namesOf(t, s, c[0])
	for id, want := range map[string]string{"zombie": "Zombie", "happy_ghast": "Happy Ghast", "villager_v2": "Villager"} {
		if got := names.Entities[id]; got != want {
			t.Errorf("with the source unreachable %s is named %q, want %q", id, got, want)
		}
	}
	if names.Containers["chest"] != "Chest" || names.Beds["red"] != "Red Bed" || names.Shulkers["light_blue"] != "Light Blue Shulker Box" || names.Structures["witch_hut"] != "Witch Hut" {
		t.Errorf("fallback names = %+v", names.Table)
	}
	if list.Names.Version != names.Version || names.Version == "" {
		t.Errorf("names version %q listed as %q", names.Version, list.Names.Version)
	}
}

// Nothing the names endpoint serves is an id, whatever the world reports
// and whether or not the language file was read.
func TestNames_NeverServeARawID(t *testing.T) {
	for name, set := range map[string]icons.Set{"fetched": fetched(t), "with no language file": {Mobs: map[string][]byte{"cow": picture(t, 16, red)}}} {
		s, mobs := withArt(t, func(context.Context) (icons.Set, error) { return set, nil })
		mobs.Run(t.Context())
		now := time.Now()
		s.Live.Ingest(liveMobs(now, "overworld", "villager_v2", "zombie_villager_v2", "xp_orb", "armor_stand", "ender_dragon", "custom:thing_v3"), now)
		names := namesOf(t, s, session(s, alex))
		if len(names.Entities) < 6 {
			t.Errorf("%s: only %d entities named: %v", name, len(names.Entities), names.Entities)
		}
		for group, held := range map[string]map[string]string{"entities": names.Entities, "containers": names.Containers, "beds": names.Beds, "shulkers": names.Shulkers, "structures": names.Structures} {
			for id, got := range held {
				runes := []rune(got)
				if got == "" || strings.ContainsAny(got, "_:") || !unicode.IsUpper(runes[0]) || (got == id && group != "entities") || len(runes) > icons.MaxNameLength {
					t.Errorf("%s: %s %q is named %q", name, group, id, got)
				}
			}
		}
	}
}

// The markers and the structure survey decide what kinds and colours
// there are; each must have a name and a picture to be asked for.
func TestNamesAndPictures_CoverEveryKindTheMapCanShow(t *testing.T) {
	s := withFetchedArt(t)
	names := namesOf(t, s, session(s, alex))
	for _, kind := range structures.Kinds {
		if names.Structures[string(kind)] == "" || !slices.Contains(icons.StructureKinds, string(kind)) {
			t.Errorf("structure kind %s has no name or no picture", kind)
		}
	}
	for _, colour := range markers.Colours {
		if names.Beds[colour] == "" || names.Shulkers[colour] == "" {
			t.Errorf("colour %s has no bed or shulker box name", colour)
		}
	}
	if names.Shulkers[markers.Undyed] == "" {
		t.Error("an undyed shulker box has no name")
	}
}

func TestNamesAndPictures_RoutesAreAbsentWithIconsOff(t *testing.T) {
	s := withLive(t)
	c := []*http.Cookie{session(s, steve)}
	for _, path := range []string{"/api/names", "/api/icons/picture/container/chest"} {
		if rec := do(s.Handler(), "GET", path, "", c); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with icons off = %d, want 404", path, rec.Code)
		}
	}
}
