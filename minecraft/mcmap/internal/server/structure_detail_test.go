package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

// The ids the world knows two players by, and what one village thinks of
// each of them.
const (
	steveInWorld, steveStanding = int64(-4294967301), int32(23)
	alexInWorld, alexStanding   = int64(-4294967302), int32(-17)
)

const villageDetailPath = "/api/structures/detail?dimension=overworld&kind=village&x=-2575&z=1830"

// detailed is the survey of surveyed with what the save holds in each of
// the overworld's structures.
func detailed() *fakeStructures {
	f := surveyed()
	elders := 2
	layer := f.survey.Layers[chunks.Overworld]
	layer.Details = []*structures.Detail{
		{MobsTotal: 5, Mobs: []structures.MobCount{{Kind: "guardian", Count: 3}, {Kind: "elder_guardian", Count: 2}}, Elders: &elders},
		{
			MobsTotal: 1, Mobs: []structures.MobCount{{Kind: "villager_v2", Count: 1}},
			Named: []structures.NamedMob{{Kind: "villager_v2", Name: "<img src=x onerror=alert(1)>", Profession: "farmer", Level: 2}},
			Village: &structures.VillageDetail{
				Professions: []structures.Profession{{Profession: "farmer", Count: 1, Levels: [5]int{0, 1, 0, 0, 0}}},
				JobSites:    []structures.JobSites{{Profession: "farmer", Count: 1}},
				Met:         2,
				Standings:   []structures.Standing{{Player: steveInWorld, Value: steveStanding}, {Player: alexInWorld, Value: alexStanding}},
			},
		},
		{},
	}
	f.survey.Layers[chunks.Overworld] = layer
	return f
}

type detailResponse struct {
	At     *time.Time `json:"at"`
	Kind   string     `json:"kind"`
	MinX   *int32     `json:"minX"`
	Detail *struct {
		MobsTotal int `json:"mobsTotal"`
		Elders    *int
		Village   *struct {
			Met int `json:"met"`
		}
	} `json:"detail"`
	Standing *struct {
		State string
		Value *int32
	} `json:"standing"`
}

func detailOf(t *testing.T, s *Server, path string, c *http.Cookie) (detailResponse, string) {
	t.Helper()
	rec := do(s.Handler(), "GET", path, "", []*http.Cookie{c})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the answer is one player's", cc)
	}
	var got detailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got, rec.Body.String()
}

// sample numbers the live samples the tests feed, each later than the last.
var sample int

// inGame has the agent report who is online and the game say where they
// are, which between them join a session to the world's own id for it.
func inGame(t *testing.T, s *Server, players map[auth.Identity]int64) {
	t.Helper()
	var reported, items []string
	for id, inWorld := range players {
		reported = append(reported, fmt.Sprintf(`{"xuid":%q,"gamertag":%q}`, id.XUID, id.Gamertag))
		items = append(items, fmt.Sprintf(`{"i":"%d","n":%q,"x":1,"y":64,"z":1,"r":0}`, inWorld, id.Gamertag))
	}
	report(t, s, `{"players":[`+strings.Join(reported, ",")+`]}`)
	now := time.Now()
	sample++
	for kind, list := range map[string]string{"players": strings.Join(items, ","), "mobs": ""} {
		s.Live.Ingest(live.RawRecord{At: now, Data: fmt.Sprintf(`{"gen":%d,"dim":"overworld","kind":%q,"part":0,"parts":1,"more":0,"items":[%s]}`, sample, kind, list)}, now)
	}
	s.Live.Flush(now)
}

func TestStructureDetail_NeedsASession(t *testing.T) {
	s := withIcons(t)
	s.Structures = detailed()
	for name, cookies := range map[string][]*http.Cookie{"no session": nil, "a forged session": {{Name: "__Host-mcmap_session", Value: "e30.nope"}}} {
		rec := do(s.Handler(), "GET", villageDetailPath, "", cookies)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "village") {
			t.Errorf("%s = %d %s, want 401 and nothing of the village", name, rec.Code, rec.Body)
		}
	}
}

