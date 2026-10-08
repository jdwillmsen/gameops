package mcauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type stubTokenSource struct {
	mu     sync.Mutex
	tokens []*oauth2.Token
	calls  int
	// err stands for the refresh Microsoft rejects -- invalid_grant against
	// a refresh token something else has already rotated past.
	err error
}

func (s *stubTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	// The last token repeats once the script runs out, which is what a real
	// source does between refreshes: the same token until it expires.
	return s.tokens[min(s.calls-1, len(s.tokens)-1)], nil
}

func (s *stubTokenSource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// withStubLogin swaps requestLiveToken for the duration of a test and
// restores the real one afterward, since it's a package-level var shared
// across the test binary.
func withStubLogin(t *testing.T, stub func(ctx context.Context, out io.Writer) (*oauth2.Token, error)) {
	t.Helper()
	orig := requestLiveToken
	requestLiveToken = stub
	t.Cleanup(func() { requestLiveToken = orig })
}

func TestTokenSource_ColdStartWithNothingCachedLogsIn(t *testing.T) {
	store := &memStore{}
	loginCalls := 0
	withStubLogin(t, func(ctx context.Context, out io.Writer) (*oauth2.Token, error) {
		loginCalls++
		return &oauth2.Token{AccessToken: "fresh", RefreshToken: "fresh-refresh", Expiry: time.Now().Add(time.Hour)}, nil
	})

	ts, err := TokenSource(context.Background(), store, io.Discard)
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	if _, err := ts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if loginCalls != 1 {
		t.Errorf("login called %d times, want 1 for an empty store", loginCalls)
	}

	saved, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("the token the login returned was not persisted: %v", err)
	}
	if saved.AccessToken != "fresh" {
		t.Errorf("saved AccessToken = %q, want fresh", saved.AccessToken)
	}
}

// A standby has no lock, so it has no claim on the one login the account
// allows. Prompting anyway costs that pod ~15 minutes blocked on a code
// nobody is watching for, and an operator who does answer it creates a second
// grant the process holding the game knows nothing about.
func TestTokenSource_AStandbyDoesNotPromptForAColdStart(t *testing.T) {
	store := &memStore{}
	withStubLogin(t, func(ctx context.Context, out io.Writer) (*oauth2.Token, error) {
		t.Error("a standby printed a device code")
		return nil, errors.New("should not be reached")
	})

	ts, err := TokenSource(context.Background(), store, io.Discard, WithLiveGate(func() bool { return false }))
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	if _, err := ts.Token(); err == nil {
		t.Fatal("a standby reported a usable token from an empty store")
	}
}

// And the login is not lost, only deferred: the moment this process is the
// one entitled to it, it runs.
func TestTokenSource_TheDeferredLoginRunsOnceTheProcessGoesLive(t *testing.T) {
	store := &memStore{}
	var live atomic.Bool
	loginCalls := 0
	withStubLogin(t, func(ctx context.Context, out io.Writer) (*oauth2.Token, error) {
		loginCalls++
		return &oauth2.Token{AccessToken: "fresh", RefreshToken: "fresh-refresh", Expiry: time.Now().Add(time.Hour)}, nil
	})

	ts, err := TokenSource(context.Background(), store, io.Discard, WithLiveGate(live.Load))
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	if _, err := ts.Token(); err == nil {
		t.Fatal("a standby reported a usable token from an empty store")
	}

	live.Store(true)
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token once live: %v", err)
	}
	if tok.AccessToken != "fresh" {
		t.Errorf("AccessToken = %q, want the login's", tok.AccessToken)
	}
	if loginCalls != 1 {
		t.Errorf("login called %d times, want 1", loginCalls)
	}
	saved, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("the token the login returned was not persisted: %v", err)
	}
	if saved.RefreshToken != "fresh-refresh" {
		t.Errorf("saved RefreshToken = %q, want the login's", saved.RefreshToken)
	}
}

func TestTokenSource_CorruptCacheIsAHardErrorNotALoginPrompt(t *testing.T) {
	store := &memStore{failLoad: errors.New("cached token is not valid JSON")}

	loginCalls := 0
	withStubLogin(t, func(ctx context.Context, out io.Writer) (*oauth2.Token, error) {
		loginCalls++
		t.Error("interactive login must not be attempted for a corrupt cache")
		return nil, errors.New("should not be reached")
	})

	if _, err := TokenSource(context.Background(), store, io.Discard); err == nil {
		t.Fatal("expected TokenSource to fail hard on a corrupt cache")
	}
	if loginCalls != 0 {
		t.Errorf("login called %d times, want 0", loginCalls)
	}
}

// A store that cannot be reached is the state a pod is in when it has been
// released ahead of the migration that gives it its table, and it is the one
// most worth getting right: a device code printed into a pod log that nobody
// is watching blocks the agent for as long as the code lasts, on a database
// problem that would have cleared itself.
func TestTokenSource_UnavailableStoreIsAHardErrorNotALoginPrompt(t *testing.T) {
	store := &memStore{failLoad: ErrStoreUnavailable}

	withStubLogin(t, func(ctx context.Context, out io.Writer) (*oauth2.Token, error) {
		t.Error("interactive login must not be attempted when the store cannot be read")
		return nil, errors.New("should not be reached")
	})

	_, err := TokenSource(context.Background(), store, io.Discard)
	if err == nil {
		t.Fatal("expected TokenSource to fail hard on an unavailable store")
	}
	if !errors.Is(err, ErrStoreUnavailable) {
		t.Errorf("error = %v, want it to carry ErrStoreUnavailable", err)
	}
}

func TestTokenSource_RejectsANilStore(t *testing.T) {
	if _, err := TokenSource(context.Background(), nil, io.Discard); err == nil {
		t.Fatal("expected an error for a nil store")
	}
}

func TestCachingTokenSource_PersistsEachRefresh(t *testing.T) {
	ctx := context.Background()
	store := &memStore{}
	store.seed(t, storable("first", "r1"))

	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return true },
		inner: &stubTokenSource{tokens: []*oauth2.Token{
			storable("first", "r1"),
			storable("second", "r2"),
		}},
	}

	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load after first call: %v", err)
	}
	if got.AccessToken != "first" {
		t.Errorf("AccessToken = %q, want first", got.AccessToken)
	}

	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	got, err = store.Load(ctx)
	if err != nil {
		t.Fatalf("Load after second call: %v", err)
	}
	if got.AccessToken != "second" {
		t.Errorf("AccessToken = %q, want second after refresh", got.AccessToken)
	}
}

func TestCachingTokenSource_UnchangedTokenIsNotRewritten(t *testing.T) {
	store := &memStore{}
	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return true },
		inner: &stubTokenSource{tokens: []*oauth2.Token{storable("a", "r1")}},
	}

	for i := 0; i < 5; i++ {
		if _, err := cts.Token(); err != nil {
			t.Fatalf("Token: %v", err)
		}
	}
	if _, saves := store.counts(); saves != 1 {
		t.Errorf("saves = %d, want 1: a token that has not rotated is not news", saves)
	}
}

// The invariant the warm standby rests on, and the reason the gate cannot sit
// on the write alone: Microsoft retires the old refresh token as it issues
// the new one, so a standby that refreshed would revoke the credential the
// live agent is playing on -- the row would still say r1, and r1 would be
// dead. Suppressing the write does not undo that.
func TestCachingTokenSource_StandbyNeverRotatesTheSharedToken(t *testing.T) {
	ctx := context.Background()
	store := &memStore{}
	store.seed(t, storable("live-token", "r1"))

	refresher := &stubTokenSource{tokens: []*oauth2.Token{storable("standby-refreshed", "r2")}}
	standby := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return false },
		inner: refresher,
		held:  storable("live-token", "r1"),
	}

	tok, err := standby.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if refresher.callCount() != 0 {
		t.Errorf("standby refreshed %d times, want 0", refresher.callCount())
	}
	if tok.RefreshToken != "r1" {
		t.Errorf("standby got %q, want the token the live agent is using", tok.RefreshToken)
	}
	if _, saves := store.counts(); saves != 0 {
		t.Errorf("standby wrote %d times, want 0", saves)
	}
	stored, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.RefreshToken != "r1" {
		t.Errorf("stored RefreshToken = %q, want the live agent's r1 left untouched", stored.RefreshToken)
	}
}

