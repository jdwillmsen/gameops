// Package pack holds the behaviour pack that reports live positions from
// inside the game server. It is plain files with no build step, so what is
// in this directory is exactly what the server loads.
package pack

import "embed"

// Named one by one so that nothing else in this directory can reach a world
// by being dropped here.
//
//go:embed manifest.json scripts/main.js
var FS embed.FS
