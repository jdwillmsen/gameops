// Package console serves the static console page.
package console

import (
	"embed"
	"io/fs"
	"net/http"
)

// The explicit list keeps the node test files out of the binary.
//
//go:embed web/index.html web/signin.html web/signin.js web/signin-xbox.js web/enrol.js web/console.js web/state.js web/fleet.js web/ops.js web/squad.js web/alerts.js web/timeline.js web/timeline-ui.js web/explorer.js web/explorer-ui.js web/explorer.css web/overview.js web/overview-ui.js web/detail.js web/detail-ui.js web/containers.js web/containers-ui.js web/console.css web/overview.css web/detail.css web/containers.css web/alerts.css web/timeline.css web/charts/*.js web/charts/*.css web/charts/demo.html web/graph/*.js web/graph/*.css web/graph/demo.html
//go:embed web/dash.js web/dash-data.js web/dash-panels.js web/dash-ui.js web/dash.css
var assets embed.FS

const csp = "default-src 'self'; frame-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'"

// Mount registers the page, its static files and the redirect for the old
// /camera bookmark.
func Mount(mux *http.ServeMux) {
	web, err := fs.Sub(assets, "web")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(web)

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		secure(w)
		http.ServeFileFS(w, r, web, "index.html")
	})
	mux.HandleFunc("GET /signin", func(w http.ResponseWriter, r *http.Request) {
		secure(w)
		// Referrers must not expose the invite fragment to resources loaded by this page.
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.ServeFileFS(w, r, web, "signin.html")
	})
	mux.HandleFunc("GET /static/{file}", func(w http.ResponseWriter, r *http.Request) {
		secure(w)
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + r.PathValue("file")
		files.ServeHTTP(w, r2)
	})
	mux.HandleFunc("GET /static/charts/{file}", func(w http.ResponseWriter, r *http.Request) {
		secure(w)
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/charts/" + r.PathValue("file")
		files.ServeHTTP(w, r2)
	})
	mux.HandleFunc("GET /static/graph/{file}", func(w http.ResponseWriter, r *http.Request) {
		secure(w)
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/graph/" + r.PathValue("file")
		files.ServeHTTP(w, r2)
	})
	mux.HandleFunc("GET /camera", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
}

func secure(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
}