// How a standby stays warm without rotating anything: when what it holds has
// expired, it re-reads the store, which the live agent keeps current. The
// round trip the handover would have paid is paid here, against the database
// instead of against Microsoft.
func TestCachingTokenSource_StandbyRewarmsFromWhatTheLiveAgentStored(t *testing.T) {
	store := &memStore{}
	store.seed(t, &oauth2.Token{AccessToken: "live-refreshed", RefreshToken: "r2", Expiry: time.Now().Add(time.Hour)})

	refresher := &stubTokenSource{tokens: []*oauth2.Token{storable("must-not-happen", "r3")}}
	standby := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return false },
		inner: refresher,
		held:  &oauth2.Token{AccessToken: "stale", RefreshToken: "r1", Expiry: time.Now().Add(-time.Minute)},
	}

	tok, err := standby.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if refresher.callCount() != 0 {
		t.Errorf("standby refreshed %d times, want 0", refresher.callCount())
	}
	if tok.RefreshToken != "r2" {
		t.Errorf("standby got %q, want the live agent's stored r2", tok.RefreshToken)
	}
	if _, saves := store.counts(); saves != 0 {
		t.Errorf("standby wrote %d times, want 0", saves)
	}
}

// With nothing newer to read, the standby stays cold and says so rather than
// refreshing its way out of it.
func TestCachingTokenSource_AnUnwarmableStandbyReportsItRatherThanRefreshing(t *testing.T) {
	refresher := &stubTokenSource{tokens: []*oauth2.Token{storable("must-not-happen", "r2")}}
	standby := &cachingTokenSource{
		store: &memStore{failLoad: ErrStoreUnavailable},
		out:   io.Discard,
		live:  func() bool { return false },
		inner: refresher,
		held:  &oauth2.Token{AccessToken: "stale", RefreshToken: "r1", Expiry: time.Now().Add(-time.Minute)},
	}

	if _, err := standby.Token(); err == nil {
		t.Fatal("an unwarmable standby reported success")
	}
	if refresher.callCount() != 0 {
		t.Errorf("standby refreshed %d times, want 0", refresher.callCount())
	}
}

// A process does not necessarily hold the account's current refresh token: a
// load that reached past an unreachable database answers from the file the
// migration left behind, and that copy died the first time the live agent
// rotated. Nothing else re-reads the store, so the rejection has to, or the
// connect loop retries a dead credential for as long as the process lives
// while the recovered database holds one that works.
func TestCachingTokenSource_ARejectedRefreshReReadsTheStore(t *testing.T) {
	store := &memStore{}
	store.seed(t, &oauth2.Token{AccessToken: "current", RefreshToken: "r5", Expiry: time.Now().Add(time.Hour)})

	refresher := &stubTokenSource{err: errors.New("invalid_grant")}
	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return true },
		inner: refresher,
		held:  &oauth2.Token{AccessToken: "stale", RefreshToken: "r0", Expiry: time.Now().Add(-time.Minute)},
	}

	tok, err := cts.Token()
	if err != nil {
		t.Fatalf("a rejected refresh was fatal even though the store held a usable token: %v", err)
	}
	if tok.RefreshToken != "r5" {
		t.Errorf("got %q, want the r5 the live agent stored", tok.RefreshToken)
	}
	if cts.held.RefreshToken != "r5" {
		t.Errorf("held RefreshToken = %q, want r5: the dead r0 must not be what the next refresh starts from", cts.held.RefreshToken)
	}
}

// The re-read is a second look, not a second chance: a store that agrees with
// what was just rejected has nothing to add, and swallowing the rejection
// would hide a genuinely revoked account behind a nil error.
func TestCachingTokenSource_ARejectedRefreshStandsWhenTheStoreAgrees(t *testing.T) {
	store := &memStore{}
	store.seed(t, &oauth2.Token{AccessToken: "stale", RefreshToken: "r0", Expiry: time.Now().Add(-time.Minute)})

	rejection := errors.New("invalid_grant")
	refresher := &stubTokenSource{err: rejection}
	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return true },
		inner: refresher,
		held:  &oauth2.Token{AccessToken: "stale", RefreshToken: "r0", Expiry: time.Now().Add(-time.Minute)},
	}

	if _, err := cts.Token(); !errors.Is(err, rejection) {
		t.Fatalf("err = %v, want the rejection itself", err)
	}
	if refresher.callCount() != 1 {
		t.Errorf("refreshed %d times, want 1: a store that agrees is not worth a retry", refresher.callCount())
	}
}

// Adopting rebuilds the refresher, so a reload is only ever an improvement
// when it can be used. The standby reload runs with what is held already
// expired, which means the one thing a useless answer can still do is take
// over which credential a later rotation starts from -- and a fallback
// reaching past an unreachable database answers with the file's older copy.
func TestCachingTokenSource_AnUnusableStandbyReloadKeepsTheHeldToken(t *testing.T) {
	store := &memStore{}
	store.seed(t, &oauth2.Token{AccessToken: "pre-migration", RefreshToken: "r0", Expiry: time.Now().Add(-time.Hour)})

	refresher := &stubTokenSource{tokens: []*oauth2.Token{{AccessToken: "must-not-happen", RefreshToken: "r6"}}}
	standby := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return false },
		inner: refresher,
		held:  &oauth2.Token{AccessToken: "expired", RefreshToken: "r5", Expiry: time.Now().Add(-time.Minute)},
	}

	if _, err := standby.Token(); !errors.Is(err, ErrStandbyUnwarmed) {
		t.Fatalf("err = %v, want ErrStandbyUnwarmed", err)
	}
	if standby.held.RefreshToken != "r5" {
		t.Errorf("held RefreshToken = %q, want r5 kept: the reload was no better than what it replaced", standby.held.RefreshToken)
	}
	if standby.inner != oauth2.TokenSource(refresher) {
		t.Error("the refresher was rebuilt around a token that could not be used")
	}
}

// A standby that loaded nothing at all is the exception: an expired token is
// worse than a usable one and better than none, because its refresh token is
// what this process will rotate from the moment it goes live.
func TestCachingTokenSource_AStandbyHoldingNothingAdoptsAnExpiredReload(t *testing.T) {
	store := &memStore{}
	store.seed(t, &oauth2.Token{AccessToken: "expired", RefreshToken: "r2", Expiry: time.Now().Add(-time.Minute)})

	standby := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return false },
	}

	if _, err := standby.Token(); !errors.Is(err, ErrStandbyUnwarmed) {
		t.Fatalf("err = %v, want ErrStandbyUnwarmed", err)
	}
	if standby.held == nil || standby.held.RefreshToken != "r2" {
		t.Errorf("held = %+v, want the stored r2: a process with nothing has nothing to lose", standby.held)
	}
}

// Going live is what lifts the restriction, and the first refresh after it is
// written -- nothing this process held was ever recorded from here before.
func TestCachingTokenSource_RefreshesAndWritesOnceItGoesLive(t *testing.T) {
	ctx := context.Background()
	store := &memStore{}
	store.seed(t, storable("old", "r1"))

	var live atomic.Bool
	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  live.Load,
		inner: &stubTokenSource{tokens: []*oauth2.Token{storable("refreshed", "r2")}},
		held:  storable("old", "r1"),
	}

	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token as standby: %v", err)
	}
	if _, saves := store.counts(); saves != 0 {
		t.Fatalf("standby wrote %d times, want 0", saves)
	}

	live.Store(true)
	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token as leader: %v", err)
	}
	stored, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.RefreshToken != "r2" {
		t.Errorf("stored RefreshToken = %q, want r2 written once live", stored.RefreshToken)
	}
}

