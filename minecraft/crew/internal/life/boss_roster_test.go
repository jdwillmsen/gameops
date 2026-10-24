package life

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadclient"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
	"github.com/jdwillmsen/gameops/minecraft/internal/squad"
)

// squadPost speaks to crew-control's unit routes as another unit would.
func squadPost(t *testing.T, srvURL, leaf, body string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srvURL+"/v1/squads/"+e2eSquad+"/"+leaf, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e2eToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("%s %s: status %d", leaf, body, resp.StatusCode)
	}
}

// A Striker fights a warden with the real client and the real routes behind
// it. Its squadmates' deaths and the start point reach it only as the
// server's poll answer carries them, and with two down it walks back to the
// start without trading a blow, where with one down it fights on.
func TestSurvivorRegroupsAtTheStartWhenCrewControlReportsTwoDown(t *testing.T) {
	home := mgl32.Vec3{10.5, 1, 40.5}
	for _, tc := range []struct {
		name    string
		dead    []string
		regroup bool
	}{
		{name: "two down", dead: []string{"crew-18", "crew-20"}, regroup: true},
		{name: "one down", dead: []string{"crew-18"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			api := squad.Routes(mux, squad.NewStore(), e2eUnits{"crew-18": true, "crew-19": true, "crew-20": true}, e2eToken)
			api.MountOperator(mux, e2eGate{})
			srv := httptest.NewServer(mux)
			base, err := url.Parse(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(srv.Close)
			t.Cleanup(cancel)

			r, _ := newBossWarrior(t, "striker", arena.Sub(mgl32.Vec3{10, 0, 0}))
			r.setHealth(20)
			c := squadclient.New(base, "crew-19", e2eSquad, "", squadmsg.RoleStriker, e2eToken, e2eLog{})
			c.SetSource(e2eSource(r))
			r.l.SetSquad(c)
			go c.Run(ctx)

			squadPost(t, srv.URL, "hello", `{"unit":"crew-18","role":"vanguard"}`)
			squadPost(t, srv.URL, "hello", `{"unit":"crew-20","role":"ranger"}`)
			for _, u := range []string{"crew-18", "crew-20"} {
				hp := "20"
				for _, d := range tc.dead {
					if d == u {
						hp = "0"
					}
				}
				squadPost(t, srv.URL, "status", `{"unit":"`+u+`","hp":`+hp+`,"food":20,"weapon":"sword","pos":{"x":30,"y":1,"z":40}}`)
			}
			operatorStart(t, srv.URL, `{"pos":{"x":10.5,"y":1,"z":40.5}}`)

			deadline := time.Now().Add(20 * time.Second)
			for {
				_, started := c.Start()
				if started && c.Down() == len(tc.dead) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the client never saw %d down and a start (down %d, start %v)", len(tc.dead), c.Down(), started)
				}
				time.Sleep(25 * time.Millisecond)
			}

			r.run(250)
			at := dist(r.feet(), home) <= regroupWithin
			if at != tc.regroup {
				t.Fatalf("at the start = %v (%.1f blocks off), want %v", at, dist(r.feet(), home), tc.regroup)
			}
			if tc.regroup && len(r.out.attacks()) != 0 {
				t.Error("traded blows on the way back")
			}
		})
	}
}

// e2eGate lets every operator call through, standing in for the console's
// sign-in, which the squad package takes as an interface.
type e2eGate struct{}

func (e2eGate) Require(h http.Handler) http.Handler   { return h }
func (e2eGate) Operator(*http.Request) (string, bool) { return "owner", true }

// operatorStart posts the squad's start point as an operator would.
func operatorStart(t *testing.T, base, body string) {
	t.Helper()
	resp, err := http.Post(base+"/v1/squads/"+e2eSquad+"/start", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("operator start %s: status %d", body, resp.StatusCode)
	}
}
