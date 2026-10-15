package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/players"
)

type playerAnswer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Saved struct {
		State  string          `json:"state"`
		AsOf   *time.Time      `json:"asOf"`
		Record *players.Record `json:"record"`
	} `json:"saved"`
	Counters struct {
		State         string           `json:"state"`
		Since         *time.Time       `json:"since"`
		Kills         map[string]int64 `json:"kills"`
		Deaths        map[string]int64 `json:"deaths"`
		Broken        int64            `json:"broken"`
		Placed        int64            `json:"placed"`
		Joins         int64            `json:"joins"`
		OnlineSeconds int64            `json:"onlineSeconds"`
	} `json:"counters"`
	Live *struct {
		Health    *float64 `json:"health"`
		MaxHealth *float64 `json:"maxHealth"`
		Level     *int     `json:"level"`
		Dimension string   `json:"dimension"`
	} `json:"live"`
	Trails struct {
		State string `json:"state"`
		Stats *struct {
			Name          string `json:"name"`
			OnlineSeconds int    `json:"onlineSeconds"`
			Blocks        int    `json:"blocks"`
			LastDimension string `json:"lastDimension"`
			Days          []struct {
				Date string `json:"date"`
			} `json:"days"`
		} `json:"stats"`
	} `json:"trails"`
}

var savedAt = time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC)

// withPlayers is a server whose live feed has Dotablaze online as id -9 and
// whose save holds a record for id -9 and none for the others.
func withPlayers(t *testing.T) *Server {
	t.Helper()
	s, _ := withTrails(t)
	s.Live = live.New(10*time.Second, 1000, nil)
	now := time.Now()
	s.Live.Ingest(live.RawRecord{At: now, Data: `{"gen":1,"dim":"overworld","kind":"players","part":0,"parts":1,"more":0,"items":[{"i":"-9","n":"Dotablaze","x":1,"y":64,"z":1}]}`}, now)
	s.Live.Flush(now)
	lvl := 7
	store := &players.Store{}
	store.Set(savedAt, map[int64]players.Record{-9: {Level: &lvl, Armor: []players.Item{}, Inventory: []players.Item{}, EnderChest: []players.Item{}}})
	s.Players = store
	return s
}

func askPlayer(t *testing.T, s *Server, path string) playerAnswer {
	t.Helper()
	rec := get(t, s, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	return decodeBody[playerAnswer](t, rec)
}

func TestPlayerDetailsJoinTheSaveAndTheTrailsByTheLiveId(t *testing.T) {
	s := withPlayers(t)
	got := askPlayer(t, s, "/api/players/-9")
	if got.Name != "Dotablaze" || got.Saved.State != "known" || got.Saved.Record == nil || *got.Saved.Record.Level != 7 {
		t.Fatalf("saved = %+v", got)
	}
	if got.Saved.AsOf == nil || !got.Saved.AsOf.Equal(savedAt) {
		t.Errorf("asOf = %v, want when the snapshot was taken", got.Saved.AsOf)
	}
	if got.Trails.State != "known" || got.Trails.Stats == nil || got.Trails.Stats.Name != "Dotablaze" ||
		got.Trails.Stats.LastDimension != "overworld" || got.Trails.Stats.Blocks != 50 || len(got.Trails.Stats.Days) != 1 {
		t.Errorf("trails = %+v %+v", got.Trails, got.Trails.Stats)
	}
}

func TestPlayerDetailsAcceptTheUnsignedReadingOfAnId(t *testing.T) {
	s := withPlayers(t)
	if got := askPlayer(t, s, "/api/players/18446744073709551607"); got.Saved.State != "known" {
		t.Errorf("saved = %+v", got.Saved)
	}
}

func TestPlayerDetailsWithNoRecordYetStillHaveTheTrails(t *testing.T) {
	s := withPlayers(t)
	got := askPlayer(t, s, "/api/players/123?name=steve")
	if got.Saved.State != "none" || got.Saved.Record != nil || got.Saved.AsOf == nil {
		t.Errorf("saved = %+v, want an explicit none that says when the save was read", got.Saved)
	}
	if got.Trails.State != "known" || got.Trails.Stats.Name != "Steve" {
		t.Errorf("trails = %+v", got.Trails)
	}
}

// A name asked for must not override the name the live feed has for the id.
func TestPlayerDetailsTrustTheLiveFeedOverTheNameAsked(t *testing.T) {
	s := withPlayers(t)
	if got := askPlayer(t, s, "/api/players/-9?name=steve"); got.Name != "Dotablaze" {
		t.Errorf("name = %q", got.Name)
	}
}

func TestPlayerDetailsBeforeTheFirstSnapshotAreSaidToBePending(t *testing.T) {
	s := withPlayers(t)
	s.Players = &players.Store{}
	got := askPlayer(t, s, "/api/players/-9")
	if got.Saved.State != "pending" || got.Saved.AsOf != nil {
		t.Errorf("saved = %+v", got.Saved)
	}
}

func TestPlayerDetailsWithoutTrailsSayTheyAreOff(t *testing.T) {
	s := withPlayers(t)
	s.Trails = nil
	if got := askPlayer(t, s, "/api/players/-9"); got.Trails.State != "off" || got.Saved.State != "known" {
		t.Errorf("answer = %+v", got)
	}
}

func TestPlayerDetailsRefuseAnIdThatIsNotANumber(t *testing.T) {
	s := withPlayers(t)
	for _, id := range []string{"abc", "1.5", "1e9", "123456789012345678901", "%20"} {
		if rec := get(t, s, "/api/players/"+id); rec.Code != http.StatusBadRequest {
			t.Errorf("id %q = %d, want 400", id, rec.Code)
		}
	}
}

func TestPlayerDetailsAreBehindTheLogin(t *testing.T) {
	s := withPlayers(t)
	s.Sessions = withLogin(t).Sessions
	if rec := get(t, s, "/api/players/-9"); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session = %d", rec.Code)
	}
}

