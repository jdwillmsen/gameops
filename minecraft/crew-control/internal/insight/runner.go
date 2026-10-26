package insight

import (
	"context"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

const (
	// Tick is how often the rules run. Twenty series are cheap; ten seconds
	// keeps a stalled unit visible within one poll of the console.
	Tick = 10 * time.Second

	// lookback must cover the baseline and recent windows plus the history R3
	// needs for its per-template durations.
	lookback = 45 * time.Minute

	pageSize = 200
	maxPages = 50
)

// Runner evaluates the rules on a tick against the event store.
type Runner struct {
	Store   events.Store
	Cfg     Config
	Tracker *Tracker
	Now     func() time.Time
	// Changed is called after an evaluation that changed the insight list.
	Changed func()
}

func NewRunner(s events.Store, t *Tracker) *Runner {
	return &Runner{Store: s, Cfg: DefaultConfig(), Tracker: t, Now: time.Now, Changed: func() {}}
}

// Run blocks until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	tk := time.NewTicker(Tick)
	defer tk.Stop()
	for {
		r.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// Once runs one evaluation. A store that cannot be read leaves the insights
// as they were: not knowing is not the same as everything being fine.
func (r *Runner) Once(ctx context.Context) {
	now := r.Now()
	evs, err := r.load(ctx, now)
	if err != nil {
		return
	}
	open, history := Derive(evs)
	fs := Evaluate(r.Cfg, Input{Now: now, Events: evs, Open: open}, history)
	if r.Tracker.Apply(now, fs) {
		r.Changed()
	}
}

func (r *Runner) load(ctx context.Context, now time.Time) ([]unitlog.Event, error) {
	q := events.Query{Since: now.Add(-lookback), Until: now, Limit: pageSize, Asc: true}
	var out []unitlog.Event
	for i := 0; i < maxPages; i++ {
		page, err := r.Store.Query(ctx, q)
		if err != nil {
			return nil, err
		}
		out = append(out, unitlog.FromAll(page.Events)...)
		if page.Next == nil {
			break
		}
		q.Cursor = *page.Next
	}
	return out, nil
}

// Derive finds the tasks still running and the finished durations per
// template. Idle roll-ups are not tasks.
func Derive(evs []unitlog.Event) ([]OpenTask, map[string][]time.Duration) {
	started := map[string]unitlog.Event{}
	order := []string{}
	history := map[string][]time.Duration{}
	for _, e := range evs {
		switch e.Kind {
		case unitlog.KindTaskStarted:
			if e.Task != "" && !e.Idle {
				if _, ok := started[e.Task]; !ok {
					order = append(order, e.Task)
				}
				started[e.Task] = e
			}
		case unitlog.KindTaskDone, unitlog.KindTaskFailed, "task_cancelled", "task_ended":
			delete(started, e.Task)
			if e.Kind == unitlog.KindTaskDone && !e.Idle && e.Template != "" && e.Duration > 0 {
				history[e.Template] = append(history[e.Template], e.Duration)
			}
		}
	}
	var open []OpenTask
	for _, id := range order {
		if st, ok := started[id]; ok {
			open = append(open, OpenTask{StartID: st.ID, Unit: st.Unit, Template: st.Template, Since: st.At})
		}
	}
	return open, history
}
