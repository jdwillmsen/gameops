// Package blueprints embeds the checked-in crew blueprints, so binaries
// stamp them without a repository checkout — the nightly job's image has
// none.
package blueprints

import "embed"

//go:embed *.json
var Files embed.FS
