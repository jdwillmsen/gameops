package mcproto

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft"
)

// Captured from the production server on 2026-09-08 rather than written by
// hand, so the field positions this package depends on are the real ones.
const realPong = "MCPE;FWB Server;2169;1.26.45;4;20;13467591190557326198;FWB;Survival;1;31132;19133;0;1;0;"

func TestParsePongReadsTheLiveServersAdvertisement(t *testing.T) {
	ad, err := ParsePong(realPong)
	if err != nil {
		t.Fatalf("parsing a real pong failed: %v", err)
	}
	if ad.Protocol != 2169 {
		t.Errorf("protocol = %d, want 2169", ad.Protocol)
	}
	if ad.Version != "1.26.45" {
		t.Errorf("version = %q, want \"1.26.45\"", ad.Version)
	}
}

// A MOTD may contain an escaped semicolon. Splitting naively would shift
// every later field by one and read the MOTD's tail as the protocol number.
func TestParsePongHandlesEscapedSemicolonsInTheMotd(t *testing.T) {
	ad, err := ParsePong(`MCPE;Server\;s Home;2169;1.26.45;0;10;1;x;Survival;`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ad.Protocol != 2169 {
		t.Errorf("protocol = %d, want 2169 -- the escaped semicolon shifted the fields", ad.Protocol)
	}
}

func TestParsePongRejectsUnusablePongs(t *testing.T) {
	for name, pong := range map[string]string{
		"truncated":      "MCPE;FWB Server;2169",
		"empty":          "",
		"not a number":   "MCPE;FWB Server;latest;1.26.45;0;10;",
		"zero":           "MCPE;FWB Server;0;1.26.45;0;10;",
		"negative":       "MCPE;FWB Server;-2169;1.26.45;0;10;",
		"empty protocol": "MCPE;FWB Server;;1.26.45;0;10;",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePong(pong); err == nil {
				t.Errorf("ParsePong(%q) succeeded; a bad pong must fall back to the compiled-in protocol", pong)
			}
		})
	}
}

// The case that costs nothing must also change nothing: when the server is on
// the version this binary was built for, the caller gets the default back and
// nothing is logged.
func TestMatchingAdvertisementUsesTheCompiledProtocolUntouched(t *testing.T) {
	baked := minecraft.DefaultProtocol
	spoofs := 0
	got := protocolFor(Advertisement{Protocol: baked.ID(), Version: baked.Ver()}, baked, func(Advertisement) { spoofs++ })
	if got != minecraft.Protocol(baked) {
		t.Error("a server on the compiled-in protocol should dial with the compiled-in protocol")
	}
	if spoofs != 0 {
		t.Errorf("logged %d spoofs for a server that needed none", spoofs)
	}
}

func TestDifferentAdvertisementAnnouncesTheServersNumber(t *testing.T) {
	baked := minecraft.DefaultProtocol
	advertised := baked.ID() + 7

	var seen Advertisement
	got := protocolFor(Advertisement{Protocol: advertised, Version: "1.27.0"}, baked, func(ad Advertisement) { seen = ad })

	if got.ID() != advertised {
		t.Errorf("announced protocol %d, want the server's %d", got.ID(), advertised)
	}
	if seen.Protocol != advertised || seen.Version != "1.27.0" {
		t.Errorf("spoof callback got %+v, want the advertised values", seen)
	}
}

// The reason this is an embed and not a reimplementation: announcing a
// different number must not change the packet schema the client speaks. If
// Ver or the packet pool moved with the number, the client would be claiming
// a schema it does not have.
func TestSpoofingChangesOnlyTheAnnouncedNumber(t *testing.T) {
	baked := minecraft.DefaultProtocol
	got := protocolFor(Advertisement{Protocol: baked.ID() + 1}, baked, nil)

	if got.Ver() != baked.Ver() {
		t.Errorf("version became %q, want the compiled-in %q", got.Ver(), baked.Ver())
	}
	if len(got.Packets(false)) != len(baked.Packets(false)) {
		t.Error("packet pool changed; spoofing must only change the announced number")
	}
}

// A server that rolls back -- a restore, a pinned downgrade -- must clear the
// spoof rather than leave the client announcing a number that no longer
// exists. Negotiate is stateless precisely so this needs no unwinding.
func TestRollbackClearsTheSpoof(t *testing.T) {
	baked := minecraft.DefaultProtocol
	if p := protocolFor(Advertisement{Protocol: baked.ID() + 1}, baked, nil); p.ID() == baked.ID() {
		t.Fatal("setup: expected a spoof")
	}
	if p := protocolFor(Advertisement{Protocol: baked.ID()}, baked, nil); p.ID() != baked.ID() {
		t.Error("a server back on the compiled-in protocol still got a spoofed number")
	}
}

func TestIsNetherNetTellsTheTransportFromTheAddress(t *testing.T) {
	for address, want := range map[string]bool{
		"http://192.168.1.87:31134": true,
		"https://mc.example:31134":  true,
		"HTTP://192.168.1.87:31134": true,
		"192.168.1.87:19132":        false,
		"mc.example.internal:19132": false,
		"[::1]:19132":               false,
		"httpbin.example.org:19132": false,
		"":                          false,
	} {
		if got := IsNetherNet(address); got != want {
			t.Errorf("IsNetherNet(%q) = %v, want %v", address, got, want)
		}
	}
}

// signaling serves /v1/join the way a NetherNet-only server does, with a name
// that would break a semicolon-delimited pong.
func signaling(t *testing.T, status string, code int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/join" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(status))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const nnStatus = `{"name":"FWB;Server","protocol":2200,"version":"1.26.60","level":"FWB","players":1,"maxPlayers":20,"gameType":0}`

func TestAdvertiseReadsANetherNetServersJoinEndpoint(t *testing.T) {
	ad, err := Advertise(context.Background(), signaling(t, nnStatus, http.StatusOK))
	if err != nil {
		t.Fatalf("Advertise: %v", err)
	}
	if ad.Protocol != 2200 || ad.Version != "1.26.60" {
		t.Errorf("advertisement = %+v, want protocol 2200 version 1.26.60", ad)
	}
}

func TestAdvertiseRejectsAnUnusableNetherNetStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		code int
	}{
		"server error": {nnStatus, http.StatusInternalServerError},
		"not json":     {"<html>", http.StatusOK},
		"zero":         {`{"protocol":0,"version":"1"}`, http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Advertise(context.Background(), signaling(t, tc.body, tc.code)); err == nil {
				t.Error("Advertise succeeded; the caller must fall back to the compiled-in protocol")
			}
		})
	}
}

func TestNegotiateSpoofsFromANetherNetServer(t *testing.T) {
	advertised := minecraft.DefaultProtocol.ID() + 5
	status := fmt.Sprintf(`{"name":"x","protocol":%d,"version":"9.9.9","level":"x","players":0,"maxPlayers":1,"gameType":0}`, advertised)

	var seen Advertisement
	got := Negotiate(context.Background(), signaling(t, status, http.StatusOK), func(ad Advertisement) { seen = ad })
	if got.ID() != advertised {
		t.Errorf("announced %d, want the server's %d", got.ID(), advertised)
	}
	if seen.Version != "9.9.9" {
		t.Errorf("spoof callback got %+v", seen)
	}
}

func TestNegotiateFallsBackWhenNetherNetSignalingIsDown(t *testing.T) {
	got := Negotiate(context.Background(), "http://127.0.0.1:1", nil)
	if got.ID() != minecraft.DefaultProtocol.ID() {
		t.Errorf("announced %d, want the compiled-in %d", got.ID(), minecraft.DefaultProtocol.ID())
	}
}
