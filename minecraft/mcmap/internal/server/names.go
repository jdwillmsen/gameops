package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

type namesJSON struct {
	// Version changes when any name does, and is the version /api/icons
	// gives for the names.
	Version string `json:"version"`
	icons.Table
}

// namedMobs is implemented by a marker store that can say which mob types
// its markers hold.
type namedMobs interface {
	MobKinds() []string
}

// seenEntities is every mob type the page can be holding right now: those
// on the live layer and those among the markers. They are named along
// with what the samples define, so that a type has a name on the page
// while the samples are out of reach, when nothing else says it exists.
func (s *Server) seenEntities() []string {
	var kinds []string
	if store, ok := s.Markers.(namedMobs); ok {
		kinds = append(kinds, store.MobKinds()...)
	}
	if s.Live != nil {
		now := time.Now()
		for _, dimension := range render.Dimensions {
			for _, mob := range s.Live.Store.Snapshot(dimension, now).Mobs {
				kinds = append(kinds, mob.Type)
			}
		}
	}
	return kinds
}

// names is the table of display names as it is served, and its version.
func (s *Server) names() (version string, body []byte) {
	out := namesJSON{Table: s.Art.Names().Table(s.seenEntities())}
	plain, _ := json.Marshal(out.Table)
	sum := sha256.Sum256(plain)
	out.Version = hex.EncodeToString(sum[:8])
	body, _ = json.Marshal(out)
	return out.Version, body
}

// handleNames serves the display name of everything the map shows, by the
// ids the page holds. Every value is text for a person: the game's own
// name where its language file has one, the id tidied into words where it
// does not. It changes only with the pin or when a new type of mob turns
// up, so it carries a tag and answers an unchanged table with a 304.
func (s *Server) handleNames(w http.ResponseWriter, r *http.Request) {
	version, body := s.names()
	etag := `"` + version + `"`
	h := w.Header()
	h.Set("Cache-Control", cacheChecked)
	h.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
