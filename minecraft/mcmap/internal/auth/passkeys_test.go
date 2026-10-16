package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newPasskeys(t *testing.T, c *clock) (*Passkeys, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passkeys.json")
	p, err := LoadPasskeys(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return p, path
}

func key(id string, xuid string) Passkey {
	return Passkey{ID: []byte(id), PublicKey: []byte("pk-" + id), XUID: xuid, Gamertag: "Steve Builds", Label: "Phone"}
}

const issued = int64(1_790_000_000_000)

func pendingFor(p *Passkeys, id string) bool {
	_, _, pending, ok := p.Lookup(p.UserHandle(steve.XUID), []byte(id))
	return ok && pending
}

func confirmOne(t *testing.T, p *Passkeys, id string) Passkey {
	t.Helper()
	if err := p.StartPending(key(id, steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	code, _, err := p.IssueCode(steve.XUID)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := p.Confirm(steve.XUID, issued, []byte(id), code)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestPasskeys_PendingIsUselessUntilTheWhisperedCodeIsTyped(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	if err := p.StartPending(key("cred-1", steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	if p.Active(KeyOf([]byte("cred-1")), steve.XUID) || !pendingFor(p, "cred-1") {
		t.Fatal("a pending credential is active")
	}
	code, id, err := p.IssueCode(steve.XUID)
	if err != nil || len(code) != 6 || string(id) != "cred-1" {
		t.Fatalf("IssueCode = %q %q %v", code, id, err)
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), strings.ToLower(code)); err != nil {
		t.Fatalf("a right code typed in lower case: %v", err)
	}
	if !p.Active(KeyOf([]byte("cred-1")), steve.XUID) {
		t.Error("a confirmed credential is not active")
	}
}

func TestPasskeys_ASecondRegistrationWhileOneIsPendingIsRefused(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	if err := p.StartPending(key("cred-a", steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	if err := p.StartPending(key("cred-b", steve.XUID), issued); !errors.Is(err, ErrPendingExists) {
		t.Fatalf("second registration: %v, want ErrPendingExists", err)
	}
	// Any of the player's sessions may cancel, so losing the one that
	// started it does not lock them out; then a new one may start.
	if _, ok := p.Cancel("2535499999999999"); ok {
		t.Error("another player cancelled the pending credential")
	}
	if _, ok := p.Cancel(steve.XUID); !ok {
		t.Fatal("the player could not cancel")
	}
	if err := p.StartPending(key("cred-b", steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	c.t = c.t.Add(PendingFor)
	if err := p.StartPending(key("cred-c", steve.XUID), issued); err != nil {
		t.Errorf("after the pending one expired: %v", err)
	}
}

// Two browsers on one stolen cookie: the attacker's page cannot have its
// credential activated by the owner's page, which confirms its own.
func TestPasskeys_TheReplacementRace(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	_ = p.StartPending(key("owners", steve.XUID), issued)
	if err := p.StartPending(key("attackers", steve.XUID), issued); !errors.Is(err, ErrPendingExists) {
		t.Fatalf("the attacker replaced the pending credential: %v", err)
	}
	p.Cancel(steve.XUID)
	_ = p.StartPending(key("attackers", steve.XUID), issued)
	code, id, _ := p.IssueCode(steve.XUID)
	if Fingerprint(id) == Fingerprint([]byte("owners")) {
		t.Fatal("the whisper shows the owner's fingerprint for the attacker's credential")
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("owners"), code); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("the owner's page confirmed with the attacker's code: %v", err)
	}
	if p.Active(KeyOf([]byte("attackers")), steve.XUID) {
		t.Error("the attacker's credential was activated by the owner's page")
	}
}

func TestPasskeys_ThreeWrongCodesAbandonTheRegistration(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	_ = p.StartPending(key("cred-1", steve.XUID), issued)
	code, _, _ := p.IssueCode(steve.XUID)
	for want := 2; want >= 1; want-- {
		if _, left, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), "ZZZZZZ"); !errors.Is(err, ErrWrongCode) || left != want {
			t.Fatalf("wrong code: left %d err %v, want %d", left, err, want)
		}
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), "ZZZZZZ"); !errors.Is(err, ErrAbandoned) {
		t.Fatalf("third wrong code: %v, want ErrAbandoned", err)
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Fatalf("the right code after abandonment: %v", err)
	}
}

func TestPasskeys_ConfirmIsRefusedOutsideItsSessionAndTime(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	_ = p.StartPending(key("cred-1", steve.XUID), issued)
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), "ABC234"); !errors.Is(err, ErrWrongCode) {
		t.Errorf("before any code was issued: %v", err)
	}
	old, _, _ := p.IssueCode(steve.XUID)
	code, _, _ := p.IssueCode(steve.XUID)
	if old == code {
		t.Fatal("a second code is the same as the first")
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), old); !errors.Is(err, ErrWrongCode) {
		t.Errorf("a replaced code: %v", err)
	}
	if _, _, err := p.Confirm(steve.XUID, issued+1, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Errorf("another session's IssuedMilli: %v", err)
	}
	if _, _, err := p.Confirm("2535412345678902", issued, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Errorf("another XUID: %v", err)
	}
	c.t = c.t.Add(PendingFor)
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Errorf("after two minutes: %v", err)
	}
	if _, _, err := p.IssueCode(steve.XUID); !errors.Is(err, ErrNothingPending) {
		t.Errorf("a code for an expired registration: %v", err)
	}
}

// Review focus: a retried confirm adds the passkey once.
func TestPasskeys_ConfirmTwiceAddsOnce(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	_ = p.StartPending(key("cred-1", steve.XUID), issued)
	code, _, _ := p.IssueCode(steve.XUID)
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); !errors.Is(err, ErrNothingPending) {
		t.Errorf("second confirm: %v", err)
	}
	if _, active := p.Counts(); active != 1 {
		t.Errorf("%d active, want 1", active)
	}
}