// Both roles against one store at once, which is what a rolling release
// actually looks like. The assertion is not just that nothing races: it is
// that the stored token is only ever one the live agent put there, and that
// the standby got every token it answered with out of the live agent's row.
//
// The standby's held token is expired on purpose. A zero Expiry reads as
// "never expires" to oauth2, which would have the standby answer from memory
// every time and touch the shared store not once -- the property this is
// here for, serialised away by construction.
func TestCachingTokenSource_LiveAndStandbyShareOneStoreConcurrently(t *testing.T) {
	ctx := context.Background()
	store := &memStore{}
	// The live agent's own token, since that is all this store ever holds:
	// a standby that reads before the first rotation below still reads one.
	store.seed(t, storable("live", "live-seed"))

	liveTokens := make([]*oauth2.Token, 50)
	for i := range liveTokens {
		liveTokens[i] = &oauth2.Token{
			AccessToken:  "live",
			RefreshToken: "live-" + string(rune('a'+i%26)),
			Expiry:       time.Now().Add(time.Hour),
		}
	}
	standbyTokens := make([]*oauth2.Token, 50)
	for i := range standbyTokens {
		standbyTokens[i] = &oauth2.Token{AccessToken: "must-not-happen", RefreshToken: "standby-only", Expiry: time.Now().Add(time.Hour)}
	}

	liveAgent := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return true },
		inner: &stubTokenSource{tokens: liveTokens},
	}
	standbyRefresher := &stubTokenSource{tokens: standbyTokens}
	standby := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return false },
		inner: standbyRefresher,
		held:  &oauth2.Token{AccessToken: "standby", RefreshToken: "standby-held", Expiry: time.Now().Add(-time.Minute)},
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2*len(liveTokens))
	for i := 0; i < len(liveTokens); i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := liveAgent.Token(); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			tok, err := standby.Token()
			if err != nil {
				// The one failure a standby is allowed: nothing fresh in the
				// store and no licence to refresh its way out of that.
				if !errors.Is(err, ErrStandbyUnwarmed) {
					errCh <- err
				}
				return
			}
			// Whatever it answers with came out of the live agent's row --
			// it has no other source, having rotated nothing itself.
			if tok.AccessToken != "live" {
				errCh <- errors.New("standby answered with a token the live agent never stored")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Token() call: %v", err)
	}

	if standbyRefresher.callCount() != 0 {
		t.Errorf("standby refreshed %d times, want 0", standbyRefresher.callCount())
	}
	loads, saves := store.counts()
	if loads == 0 {
		t.Error("the standby never read the shared store, so neither role exercised it")
	}
	if saves != len(liveTokens) {
		t.Errorf("saves = %d, want one per live refresh (%d): only the live agent writes", saves, len(liveTokens))
	}

	stored, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.RefreshToken == "standby-only" {
		t.Error("the standby's refresh token reached the store")
	}
	if stored.AccessToken != "live" {
		t.Errorf("stored AccessToken = %q, want the live agent's", stored.AccessToken)
	}
}

// A store that has gone away costs the next restart a re-authentication. It
// must not cost this process its connection, which is the one thing keeping
// the agent in the game.
func TestCachingTokenSource_SaveFailureDoesNotFailTheCall(t *testing.T) {
	store := &memStore{failSave: ErrStoreUnavailable}
	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return true },
		inner: &stubTokenSource{tokens: []*oauth2.Token{storable("a", "r1")}},
	}

	tok, err := cts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "a" {
		t.Errorf("AccessToken = %q, want a", tok.AccessToken)
	}

	// And the failure is not remembered as a success: the next call tries
	// again rather than assuming the store holds what it does not.
	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if _, saves := store.counts(); saves != 2 {
		t.Errorf("saves = %d, want 2 attempts", saves)
	}
}

func TestCachingTokenSource_TokenIsSafeForConcurrentUse(t *testing.T) {
	store := &memStore{}
	tokens := make([]*oauth2.Token, 50)
	for i := range tokens {
		tokens[i] = storable("tok", "refresh")
	}
	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return true },
		inner: &stubTokenSource{tokens: tokens},
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(tokens))
	for range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cts.Token(); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Token() call failed: %v", err)
	}
}

// errTokenSource is a refresher that only fails, which is what Microsoft's
// does once the token it holds has been retired by a newer one.
type errTokenSource struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (e *errTokenSource) Token() (*oauth2.Token, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	return nil, e.err
}

func (e *errTokenSource) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// A promoted standby holds whatever the store said when it last read it, and
// the live agent has rotated since. Microsoft retired that token as it issued
// the replacement, so the first thing the new live agent must do with the
// login is read the row -- not write its own copy over it. Flushing leaves
// the account's only stored credential dead: this process fails at its next
// refresh, and every restart after it loads the dead one too.
func TestCachingTokenSource_PromotionReadsTheStoreBeforeWritingToIt(t *testing.T) {
	ctx := context.Background()
	store := &memStore{}
	store.seed(t, storable("boot", "r1"))

	var live atomic.Bool
	cts := &cachingTokenSource{store: store, out: io.Discard, live: live.Load}
	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token while standing by: %v", err)
	}

	// The live agent rotates r1 into r2 and stores it, which is what retires
	// r1 at Microsoft.
	if err := store.Save(ctx, storable("live", "r2")); err != nil {
		t.Fatalf("the live agent's Save: %v", err)
	}

	live.Store(true)
	tok, err := cts.Token()
	if err != nil {
		t.Fatalf("Token once promoted: %v", err)
	}
	if tok.RefreshToken != "r2" {
		t.Errorf("promoted standby answered with %q, want the stored r2", tok.RefreshToken)
	}
	stored, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.RefreshToken != "r2" {
		t.Errorf("the row now holds %q: a promoted standby overwrote a newer token with a retired one", stored.RefreshToken)
	}
}

// The same handover with the standby's copy already expired, which is the
// likelier half: refreshing from the retired token answers invalid_grant, and
// the connect loop then parks on AUTH_RETRY_DELAY_MS -- the handover cost this
// design exists to remove. The store holds a usable token the whole time.
func TestCachingTokenSource_PromotionWithAnExpiredCopyTakesTheStoredTokenNotARefresh(t *testing.T) {
	ctx := context.Background()
	withStubLogin(t, func(ctx context.Context, out io.Writer) (*oauth2.Token, error) {
		t.Error("a promoted standby printed a device code with a usable token in the store")
		return nil, errors.New("should not be reached")
	})

	store := &memStore{}
	store.seed(t, &oauth2.Token{AccessToken: "boot", RefreshToken: "r1", Expiry: time.Now().Add(-time.Minute)})

	var live atomic.Bool
	refresher := &errTokenSource{err: errors.New("oauth2: invalid_grant")}
	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  live.Load,
		inner: refresher,
		held:  &oauth2.Token{AccessToken: "boot", RefreshToken: "r1", Expiry: time.Now().Add(-time.Minute)},
	}
	if _, err := cts.Token(); err == nil {
		t.Fatal("a standby with nothing unexpired to read reported a usable token")
	}

	if err := store.Save(ctx, storable("live", "r2")); err != nil {
		t.Fatalf("the live agent's Save: %v", err)
	}

	live.Store(true)
	tok, err := cts.Token()
	if err != nil {
		t.Fatalf("Token once promoted: %v", err)
	}
	if tok.RefreshToken != "r2" {
		t.Errorf("promoted standby answered with %q, want the stored r2", tok.RefreshToken)
	}
	if refresher.callCount() != 0 {
		t.Errorf("refreshed %d times from a token the live agent retired, want 0", refresher.callCount())
	}
}

// A rotation that reached only the file is not one the row holds, and the row
// is what the next load prefers. Recording it as saved leaves the row wrong
// until the next rotation -- about an access token's lifetime -- and a
// restart in that window loads the superseded token from a healthy database
// and cannot refresh it. The connect loop calls Token per dial, so retrying
// the primary converges in seconds.
func TestCachingTokenSource_ARotationThatOnlyReachedTheFallbackIsWrittenAgain(t *testing.T) {
	primary := &memStore{failSave: fmt.Errorf("%w: connection reset by peer", ErrStoreUnavailable)}
	secondary := &memStore{}
	cts := &cachingTokenSource{
		store: NewFallback(primary, secondary),
		out:   io.Discard,
		live:  func() bool { return true },
		inner: &stubTokenSource{tokens: []*oauth2.Token{storable("a", "r2")}},
	}

	for i := 0; i < 2; i++ {
		if _, err := cts.Token(); err != nil {
			t.Fatalf("Token: %v", err)
		}
	}

	if _, saves := primary.counts(); saves != 2 {
		t.Errorf("the primary was written %d times, want 2: a write it never took is not a write", saves)
	}
}

// conflictStore rejects the next Save the way a compare-and-swap store does
// when another process wrote the row first, and answers reads with what that
// process left there.
type conflictStore struct {
	*memStore
	conflicts int
}

func (c *conflictStore) Save(ctx context.Context, tok *oauth2.Token) error {
	if c.conflicts > 0 {
		c.conflicts--
		return ErrStoreConflict
	}
	return c.memStore.Save(ctx, tok)
}

