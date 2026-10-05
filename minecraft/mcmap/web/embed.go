// Package web holds the map page. It is plain files with no build step, so
// what is in this directory is exactly what the browser gets.
package web

import "embed"

//go:embed index.html app.js live.js markers.js style.css lib
var FS embed.FS
