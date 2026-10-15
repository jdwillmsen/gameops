package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
)

const PasskeyChallengeCookie = cookiePrefix + "mcmap_passkey"

const (
	ResultBadAssertion      = "bad_assertion"
	ResultUnknownCredential = "unknown_credential"
	ResultExpired           = "expired"
	ResultPending           = "pending"
	ResultNotOperator       = "not_operator"
	ResultCounter           = "counter"
	ResultReplayed          = "replayed"
)

var (
	ErrLabel               = errors.New("a label is 1 to 32 letters, digits, spaces or hyphens")
	ErrRegistrationExpired = errors.New("that registration has expired or was already used; start again")
	ErrRegistrationRefused = errors.New("that passkey was not accepted")
)

type CeremonyError struct{ Result string }

func (e *CeremonyError) Error() string { return "passkey login refused: " + e.Result }

var labelShape = regexp.MustCompile(`^[A-Za-z0-9 -]{1,32}$`)

// maxRegistrations bounds the waiting registrations. Only an admin can
// start one, so this is a backstop and not the defence.
const maxRegistrations = 64

type registration struct {
	session     webauthn.SessionData
	issuedMilli int64
	label       string
	expires     time.Time
}

// Ceremonies runs WebAuthn for the map. The library checks the signatures,
// the CBOR and most of each ceremony; the checks the spec makes stricter
// than the library's defaults (the exact origin, no framing, attestation
// "none" only, the key's algorithm, the counter, the user handle) are made
// here, so that a library upgrade cannot loosen them.
type Ceremonies struct {
	wa      *webauthn.WebAuthn
	origin  string
	rpID    string
	keys    *Passkeys
	hmacKey []byte
	now     func() time.Time

	mu   sync.Mutex
	regs map[string]registration
	used map[[sha256.Size]byte]time.Time
}

func PasskeyChallengeKey(sessionKey []byte) []byte {
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte("mcmap passkey login challenge v1\x00"))
	return mac.Sum(nil)
}

func NewCeremonies(origin, rpID string, sessionKey []byte, keys *Passkeys, now func() time.Time) (*Ceremonies, error) {
	// The origin is compared byte for byte, where the library would fold
	// case and drop a default port; given any other way than the browser
	// writes it, no passkey could ever log in.
	if rpID == "" || rpID != strings.ToLower(rpID) || origin != "https://"+rpID {
		return nil, errors.New("a passkey origin is https:// and the RP ID, in lower case")
	}
	wa, err := webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "FWB map", RPOrigins: []string{origin}})
	if err != nil {
		return nil, err
	}
	return &Ceremonies{wa: wa, origin: origin, rpID: rpID, keys: keys, hmacKey: PasskeyChallengeKey(sessionKey), now: now,
		regs: map[string]registration{}, used: map[[sha256.Size]byte]time.Time{}}, nil
}

type waUser struct {
	handle []byte
	keys   []Passkey
}

func (u waUser) WebAuthnID() []byte          { return u.handle }
func (u waUser) WebAuthnName() string        { return "fwb-map" }
func (u waUser) WebAuthnDisplayName() string { return "FWB map admin" }
func (u waUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(u.keys))
	for _, k := range u.keys {
		out = append(out, webauthn.Credential{
			ID: k.ID, PublicKey: k.PublicKey,
			Authenticator: webauthn.Authenticator{SignCount: k.SignCount},
			Flags:         webauthn.CredentialFlags{UserPresent: true, UserVerified: true, BackupEligible: k.BackupEligible, BackupState: k.BackupState},
		})
	}
	return out
}