// Two live agents at once is a designed state, not a Kubernetes failure:
// leadership can be forced when the lock holder is gone without having
// released it. The loser of a write must take the winner's token rather than
// go on with one the store no longer holds -- and must not record its own as
// stored.
func TestCachingTokenSource_AWriteThatLostToAnotherProcessTakesTheWinnersToken(t *testing.T) {
	ctx := context.Background()
	backing := &memStore{}
	backing.seed(t, storable("winner", "r2"))
	store := &conflictStore{memStore: backing, conflicts: 1}

	cts := &cachingTokenSource{
		store: store,
		out:   io.Discard,
		live:  func() bool { return true },
		inner: &stubTokenSource{tokens: []*oauth2.Token{storable("loser", "r9")}},
	}

	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	tok, err := cts.Token()
	if err != nil {
		t.Fatalf("Token after the lost write: %v", err)
	}
	if tok.RefreshToken != "r2" {
		t.Errorf("answered with %q, want the winner's stored token", tok.RefreshToken)
	}
	stored, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored.RefreshToken != "r2" {
		t.Errorf("the row now holds %q, want the winner's token untouched", stored.RefreshToken)
	}
}

// withGatePoll shortens the interval at which a login in progress notices
// that this process is no longer the live agent, so a test need not wait a
// real one.
func withGatePoll(t *testing.T, d time.Duration) {
	t.Helper()
	orig := gatePoll
	gatePoll = d
	t.Cleanup(func() { gatePoll = orig })
}

// A device code blocks for as long as it lasts, which is long enough to
// outlive the turn that printed it. Cold start with an empty row: this pod
// prints a code nobody answers and loses the lock, its successor prints its
// own and an operator answers that one. The first login must stop with the
// turn -- left waiting, it would answer to a grant of its own and store it
// over the successor's, and the row would stop describing the pod in the
// game.
func TestCachingTokenSource_TheDeviceCodeLoginEndsWithTheTurn(t *testing.T) {
	withGatePoll(t, time.Millisecond)
	printed := make(chan struct{})
	withStubLogin(t, func(ctx context.Context, out io.Writer) (*oauth2.Token, error) {
		close(printed)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	var live atomic.Bool
	live.Store(true)
	cts := &cachingTokenSource{
		store:    &memStore{},
		out:      io.Discard,
		live:     live.Load,
		loginCtx: context.Background(),
	}

	done := make(chan error, 1)
	go func() {
		_, err := cts.Token()
		done <- err
	}()

	<-printed
	live.Store(false)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the login outlived the turn and reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the login was still waiting for a code after the turn ended")
	}
}

// And the same for a code answered at the moment the turn ends: the grant is
// real, but it belongs to a process that no longer holds the account's login.
func TestCachingTokenSource_AGrantThatArrivesAfterTheTurnIsNotStored(t *testing.T) {
	ctx := context.Background()
	var live atomic.Bool
	live.Store(true)
	withStubLogin(t, func(context.Context, io.Writer) (*oauth2.Token, error) {
		// The operator answers just as the lock moves on.
		live.Store(false)
		return storable("a", "grant-a"), nil
	})

	store := &memStore{}
	cts := &cachingTokenSource{
		store:    store,
		out:      io.Discard,
		live:     live.Load,
		loginCtx: ctx,
	}

	if _, err := cts.Token(); err == nil {
		t.Fatal("a grant that arrived after the turn was reported as this process's token")
	}
	if _, saves := store.counts(); saves != 0 {
		t.Errorf("stored the late grant %d times, want 0: the successor's row is not this process's to write", saves)
	}
}

const interactionRequired = "The user could not be authenticated or user interaction is required. The user must sign in again and if needed grant the client application access to the requested scope."

func invalidGrant(description string) error {
	return fmt.Errorf("xal/sisu: request access token for authorization: %w", &oauth2.RetrieveError{
		ErrorCode:        "invalid_grant",
		ErrorDescription: description,
	})
}

// signInRefusal is what Microsoft answers with once it will no longer issue a
// token without a person doing something, in the shape the client library
// returns it.
func signInRefusal() error { return invalidGrant(interactionRequired) }

// abuseHold is the other invalid_grant: the account itself is held, and no
// login of any kind gets past it until Microsoft lifts the hold.
func abuseHold() error { return invalidGrant("User account is found to be in service abuse mode.") }

// hookLog records what a token source reported through its hooks.
type hookLog struct {
	refreshRejected int
	required        []bool
	signInRejected  int
	abuse           []bool
}

func (h *hookLog) hooks() Hooks {
	return Hooks{
		RefreshRejected: func() { h.refreshRejected++ },
		SignInRequired:  func(required bool) { h.required = append(h.required, required) },
		SignInRejected:  func() { h.signInRejected++ },
		AbuseHold:       func(held bool) { h.abuse = append(h.abuse, held) },
	}
}

func (h *hookLog) wantRequired(t *testing.T, want ...bool) {
	t.Helper()
	if fmt.Sprint(h.required) != fmt.Sprint(want) {
		t.Errorf("sign-in required = %v, want %v", h.required, want)
	}
}

// refusedSource is a live process holding the stored token r0, whose every
// refresh fails with refusal.
func refusedSource(t *testing.T, store *memStore, refusal error) (*cachingTokenSource, *stubTokenSource, *hookLog) {
	t.Helper()
	store.seed(t, &oauth2.Token{AccessToken: "stale", RefreshToken: "r0", Expiry: time.Now().Add(-time.Minute)})
	refresher := &stubTokenSource{err: refusal}
	seen := &hookLog{}
	return &cachingTokenSource{
		store:    store,
		out:      io.Discard,
		loginCtx: context.Background(),
		live:     func() bool { return true },
		inner:    refresher,
		held:     &oauth2.Token{AccessToken: "stale", RefreshToken: "r0", Expiry: time.Now().Add(-time.Minute)},
		hooks:    seen.hooks(),
		floor:    2 * time.Hour,
	}, refresher, seen
}

func (s *stubTokenSource) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// notSignIn is what paceOf answers for an error that is not a sign-in's.
const notSignIn SignInPace = -1

func paceOf(err error) SignInPace {
	var signIn *SignInError
	if errors.As(err, &signIn) {
		return signIn.Pace
	}
	return notSignIn
}

// fakeClock is a clock a test moves by hand, for the pacing that is measured
// in hours.
type fakeClock struct{ at time.Time }

func (f *fakeClock) now() time.Time          { return f.at }
func (f *fakeClock) advance(d time.Duration) { f.at = f.at.Add(d) }

func withFakeClock(cts *cachingTokenSource) *fakeClock {
	clock := &fakeClock{at: time.Date(2026, 10, 8, 16, 42, 0, 0, time.UTC)}
	cts.now = clock.now
	return clock
}

const heldForAbuse = "User account is found to be in service abuse mode."

// refuse makes calls that must each end in the refused refresh itself.
func refuse(t *testing.T, cts *cachingTokenSource, times int) {
	t.Helper()
	for attempt := 1; attempt <= times; attempt++ {
		_, err := cts.Token()
		if !IsRejection(err) || paceOf(err) != notSignIn {
			t.Fatalf("attempt %d: err = %v, want the refused refresh itself", attempt, err)
		}
	}
}

func loginReturning(t *testing.T, tok *oauth2.Token, err error) *int {
	t.Helper()
	calls := 0
	withStubLogin(t, func(context.Context, io.Writer) (*oauth2.Token, error) {
		calls++
		return tok, err
	})
	return &calls
}

func noLogin(t *testing.T, why string) {
	t.Helper()
	withStubLogin(t, func(context.Context, io.Writer) (*oauth2.Token, error) {
		t.Error(why)
		return nil, errors.New("unreachable")
	})
}

func pollRefused(description string) error {
	return fmt.Errorf("poll device token: %w", &oauth2.RetrieveError{ErrorCode: "invalid_grant", ErrorDescription: description})
}

// A refresh token Microsoft has stopped honouring was retried forever, and
// the agent stayed out of the game until somebody edited the row and deleted
// the pod. A bounded number of refusals has to end in the login prompt.
func TestCachingTokenSource_AStoredTokenRefusedRepeatedlyEndsInASignIn(t *testing.T) {
	store := &memStore{}
	cts, refresher, seen := refusedSource(t, store, signInRefusal())
	logins := loginReturning(t, &oauth2.Token{AccessToken: "fresh", RefreshToken: "fresh-refresh", Expiry: time.Now().Add(time.Hour)}, nil)

	refuse(t, cts, refusalsBeforeSignIn-1)
	if *logins != 0 || len(seen.required) != 0 {
		t.Fatalf("after %d refusals: logins=%d required=%v, want neither yet", refusalsBeforeSignIn-1, *logins, seen.required)
	}

	tok, err := cts.Token()
	if err != nil {
		t.Fatalf("Token on the final refusal: %v", err)
	}
	if tok.RefreshToken != "fresh-refresh" {
		t.Errorf("got %q, want the token the sign-in returned", tok.RefreshToken)
	}
	if *logins != 1 {
		t.Errorf("login called %d times, want 1", *logins)
	}
	if refresher.callCount() != refusalsBeforeSignIn {
		t.Errorf("refreshed the refused token %d times, want exactly %d", refresher.callCount(), refusalsBeforeSignIn)
	}
	if saved, err := store.Load(context.Background()); err != nil || saved.RefreshToken != "fresh-refresh" {
		t.Errorf("store holds %v (%v), want the sign-in's token in place of the refused one", saved, err)
	}
	seen.wantRequired(t, true, false)
	if seen.signInRejected != 0 {
		t.Errorf("sign-in rejections = %d, want 0", seen.signInRejected)
	}
}

// From the refusal that offers a sign-in onwards, the refused refresh is
// followed by the login in the same call and never reaches the connect loop.
// An alert on the rate of rejections would resolve with the agent still out
// of the game unless each is counted where it happens.
func TestCachingTokenSource_EveryRefusedRefreshIsCountedEvenWhenASignInFollows(t *testing.T) {
	cts, _, seen := refusedSource(t, &memStore{}, signInRefusal())
	clock := withFakeClock(cts)
	loginReturning(t, nil, context.DeadlineExceeded)

	refuse(t, cts, refusalsBeforeSignIn-1)
	for range 3 {
		if _, err := cts.Token(); paceOf(err) != SignInRetryBackoff {
			t.Fatalf("err = %v, want an unanswered sign-in", err)
		}
		clock.advance(cts.floor)
	}
	if want := refusalsBeforeSignIn + 2; seen.refreshRejected != want {
		t.Errorf("refused refreshes counted = %d, want %d", seen.refreshRejected, want)
	}
}

// Microsoft refusing the refresh and then the sign-in too means the account
// is what it objects to. The stored token is then the only thing that will
// notice the account come back, so it must survive the failed sign-in -- in
// the store for the next process, and in this one to be tried again.
func TestCachingTokenSource_ASignInMicrosoftRefusesLeavesTheStoredTokenInPlace(t *testing.T) {
	store := &memStore{}
	cts, refresher, seen := refusedSource(t, store, signInRefusal())
	loginReturning(t, nil, pollRefused(interactionRequired))

	refuse(t, cts, refusalsBeforeSignIn-1)
	_, err := cts.Token()
	if paceOf(err) != SignInRetrySoon {
		t.Fatalf("pace of %v = %v, want SignInRetrySoon for a first refusal", err, paceOf(err))
	}
	if !IsRejection(err) {
		t.Errorf("err = %v, want Microsoft's refusal still recognisable in it", err)
	}
	if seen.signInRejected != 1 {
		t.Errorf("sign-in rejections = %d, want 1", seen.signInRejected)
	}
	seen.wantRequired(t, true)
	if saved, err := store.Load(context.Background()); err != nil || saved.RefreshToken != "r0" {
		t.Errorf("store holds %v (%v), want the refused token untouched", saved, err)
	}
	if _, saves := store.counts(); saves != 0 {
		t.Errorf("store written %d times, want 0", saves)
	}

	// Whatever was wrong with the account is put right: the same token
	// refreshes again, with nobody signing in.
	cts.lastRefresh = time.Now().Add(-cts.floor)
	refresher.mu.Lock()
	refresher.err = nil
	refresher.tokens = []*oauth2.Token{{AccessToken: "back", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}}
	refresher.mu.Unlock()
	tok, err := cts.Token()
	if err != nil || tok.RefreshToken != "r1" {
		t.Fatalf("Token once the account recovered = %v, %v; want the refreshed r1", tok, err)
	}
	seen.wantRequired(t, true, false)
}

// The case this pacing exists for: a person completing code after code that
// Microsoft goes on refusing. Each is an attempt on the account, so only the
// first may be followed quickly.
func TestCachingTokenSource_RepeatedlyRefusedSignInsFallBackToTheFloor(t *testing.T) {
	seen := &hookLog{}
	loginReturning(t, nil, pollRefused(interactionRequired))
	ts, err := TokenSource(context.Background(), &memStore{}, io.Discard, WithHooks(seen.hooks()))
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}

	for attempt := 1; attempt <= signInRefusalsBeforeFloor+2; attempt++ {
		_, err := ts.Token()
		want := SignInRetryFloor
		if attempt < signInRefusalsBeforeFloor {
			want = SignInRetrySoon
		}
		if got := paceOf(err); got != want {
			t.Errorf("refusal %d: pace = %v, want %v", attempt, got, want)
		}
	}
	if want := signInRefusalsBeforeFloor + 2; seen.signInRejected != want {
		t.Errorf("sign-in rejections = %d, want %d", seen.signInRejected, want)
	}
	seen.wantRequired(t, true)
}

