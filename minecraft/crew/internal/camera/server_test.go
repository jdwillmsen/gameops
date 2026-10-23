package camera

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func okBase() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("base")) })
}

func TestMountServesThePageAndKeepsTheStatusRoutes(t *testing.T) {
	h, _ := newTestHub(t, 1)
	srv := httptest.NewServer(Mount(okBase(), h))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/camera")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	if res.StatusCode != 200 || !strings.Contains(string(buf[:n]), "three") {
		t.Errorf("page = %d %.60q", res.StatusCode, buf[:n])
	}

	res2, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != 200 {
		t.Errorf("base route = %d", res2.StatusCode)
	}
}

func dial(t *testing.T, srv *httptest.Server) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/camera/ws", nil)
}

func TestWebsocketStreamsTheWorldAndEnforcesTheViewerCap(t *testing.T) {
	tr := belief.New(time.Now)
	tr.Spawn(self, mgl32.Vec3{}, 0, 0, 0)
	h := New(Options{Decoder: fakeDecoder{section: stoneOnFloor()}, Self: tr, MaxViewers: 1})
	h.Spawn(self)
	placeUnit(h, tr, 8, 8, 8)
	h.Apply(subChunkPacket(0, 0, 0), &sink{})
	srv := httptest.NewServer(Mount(okBase(), h))
	defer srv.Close()

	c, _, err := dial(t, srv)
	if err != nil {
		t.Fatalf("first viewer: %v", err)
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var types []byte
	for len(types) < 4 {
		typ, data, err := c.Read(ctx)
		if err != nil || typ != websocket.MessageBinary {
			t.Fatalf("read = %v, %v; want binary frames", typ, err)
		}
		if data[0] == frameTick {
			continue
		}
		types = append(types, data[0])
		if data[0] == frameSubChunk {
			break
		}
	}
	if want := []byte{frameHello, frameMaterials, frameEntityTypes, frameSubChunk}; !bytes.Equal(types, want) {
		t.Fatalf("frame types = %v, want %v", types, want)
	}

	_, res, err := dial(t, srv)
	if err == nil || res == nil || res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("second viewer: status %v err %v, want 503", res, err)
	}
}

// subChunkCounts reads frames in the background and reports each sub-chunk
// as it arrives; a timed-out Read would close the connection.
func subChunkCounts(c *websocket.Conn) <-chan struct{} {
	ch := make(chan struct{}, 64)
	go func() {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			if data[0] == frameSubChunk {
				ch <- struct{}{}
			}
		}
	}()
	return ch
}

func count(ch <-chan struct{}, quiet time.Duration) (n int) {
	for {
		select {
		case <-ch:
			n++
		case <-time.After(quiet):
			return n
		}
	}
}

func TestViewerChoosesItsRadiusByQueryAndWidensItOverTheSocket(t *testing.T) {
	tr := belief.New(time.Now)
	tr.Spawn(self, mgl32.Vec3{}, 0, 0, 0)
	h := New(Options{Decoder: fakeDecoder{section: stoneOnFloor()}, Self: tr, MaxViewers: 2})
	h.Spawn(self)
	placeUnit(h, tr, 8, 8, 8)
	h.Apply(subChunkPacket(0, 0, 0), &sink{})
	h.Apply(subChunkPacket(3, 0, 0), &sink{})
	srv := httptest.NewServer(Mount(okBase(), h))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/camera/ws?r=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	subs := subChunkCounts(c)
	if n := count(subs, 300*time.Millisecond); n != 1 {
		t.Fatalf("radius 1 replayed %d chunks, want 1", n)
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte{cmdRadius, 4}); err != nil {
		t.Fatal(err)
	}
	if n := count(subs, 300*time.Millisecond); n != 1 {
		t.Errorf("widening replayed %d more chunks, want 1", n)
	}
}

func TestAtlasRoutesAnswer404UntilTheAtlasIsBuilt(t *testing.T) {
	h, _ := newTestHub(t, 1)
	srv := httptest.NewServer(Mount(okBase(), h))
	defer srv.Close()
	get := func(path string) (int, string) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode, res.Header.Get("Content-Type")
	}
	for _, p := range []string{"/v1/camera/atlas.png", "/v1/camera/atlas.json", "/v1/camera/items.png", "/v1/camera/items.json"} {
		if code, _ := get(p); code != http.StatusNotFound {
			t.Errorf("%s before the build = %d, want 404", p, code)
		}
	}
	if err := h.SetAtlas(testAtlas(t)); err != nil {
		t.Fatal(err)
	}
	if code, ct := get("/v1/camera/atlas.png"); code != 200 || ct != "image/png" {
		t.Errorf("png = %d %q", code, ct)
	}
	if code, ct := get("/v1/camera/atlas.json"); code != 200 || ct != "application/json" {
		t.Errorf("json = %d %q", code, ct)
	}
	if code, ct := get("/v1/camera/items.png"); code != 200 || ct != "image/png" {
		t.Errorf("items png = %d %q", code, ct)
	}
	if code, ct := get("/v1/camera/items.json"); code != 200 || ct != "application/json" {
		t.Errorf("items json = %d %q", code, ct)
	}
}

// The page is embedded and not type-checked, so a wire bump that forgets it
// would only show in a browser.
func TestPageSpeaksTheCurrentWireVersionAndHandlesTheAtlas(t *testing.T) {
	want := "u8[1] !== " + strconv.Itoa(int(legacyWireVersion))
	for _, s := range []string{want, "case " + strconv.Itoa(int(frameAtlas)) + ":"} {
		if !strings.Contains(string(page), s) {
			t.Errorf("page does not contain %q", s)
		}
	}
}

