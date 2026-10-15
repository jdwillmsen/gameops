package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

var b64url = base64.RawURLEncoding

type device struct {
	t     *testing.T
	priv  *ecdsa.PrivateKey
	id    []byte
	count uint32
}

func newDevice(t *testing.T) *device {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &device{t: t, priv: priv, id: id}
}

func clientData(typ, challenge string) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": "https://map.example", "crossOrigin": false})
	return raw
}

func (d *device) create(challenge string) string {
	x, y := d.priv.PublicKey.X.FillBytes(make([]byte, 32)), d.priv.PublicKey.Y.FillBytes(make([]byte, 32))
	cose, _ := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	h := sha256.Sum256([]byte("map.example"))
	ad := append(append(h[:], 0x45), 0, 0, 0, 0)
	ad = append(ad, make([]byte, 16)...)
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(d.id)))
	ad = append(append(ad, d.id...), cose...)
	att, _ := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad})
	body, _ := json.Marshal(map[string]any{"id": b64url.EncodeToString(d.id), "rawId": b64url.EncodeToString(d.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64url.EncodeToString(clientData("webauthn.create", challenge)), "attestationObject": b64url.EncodeToString(att)}})
	return string(body)
}

func (d *device) get(challenge string, handle []byte) string {
	d.count++
	h := sha256.Sum256([]byte("map.example"))
	ad := binary.BigEndian.AppendUint32(append(h[:], 0x05), d.count)
	cdj := clientData("webauthn.get", challenge)
	ch := sha256.Sum256(cdj)
	digest := sha256.Sum256(append(append([]byte{}, ad...), ch[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, d.priv, digest[:])
	body, _ := json.Marshal(map[string]any{"id": b64url.EncodeToString(d.id), "rawId": b64url.EncodeToString(d.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64url.EncodeToString(cdj), "authenticatorData": b64url.EncodeToString(ad), "signature": b64url.EncodeToString(sig), "userHandle": b64url.EncodeToString(handle)}})
	return string(body)
}

func challengeIn(t *testing.T, body []byte) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(body, &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("no challenge in %s", body)
	}
	return o.PublicKey.Challenge
}

// addPasskey runs the whole of D3's adding, the agent's part included, as
// the operator whose game-code session is given. It returns the code and
// the registration challenge, which a test may look for in the log.
func addPasskey(t *testing.T, s *Server, as *http.Cookie, d *device) (code, challenge string) {
	t.Helper()
	h, internal := s.Handler(), s.InternalHandler()
	opts := do(h, "POST", "/auth/passkey/register/options", `{"label":"Phone"}`, []*http.Cookie{as})
	if opts.Code != http.StatusOK {
		t.Fatalf("register options = %d %s", opts.Code, opts.Body)
	}
	challenge = challengeIn(t, opts.Body.Bytes())
	fin := do(h, "POST", "/auth/passkey/register/finish", d.create(challenge), []*http.Cookie{as})
	if fin.Code != http.StatusOK || !strings.Contains(fin.Body.String(), auth.Fingerprint(d.id)) {
		t.Fatalf("register finish = %d %s", fin.Code, fin.Body)
	}
	asked := do(internal, "POST", "/internal/v1/passkey-codes", `{"xuid":"`+steve.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
	var got struct{ Code, Fingerprint string }
	_ = json.Unmarshal(asked.Body.Bytes(), &got)
	if asked.Code != http.StatusOK || got.Fingerprint != auth.Fingerprint(d.id) {
		t.Fatalf("passkey code = %d %s", asked.Code, asked.Body)
	}
	confirm := do(h, "POST", "/auth/passkey/register/confirm", `{"credentialId":"`+b64url.EncodeToString(d.id)+`","code":"`+got.Code+`"}`, []*http.Cookie{as})
	if confirm.Code != http.StatusNoContent {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body)
	}
	return got.Code, challenge
}

func passkeyLogin(t *testing.T, s *Server, d *device) *httptest.ResponseRecorder {
	t.Helper()
	h := s.Handler()
	opts := do(h, "POST", "/auth/passkey/options", "", nil)
	jar := cookie(opts, auth.PasskeyChallengeCookie)
	return do(h, "POST", "/auth/passkey/finish", d.get(challengeIn(t, opts.Body.Bytes()), s.Passkeys.UserHandle(steve.XUID)), []*http.Cookie{jar})
}

func TestPasskey_AddThenLogInWithoutTheGame(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	var log bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&log, nil))
	confirmed := testutil.ToFloat64(metricPasskeysRegistered.WithLabelValues("confirmed"))

	d := newDevice(t)
	as := session(s, steve)
	code, challenge := addPasskey(t, s, as, d)
	if testutil.ToFloat64(metricPasskeysRegistered.WithLabelValues("confirmed")) != confirmed+1 {
		t.Error("a confirmation was not counted")
	}
	rec := passkeyLogin(t, s, d)
	sess := cookie(rec, "__Host-mcmap_session")
	if rec.Code != http.StatusOK || sess == nil || sess.MaxAge != int((12*time.Hour).Seconds()) {
		t.Fatalf("passkey login = %d %s, cookie %+v", rec.Code, rec.Body, sess)
	}
	if me := do(s.Handler(), "GET", "/api/me", "", []*http.Cookie{sess}); !strings.Contains(me.Body.String(), `"admin":true`) {
		t.Errorf("/api/me after a passkey login = %s", me.Body)
	}
	for _, never := range []string{steve.Gamertag, "Phone", b64url.EncodeToString(d.id), code, challenge, as.Value, sess.Value} {
		if strings.Contains(log.String(), never) {
			t.Errorf("the log holds %q:\n%s", never, log.String())
		}
	}
	for _, line := range []string{"map passkey pending", "map passkey code issued", "map passkey registered", `"method":"passkey"`} {
		if !strings.Contains(log.String(), line) {
			t.Errorf("the log lacks %s", line)
		}
	}
}

