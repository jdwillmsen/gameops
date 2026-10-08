// Package mcauth handles Xbox Live device-code authentication and caches
// the resulting token so the agent doesn't need an interactive login on
// every restart, built on gophertunnel's auth package.
//
// Where the token is cached is a Store, not a path: two agent processes
// coexist during a release, and only a cache both of them can read lets the
// standby finish its login before the handover. See store.go.
package mcauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/auth"
	"golang.org/x/oauth2"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// storeTimeout bounds one attempt to read or write the store from inside
// Token, which has no context of its own. Generous for a single row or a
// single file, and short relative to the hour an access token lasts, so a
// store that has gone away costs a refresh rather than the connection.
const storeTimeout = 5 * time.Second

// ErrStandbyUnwarmed means this process is not the live agent and has no
// usable token it may refresh into existence: what it loaded has expired and
// the store holds nothing newer, or nothing has ever been stored at all. The
// two collapse here deliberately -- neither is something a standby may act
// on, and only the live agent's first-run login tells them apart.
//
// The ordinary state of a standby that started more than an access token's
// lifetime after the live agent last rotated, not a failure: the live agent
// writes when the credential rotates, which a stable connection can go hours
// without doing. It costs the handover the one refresh the warm-up hoped to
// save, and it is reported so a reader can tell it from a store that broke.
var ErrStandbyUnwarmed = errors.New("mcauth: standby holds no unexpired token and may not refresh one")

// gatePoll is how often a device-code login in progress asks whether this
// process is still the one entitled to it. Negligible against the quarter of
// an hour a code lasts, and the gate is the only liveness this package has --
// it reports a state, not a moment it changed.
var gatePoll = time.Second

// rejectionCode is the OAuth error code Microsoft answers with when it
// refuses a login because of the account or the credential rather than the
// request: an abuse-mode hold, a refresh token it no longer honours, a
// sign-in it wants repeated. It is the only code this agent has ever been
// refused with, so it is the only one IsRejection recognises.
const rejectionCode = "invalid_grant"

// The wordings Microsoft has actually refused this agent with, matched on
// text because both arrive under the one code. They call for opposite
// responses, and a rejection worded any other way gets neither: it is paced
// like a hold and never answered with a prompt, which is what every rejection
// got before the two were told apart.
const (
	// The account's standing. A sign-in is refused under it exactly as a
	// refresh is, and each attempt is what the hold is counting.
	abuseHoldText = "abuse mode"
	// A person has to do something. Seen both for a refresh token Microsoft
	// stopped honouring and for a completed sign-in it would not issue a
	// token for.
	interactionText = "user interaction is required"
)

// deviceRequestText opens the client library's error for a device code that
// could not be requested at all, as opposed to one whose poll was answered.
// Only the second has a person's completed sign-in behind it.
const deviceRequestText = "start device auth"

// What a rejection was about, as reported in the logs.
const (
	refusalInteraction  = "interaction_required"
	refusalAbuseHold    = "abuse_hold"
	refusalUnrecognised = "unrecognised"
)

// refusalsBeforeSignIn is how many times running Microsoft must refuse the
// stored refresh token before the live agent offers a sign-in beside it.
//
// More than one because a single refusal has been transient before, and each
// costs only the wait the connect loop already imposes. Few, because past
// that the token has never once recovered by being retried, and until a
// person signs in the agent is simply out of the game.
const refusalsBeforeSignIn = 3

// signInRefusalsBeforeFloor is the consecutive refused sign-in from which
// each further one waits the full rejection floor.
//
// A refused sign-in is an attempt on the account like any other, and the case
// this was written for is a person completing code after code that Microsoft
// goes on refusing. The first refusal earns one prompt retry, because it is
// usually followed by somebody changing something on the account and wanting
// to try again; a second in a row says that was not it, and from there the
// attempts are spaced like the rejections they are.
const signInRefusalsBeforeFloor = 2

