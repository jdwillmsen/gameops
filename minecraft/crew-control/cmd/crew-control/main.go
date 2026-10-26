// Command crew-control serves the public console for the Crew units, proxies
// an allow-list of each unit's camera routes, and queues the work that
// signed-in operators give the units.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/alerts"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/capacity"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/console"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/detail"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/explain"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/explorer"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/export"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/heat"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/insight"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/invhistory"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/jobs"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/metrics"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/proxy"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/replay"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
	"github.com/jdwillmsen/gameops/minecraft/internal/squad"
)

// Stamped by the Dockerfile at build time.
var version = "dev"

const (
	shutdownTimeout = 10 * time.Second
	pollEvery       = time.Second

	pruneAfter     = 30 * 24 * time.Hour
	pruneEvery     = 24 * time.Hour
	connectTimeout = 5 * time.Second
	maxRetryWait   = 30 * time.Second

	// worldReadTimeout bounds one read of the world save behind the unit
	// detail view; an operator's request waits it out, no longer.
	worldReadTimeout = 10 * time.Second
	defaultWorldView = "/tmp/crew-worldview"
)

func main() {
	log := logging.New(os.Getenv("LOG_LEVEL"))

	units, err := roster.Parse(os.Getenv("CREW_UNITS"))
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8080"
	}

	poller := telemetry.NewPoller(units, &http.Client{}, pollEvery)

	dsn, err := postgresDSN()
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	xboxLogin, err := auth.XboxConfigFromEnv(os.Getenv)
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	operators, err := auth.New(auth.Config{
		Xbox:       xboxLogin,
		Origin:     os.Getenv("PUBLIC_ORIGIN"),
		DataDir:    os.Getenv("DATA_DIR"),
		Operators:  os.Getenv("CREW_OPERATORS"),
		InvitesDir: os.Getenv("CREW_INVITES_DIR"),
		Log:        log,
	})
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	if xboxLogin.Enabled && !operators.Enabled() {
		log.Warn("xbox_login_ignored", logging.Fields{"effect": "sign-in is off, so Xbox sign-in is too; set PUBLIC_ORIGIN and CREW_OPERATORS"})
	}
	if !operators.Enabled() {
		log.Warn("auth_disabled", logging.Fields{"effect": "no /auth routes; every command route answers 401; set PUBLIC_ORIGIN and CREW_OPERATORS"})
	}
	places, err := ledger.ParseSeed(os.Getenv("CREW_PLACES"))
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	saves, err := saveSource(log)
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	store := &work.Lazy{}
	// Set once the backend is ready; until then reports cannot be applied
	// either, so there is nothing for it to miss.
	var world atomic.Pointer[ledger.Ledger]
	// Set with the store; a report that finishes work before then has no job
	// to wake, and the scheduler's first tick reads the store anyway.
	var sched atomic.Pointer[jobs.Scheduler]
	// The event history is filled from the poller. It never goes dark: until
	// the database answers, and whenever it stops, a bounded memory store
	// serves and the metric says so.
	eventFallback := events.NewFallback(log)
	eventLog := &events.Lazy{}
	eventLog.Set(eventFallback)
	recorder := events.NewWriter(eventLog, log)
	ids := make([]string, 0, len(units.Units()))
	for _, u := range units.Units() {
		ids = append(ids, u.ID)
	}
	fleet := metrics.New(ids)
	fleet.WatchEventStore(eventFallback)
	alertCfg, err := alerts.ParseConfig(os.Getenv("CREW_ALERTS"))
	if err != nil {
		log.Error("config_invalid", logging.Fields{"error": err.Error()})
		os.Exit(1)
	}
	alertEngine := alerts.NewEngine(alertCfg, time.Now())
	inventory := invhistory.NewTracker()
	poller.OnPoll = func(now time.Time, states []telemetry.UnitState) {
		fleet.ObservePoll(now, states)
		alertEngine.Poll(now, poller.Snapshot(), states)
		for _, id := range ids {
			lv, _ := poller.Live(id)
			if ev, ok := inventory.Observe(now, id, lv.Online, lv.Inventory); ok {
				recorder.Add(ev, events.Hint{})
			}
		}
	}
	poller.OnEvents = func(batch []telemetry.Born) {
		fleet.Observe(batch)
		alertEngine.Observe(batch)
		for _, b := range batch {
			recorder.Add(bornEvent(b))
		}
	}
	store.OnReady(poller.PublishAllWork)
	workToken := os.Getenv("WORK_TOKEN")
	if workToken == "" {
		log.Warn("work_token_unset", logging.Fields{"effect": "unit work routes are not registered"})
	}
	// Squad state is in-memory only and rebuilt from units' hellos, so
	// unlike the work store it is ready the moment the process is.
	squadStore := squad.NewStore()
	insights := insight.NewTracker()

	// No WriteTimeout: it would cut the camera websocket and the status
	// stream, which are long-lived by design.
	// Shutdown waits for active requests, and an SSE stream never ends on its
	// own; cancelling the base context when shutdown starts ends them at once.
	streamCtx, endStreams := context.WithCancel(context.Background())
	defer endStreams()
	heatmap := heat.New()
	// Like the event history, replay never goes dark: memory serves until
	// the database answers.
	positions := replay.NewFallback(log)
	srv := &http.Server{
		Addr: addr,
		Handler: newHandler(units, poller, saves, operators.Operator, log, operators.Mount, func(mux *http.ServeMux) {
			api := work.Routes(mux, store, units, workToken, poller.PublishWork)
			api.MountOperator(mux, operators)
			api.OnSeen = observeSeen(&world, log)
			// Wired as a no-op: the builds jobs that compare an observation
			// to a blueprint replace this without touching the routes.
			api.OnBlocks = func(string, uuid.UUID, work.Blocks) {}
			api.OnTerminal = chainWake(api.OnTerminal, &sched)
			squadMount := squad.Routes(mux, squadStore, units, workToken)
			squadMount.MountOperator(mux, squadGate{operators})

			alerts.NewAPI(alertEngine).MountOperator(mux, operators)

			poller.SetWorkSource(api.Feed)
			events.NewAPI(eventLog).MountOperator(mux, operators)
			explorer.NewAPI(explorer.StoreSource{Store: eventLog}, explorer.NewMemoryViews(), operators).Mount(mux)
			heatmap.MountOperator(mux, operators, units.Has)
			replay.NewAPI(positions).MountOperator(mux, operators, units.Has)
			explain.NewAPI(eventLog).Mount(mux, operators)
			export.NewAPI(eventLog, exportAudit(log)).MountOperator(mux, operators)
			invhistory.NewAPI(eventLog, units.Has).Mount(mux, operators)
			capacity.NewAPI(eventLog).Mount(mux, operators)
			insight.NewAPI(insights).Mount(mux, operators)
			ledger.NewAPI(func() ledger.Ledger {
				if l := world.Load(); l != nil {
					return *l
				}
				return nil
			}).MountOperator(mux, operators)
			jobAPI := jobs.NewAPI(func() (ledger.Ledger, *jobs.Scheduler) {
				l := world.Load()
				if l == nil {
					return nil, nil
				}
				return *l, sched.Load()
			}, poller.PublishJobs)
			jobAPI.MountOperator(mux, operators)
			poller.SetJobsSource(jobAPI.Feed)
		}, mountMetrics(fleet)),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return streamCtx },
	}
	srv.RegisterOnShutdown(endStreams)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go heatmap.Run(ctx, poller, heatEvery(log))
	go replay.NewRecorder(positions).Run(ctx, poller, replay.DefaultEvery)

	pollDone := make(chan struct{})
	go func() { poller.Run(ctx); close(pollDone) }()

	insightDone := make(chan struct{})
	go func() { insight.NewRunner(eventLog, insights).Run(ctx); close(insightDone) }()

	recordDone := make(chan struct{})
	go func() { recorder.Run(ctx); close(recordDone) }()

	storeDone := make(chan struct{})
	go func() {
		openStore(ctx, store, &world, &sched, eventLog, poller, places, dsn, os.Getenv("WORK_STORE") == "memory", log, func(pool *pgxpool.Pool) {
			go alerts.Persist(ctx, alertEngine, alerts.NewPostgres(pool), log)
			positions.SetPrimary(replay.NewPostgres(pool))
			go prunePositions(ctx, positions, log)
		})
		close(storeDone)
	}()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", logging.Fields{"version": version, "addr": addr, "units": len(units.Units())})

	select {
	case err := <-errc:
		log.Error("serve_failed", logging.Fields{"error": err.Error()})
		os.Exit(1)
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("shutdown_incomplete", logging.Fields{"error": err.Error()})
	}
	<-pollDone
	<-recordDone
	<-insightDone
	<-storeDone
}

