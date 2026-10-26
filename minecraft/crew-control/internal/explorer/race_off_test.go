//go:build !race

package explorer

import "time"

const budget = 200 * time.Millisecond