func (c *Ceremonies) BeginRegistration(xuid string, issuedMilli int64, label string) (*protocol.CredentialCreation, error) {
	if !labelShape.MatchString(label) || strings.TrimSpace(label) == "" {
		return nil, ErrLabel
	}
	if c.keys.HasPending(xuid) {
		return nil, ErrPendingExists
	}
	u := waUser{handle: c.keys.UserHandle(xuid), keys: c.keys.Existing(xuid)}
	var exclude []protocol.CredentialDescriptor
	for _, cred := range u.WebAuthnCredentials() {
		exclude = append(exclude, cred.Descriptor())
	}
	creation, session, err := c.wa.BeginRegistration(u,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithExclusions(exclude),
		webauthn.WithCredentialParameters([]protocol.CredentialParameter{
			{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgES256},
			{Type: protocol.PublicKeyCredentialType, Algorithm: webauthncose.AlgRS256},
		}),
	)
	if err != nil {
		return nil, err
	}
	// The library's expiry reads the wall clock; the map's own, below, is
	// the one that counts, and the tests' clock drives it.
	session.Expires = time.Time{}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for x, r := range c.regs {
		if !now.Before(r.expires) {
			delete(c.regs, x)
		}
	}
	if len(c.regs) >= maxRegistrations {
		return nil, ErrRegistrationRefused
	}
	c.regs[xuid] = registration{session: *session, issuedMilli: issuedMilli, label: label, expires: now.Add(PendingFor)}
	return creation, nil
}

func (c *Ceremonies) FinishRegistration(id Identity, issuedMilli int64, body []byte) (Passkey, error) {
	// Compared before it is spent, so that another session of the same
	// player cannot use up the challenge the owner's page is waiting on.
	c.mu.Lock()
	reg, ok := c.regs[id.XUID]
	ok = ok && reg.issuedMilli == issuedMilli
	if ok {
		delete(c.regs, id.XUID)
	}
	c.mu.Unlock()
	if !ok || !c.now().Before(reg.expires) {
		return Passkey{}, ErrRegistrationExpired
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return Passkey{}, ErrRegistrationRefused
	}
	cd := parsed.Response.CollectedClientData
	if cd.Type != protocol.CreateCeremony || cd.Origin != c.origin || cd.CrossOrigin || cd.TopOrigin != "" ||
		parsed.Response.AttestationObject.Format != "none" || len(parsed.RawID) > 1023 {
		return Passkey{}, ErrRegistrationRefused
	}
	u := waUser{handle: c.keys.UserHandle(id.XUID), keys: c.keys.Existing(id.XUID)}
	cred, err := c.wa.CreateCredential(u, reg.session, parsed)
	if err != nil || len(cred.ID) > 1023 || !allowedAlg(cred.PublicKey) {
		return Passkey{}, ErrRegistrationRefused
	}
	k := Passkey{
		ID: cred.ID, PublicKey: cred.PublicKey, UserHandle: u.handle, SignCount: cred.Authenticator.SignCount,
		BackupEligible: cred.Flags.BackupEligible, BackupState: cred.Flags.BackupState,
		XUID: id.XUID, Gamertag: id.Gamertag, Label: reg.label,
	}
	if err := c.keys.StartPending(k, issuedMilli); err != nil {
		return Passkey{}, err
	}
	return k, nil
}

func allowedAlg(publicKey []byte) bool {
	var pk webauthncose.PublicKeyData
	if err := webauthncbor.Unmarshal(publicKey, &pk); err != nil {
		return false
	}
	alg := webauthncose.COSEAlgorithmIdentifier(pk.Algorithm)
	return alg == webauthncose.AlgES256 || alg == webauthncose.AlgRS256
}

