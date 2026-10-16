// Package web holds the map page. It is plain files with no build step, so
// what is in this directory is exactly what the browser gets.
package web

import "embed"

//go:embed index.html about.html guide.html privacy.html terms.html credits.html pages.css settings.js diag.js appearance.js views.js app.js zoom.js duration.js names.js icons.js layers.js room.js groups.js heights.js playerdetails.js live.js markers.js structures.js biomes.js topography.js underground.js lost.js search.js menu.js compact.js debug.js passkey.js xbox.js slime.js chunk.js trails.js history.js levels.js style.css lib
var FS embed.FS

// Owner is who runs the map and holds the copyright in what is written
// here, and Contact is where to reach them: the game's usage guidelines do
// not accept a chat as the way to get in touch. The pages say {{owner}},
// {{copyright}} and {{contact}} where they mean them, and the server fills
// them in, so this is the one place to change.
const (
	Owner     = "Jake Willmsen"
	Copyright = "© 2026 " + Owner
	Contact   = "admin@jdwlabs.com"
)

// Pages are the routes of the pages that need no login, with the file each
// is served from.
var Pages = map[string]string{
	"/about":   "about.html",
	"/guide":   "guide.html",
	"/privacy": "privacy.html",
	"/terms":   "terms.html",
	"/credits": "credits.html",
}
