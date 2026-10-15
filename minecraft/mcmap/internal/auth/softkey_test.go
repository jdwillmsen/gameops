package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// softKey is an authenticator in the test: an ES256 key, or an RS256 one,
// that answers create() and get() the way a browser hands them to the
// page, so that the map's checks are run against real signatures and real
// CBOR.
type softKey struct {
	t     *testing.T
	priv  *ecdsa.PrivateKey
	rsa   *rsa.PrivateKey // set for an RS256 key, in place of priv
	id    []byte
	count uint32
	// flags beyond UP and UV, which every ceremony sets unless told not to.
	be, bs bool
	// badSig spoils the next signature after it is made.
	badSig bool
	// still keeps no counter, as a synced passkey does: every count is 0.
	still bool
}

func newRSASoftKey(t *testing.T) *softKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &softKey{t: t, rsa: priv, id: id}
}

const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagBS = 0x10
	flagAT = 0x40
)

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &softKey{t: t, priv: priv, id: id}
}

var b64 = base64.RawURLEncoding

// ceremony is what a test may change about one answer.
type ceremony struct {
	rpID, origin, challenge, typ string
	clientData                   func(map[string]any)
	flags                        byte // replaces UP|UV when non-zero
	format                       string
	alg                          int
	id                           []byte
}

func (k *softKey) flagsFor(c ceremony) byte {
	f := byte(flagUP | flagUV)
	if c.flags != 0 {
		f = c.flags
	}
	if k.be {
		f |= flagBE
	}
	if k.bs {
		f |= flagBS
	}
	return f
}

func (k *softKey) clientDataJSON(c ceremony) []byte {
	cd := map[string]any{"type": c.typ, "challenge": c.challenge, "origin": c.origin, "crossOrigin": false}
	if c.clientData != nil {
		c.clientData(cd)
	}
	raw, _ := json.Marshal(cd)
	return raw
}

func (k *softKey) cose(alg int) []byte {
	var key map[int]any
	if k.rsa != nil {
		key = map[int]any{1: 3, 3: alg, -1: k.rsa.PublicKey.N.Bytes(), -2: big.NewInt(int64(k.rsa.PublicKey.E)).Bytes()}
	} else {
		// Uncompressed P-256 point: 0x04 || X || Y, 32 bytes each.
		point, err := k.priv.PublicKey.Bytes()
		if err != nil {
			k.t.Fatal(err)
		}
		x, y := point[1:33], point[33:65]
		key = map[int]any{1: 2, 3: alg, -1: 1, -2: x, -3: y}
	}
	raw, err := webauthncbor.Marshal(key)
	if err != nil {
		k.t.Fatal(err)
	}
	return raw
}

func authData(rpID string, flags byte, count uint32, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, count)
	return append(out, attested...)
}

// create answers navigator.credentials.create() as the page would send it.
func (k *softKey) create(c ceremony) []byte {
	if c.typ == "" {
		c.typ = "webauthn.create"
	}
	if c.format == "" {
		c.format = "none"
	}
	if c.alg == 0 {
		c.alg = -7
		if k.rsa != nil {
			c.alg = -257
		}
	}
	id := k.id
	if c.id != nil {
		id = c.id
	}
	attested := make([]byte, 16) // AAGUID, all zero for "none"
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(id)))
	attested = append(attested, id...)
	attested = append(attested, k.cose(c.alg)...)
	ad := authData(c.rpID, k.flagsFor(c)|flagAT, k.count, attested)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": c.format, "attStmt": map[string]any{}, "authData": ad})
	if err != nil {
		k.t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(id), "rawId": b64.EncodeToString(id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(k.clientDataJSON(c)), "attestationObject": b64.EncodeToString(att)},
	})
	return body
}

// get answers navigator.credentials.get() for a discoverable login.
func (k *softKey) get(c ceremony, userHandle []byte) []byte {
	if c.typ == "" {
		c.typ = "webauthn.get"
	}
	if !k.still {
		k.count++
	}
	ad := authData(c.rpID, k.flagsFor(c), k.count, nil)
	cdj := k.clientDataJSON(c)
	h := sha256.Sum256(cdj)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	var sig []byte
	var err error
	if k.rsa != nil {
		sig, err = rsa.SignPKCS1v15(rand.Reader, k.rsa, crypto.SHA256, digest[:])
	} else {
		sig, err = ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	}
	if err != nil {
		k.t.Fatal(err)
	}
	if k.badSig {
		sig[len(sig)-1] ^= 0xff
	}
	resp := map[string]any{"clientDataJSON": b64.EncodeToString(cdj), "authenticatorData": b64.EncodeToString(ad), "signature": b64.EncodeToString(sig)}
	if userHandle != nil {
		resp["userHandle"] = b64.EncodeToString(userHandle)
	}
	body, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key", "response": resp})
	return body
}
