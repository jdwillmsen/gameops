// Package lab carries the lab scripts that a job image without a checkout
// must still run, so the generator binary can write them out beside its plans.
package lab

import "embed"

//go:embed courses.sh nightly.sh
var Scripts embed.FS
