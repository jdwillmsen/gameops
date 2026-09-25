package presence

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/jdwillmsen/minecraft-afk-bot/internal/logging"
	"github.com/jdwillmsen/minecraft-server-agent/presenceapi"
)

// Logger is the subset of *logging.Logger the reconciler writes to.
type Logger interface {
	Info(event string, fields logging.Fields)
	Warn(event string, fields logging.Fields)
	Error(event string, fields logging.Fields)
}

// Reconciler acts on the last desired state the agent gave it.
//
// It is fail-static on purpose: an agent outage, a bad token or a malformed
// answer leaves the bot doing whatever it was last told, because a bot that
// flapped with the agent's health would drop the farms every time the agent
// restarted.
type Reconciler struct {
	client   *Client
	log      Logger
	interval time.Duration
	version  string
	now      func() time.Time

	mu        sync.Mutex
	desired   presenceapi.State
	etag      string
	connected bool
	// changed is closed and replaced on every change of desired, which is
	// how any number of waiters hear about it without missing one.
	changed chan struct{}

	// Touched only from Run's goroutine.
	fetchStreak  streak
	reportStreak streak
}

func NewReconciler(c *Client, initial presenceapi.State, interval time.Duration, version string, log Logger) *Reconciler {
	return &Reconciler{
		client:       c,
		log:          log,
		interval:     interval,
		version:      version,
		now:          time.Now,
		desired:      initial,
		changed:      make(chan struct{}),
		fetchStreak:  streak{event: "presence_fetch"},
		reportStreak: streak{event: "presence_report"},
	}
}

// Run polls immediately and then every interval until ctx ends.
func (r *Reconciler) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		r.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	r.poll(ctx)
	r.report(ctx)
}

func (r *Reconciler) poll(ctx context.Context) {
	r.mu.Lock()
	etag := r.etag
	r.mu.Unlock()

	res, err := r.client.Fetch(ctx, etag)
	if err != nil {
		if ctx.Err() == nil {
			r.fetchStreak.failed(r.log, err, r.Desired())
		}
		return
	}
	r.fetchStreak.succeeded(r.log)
	if !res.NotModified {
		r.set(res.Presence.Effective, res.ETag)
	}
}

func (r *Reconciler) report(ctx context.Context) {
	// LastSeen is advisory: the agent stamps it with its own clock on receipt.
	r.mu.Lock()
	st := presenceapi.Status{
		Connected:      r.connected,
		ObservedState:  r.desired,
		LastSeen:       r.now().UTC(),
		ProcessVersion: r.version,
	}
	r.mu.Unlock()

	if err := r.client.Report(ctx, st); err != nil {
		if ctx.Err() == nil {
			r.reportStreak.failed(r.log, err, st.ObservedState)
		}
		return
	}
	r.reportStreak.succeeded(r.log)
}

func (r *Reconciler) set(state presenceapi.State, etag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.etag = etag
	if state == r.desired {
		return
	}
	r.log.Info("presence_changed", logging.Fields{"from": string(r.desired), "to": string(state)})
	r.desired = state
	close(r.changed)
	r.changed = make(chan struct{})
}

// Desired is the state the bot is acting on.
func (r *Reconciler) Desired() presenceapi.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.desired
}

func (r *Reconciler) Connected(c bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connected = c
}

func (r *Reconciler) Admit(ctx context.Context) (context.Context, context.CancelFunc, error) {
	for {
		// Checked before the desired state so a caller that raced shutdown
		// against a present bot still gets ctx.Err() rather than a session
		// built on an already-cancelled context -- matching AlwaysPresent.
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		r.mu.Lock()
		state, changed := r.desired, r.changed
		r.mu.Unlock()

		if state == presenceapi.StatePresent {
			sess, cancel := context.WithCancel(ctx)
			go r.cancelOnPark(sess, cancel, changed)
			return sess, cancel, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
}

// cancelOnPark starts from the change channel read together with the state
// that admitted the session, so a park landing between admission and this
// goroutine starting is still seen.
func (r *Reconciler) cancelOnPark(sess context.Context, cancel context.CancelFunc, changed <-chan struct{}) {
	for {
		select {
		case <-sess.Done():
			return
		case <-changed:
		}
		r.mu.Lock()
		state, next := r.desired, r.changed
		r.mu.Unlock()
		if state == presenceapi.StateParked {
			cancel()
			return
		}
		changed = next
	}
}

// streak logs a failure when it starts or changes kind, and once when it
// ends, so a poll every ten seconds against a dead agent writes two lines
// rather than one per poll.
type streak struct {
	event string
	kind  string
}

func (s *streak) failed(log Logger, err error, actingOn presenceapi.State) {
	kind, status := classify(err)
	if kind == s.kind {
		return
	}
	s.kind = kind
	fields := logging.Fields{"error": err.Error(), "acting_on": string(actingOn)}
	if status != 0 {
		fields["status"] = status
	}
	if kind == "unreachable" {
		log.Warn(s.event+"_unreachable", fields)
		return
	}
	// Rejected and invalid answers mean a wrong token, an unregistered actor
	// or an agent bug: waiting will not fix them, so they are errors.
	log.Error(s.event+"_"+kind, fields)
}

func (s *streak) succeeded(log Logger) {
	if s.kind == "" {
		return
	}
	s.kind = ""
	log.Info(s.event+"_recovered", nil)
}

func classify(err error) (kind string, status int) {
	var he *HTTPError
	switch {
	case errors.Is(err, ErrInvalidResponse):
		return "invalid", 0
	case errors.As(err, &he) && (he.StatusCode == http.StatusRequestTimeout || he.StatusCode == http.StatusTooManyRequests):
		// Unlike the rest of 4xx below, these mean the agent wants the bot to
		// slow down or retry, not that the token or actor is wrong -- waiting
		// can fix them, so they warn as unreachable rather than error.
		return "unreachable", he.StatusCode
	case errors.As(err, &he) && he.StatusCode >= 400 && he.StatusCode < 500:
		return "rejected", he.StatusCode
	case errors.As(err, &he):
		return "unreachable", he.StatusCode
	default:
		return "unreachable", 0
	}
}

var _ Gate = (*Reconciler)(nil)
