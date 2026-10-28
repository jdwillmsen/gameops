package life

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// packetDigest hashes the wire form of every packet a unit sent, so a run is
// comparable to one recorded by another build of the engine.
func packetDigest(t *testing.T, pks []any) string {
	t.Helper()
	h := sha256.New()
	for _, p := range pks {
		pk := p.(packet.Packet)
		var buf bytes.Buffer
		pk.Marshal(protocol.NewWriter(&buf, 0))
		h.Write([]byte{byte(pk.ID())})
		h.Write(buf.Bytes())
		h.Write([]byte{0xff})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// The default profile must fight as the engine did before profiles existed.
// Comparing it with the unprofiled run of the same build cannot show that, so
// the digests in testdata were recorded from the engine at the commit that
// preceded the profile work; testdata/golden_generator.go.txt says how.
func TestTheDefaultProfileMatchesTheShippedEnginesCapture(t *testing.T) {
	raw, err := os.ReadFile("testdata/default_profile_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	explicit := tactics.NewProfile("explicit")
	seen := 0
	check := func(key string, got []any) {
		t.Helper()
		seen++
		want, ok := golden[key]
		if !ok {
			t.Errorf("%s has no recorded capture", key)
			return
		}
		if d := packetDigest(t, got); d != want {
			t.Errorf("%s: %d packets hash to %s, the shipped engine's capture is %s", key, len(got), d, want)
		}
	}
	for name, scenario := range fightScenarios {
		for _, role := range []string{"vanguard", "striker", "ranger"} {
			key := "fight/" + name + "/" + role
			check(key, replay(t, role, scenario, nil))
			check(key, replay(t, role, scenario, func(l *Life) {
				if err := l.SetProfile(explicit); err != nil {
					t.Fatal(err)
				}
			}))
		}
	}
	for name := range bossScenarios {
		check("boss/"+name, replayBoss(t, name, nil))
		check("boss/"+name, replayBoss(t, name, &explicit))
	}
	if want := 2 * len(golden); seen != want {
		t.Errorf("%d runs checked against %d recorded captures, want every capture checked with and without a profile", seen, len(golden))
	}
}