// A code that expires between two refusals says nothing about the account,
// so it must not buy the next refusal another quick retry.
func TestCachingTokenSource_AnExpiredCodeDoesNotResetTheRunOfRefusedSignIns(t *testing.T) {
	var next error
	withStubLogin(t, func(context.Context, io.Writer) (*oauth2.Token, error) { return nil, next })
	ts, err := TokenSource(context.Background(), &memStore{}, io.Discard)
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}

	paces := []SignInPace{}
	for _, next = range []error{pollRefused(interactionRequired), context.DeadlineExceeded, pollRefused(interactionRequired)} {
		_, err := ts.Token()
		paces = append(paces, paceOf(err))
	}
	if want := []SignInPace{SignInRetrySoon, SignInRetryUnpaced, SignInRetryFloor}; fmt.Sprint(paces) != fmt.Sprint(want) {
		t.Errorf("paces = %v, want %v", paces, want)
	}
}

// A sign-in that works ends the run: the next refusal, whenever it comes, is
// a first one again.
func TestCachingTokenSource_ASignInThatWorksResetsTheRunOfRefusals(t *testing.T) {
	cts, _, _ := refusedSource(t, &memStore{}, signInRefusal())
	cts.signInRefusals = signInRefusalsBeforeFloor + 3
	loginReturning(t, &oauth2.Token{AccessToken: "fresh", RefreshToken: "fresh-refresh", Expiry: time.Now().Add(time.Hour)}, nil)

	refuse(t, cts, refusalsBeforeSignIn-1)
	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if cts.signInRefusals != 0 {
		t.Errorf("signInRefusals = %d, want 0", cts.signInRefusals)
	}
}

// A code nobody answered is reported as that and nothing more: it is not a
// rejection, it is not counted as one, and the next code may follow at once.
func TestCachingTokenSource_AnExpiredCodeIsAFailedSignInNotARejectedOne(t *testing.T) {
	for name, expiry := range map[string]error{
		"deadline":      fmt.Errorf("poll device token: %w", context.DeadlineExceeded),
		"expired_token": fmt.Errorf("poll device token: %w", &oauth2.RetrieveError{ErrorCode: "expired_token"}),
	} {
		t.Run(name, func(t *testing.T) {
			seen := &hookLog{}
			loginReturning(t, nil, expiry)
			ts, err := TokenSource(context.Background(), &memStore{}, io.Discard, WithHooks(seen.hooks()))
			if err != nil {
				t.Fatalf("TokenSource: %v", err)
			}

			_, err = ts.Token()
			if paceOf(err) != SignInRetryUnpaced {
				t.Errorf("pace of %v = %v, want SignInRetryUnpaced", err, paceOf(err))
			}
			if IsRejection(err) {
				t.Errorf("err = %v, classed as a rejection", err)
			}
			if seen.signInRejected != 0 {
				t.Errorf("sign-in rejections = %d, want 0", seen.signInRejected)
			}
			seen.wantRequired(t, true)
		})
	}
}

// A code that fails at once is redialled within seconds, and each redial
// would otherwise put the refused token in front of Microsoft again -- far
// faster than the floor a rejection is supposed to wait.
func TestCachingTokenSource_AFailingSignInDoesNotCarryARefreshWithEveryAttempt(t *testing.T) {
	cts, refresher, _ := refusedSource(t, &memStore{}, signInRefusal())
	logins := loginReturning(t, nil, errors.New("start device auth: 429 Too Many Requests"))

	refuse(t, cts, refusalsBeforeSignIn-1)
	for range 4 {
		if _, err := cts.Token(); paceOf(err) != SignInRetryBackoff {
			t.Fatalf("err = %v, want the sign-in's own failure", err)
		}
	}
	if *logins != 4 {
		t.Errorf("login called %d times, want 4", *logins)
	}
	if refresher.callCount() != refusalsBeforeSignIn {
		t.Errorf("refreshed the refused token %d times, want %d: none while the floor lasts", refresher.callCount(), refusalsBeforeSignIn)
	}

	cts.lastRefresh = time.Now().Add(-cts.floor)
	_, _ = cts.Token()
	if refresher.callCount() != refusalsBeforeSignIn+1 {
		t.Errorf("refreshed %d times, want one more once the floor passed", refresher.callCount())
	}
}

