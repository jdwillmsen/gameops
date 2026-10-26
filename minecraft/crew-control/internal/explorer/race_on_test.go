//go:build race

package explorer

import "time"

// The race detector slows a scan by an order of magnitude, so the budget is
// only enforced at full speed; the bound here just catches a hang.
const budget = 3 * time.Second
