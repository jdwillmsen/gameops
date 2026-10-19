package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/chat"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/plugin"
)

type fakeClaimer struct {
	code, xuid, gamertag   string
	calls                  int
	err                    error
	revoked                []string
	passkeyCode, passkeyFP string
	passkeyErr             error
	passkeyFor             []string
}

func (f *fakeClaimer) Revoke(_ context.Context, xuid string) error {
	f.revoked = append(f.revoked, xuid)
	return f.err
}

func (f *fakeClaimer) PasskeyCode(_ context.Context, xuid string) (string, string, error) {
	f.passkeyFor = append(f.passkeyFor, xuid)
	return f.passkeyCode, f.passkeyFP, f.passkeyErr
}

func (f *fakeClaimer) Claim(_ context.Context, code, xuid, gamertag string) error {
	f.calls++
	f.code, f.xuid, f.gamertag = code, xuid, gamertag
	return f.err
}

type fakeNames map[string]string

func (f fakeNames) NameFor(xuid string) (string, bool) {
	name, ok := f[xuid]
	return name, ok
}

const mapURL = "https://map.example"

func runMap(t *testing.T, claimer *fakeClaimer, args ...string) (string, error) {
	t.Helper()
	p := NewMapLogin(claimer, fakeNames{"2535412345678901": "Steve Builds"}, mapURL)
	var cmd plugin.Command
	for _, c := range p.Commands() {
		if c.Name == "map" {
			cmd = c
		}
	}
	if cmd.Run == nil {
		t.Fatal("no !map command")
	}
	// Anyone who can type in chat is on the server, which is all the map
	// asks; it is not a privilege to gate further.
	if cmd.Permission != plugin.PermissionVisitor {
		t.Errorf("!map needs %v, want visitor", cmd.Permission)
	}
	return cmd.Run(context.Background(), &plugin.Context{}, plugin.Invocation{ActorXUID: "2535412345678901", Args: args})
}

func TestMap_ClaimsTheCodeForWhoeverTypedIt(t *testing.T) {
	claimer := &fakeClaimer{}
	reply, err := runMap(t, claimer, "abc234")
	if err != nil {
		t.Fatal(err)
	}
	if claimer.calls != 1 || claimer.code != "ABC234" || claimer.xuid != "2535412345678901" || claimer.gamertag != "Steve Builds" {
		t.Errorf("claimed %+v", claimer)
	}
	if !strings.Contains(strings.ToLower(reply), "unlocked") {
		t.Errorf("reply = %q", reply)
	}
	// Someone talked into typing another person's code only ever sees this
	// reply, so it is the one place that can tell them what they just did.
	if !strings.Contains(reply, "not your") {
		t.Errorf("reply = %q, want it to say what to do if the screen was someone else's", reply)
	}
}

// The console can speak in chat but is not a player; a map login is a
// player's.
func TestMap_TheConsoleCannotLogIn(t *testing.T) {
	claimer := &fakeClaimer{}
	p := NewMapLogin(claimer, fakeNames{}, mapURL)
	reply, err := p.Commands()[0].Run(context.Background(), &plugin.Context{}, plugin.Invocation{ActorXUID: chat.ServerOrigin, Args: []string{"ABC234"}})
	if err != nil || claimer.calls != 0 {
		t.Fatalf("err %v, %d claims", err, claimer.calls)
	}
	if !strings.Contains(reply, "player") {
		t.Errorf("reply = %q", reply)
	}
}

func TestMap_WithNoCodeSaysWhereToGetOne(t *testing.T) {
	claimer := &fakeClaimer{}
	reply, err := runMap(t, claimer)
	if err != nil || claimer.calls != 0 {
		t.Fatalf("err %v, %d claims", err, claimer.calls)
	}
	if !strings.Contains(reply, mapURL) || !strings.Contains(reply, "!map") {
		t.Errorf("reply = %q, want the address and the command", reply)
	}
}

// What a player mistypes should never reach the map service: the service
// answers every bad code the same way, and that answer is for real codes.
func TestMap_MalformedCodesAreAnsweredWithoutAsking(t *testing.T) {
	for _, args := range [][]string{{"ABC"}, {"ABCDEFG"}, {"ABC", "234"}, {"ABCDE0"}, {"ABCD-1"}, {"ABCDEI"}} {
		claimer := &fakeClaimer{}
		reply, err := runMap(t, claimer, args...)
		if err != nil || claimer.calls != 0 {
			t.Errorf("%v: err %v, %d claims", args, err, claimer.calls)
		}
		if !strings.Contains(reply, mapURL) {
			t.Errorf("%v: reply %q does not say where to get a code", args, reply)
		}
	}
}

// The map page shows a passkey's fingerprint beside the box for its code,
// and a player who types that fingerprint, or the passkey's name, has
// mistaken which command adds it.
func TestMap_APasskeyFingerprintOrNamePointsToMapPasskey(t *testing.T) {
	for _, args := range [][]string{{"3EB9-B4A4"}, {"3eb9-b4a4"}, {"google"}} {
		claimer := &fakeClaimer{}
		reply, err := runMap(t, claimer, args...)
		if err != nil || claimer.calls != 0 || len(claimer.passkeyFor) != 0 {
			t.Errorf("%v: err %v, %d claims, %d passkey codes", args, err, claimer.calls, len(claimer.passkeyFor))
		}
		if !strings.Contains(reply, "!map passkey") {
			t.Errorf("%v: reply %q does not name !map passkey", args, reply)
		}
	}
}

