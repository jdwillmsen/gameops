package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/icons"
)

// picture is a synthetic PNG of one colour; nothing here is real artwork.
func picture(t testing.TB, side int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{c.R, c.G, c.B, c.A})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func b64(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

var (
	red  = color.NRGBA{200, 0, 0, 255}
	blue = color.NRGBA{0, 0, 200, 255}
)

func mobIcons(t *testing.T, fetch func(context.Context) (map[string][]byte, error)) *icons.Mobs {
	t.Helper()
	return &icons.Mobs{Dir: t.TempDir(), Ref: "v9.9.9", Logger: slog.New(slog.DiscardHandler), Fetch: fetch}
}

func withIcons(t *testing.T) *Server {
	t.Helper()
	s := withLive(t)
	mobs := mobIcons(t, func(context.Context) (map[string][]byte, error) {
		return map[string][]byte{"cow": picture(t, 16, red), "pig": picture(t, 16, blue)}, nil
	})
	mobs.Run(t.Context())
	s.MobIcons = mobs
	s.Heads = &icons.Heads{}
	return s
}

func report(t *testing.T, s *Server, body string) map[string]int {
	t.Helper()
	rec := do(s.InternalHandler(), "PUT", "/internal/v1/heads", body, nil, "Authorization", "Bearer "+internalToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT heads = %d %s", rec.Code, rec.Body)
	}
	var out map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func listing(t *testing.T, s *Server, c *http.Cookie) iconsJSON {
	t.Helper()
	rec := do(s.Handler(), "GET", "/api/icons", "", []*http.Cookie{c})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/icons = %d %s", rec.Code, rec.Body)
	}
	var out iconsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIcons_NeedASession(t *testing.T) {
	s := withIcons(t)
	report(t, s, `{"players":[{"xuid":"`+steve.XUID+`","gamertag":"Steve Builds","head":"`+b64(picture(t, 8, red))+`"}]}`)
	h := s.Handler()
	paths := []string{"/api/icons", "/api/icons/mob/cow?v=x", "/api/icons/head?name=Steve+Builds"}
	for _, path := range paths {
		rec := do(h, "GET", path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, rec.Code)
		}
		if bytes.Contains(rec.Body.Bytes(), []byte("PNG")) || strings.Contains(rec.Body.String(), "steve") {
			t.Errorf("GET %s without a session leaked %q", path, rec.Body)
		}
	}
	c := session(s, alex)
	for _, path := range paths {
		if rec := do(h, "GET", path, "", []*http.Cookie{c}); rec.Code != http.StatusOK {
			t.Errorf("GET %s with a session = %d, want 200", path, rec.Code)
		}
	}
}

func TestIcons_AreImagesABrowserKeepsUntilTheirVersionChanges(t *testing.T) {
	s := withIcons(t)
	report(t, s, `{"players":[{"xuid":"`+steve.XUID+`","gamertag":"Steve Builds","head":"`+b64(picture(t, 8, red))+`"}]}`)
	c := []*http.Cookie{session(s, alex)}
	list := listing(t, s, c[0])
	if list.Mobs.Version == "" || strings.Join(list.Mobs.Types, ",") != "cow,pig" {
		t.Fatalf("mobs listed as %+v", list.Mobs)
	}
	head := list.Heads["steve builds"]
	if head == "" {
		t.Fatalf("heads listed as %v", list.Heads)
	}
	h := s.Handler()
	for path, version := range map[string]string{
		"/api/icons/mob/cow?v=":                list.Mobs.Version,
		"/api/icons/head?name=steve+builds&v=": head,
	} {
		rec := do(h, "GET", path+version, "", c)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if _, err := png.Decode(rec.Body); err != nil {
			t.Errorf("GET %s is not a PNG: %v", path, err)
		}
		if got := rec.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
			t.Errorf("GET %s at its own version is cached as %q", path, got)
		}
		// Under any other version it must not be kept: the address would go
		// on answering with a picture that has since been replaced.
		stale := do(h, "GET", path+"an-older-one", "", c)
		if got := stale.Header().Get("Cache-Control"); stale.Code != http.StatusOK || got != "private, no-cache" {
			t.Errorf("GET %s at another version = %d, cached as %q", path, stale.Code, got)
		}
	}
	if rec := do(h, "GET", "/api/icons/mob/unicorn?v="+list.Mobs.Version, "", c); rec.Code != http.StatusNotFound {
		t.Errorf("a mob with no icon = %d, want 404", rec.Code)
	}

	first := do(h, "GET", "/api/icons", "", c)
	again := do(h, "GET", "/api/icons", "", c, "If-None-Match", first.Header().Get("ETag"))
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Errorf("an unchanged listing = %d with %d bytes, want an empty 304", again.Code, again.Body.Len())
	}
}