// Review focus: pending credentials are in memory, and a restart drops them.
func TestPasskeys_PendingDoesNotSurviveAReload(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, path := newPasskeys(t, c)
	confirmOne(t, p, "kept")
	_ = p.StartPending(key("half-made", steve.XUID), issued)
	again, err := LoadPasskeys(path, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Active(KeyOf([]byte("kept")), steve.XUID) {
		t.Error("an active passkey did not survive a reload")
	}
	if pendingFor(again, "half-made") {
		t.Error("a pending credential survived a reload")
	}
	if _, _, err := again.IssueCode(steve.XUID); !errors.Is(err, ErrNothingPending) {
		t.Errorf("a code after a reload: %v", err)
	}
}

func TestPasskeys_AtMostFiveAndNoDuplicates(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	for i := range MaxPerPlayer {
		confirmOne(t, p, "cred-"+string(rune('a'+i)))
	}
	if err := p.StartPending(key("cred-z", steve.XUID), issued); !errors.Is(err, ErrTooMany) {
		t.Errorf("a sixth: %v", err)
	}
	if err := p.StartPending(key("cred-a", "2535412345678902"), issued); !errors.Is(err, ErrDuplicate) {
		t.Errorf("a credential ID already held: %v", err)
	}
}

func TestPasskeys_CounterRules(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	confirmOne(t, p, "synced")
	if _, err := p.Use([]byte("synced"), 0, false, false); err != nil {
		t.Errorf("both counters zero: %v", err)
	}
	if _, err := p.Use([]byte("synced"), 5, false, false); err != nil {
		t.Errorf("a higher count: %v", err)
	}
	for _, n := range []uint32{5, 4, 0} {
		if _, err := p.Use([]byte("synced"), n, false, false); !errors.Is(err, ErrCounter) {
			t.Errorf("count %d after 5: %v, want ErrCounter", n, err)
		}
	}
	if _, err := p.Use([]byte("synced"), 9, true, false); !errors.Is(err, ErrCounter) {
		t.Errorf("BE changed since registration: %v", err)
	}
}

func TestPasskeys_ConcurrentUsesOfOneCountPassOnce(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	confirmOne(t, p, "key")
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Use([]byte("key"), 7, false, false); err == nil {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if passed != 1 {
		t.Errorf("%d of 20 uses with the same count passed, want 1", passed)
	}
}

func TestPasskeys_RemoveAllIsOnePlayersOnly(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, path := newPasskeys(t, c)
	confirmOne(t, p, "steves")
	alex := key("alexs", "2535412345678902")
	_ = p.StartPending(alex, issued)
	code, _, _ := p.IssueCode(alex.XUID)
	_, _, _ = p.Confirm(alex.XUID, issued, alex.ID, code)
	_ = p.StartPending(key("steves-pending", steve.XUID), issued)

	if gone, err := p.RemoveAll(steve.XUID); err != nil || len(gone) != 2 {
		t.Errorf("removed %d of Steve's, want the active and the pending one", len(gone))
	}
	if !p.Active(KeyOf(alex.ID), alex.XUID) {
		t.Error("another player's passkey was removed")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), steve.XUID) {
		t.Error("the removal is not on disk")
	}
}

// Known answers: SHA-256 of "abc" begins ba7816bf8f01cfea414140de5dae2223.
func TestPasskeys_NamesAreTheSpecsBytes(t *testing.T) {
	id := []byte("abc")
	if got := Fingerprint(id); got != "BA78-16BF" {
		t.Errorf("Fingerprint = %q, want BA78-16BF", got)
	}
	if got := LogName(id); got != "ba7816bf" {
		t.Errorf("LogName = %q, want ba7816bf", got)
	}
	if got := KeyOf(id); got != "ungWv48Bz-pBQUDeXa4iIw" {
		t.Errorf("KeyOf = %q, want the base64url of the first 16 bytes", got)
	}
}

// A revocation that could not be written must say so: passkeys left in the
// file would come back at the next restart.
func TestPasskeys_RemoveAllReportsAFailedWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory is still writable")
	}
	c := &clock{time.Unix(1_790_000_000, 0)}
	dir := t.TempDir()
	p, err := LoadPasskeys(filepath.Join(dir, "passkeys.json"), c.now)
	if err != nil {
		t.Fatal(err)
	}
	confirmOne(t, p, "kept")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := p.RemoveAll(steve.XUID); err == nil {
		t.Error("RemoveAll reported success with the file left unwritten")
	}
	if !p.Active(KeyOf([]byte("kept")), steve.XUID) {
		t.Error("memory and disk disagree after a failed removal")
	}
}