func TestStructureDetail_SaysWhatTheSaveHoldsInOneStructure(t *testing.T) {
	s := withIcons(t)
	s.Structures = detailed()
	got, body := detailOf(t, s, "/api/structures/detail?dimension=overworld&kind=monument&x=4023&z=6023", session(s, steve))
	if got.Kind != "monument" || got.MinX == nil || *got.MinX != 4000 || got.At == nil || !got.At.Equal(renderedAt) {
		t.Errorf("answer = %s", body)
	}
	if got.Detail == nil || got.Detail.MobsTotal != 5 || got.Detail.Elders == nil || *got.Detail.Elders != 2 || got.Standing != nil {
		t.Errorf("detail = %s", body)
	}
	// A name tag is a player's text, and goes out as the text it is.
	_, body = detailOf(t, s, villageDetailPath, session(s, steve))
	if escaped := `\u` + `003cimg src=x onerror=alert(1)\u` + `003e`; !strings.Contains(body, escaped) {
		t.Errorf("the name tag is not in the answer as text: %s", body)
	}
	for _, secret := range []string{fmt.Sprint(secretSeed), "seed"} {
		if strings.Contains(strings.ToLower(body), secret) {
			t.Errorf("the answer carries %q: %s", secret, body)
		}
	}
}

func TestStructureDetail_IsOfOneTheListHolds(t *testing.T) {
	s := withIcons(t)
	s.Structures = detailed()
	c := []*http.Cookie{session(s, steve)}
	for path, want := range map[string]int{
		"/api/structures/detail?dimension=overworld&kind=monument&x=4023&z=6024":     http.StatusNotFound,
		"/api/structures/detail?dimension=overworld&kind=fortress&x=4023&z=6023":     http.StatusNotFound,
		"/api/structures/detail?dimension=nether&kind=monument&x=4023&z=6023":        http.StatusNotFound,
		"/api/structures/detail?dimension=moon&kind=monument&x=4023&z=6023":          http.StatusBadRequest,
		"/api/structures/detail?dimension=overworld&kind=monument&x=55":              http.StatusBadRequest,
		"/api/structures/detail?dimension=overworld&kind=monument&x=5e1&z=1":         http.StatusBadRequest,
		"/api/structures/detail?dimension=overworld&x=4023&z=6023":                   http.StatusBadRequest,
		"/api/structures/detail?dimension=overworld&kind=monument&x=99999999999&z=1": http.StatusBadRequest,
	} {
		if rec := do(s.Handler(), "GET", path, "", c); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
	// A survey that could not work out what its structures hold still
	// answers for the structure, without it.
	bare := surveyed()
	s.Structures = bare
	if got, body := detailOf(t, s, villageDetailPath, c[0]); got.Kind != "village" || got.Detail != nil || got.Standing != nil {
		t.Errorf("without details: %s", body)
	}
	s.Structures = &fakeStructures{}
	if rec := do(s.Handler(), "GET", villageDetailPath, "", c); rec.Code != http.StatusNotFound {
		t.Errorf("before any survey = %d, want 404", rec.Code)
	}
}

// seenAt sets the clock the server remembers players by.
func seenAt(s *Server, at time.Time) {
	s.clock = func() time.Time { return at }
}

// stateOf is how the village's standing for a player is answered.
func stateOf(t *testing.T, s *Server, id auth.Identity) string {
	t.Helper()
	got, body := detailOf(t, s, villageDetailPath, session(s, id))
	if got.Standing == nil || (got.Standing.Value != nil) != (got.Standing.State == "known") {
		t.Fatalf("standing = %s", body)
	}
	return got.Standing.State
}

// What a village thinks of a player is that player's to see and nobody
// else's. Whose it is comes from the session: nothing a request says can
// name another player.
func TestStructureDetail_AStandingGoesOnlyToThePlayerItIsOf(t *testing.T) {
	s := withIcons(t)
	s.Structures = detailed()
	// Both were in the game before the snapshot the standings are from.
	seenAt(s, renderedAt.Add(-time.Minute))
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld, alex: alexInWorld})

	for id, want := range map[auth.Identity]int32{steve: steveStanding, alex: alexStanding} {
		for _, path := range []string{
			villageDetailPath,
			// Naming the other player changes nothing.
			villageDetailPath + "&xuid=" + alex.XUID + "&player=" + fmt.Sprint(alexInWorld) + "&id=" + fmt.Sprint(steveInWorld),
		} {
			got, body := detailOf(t, s, path, session(s, id))
			if got.Standing == nil || got.Standing.State != "known" || got.Standing.Value == nil || *got.Standing.Value != want {
				t.Errorf("%s asking %s: standing = %s, want %d", id.Gamertag, path, body, want)
			}
			if got.Detail == nil || got.Detail.Village == nil || got.Detail.Village.Met != 2 {
				t.Errorf("%s: village = %s", id.Gamertag, body)
			}
			other, theirs := fmt.Sprint(alexInWorld), fmt.Sprint(alexStanding)
			if id == alex {
				other, theirs = fmt.Sprint(steveInWorld), fmt.Sprint(steveStanding)
			}
			for _, secret := range []string{other, theirs, fmt.Sprint(steveInWorld), fmt.Sprint(alexInWorld), steve.XUID, alex.XUID} {
				if strings.Contains(body, secret) {
					t.Errorf("%s was sent %q: %s", id.Gamertag, secret, body)
				}
			}
		}
	}
}