// The icon source being out of reach is the ordinary case on a first start
// with no route out, and the map has to be the map it was before there
// were icons: everything else answers as it did, and the listing is empty,
// which is what the page draws dots for.
func TestIcons_WithTheSourceUnreachableTheMapWorksAndListsNone(t *testing.T) {
	s := withLive(t)
	ctx, cancel := context.WithCancel(t.Context())
	asked := make(chan struct{}, 1)
	mobs := mobIcons(t, func(ctx context.Context) (map[string][]byte, error) {
		select {
		case asked <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, errors.New("dial tcp: i/o timeout")
	})
	done := make(chan struct{})
	go func() { mobs.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	<-asked
	s.MobIcons = mobs
	s.Heads = &icons.Heads{}

	c := []*http.Cookie{session(s, steve)}
	h := s.Handler()
	for _, path := range []string{"/api/map", "/tiles/overworld/0/37/-4.webp", "/api/me", "/"} {
		if rec := do(h, "GET", path, "", c); rec.Code != http.StatusOK {
			t.Errorf("GET %s with the icon source unreachable = %d, want 200", path, rec.Code)
		}
	}
	st := open(t, serve(t, s), "/api/live?dimension=overworld", c[0])
	if frame := st.next(t); !frame.Stale {
		t.Errorf("the live stream's first frame is %+v", frame)
	}
	list := listing(t, s, c[0])
	if list.Mobs.Version != "" || len(list.Mobs.Types) != 0 || len(list.Heads) != 0 {
		t.Errorf("icons are listed with none fetched: %+v", list)
	}
	if rec := do(h, "GET", "/api/icons/mob/cow", "", c); rec.Code != http.StatusNotFound {
		t.Errorf("an icon that was never fetched = %d, want 404", rec.Code)
	}
}

func TestHeads_AreReportedByTheAgentOnly(t *testing.T) {
	s := withIcons(t)
	body := `{"players":[{"xuid":"` + steve.XUID + `","gamertag":"Steve Builds","head":"` + b64(picture(t, 8, red)) + `"}]}`
	internal := s.InternalHandler()
	for name, header := range map[string][]string{
		"no token":       nil,
		"a wrong token":  {"Authorization", "Bearer not-the-token-at-all"},
		"a bare token":   {"Authorization", internalToken},
		"a session only": {"Cookie", session(s, steve).String()},
	} {
		if rec := do(internal, "PUT", "/internal/v1/heads", body, nil, header...); rec.Code != http.StatusUnauthorized {
			t.Errorf("PUT heads with %s = %d, want 401", name, rec.Code)
		}
	}
	// The page's listener does not have the route at all.
	if rec := do(s.Handler(), "PUT", "/internal/v1/heads", body, nil, "Authorization", "Bearer "+internalToken); rec.Code == http.StatusOK {
		t.Error("the public listener accepted a report of heads")
	}
	if list := listing(t, s, session(s, alex)); len(list.Heads) != 0 {
		t.Errorf("a refused report was kept: %v", list.Heads)
	}
	if got := report(t, s, body); got["players"] != 1 || got["refused"] != 0 {
		t.Errorf("the agent's report was answered %v", got)
	}
}

func TestHeads_AMalformedOrOversizedSkinIsRefused(t *testing.T) {
	for name, head := range map[string]string{
		"not base64":   "!!!not base64!!!",
		"not an image": b64([]byte("GIF89a not a png")),
		"a whole skin": b64(picture(t, 64, red)),
		"too large":    b64(picture(t, 33, red)),
	} {
		t.Run(name, func(t *testing.T) {
			s := withIcons(t)
			got := report(t, s, `{"players":[
				{"xuid":"`+steve.XUID+`","gamertag":"Steve Builds","head":"`+head+`"},
				{"xuid":"`+alex.XUID+`","gamertag":"Alex","head":"`+b64(picture(t, 8, blue))+`"}]}`)
			if got["refused"] != 1 {
				t.Errorf("refused %d heads, want 1", got["refused"])
			}
			c := []*http.Cookie{session(s, alex)}
			if rec := do(s.Handler(), "GET", "/api/icons/head?name=Steve+Builds", "", c); rec.Code != http.StatusNotFound {
				t.Errorf("a head that is %s is served: %d", name, rec.Code)
			}
			if rec := do(s.Handler(), "GET", "/api/icons/head?name=Alex", "", c); rec.Code != http.StatusOK {
				t.Errorf("the good head beside it = %d", rec.Code)
			}
		})
	}

	s := withIcons(t)
	internal := s.InternalHandler()
	for name, body := range map[string]string{
		"a body over the limit": `{"players":[{"xuid":"` + steve.XUID + `","gamertag":"Steve","head":"` + strings.Repeat("A", maxHeadsBody) + `"}]}`,
		"not JSON":              `players=steve`,
		"no list":               `{}`,
		"an unknown field":      `{"players":[],"admin":true}`,
		"a player with no xuid": `{"players":[{"gamertag":"Steve"}]}`,
	} {
		if rec := do(internal, "PUT", "/internal/v1/heads", body, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT heads with %s = %d, want 400", name, rec.Code)
		}
	}
}

// The page asks for a head by the gamertag on a marker. Two online players
// answering to one gamertag would make either head a guess.
func TestHeads_AGamertagTwoPlayersHoldHasNoHead(t *testing.T) {
	s := withIcons(t)
	report(t, s, `{"players":[
		{"xuid":"`+steve.XUID+`","gamertag":"Twin","head":"`+b64(picture(t, 8, red))+`"},
		{"xuid":"`+alex.XUID+`","gamertag":"twin","head":"`+b64(picture(t, 8, blue))+`"}]}`)
	c := session(s, steve)
	if rec := do(s.Handler(), "GET", "/api/icons/head?name=Twin", "", []*http.Cookie{c}); rec.Code != http.StatusNotFound {
		t.Errorf("a gamertag two players hold was answered with a head: %d", rec.Code)
	}
	if list := listing(t, s, c); len(list.Heads) != 0 || list.Me != "" {
		t.Errorf("listing is %+v", list)
	}
}

func TestIcons_TellASessionWhichGamertagIsItsOwnNow(t *testing.T) {
	s := withIcons(t)
	// The session was issued to "Steve Builds"; the player has since been
	// renamed, and the agent reports them under the new name.
	report(t, s, `{"players":[{"xuid":"`+steve.XUID+`","gamertag":"Stefan"},{"xuid":"`+alex.XUID+`","gamertag":"Alex"}]}`)
	if list := listing(t, s, session(s, steve)); list.Me != "stefan" {
		t.Errorf("the session's own gamertag is %q, want the one its XUID is online under", list.Me)
	}
}

func TestIcons_RoutesAreAbsentWhenThereIsNothingBehindThem(t *testing.T) {
	s := withLive(t)
	c := []*http.Cookie{session(s, steve)}
	for _, path := range []string{"/api/icons", "/api/icons/mob/cow", "/api/icons/head?name=Steve"} {
		if rec := do(s.Handler(), "GET", path, "", c); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with icons off = %d, want 404", path, rec.Code)
		}
	}
	if rec := do(s.InternalHandler(), "PUT", "/internal/v1/heads", `{"players":[]}`, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusNotFound {
		t.Errorf("PUT heads with icons off = %d, want 404", rec.Code)
	}
}

// Icons are images from this origin and nowhere else; the policy that says
// so is the one the page had before there were any.
func TestIcons_LeaveTheContentSecurityPolicyAsItWas(t *testing.T) {
	s := withIcons(t)
	const want = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	c := []*http.Cookie{session(s, steve)}
	for _, path := range []string{"/", "/api/icons", "/api/icons/mob/cow"} {
		rec := do(s.Handler(), "GET", path, "", c)
		if got := rec.Header().Get("Content-Security-Policy"); got != want {
			t.Errorf("GET %s carries the policy %q", path, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s may be sniffed: %q", path, got)
		}
	}
}