// Everything below is a multiple or a fraction of one number, the caller's
// rejection floor: the wait it leaves after Microsoft refuses the account.
// An operator who lengthens that wait lengthens all of these with it.
const (
	// defaultRejectionFloor stands in when the caller configured none.
	defaultRejectionFloor = 15 * time.Minute

	// refreshFloorDivisor: a token Microsoft is refusing is refreshed no more
	// often than half the floor while a sign-in is on offer beside it. Half,
	// because the caller jitters its wait down to that at the shortest, so
	// offering a sign-in never puts the token in front of Microsoft more
	// often than retrying it alone did.
	refreshFloorDivisor = 2

	// signInPauseDivisor: the pause after the first sign-in Microsoft
	// refuses, and before the first re-prompt after a code nobody answered,
	// is a sixth of the floor -- 2.5 minutes at the default, 75-150s once the
	// caller has jittered it. Long enough that neither is followed by a new
	// code within seconds, short enough that whoever has just changed
	// something on the account can try once more without waiting the floor.
	signInPauseDivisor = 6

	// unansweredCapFloors: each further code nobody answers doubles the
	// pause before the next, up to four floors. A code is polled every few
	// seconds for the quarter of an hour it lasts, so codes printed back to
	// back for a prompt nobody is reading are a steady stream of requests to
	// the endpoint that has just refused this account. Capped where a person
	// arriving late still finds a code within the hour.
	unansweredCapFloors = 4

	// holdCooldownFloors: a hold Microsoft announced at the sign-in is
	// assumed to stand for 24 floors -- six hours at the default -- after its
	// last mention, unless a token works sooner. Without any limit a hold
	// that had lifted over a token that stayed dead would leave the agent
	// out of the game until its pod was deleted; with a short one the agent
	// would be back at the prompt, under a hold it had been told about,
	// several times a day. Many floors, because every prompt this allows is
	// one a person may answer, and an answered prompt is an attempt on a
	// held account.
	holdCooldownFloors = 24
)

// signInErrorText opens every error a device-code login ends in.
const signInErrorText = "mcauth: device-code login"

// SignInPace is how soon a device-code login that failed may be followed by
// another.
type SignInPace int

const (
	// SignInRetryUnpaced leaves the next code to the caller's ordinary
	// backoff: nobody answered and there is no token to fall back on, so
	// until somebody does answer there is nothing else to try.
	SignInRetryUnpaced SignInPace = iota
	// SignInRetryBackoff follows a code that came to nothing beside a stored
	// token, and waits longer for each one in a row.
	SignInRetryBackoff
	// SignInRetrySoon follows the first sign-in Microsoft refused.
	SignInRetrySoon
	// SignInRetryFloor follows a refusal that is one of a run, or that was
	// not about something a person can put right at the prompt.
	SignInRetryFloor
)

// SignInError is a device-code login that did not end in a token.
//
// Distinct from a refused refresh because the caller has to pace the two
// differently and report them differently: one is this process trying a
// credential it holds, the other is a person being refused, or not there.
type SignInError struct {
	Err  error
	Pace SignInPace
	// Unanswered is how many codes running have come to nothing, this one
	// included. Set only with SignInRetryBackoff.
	Unanswered int
}

func (e *SignInError) Error() string { return signInErrorText + ": " + e.Err.Error() }
func (e *SignInError) Unwrap() error { return e.Err }

// IsSignIn reports whether err came out of a device-code login rather than
// out of refreshing a stored token. Falls back to the text for the reason
// IsRejection does.
func IsSignIn(err error) bool {
	if err == nil {
		return false
	}
	var signIn *SignInError
	return errors.As(err, &signIn) || strings.Contains(err.Error(), signInErrorText)
}

// SignInWait is how long the caller should leave, before any jitter of its
// own, between the device-code login that ended in err and its next attempt.
// floor is the caller's wait after a rejection.
//
// paced is false when err is not a sign-in's, or is one the caller's ordinary
// backoff should handle.
func SignInWait(err error, floor time.Duration) (wait time.Duration, paced bool) {
	if !IsSignIn(err) {
		return 0, false
	}
	var signIn *SignInError
	if !errors.As(err, &signIn) {
		// A layer in between flattened the error to text, which carries
		// neither the run this refusal belongs to nor whether a token is
		// held. A refusal assumes the worst; anything else still gets a real
		// pause.
		if IsRejection(err) {
			return floor, true
		}
		return floor / signInPauseDivisor, true
	}
	switch signIn.Pace {
	case SignInRetryFloor:
		return floor, true
	case SignInRetrySoon:
		return floor / signInPauseDivisor, true
	case SignInRetryBackoff:
		wait, limit := floor/signInPauseDivisor, unansweredCapFloors*floor
		for n := 1; n < signIn.Unanswered && wait < limit; n++ {
			wait *= 2
		}
		return min(wait, limit), true
	default:
		return 0, false
	}
}

// IsRejection reports whether err is Microsoft refusing the account or its
// credential, as opposed to a transient network, protocol or server failure.
func IsRejection(err error) bool {
	if err == nil {
		return false
	}
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) {
		return retrieveErr.ErrorCode == rejectionCode
	}
	// The error crosses gophertunnel, xal and x/oauth2 before it reaches a
	// caller, and nothing in any of those layers guarantees it goes on
	// wrapping with %w forever -- a version bump anywhere in that chain that
	// starts formatting the error into a plain string instead would quietly
	// turn a real rejection back into an ordinary failure. Matching the text
	// the account was actually rejected with is the honest belt-and-braces
	// for that gap, not a substitute for the typed check above.
	return strings.Contains(err.Error(), rejectionCode)
}