func TestStructureDetail_AStandingIsNotGuessedAt(t *testing.T) {
	stranger := auth.Identity{XUID: "2535400000000003", Gamertag: "Made Up Stranger"}
	s := withIcons(t)
	s.Structures = detailed()
	seenAt(s, renderedAt.Add(-time.Minute))
	// Nobody has been seen in the game: nothing says which record is whose.
	if got := stateOf(t, s, steve); got != "unknown" {
		t.Errorf("before anybody is online: %q, want unknown", got)
	}
	// Two players under one name cannot be told apart by it.
	twin := auth.Identity{XUID: alex.XUID, Gamertag: steve.Gamertag}
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld, twin: alexInWorld})
	if got := stateOf(t, s, steve); got != "unknown" {
		t.Errorf("with a namesake online: %q, want unknown", got)
	}
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld, stranger: -4294967399})
	if got := stateOf(t, s, steve); got != "known" {
		t.Errorf("online: %q, want known", got)
	}
	// One the village has never met has no standing there, which is not
	// the same as one of nought.
	if got := stateOf(t, s, stranger); got != "none" {
		t.Errorf("a player the village never met: %q, want none", got)
	}
	// Somebody who has just left the game is still who they were.
	inGame(t, s, map[auth.Identity]int64{stranger: -4294967399})
	if got := stateOf(t, s, steve); got != "known" {
		t.Errorf("after leaving the game: %q, want known", got)
	}
	if got := stateOf(t, s, alex); got != "unknown" {
		t.Errorf("never seen in the game: %q, want unknown", got)
	}
	// Without a login there is nobody to be.
	open, _ := fixture(t)
	open.Structures = detailed()
	rec := do(open.Handler(), "GET", villageDetailPath, "", nil)
	var got detailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Standing == nil || got.Standing.State != "unknown" {
		t.Errorf("with no login: %d %s", rec.Code, rec.Body)
	}
}