// The floor is timed from the refresh, not from what the store said about it
// afterwards. With the store gone the refusal cannot be confirmed, and if
// that also left the attempt untimed every fast-failing sign-in would carry
// another refresh.
func TestCachingTokenSource_AStoreThatGoesAwayDoesNotUnpaceARefusedToken(t *testing.T) {
	store := &memStore{}
	cts, refresher, _ := refusedSource(t, store, signInRefusal())
	loginReturning(t, nil, errors.New("start device auth: 429 Too Many Requests"))

	refuse(t, cts, refusalsBeforeSignIn-1)
	if _, err := cts.Token(); paceOf(err) != SignInRetryBackoff {
		t.Fatalf("err = %v, want the sign-in on offer", err)
	}
	store.mu.Lock()
	store.failLoad = ErrStoreUnavailable
	store.mu.Unlock()

	cts.lastRefresh = time.Now().Add(-cts.floor)
	for range 4 {
		if _, err := cts.Token(); paceOf(err) != SignInRetryBackoff {
			t.Fatalf("err = %v, want the sign-in still on offer", err)
		}
	}
	if want := refusalsBeforeSignIn + 1; refresher.callCount() != want {
		t.Errorf("refreshed the refused token %d times, want %d: one once the floor passed and none after", refresher.callCount(), want)
	}
}

// An account under an abuse hold is refused at every door, the sign-in page
// included, and each further attempt is what the hold is counting. A prompt
// would be a code nobody can complete, printed on a timer.
func TestCachingTokenSource_AnAbuseHoldIsNeverAnsweredWithASignIn(t *testing.T) {
	cts, _, seen := refusedSource(t, &memStore{}, abuseHold())
	noLogin(t, "an abuse hold reached the device-code login")

	refuse(t, cts, refusalsBeforeSignIn+2)
	seen.wantRequired(t)
	if want := []bool{true}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
		t.Errorf("abuse hold = %v, want %v", seen.abuse, want)
	}
	if want := refusalsBeforeSignIn + 2; seen.refreshRejected != want {
		t.Errorf("refused refreshes counted = %d, want %d", seen.refreshRejected, want)
	}
}

// Refusals under a hold say nothing about the token, so they must not be
// banked: otherwise the first ordinary refusal after the hold prompts at
// once, on the strength of one. Nor does a differently worded refusal end the
// hold -- see AHoldLearnedAtTheSignInOutlastsWhatTheRefreshSays for why.
func TestCachingTokenSource_AbuseHoldRefusalsDoNotCountTowardsASignIn(t *testing.T) {
	cts, refresher, seen := refusedSource(t, &memStore{}, abuseHold())
	noLogin(t, "a sign-in was offered under a hold")

	refuse(t, cts, refusalsBeforeSignIn)
	refresher.fail(signInRefusal())
	refuse(t, cts, refusalsBeforeSignIn+3)
	if want := []bool{true}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
		t.Errorf("abuse hold = %v, want %v", seen.abuse, want)
	}
}

// A hold that arrives while a sign-in is on offer withdraws it: the prompt
// stops, the report that one is wanted stops, and both stay stopped.
func TestCachingTokenSource_AnAbuseHoldWithdrawsASignInAlreadyOnOffer(t *testing.T) {
	cts, refresher, seen := refusedSource(t, &memStore{}, signInRefusal())
	clock := withFakeClock(cts)
	logins := loginReturning(t, nil, context.DeadlineExceeded)

	refuse(t, cts, refusalsBeforeSignIn-1)
	if _, err := cts.Token(); paceOf(err) != SignInRetryBackoff {
		t.Fatalf("err = %v, want the sign-in on offer", err)
	}

	clock.advance(cts.floor)
	refresher.fail(abuseHold())
	refuse(t, cts, 2)
	refresher.fail(signInRefusal())
	refuse(t, cts, refusalsBeforeSignIn+3)
	if *logins != 1 {
		t.Errorf("login called %d times, want only the one before the hold", *logins)
	}
	seen.wantRequired(t, true, false)
}

// Under a hold the two doors disagree: the sign-in is refused as a hold while
// the refresh beside it goes on being refused as needing a person. Taking the
// refresh at its word forgot the hold at the very next attempt, counted three
// more refusals and printed another code -- every third call, for as long as
// the hold lasted. A hold learned at the sign-in has to be remembered.
func TestCachingTokenSource_AHoldLearnedAtTheSignInOutlastsWhatTheRefreshSays(t *testing.T) {
	cts, refresher, seen := refusedSource(t, &memStore{}, signInRefusal())
	clock := withFakeClock(cts)
	logins := loginReturning(t, nil, pollRefused(heldForAbuse))

	refuse(t, cts, refusalsBeforeSignIn-1)
	if _, err := cts.Token(); paceOf(err) != SignInRetryFloor {
		t.Fatalf("pace of %v = %v, want SignInRetryFloor", err, paceOf(err))
	}

	// Four times the refusals that bought the first prompt, at the floor's
	// own cadence, and still well inside the cooldown.
	for range 4 * refusalsBeforeSignIn {
		clock.advance(cts.floor)
		refuse(t, cts, 1)
	}
	if *logins != 1 {
		t.Fatalf("login called %d times, want 1: the hold was forgotten", *logins)
	}
	if cts.refusals != 0 {
		t.Errorf("refusals = %d, want none counted under a hold", cts.refusals)
	}
	seen.wantRequired(t, true, false)
	if want := []bool{true}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
		t.Errorf("abuse hold = %v, want %v: reported once and left standing", seen.abuse, want)
	}

	// A token that works is what ends it.
	refresher.mu.Lock()
	refresher.err = nil
	refresher.tokens = []*oauth2.Token{{AccessToken: "back", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)}}
	refresher.mu.Unlock()
	if _, err := cts.Token(); err != nil {
		t.Fatalf("Token once the hold lifted: %v", err)
	}
	if want := []bool{true, false}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
		t.Errorf("abuse hold = %v, want %v", seen.abuse, want)
	}
}

// A hold can lift over a token that stays dead, and then nothing would ever
// work to end it. Left unmentioned for the cooldown it is taken to be over,
// and the refusals that follow count from one.
func TestCachingTokenSource_AHoldLeftUnmentionedForTheCooldownIsLetGo(t *testing.T) {
	cts, _, seen := refusedSource(t, &memStore{}, signInRefusal())
	clock := withFakeClock(cts)
	logins := loginReturning(t, nil, pollRefused(heldForAbuse))

	refuse(t, cts, refusalsBeforeSignIn-1)
	_, _ = cts.Token()

	clock.advance(holdCooldownFloors*cts.floor - time.Second)
	refuse(t, cts, 1)
	if cts.refusals != 0 {
		t.Fatalf("refusals = %d just inside the cooldown, want 0", cts.refusals)
	}

	clock.advance(time.Second)
	refuse(t, cts, refusalsBeforeSignIn-1)
	if *logins != 1 {
		t.Fatalf("login called %d times before the count was complete", *logins)
	}
	if got := seen.abuse; len(got) != 2 || got[1] {
		t.Errorf("abuse hold = %v, want it withdrawn once the cooldown passed", got)
	}
	clock.advance(cts.floor)
	if _, err := cts.Token(); !IsSignIn(err) {
		t.Fatalf("err = %v, want a sign-in offered again after the cooldown", err)
	}
	if *logins != 2 {
		t.Errorf("login called %d times, want 2", *logins)
	}
}

// A token merely read back from the store has proved nothing, so it must not
// end a hold or the run of refused sign-ins. Only Microsoft issuing a token
// does.
func TestCachingTokenSource_ATokenReloadedFromTheStoreDoesNotEndAHold(t *testing.T) {
	cts, _, _ := refusedSource(t, &memStore{}, abuseHold())
	cts.signInRefusals = 2
	refuse(t, cts, 1)

	cts.adopt(&oauth2.Token{AccessToken: "other", RefreshToken: "r9", Expiry: time.Now().Add(time.Hour)}, originStore)
	if !cts.abuseHold || cts.signInRefusals != 2 {
		t.Errorf("abuseHold=%v signInRefusals=%d after a reload, want true and 2", cts.abuseHold, cts.signInRefusals)
	}
}

