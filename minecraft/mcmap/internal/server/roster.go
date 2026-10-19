package server

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/roster"
)

// Roster is every player the live feed has shown.
type Roster interface {
	List() []roster.Entry
	Link(gamertag, xuid string)
}

// What a player in the list may be tagged with.
const (
	tagOnline   = "online"
	tagYou      = "you"
	tagOperator = "operator"
)

// rosterPlayerJSON is one player of the list. It says where nobody is: an
// offline player's saved position is in their details, and an online one's
// is on the map.
type rosterPlayerJSON struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Earlier []string `json:"earlier,omitempty"`
	// Dimension is where an online player is now.
	Dimension string    `json:"dimension,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Tags      []string  `json:"tags"`
}

type rosterJSON struct {
	Players []rosterPlayerJSON `json:"players"`
}

// handleRoster lists every player the map has seen, online or not, for
// every logged-in viewer: it says no more of anyone than their details,
// which every viewer may open, already do.
func (s *Server) handleRoster(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	type where struct {
		dimension string
		at        time.Time
	}
	online := map[string]where{}
	names := map[string]string{}
	if s.Live != nil {
		for _, dimension := range render.Dimensions {
			frame := s.Live.Store.Snapshot(dimension, now)
			for _, p := range frame.Players {
				online[p.ID] = where{dimension, frame.At}
				names[p.ID] = p.Name
			}
		}
	}
	var viewer string
	if v, ok := auth.ViewerFromContext(r.Context()); ok && !v.Service {
		viewer = v.Name
	}

	out := rosterJSON{Players: []rosterPlayerJSON{}}
	listed := map[string]bool{}
	add := func(p rosterPlayerJSON, xuid string) {
		listed[p.ID] = true
		if w, ok := online[p.ID]; ok {
			p.Dimension = w.dimension
			p.Tags = append(p.Tags, tagOnline)
			if !w.at.IsZero() && w.at.After(p.LastSeen) {
				p.LastSeen = w.at.UTC().Truncate(time.Second)
			}
		}
		if xuid != "" && s.isAdmin(r.Context(), xuid) {
			p.Tags = append(p.Tags, tagOperator)
		}
		if p.Tags == nil {
			p.Tags = []string{}
		}
		out.Players = append(out.Players, p)
	}
	for _, e := range s.Roster.List() {
		name := e.Name
		if live := names[e.ID]; live != "" {
			name = live
		}
		add(rosterPlayerJSON{ID: e.ID, Name: name, Earlier: e.Earlier, FirstSeen: e.First, LastSeen: e.Last}, e.XUID)
	}
	// Online and not yet in the roster: between a sample and the next
	// write, or a roster that refused them.
	for id, w := range online {
		if !listed[id] && names[id] != "" {
			at := w.at.UTC().Truncate(time.Second)
			add(rosterPlayerJSON{ID: id, Name: names[id], FirstSeen: at, LastSeen: at}, "")
		}
	}
	// A gamertag is the viewer's only on whoever was seen under it last.
	you := -1
	for i, p := range out.Players {
		if viewer != "" && strings.EqualFold(p.Name, viewer) && (you < 0 || p.LastSeen.After(out.Players[you].LastSeen)) {
			you = i
		}
	}
	if you >= 0 {
		p := &out.Players[you]
		p.Tags = slices.Insert(p.Tags, slices.Index(p.Tags, tagOnline)+1, tagYou)
	}
	slices.SortStableFunc(out.Players, func(a, b rosterPlayerJSON) int {
		ao, bo := slices.Contains(a.Tags, tagOnline), slices.Contains(b.Tags, tagOnline)
		switch {
		case ao && !bo:
			return -1
		case bo && !ao:
			return 1
		case ao:
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		return b.LastSeen.Compare(a.LastSeen)
	})
	writeJSON(w, http.StatusOK, out)
}

// linkRoster ties the player who just logged in to the map to the id the
// game gives them, by the gamertag the login vouched for.
func (s *Server) linkRoster(id auth.Identity) {
	if s.Roster != nil && id.XUID != "" && id.Gamertag != "" {
		s.Roster.Link(id.Gamertag, id.XUID)
	}
}
