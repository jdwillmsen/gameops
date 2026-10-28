package squad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// testProfile is a whole profile as the trainer would send it: a named
// champion with a parent and one family override.
const testProfile = `{"version":1,"name":"champ-4","parent":"champ-3","default":{"focusRange":12,"healBelow":14,"healSafe":3,"pressRange":1.6,"regroupWithin":3,"kiteBackoff":5,"shotEvery":30,"arrowNear":3,"lineWidth":1.2,"orbitMin":1.5,"critWindow":12,"meleeShieldTicks":12},"overrides":{"creeper":{"kiteBackoff":9}}}`

// The profile routes answer a lab squad's round trip: nothing before the
// PUT, the stored profile after it, and the newest one once it is
// replaced — the squad's *current* profile, not its history.
func TestProfileRoundTripOnALabSquad(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-alpha", "crew-18", "vanguard")

	if resp := r.do(t, "GET", "/v1/squads/lab-alpha/profile", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("before any PUT: %d, want 404 for the unit to read as defaults", resp.StatusCode)
	}
	if resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile", testProfile, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put: %d", resp.StatusCode)
	}
	body := readBody(t, r.do(t, "GET", "/v1/squads/lab-alpha/profile", "", nil))
	for _, want := range []string{`"version":1`, `"name":"champ-4"`, `"parent":"champ-3"`,
		`"focusRange":12`, `"meleeShieldTicks":12`, `"overrides":{"creeper":`, `"kiteBackoff":9`} {
		if !strings.Contains(body, want) {
			t.Errorf("the profile is missing %s: %s", want, body)
		}
	}

	second := strings.Replace(testProfile, `"name":"champ-4"`, `"name":"champ-5"`, 1)
	if resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile", second, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("replace: %d", resp.StatusCode)
	}
	if body := readBody(t, r.do(t, "GET", "/v1/squads/lab-alpha/profile", "", nil)); !strings.Contains(body, `"name":"champ-5"`) {
		t.Errorf("the squad still serves the old profile: %s", body)
	}
}

// The profile routes are unit routes: they do not exist without the
// token, and with it they take exactly the shared-token, cluster-only
// contract the hello and the call-out take.
func TestProfileRoutesAreGuardedLikeTheOtherWrites(t *testing.T) {
	if resp := newRig(t, false).do(t, "GET", "/v1/squads/lab-alpha/profile", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a read without a token: %d, want the route not to exist", resp.StatusCode)
	}
	if resp := newRig(t, false).do(t, "PUT", "/v1/squads/lab-alpha/profile", testProfile, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a write without a token: %d, want the route not to exist", resp.StatusCode)
	}

	r := newRig(t, true)
	tests := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"no bearer at all", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"wrong bearer", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"through the ingress", map[string]string{"X-Forwarded-For": "203.0.113.7"}, http.StatusNotFound},
		{"the shared token", map[string]string{}, http.StatusNoContent},
	}
	for _, tt := range tests {
		r.hello(t, "lab-alpha", "crew-18", "vanguard")
		if resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile", testProfile, tt.hdr); resp.StatusCode != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, resp.StatusCode, tt.want)
		}
	}
	if resp := r.do(t, "GET", "/v1/squads/lab-alpha/profile", "", map[string]string{"Authorization": ""}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a read without a bearer: %d, want 401", resp.StatusCode)
	}
}

// Profiles are lab-only: a squad not marked lab — which is any squad
// whose id does not say so — refuses the PUT, and never serves one.
func TestProfilePUTNeedsALabSquad(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	if resp := r.do(t, "PUT", "/v1/squads/alpha/profile", testProfile, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a production squad took a profile: %d, want 403", resp.StatusCode)
	}
	if resp := r.do(t, "GET", "/v1/squads/alpha/profile", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the refused squad serves a profile: %d, want 404", resp.StatusCode)
	}
	if resp := r.do(t, "PUT", "/v1/squads/lab-nope/profile", testProfile, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a squad that does not exist: %d, want 404", resp.StatusCode)
	}
	if resp := r.do(t, "PUT", "/v1/squads/nope/profile", testProfile, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unmarked squad that does not exist: %d, want 404", resp.StatusCode)
	}
}

