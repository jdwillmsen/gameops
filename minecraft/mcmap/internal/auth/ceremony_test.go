package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	origin = "https://map.example"
	rpID   = "map.example"
)

func ceremonies(t *testing.T, c *clock) (*Ceremonies, *Passkeys) {
	t.Helper()
	keys, _ := newPasskeys(t, c)
	cer, err := NewCeremonies(origin, rpID, []byte(strings.Repeat("k", 32)), keys, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return cer, keys
}

func challengeOf(t *testing.T, options any) string {
	t.Helper()
	raw, _ := json.Marshal(options)
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("options without a challenge: %s", raw)
	}
	return o.PublicKey.Challenge
}

// register runs a whole registration and confirmation; it is the fixture
// every login test starts from.
func register(t *testing.T, cer *Ceremonies, keys *Passkeys, k *softKey) {
	t.Helper()
	opts, err := cer.BeginRegistration(steve.XUID, issued, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cer.FinishRegistration(steve, issued, k.create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})); err != nil {
		t.Fatal(err)
	}
	code, _, _ := keys.IssueCode(steve.XUID)
	if _, _, err := keys.Confirm(steve.XUID, issued, k.id, code); err != nil {
		t.Fatal(err)
	}
}

func TestRegistration_OptionsAreTheSpecsAndNameNobody(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, _ := ceremonies(t, c)
	opts, err := cer.BeginRegistration(steve.XUID, issued, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(opts)
	for _, want := range []string{`"residentKey":"required"`, `"userVerification":"required"`, `"attestation":"none"`, `"alg":-7`, `"alg":-257`, `"name":"fwb-map"`, `"displayName":"FWB map admin"`, `"id":"map.example"`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("options lack %s: %s", want, raw)
		}
	}
	for _, never := range []string{steve.XUID, steve.Gamertag} {
		if bytes.Contains(raw, []byte(never)) {
			t.Errorf("options name the player (%q): %s", never, raw)
		}
	}
}

// Review focus: a label is judged before anything is made.
func TestRegistration_LabelIsCheckedBeforeAnythingIsMade(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	for _, label := range []string{"", "   ", "Café", "Phone 📱", strings.Repeat("a", 33), "<b>x</b>", "tab\there"} {
		if _, err := cer.BeginRegistration(steve.XUID, issued, label); !errors.Is(err, ErrLabel) {
			t.Errorf("label %q: %v, want ErrLabel", label, err)
		}
	}
	if keys.HasPending(steve.XUID) {
		t.Error("a refused label left something pending")
	}
	for _, label := range []string{"Phone", "Work laptop 2", "Key-A"} {
		if _, err := cer.BeginRegistration(steve.XUID, issued, label); err != nil {
			t.Errorf("label %q: %v", label, err)
		}
	}
}

func TestRegistration_EveryCheckOfTheSpec(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	for name, tc := range map[string]struct {
		mutate  func(*ceremony)
		session func() (Identity, int64)
		key     func(*softKey)
	}{
		"wrong type":              {mutate: func(c *ceremony) { c.typ = "webauthn.get" }},
		"wrong origin":            {mutate: func(c *ceremony) { c.origin = "https://elsewhere.example" }},
		"origin with its port":    {mutate: func(c *ceremony) { c.origin = origin + ":443" }},
		"origin in capitals":      {mutate: func(c *ceremony) { c.origin = "https://MAP.example" }},
		"cross origin":            {mutate: func(c *ceremony) { c.clientData = func(m map[string]any) { m["crossOrigin"] = true } }},
		"top origin present":      {mutate: func(c *ceremony) { c.clientData = func(m map[string]any) { m["topOrigin"] = origin } }},
		"wrong rp id":             {mutate: func(c *ceremony) { c.rpID = "elsewhere.example" }},
		"wrong challenge":         {mutate: func(c *ceremony) { c.challenge = b64.EncodeToString(bytes.Repeat([]byte{7}, 32)) }},
		"no user presence":        {mutate: func(c *ceremony) { c.flags = flagUV }},
		"no verification":         {mutate: func(c *ceremony) { c.flags = flagUP }},
		"backed up, not eligible": {key: func(k *softKey) { k.bs = true }},
		"another alg":             {mutate: func(c *ceremony) { c.alg = -8 }},
		"packed attestation":      {mutate: func(c *ceremony) { c.format = "packed" }},
		"id over 1023 bytes":      {mutate: func(c *ceremony) { c.id = bytes.Repeat([]byte{1}, 1024) }},
		"another session":         {session: func() (Identity, int64) { return steve, issued + 1 }},
		"another player":          {session: func() (Identity, int64) { return Identity{XUID: "2535412345678902", Gamertag: "Alex"}, issued }},
	} {
		t.Run(name, func(t *testing.T) {
			cer, keys := ceremonies(t, c)
			opts, err := cer.BeginRegistration(steve.XUID, issued, "Phone")
			if err != nil {
				t.Fatal(err)
			}
			k := newSoftKey(t)
			if tc.key != nil {
				tc.key(k)
			}
			cr := ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}
			if tc.mutate != nil {
				tc.mutate(&cr)
			}
			id, ms := steve, issued
			if tc.session != nil {
				id, ms = tc.session()
			}
			if _, err := cer.FinishRegistration(id, ms, k.create(cr)); err == nil {
				t.Fatal("accepted")
			}
			if keys.HasPending(steve.XUID) {
				t.Error("a refused registration left a pending credential")
			}
		})
	}
}

