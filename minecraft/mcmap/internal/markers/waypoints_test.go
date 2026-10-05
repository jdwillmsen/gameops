package markers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const agentToken = "internal-token-0123456789"

func agentServing(t *testing.T, h http.HandlerFunc) *Agent {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewAgent(srv.URL+"/", agentToken)
}

func TestAgent_AsksForOnePlayersWaypointsWithTheInternalToken(t *testing.T) {
	var path, authz string
	a := agentServing(t, func(w http.ResponseWriter, r *http.Request) {
		path, authz = r.URL.Path, r.Header.Get("Authorization")
		fmt.Fprint(w, `{"waypoints":[{"name":"home","x":-12,"y":64,"z":300,"dimension":"overworld"},{"name":"hub","x":1,"y":2,"z":3,"dimension":"nether"}],"more":2}`)
	})
	got, more, err := a.Waypoints(context.Background(), "2535400000000001")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/players/2535400000000001/waypoints" || authz != "Bearer "+agentToken {
		t.Errorf("asked %q with %q", path, authz)
	}
	want := []Waypoint{{"home", -12, 64, 300, "overworld"}, {"hub", 1, 2, 3, "nether"}}
	if !reflect.DeepEqual(got, want) || more != 2 {
		t.Errorf("waypoints = %v, %d more; want %v, 2", got, more, want)
	}
}

func TestAgent_RefusesAnXUIDThatIsNotOne(t *testing.T) {
	called := false
	a := agentServing(t, func(http.ResponseWriter, *http.Request) { called = true })
	for _, xuid := range []string{"", "../1", "1/waypoints?x=", "-1", "abc"} {
		if _, _, err := a.Waypoints(context.Background(), xuid); err == nil {
			t.Errorf("%q was accepted", xuid)
		}
	}
	if called {
		t.Error("the agent was asked")
	}
}

func TestAgent_ChecksWhatTheAgentSends(t *testing.T) {
	var list []Waypoint
	for i := range MaxWaypoints + 3 {
		list = append(list, Waypoint{Name: fmt.Sprintf("w%d", i), Dimension: "overworld"})
	}
	list = append(list,
		Waypoint{Name: "moon", Dimension: "moon"},
		Waypoint{Name: "", Dimension: "end"},
		Waypoint{Name: "far", X: 2_000_000_000, Dimension: "end"},
	)
	list[0].Name = strings.Repeat("n", 500) + "\n"
	a := agentServing(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"waypoints": list, "more": -9})
	})
	got, more, err := a.Waypoints(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxWaypoints || more != 3 {
		t.Errorf("kept %d, %d more; want %d and 3", len(got), more, MaxWaypoints)
	}
	if got[0].Name != strings.Repeat("n", MaxName) {
		t.Errorf("name is %d characters, want it cut to %d", len(got[0].Name), MaxName)
	}
}

func TestAgent_FailsOnAnAnswerItCannotUse(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"refused":  func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusUnauthorized) },
		"not json": func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<html>") },
		"a redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusFound)
		},
		"far too big": func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `{"waypoints":[{"name":"%s"}]}`, strings.Repeat("a", maxWaypointReply))
		},
	} {
		if got, _, err := agentServing(t, h).Waypoints(context.Background(), "1"); err == nil {
			t.Errorf("%s: no error, got %d waypoints", name, len(got))
		}
	}
}

func TestAgent_HoldsOnlySoManyRequestsOpen(t *testing.T) {
	release, entered := make(chan struct{}), make(chan struct{}, maxWaypointCalls)
	a := agentServing(t, func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		fmt.Fprint(w, `{"waypoints":[]}`)
	})
	done := make(chan error, maxWaypointCalls)
	for range maxWaypointCalls {
		go func() {
			_, _, err := a.Waypoints(context.Background(), "1")
			done <- err
		}()
	}
	for range maxWaypointCalls {
		<-entered
	}
	if _, _, err := a.Waypoints(context.Background(), "1"); err != ErrBusy {
		t.Errorf("one more than the limit = %v, want ErrBusy", err)
	}
	close(release)
	for range maxWaypointCalls {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	if _, _, err := a.Waypoints(context.Background(), "1"); err != nil {
		t.Errorf("after the others finished: %v", err)
	}
}
