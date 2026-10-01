package auth

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func newCodes(c *clock) *Codes {
	return &Codes{TTL: 10 * time.Minute, Max: 3, Now: c.now}
}

var codeShape = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)

func TestCodes_StartClaimPoll(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := newCodes(c)

	code, secret := d.Start("")
	// No 0/O or 1/I: the code is read off one screen and typed on another.
	if !codeShape.MatchString(code) || len(secret) < 32 {
		t.Fatalf("code %q, secret of %d chars", code, len(secret))
	}
	if state, _ := d.Poll(secret); state != Pending {
		t.Fatalf("before the claim: %v", state)
	}

	if err := d.Claim(strings.ToLower(code), steve); err != nil {
		t.Fatalf("Claim (typed in lower case): %v", err)
	}
	state, id := d.Poll(secret)
	if state != Claimed || id != steve {
		t.Fatalf("after the claim: %v %+v", state, id)
	}
	// A login is handed over exactly once.
	if state, _ := d.Poll(secret); state != Unknown {
		t.Errorf("second poll after the hand-over: %v, want unknown", state)
	}
}

func TestCodes_ClaimErrors(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := newCodes(c)
	code, _ := d.Start("")

	if err := d.Claim("ZZZZZZ", steve); !errors.Is(err, ErrUnknownCode) {
		t.Errorf("unknown code: %v", err)
	}
	for _, bad := range []string{"", "ABC", "ABCDEFG", "ABC 12", "ABCD-1", "ABCDE0"} {
		if err := d.Claim(bad, steve); !errors.Is(err, ErrUnknownCode) {
			t.Errorf("malformed %q: %v", bad, err)
		}
	}
	if err := d.Claim(code, Identity{Gamertag: "NoXUID"}); err == nil {
		t.Error("a claim with no XUID was accepted")
	}
	if err := d.Claim(code, steve); err != nil {
		t.Fatal(err)
	}
	// Someone else typing the same code afterwards must not take the login
	// over, nor learn whose it was.
	if err := d.Claim(code, Identity{XUID: "999", Gamertag: "Alex"}); !errors.Is(err, ErrUnknownCode) {
		t.Errorf("second claim: %v, want unknown", err)
	}
}

func TestCodes_Expire(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := newCodes(c)
	code, secret := d.Start("")

	c.t = c.t.Add(10*time.Minute + time.Second)
	if err := d.Claim(code, steve); !errors.Is(err, ErrUnknownCode) {
		t.Errorf("claim after expiry: %v", err)
	}
	if state, _ := d.Poll(secret); state != Unknown {
		t.Errorf("poll after expiry: %v", state)
	}
}

// A claimed login nobody collected must not wait forever for whoever later
// presents its secret.
func TestCodes_AClaimStillExpires(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := newCodes(c)
	code, secret := d.Start("")
	_ = d.Claim(code, steve)

	c.t = c.t.Add(11 * time.Minute)
	if state, _ := d.Poll(secret); state != Unknown {
		t.Errorf("claimed code polled after expiry: %v", state)
	}
}

func TestCodes_SameBrowserKeepsItsCode(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := newCodes(c)

	code, secret := d.Start("")
	again, sameSecret := d.Start(secret)
	if again != code || sameSecret != secret {
		t.Fatalf("a page reload got a new code: %q then %q", code, again)
	}
}

// Anyone on the internet can ask for a code, so the table can be filled at
// will. Refusing new logins when it is full would let one burst of requests
// lock every player out until those entries expired. The oldest waiting
// login is dropped instead: a flood then has to be sustained, and fast, to
// push out a code a player is still typing.
func TestCodes_AFullTableDropsTheOldestRatherThanRefusingNewLogins(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := newCodes(c) // room for 3

	oldCode, oldSecret := d.Start("")
	_, second := d.Start("")
	d.Start("")
	newCode, newSecret := d.Start("")

	if state, _ := d.Poll(oldSecret); state != Unknown {
		t.Errorf("the oldest login is still %v after the table overflowed", state)
	}
	if err := d.Claim(oldCode, steve); !errors.Is(err, ErrUnknownCode) {
		t.Errorf("the evicted code can still be claimed: %v", err)
	}
	if state, _ := d.Poll(second); state != Pending {
		t.Errorf("a login that was not the oldest was dropped: %v", state)
	}
	if err := d.Claim(newCode, steve); err != nil {
		t.Fatalf("the newest login cannot be claimed: %v", err)
	}
	if state, id := d.Poll(newSecret); state != Claimed || id != steve {
		t.Errorf("the newest login: %v %+v", state, id)
	}
}

// The table never holds more than Max, whatever is thrown at it.
func TestCodes_TableStaysBoundedUnderAFlood(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := &Codes{TTL: 10 * time.Minute, Max: 50, Now: c.now}
	for range 5000 {
		d.Start("")
	}
	if n := d.waiting(); n != 50 {
		t.Errorf("%d logins waiting with a limit of 50", n)
	}
	c.t = c.t.Add(11 * time.Minute)
	d.Start("")
	if n := d.waiting(); n != 1 {
		t.Errorf("%d logins waiting after all but one expired", n)
	}
}

func TestCodes_AreNotPredictable(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := &Codes{TTL: time.Minute, Max: 500, Now: c.now}
	seen := map[string]bool{}
	for range 200 {
		code, _ := d.Start("")
		if seen[code] {
			t.Fatalf("code %s issued twice while still pending", code)
		}
		seen[code] = true
	}
}

// The page asks for its code again whenever it loads. If that happened
// between the player typing the code and the page collecting the login, a
// fresh code here would strand a login the player had already earned.
func TestCodes_AReloadAfterTheClaimStillCollectsIt(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	d := newCodes(c)
	code, secret := d.Start("")
	if err := d.Claim(code, steve); err != nil {
		t.Fatal(err)
	}

	again, sameSecret := d.Start(secret)
	if again != code || sameSecret != secret {
		t.Fatalf("the reload was given a new login: %q then %q", code, again)
	}
	if state, id := d.Poll(secret); state != Claimed || id != steve {
		t.Errorf("after the reload: %v %+v", state, id)
	}
}

// Browsers poll, players claim and strangers start logins all at once; run
// under the race detector this is what says the table tolerates it.
func TestCodes_ConcurrentUse(t *testing.T) {
	d := &Codes{TTL: time.Minute, Max: 16, Now: time.Now}
	var wg sync.WaitGroup
	collected := make(chan Identity, 64)
	for range 64 {
		wg.Go(func() {
			code, secret := d.Start("")
			d.Start(secret)
			_ = d.Claim(code, steve)
			if state, id := d.Poll(secret); state == Claimed {
				collected <- id
			}
		})
	}
	wg.Wait()
	close(collected)
	for id := range collected {
		if id != steve {
			t.Fatalf("collected %+v", id)
		}
	}
	if n := d.waiting(); n > 16 {
		t.Errorf("%d logins waiting with a limit of 16", n)
	}
}