func TestPasskey_ANonOperatorWithAPasskeyIsRefusedAfterTheSignature(t *testing.T) {
	ops := fakeOperators{steve.XUID: true}
	s := withAdmin(t, ops)
	withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	delete(ops, steve.XUID)
	before := testutil.ToFloat64(metricAdminLogins.WithLabelValues("not_operator"))
	if rec := passkeyLogin(t, s, d); rec.Code != http.StatusUnauthorized || cookie(rec, "__Host-mcmap_session") != nil {
		t.Errorf("a demoted holder's login = %d", rec.Code)
	}
	if testutil.ToFloat64(metricAdminLogins.WithLabelValues("not_operator")) != before+1 {
		t.Error("not counted as not_operator")
	}
}

func TestPasskey_RegistrationStatuses(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	as := session(s, steve)
	h := s.Handler()
	if rec := do(h, "POST", "/auth/passkey/register/options", `{"label":"Café"}`, []*http.Cookie{as}); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad label = %d", rec.Code)
	}
	opts := do(h, "POST", "/auth/passkey/register/options", `{"label":"Phone"}`, []*http.Cookie{as})
	d := newDevice(t)
	_ = do(h, "POST", "/auth/passkey/register/finish", d.create(challengeIn(t, opts.Body.Bytes())), []*http.Cookie{as})
	if rec := do(h, "POST", "/auth/passkey/register/options", `{"label":"Laptop"}`, []*http.Cookie{as}); rec.Code != http.StatusConflict {
		t.Errorf("a second while one is pending = %d", rec.Code)
	}
	for i, want := range []int{http.StatusBadRequest, http.StatusBadRequest, http.StatusGone} {
		rec := do(h, "POST", "/auth/passkey/register/confirm", `{"credentialId":"`+b64url.EncodeToString(d.id)+`","code":"ZZZZZZ"}`, []*http.Cookie{as})
		if rec.Code != want {
			t.Errorf("wrong code %d = %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "POST", "/auth/passkey/register/confirm", `{"credentialId":"`+b64url.EncodeToString(d.id)+`","code":"ABC234"}`, []*http.Cookie{as}); rec.Code != http.StatusNotFound {
		t.Errorf("after abandonment = %d", rec.Code)
	}
	if rec := do(h, "POST", "/auth/passkey/register/cancel", "", []*http.Cookie{as}); rec.Code != http.StatusNoContent {
		t.Errorf("cancel = %d", rec.Code)
	}
}

// Review focus: garbage on the public finish route is a counted
// bad_assertion, read no further than the cap.
func TestPasskeyLogin_GarbageIsABadAssertionNotAFault(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	h := s.Handler()
	before := testutil.ToFloat64(metricAdminLogins.WithLabelValues("bad_assertion"))
	for name, body := range map[string]string{
		"not json":   "{{{",
		"bad base64": `{"id":"!!","rawId":"!!","type":"public-key","response":{}}`,
		"a megabyte": `{"id":"` + strings.Repeat("A", 1<<20) + `"}`,
		"empty":      "",
	} {
		opts := do(h, "POST", "/auth/passkey/options", "", nil)
		rec := do(h, "POST", "/auth/passkey/finish", body, []*http.Cookie{cookie(opts, auth.PasskeyChallengeCookie)})
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", name, rec.Code)
		}
	}
	if got := testutil.ToFloat64(metricAdminLogins.WithLabelValues("bad_assertion")) - before; got != 4 {
		t.Errorf("%v counted as bad_assertion, want 4", got)
	}
}