func TestRegistration_ChallengeIsSingleUseAndShortLived(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	opts, _ := cer.BeginRegistration(steve.XUID, issued, "Phone")
	k := newSoftKey(t)
	body := k.create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})
	if _, err := cer.FinishRegistration(steve, issued, body); err != nil {
		t.Fatal(err)
	}
	keys.Cancel(steve.XUID)
	if _, err := cer.FinishRegistration(steve, issued, body); !errors.Is(err, ErrRegistrationExpired) {
		t.Errorf("a reused challenge: %v", err)
	}
	opts, _ = cer.BeginRegistration(steve.XUID, issued, "Phone")
	c.t = c.t.Add(PendingFor)
	if _, err := cer.FinishRegistration(steve, issued, newSoftKey(t).create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})); !errors.Is(err, ErrRegistrationExpired) {
		t.Errorf("an expired challenge: %v", err)
	}
}

func TestRegistration_RefusedWhileOneIsPending(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, _ := ceremonies(t, c)
	opts, _ := cer.BeginRegistration(steve.XUID, issued, "Phone")
	_, _ = cer.FinishRegistration(steve, issued, newSoftKey(t).create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}))
	if _, err := cer.BeginRegistration(steve.XUID, issued, "Laptop"); !errors.Is(err, ErrPendingExists) {
		t.Errorf("a second registration while one is pending: %v", err)
	}
}

// loginWith runs BeginLogin and returns the challenge and its cookie.
func loginWith(t *testing.T, cer *Ceremonies) (string, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	opts, err := cer.BeginLogin(rec)
	if err != nil {
		t.Fatal(err)
	}
	var jar *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == PasskeyChallengeCookie {
			jar = ck
		}
	}
	if jar == nil || !jar.HttpOnly || !jar.Secure || jar.SameSite != http.SameSiteStrictMode {
		t.Fatalf("challenge cookie = %+v", jar)
	}
	raw, _ := json.Marshal(opts)
	if bytes.Contains(raw, []byte("allowCredentials")) {
		t.Errorf("a discoverable login lists credentials: %s", raw)
	}
	return challengeOf(t, opts), jar
}

func finish(cer *Ceremonies, jar *http.Cookie, body []byte) (Passkey, error) {
	req := httptest.NewRequest("POST", "/auth/passkey/finish", bytes.NewReader(body))
	if jar != nil {
		req.AddCookie(jar)
	}
	return cer.FinishLogin(httptest.NewRecorder(), req, body, func(string) bool { return true })
}

func resultOf(err error) string {
	var ce *CeremonyError
	if errors.As(err, &ce) {
		return ce.Result
	}
	return "not a ceremony error: " + errorString(err)
}

