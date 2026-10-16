package camera

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

//go:embed web/index.html
var page []byte

// A browser that stops reading must not pin a goroutine and a socket forever.
const writeTimeout = 5 * time.Second

// Mount adds the camera routes to the status API handler. It is the only way
// the camera becomes reachable: nothing registers itself.
func Mount(base http.Handler, h *Hub) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("GET /camera", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /v1/camera/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.Stats())
	})
	mux.HandleFunc("GET /v1/camera/ws", h.serveWS)
	return mux
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	// Checked before the upgrade so a refused viewer gets a plain status the
	// page can act on, and no websocket state is allocated for it.
	v, err := h.Attach()
	if errors.Is(err, ErrFull) {
		http.Error(w, "viewer limit reached", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "camera unavailable", http.StatusInternalServerError)
		return
	}
	defer v.Close()
	if r, err := strconv.Atoi(r.URL.Query().Get("r")); err == nil {
		v.SetRadius(r)
	}

	// Accept refuses a cross-origin page by default; the viewer is served from
	// this same origin.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	// The only thing a viewer says is how far it wants to see; the read loop
	// also keeps control frames answered.
	conn.SetReadLimit(16)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if len(msg) == 2 && msg[0] == cmdRadius {
				v.SetRadius(int(msg[1]))
			}
		}
	}()
	for {
		m, err := v.Next(ctx)
		if err != nil {
			if errors.Is(err, ErrTooSlow) {
				_ = conn.Close(websocket.StatusTryAgainLater, "too slow")
			}
			return
		}
		wctx, cancel := context.WithTimeout(ctx, writeTimeout)
		err = conn.Write(wctx, websocket.MessageBinary, m)
		cancel()
		if err != nil {
			return
		}
	}
}