// The sign-in is refused under a hold exactly as the refresh is. That has to
// stop the prompting and wait the floor, with or without a stored token.
func TestCachingTokenSource_AnAbuseHoldAnsweringTheSignInStopsThePrompting(t *testing.T) {
	const held = "User account is found to be in service abuse mode."

	t.Run("beside a refused token", func(t *testing.T) {
		cts, _, seen := refusedSource(t, &memStore{}, signInRefusal())
		logins := loginReturning(t, nil, pollRefused(held))

		refuse(t, cts, refusalsBeforeSignIn-1)
		if _, err := cts.Token(); paceOf(err) != SignInRetryFloor {
			t.Fatalf("pace of %v = %v, want SignInRetryFloor", err, paceOf(err))
		}
		seen.wantRequired(t, true, false)
		if want := []bool{true}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
			t.Errorf("abuse hold = %v, want %v", seen.abuse, want)
		}

		refuse(t, cts, 4*refusalsBeforeSignIn)
		if *logins != 1 {
			t.Errorf("login called %d times, want 1: the offer was not withdrawn", *logins)
		}
	})

	t.Run("on a cold start", func(t *testing.T) {
		seen := &hookLog{}
		loginReturning(t, nil, pollRefused(held))
		ts, err := TokenSource(context.Background(), &memStore{}, io.Discard, WithHooks(seen.hooks()))
		if err != nil {
			t.Fatalf("TokenSource: %v", err)
		}

		if _, err := ts.Token(); paceOf(err) != SignInRetryFloor {
			t.Fatalf("pace of %v = %v, want SignInRetryFloor", err, paceOf(err))
		}
		// With no token to go back to, asking is all there is: the agent
		// goes on wanting a sign-in, and says so alongside the hold.
		seen.wantRequired(t, true)
		if want := []bool{true}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
			t.Errorf("abuse hold = %v, want %v", seen.abuse, want)
		}
		if seen.signInRejected != 1 {
			t.Errorf("sign-in rejections = %d, want 1", seen.signInRejected)
		}
	})
}

// Wording never seen before gets what every rejection got before the two
// known ones were told apart: the floor, and no prompt. A reworded or
// localised message must fail towards doing less.
func TestCachingTokenSource_AnUnrecognisedRejectionIsNeverAnsweredWithASignIn(t *testing.T) {
	cts, _, seen := refusedSource(t, &memStore{}, invalidGrant("Der Benutzer muss sich erneut anmelden."))
	noLogin(t, "an unrecognised rejection reached the device-code login")

	refuse(t, cts, refusalsBeforeSignIn+2)
	seen.wantRequired(t)
	if len(seen.abuse) != 0 {
		t.Errorf("abuse hold = %v, want never reported for wording that does not say so", seen.abuse)
	}
}

func TestCachingTokenSource_ASignInRefusedInUnrecognisedWordsWaitsTheFloor(t *testing.T) {
	loginReturning(t, nil, pollRefused("Der Benutzer muss sich erneut anmelden."))
	ts, err := TokenSource(context.Background(), &memStore{}, io.Discard)
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	if _, err := ts.Token(); paceOf(err) != SignInRetryFloor {
		t.Errorf("pace of %v = %v, want SignInRetryFloor", err, paceOf(err))
	}
}

// Only Microsoft saying no counts. A refresh that failed because the network
// did says nothing about the token.
func TestCachingTokenSource_AFailedRefreshThatIsNotARefusalNeverEndsInASignIn(t *testing.T) {
	cts, _, seen := refusedSource(t, &memStore{}, errors.New("Post https://login.live.com/oauth20_token.srf: context deadline exceeded"))
	noLogin(t, "a network failure reached the device-code login")

	for attempt := 1; attempt <= refusalsBeforeSignIn+2; attempt++ {
		if _, err := cts.Token(); err == nil {
			t.Fatalf("attempt %d: no error from a refresh that failed", attempt)
		}
	}
	seen.wantRequired(t)
	if seen.refreshRejected != 0 {
		t.Errorf("refused refreshes counted = %d, want 0", seen.refreshRejected)
	}
}

// A refusal only says something about the token when the store agrees it is
// the current one. A store that cannot be read leaves that open, and a prompt
// on the strength of it would be a code printed for a database problem. The
// refusal itself still happened, and is still counted as one.
func TestCachingTokenSource_ARefusalTheStoreCannotConfirmDoesNotCountTowardsASignIn(t *testing.T) {
	store := &memStore{}
	cts, _, seen := refusedSource(t, store, signInRefusal())
	store.mu.Lock()
	store.failLoad = ErrStoreUnavailable
	store.mu.Unlock()
	noLogin(t, "an unconfirmed refusal reached the device-code login")

	refuse(t, cts, refusalsBeforeSignIn+2)
	if cts.refusals != 0 {
		t.Errorf("refusals = %d, want 0", cts.refusals)
	}
	if want := refusalsBeforeSignIn + 2; seen.refreshRejected != want {
		t.Errorf("refused refreshes counted = %d, want %d", seen.refreshRejected, want)
	}
}

// A process that loses its turn under its own prompt has stopped asking, and
// nothing calls it again until it is promoted -- so the report that a sign-in
// is wanted has to be withdrawn as the login ends, not at the next call.
func TestCachingTokenSource_LosingTheTurnMidSignInWithdrawsTheReport(t *testing.T) {
	withGatePoll(t, time.Millisecond)

	var live atomic.Bool
	live.Store(true)
	seen := &hookLog{}
	withStubLogin(t, func(ctx context.Context, _ io.Writer) (*oauth2.Token, error) {
		live.Store(false)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ts, err := TokenSource(context.Background(), &memStore{}, io.Discard, WithLiveGate(live.Load), WithHooks(seen.hooks()))
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}

	if _, err := ts.Token(); paceOf(err) != SignInRetryUnpaced {
		t.Fatalf("err = %v, want the sign-in ended by the turn", err)
	}
	seen.wantRequired(t, true, false)
}

// The same report must not outlive a demotion that happens between calls.
func TestCachingTokenSource_AStandbyNeverReportsThatASignInIsWanted(t *testing.T) {
	var live atomic.Bool
	live.Store(true)
	seen := &hookLog{}
	loginReturning(t, nil, pollRefused(interactionRequired))
	ts, err := TokenSource(context.Background(), &memStore{}, io.Discard, WithLiveGate(live.Load), WithHooks(seen.hooks()))
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	_, _ = ts.Token()
	seen.wantRequired(t, true)

	live.Store(false)
	if _, err := ts.Token(); !errors.Is(err, ErrStandbyUnwarmed) {
		t.Fatalf("standby Token = %v, want ErrStandbyUnwarmed", err)
	}
	seen.wantRequired(t, true, false)
}

func TestIsRejection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"typed invalid_grant", signInRefusal(), true},
		{"abuse hold", abuseHold(), true},
		{"untyped text", errors.New(`oauth2: "invalid_grant" "..."`), true},
		{"another oauth code", &oauth2.RetrieveError{ErrorCode: "temporarily_unavailable"}, false},
		{"network", errors.New("connection refused"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := IsRejection(tc.err); got != tc.want {
			t.Errorf("%s: IsRejection = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Nothing asks a standby for a token, so a process demoted between two codes
// never reaches the call that would withdraw what it last reported. The turn
// ending has to do it.
func TestStandDown_WithdrawsWhatWasReportedWithoutAnotherTokenCall(t *testing.T) {
	var live atomic.Bool
	live.Store(true)
	seen := &hookLog{}
	loginReturning(t, nil, pollRefused(heldForAbuse))
	ts, err := TokenSource(context.Background(), &memStore{}, io.Discard, WithLiveGate(live.Load), WithHooks(seen.hooks()))
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	_, _ = ts.Token()
	seen.wantRequired(t, true)
	if want := []bool{true}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
		t.Fatalf("abuse hold = %v, want %v", seen.abuse, want)
	}

	live.Store(false)
	StandDown(ts)
	seen.wantRequired(t, true, false)
	if want := []bool{true, false}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
		t.Errorf("abuse hold = %v, want %v", seen.abuse, want)
	}

	// Promoted again, it still knows what it knew, and says so at its first
	// call.
	live.Store(true)
	_, _ = ts.Token()
	seen.wantRequired(t, true, false, true)
	if want := []bool{true, false, true}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
		t.Errorf("abuse hold = %v, want %v", seen.abuse, want)
	}
}

// A refusal that arrives as the turn ends must not leave a hold reported by a
// process that is no longer the one to report it.
func TestCachingTokenSource_ARefusalArrivingAsTheTurnEndsReportsNothing(t *testing.T) {
	var live atomic.Bool
	live.Store(true)
	seen := &hookLog{}
	withStubLogin(t, func(context.Context, io.Writer) (*oauth2.Token, error) {
		live.Store(false)
		return nil, pollRefused(heldForAbuse)
	})
	ts, err := TokenSource(context.Background(), &memStore{}, io.Discard, WithLiveGate(live.Load), WithHooks(seen.hooks()))
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}

	_, _ = ts.Token()
	seen.wantRequired(t, true, false)
	if len(seen.abuse) != 0 {
		t.Errorf("abuse hold = %v, want never reported by a process that had lost its turn", seen.abuse)
	}
}