func errorString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func TestLogin_AGoodAssertionLogsIn(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	ch, jar := loginWith(t, cer)
	got, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID)))
	if err != nil || got.XUID != steve.XUID || got.SignCount != k.count {
		t.Fatalf("FinishLogin = %+v, %v", got, err)
	}
	if stored := keys.Existing(steve.XUID)[0]; stored.SignCount != k.count || stored.LastUsed.IsZero() {
		t.Errorf("the higher count was not stored: %+v", stored)
	}
}

func TestLogin_EveryRefusal(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	other := []byte(strings.Repeat("o", 32))
	for name, tc := range map[string]struct {
		mutate func(*ceremony)
		handle func(*Passkeys) []byte
		want   string
	}{
		"wrong type":           {mutate: func(c *ceremony) { c.typ = "webauthn.create" }, want: ResultBadAssertion},
		"wrong origin":         {mutate: func(c *ceremony) { c.origin = "https://elsewhere.example" }, want: ResultBadAssertion},
		"origin with its port": {mutate: func(c *ceremony) { c.origin = origin + ":443" }, want: ResultBadAssertion},
		"origin in capitals":   {mutate: func(c *ceremony) { c.origin = "https://MAP.example" }, want: ResultBadAssertion},
		"cross origin":         {mutate: func(c *ceremony) { c.clientData = func(m map[string]any) { m["crossOrigin"] = true } }, want: ResultBadAssertion},
		"top origin":           {mutate: func(c *ceremony) { c.clientData = func(m map[string]any) { m["topOrigin"] = origin } }, want: ResultBadAssertion},
		"wrong rp id":          {mutate: func(c *ceremony) { c.rpID = "elsewhere.example" }, want: ResultBadAssertion},
		"no user presence":     {mutate: func(c *ceremony) { c.flags = flagUV }, want: ResultBadAssertion},
		"no verification":      {mutate: func(c *ceremony) { c.flags = flagUP }, want: ResultBadAssertion},
		"wrong challenge":      {mutate: func(c *ceremony) { c.challenge = b64.EncodeToString(bytes.Repeat([]byte{9}, 32)) }, want: ResultBadAssertion},
		"no user handle":       {handle: func(*Passkeys) []byte { return nil }, want: ResultBadAssertion},
		"another's handle":     {handle: func(p *Passkeys) []byte { return p.UserHandle("2535412345678902") }, want: ResultUnknownCredential},
		"an unknown handle":    {handle: func(*Passkeys) []byte { return other }, want: ResultUnknownCredential},
	} {
		t.Run(name, func(t *testing.T) {
			cer, keys := ceremonies(t, c)
			k := newSoftKey(t)
			register(t, cer, keys, k)
			ch, jar := loginWith(t, cer)
			cr := ceremony{rpID: rpID, origin: origin, challenge: ch}
			if tc.mutate != nil {
				tc.mutate(&cr)
			}
			handle := keys.UserHandle(steve.XUID)
			if tc.handle != nil {
				handle = tc.handle(keys)
			}
			if _, err := finish(cer, jar, k.get(cr, handle)); resultOf(err) != tc.want {
				t.Errorf("result %s, want %s", resultOf(err), tc.want)
			}
		})
	}
}

func TestLogin_ChallengeCookie(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	handle := keys.UserHandle(steve.XUID)

	ch, jar := loginWith(t, cer)
	if _, err := finish(cer, nil, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, handle)); resultOf(err) != ResultExpired {
		t.Errorf("no cookie: %s", resultOf(err))
	}
	forged := *jar
	tail := "AA"
	if strings.HasSuffix(forged.Value, tail) {
		tail = "BB"
	}
	forged.Value = forged.Value[:len(forged.Value)-2] + tail
	if _, err := finish(cer, &forged, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, handle)); resultOf(err) != ResultBadAssertion {
		t.Errorf("a cookie with a bad HMAC: %s", resultOf(err))
	}
	// A cookie signed under any key but the one derived with its own label.
	other, _ := NewCeremonies(origin, rpID, []byte(strings.Repeat("x", 32)), keys, c.now)
	_, foreign := loginWith(t, other)
	if _, err := finish(cer, foreign, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, handle)); resultOf(err) != ResultBadAssertion {
		t.Errorf("a cookie from another key: %s", resultOf(err))
	}
	c.t = c.t.Add(PendingFor)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, handle)); resultOf(err) != ResultExpired {
		t.Errorf("an expired challenge: %s", resultOf(err))
	}
}