// Same reasoning: the only key that hides the debug box exists nowhere the
// compiler can see.
func TestPageTogglesDebugOnF3(t *testing.T) {
	if !strings.Contains(string(page), "'F3'") {
		t.Error("page does not contain 'F3'")
	}
	// Server-supplied text must not be parsed as markup.
	if strings.Contains(string(page), "innerHTML") {
		t.Error("page assigns innerHTML")
	}
}

// The worker file is embedded and not type-checked, like the page.
func TestMesherWorkerIsServed(t *testing.T) {
	const path = "/v1/camera/mesher.js"
	h, _ := newTestHub(t, 1)
	srv := httptest.NewServer(Mount(okBase(), h))
	defer srv.Close()
	res, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") || !bytes.Equal(body, mesher) || len(body) == 0 {
		t.Errorf("mesher.js = %d %q, %d bytes", res.StatusCode, res.Header.Get("Content-Type"), len(body))
	}
}

func TestPlainProbeOnAFullHubAnswers503WithoutCountingARefusal(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v, err := h.Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	srv := httptest.NewServer(Mount(okBase(), h))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/camera/ws")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("probe on a full hub = %d, want 503", res.StatusCode)
	}
	if got := h.Stats().ViewersRefused; got != 0 {
		t.Errorf("viewersRefused = %d, want 0", got)
	}
}

func TestPlainProbeOnAHubWithRoomAnswers426AndAttachesNobody(t *testing.T) {
	h, _ := newTestHub(t, 1)
	srv := httptest.NewServer(Mount(okBase(), h))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/camera/ws")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("probe with room = %d, want 426", res.StatusCode)
	}
	if got := h.Stats().Viewers; got != 0 {
		t.Errorf("viewers = %d, want 0", got)
	}
}

// wideSection uses n distinct blocks, one per cell of the floor and beyond.
func wideSection(n int) Section {
	s := Section{Palette: []string{"minecraft:air"}}
	for i := 1; i <= n; i++ {
		s.Palette = append(s.Palette, "minecraft:block_"+strconv.Itoa(i))
	}
	for i := range s.Cells {
		s.Cells[i] = uint16(1 + i%n)
	}
	return s
}

// readUntilSubChunk returns the frames up to and including the first
// sub-chunk, skipping ticks.
func readUntilSubChunk(t *testing.T, c *websocket.Conn) [][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var out [][]byte
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v after %d frames", err, len(out))
		}
		if data[0] == frameTick {
			continue
		}
		out = append(out, data)
		if data[0] == frameSubChunk {
			return out
		}
	}
}

func wideHub(t *testing.T) *httptest.Server {
	t.Helper()
	tr := belief.New(time.Now)
	tr.Spawn(self, mgl32.Vec3{}, 0, 0, 0)
	h := New(Options{Decoder: fakeDecoder{section: wideSection(300)}, Self: tr, MaxViewers: 3})
	h.Spawn(self)
	placeUnit(h, tr, 8, 8, 8)
	h.Apply(subChunkPacket(0, 0, 0), &sink{})
	srv := httptest.NewServer(Mount(okBase(), h))
	t.Cleanup(srv.Close)
	return srv
}

func dialQuery(t *testing.T, srv *httptest.Server, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/camera/ws"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func TestPageThatAsksForWireFourGetsEveryBlockItsOwnId(t *testing.T) {
	frames := readUntilSubChunk(t, dialQuery(t, wideHub(t), "?wire=4"))
	if frames[0][0] != frameHello || frames[0][1] != 4 || frames[1][0] != frameMaterials {
		t.Fatalf("head = % x", frames[0])
	}
	if n := materialCount(frames[1]); n != 300 {
		t.Errorf("materials frame lists %d, want 300", n)
	}
	_, _, _, cells, err := decodeSubChunk(frames[len(frames)-1])
	if err != nil {
		t.Fatal(err)
	}
	distinct := map[uint16]bool{}
	for _, c := range cells {
		distinct[c] = true
	}
	if len(distinct) != 300 || !distinct[300] {
		t.Errorf("sub-chunk holds %d distinct ids, want 300 up to id 300", len(distinct))
	}
}

// A tab opened before the upgrade asks for nothing and reads version 3 only;
// it must keep drawing, with the blocks it cannot name all as the last id.
func TestPageThatDoesNotAskForAVersionStillGetsWireThree(t *testing.T) {
	for _, query := range []string{"", "?r=2", "?wire=3", "?wire=x"} {
		frames := readUntilSubChunk(t, dialQuery(t, wideHub(t), query))
		if frames[0][0] != frameHello || frames[0][1] != 3 {
			t.Fatalf("%q: hello = % x, want version 3", query, frames[0])
		}
		mats := parseMaterialsV3(t, frames[1])
		if len(mats) != 255 || mats[254].id != 255 {
			t.Errorf("%q: %d materials, want 255 ending at id 255", query, len(mats))
		}
		palette, cells := decodeSubChunkV3(t, frames[len(frames)-1])
		if len(palette) != 255 || slices.Max(cells[:]) != 255 {
			t.Errorf("%q: palette %d max id %d, want 255 and 255", query, len(palette), slices.Max(cells[:]))
		}
	}
}
