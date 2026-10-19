package server

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/roster"
)

type rosterAnswer struct {
	Players []struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Earlier   []string  `json:"earlier"`
		Dimension string    `json:"dimension"`
		LastSeen  time.Time `json:"lastSeen"`
		Tags      []string  `json:"tags"`
	} `json:"players"`
}

// withRoster is a server whose roster has seen three players, of whom the
// live feed has one online now and one not yet written to the roster.
func withRoster(t *testing.T) (*Server, *roster.Roster) {
	t.Helper()
	s := withLogin(t)
	s.Live = live.New(10*time.Second, 1000, nil)
	r := &roster.Roster{}
	long := time.Now().Add(-72 * time.Hour)
	r.Seen("-1", "Offline", long)
	r.Seen("2", "Recent", time.Now().Add(-time.Hour))
	r.Seen("3", "Renamed", long)
	r.Seen("3", "Nowonline", long.Add(time.Hour))
	s.Roster = r
	now := time.Now()
	s.Live.Ingest(live.RawRecord{At: now, Data: `{"gen":1,"dim":"nether","kind":"players","part":0,"parts":1,"more":0,"items":[` +
		`{"i":"3","n":"Nowonline","x":1,"y":64,"z":1},{"i":"4","n":"Fresh","x":2,"y":64,"z":2}]}`}, now)
	s.Live.Flush(now)
	return s, r
}

func askRoster(t *testing.T, s *Server, cookie *http.Cookie) rosterAnswer {
	t.Helper()
	rec := do(s.Handler(), "GET", "/api/players", "", []*http.Cookie{cookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/players = %d %s", rec.Code, rec.Body)
	}
	return decodeBody[rosterAnswer](t, rec)
}

func TestRosterListsOfflinePlayersAfterTheOnlineOnes(t *testing.T) {
	s, _ := withRoster(t)
	got := askRoster(t, s, session(s, auth.Identity{XUID: "100", Gamertag: "Recent"}))
	var order []string
	for _, p := range got.Players {
		order = append(order, p.Name)
	}
	if want := []string{"Fresh", "Nowonline", "Recent", "Offline"}; !slices.Equal(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	online := got.Players[1]
	if online.Dimension != "nether" || !slices.Contains(online.Tags, "online") || !slices.Equal(online.Earlier, []string{"Renamed"}) {
		t.Errorf("online player = %+v", online)
	}
	offline := got.Players[3]
	if offline.Dimension != "" || slices.Contains(offline.Tags, "online") || time.Since(offline.LastSeen) < 71*time.Hour {
		t.Errorf("offline player = %+v", offline)
	}
	if !slices.Contains(got.Players[2].Tags, "you") {
		t.Errorf("the viewer is not tagged as themselves: %+v", got.Players[2])
	}
}

func TestRosterTagsOperatorsLinkedByALogin(t *testing.T) {
	s, r := withRoster(t)
	s.Admin, s.Operators = true, fakeOperators{"200": true}
	r.Link("offline", "200")
	got := askRoster(t, s, session(s, auth.Identity{XUID: "100", Gamertag: "Someone"}))
	for _, p := range got.Players {
		if op := slices.Contains(p.Tags, "operator"); op != (p.Name == "Offline") {
			t.Errorf("%s tagged operator = %v", p.Name, op)
		}
	}
}

func TestRosterTagsOnlyTheLatestHolderOfTheViewersGamertag(t *testing.T) {
	s, r := withRoster(t)
	r.Seen("-1", "Recent", time.Now().Add(-2*time.Hour))
	got := askRoster(t, s, session(s, auth.Identity{XUID: "100", Gamertag: "Recent"}))
	for _, p := range got.Players {
		if you := slices.Contains(p.Tags, "you"); you != (p.ID == "2") {
			t.Errorf("%s tagged you = %v", p.ID, you)
		}
	}
}

func TestRosterIsBehindTheLogin(t *testing.T) {
	s, _ := withRoster(t)
	if rec := do(s.Handler(), "GET", "/api/players", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session = %d", rec.Code)
	}
}

func TestRosterIsLeftOutWithoutOne(t *testing.T) {
	s, _ := withRoster(t)
	s.Roster = nil
	rec := do(s.Handler(), "GET", "/api/players", "", []*http.Cookie{session(s, auth.Identity{XUID: "1", Gamertag: "A"})})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestAClaimLinksThePlayerToTheRoster(t *testing.T) {
	s, r := withRoster(t)
	code, _ := s.Codes.Start("")
	rec := do(s.InternalHandler(), "POST", "/internal/v1/claims", `{"code":"`+code+`","xuid":"300","gamertag":"Recent"}`, nil, "Authorization", "Bearer "+internalToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("claim = %d %s", rec.Code, rec.Body)
	}
	i := slices.IndexFunc(r.List(), func(e roster.Entry) bool { return e.Name == "Recent" })
	if i < 0 || r.List()[i].XUID != "300" {
		t.Fatalf("the claim's XUID was not linked: %+v", r.List())
	}
}