// sealWith makes a challenge cookie the map's way under any key, so that a
// test can show only the key derived with the challenge label is accepted.
func sealWith(key, challenge []byte, expires time.Time) *http.Cookie {
	payload := binary.BigEndian.AppendUint64(append([]byte{}, challenge...), uint64(expires.Unix()))
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return &http.Cookie{Name: PasskeyChallengeCookie, Value: encoding.EncodeToString(mac.Sum(payload))}
}

func TestLogin_OnlyTheChallengeKeySealsAChallenge(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	sessionKey := []byte(strings.Repeat("k", 32))
	ch, _ := loginWith(t, cer)
	raw, _ := b64.DecodeString(ch)
	for name, key := range map[string][]byte{
		"the session key itself":  sessionKey,
		"the service session key": ServiceKey(sessionKey, strings.Repeat("s", 48)),
	} {
		jar := sealWith(key, raw, c.now().Add(time.Minute))
		if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); resultOf(err) != ResultBadAssertion {
			t.Errorf("a cookie sealed with %s: %s", name, resultOf(err))
		}
	}
	jar := sealWith(PasskeyChallengeKey(sessionKey), raw, c.now().Add(time.Minute))
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); err != nil {
		t.Errorf("a cookie sealed with the challenge key: %v", err)
	}
}

func TestRegistration_AnRS256KeyIsAccepted(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newRSASoftKey(t)
	register(t, cer, keys, k)
	ch, jar := loginWith(t, cer)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); err != nil {
		t.Errorf("logging in with an RS256 passkey: %s", resultOf(err))
	}
}

// Another session of the same player cannot use up the owner's challenge.
func TestRegistration_AnotherSessionDoesNotSpendTheChallenge(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, _ := ceremonies(t, c)
	opts, _ := cer.BeginRegistration(steve.XUID, issued, "Phone")
	k := newSoftKey(t)
	body := k.create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})
	if _, err := cer.FinishRegistration(steve, issued+1, body); !errors.Is(err, ErrRegistrationExpired) {
		t.Fatalf("another session: %v", err)
	}
	if _, err := cer.FinishRegistration(steve, issued, body); err != nil {
		t.Errorf("the owner's session after another tried: %v", err)
	}
}

// The same assertion sent twice at once gets exactly one session.
func TestLogin_AReplayedAssertionIsRefusedEvenAtTheSameMoment(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	ch, jar := loginWith(t, cer)
	body := k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))
	var wg sync.WaitGroup
	results := make([]string, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := finish(cer, jar, body)
			results[i] = resultOf(err)
		}()
	}
	wg.Wait()
	ok := 0
	for _, r := range results {
		switch r {
		case "not a ceremony error: nil":
			ok++
		case ResultReplayed:
		default:
			t.Errorf("unexpected result %s", r)
		}
	}
	if ok != 1 {
		t.Errorf("%d of 8 identical assertions logged in, want 1", ok)
	}
}

func TestLogin_PendingAndCounterAreJudgedAfterTheSignature(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	opts, _ := cer.BeginRegistration(steve.XUID, issued, "Phone")
	_, _ = cer.FinishRegistration(steve, issued, k.create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}))
	ch, jar := loginWith(t, cer)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); resultOf(err) != ResultPending {
		t.Errorf("a pending credential: %s", resultOf(err))
	}

	code, _, _ := keys.IssueCode(steve.XUID)
	_, _, _ = keys.Confirm(steve.XUID, issued, k.id, code)
	ch, jar = loginWith(t, cer)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); err != nil {
		t.Fatal(err)
	}
	k.count -= 2 // a clone, behind the original
	ch, jar = loginWith(t, cer)
	if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); resultOf(err) != ResultCounter {
		t.Errorf("a counter that went back: %s", resultOf(err))
	}
}

func TestLogin_SignatureAndBackupFlags(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	for name, spoil := range map[string]func(*softKey){
		"a bad signature":               func(k *softKey) { k.badSig = true },
		"BE changed since registration": func(k *softKey) { k.be = true },
		"BS without BE":                 func(k *softKey) { k.bs = true },
	} {
		t.Run(name, func(t *testing.T) {
			cer, keys := ceremonies(t, c)
			k := newSoftKey(t)
			register(t, cer, keys, k)
			spoil(k)
			ch, jar := loginWith(t, cer)
			if _, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID))); resultOf(err) != ResultBadAssertion {
				t.Errorf("result %s, want bad_assertion", resultOf(err))
			}
		})
	}
}