// refusalKind says what a rejection was about, or "" for an error that is
// not one.
func refusalKind(err error) string {
	if !IsRejection(err) {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, abuseHoldText):
		return refusalAbuseHold
	case strings.Contains(text, interactionText):
		return refusalInteraction
	default:
		return refusalUnrecognised
	}
}

// Hooks lets the caller count what this package can only log. Any field may
// be nil.
type Hooks struct {
	// RefreshRejected is called for every refresh Microsoft refused, whatever
	// this process goes on to do about it.
	RefreshRejected func()
	// SignInRequired is called with true when the live agent starts asking
	// for a device-code login and with false when it stops: it has a token to
	// use, it is no longer the live agent, or it has a stored token to go
	// back to and Microsoft is holding the account.
	SignInRequired func(required bool)
	// SignInRejected is called each time Microsoft refuses a device-code
	// login that a person completed.
	SignInRejected func()
	// AbuseHold is called with true when the live agent learns of an
	// abuse-mode hold, and with false when a token works, the hold has gone
	// unmentioned for its cooldown, or the process stops being the live
	// agent.
	AbuseHold func(held bool)
}

// WithHooks reports rejections and the sign-in state to hooks.
func WithHooks(hooks Hooks) Option {
	return func(c *cachingTokenSource) { c.hooks = hooks }
}

// WithRejectionFloor tells the token source how long the caller waits after
// Microsoft refuses the account, which is the one number its own pacing is
// derived from -- see refreshFloorDivisor and holdCooldownFloors. The caller
// owns it because it owns the wait these must never undercut.
func WithRejectionFloor(floor time.Duration) Option {
	return func(c *cachingTokenSource) {
		if floor > 0 {
			c.floor = floor
		}
	}
}

// StandDown withdraws everything ts has reported through its hooks.
//
// For the moment a process stops being the live agent. Nothing asks a standby
// for a token, so a token source left to notice the change at its next call
// would go on reporting a sign-in wanted, or a hold, for as long as the
// process stood by.
func StandDown(ts oauth2.TokenSource) {
	if c, ok := ts.(*cachingTokenSource); ok {
		c.reported.set(c.hooks, nil, false, false)
	}
}

// reports is the last state given to the hooks. Guarded separately from the
// token source because StandDown has to get through while a device-code login
// holds the source's own mutex for as long as the code lasts.
type reports struct {
	mu             sync.Mutex
	signInRequired bool
	abuseHold      bool
}

// set reports a state, or withdraws it if live says the process is no longer
// the one to report it. live is consulted under the lock so that a report
// computed just before a turn ended cannot land after StandDown's.
func (r *reports) set(hooks Hooks, live func() bool, signInRequired, abuseHold bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if live != nil && !live() {
		signInRequired, abuseHold = false, false
	}
	if r.signInRequired != signInRequired {
		r.signInRequired = signInRequired
		if hooks.SignInRequired != nil {
			hooks.SignInRequired(signInRequired)
		}
	}
	if r.abuseHold != abuseHold {
		r.abuseHold = abuseHold
		if hooks.AbuseHold != nil {
			hooks.AbuseHold(abuseHold)
		}
	}
}

// Where the refresh token a process holds came from, reported with a refusal
// so a reader can tell a token Microsoft issued to this very process from one
// that passed through the store first.
const (
	originStore   = "store"
	originRefresh = "refresh"
	originSignIn  = "sign_in"
)

// Why a device code is being printed.
const (
	signInNoStoredToken = "no_stored_token"
	signInTokenRefused  = "stored_token_refused"
)

// requestLiveToken is the interactive device-code login. A package-level
// var, not a direct call, so tests can substitute a stub and prove it is
// reached only on a genuinely empty store - never on one that is merely
// unreadable - and only by a process the gate says holds the login.
var requestLiveToken = auth.RequestLiveTokenContext

// refreshSource builds the refresher around a token this process has come to
// hold. A var for the same reason: a test has to be able to say what
// Microsoft answers for a token it did not start with.
var refreshSource = auth.RefreshTokenSourceWriter

// Option configures a TokenSource.
type Option func(*cachingTokenSource)

