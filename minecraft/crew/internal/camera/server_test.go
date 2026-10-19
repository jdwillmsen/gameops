package camera

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
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
func TestPageSpeaksTheCurrentWireVersionAndFetchesTheAtlas(t *testing.T) {
	want := "u8[1] !== " + strconv.Itoa(int(wireVersion))
	for _, s := range []string{want, "/v1/camera/atlas.json", "/v1/camera/atlas.png", "case " + strconv.Itoa(int(frameAtlas)) + ":"} {
		if !strings.Contains(string(page), s) {
			t.Errorf("page does not contain %q", s)
		}
	}
}
