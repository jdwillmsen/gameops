// Package colosseum carries the larger Colosseum's plan inside the binary: the
// unit image has no Python, so the generator's output is committed in
// compressed form and a test fails when it drifts from the generator.
package colosseum

//go:generate sh regen.sh

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"sync"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/repair"
)

//go:embed colosseum.cmds.gz
var planGz []byte

//go:embed layout.json
var layoutJSON []byte

var (
	once sync.Once
	plan []repair.Op
	perr error
)

// Plan is the clear commands followed by the build commands, the order the
// console applies them in. The slice is shared and must not be changed.
func Plan() ([]repair.Op, error) {
	once.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(planGz))
		if err != nil {
			perr = err
			return
		}
		defer zr.Close()
		plan, perr = repair.ParseOps(zr)
	})
	return plan, perr
}

// Layout is the arena the plan builds.
func Layout() (arena.Layout, error) { return arena.ParseLayout(layoutJSON) }