// WithLiveGate makes every use of the shared login conditional on live
// returning true: refreshing the cached token, persisting the result, and the
// first-run device-code login.
//
// This is how a warm standby stays harmless. Microsoft retires a refresh
// token the moment it issues the replacement, so the damage a second process
// does is done by the refresh itself and not by storing it -- a gate on the
// write alone would suppress the copy and leave the live agent holding a
// credential that has already been revoked. A standby therefore rotates
// nothing and reads the live agent's work out of the store instead; see
// Token.
//
// The gate is consulted per call rather than once at construction because a
// standby becomes the live agent without rebuilding anything, and from a
// second goroutine while a device-code login is in flight, so it has to be
// safe for concurrent use.
func WithLiveGate(live func() bool) Option {
	return func(c *cachingTokenSource) {
		if live != nil {
			c.live = live
		}
	}
}

// WithLogger reports each refresh, and each refresh deliberately not
// persisted, so a reader can tell from two pods' logs that both hold a valid
// token and only one of them is writing.
func WithLogger(l *logging.Logger) Option {
	return func(c *cachingTokenSource) { c.log = l }
}

// TokenSource returns an oauth2.TokenSource backed by the token cached in
// store for one account.
//
// A store that holds nothing for this account yet is not an error here: the
// device-code login it calls for is deferred to the first Token call made by
// a process the gate says may hold the login, because a standby that printed
// a code would print it into a pod log nobody is watching and block for as
// long as the code lasts. Any other load failure (a corrupt entry, a store
// that cannot be reached, a transient I/O error) is a hard error instead,
// for the same reason stated the other way round: those must never be
// mistaken for an empty store and answered with a prompt.
//
// ctx bounds the initial load and, later, that deferred login, so a process
// asked to shut down while it waits on a device code stops waiting.
//
// Every refresh is persisted back to the store, subject to WithLiveGate, so
// a later restart resumes without a fresh login as long as the refresh token
// is still valid.
func TokenSource(ctx context.Context, store Store, out io.Writer, opts ...Option) (oauth2.TokenSource, error) {
	if store == nil {
		return nil, errors.New("mcauth: nil token store")
	}

	tok, err := store.Load(ctx)
	if err != nil && !errors.Is(err, ErrNoToken) {
		return nil, fmt.Errorf("mcauth: load cached token: %w", err)
	}

	c := &cachingTokenSource{
		store:    store,
		out:      out,
		loginCtx: ctx,
		live:     func() bool { return true },

		floor: defaultRejectionFloor,
	}
	if tok != nil {
		c.adopt(tok, originStore)
	}
	for _, opt := range opts {
		opt(c)
	}
	// A process that starts as a standby reads the store again before it
	// rotates anything, however long it stands by first: what was loaded
	// here is the live agent's token, and the live agent goes on rotating it.
	c.mustReload = !c.live()
	return c, nil
}

// cachingTokenSource holds one account's token for one process and persists
// every rotation of it, so a background refresh doesn't get lost on restart.
//
// gophertunnel may call Token() concurrently with its own background refresh
// goroutine, so access to inner, to held and to the store is serialised by mu
// rather than relying on inner's own thread-safety for the write side.
type cachingTokenSource struct {
	mu    sync.Mutex
	store Store
	out   io.Writer
	// loginCtx bounds the deferred device-code login, the one call here that
	// blocks for minutes rather than milliseconds. Held on the struct
	// because oauth2.TokenSource gives Token() no context to inherit and
	// the login no longer happens at construction, where ctx was in scope.
	loginCtx context.Context
	// inner refreshes held, and is rebuilt whenever held is replaced by a
	// token this process did not derive from the previous one. nil until
	// there is a token at all, which is the cold start.
	inner oauth2.TokenSource
	held  *oauth2.Token
	live  func() bool
	log   *logging.Logger
	// saved is the refresh token this process has written, and starts empty
	// even though the store was just read: what was loaded is not
	// necessarily what the store the agent writes to holds. A token read
	// through a Fallback came from the file the cluster is moving away from,
	// and the one write that starting empty costs is what copies it into the
	// row. It is only ever that copy: a process that stood by first seeds
	// this from the store before it rotates anything, so an empty saved can
	// no longer flush a superseded token over a newer one -- see reload.
	saved string
	// mustReload records that the store may hold a token newer than the one
	// this process is holding: it stood by while another process was live,
	// or a write of its own lost to one. Cleared by the reload the next live
	// call performs.
	mustReload bool

	hooks    Hooks
	reported reports
	// floor is the caller's wait after a rejection -- see WithRejectionFloor.
	floor time.Duration
	// now replaces the clock in tests that count attempts over a day.
	now func() time.Time
	// origin and heldSince describe the refresh token in held, for the one
	// log line that has to say whose token Microsoft refused.
	origin    string
	heldSince time.Time
	// lastRefresh is when a refresh last failed.
	lastRefresh time.Time

	// What follows is about the prompt, and starts again with every token
	// this process comes to hold and every token that can be used.
	//
	// refusals counts consecutive refreshes of held that Microsoft refused
	// as needing a person, while the store held the same token.
	refusals int
	// signInOffered records that held has been refused often enough for a
	// device-code login to be run beside it. The token stays held and stored
	// throughout: a refusal that is really a hold on the account lifts
	// without anyone signing in, and only the token can find that out.
	signInOffered bool
	// unanswered counts consecutive codes beside a stored token that came to
	// nothing.
	unanswered int
	// wantSignIn is what SignInRequired reports while this process is live.
	wantSignIn bool

	// What follows is about the account, and outlives any one token: only
	// Microsoft issuing a token clears it -- see proven.
	//
	// signInRefusals counts consecutive completed sign-ins Microsoft refused.
	// A code that expires between two refusals says nothing about the
	// account.
	signInRefusals int
	// abuseHold records an abuse-mode hold, and abuseSeen when Microsoft last
	// mentioned it -- see answered.
	abuseHold bool
	abuseSeen time.Time
}

