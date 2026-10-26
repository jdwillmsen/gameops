package auth

import (
	"time"

	"github.com/jdwillmsen/gameops/minecraft/pkg/xbox"
)

// XboxStateCookie holds an Xbox sign-in's state and PKCE verifier between
// the browser leaving for Microsoft and coming back.
const XboxStateCookie = cookiePrefix + "mcmap_xbox"

// XboxSessionTTL is a day. A code login proves someone is in the game now
// and lasts a week; an Xbox sign-in proves who owns an account and costs
// one click to repeat.
const XboxSessionTTL = 24 * time.Hour

// XboxStateTTL is long enough for a password and a second factor.
const XboxStateTTL = xbox.StateTTL

// Why a callback's cookie did not do.
var (
	ErrXboxNoState  = xbox.ErrNoState
	ErrXboxMismatch = xbox.ErrMismatch
	ErrXboxReplayed = xbox.ErrReplayed
)

// The label is part of the signature of every state cookie already in a
// browser, so it must not change.
const xboxStateLabel = "mcmap xbox sign-in state v1"

func XboxStateKey(sessionKey []byte) []byte { return xbox.StateKey(sessionKey, xboxStateLabel) }

type XboxFlow = xbox.Flow

func NewXboxFlow(sessionKey []byte, now func() time.Time) (*XboxFlow, error) {
	return xbox.NewFlow(XboxStateCookie, xboxStateLabel, sessionKey, now)
}
