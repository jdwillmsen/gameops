// Package web holds the map page. It is plain files with no build step, so
// what is in this directory is exactly what the browser gets.
package web

import "embed"

//go:embed index.html settings.js appearance.js views.js app.js duration.js names.js icons.js layers.js live.js markers.js structures.js biomes.js search.js menu.js compact.js slime.js chunk.js trails.js style.css lib
var FS embed.FS