// Token returns a usable token, doing only what this process is entitled to
// do to get one.
//
// The live agent holds the account's login and so may rotate it: it
// refreshes, or on a cold start logs in, and persists the result. A standby
// holds nothing and may rotate nothing -- see standbyToken.
func (c *cachingTokenSource) Token() (*oauth2.Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Deferred so every way out reports, a turn that ended mid-call included:
	// a standby reports nothing wanted and nothing held.
	defer c.report()

	if !c.live() {
		// Another process is rotating the account's token while this one
		// stands by, so anything this one holds may be superseded before it
		// is allowed to use it.
		c.mustReload = true
		return c.standbyToken()
	}
	if c.mustReload {
		if err := c.reload(); err != nil {
			// A token that may have been superseded is still one this
			// process can dial with; what it may not do is rotate from it,
			// since refreshing a token Microsoft has already retired is what
			// costs the account its login. The connect loop calls Token per
			// dial, so the reload retries within seconds.
			if c.held.Valid() {
				return c.held, nil
			}
			return nil, err
		}
	}

	tok, err := c.liveToken()
	if err != nil {
		return nil, err
	}
	if c.held != nil && c.held.RefreshToken != tok.RefreshToken {
		c.origin, c.heldSince = originRefresh, c.clock()
	}
	c.held = tok
	if tok.RefreshToken == c.saved {
		return tok, nil
	}
	// Bounded and detached: oauth2 gives Token() no context to inherit, and
	// an unbounded write against an unreachable database would hold the
	// mutex that every dial and every background refresh waits on.
	saveCtx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	// Best-effort: a failed cache write shouldn't fail the connection, but
	// it does mean the next restart re-authenticates.
	if err := c.store.Save(saveCtx, tok); err != nil {
		if errors.Is(err, ErrSavedToFallback) {
			// Durable, but not where the next load prefers to look. Leaving
			// saved untouched is what retries the primary: the connect loop
			// calls Token per dial, so the row catches up in seconds rather
			// than at the end of this access token's life.
			c.note(func() { c.log.Info("auth_token_written_to_fallback", nil) })
			return tok, nil
		}
		if errors.Is(err, ErrStoreConflict) {
			// Another process wrote the account's row, so it holds the
			// login this one was rotating. Taking its token back is the
			// next call's job -- see reload.
			c.mustReload = true
			c.note(func() { c.log.Info("auth_token_write_superseded", nil) })
			return tok, nil
		}
		c.note(func() { c.log.Error("auth_token_write_failed", logging.Fields{"error": err.Error()}) })
		return tok, nil
	}
	c.saved = tok.RefreshToken
	c.note(func() { c.log.Info("auth_token_written", nil) })
	return tok, nil
}

// reload takes what the store holds before this process rotates anything.
//
// The gate opens on a process that has been standing by, holding the token it
// last read. The agent that was live has rotated the account since, and
// Microsoft retired that copy as it issued the replacement: refreshing from
// it fails, and writing it back leaves the account's only stored credential
// dead and the next restart loading it too.
//
// An empty store is not a failure here. That is the cold start, and the
// migration window where the row is empty and the load was answered by the
// file behind it -- leaving saved empty is what copies that file into the row
// on the first write.
func (c *cachingTokenSource) reload() error {
	loadCtx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()

	tok, err := c.store.Load(loadCtx)
	switch {
	case errors.Is(err, ErrNoToken):
	case err != nil:
		return fmt.Errorf("mcauth: reload before rotating: %w", err)
	default:
		if c.held == nil || tok.RefreshToken != c.held.RefreshToken {
			c.adopt(tok, originStore)
			c.note(func() { c.log.Info("auth_token_adopted_from_store", nil) })
		}
		c.saved = tok.RefreshToken
	}
	c.mustReload = false
	return nil
}

