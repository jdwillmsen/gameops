package camera

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	typ, _, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageText {
		t.Fatalf("first message = %v, %v; want the material table", typ, err)
	}
	if typ, data, err := c.Read(ctx); err != nil || typ != websocket.MessageBinary || len(data) != 13+4096 {
		t.Fatalf("second message = %v %d bytes, %v; want a sub-chunk frame", typ, len(data), err)
	}

	_, res, err := dial(t, srv)
	if err == nil || res == nil || res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("second viewer: status %v err %v, want 503", res, err)
	}
}
