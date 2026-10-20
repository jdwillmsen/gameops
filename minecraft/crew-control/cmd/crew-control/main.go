// Command crew-control serves the public console for the Crew units, proxies
// an allow-list of each unit's camera routes, and queues the work that
// signed-in operators give the units.
package main

import (
	"context"
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
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/console"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/jobs"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/proxy"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
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
	operators, err := auth.New(auth.Config{
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
	if !operators.Enabled() {
		log.Warn("auth_disabled", logging.Fields{"effect": "no /auth routes; every command route answers 401; set PUBLIC_ORIGIN and CREW_OPERATORS"})
	}
	places, err := ledger.ParseSeed(os.Getenv("CREW_PLACES"))
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
	store.OnReady(poller.PublishAllWork)
	workToken := os.Getenv("WORK_TOKEN")
	if workToken == "" {
		log.Warn("work_token_unset", logging.Fields{"effect": "unit work routes are not registered"})
	}

	// No WriteTimeout: it would cut the camera websocket and the status
	// stream, which are long-lived by design.
	// Shutdown waits for active requests, and an SSE stream never ends on its
	// own; cancelling the base context when shutdown starts ends them at once.
	streamCtx, endStreams := context.WithCancel(context.Background())
	defer endStreams()
	srv := &http.Server{
		Addr: addr,
		Handler: newHandler(units, poller, operators.Operator, log, operators.Mount, func(mux *http.ServeMux) {
			api := work.Routes(mux, store, units, workToken, poller.PublishWork)
			api.MountOperator(mux, operators)
			api.OnSeen = observeSeen(&world, log)
			api.OnTerminal = chainWake(api.OnTerminal, &sched)
			poller.SetWorkSource(api.Feed)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return streamCtx },
	}
	srv.RegisterOnShutdown(endStreams)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pollDone := make(chan struct{})
	go func() { poller.Run(ctx); close(pollDone) }()

	storeDone := make(chan struct{})
	go func() {
		openStore(ctx, store, &world, &sched, poller, places, dsn, os.Getenv("WORK_STORE") == "memory", log)
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
	<-storeDone
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
func openStore(ctx context.Context, store *work.Lazy, world *atomic.Pointer[ledger.Ledger], sched *atomic.Pointer[jobs.Scheduler], poller *telemetry.Poller, places []ledger.Place, dsn string, memory bool, log *logging.Logger) {
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
	scheduler := jobs.New(jobs.Config{
		Jobs: js, Work: backend, Checks: checks, Log: log, Units: units,
		// Task 7 streams job changes; until then nothing listens.
		Notify: func(uuid.UUID) {},
	})
	sched.Store(scheduler)
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
		if err := (*l).Observe(ctx, unit, s.At, pos, s.Items); err != nil {
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
func newHandler(units roster.Roster, poller *telemetry.Poller, who func(*http.Request) (auth.Operator, bool), log *logging.Logger, mounts ...func(*http.ServeMux)) http.Handler {
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/units/") {
			unitProxy.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
