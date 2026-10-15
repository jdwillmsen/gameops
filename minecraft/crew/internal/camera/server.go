package camera

import (
	"bytes"
	"compress/flate"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

//go:embed web/index.html
var page []byte

type msg struct {
	text bool
	data []byte
}

type client struct {
	out  chan msg
	dead bool // guarded by Hub.mu, which every send holds
}

// send never blocks the packet loop: a viewer that falls 4096 messages behind
// is dropped, since replaying world state on reconnect is cheap.
func (c *client) send(m msg) {
	if c.dead {
		return
	}
	select {
	case c.out <- m:
	default:
		select {
		case <-c.out:
		default:
		}
		c.dead = true
		close(c.out)
	}
}

// Mount adds the camera routes to the status API handler.
func Mount(base http.Handler, h *Hub) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("GET /camera", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /v1/camera/stats", func(w http.ResponseWriter, _ *http.Request) {
		h.mu.Lock()
		n, ne, nv := len(h.subs), len(h.ents), len(h.clients)
		h.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub_chunks_held": n, "entities": ne, "viewers": nv,
			"sub_chunks_decoded": h.SubChunks.Load(), "decode_failures": h.DecodeFail.Load(),
			"level_chunks": h.LevelChunks.Load(), "subs_requested": h.RequestedSubs.Load(), "sub_packets": h.SubPackets.Load(),
			"sub_ok": h.SubOK.Load(), "sub_all_air": h.SubAllAir.Load(), "sub_other": h.SubOther.Load(),
			"last_decode_error": h.LastErr.Load(), "unmapped_block_lookups": h.Unmapped.Load(),
			"decode_ms_total": h.DecodeNs.Load() / 1e6,
			"raw_bytes_sent":  h.RawBytes.Load(), "deflated_bytes_sent": h.deflated.Load(),
		})
	})
	mux.HandleFunc("GET /v1/camera/ws", h.serveWS)
	return mux
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionContextTakeover,
		// The page is served from the same origin; anything else is refused.
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	c := &client{out: make(chan msg, 16384)}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	c.send(msg{text: true, data: mustJSON(map[string]any{"t": "materials", "list": h.mats.all()})})
	c.send(msg{text: true, data: mustJSON(h.me)})
	for _, e := range h.ents {
		c.send(msg{text: true, data: mustJSON(struct {
			T string `json:"t"`
			*entity
		}{"ent", e})})
	}
	for _, f := range h.subs {
		c.send(msg{data: f})
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
	}()

	ctx := conn.CloseRead(r.Context())
	for {
		select {
		case m, ok := <-c.out:
			if !ok {
				_ = conn.Close(websocket.StatusPolicyViolation, "too slow")
				return
			}
			typ := websocket.MessageBinary
			if m.text {
				typ = websocket.MessageText
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(wctx, typ, m.data)
			cancel()
			if err != nil {
				return
			}
			h.RawBytes.Add(uint64(len(m.data)))
			h.deflated.Add(deflatedSize(m.data))
		case <-ctx.Done():
			return
		}
	}
}

// deflatedSize approximates the wire size, since the browser negotiates
// permessage-deflate and the library does not expose bytes on the socket.
func deflatedSize(b []byte) uint64 {
	var buf bytes.Buffer
	fw, _ := flate.NewWriter(&buf, flate.BestSpeed)
	_, _ = fw.Write(b)
	_ = fw.Close()
	return uint64(buf.Len())
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