// liveToken refreshes the held token, or runs the device-code login when
// there is none to refresh or the one there is has been refused too often.
//
// The login lands here rather than at construction because this is the first
// moment the process is known to be the one entitled to it, and the account
// allows one login at a time: two pods prompting independently produce two
// grants, of which the stored one is not necessarily the one either process
// is using.
func (c *cachingTokenSource) liveToken() (*oauth2.Token, error) {
	if c.inner == nil {
		return c.signIn(signInNoStoredToken)
	}
	if c.signInOffered && c.clock().Sub(c.lastRefresh) < c.floor/refreshFloorDivisor {
		return c.signIn(signInTokenRefused)
	}
	tok, err := c.refresh()
	if err == nil {
		c.stopOffering()
		return tok, nil
	}
	tok, err = c.reloadAfterFailedRefresh(err)
	if err == nil {
		c.stopOffering()
		return tok, nil
	}
	if !c.signInOffered {
		return nil, err
	}
	return c.signIn(signInTokenRefused)
}

// refresh asks the refresher for a token and accounts for the answer.
//
// The refresher answers from memory until the access token expires, so an
// error here is always an attempt that went to Microsoft. It is timed and
// counted at this point rather than where its consequences are decided,
// because both have to hold whatever the store says afterwards and whether or
// not the caller ever sees this error.
//
// The same reasoning read the other way round decides what counts as the
// account working: a token that differs from the one held is one Microsoft
// has just issued, while the held one handed back says only that it has not
// expired yet.
func (c *cachingTokenSource) refresh() (*oauth2.Token, error) {
	tok, err := c.inner.Token()
	if err != nil {
		c.lastRefresh = c.clock()
		if IsRejection(err) && c.hooks.RefreshRejected != nil {
			c.hooks.RefreshRejected()
		}
		return nil, err
	}
	if c.held == nil || tok.AccessToken != c.held.AccessToken {
		c.proven()
	}
	return tok, nil
}

// signIn runs one device-code login and adopts its token.
//
// Nothing is removed from the store first. A refused token is replaced by the
// write that follows a login which worked, and by nothing else: if Microsoft
// refuses the sign-in as well, the problem is the account and not the token,
// and the token is then the only thing that will notice the account come
// back.
func (c *cachingTokenSource) signIn(reason string) (*oauth2.Token, error) {
	c.wantSignIn = true
	// Now rather than when Token returns: the login below blocks for as long
	// as the code lasts, and that is exactly the time the report is for.
	c.report()
	c.note(func() { c.log.Info("auth_device_code_login", logging.Fields{"reason": reason}) })
	loginCtx, cancel := c.turnContext()
	defer cancel()
	tok, err := requestLiveToken(loginCtx, c.out)
	if err != nil {
		return nil, c.signInEnded(reason, err)
	}
	if !c.live() {
		// The code was answered after this process stopped being the one
		// entitled to the login. The grant is real and so is its successor's:
		// storing this one would leave the row describing neither the pod in
		// the game nor the token it is playing on.
		return nil, &SignInError{Err: errors.New("finished after this process's turn ended")}
	}
	c.adopt(tok, originSignIn)
	c.proven()
	c.note(func() { c.log.Info("auth_sign_in_completed", logging.Fields{"reason": reason}) })
	return tok, nil
}

// signInEnded accounts for a device-code login that returned no token.
func (c *cachingTokenSource) signInEnded(reason string, err error) error {
	kind := refusalKind(err)
	polled := !strings.Contains(err.Error(), deviceRequestText)
	if kind == "" || !polled {
		c.note(func() {
			c.log.Info("auth_sign_in_failed", logging.Fields{"reason": reason, "error": err.Error()})
		})
		switch {
		case !c.live():
			// The turn ended under the prompt, which says nothing about how
			// the next one should be paced.
			return &SignInError{Err: err}
		case kind != "":
			// Microsoft would not even issue a code. Nobody signed in, so it
			// is not a refused sign-in, but it is a refusal.
			return &SignInError{Err: err, Pace: SignInRetryFloor}
		case c.inner == nil:
			return &SignInError{Err: err}
		}
		c.unanswered++
		return &SignInError{Err: err, Pace: SignInRetryBackoff, Unanswered: c.unanswered}
	}

	// A person finished the sign-in and Microsoft refused to issue a token
	// for it, so no credential this process could hold would do better.
	// Louder than a code nobody answered, and counted, because it is the one
	// outcome that needs the account looked at rather than the prompt.
	c.unanswered = 0
	c.signInRefusals++
	if c.hooks.SignInRejected != nil {
		c.hooks.SignInRejected()
	}
	c.answered(kind)
	c.note(func() {
		c.log.Error("auth_sign_in_rejected", logging.Fields{
			"reason":      reason,
			"refusal":     kind,
			"consecutive": c.signInRefusals,
			"error":       err.Error(),
		})
	})
	pace := SignInRetryFloor
	if kind == refusalInteraction && c.signInRefusals < signInRefusalsBeforeFloor {
		pace = SignInRetrySoon
	}
	return &SignInError{Err: err, Pace: pace}
}