func TestPasskeys_AConfirmWhoseWriteFailedCanBeRetried(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory is still writable")
	}
	c := &clock{time.Unix(1_790_000_000, 0)}
	dir := t.TempDir()
	p, err := LoadPasskeys(filepath.Join(dir, "passkeys.json"), c.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.StartPending(key("cred-1", steve.XUID), issued); err != nil {
		t.Fatal(err)
	}
	code, _, err := p.IssueCode(steve.XUID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); err == nil {
		t.Fatal("Confirm reported success with the file left unwritten")
	}
	if p.Active(KeyOf([]byte("cred-1")), steve.XUID) || !pendingFor(p, "cred-1") {
		t.Fatal("a failed write did not leave the credential pending")
	}
	if pending, active := p.Counts(); pending != 1 || active != 0 {
		t.Errorf("Counts = %d active, %d pending, want 0 and 1", active, pending)
	}
	_ = os.Chmod(dir, 0o700)
	if _, _, err := p.Confirm(steve.XUID, issued, []byte("cred-1"), code); err != nil {
		t.Fatalf("retrying the same code after the write recovered: %v", err)
	}
	if !p.Active(KeyOf([]byte("cred-1")), steve.XUID) || pendingFor(p, "cred-1") {
		t.Error("the retried confirm did not activate the credential")
	}
}

func TestPasskeys_TheUserHandleIsRandomAndKept(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	h := p.UserHandle(steve.XUID)
	if len(h) != 32 || strings.Contains(string(h), steve.XUID) {
		t.Fatalf("handle %x", h)
	}
	k := key("cred-1", steve.XUID)
	k.UserHandle = h
	_ = p.StartPending(k, issued)
	code, _, _ := p.IssueCode(steve.XUID)
	_, _, _ = p.Confirm(steve.XUID, issued, k.ID, code)
	if string(p.UserHandle(steve.XUID)) != string(h) {
		t.Error("a player's handle changed once they had a passkey")
	}
	if _, _, _, ok := p.Lookup(p.UserHandle("2535412345678902"), k.ID); ok {
		t.Error("a credential was found under another player's handle")
	}
	if xuid, _, pend, ok := p.Lookup(h, k.ID); !ok || pend || xuid != steve.XUID {
		t.Errorf("Lookup = %q pending %v ok %v", xuid, pend, ok)
	}
}

func TestPasskeys_TheStoreOwnsTheHandle(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	first := confirmOne(t, p, "first")
	if string(first.UserHandle) != string(p.UserHandle(steve.XUID)) || len(first.UserHandle) != 32 {
		t.Fatalf("a nil handle was not filled from the store: %x", first.UserHandle)
	}
	bad := key("second", steve.XUID)
	bad.UserHandle = []byte("some other handle")
	if err := p.StartPending(bad, issued); !errors.Is(err, ErrWrongHandle) {
		t.Fatalf("a different handle: %v, want ErrWrongHandle", err)
	}
	if _, _, _, ok := p.Lookup(first.UserHandle, first.ID); !ok {
		t.Error("the player's existing passkey is no longer found under their handle")
	}
	second := confirmOne(t, p, "second")
	if string(second.UserHandle) != string(first.UserHandle) {
		t.Error("a second passkey did not keep the stored handle")
	}
}

func TestPasskeys_AnEmptyHandleNeverMatches(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	confirmOne(t, p, "cred-1")
	for _, h := range [][]byte{nil, {}} {
		if _, _, _, ok := p.Lookup(h, []byte("cred-1")); ok {
			t.Error("Lookup matched an empty handle")
		}
	}
}

func TestPasskeys_RemoveReportsWhyItFailed(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	p, _ := newPasskeys(t, c)
	confirmOne(t, p, "kept")
	k := KeyOf([]byte("kept"))
	if _, err := p.Remove(steve.XUID, "nope"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := p.Remove("2535412345678902", k); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("another player's key: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory is still writable")
	}
	dir := filepath.Dir(p.path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := p.Remove(steve.XUID, k); err == nil || errors.Is(err, ErrUnknownKey) {
		t.Errorf("a failed write: %v, want the write's error", err)
	}
	if !p.Active(k, steve.XUID) {
		t.Error("memory and disk disagree after a failed removal")
	}
	_ = os.Chmod(dir, 0o700)
	if _, err := p.Remove(steve.XUID, k); err != nil {
		t.Errorf("remove once writable: %v", err)
	}
}