// Who a player is in the world is believed only for a while after the game
// last showed them, and again for as long as it goes on showing them.
func TestStructureDetail_APlayersIDIsForgottenUnlessTheGameGoesOnShowingThem(t *testing.T) {
	s := withIcons(t)
	s.Structures = detailed()
	seen := renderedAt.Add(-2 * time.Hour)
	seenAt(s, seen)
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld})
	if got := stateOf(t, s, steve); got != "known" {
		t.Fatalf("seen in the game: %q, want known", got)
	}
	// Out of the game from here on.
	inGame(t, s, nil)
	seenAt(s, seen.Add(playerMemory))
	if got := stateOf(t, s, steve); got != "known" {
		t.Errorf("at the end of the memory: %q, want known", got)
	}
	seenAt(s, seen.Add(playerMemory+time.Second))
	if got := stateOf(t, s, steve); got != "unknown" {
		t.Errorf("past the memory: %q, want unknown", got)
	}
	// Asking again does not bring it back, and nor does the clock going
	// back: it was forgotten, not hidden.
	seenAt(s, seen)
	if got := stateOf(t, s, steve); got != "unknown" {
		t.Errorf("once forgotten: %q, want unknown", got)
	}

	// Seen again and again, the memory runs from the last time.
	seenAt(s, seen)
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld})
	stateOf(t, s, steve)
	seenAt(s, seen.Add(playerMemory-time.Minute))
	stateOf(t, s, steve)
	inGame(t, s, nil)
	seenAt(s, seen.Add(2*playerMemory-2*time.Minute))
	if got := stateOf(t, s, steve); got != "known" {
		t.Errorf("within the memory of the last time seen: %q, want known", got)
	}
}

// An id is one world's. In a world put in place of another, or put back to
// an earlier copy of itself, the same id may be somebody else's.
func TestStructureDetail_APlayersIDIsForgottenWhenTheWorldIsAnother(t *testing.T) {
	for name, change := range map[string]func(*structures.Survey){
		"another seed":             func(v *structures.Survey) { v.Level.Seed++ },
		"an earlier copy":          func(v *structures.Survey) { v.Level.Tick-- },
		"a level that is not read": func(v *structures.Survey) { v.HasLevel = false },
	} {
		t.Run(name, func(t *testing.T) {
			s := withIcons(t)
			source := detailed()
			source.survey.Level.Tick, source.survey.Level.TickKnown = 5000, true
			s.Structures = source
			seenAt(s, renderedAt.Add(-time.Minute))
			inGame(t, s, map[auth.Identity]int64{steve: steveInWorld})
			if got := stateOf(t, s, steve); got != "known" {
				t.Fatalf("seen in the game: %q, want known", got)
			}
			inGame(t, s, nil)
			// The same world a snapshot later keeps what it knows.
			source.survey.Level.Tick += 18_000
			if got := stateOf(t, s, steve); got != "known" {
				t.Fatalf("a snapshot later: %q, want known", got)
			}
			change(&source.survey)
			if got := stateOf(t, s, steve); got != "unknown" {
				t.Errorf("in another world: %q, want unknown", got)
			}
		})
	}
}

// A snapshot from before a player was first seen may be of the world
// before this one, and nothing can tell until the next is read. Their
// standing waits for a snapshot taken after they were seen.
func TestStructureDetail_AStandingWaitsForASnapshotTakenAfterThePlayerWasSeen(t *testing.T) {
	s := withIcons(t)
	source := detailed()
	s.Structures = source
	seenAt(s, renderedAt.Add(time.Minute))
	inGame(t, s, map[auth.Identity]int64{steve: steveInWorld})
	got, body := detailOf(t, s, villageDetailPath, session(s, steve))
	if got.Standing == nil || got.Standing.State != "pending" || got.Standing.Value != nil || strings.Contains(body, fmt.Sprint(steveStanding)) {
		t.Fatalf("seen only since the snapshot: %s, want pending and no value", body)
	}
	source.survey.At = renderedAt.Add(15 * time.Minute)
	seenAt(s, renderedAt.Add(16*time.Minute))
	if got := stateOf(t, s, steve); got != "known" {
		t.Errorf("after the next snapshot: %q, want known", got)
	}
	// A player the game shows under another id is somebody new to it.
	inGame(t, s, map[auth.Identity]int64{steve: alexInWorld})
	if got := stateOf(t, s, steve); got != "pending" {
		t.Errorf("shown under another id: %q, want pending", got)
	}
}