// answered records what Microsoft's latest rejection says about a hold on the
// account.
//
// A hold is remembered rather than re-read from each answer, because the
// answers disagree: under one, the sign-in is refused as a hold while the
// refresh beside it goes on being refused as needing a person. Taking the
// refresh at its word would count three of those and print another code,
// under a hold the agent had been told about, for as long as the hold lasted.
// It stands until a token works, or until it has gone unmentioned for the
// cooldown -- see holdCooldownFloors.
func (c *cachingTokenSource) answered(kind string) {
	switch {
	case kind == refusalAbuseHold:
		c.abuseHold = true
		c.abuseSeen = c.clock()
		// With a token held there is something better to do than ask. With
		// none, asking is all there is, and the report that a sign-in is
		// wanted stays true.
		if c.inner != nil {
			c.stopOffering()
		}
	case c.abuseHold && c.clock().Sub(c.abuseSeen) >= holdCooldownFloors*c.floor:
		c.abuseHold = false
	}
}

// refused accounts for a refresh Microsoft refused and that left this process
// holding the token it was refused with, and decides whether a sign-in is the
// next thing to try.
//
// confirmed says the store holds that same token, which is the only evidence
// that the token is the account's current one and not a copy another process
// has rotated past. Only a confirmed refusal counts towards a prompt: a store
// that cannot be read leaves that open, and a code printed on the strength of
// it would be a code printed for a database problem.
func (c *cachingTokenSource) refused(refreshErr error, confirmed bool) {
	kind := refusalKind(refreshErr)
	if kind == "" {
		// Worth a line of its own: a refresh whose answer was lost on the way
		// back may still have rotated the token at Microsoft, and this is the
		// only trace that it was ever sent.
		c.note(func() { c.log.Info("auth_refresh_failed", logging.Fields{"error": refreshErr.Error()}) })
		return
	}
	c.answered(kind)
	switch {
	case c.abuseHold || kind != refusalInteraction:
		// A hold, or wording never seen before. Neither is something a
		// sign-in is known to cure, so neither counts towards one and either
		// withdraws one already on offer.
		c.stopOffering()
	case confirmed:
		c.refusals++
	}
	c.note(func() {
		fields := logging.Fields{
			"refusal":         kind,
			"consecutive":     c.refusals,
			"store_confirmed": confirmed,
			"abuse_hold":      c.abuseHold,
			"token_origin":    c.origin,
			"token_held_ms":   c.clock().Sub(c.heldSince).Milliseconds(),
		}
		if kind == refusalUnrecognised {
			fields["error"] = refreshErr.Error()
		}
		c.log.Error("auth_refresh_rejected", fields)
	})
	if c.signInOffered || c.refusals < refusalsBeforeSignIn {
		return
	}
	c.signInOffered = true
	c.note(func() {
		c.log.Error("auth_sign_in_required", logging.Fields{"reason": signInTokenRefused, "refusals": c.refusals})
	})
}

// stopOffering withdraws the sign-in and starts the count towards the next
// one again. It says nothing about the account: that is proven's to say.
func (c *cachingTokenSource) stopOffering() {
	c.refusals = 0
	c.unanswered = 0
	c.signInOffered = false
	c.wantSignIn = false
}

// proven records that Microsoft has just issued this process a token, which
// is the only evidence that whatever it held against the account is over.
func (c *cachingTokenSource) proven() {
	c.signInRefusals = 0
	c.abuseHold = false
}

// report gives the hooks the state as it now stands.
func (c *cachingTokenSource) report() {
	c.reported.set(c.hooks, c.live, c.wantSignIn, c.abuseHold)
}