// mountMetrics serves /metrics on the listener the cluster scrapes. It is
// registered like any other route and so is reachable by anything that can
// reach the pod; keeping it off the public host is the HTTPRoute's job, which
// lists paths and does not list this one.
func mountMetrics(f *metrics.Fleet) func(*http.ServeMux) {
	return func(mux *http.ServeMux) { mux.Handle("GET /metrics", f.Handler()) }
}

// squadGate adapts the console's auth to the squad routes' gate, which asks
// only for the signed-in operator's id: the shared squad package cannot
// name crew-control's auth types.
type squadGate struct{ a *auth.Auth }

func (g squadGate) Require(h http.Handler) http.Handler { return g.a.Require(h) }

func (g squadGate) Operator(r *http.Request) (string, bool) {
	op, ok := g.a.Operator(r)
	return op.ID, ok
}

// saveSource reads the world save behind the unit detail route. Unset, the
// route still answers with what the live poll holds and says the save is
// off; a wrong view directory or interval is a config error, like anywhere
// else.
func saveSource(log *logging.Logger) (detail.Source, error) {
	dir := os.Getenv("CREW_WORLD_DB")
	if dir == "" {
		return nil, nil
	}
	every := detail.DefaultEvery
	if v := os.Getenv("CREW_WORLD_EVERY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second {
			return nil, fmt.Errorf("CREW_WORLD_EVERY %q is not a duration of a second or more", v)
		}
		every = d
	}
	work := os.Getenv("CREW_WORLD_WORK")
	if work == "" {
		work = defaultWorldView
	}
	return &detail.Save{WorldDir: dir, WorkDir: work, Every: every, Timeout: worldReadTimeout, Log: log}, nil
}