// A player refused after the signature leaves the stored count where it
// was: only a login that succeeds moves it.
func TestLogin_ARefusedPlayerMovesNothing(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	before := keys.Existing(steve.XUID)[0]
	ch, jar := loginWith(t, cer)
	req := httptest.NewRequest("POST", "/auth/passkey/finish", nil)
	req.AddCookie(jar)
	got, err := cer.FinishLogin(httptest.NewRecorder(), req, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID)), func(string) bool { return false })
	if resultOf(err) != ResultNotOperator || got.XUID != steve.XUID {
		t.Fatalf("result %s for %q, want not_operator for Steve", resultOf(err), got.XUID)
	}
	after := keys.Existing(steve.XUID)[0]
	if after.SignCount != before.SignCount || !after.LastUsed.Equal(before.LastUsed) {
		t.Errorf("a refused login moved the stored count from %d to %d", before.SignCount, after.SignCount)
	}
}

// Leaving out the operator check is not a way past it.
func TestLogin_NoOneIsAllowedWhenNothingIsAsked(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	ch, jar := loginWith(t, cer)
	req := httptest.NewRequest("POST", "/auth/passkey/finish", nil)
	req.AddCookie(jar)
	if _, err := cer.FinishLogin(httptest.NewRecorder(), req, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID)), nil); resultOf(err) != ResultNotOperator {
		t.Errorf("result %s with no operator check, want not_operator", resultOf(err))
	}
}

// A passkey removed while its login was being checked logs nobody in.
func TestLogin_APasskeyRemovedMidwayIsUnknown(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	ch, jar := loginWith(t, cer)
	req := httptest.NewRequest("POST", "/auth/passkey/finish", nil)
	req.AddCookie(jar)
	removeFirst := func(xuid string) bool {
		if _, err := keys.Remove(xuid, KeyOf(k.id)); err != nil {
			t.Fatal(err)
		}
		return true
	}
	if _, err := cer.FinishLogin(httptest.NewRecorder(), req, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID)), removeFirst); resultOf(err) != ResultUnknownCredential {
		t.Errorf("result %s, want unknown_credential", resultOf(err))
	}
}

// A synced passkey keeps no counter: both counts 0 are not judged.
func TestLogin_NoCounterOnEitherSideIsNotJudged(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	k.still = true
	register(t, cer, keys, k)
	for i := range 2 {
		ch, jar := loginWith(t, cer)
		got, err := finish(cer, jar, k.get(ceremony{rpID: rpID, origin: origin, challenge: ch}, keys.UserHandle(steve.XUID)))
		if err != nil || got.SignCount != 0 {
			t.Fatalf("login %d without a counter = %+v, %s", i+1, got, resultOf(err))
		}
	}
}

func TestRegistration_ACredentialAlreadyHeldIsRefused(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	cer, keys := ceremonies(t, c)
	k := newSoftKey(t)
	register(t, cer, keys, k)
	opts, err := cer.BeginRegistration(steve.XUID, issued, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cer.FinishRegistration(steve, issued, k.create(ceremony{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})); !errors.Is(err, ErrDuplicate) {
		t.Errorf("registering a held credential again: %v", err)
	}
	if keys.HasPending(steve.XUID) {
		t.Error("a refused registration left a pending credential")
	}
}

// The origin is compared exactly, so it has to be given the way a browser
// writes it.
func TestNewCeremonies_RefusesAnOriginNoBrowserSends(t *testing.T) {
	c := &clock{time.Unix(1_790_000_000, 0)}
	keys, _ := newPasskeys(t, c)
	for _, o := range []string{"https://Map.Example", "https://map.example/", "https://map.example:443", "http://map.example"} {
		if _, err := NewCeremonies(o, rpID, []byte(strings.Repeat("k", 32)), keys, c.now); err == nil {
			t.Errorf("origin %q accepted", o)
		}
	}
}