// clock is time.Now, unless a test is measuring a day's attempts.
func (c *cachingTokenSource) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// turnContext bounds a call by this process's turn as the live agent rather
// than by the process itself.
//
// Only the login needs it. Everything else here is a round trip of
// milliseconds, where a turn that ends mid-call costs nothing, but a device
// code lasts about a quarter of an hour: long enough for the lock to move on,
// for a successor to print its own code and have it answered, and for this
// one to then write its grant over the successor's.
func (c *cachingTokenSource) turnContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(c.loginCtx)
	poll := gatePoll
	go func() {
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !c.live() {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

// standbyToken answers without rotating anything.
//
// The token this process loaded stays usable until its access token expires.
// Past that, refreshing is not an option a standby has: Microsoft retires the
// refresh token as it issues the replacement, so a standby that refreshed
// would revoke the credential the live agent is holding the game with and
// leave neither process able to reconnect. What it does instead is re-read
// the store, because the live agent persists every rotation -- staying warm
// on the other process's work rather than on work of its own.
//
// In practice a standby asks for a token once, at start-up, and has no use
// for another until it is live. Staying current across the wait is not this
// function's job but reload's, which runs before the promoted process rotates
// anything.
//
// A store that has nothing newer leaves this process unwarmed, which is
// reported rather than worked around. It costs the handover one refresh; the
// alternative costs the account its login.
func (c *cachingTokenSource) standbyToken() (*oauth2.Token, error) {
	if c.held.Valid() {
		return c.held, nil
	}
	loadCtx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	tok, err := c.store.Load(loadCtx)
	switch {
	case errors.Is(err, ErrNoToken):
		return nil, ErrStandbyUnwarmed
	case err != nil:
		return nil, fmt.Errorf("mcauth: standby reload: %w", err)
	case !tok.Valid():
		// Adopting would point the refresher at this token, and a standby
		// that already holds one reaches here only because what it holds is
		// no fresher -- so taking the reload over would leave the process
		// rotating from whichever of the two is older.
		//
		// A process holding nothing is the exception: an expired token is
		// still a refresh token, and having one to rotate from on promotion
		// beats having none at all.
		if c.held == nil {
			c.adopt(tok, originStore)
		}
		return nil, ErrStandbyUnwarmed
	}
	// Adopting rebuilds the refresher, so on this path -- reached only once
	// what is held has expired -- the single thing it can still change is
	// which credential a later rotation starts from. Only an unexpired
	// reload earns that, which the switch above has already established: a
	// store answering at all does not make its answer the newer one, and a
	// fallback reaching past an unreachable database returns the copy the
	// migration left behind.
	c.adopt(tok, originStore)
	c.note(func() { c.log.Info("auth_token_standby_reloaded", nil) })
	return tok, nil
}

// reloadAfterFailedRefresh reads the store again, once, when a refresh has
// just been rejected.
//
// The refresh token this process holds is not always the account's current
// one. A load that fell through to the file cache because the database could
// not be reached answers with whatever the volume still holds, and that copy
// stopped being current the first time the live agent rotated it: Microsoft
// rejects it, and goes on rejecting it for as long as this process lives,
// while the database that has since come back holds one that works. A
// rejected refresh is therefore the moment to look at the store rather than
// the moment to give up -- the connect loop's backoff would otherwise retry
// the same dead credential forever.
//
// A store that cannot answer, or that answers with the refresh token that
// was just rejected, has nothing to add, and the original failure stands. The
// second of those is also the only evidence there is that the token itself is
// what Microsoft refused -- see refused.
func (c *cachingTokenSource) reloadAfterFailedRefresh(refreshErr error) (*oauth2.Token, error) {
	rejected := ""
	if c.held != nil {
		rejected = c.held.RefreshToken
	}
	loadCtx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	tok, err := c.store.Load(loadCtx)
	if err != nil {
		c.refused(refreshErr, false)
		return nil, refreshErr
	}
	if tok.RefreshToken == rejected {
		c.refused(refreshErr, true)
		return nil, refreshErr
	}
	c.note(func() { c.log.Info("auth_token_reloaded_after_failed_refresh", nil) })
	c.adopt(tok, originStore)
	if tok.Valid() {
		return tok, nil
	}
	tok, err = c.refresh()
	if err != nil {
		// Refused with the token the store has just handed over, so there is
		// no doubt it is the current one.
		c.refused(err, true)
	}
	return tok, err
}

// adopt makes tok the token this process holds, rebuilding the refresher
// around it: inner keeps its own copy, so replacing held without this would
// leave the next refresh working from the token it superseded.
//
// Whatever was counted against the previous token does not carry over. What
// is known about the account does: a token is not evidence of anything until
// Microsoft has honoured it.
func (c *cachingTokenSource) adopt(tok *oauth2.Token, origin string) {
	c.held = tok
	c.inner = refreshSource(tok, c.out)
	c.origin, c.heldSince = origin, c.clock()
	c.stopOffering()
}

// note runs emit only when a logger was configured. A closure rather than a
// nil-checked logger at each call site because every one of them builds
// fields that are pure waste when nothing is listening.
func (c *cachingTokenSource) note(emit func()) {
	if c.log != nil {
		emit()
	}
}