// Only a refused poll has a person's completed sign-in behind it. A code
// Microsoft would not issue is a refusal to be paced like one, but nobody
// signed in.
func TestCachingTokenSource_ARefusedCodeRequestIsNotARefusedSignIn(t *testing.T) {
	seen := &hookLog{}
	loginReturning(t, nil, fmt.Errorf("start device auth: %w", &oauth2.RetrieveError{ErrorCode: "invalid_grant", ErrorDescription: interactionRequired}))
	ts, err := TokenSource(context.Background(), &memStore{}, io.Discard, WithHooks(seen.hooks()))
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}

	if _, err := ts.Token(); paceOf(err) != SignInRetryFloor {
		t.Errorf("pace of %v = %v, want SignInRetryFloor", err, paceOf(err))
	}
	if seen.signInRejected != 0 {
		t.Errorf("sign-in rejections = %d, want 0", seen.signInRejected)
	}
}

// The second refresh a rejection can lead to -- of the token the store handed
// over in place of the refused one -- is as much a refusal as the first, and
// was counted without ever being classified.
func TestCachingTokenSource_ARefusalOfTheReloadedTokenIsClassifiedToo(t *testing.T) {
	store := &memStore{}
	cts, _, seen := refusedSource(t, store, signInRefusal())
	store.seed(t, &oauth2.Token{AccessToken: "newer", RefreshToken: "r5", Expiry: time.Now().Add(-time.Minute)})
	orig := refreshSource
	refreshSource = func(*oauth2.Token, io.Writer) oauth2.TokenSource {
		return &stubTokenSource{err: abuseHold()}
	}
	t.Cleanup(func() { refreshSource = orig })

	refuse(t, cts, 1)
	if !cts.abuseHold {
		t.Error("the hold announced for the reloaded token was not recorded")
	}
	if want := []bool{true}; fmt.Sprint(seen.abuse) != fmt.Sprint(want) {
		t.Errorf("abuse hold = %v, want %v", seen.abuse, want)
	}
	if seen.refreshRejected != 2 {
		t.Errorf("refused refreshes counted = %d, want both", seen.refreshRejected)
	}
}

func TestSignInWait(t *testing.T) {
	const floor = 15 * time.Minute
	wrap := func(e *SignInError) error { return fmt.Errorf("dial: login to xbox live: %w", e) }
	refusal := pollRefused(interactionRequired)
	unanswered := func(n int) error {
		return wrap(&SignInError{Err: context.DeadlineExceeded, Pace: SignInRetryBackoff, Unanswered: n})
	}
	cases := []struct {
		name  string
		err   error
		want  time.Duration
		paced bool
	}{
		{"first refused sign-in", wrap(&SignInError{Err: refusal, Pace: SignInRetrySoon}), floor / 6, true},
		{"refused again", wrap(&SignInError{Err: refusal, Pace: SignInRetryFloor}), floor, true},
		{"first unanswered code", unanswered(1), floor / 6, true},
		{"second", unanswered(2), floor / 3, true},
		{"fifth", unanswered(5), floor * 16 / 6, true},
		{"capped", unanswered(6), 4 * floor, true},
		{"capped however many", unanswered(500), 4 * floor, true},
		{"unanswered with no token held", wrap(&SignInError{Err: context.DeadlineExceeded}), 0, false},
		// What survives being flattened to text is only that it was a
		// sign-in and whether Microsoft refused.
		{"a refusal flattened to text", errors.New(wrap(&SignInError{Err: refusal, Pace: SignInRetrySoon}).Error()), floor, true},
		{"an expiry flattened to text", errors.New(wrap(&SignInError{Err: context.DeadlineExceeded}).Error()), floor / 6, true},
		{"a refused refresh", signInRefusal(), 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range cases {
		wait, paced := SignInWait(tc.err, floor)
		if wait != tc.want || paced != tc.paced {
			t.Errorf("%s: SignInWait = %v, %v; want %v, %v", tc.name, wait, paced, tc.want, tc.paced)
		}
	}
}

// aDay drives a token source the way the connect loop does for 24 hours of a
// clock the test owns, and returns nothing: the caller counts what its stubs
// saw. Every wait is taken at half its length, which is the shortest the
// connect loop's jitter ever makes it, so what is counted is the most the
// agent can do in a day and not the average.
func aDay(t *testing.T, cts *cachingTokenSource, clock *fakeClock) {
	t.Helper()
	const ladderMin = 5 * time.Second
	for end := clock.now().Add(24 * time.Hour); clock.now().Before(end); {
		_, err := cts.Token()
		if err == nil {
			t.Fatal("Token succeeded in a day meant to be all refusals")
		}
		wait, paced := SignInWait(err, cts.floor)
		switch {
		case paced:
		case IsRejection(err):
			wait = cts.floor
		default:
			wait = ladderMin
		}
		clock.advance(wait / 2)
	}
}

// The case the pacing was written for, measured end to end: a person who
// answers every code within half a minute, and Microsoft refusing every one.
// Each refusal is an attempt on the account, so a whole day of them must not
// exceed what retrying the refused token alone used to make.
func TestCadence_APersonRefusedAtEveryCodeIsPacedLikeTheRejectionsTheyAre(t *testing.T) {
	const floor = 15 * time.Minute
	perDayBefore := int(24*time.Hour/(floor/2)) + 1

	cts, refresher, _ := refusedSource(t, &memStore{}, signInRefusal())
	cts.floor = floor
	clock := withFakeClock(cts)
	logins := 0
	withStubLogin(t, func(context.Context, io.Writer) (*oauth2.Token, error) {
		logins++
		clock.advance(30 * time.Second)
		return nil, pollRefused(interactionRequired)
	})

	aDay(t, cts, clock)
	if logins > perDayBefore {
		t.Errorf("%d refused sign-ins in a day, want at most the %d refusals a day the floor allowed before", logins, perDayBefore)
	}
	if got := refresher.callCount(); got > perDayBefore {
		t.Errorf("%d refreshes of the refused token in a day, want at most %d", got, perDayBefore)
	}
	if logins < 24 {
		t.Errorf("only %d sign-ins offered in a day: the person fixing the account is being kept waiting", logins)
	}
}

// And the other one: a sign-in on offer that nobody is there to answer. A
// code is polled every few seconds for the quarter of an hour it lasts, so
// codes printed back to back are a day-long stream of requests. Backed off,
// there are a few dozen.
func TestCadence_CodesNobodyAnswersAreBackedOff(t *testing.T) {
	const floor = 15 * time.Minute
	const codeLasts = 15 * time.Minute

	cts, refresher, _ := refusedSource(t, &memStore{}, signInRefusal())
	cts.floor = floor
	clock := withFakeClock(cts)
	logins := 0
	withStubLogin(t, func(context.Context, io.Writer) (*oauth2.Token, error) {
		logins++
		clock.advance(codeLasts)
		return nil, fmt.Errorf("poll device token: %w", context.DeadlineExceeded)
	})

	aDay(t, cts, clock)
	backToBack := int(24 * time.Hour / codeLasts)
	if logins > backToBack/2 {
		t.Errorf("%d codes printed in a day, want fewer than half the %d that back-to-back codes come to", logins, backToBack)
	}
	if logins < 12 {
		t.Errorf("only %d codes in a day: a person arriving late should find one within about two hours", logins)
	}
	if got, most := refresher.callCount(), int(24*time.Hour/(floor/2))+1; got > most {
		t.Errorf("%d refreshes of the refused token in a day, want at most %d", got, most)
	}
}
