package icons

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const testRef = "v9.9.9"

// picture is a synthetic PNG of one colour. Nothing in these tests is a
// real texture.
func picture(t testing.TB, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:], []byte{c.R, c.G, c.B, c.A})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func colourOf(t testing.TB, raw []byte) color.NRGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	return color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
}

var (
	red    = color.NRGBA{200, 0, 0, 255}
	green  = color.NRGBA{0, 200, 0, 255}
	blue   = color.NRGBA{0, 0, 200, 255}
	yellow = color.NRGBA{200, 200, 0, 255}
)

// samples is a stand-in for the published repository: a listing on one
// path and files on another, as the two real hosts serve them.
type samples struct {
	mu    sync.Mutex
	files map[string][]byte
	// listed overrides the listing built from files.
	listed []string
	hits   atomic.Int64
	srv    *httptest.Server
}

func entity(identifier, minEngine, egg string) []byte {
	version := ""
	if minEngine != "" {
		version = fmt.Sprintf(`"min_engine_version": %q,`, minEngine)
	}
	if egg != "" {
		egg = `, "spawn_egg": ` + egg
	}
	// The real files open with a comment line, which is not JSON.
	return fmt.Appendf(nil, "// a comment the real files carry\n"+`{"format_version":"1.10.0","minecraft:client_entity":{"description":{"identifier":%q,%s "textures":{"default":"textures/entity/x" /* and one here */}%s}}}`, identifier, version, egg)
}

func newSamples(t *testing.T) *samples {
	t.Helper()
	s := &samples{files: map[string][]byte{
		"resource_pack/textures/item_texture.json": []byte(`// header comment
{"texture_data": {
  "spawn_egg": {"textures": ["textures/items/egg_chicken", "textures/items/egg_villager"]},
  "spawn_egg_cow": {"textures": "textures/items/spawn_eggs/spawn_egg_cow"},
  "spawn_egg_evoker": {"textures": "textures/items/spawn_eggs/spawn_egg_evoker"},
  "url_like": {"textures": "http://elsewhere.example//not/a/path"},
  "outside": {"textures": "textures/items/../../../secrets"}
}}`),
		// The type is not in the texture's name.
		"resource_pack/entity/evocation_illager.entity.json": entity("minecraft:evocation_illager", "1.8.0", `{"texture":"spawn_egg_evoker"}`),
		// One of a shared list, picked by index.
		"resource_pack/entity/villager.entity.json": entity("minecraft:villager", "1.8.0", `{"texture":"spawn_egg","texture_index":1}`),
		// Two definitions: the older one indexes the list, the newer names
		// a texture of its own, and the newer is the one the game uses.
		"resource_pack/entity/cow.v1.0.entity.json": entity("minecraft:cow", "", `{"texture":"spawn_egg","texture_index":0}`),
		"resource_pack/entity/cow.entity.json":      entity("minecraft:cow", "1.8.0", `{"texture":"spawn_egg_cow"}`),
		// Nothing to draw: no egg, an egg of two colours, an egg the atlas
		// does not hold, and entries that are not texture paths.
		"resource_pack/entity/armor_stand.entity.json": entity("minecraft:armor_stand", "1.8.0", ""),
		"resource_pack/entity/wither.entity.json":      entity("minecraft:wither", "1.8.0", `{"base_color":"#141414","overlay_color":"#4d72a0"}`),
		"resource_pack/entity/ghost.entity.json":       entity("minecraft:ghost", "1.8.0", `{"texture":"spawn_egg_missing"}`),
		"resource_pack/entity/sly.entity.json":         entity("minecraft:sly", "1.8.0", `{"texture":"url_like"}`),
		"resource_pack/entity/climber.entity.json":     entity("minecraft:climber", "1.8.0", `{"texture":"outside"}`),
		"resource_pack/entity/broken.entity.json":      []byte(`{"minecraft:client_entity": `),

		"resource_pack/textures/items/egg_chicken.png":                 picture(t, 16, 16, yellow),
		"resource_pack/textures/items/egg_villager.png":                picture(t, 16, 16, green),
		"resource_pack/textures/items/spawn_eggs/spawn_egg_cow.png":    picture(t, 16, 16, red),
		"resource_pack/textures/items/spawn_eggs/spawn_egg_evoker.png": picture(t, 16, 16, blue),
	}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/list/resource_pack/entity" {
			if r.URL.Query().Get("ref") != testRef {
				http.Error(w, "No commit found for the ref", http.StatusNotFound)
				return
			}
			type entry struct {
				Name string `json:"name"`
				Type string `json:"type"`
			}
			out := []entry{{Name: "a_directory", Type: "dir"}}
			names := s.listed
			if names == nil {
				for path := range s.files {
					if name, ok := strings.CutPrefix(path, "resource_pack/entity/"); ok {
						names = append(names, name)
					}
				}
			}
			for _, name := range names {
				out = append(out, entry{Name: name, Type: "file"})
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		path, ok := strings.CutPrefix(r.URL.Path, "/raw/"+testRef+"/")
		body, held := s.files[path]
		if !ok || !held {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *samples) set(path string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = body
}

func (s *samples) source() *Source {
	return &Source{Ref: testRef, ListURL: s.srv.URL + "/list", RawURL: s.srv.URL + "/raw", HTTP: NewClient()}
}

func TestFetchTakesEachMobsTextureFromTheSamplesOwnData(t *testing.T) {
	icons, err := newSamples(t).source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]color.NRGBA{"evocation_illager": blue, "villager": green, "cow": red}
	if len(icons) != len(want) {
		t.Errorf("got icons for %d types, want %d: %v", len(icons), len(want), keys(icons))
	}
	for kind, colour := range want {
		raw, ok := icons[kind]
		if !ok {
			t.Errorf("no icon for %s", kind)
			continue
		}
		if got := colourOf(t, raw); got != colour {
			t.Errorf("%s got the texture coloured %v, want %v", kind, got, colour)
		}
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// chunk is one PNG chunk, for building files a decoder accepts and that
// carry more than pixels.
func chunk(kind string, data []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{byte(len(data) >> 24), byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))})
	b.WriteString(kind)
	b.Write(data)
	sum := crc32.ChecksumIEEE(append([]byte(kind), data...))
	b.Write([]byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)})
	return b.Bytes()
}