// postgresDSN is empty when PG_HOST is unset. The password is read apart from
// the host so it can come from a Secret; url.UserPassword escapes it.
func postgresDSN() (string, error) {
	host := os.Getenv("PG_HOST")
	if host == "" {
		return "", nil
	}
	port := 5432
	if v := os.Getenv("PG_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("PG_PORT %q is not a port", v)
		}
		port = n
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(os.Getenv("PG_USERNAME"), os.Getenv("PG_PASSWORD")),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + os.Getenv("PG_DATABASE"),
		RawQuery: "sslmode=require",
	}
	return u.String(), nil
}

// openStore fills store once a backend is ready, then prunes it until ctx
// ends. A database that is down at start must not stop the console, so the
// work routes answer 503 while this retries.
//
// The memory store needs an explicit opt-in: a production deployment that
// lost its PG_HOST would otherwise run on a queue that vanishes on restart,
// and nothing would say so. Unconfigured behaves like a database that is down.
func openStore(ctx context.Context, store *work.Lazy, world *atomic.Pointer[ledger.Ledger], sched *atomic.Pointer[jobs.Scheduler], eventLog *events.Lazy, poller *telemetry.Poller, places []ledger.Place, dsn string, memory bool, log *logging.Logger, onPool ...func(*pgxpool.Pool)) {
	var (
		backend work.Store
		led     ledger.Ledger
		js      jobs.Store
	)
	switch {
	case dsn == "" && memory:
		log.Info("work_store_memory", nil)
		backend = work.NewMemory()
		led = ledger.NewMemory()
		js = jobs.NewMemory()
		eventLog.Connect(events.NewMemory())
	case dsn == "":
		log.Error("work_store_unconfigured", logging.Fields{"effect": "work routes answer 503; set PG_HOST, or WORK_STORE=memory for development"})
		return
	default:
		wait := time.Second
		warned := false
		for backend == nil {
			pg, err := work.OpenPostgres(ctx, dsn, connectTimeout)
			if err == nil {
				defer pg.Close()
				log.Info("work_store_ready", nil)
				backend = pg
				led = ledger.NewPostgres(pg.Pool())
				js = jobs.NewPostgres(pg.Pool())
				eventLog.Connect(events.NewPostgres(pg.Pool()))
				for _, f := range onPool {
					f(pg.Pool())
				}
				break
			}
			if ctx.Err() != nil {
				return
			}
			if !warned {
				warned = true
				log.Error("work_store_unavailable", logging.Fields{"error": err.Error()})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = min(wait*2, maxRetryWait)
		}
	}
	if places != nil {
		seedCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		err := led.Seed(seedCtx, places)
		cancel()
		if err != nil {
			log.Error("places_seed_failed", logging.Fields{"error": err.Error()})
		} else {
			log.Info("places_seeded", logging.Fields{"count": len(places)})
		}
	}
	world.Store(&led)
	store.Set(backend)

	units := func(ctx context.Context) ([]ledger.UnitStatus, error) {
		return unitStatuses(ctx, poller.Snapshot(), backend)
	}
	checks := jobs.DefaultChecks()
	checks[jobs.KindStock] = jobs.StockCheck(led, units, time.Now)
	checks[jobs.KindCraft] = jobs.CraftCheck(led, units)
	scheduler := jobs.New(jobs.Config{
		Jobs: js, Work: backend, Checks: checks, Log: log, Units: units,
		Notify:     func(uuid.UUID) { poller.PublishJobs() },
		NotifyWork: poller.PublishWork,
	})
	sched.Store(scheduler)
	// Streams that opened before the store was ready have no job list yet.
	poller.PublishJobs()
	schedDone := make(chan struct{})
	go func() { scheduler.Run(ctx); close(schedDone) }()
	defer func() { <-schedDone }()

	for {
		n, err := backend.Prune(ctx, time.Now().Add(-pruneAfter))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn("work_prune_failed", logging.Fields{"error": err.Error()})
		} else {
			log.Info("work_pruned", logging.Fields{"rows": n})
		}
		// History keeps at least a month; the same loop and cadence as the
		// queue, so one pass a day prunes the whole append-only side of the
		// database.
		if rep, err := eventLog.Prune(ctx, time.Now().Add(-events.MinRetention), 0, false); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn("event_prune_failed", logging.Fields{"error": err.Error()})
		} else if rep.Pruned > 0 {
			log.Info("event_pruned", logging.Fields{"rows": rep.Pruned})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pruneEvery):
		}
	}
}

