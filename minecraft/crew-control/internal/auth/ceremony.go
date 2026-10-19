package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

const (
	CeremonyCookie = "__Host-crew_ceremony"
	ceremonyTTL    = 5 * time.Minute
	// Anonymous login options also take a slot, so a flood evicts the oldest
	// instead of refusing: a refusal would let anyone lock operators out.
	// Each kind has its own budget so a login flood cannot push out an invited
	// operator's pending registration.
	maxRegisterCeremonies = 16
	maxLoginCeremonies    = 64

	kindRegister = "register"
	kindLogin    = "login"
)

type ceremony struct {
	kind    string
	op      string
	invite  string
	session webauthn.SessionData
	expires time.Time
}

// start stores c under a fresh random id and hands the id to the browser.
// A browser that restarts keeps one slot: its previous ceremony is dropped.
func (a *Auth) start(w http.ResponseWriter, r *http.Request, c *ceremony) {
	id := b64.EncodeToString(randomBytes(24))
	now := a.now()
	c.expires = now.Add(ceremonyTTL)

	a.cmu.Lock()
	defer a.cmu.Unlock()
	if old, err := r.Cookie(CeremonyCookie); err == nil {
		delete(a.cer, old.Value)
	}
	a.prune(now)
	limit := maxLoginCeremonies
	if c.kind == kindRegister {
		limit = maxRegisterCeremonies
	}
	for {
		count, oldest := 0, ""
		for k, v := range a.cer {
			if v.kind != c.kind {
				continue
			}
			count++
			if oldest == "" || v.expires.Before(a.cer[oldest].expires) {
				oldest = k
			}
		}
		if count < limit {
			break
		}
		delete(a.cer, oldest)
	}
	a.cer[id] = c
	http.SetCookie(w, &http.Cookie{
		Name: CeremonyCookie, Value: id, Path: "/", MaxAge: int(ceremonyTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func (a *Auth) prune(now time.Time) {
	for k, v := range a.cer {
		if !now.Before(v.expires) {
			delete(a.cer, k)
		}
	}
}

func (a *Auth) ceremonyCount() int {
	a.cmu.Lock()
	defer a.cmu.Unlock()
	a.prune(a.now())
	return len(a.cer)
}

// take removes and returns the browser's ceremony. It is spent whether or
// not the answer is good, so one challenge never gets a second try.
func (a *Auth) take(w http.ResponseWriter, r *http.Request, kind string) (*ceremony, bool) {
	c, err := r.Cookie(CeremonyCookie)
	if err != nil {
		return nil, false
	}
	clearCookie(w, CeremonyCookie)
	a.cmu.Lock()
	defer a.cmu.Unlock()
	cer, ok := a.cer[c.Value]
	delete(a.cer, c.Value)
	if !ok || cer.kind != kind || !a.now().Before(cer.expires) {
		return nil, false
	}
	return cer, true
}

type waUser struct {
	handle []byte
	op     Operator
	keys   []Passkey
}

func (u waUser) WebAuthnID() []byte          { return u.handle }
func (u waUser) WebAuthnName() string        { return u.op.ID }
func (u waUser) WebAuthnDisplayName() string { return u.op.Name }
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

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_body")
		return nil, false
	}
	return raw, true
}

func (a *Auth) registerOptions(w http.ResponseWriter, r *http.Request) {
	if !stateChanging(r) {
		fail(w, http.StatusForbidden, "bad_request")
		return
	}
	raw, ok := readBody(w, r)
	if !ok {
		return
	}
	var req struct {
		Operator string `json:"operator"`
		Invite   string `json:"invite"`
	}
	if json.Unmarshal(raw, &req) != nil {
		fail(w, http.StatusBadRequest, "bad_body")
		return
	}
	if a.invites.Throttled() {
		fail(w, http.StatusTooManyRequests, "slow_down")
		return
	}
	// One answer for an unknown operator, another operator's code, a used
	// code and a wrong one, so the response teaches a guesser nothing.
	op, listed := a.operator(req.Operator)
	if !listed || !a.invites.Valid(op.ID, req.Invite) {
		a.invites.Failed()
		a.event("invite_refused", nil)
		fail(w, http.StatusForbidden, "invite_refused")
		return
	}
	existing := a.keys.Existing(op.ID)
	if len(existing) >= maxPasskeys {
		fail(w, http.StatusConflict, "passkey_limit")
		return
	}
	u := waUser{handle: a.keys.UserHandle(op.ID), op: op, keys: existing}
	var exclude []protocol.CredentialDescriptor
	for _, cred := range u.WebAuthnCredentials() {
		exclude = append(exclude, cred.Descriptor())
	}
	creation, session, err := a.wa.BeginRegistration(u,
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
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	// The ceremony's own TTL is the one that counts, on the injected clock.
	session.Expires = time.Time{}
	a.start(w, r, &ceremony{kind: kindRegister, op: op.ID, invite: req.Invite, session: *session})
	writeJSON(w, http.StatusOK, creation)
}

func allowedAlg(publicKey []byte) bool {
	var pk webauthncose.PublicKeyData
	if err := webauthncbor.Unmarshal(publicKey, &pk); err != nil {
		return false
	}
	alg := webauthncose.COSEAlgorithmIdentifier(pk.Algorithm)
	return alg == webauthncose.AlgES256 || alg == webauthncose.AlgRS256
}

func (a *Auth) registerFinish(w http.ResponseWriter, r *http.Request) {
	if !stateChanging(r) {
		fail(w, http.StatusForbidden, "bad_request")
		return
	}
	cer, ok := a.take(w, r, kindRegister)
	if !ok {
		fail(w, http.StatusBadRequest, "ceremony_expired")
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	op, listed := a.operator(cer.op)
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if !listed || err != nil {
		fail(w, http.StatusBadRequest, "passkey_refused")
		return
	}
	cd := parsed.Response.CollectedClientData
	if cd.Type != protocol.CreateCeremony || cd.Origin != a.origin || cd.CrossOrigin || cd.TopOrigin != "" ||
		parsed.Response.AttestationObject.Format != "none" || len(parsed.RawID) > 1023 {
		fail(w, http.StatusBadRequest, "passkey_refused")
		return
	}
	u := waUser{handle: cer.session.UserID, op: op, keys: a.keys.Existing(op.ID)}
	cred, err := a.wa.CreateCredential(u, cer.session, parsed)
	if err != nil || len(cred.ID) > 1023 || !allowedAlg(cred.PublicKey) {
		fail(w, http.StatusBadRequest, "passkey_refused")
		return
	}

	// The invite is spent before the passkey is stored, atomically, so two
	// ceremonies on one code cannot both enrol; it is given back if the
	// passkey then cannot be stored.
	spent, err := a.invites.Spend(op.ID, cer.invite)
	if err != nil {
		a.event("enrol_failed", logging.Fields{"operator": op.ID, "stage": "invite_store"})
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	if !spent {
		a.event("invite_refused", logging.Fields{"operator": op.ID})
		fail(w, http.StatusForbidden, "invite_refused")
		return
	}
	k := Passkey{
		ID: cred.ID, PublicKey: cred.PublicKey, UserHandle: cer.session.UserID, Operator: op.ID,
		SignCount: cred.Authenticator.SignCount, BackupEligible: cred.Flags.BackupEligible, BackupState: cred.Flags.BackupState,
	}
	if err := a.keys.Add(k); err != nil {
		a.invites.Unspend(cer.invite)
		switch {
		case errors.Is(err, ErrTooMany):
			fail(w, http.StatusConflict, "passkey_limit")
		case errors.Is(err, ErrDuplicate), errors.Is(err, ErrWrongHandle):
			fail(w, http.StatusBadRequest, "passkey_refused")
		default:
			a.event("enrol_failed", logging.Fields{"operator": op.ID, "stage": "passkey_store"})
			fail(w, http.StatusInternalServerError, "internal")
		}
		return
	}
	a.issue(w, op.ID, KeyOf(k.ID))
	a.event("operator_enrolled", logging.Fields{"operator": op.ID})
	writeJSON(w, http.StatusOK, map[string]Operator{"operator": op})
}

func (a *Auth) loginOptions(w http.ResponseWriter, r *http.Request) {
	if !stateChanging(r) {
		fail(w, http.StatusForbidden, "bad_request")
		return
	}
	assertion, session, err := a.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	session.Expires = time.Time{}
	a.start(w, r, &ceremony{kind: kindLogin, session: *session})
	writeJSON(w, http.StatusOK, assertion)
}

var errUnknownCredential = errors.New("unknown credential")

func (a *Auth) loginFinish(w http.ResponseWriter, r *http.Request) {
	if !stateChanging(r) {
		fail(w, http.StatusForbidden, "bad_request")
		return
	}
	// Every refusal is the same 401 and the same body.
	refuse := func(reason string, op string) {
		f := logging.Fields{"reason": reason}
		if op != "" {
			f["operator"] = op
		}
		a.event("login_refused", f)
		fail(w, http.StatusUnauthorized, "login_refused")
	}
	cer, ok := a.take(w, r, kindLogin)
	if !ok {
		refuse("ceremony", "")
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		refuse("malformed", "")
		return
	}
	cd := parsed.Response.CollectedClientData
	if cd.Type != protocol.AssertCeremony || cd.Origin != a.origin || cd.CrossOrigin || cd.TopOrigin != "" || len(parsed.Response.UserHandle) == 0 {
		refuse("client_data", "")
		return
	}
	var found Passkey
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		k, ok := a.keys.Lookup(userHandle, rawID)
		if !ok {
			return nil, errUnknownCredential
		}
		op, ok := a.operator(k.Operator)
		if !ok {
			return nil, errUnknownCredential
		}
		found = k
		return waUser{handle: userHandle, op: op, keys: []Passkey{k}}, nil
	}
	if _, _, err := a.wa.ValidatePasskeyLogin(handler, cer.session, parsed); err != nil {
		if errors.Is(err, errUnknownCredential) {
			refuse("unknown_credential", "")
		} else {
			refuse("bad_assertion", "")
		}
		return
	}
	op, ok := a.operator(found.Operator)
	if !ok {
		refuse("not_operator", found.Operator)
		return
	}
	// The count is read from what the authenticator signed: on a count that
	// went back the library keeps the old one and only raises a warning.
	flags := parsed.Response.AuthenticatorData.Flags
	k, err := a.keys.Use(found.ID, parsed.Response.AuthenticatorData.Counter, flags.HasBackupEligible(), flags.HasBackupState())
	switch {
	case errors.Is(err, ErrCounter):
		refuse("counter", op.ID)
		return
	case errors.Is(err, ErrUnknownKey):
		refuse("unknown_credential", op.ID)
		return
	case err != nil:
		a.event("login_failed", logging.Fields{"operator": op.ID, "stage": "passkey_store"})
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	a.issue(w, op.ID, KeyOf(k.ID))
	a.event("operator_login", logging.Fields{"operator": op.ID})
	writeJSON(w, http.StatusOK, map[string]Operator{"operator": op})
}