// declaring rewrites a PNG's header, checksum included, to claim other
// dimensions over the same few bytes of data.
func declaring(raw []byte, w, h uint32) []byte {
	out := append([]byte{}, raw...)
	binary.BigEndian.PutUint32(out[16:], w)
	binary.BigEndian.PutUint32(out[20:], h)
	binary.BigEndian.PutUint32(out[29:], crc32.ChecksumIEEE(out[12:29]))
	return out
}

// withText is a valid PNG with a text chunk put in after the header.
func withText(t testing.TB, raw []byte, text string) []byte {
	t.Helper()
	const headerEnd = 8 + 25
	out := append([]byte{}, raw[:headerEnd]...)
	out = append(out, chunk("tEXt", []byte("Comment\x00"+text))...)
	return append(out, raw[headerEnd:]...)
}

func TestWhatIsServedIsAReencodingNotTheDownload(t *testing.T) {
	s := newSamples(t)
	const smuggled = "<script>alert(1)</script>"
	original := withText(t, picture(t, 16, 16, red), smuggled)
	if _, err := png.Decode(bytes.NewReader(original)); err != nil {
		t.Fatalf("the fixture is not a PNG a decoder accepts: %v", err)
	}
	s.set("resource_pack/textures/items/spawn_eggs/spawn_egg_cow.png", original)
	icons, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(icons["cow"], []byte(smuggled)) || bytes.Equal(icons["cow"], original) {
		t.Error("the downloaded bytes were kept as they came, text chunk and all")
	}
	if got := colourOf(t, icons["cow"]); got != red {
		t.Errorf("the picture changed in re-encoding: %v", got)
	}
}