// A flood of refusals anyone could send is a count: five lines at once,
// then one a minute.
func TestPasskeyLogin_RefusalsAreLoggedWithinABudget(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	var log bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&log, nil))
	now := time.Unix(1_790_000_000, 0)
	s.clock = func() time.Time { return now }
	h := s.Handler()
	before := testutil.ToFloat64(metricAdminLogins.WithLabelValues("bad_assertion"))
	// An empty body is refused before anything else is read, as bad_assertion.
	for range 20 {
		_ = do(h, "POST", "/auth/passkey/finish", "", nil)
	}
	if got := strings.Count(log.String(), "map passkey login refused"); got != 5 {
		t.Errorf("%d lines for 20 refusals at once, want 5", got)
	}
	if got := testutil.ToFloat64(metricAdminLogins.WithLabelValues("bad_assertion")) - before; got != 20 {
		t.Errorf("%v refusals counted, want 20", got)
	}
	now = now.Add(time.Minute)
	_ = do(h, "POST", "/auth/passkey/finish", "", nil)
	if got := strings.Count(log.String(), "map passkey login refused"); got != 6 {
		t.Errorf("a minute later, %d lines, want 6", got)
	}
}

func TestPasskeyCode_OnlyTheAgentAndOnlyWhilePending(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	internal := s.InternalHandler()
	body := `{"xuid":"` + steve.XUID + `"}`
	if rec := do(internal, "POST", "/internal/v1/passkey-codes", body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without the token = %d", rec.Code)
	}
	if rec := do(internal, "POST", "/internal/v1/passkey-codes", body, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusNotFound {
		t.Errorf("nothing pending = %d", rec.Code)
	}
	if rec := do(internal, "POST", "/internal/v1/passkey-codes", `{"xuid":"<server>"}`, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusBadRequest {
		t.Errorf("not an XUID = %d", rec.Code)
	}
}

// A store that cannot be written keeps the count the login would have
// moved, so nobody is let in on it.
func TestPasskeyLogin_AFailedStoreWriteIssuesNoSession(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	_, _, path := withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if f, err := os.CreateTemp(dir, "probe"); err == nil {
		_ = f.Close()
		t.Skip("the directory is writable even when made read-only")
	}
	rec := passkeyLogin(t, s, d)
	if rec.Code != http.StatusInternalServerError || cookie(rec, "__Host-mcmap_session") != nil {
		t.Errorf("a login whose count could not be stored = %d, session cookie %v", rec.Code, cookie(rec, "__Host-mcmap_session"))
	}
}

func TestPasskeys_APlayerListsAndRemovesTheirOwnOnly(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	login := cookie(passkeyLogin(t, s, d), "__Host-mcmap_session")
	h := s.Handler()

	if rec := do(h, "GET", "/api/passkeys", "", []*http.Cookie{session(s, alex)}); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("another player's list = %s", rec.Body)
	}
	key := auth.KeyOf(d.id)
	if rec := do(h, "DELETE", "/api/passkeys/"+key, "", []*http.Cookie{session(s, alex)}); rec.Code != http.StatusNotFound {
		t.Errorf("another player removing it = %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/passkeys", "", []*http.Cookie{session(s, steve)}); !strings.Contains(rec.Body.String(), key) || !strings.Contains(rec.Body.String(), "Phone") {
		t.Errorf("own list = %s", rec.Body)
	}
	// Removing it ends the session made with it, on the next request.
	if rec := do(h, "DELETE", "/api/passkeys/"+key, "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/me", "", []*http.Cookie{login}); rec.Code != http.StatusUnauthorized {
		t.Errorf("the removed passkey's session = %d", rec.Code)
	}
}

// !map logout and Log out everywhere end sessions and delete passkeys.
func TestRevocation_DeletesThatPlayersPasskeys(t *testing.T) {
	for _, via := range []string{"agent", "page"} {
		t.Run(via, func(t *testing.T) {
			s := withAdmin(t, fakeOperators{steve.XUID: true})
			withPasskeys(t, s)
			d := newDevice(t)
			addPasskey(t, s, session(s, steve), d)
			switch via {
			case "agent":
				rec := do(s.InternalHandler(), "POST", "/internal/v1/revocations", `{"xuid":"`+steve.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
				if rec.Code != http.StatusNoContent {
					t.Fatalf("revoke = %d", rec.Code)
				}
			case "page":
				rec := do(s.Handler(), "POST", "/api/logout-all", "", []*http.Cookie{session(s, steve)})
				if rec.Code != http.StatusNoContent {
					t.Fatalf("logout-all = %d", rec.Code)
				}
			}
			if _, active := s.Passkeys.Counts(); active != 0 {
				t.Errorf("%d passkeys left after revocation", active)
			}
			if rec := passkeyLogin(t, s, d); rec.Code != http.StatusUnauthorized {
				t.Errorf("logging in with a revoked passkey = %d", rec.Code)
			}
		})
	}
}

// A store that cannot write must never be reported as having removed.
func TestPasskeys_AFailedWriteIsNeverReportedAsRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only directory")
	}
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	_, _, path := withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	h := s.Handler()
	if rec := do(h, "DELETE", "/api/passkeys/"+auth.KeyOf(d.id), "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusInternalServerError {
		t.Errorf("remove with an unwritable store = %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/logout-all", "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusInternalServerError {
		t.Errorf("logout-all with an unwritable store = %d", rec.Code)
	}
	if _, active := s.Passkeys.Counts(); active != 1 {
		t.Errorf("%d passkeys after failed removals, want 1", active)
	}
}

// A game login refreshes the gamertag a passkey session will carry.
func TestPasskeys_AGameLoginRefreshesTheGamertag(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	d := newDevice(t)
	addPasskey(t, s, session(s, steve), d)
	public, internal := s.Handler(), s.InternalHandler()
	start := do(public, "POST", "/auth/start", "", nil)
	var started struct{ Code string }
	_ = json.Unmarshal(start.Body.Bytes(), &started)
	_ = do(internal, "POST", "/internal/v1/claims", `{"code":"`+started.Code+`","xuid":"`+steve.XUID+`","gamertag":"Steve Renamed"}`, nil, "Authorization", "Bearer "+internalToken)
	_ = do(public, "GET", "/auth/status", "", []*http.Cookie{cookie(start, "__Host-mcmap_pending")})
	rec := passkeyLogin(t, s, d)
	if !strings.Contains(rec.Body.String(), "Steve Renamed") {
		t.Errorf("passkey login after a rename = %s", rec.Body)
	}
}

// The gauge the alert backstop reads comes back from disk at start.
func TestPasskeys_TheGaugeIsReadBackAtStart(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	_, _, path := withPasskeys(t, s)
	addPasskey(t, s, session(s, steve), newDevice(t))
	CountPasskeys(0, 0)
	again, err := auth.LoadPasskeys(path, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	StartPasskeyMetrics(again)
	if got := testutil.ToFloat64(metricPasskeys.WithLabelValues("active")); got != 1 {
		t.Errorf("active gauge after a restart = %v, want 1", got)
	}
}

// A store that cannot write is the map's fault: it answers busy, and is
// written down as an error an operator can find, naming no secret.
func TestPasskeys_AFailedConfirmWriteIsLoggedAsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only directory")
	}
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	_, _, path := withPasskeys(t, s)
	var log bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&log, nil))
	h, as, d := s.Handler(), session(s, steve), newDevice(t)
	opts := do(h, "POST", "/auth/passkey/register/options", `{"label":"Phone"}`, []*http.Cookie{as})
	challenge := challengeIn(t, opts.Body.Bytes())
	if fin := do(h, "POST", "/auth/passkey/register/finish", d.create(challenge), []*http.Cookie{as}); fin.Code != http.StatusOK {
		t.Fatalf("register finish = %d %s", fin.Code, fin.Body)
	}
	asked := do(s.InternalHandler(), "POST", "/internal/v1/passkey-codes", `{"xuid":"`+steve.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
	var got struct{ Code string }
	_ = json.Unmarshal(asked.Body.Bytes(), &got)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	log.Reset()
	rec := do(h, "POST", "/auth/passkey/register/confirm", `{"credentialId":"`+b64url.EncodeToString(d.id)+`","code":"`+got.Code+`"}`, []*http.Cookie{as})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("confirm with an unwritable store = %d", rec.Code)
	}
	line := log.String()
	if !strings.Contains(line, `"level":"ERROR"`) || !strings.Contains(line, `"error_class":"write"`) {
		t.Errorf("confirm with an unwritable store logged %q, want an error with its class", line)
	}
	for _, secret := range []string{got.Code, challenge, steve.Gamertag, "Phone", b64url.EncodeToString(d.id)} {
		if strings.Contains(line, secret) {
			t.Errorf("the log line carries %q: %s", secret, line)
		}
	}
}

