package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// softKey is an ES256 authenticator that answers create() and get() the way
// a browser hands them to the page, so the checks run against real
// signatures and real CBOR.
type softKey struct {
	t     *testing.T
	priv  *ecdsa.PrivateKey
	id    []byte
	count uint32
}

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

type answer struct {
	rpID, origin, challenge string
	typ                     string
}

func (k *softKey) clientData(a answer, typ string) []byte {
	if a.typ != "" {
		typ = a.typ
	}
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": a.challenge, "origin": a.origin, "crossOrigin": false})
	return raw
}

func authData(rpID string, flags byte, count uint32, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, count)
	return append(out, attested...)
}

func (k *softKey) create(a answer) []byte {
	point, err := k.priv.PublicKey.Bytes()
	if err != nil {
		k.t.Fatal(err)
	}
	cose, _ := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: point[1:33], -3: point[33:65]})
	attested := make([]byte, 16)
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(k.id)))
	attested = append(attested, k.id...)
	attested = append(attested, cose...)
	ad := authData(a.rpID, 0x01|0x04|0x40, k.count, attested)
	att, _ := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad})
	body, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(k.clientData(a, "webauthn.create")), "attestationObject": b64.EncodeToString(att)},
	})
	return body
}

// get signs a discoverable login; counter 0 means "increment".
func (k *softKey) get(a answer, userHandle []byte, counter uint32) []byte {
	if counter == 0 {
		k.count++
		counter = k.count
	}
	ad := authData(a.rpID, 0x01|0x04, counter, nil)
	cdj := k.clientData(a, "webauthn.get")
	h := sha256.Sum256(cdj)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		k.t.Fatal(err)
	}
	resp := map[string]any{"clientDataJSON": b64.EncodeToString(cdj), "authenticatorData": b64.EncodeToString(ad), "signature": b64.EncodeToString(sig)}
	if userHandle != nil {
		resp["userHandle"] = b64.EncodeToString(userHandle)
	}
	body, _ := json.Marshal(map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key", "response": resp})
	return body
}