func TestFetchRefusesWhatIsNotASmallPNG(t *testing.T) {
	var jpg bytes.Buffer
	_ = jpeg.Encode(&jpg, image.NewNRGBA(image.Rect(0, 0, 16, 16)), nil)
	// A header declaring an enormous image over almost no data.
	bomb := declaring(picture(t, 16, 16, red), 60000, 60000)
	for name, body := range map[string][]byte{
		"rubbish":                                []byte("<html>rate limited</html>"),
		"a JPEG":                                 jpg.Bytes(),
		"one side too big":                       picture(t, maxIconSide+1, 16, red),
		"a declared size the data does not back": bomb,
		"over the byte limit":                    append(picture(t, 16, 16, red), make([]byte, maxTextureBytes)...),
		"empty":                                  {},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSamples(t)
			s.set("resource_pack/textures/items/spawn_eggs/spawn_egg_cow.png", body)
			if icons, err := s.source().Fetch(t.Context()); err == nil {
				t.Errorf("fetch kept %d icons with one texture being %s", len(icons), name)
			}
		})
	}
}

func TestFetchIsBoundedInCount(t *testing.T) {
	s := newSamples(t)
	for i := range maxDefinitions + 1 {
		s.listed = append(s.listed, fmt.Sprintf("mob%d.entity.json", i))
	}
	_, err := s.source().Fetch(t.Context())
	if err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("a listing of %d definitions was accepted: %v", len(s.listed), err)
	}
	// Refused on the listing alone, before any of them is asked for.
	if n := s.hits.Load(); n != 1 {
		t.Errorf("%d requests were made for an oversized listing, want 1", n)
	}
}

func TestFetchIsBoundedInSize(t *testing.T) {
	for path, limit := range map[string]int{
		"resource_pack/textures/item_texture.json": maxAtlasBytes,
		"resource_pack/entity/cow.entity.json":     maxDefinitionBytes,
	} {
		s := newSamples(t)
		s.set(path, append(entity("minecraft:cow", "1.8.0", ""), bytes.Repeat([]byte(" "), limit)...))
		if _, err := s.source().Fetch(t.Context()); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Errorf("%s over %d bytes was accepted: %v", path, limit, err)
		}
	}
}

func TestFetchNeverAsksForAPathTheAtlasMadeUp(t *testing.T) {
	s := newSamples(t)
	var asked []string
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		asked = append(asked, r.URL.Path)
		s.mu.Unlock()
		inner.ServeHTTP(w, r)
	})
	if _, err := s.source().Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, path := range asked {
		if strings.Contains(path, "secrets") || strings.Contains(path, "elsewhere") || strings.Contains(path, "..") {
			t.Errorf("asked the source for %s", path)
		}
	}
}

func TestFetchFollowsNoRedirect(t *testing.T) {
	var elsewhere atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
		_, _ = w.Write(picture(t, 16, 16, red))
	}))
	defer other.Close()

	s := newSamples(t)
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "spawn_egg_cow.png") {
			http.Redirect(w, r, other.URL+"/cow.png", http.StatusFound)
			return
		}
		inner.ServeHTTP(w, r)
	})
	if _, err := s.source().Fetch(t.Context()); err == nil {
		t.Error("a texture served by way of a redirect was accepted")
	}
	if n := elsewhere.Load(); n != 0 {
		t.Errorf("the redirect was followed to another host %d times", n)
	}
}

func TestFetchStopsWhenItsContextDoes(t *testing.T) {
	s := newSamples(t)
	ctx, cancel := context.WithCancel(t.Context())
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".png") {
			cancel()
			<-r.Context().Done()
			return
		}
		inner.ServeHTTP(w, r)
	})
	if _, err := s.source().Fetch(ctx); err == nil {
		t.Error("a fetch whose context ended reported success")
	}
}

func TestStripCommentsLeavesStringsAlone(t *testing.T) {
	in := `// top
{"a": "http://x/y", /* gone */ "b": "say \"// hi\" /* there */", "c": 1} // tail`
	var got map[string]any
	if err := json.Unmarshal(stripComments([]byte(in)), &got); err != nil {
		t.Fatal(err)
	}
	if got["a"] != "http://x/y" || got["b"] != `say "// hi" /* there */` || got["c"] != float64(1) {
		t.Errorf("got %v", got)
	}
}