// chainWake makes a finished job item wake the scheduler after the hook that
// was already there. The scheduler is a hint only; it re-reads the store.
func chainWake(prev func(work.Item), sched *atomic.Pointer[jobs.Scheduler]) func(work.Item) {
	return func(it work.Item) {
		if prev != nil {
			prev(it)
		}
		if s := sched.Load(); s != nil {
			s.Wake()
		}
	}
}

// exportAudit records each event-window export as one log line: the operator,
// the window id and bounds, and nothing of the window's content.
func exportAudit(log *logging.Logger) func(export.Audit) {
	return func(a export.Audit) {
		log.Info("event_window_exported", logging.Fields{"operator": a.Operator, "window_id": a.WindowID,
			"unit": a.Unit, "job": a.Job, "from": a.From.Format(time.RFC3339), "to": a.To.Format(time.RFC3339), "lines": a.Lines})
	}
}

// bornEvent maps a polled event onto its history row, and the hint the idle
// rule reads. The feed's text rides in the payload, with the template and
// duration where known; a task id the queue would not recognise stays in it
// too: history keeps what it was told, not only what it can join.
func bornEvent(b telemetry.Born) (events.Event, events.Hint) {
	payload := map[string]any{"text": b.Text}
	if b.Template != "" {
		payload["template"] = b.Template
	}
	if b.Took > 0 {
		payload["duration_ms"] = b.Took.Milliseconds()
	}
	e := events.Event{Unit: b.Unit, At: b.At, Kind: b.Kind, Severity: b.Level}
	if id, err := uuid.Parse(b.Task); err == nil {
		e.TaskID = &id
	} else if b.Task != "" {
		payload["task"] = b.Task
	}
	e.Payload, _ = json.Marshal(payload)
	if b.Pos.Known {
		e.Pos = &events.Pos{X: float64(b.Pos.X), Y: float64(b.Pos.Y), Z: float64(b.Pos.Z)}
	}
	h := events.Hint{Task: b.Task, Template: b.Template, Took: b.Took, ReasonCode: b.ReasonCode}
	events.Enrich(&e, h)
	return e, h
}