func TestProfilePUTChecksItsShape(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-alpha", "crew-18", "vanguard")
	tests := []struct {
		name string
		body string
		want int
	}{
		{"the wrong version", `{"version":2,"name":"x","default":{}}`, http.StatusBadRequest},
		{"unknown fields", `{"version":1,"name":"x","default":{},"mood":"bold"}`, http.StatusBadRequest},
		{"an unknown default field", `{"version":1,"name":"x","default":{"ferocity":9}}`, http.StatusBadRequest},
		{"not even JSON", `bold`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
		{"trailing data", testProfile + ` {}`, http.StatusBadRequest},
		{"a name one past the line", `{"version":1,"name":"` + strings.Repeat("a", 33) + `","default":{}}`, http.StatusBadRequest},
		{"a name with a newline", `{"version":1,"name":"champ` + "\n" + `4","default":{}}`, http.StatusBadRequest},
		{"a parent one past the line", `{"version":1,"name":"x","parent":"` + strings.Repeat("p", 33) + `","default":{}}`, http.StatusBadRequest},
		{"a body past the cap", `{"version":1,"name":"` + strings.Repeat("a", maxProfileLen) + `","default":{}}`, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile", tt.body, nil)
			if resp.StatusCode != tt.want {
				t.Errorf("status %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
	// Nothing above may have landed; the squad still serves none.
	if resp := r.do(t, "GET", "/v1/squads/lab-alpha/profile", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a refused PUT still left a profile: %d", resp.StatusCode)
	}
}

// The server is the door, not the clamp: it stores what the contract's
// shape allows and serves it as it came, because the bounds live with
// the contract and every unit runs tactics.Profile.Validate on load.
// The round trip here pins that split — a value the server cannot judge
// must survive it untouched, or the unit would silently fight on
// numbers nobody chose.
func TestProfileClampingIsTheUnitsNotTheServers(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-alpha", "crew-18", "vanguard")
	if resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile",
		`{"version":1,"name":"wide","default":{"healSafe":500}}`, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put: %d", resp.StatusCode)
	}
	if body := readBody(t, r.do(t, "GET", "/v1/squads/lab-alpha/profile", "", nil)); !strings.Contains(body, `"healSafe":500`) {
		t.Errorf("the stored value did not survive the server: %s", body)
	}
}

// A stored profile is news a parked poll must hear at once: the PUT
// moves the revision, so a unit waiting with the old one answers with
// the new profileSeq instead of riding its wait out.
func TestProfileChangeWakesThePoll(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-alpha", "crew-18", "vanguard")
	// Prime the cursor and read the revision, so the parked poll below is
	// genuinely up to date and would wait.
	op := map[string]string{"X-Crew-Request": "1"}
	r.do(t, "POST", "/v1/squads/lab-alpha/order", `{"kind":"hold"}`, op)
	fresh := r.ordersSince(t, "lab-alpha", "0")
	if fresh.ProfileSeq != 0 {
		t.Fatalf("a squad with no profile answers profileSeq %d, want 0", fresh.ProfileSeq)
	}
	rev := fresh.Rev

	type answer struct {
		status int
		d      Delta
	}
	poll := make(chan answer, 1)
	go func() {
		resp := r.do(t, "GET", "/v1/squads/lab-alpha/orders?wait=10s&since=1&rev="+fmt.Sprintf("%d", rev), "", nil)
		var d Delta
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
				panic(err)
			}
		}
		poll <- answer{resp.StatusCode, d}
	}()
	time.Sleep(200 * time.Millisecond)
	if resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile", testProfile, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put: %d", resp.StatusCode)
	}
	select {
	case got := <-poll:
		if got.status != http.StatusOK || got.d.ProfileSeq != 1 {
			t.Errorf("the woken poll answered %d with profileSeq %d, want 200 and 1", got.status, got.d.ProfileSeq)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a stored profile did not wake the wait")
	}
}

// The store refuses a profile for a squad the lab does not run whoever
// calls it, and the profile is gone with the squad: the store is rebuilt
// from hellos, so the trainer renews a profile as a unit renews its
// claim.
func TestSetProfileRefusesUnmarkedAndLapsedSquads(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	if st.SetProfile("alpha", profileWire{Version: contractVersion, Name: "x"}) {
		t.Error("a production squad took a profile at the store")
	}
	cl.hello(st, "crew-19", "lab-alpha", "striker")
	if !st.SetProfile("lab-alpha", profileWire{Version: contractVersion, Name: "x"}) {
		t.Fatal("a lab squad refused a profile")
	}
	if p, ok := st.Profile("lab-alpha"); !ok || p.Name != "x" {
		t.Errorf("the stored profile reads back as %+v, %v", p, ok)
	}
	if _, ok := st.Profile("alpha"); ok {
		t.Error("a production squad serves a profile")
	}
	cl.after(memberTTL + time.Second)
	st.Get("lab-alpha") // a prune tick with nobody left to claim anything
	if _, ok := st.Profile("lab-alpha"); ok {
		t.Error("the profile outlived the squad's last claim")
	}
}

// The wire mirror must carry the contract's tags exactly: the unit side
// reads a profile with the contract package and cannot see this one, so
// a tag renamed there but not here turns every stored profile into a
// zero tune. This pins the tags the round trip depends on.
func TestProfileWireMatchesTheContractTags(t *testing.T) {
	p := profileWire{Version: contractVersion, Name: "n", Parent: "p",
		Default: profileParams{FocusRange: 1, HealBelow: 2, HealSafe: 3, PressRange: 4,
			RegroupWithin: 5, KiteBackoff: 6, ShotEvery: 7, ArrowNear: 8, LineWidth: 9,
			OrbitMin: 10, CritWindow: 11, MeleeShieldTick: 12},
		Overrides: map[string]profileParams{"creeper": {KiteBackoff: 9}}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"version":1`, `"name":"n"`, `"parent":"p"`, `"overrides":{"creeper":`,
		`"focusRange":1`, `"healBelow":2`, `"healSafe":3`, `"pressRange":4`,
		`"regroupWithin":5`, `"kiteBackoff":6`, `"shotEvery":7`, `"arrowNear":8`,
		`"lineWidth":9`, `"orbitMin":10`, `"critWindow":11`, `"meleeShieldTicks":12`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the mirror lost the contract tag %s: %s", want, b)
		}
	}
}

// A trained profile carries its evidence path and tactic variants; the
// route stores and serves both, where a closed field table would have
// refused the trainer's own output.
func TestProfileCarriesEvidenceAndTactics(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-alpha", "crew-18", "vanguard")
	body := strings.Replace(testProfile, `"overrides"`, `"evidence":"runs/7/cards.jsonl","tactics":{"creeper":["creeper/kite"]},"overrides"`, 1)
	if resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile", body, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put: %d", resp.StatusCode)
	}
	got := readBody(t, r.do(t, "GET", "/v1/squads/lab-alpha/profile", "", nil))
	for _, want := range []string{`"evidence":"runs/7/cards.jsonl"`, `"tactics":{"creeper":["creeper/kite"]}`} {
		if !strings.Contains(got, want) {
			t.Errorf("the profile is missing %s: %s", want, got)
		}
	}
	bad := strings.Replace(body, `creeper/kite`, "a\nb", 1)
	if resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile", bad, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a tactic name with a newline: %d, want 400", resp.StatusCode)
	}
}

// A path-valued id that merely starts with the lab prefix is not a lab
// squad: the store and the route both refuse it, so a hello cannot park
// "lab-/../fwb" in the table and then take a profile.
func TestLabSquadIsTheWholeShapeOfTheID(t *testing.T) {
	for _, id := range []string{"lab-alpha", "lab-a-1", "lab-0"} {
		if !labSquad(id) {
			t.Errorf("%q is not a lab squad", id)
		}
	}
	for _, id := range []string{"lab-", "lab-/../fwb", "lab-a/b", "lab-A", "lab-a b", "lab-a\n", "alpha", "xlab-a", "lab-" + strings.Repeat("a", 80)} {
		if labSquad(id) {
			t.Errorf("%q passes as a lab squad", id)
		}
	}
	st := NewStore()
	st.Hello(Hello{Unit: "crew-18", Squad: "lab-/../fwb", Role: roleVanguard})
	if st.SetProfile("lab-/../fwb", profileWire{Version: contractVersion, Name: "x"}) {
		t.Error("the store took a profile for a path-valued id")
	}
}

func TestProfilePUTRefusesAPathValuedSquadID(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-alpha", "crew-18", "vanguard")
	resp := r.do(t, "PUT", "/v1/squads/lab-%2F..%2Ffwb/profile", testProfile, nil)
	if resp.StatusCode < 400 {
		t.Errorf("a path-valued squad id took a profile: %d", resp.StatusCode)
	}
}

func TestProfilePUTChecksFamiliesAndTacticLists(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-alpha", "crew-18", "vanguard")
	many := `"` + strings.Repeat(`spider/ground","`, maxFamilyTactics) + `spider/swarm"`
	tests := []struct {
		name string
		body string
		want int
	}{
		{"every known family", `{"version":1,"name":"x","default":{},"overrides":{"zombie":{},"player":{"kiteBackoff":3}},"tactics":{"spider":["spider/ground","spider/swarm"],"player":["player/axe-shield"],"boss":[]}}`, http.StatusNoContent},
		{"an unknown override family", `{"version":1,"name":"x","default":{},"overrides":{"dragon":{}}}`, http.StatusBadRequest},
		{"an unknown tactics family", `{"version":1,"name":"x","default":{},"tactics":{"dragon":["a/b"]}}`, http.StatusBadRequest},
		{"a path as a family", `{"version":1,"name":"x","default":{},"tactics":{"../x":["a/b"]}}`, http.StatusBadRequest},
		{"too long a list", `{"version":1,"name":"x","default":{},"tactics":{"spider":[` + many + `]}}`, http.StatusBadRequest},
		{"a tactic name with a newline", `{"version":1,"name":"x","default":{},"tactics":{"spider":["a\nb"]}}`, http.StatusBadRequest},
		{"an empty tactic name", `{"version":1,"name":"x","default":{},"tactics":{"spider":[""]}}`, http.StatusBadRequest},
		{"a shouted tactic name", `{"version":1,"name":"x","default":{},"tactics":{"spider":["Spider Ground"]}}`, http.StatusBadRequest},
		{"a list where a string was wanted", `{"version":1,"name":"x","default":{},"tactics":{"spider":"spider/ground"}}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := r.do(t, "PUT", "/v1/squads/lab-alpha/profile", tt.body, nil)
			if resp.StatusCode != tt.want {
				t.Errorf("status %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}