func (c *Ceremonies) BeginLogin(w http.ResponseWriter) (*protocol.CredentialAssertion, error) {
	assertion, session, err := c.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	challenge, err := b64Decode(session.Challenge)
	if err != nil {
		return nil, err
	}
	expires := c.now().Add(PendingFor)
	http.SetCookie(w, &http.Cookie{
		Name: PasskeyChallengeCookie, Value: c.seal(challenge, expires), Path: "/",
		MaxAge: int(PendingFor.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	return assertion, nil
}

// seal is the challenge, its expiry and an HMAC over both: everything the
// map needs to judge the answer, kept by the browser, so that a flood of
// requests for challenges costs the map nothing to hold.
func (c *Ceremonies) seal(challenge []byte, expires time.Time) string {
	payload := binary.BigEndian.AppendUint64(append([]byte{}, challenge...), uint64(expires.Unix()))
	mac := hmac.New(sha256.New, c.hmacKey)
	mac.Write(payload)
	return encoding.EncodeToString(mac.Sum(payload))
}

func (c *Ceremonies) unseal(value string) (challenge []byte, result string) {
	raw, err := encoding.DecodeString(value)
	if err != nil || len(raw) < 8+sha256.Size+16 {
		return nil, ResultBadAssertion
	}
	payload, sum := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, c.hmacKey)
	mac.Write(payload)
	if !hmac.Equal(sum, mac.Sum(nil)) {
		return nil, ResultBadAssertion
	}
	challenge, at := payload[:len(payload)-8], binary.BigEndian.Uint64(payload[len(payload)-8:])
	if !c.now().Before(time.Unix(int64(at), 0)) {
		return nil, ResultExpired
	}
	return challenge, ""
}

func refuse(result string) error { return &CeremonyError{Result: result} }

var errUnknownCredential = errors.New("unknown credential")

func (c *Ceremonies) FinishLogin(w http.ResponseWriter, r *http.Request, body []byte, allowed func(xuid string) bool) (Passkey, error) {
	jar, err := r.Cookie(PasskeyChallengeCookie)
	if err != nil {
		return Passkey{}, refuse(ResultExpired)
	}
	http.SetCookie(w, &http.Cookie{Name: PasskeyChallengeCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	challenge, result := c.unseal(jar.Value)
	if result != "" {
		return Passkey{}, refuse(result)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return Passkey{}, refuse(ResultBadAssertion)
	}
	cd := parsed.Response.CollectedClientData
	if cd.Type != protocol.AssertCeremony || cd.Origin != c.origin || cd.CrossOrigin || cd.TopOrigin != "" || len(parsed.Response.UserHandle) == 0 {
		return Passkey{}, refuse(ResultBadAssertion)
	}
	var found Passkey
	var pending bool
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		_, k, p, ok := c.keys.Lookup(userHandle, rawID)
		if !ok {
			return nil, errUnknownCredential
		}
		found, pending = k, p
		return waUser{handle: userHandle, keys: []Passkey{k}}, nil
	}
	session := webauthn.SessionData{Challenge: encoding.EncodeToString(challenge), RelyingPartyID: c.rpID, UserVerification: protocol.VerificationRequired}
	_, _, err = c.wa.ValidatePasskeyLogin(handler, session, parsed)
	switch {
	case errors.Is(err, errUnknownCredential):
		return Passkey{}, refuse(ResultUnknownCredential)
	case err != nil:
		return Passkey{}, refuse(ResultBadAssertion)
	}
	// From here the signature is good, and a refusal returns the passkey
	// it was for, so that the caller can say whose it was. The challenge
	// is spent in one step, so two copies of one assertion cannot both pass.
	spent := sha256.Sum256(challenge)
	c.mu.Lock()
	now := c.now()
	for h, until := range c.used {
		if !now.Before(until) {
			delete(c.used, h)
		}
	}
	_, replayed := c.used[spent]
	if !replayed {
		c.used[spent] = now.Add(PendingFor)
	}
	c.mu.Unlock()
	if replayed {
		return found, refuse(ResultReplayed)
	}
	if pending {
		return found, refuse(ResultPending)
	}
	// Asked before the count is stored: a refused login moves nothing. A
	// caller that asks nothing lets nobody in.
	if allowed == nil || !allowed(found.XUID) {
		return found, refuse(ResultNotOperator)
	}
	// The count is read from what the authenticator signed and not from
	// the library's credential: on a count that went back the library
	// keeps the old one and only raises CloneWarning, and the map refuses.
	flags := parsed.Response.AuthenticatorData.Flags
	k, err := c.keys.Use(found.ID, parsed.Response.AuthenticatorData.Counter, flags.HasBackupEligible(), flags.HasBackupState())
	switch {
	case errors.Is(err, ErrCounter):
		return found, refuse(ResultCounter)
	case errors.Is(err, ErrUnknownKey):
		// Removed since it was looked up.
		return Passkey{}, refuse(ResultUnknownCredential)
	case err != nil:
		// Not a refusal: the store could not be written, and no login
		// is issued on a count that was not kept.
		return Passkey{}, err
	}
	return k, nil
}

func b64Decode(s string) ([]byte, error) { return encoding.DecodeString(strings.TrimRight(s, "=")) }
