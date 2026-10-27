// Package proxy exposes an allow-list of each unit's camera routes and
// nothing else, so the public console cannot reach a unit's task or metrics
// endpoints.
package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

var cameraFiles = []string{
	"ws", "mesher.js", "entities.js", "entities.png", "entities.json",
	"atlas.png", "atlas.json", "items.png", "items.json", "sky.js", "stats",
}

// Handler serves only the allow-listed GET routes under /units/{id}/.
func Handler(r roster.Roster, log *logging.Logger) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			unit, ok := r.Get(pr.In.PathValue("id"))
			if !ok {
				// The route guard has already checked the unit; if that ever
				// breaks, an empty host makes the transport fail with a 502
				// rather than send the inbound URL anywhere.
				pr.Out.URL.Scheme = "http"
				pr.Out.URL.Host = ""
				return
			}
			pr.SetURL(unit.URL)
			// Strip the /units/{id} prefix; the mux has matched the rest
			// against the allow-list, so it is safe to forward verbatim.
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/units/"+unit.ID)
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = cameraQuery(pr.In)
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
			// A viewer closing its tile cancels the request; that is not an
			// upstream fault.
			if errors.Is(err, context.Canceled) {
				return
			}
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

var (
	radiusPattern = regexp.MustCompile(`^[0-9]{1,3}$`)
	wirePattern   = regexp.MustCompile(`^[0-9]{1,2}$`)
)

// cameraQuery forwards only the view radius and the wire version the page
// asks for, and only to the websocket: they are the query parameters the
// camera reads, and anything else a visitor supplies must not reach a unit.
// Without the wire version a unit answers every viewer with its oldest
// format, however new the page.
func cameraQuery(in *http.Request) string {
	if !strings.HasSuffix(in.URL.Path, "/v1/camera/ws") {
		return ""
	}
	q := url.Values{}
	if r := in.URL.Query().Get("r"); radiusPattern.MatchString(r) {
		q.Set("r", r)
	}
	if w := in.URL.Query().Get("wire"); wirePattern.MatchString(w) {
		q.Set("wire", w)
	}
	return q.Encode()
}
