package alerts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

// A real poller feeding the engine the way main wires it: a unit that dies
// must be an alert well inside the 10 s the product promises, and clear when
// it respawns.
func TestDeathAlertFollowsRealPoller(t *testing.T) {
	var dead atomic.Bool
	unit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"state":{"dead":%t,"health_known":true,"health":20,"hunger_known":true,"hunger":3},"tasks":{}}`, dead.Load())
	}))
	defer unit.Close()
	r, err := roster.Parse(fmt.Sprintf(`[{"id":"crew-01","name":"Agate","url":%q}]`, unit.URL))
	if err != nil {
		t.Fatal(err)
	}
	p := telemetry.NewPoller(r, http.DefaultClient, 100*time.Millisecond)
	e := NewEngine(Defaults(), time.Now())
	p.OnEvents = e.Observe
	p.OnPoll = func(now time.Time, st []telemetry.UnitState) { e.Poll(now, p.Snapshot(), st) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	wait := func(id, state string) time.Duration {
		start := time.Now()
		for time.Since(start) < 10*time.Second {
			for _, a := range e.List() {
				if a.ID == id && a.State == state {
					return time.Since(start)
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s never %s", id, state)
		return 0
	}
	wait("low_food.crew-01", StateFiring)
	dead.Store(true)
	wait("unit_died.crew-01", StateFiring)
	dead.Store(false)
	wait("unit_died.crew-01", StateResolved)
}
