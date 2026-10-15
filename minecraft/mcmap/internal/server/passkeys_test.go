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