// Only what is wrong with the credential is the caller's refusal; anything
// else is the map's own fault and must not be counted or answered as one.
func TestRegisterFinish_OnlyTheCredentialsFaultIsARefusal(t *testing.T) {
	for err, want := range map[error]bool{
		auth.ErrRegistrationRefused:                 true,
		auth.ErrDuplicate:                           true,
		auth.ErrWrongHandle:                         true,
		errors.New("open passkeys.json: read-only"): false,
		fmt.Errorf("save: %w", os.ErrPermission):    false,
	} {
		if got := refusedRegistration(err); got != want {
			t.Errorf("refusedRegistration(%v) = %v, want %v", err, got, want)
		}
	}
}

// A body that begins with a byte-order mark is refused, never stripped
// and read: what the map takes is JSON exactly as the page sends it.
func TestPasskeys_ABodyWithALeadingBOMIsRefused(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	h, as := s.Handler(), session(s, steve)
	const bom = "\xef\xbb\xbf"
	if rec := do(h, "POST", "/auth/passkey/register/options", bom+`{"label":"Phone"}`, []*http.Cookie{as}); rec.Code != http.StatusBadRequest {
		t.Errorf("register options with a BOM = %d, want 400", rec.Code)
	}
	opts := do(h, "POST", "/auth/passkey/register/options", `{"label":"Phone"}`, []*http.Cookie{as})
	d := newDevice(t)
	if rec := do(h, "POST", "/auth/passkey/register/finish", bom+d.create(challengeIn(t, opts.Body.Bytes())), []*http.Cookie{as}); rec.Code != http.StatusBadRequest {
		t.Errorf("register finish with a BOM = %d, want 400", rec.Code)
	}
}