// unitStatuses joins telemetry with each unit's unfinished work, in roster order.
func unitStatuses(ctx context.Context, snap []telemetry.Status, store work.Store) ([]ledger.UnitStatus, error) {
	open := make(map[string][]work.Item, len(snap))
	for _, s := range snap {
		items, err := store.Open(ctx, s.ID)
		if err != nil {
			return nil, err
		}
		open[s.ID] = items
	}
	return ledger.Statuses(snap, open), nil
}

// observeSeen records what a unit saw. It runs on the report request, so it
// is bounded; a lost observation costs a stale stock row until the next look.
// The log names the place and unit only, never where it is or what it holds.
func observeSeen(world *atomic.Pointer[ledger.Ledger], log *logging.Logger) func(string, work.Seen) {
	return func(unit string, s work.Seen) {
		l := world.Load()
		if l == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		pos := ledger.Pos{X: s.Chest.X, Y: s.Chest.Y, Z: s.Chest.Z}
		if err := (*l).Observe(ctx, unit, s.At, pos, s.Items, s.Partial); err != nil {
			log.Warn("stock_observe_failed", logging.Fields{"unit": unit, "error": err.Error()})
			return
		}
		// Only a configured place's id is logged: an unnamed chest's id spells
		// out its coordinates, and an operator-named one is not ours to vouch for.
		place := "unnamed"
		if p, ok, err := (*l).ChestAt(ctx, pos); err == nil && ok && p.Seeded {
			place = p.ID
		}
		log.Info("stock_observed", logging.Fields{"place": place, "unit": unit})
	}
}

// who reports whether a request carries a valid operator session; it decides
// once per stream whether work events are sent.
func newHandler(units roster.Roster, poller *telemetry.Poller, saves detail.Source, who func(*http.Request) (auth.Operator, bool), log *logging.Logger, mounts ...func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	for _, m := range mounts {
		m(mux)
	}
	mux.HandleFunc("GET /v1/units/stream", poller.StreamHandler(func(r *http.Request) bool {
		_, ok := who(r)
		return ok
	}))
	console.Mount(mux)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// The proxy bypasses the mux, which would answer an unclean path with a
	// redirect before the proxy could refuse it.
	unitProxy := proxy.Handler(units, log)
	// The detail route is crew-control's own, not a unit's, so it is served
	// here rather than added to the proxy's allow-list, which is public.
	unitDetail := detail.Handler(units, poller.Live, saves, gateFunc(who))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/units/") {
			// Only this exact spelling is crew-control's; anything else
			// under /units/ goes to the proxy, which answers what is not
			// on its allow-list with 404. No mux is involved, so the
			// unit's id is taken out of the path here.
			if strings.HasSuffix(r.URL.Path, "/detail") {
				r.SetPathValue("id", strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/units/"), "/detail"))
				unitDetail.ServeHTTP(w, r)
				return
			}
			unitProxy.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// gateFunc adapts the operator check to the detail route's gate.
type gateFunc func(*http.Request) (auth.Operator, bool)

func (f gateFunc) Operator(r *http.Request) (auth.Operator, bool) { return f(r) }

// prunePositions holds replay history to the event history's retention, on the
// same daily cadence as the other prunes.
func prunePositions(ctx context.Context, s replay.Store, log *logging.Logger) {
	for {
		if n, err := s.Prune(ctx, time.Now().Add(-events.MinRetention)); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn("position_prune_failed", logging.Fields{"error": err.Error()})
		} else if n > 0 {
			log.Info("position_pruned", logging.Fields{"rows": n})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pruneEvery):
		}
	}
}

// heatEvery reads HEAT_SAMPLE; a bad or too-short value falls back to the
// default rather than stopping the service over a tuning knob.
func heatEvery(log *logging.Logger) time.Duration {
	v := os.Getenv("HEAT_SAMPLE")
	if v == "" {
		return heat.DefaultEvery
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < heat.MinEvery {
		log.Warn("heat_sample_ignored", logging.Fields{"value": v})
		return heat.DefaultEvery
	}
	return d
}