func TestMap_UnknownOrExpiredCodeIsAPlainAnswer(t *testing.T) {
	claimer := &fakeClaimer{err: mapclient.ErrUnknownCode}
	reply, err := runMap(t, claimer, "ABC234")
	if err != nil {
		t.Fatalf("an expired code surfaced as an error: %v", err)
	}
	if !strings.Contains(reply, "expired") || !strings.Contains(reply, mapURL) {
		t.Errorf("reply = %q", reply)
	}
}

func TestMap_ServiceFailureIsAnErrorNotAClaimOfSuccess(t *testing.T) {
	claimer := &fakeClaimer{err: errors.New("map: status 502")}
	reply, err := runMap(t, claimer, "ABC234")
	if err == nil || reply != "" {
		t.Fatalf("reply %q, err %v", reply, err)
	}
}

// A player the roster has not learned yet still gets their login; the XUID
// is the identity and the name is only what the page calls them.
func TestMap_UnknownGamertagStillClaims(t *testing.T) {
	claimer := &fakeClaimer{}
	p := NewMapLogin(claimer, fakeNames{}, mapURL)
	if _, err := p.Commands()[0].Run(context.Background(), &plugin.Context{}, plugin.Invocation{ActorXUID: "42", Args: []string{"ABC234"}}); err != nil {
		t.Fatal(err)
	}
	if claimer.xuid != "42" || claimer.gamertag != "" {
		t.Errorf("claimed %+v", claimer)
	}
}

// The way out for a player who typed a code off someone else's screen.
func TestMap_LogoutEndsEveryMapLoginOfWhoeverTypedIt(t *testing.T) {
	for _, word := range []string{"logout", "LOGOUT"} {
		claimer := &fakeClaimer{}
		reply, err := runMap(t, claimer, word)
		if err != nil {
			t.Fatal(err)
		}
		if claimer.calls != 0 || len(claimer.revoked) != 1 || claimer.revoked[0] != "2535412345678901" {
			t.Errorf("!map %s: %d claims, revoked %v", word, claimer.calls, claimer.revoked)
		}
		if reply != "Every browser that was logged in to the map as you has been logged out, and any passkeys for your map login have been removed." {
			t.Errorf("reply = %q", reply)
		}
	}
}

func TestMap_LogoutThatFailedPropagatesWithoutClaimingSuccess(t *testing.T) {
	claimer := &fakeClaimer{err: errors.New("map: status 502")}
	if reply, err := runMap(t, claimer, "logout"); err == nil || reply != "" {
		t.Fatalf("reply %q, err %v", reply, err)
	}
}

func TestMap_PasskeyFailureOtherThanNothingWaitingPropagatesWithoutAWhisper(t *testing.T) {
	reply, err := runMap(t, &fakeClaimer{passkeyErr: errors.New("map: status 503")}, "passkey")
	if err == nil || reply != "" {
		t.Errorf("reply %q, err %v", reply, err)
	}
}

const passkeyWhisper = "Your map passkey code is ABC234, for the passkey with fingerprint 7F3A-91C2. Type it into the map page that asked for it, within 2 minutes, only if that page shows the same fingerprint. It lets that browser log in to the map as you, as admin, without the game. Never give it to anyone, and never type it into a page someone else sent you. If you did not just add a passkey, type !map logout."

func TestMap_PasskeyWhispersTheCodeWithItsWarning(t *testing.T) {
	for _, word := range []string{"passkey", "PASSKEY"} {
		claimer := &fakeClaimer{passkeyCode: "ABC234", passkeyFP: "7F3A-91C2"}
		reply, err := runMap(t, claimer, word)
		if err != nil {
			t.Fatal(err)
		}
		if reply != passkeyWhisper {
			t.Errorf("!map %s reply = %q", word, reply)
		}
		if len(claimer.passkeyFor) != 1 || claimer.passkeyFor[0] != "2535412345678901" || claimer.calls != 0 {
			t.Errorf("asked for %v, claims %d", claimer.passkeyFor, claimer.calls)
		}
	}
}

func TestMap_PasskeyWithNothingWaitingSaysSo(t *testing.T) {
	reply, err := runMap(t, &fakeClaimer{passkeyErr: mapclient.ErrNoPendingPasskey}, "passkey")
	if err != nil || reply != "There is no passkey waiting to be added for you. Start from the map page." {
		t.Errorf("reply %q, err %v", reply, err)
	}
}

func TestMap_PasskeyIsNotTheConsoles(t *testing.T) {
	claimer := &fakeClaimer{passkeyCode: "ABC234"}
	p := NewMapLogin(claimer, fakeNames{}, mapURL)
	reply, _ := p.Commands()[0].Run(context.Background(), &plugin.Context{}, plugin.Invocation{ActorXUID: chat.ServerOrigin, Args: []string{"passkey"}})
	if strings.Contains(reply, "ABC234") {
		t.Errorf("the console was given a code: %q", reply)
	}
	if len(claimer.passkeyFor) != 0 {
		t.Errorf("the map was asked for a code for the console: %v", claimer.passkeyFor)
	}
}

func TestMap_RepliesAreKeptOutOfTheLog(t *testing.T) {
	for _, c := range NewMapLogin(&fakeClaimer{}, fakeNames{}, mapURL).Commands() {
		if c.Name == "map" && !c.RedactReply {
			t.Error("!map's replies, which now carry passkey codes, are logged")
		}
	}
}