// Before finish there is only a challenge, and asking again replaces it
// rather than being refused. Only once a passkey is pending is a second
// registration refused.
func TestPasskeys_ASecondStartReplacesTheChallengeUntilFinish(t *testing.T) {
	s := withAdmin(t, fakeOperators{steve.XUID: true})
	withPasskeys(t, s)
	h, as := s.Handler(), session(s, steve)
	first := challengeIn(t, do(h, "POST", "/auth/passkey/register/options", `{"label":"Phone"}`, []*http.Cookie{as}).Body.Bytes())
	again := do(h, "POST", "/auth/passkey/register/options", `{"label":"Laptop"}`, []*http.Cookie{as})
	if again.Code != http.StatusOK {
		t.Fatalf("a second start before finish = %d, want 200", again.Code)
	}
	second := challengeIn(t, again.Body.Bytes())
	if first == second {
		t.Fatal("the second start reused the first challenge")
	}
	if rec := do(h, "POST", "/auth/passkey/register/finish", newDevice(t).create(second), []*http.Cookie{as}); rec.Code != http.StatusOK {
		t.Errorf("finishing the newer challenge = %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/auth/passkey/register/options", `{"label":"Spare"}`, []*http.Cookie{as}); rec.Code != http.StatusConflict {
		t.Errorf("a start once a passkey is pending = %d, want 409", rec.Code)
	}
}
