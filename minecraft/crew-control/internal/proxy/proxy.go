// Package proxy exposes an allow-list of each unit's camera routes and
// nothing else, so the public console cannot reach a unit's task or metrics
// endpoints.
package proxy

import (
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

var cameraFiles = []string{
	"ws", "mesher.js", "entities.js", "entities.png", "entities.json",
	"atlas.png", "atlas.json", "items.png", "items.json", "stats",
}

// Handler serves only the allow-listed GET routes under /units/{id}/.
func Handler(r roster.Roster, log *logging.Logger) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			unit, ok := r.Get(pr.In.PathValue("id"))
			if !ok {
				// Unreachable: the route guard has already checked the unit.
				return
			}
			pr.SetURL(unit.URL)
			// Strip the /units/{id} prefix; the mux has matched the rest
			// against the allow-list, so it is safe to forward verbatim.
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/units/"+unit.ID)
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = ""
			// A unit accepts its camera websocket with the default origin
			// check, which rejects an Origin that differs from Host. The
			// browser's Origin is the console's host, so the unit must see
			// that same Host rather than its own.
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Forwarded-Server", "Forwarded"} {
				pr.Out.Header.Del(h)
			}
			pr.SetXForwarded()
		},
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
			ResponseHeaderTimeout: 5 * time.Second,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			log.Warn("proxy_upstream_failed", logging.Fields{
				"unit":  req.PathValue("id"),
				"error": err.Error(),
			})
			http.Error(w, "unit unavailable", http.StatusBadGateway)
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/units/{id}/camera", guard(r, rp))
	mux.HandleFunc("/units/{id}/v1/state", guard(r, rp))
	for _, f := range cameraFiles {
		mux.HandleFunc("/units/{id}/v1/camera/"+f, guard(r, rp))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// ServeMux would answer an unclean path with a redirect to its
		// cleaned form; refuse instead so no spelling of a path is rewritten
		// into a different one.
		if req.URL.Path != path.Clean(req.URL.Path) {
			http.NotFound(w, req)
			return
		}
		mux.ServeHTTP(w, req)
	})
}

func guard(r roster.Roster, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		// Method is checked here rather than in the mux pattern, whose
		// mismatch answer is 405 rather than the 404 of any other refusal.
		if _, ok := r.Get(req.PathValue("id")); !ok || req.Method != http.MethodGet {
			http.NotFound(w, req)
			return
		}
		next.ServeHTTP(w, req)
	}
}