func TestPlayerDetailsAreLeftOutWithNoRecords(t *testing.T) {
	s := withPlayers(t)
	s.Players = nil
	if rec := get(t, s, "/api/players/-9"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d", rec.Code)
	}
}

func withCounters(t *testing.T, s *Server) {
	t.Helper()
	s.Live.Counters = &live.Counters{}
	now := time.Now()
	s.Live.Ingest(live.RawRecord{At: now, Data: `{"gen":2,"kind":"counts","boot":5,"seq":1,"items":[{"i":"-9","k":{"zombie":2},"d":{"fall":1},"b":4,"p":1,"j":1,"t":60}]}`}, now)
}

func TestPlayerDetailsCarryTheCounters(t *testing.T) {
	s := withPlayers(t)
	withCounters(t, s)
	got := askPlayer(t, s, "/api/players/-9")
	c := got.Counters
	if c.State != "known" || c.Since == nil || c.Kills["zombie"] != 2 || c.Deaths["fall"] != 1 || c.Broken != 4 || c.Placed != 1 || c.Joins != 1 || c.OnlineSeconds != 60 {
		t.Errorf("counters = %+v", c)
	}
	// The same player, named by the unsigned reading of the id.
	if got := askPlayer(t, s, "/api/players/18446744073709551607"); got.Counters.State != "known" {
		t.Errorf("unsigned id: counters = %+v", got.Counters)
	}
}

func TestPlayerDetailsSayWhenNothingHasBeenCountedAndWhenNothingIs(t *testing.T) {
	s := withPlayers(t)
	if got := askPlayer(t, s, "/api/players/-9"); got.Counters.State != "off" {
		t.Errorf("no counters kept = %+v", got.Counters)
	}
	withCounters(t, s)
	got := askPlayer(t, s, "/api/players/123")
	if got.Counters.State != "none" || got.Counters.Since != nil {
		t.Errorf("a player nobody counted = %+v", got.Counters)
	}
	s.Live = nil
	if got := askPlayer(t, s, "/api/players/-9"); got.Counters.State != "off" {
		t.Errorf("no live feed = %+v", got.Counters)
	}
}

func TestPlayerDetailsCarryLiveVitalsOnlyForAnOnlinePlayer(t *testing.T) {
	s := withPlayers(t)
	if got := askPlayer(t, s, "/api/players/-9"); got.Live != nil {
		t.Errorf("a pack that sends no vitals still gave %+v", got.Live)
	}
	now := time.Now()
	s.Live.Ingest(live.RawRecord{At: now, Data: `{"gen":3,"dim":"overworld","kind":"players","part":0,"parts":1,"more":0,"items":[{"i":"-9","n":"Dotablaze","x":1,"y":64,"z":1,"h":13,"hm":20,"xl":31}]}`}, now)
	got := askPlayer(t, s, "/api/players/-9")
	if got.Live == nil || *got.Live.Health != 13 || *got.Live.MaxHealth != 20 || *got.Live.Level != 31 || got.Live.Dimension != "overworld" {
		t.Errorf("live = %+v", got.Live)
	}
	if got := askPlayer(t, s, "/api/players/123"); got.Live != nil {
		t.Errorf("an offline player has live vitals: %+v", got.Live)
	}
}
