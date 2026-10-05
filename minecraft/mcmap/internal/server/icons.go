package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
)

// MobIcons is the icon of each mob type that has one. It may hold none,
// which is the state it starts in and stays in while its source is out of
// reach.
type MobIcons interface {
	Icon(kind string) (png []byte, ok bool)
	Listing() (version string, types []string)
}

// PlayerHeads is the head of each player online now, as the agent reports
// them.
type PlayerHeads interface {
	Replace(reports []icons.Report) (refused int, err error)
	ByGamertag(gamertag string) (icons.Head, bool)
	Listing(xuid string) (versions map[string]string, self string)
}

// maxHeadsBody is a full server of the largest heads, in base64, several
// times over.
const maxHeadsBody = 1 << 20

// An icon's address carries its version, so a browser may keep what it got
// for good: a changed icon has a different address. Private, because it is
// only for the logged-in browser that asked.
const (
	cacheForGood = "private, max-age=31536000, immutable"
	cacheChecked = "private, no-cache"
)

type iconsJSON struct {
	Mobs struct {
		Version string   `json:"version"`
		Types   []string `json:"types"`
	} `json:"mobs"`
	// Heads is the version of each head there is, by gamertag in lower
	// case. A gamertag two online players share is not in it.
	Heads map[string]string `json:"heads"`
	// Me is the gamertag the session's own player is online under now. It
	// is matched by XUID, so it is right after a gamertag change that the
	// session, issued under the old one, knows nothing of.
	Me string `json:"me,omitempty"`
}

// handleIcons tells the page which markers have a picture: the mob types
// with an icon, and the players with a head. It is asked again every so
// often, so it carries a tag and answers an unchanged list with a 304.
func (s *Server) handleIcons(w http.ResponseWriter, r *http.Request) {
	out := iconsJSON{Heads: map[string]string{}}
	out.Mobs.Types = []string{}
	if s.MobIcons != nil {
		if version, types := s.MobIcons.Listing(); len(types) > 0 {
			out.Mobs.Version, out.Mobs.Types = version, types
		}
	}
	if s.Heads != nil {
		id, _ := auth.FromContext(r.Context())
		out.Heads, out.Me = s.Heads.Listing(id.XUID)
	}
	body, _ := json.Marshal(out)
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
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

func servePNG(w http.ResponseWriter, r *http.Request, png []byte, version string) {
	h := w.Header()
	h.Set("Content-Type", "image/png")
	// Asked for under another version, the answer is still the current
	// picture, but not one to keep under that address.
	if r.URL.Query().Get("v") == version {
		h.Set("Cache-Control", cacheForGood)
	} else {
		h.Set("Cache-Control", cacheChecked)
	}
	_, _ = w.Write(png)
}

func (s *Server) handleMobIcon(w http.ResponseWriter, r *http.Request) {
	png, ok := s.MobIcons.Icon(r.PathValue("type"))
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	version, _ := s.MobIcons.Listing()
	servePNG(w, r, png, version)
}

// handleHead serves the head of the one online player holding a gamertag.
// A gamertag nobody holds, or two players do, has no head: the page draws
// its plain marker.
func (s *Server) handleHead(w http.ResponseWriter, r *http.Request) {
	head, ok := s.Heads.ByGamertag(r.URL.Query().Get("name"))
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	servePNG(w, r, head.PNG, head.Version)
}

type headsRequest struct {
	Players []struct {
		XUID     string `json:"xuid"`
		Gamertag string `json:"gamertag"`
		// Head is a PNG in base64, or absent.
		Head string `json:"head"`
	} `json:"players"`
}

// handleHeads takes the agent's report of who is online and what their
// heads look like. It replaces the last report whole.
func (s *Server) handleHeads(w http.ResponseWriter, r *http.Request) {
	var req headsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHeadsBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Players == nil {
		http.Error(w, `a report lists who is online: {"players":[{"xuid","gamertag","head"}]}`, http.StatusBadRequest)
		return
	}
	if len(req.Players) > icons.MaxPlayers {
		http.Error(w, "too many players", http.StatusBadRequest)
		return
	}
	reports := make([]icons.Report, 0, len(req.Players))
	undecoded := 0
	for _, p := range req.Players {
		report := icons.Report{XUID: p.XUID, Gamertag: p.Gamertag}
		if p.Head != "" {
			head, err := base64.StdEncoding.DecodeString(p.Head)
			if err != nil {
				undecoded++
			} else {
				report.Head = head
			}
		}
		reports = append(reports, report)
	}
	refused, err := s.Heads.Replace(reports)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if refused += undecoded; refused > 0 {
		s.log().Warn("player heads refused", "refused", refused, "players", len(reports))
	}
	writeJSON(w, http.StatusOK, map[string]int{"players": len(reports), "refused": refused})
}
