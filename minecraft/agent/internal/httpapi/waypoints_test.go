package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/waypoints"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

const mapToken = "internal-token-0123456789"

// fakeWaypoints holds waypoints by XUID, as the table does.
type fakeWaypoints struct {
	by       map[string][]waypoints.Waypoint
	asked    []string
	err      error
	disabled bool
}

func (f *fakeWaypoints) List(_ context.Context, xuid string) ([]waypoints.Waypoint, error) {
	f.asked = append(f.asked, xuid)
	return f.by[xuid], f.err
}

func (f *fakeWaypoints) Enabled() bool { return !f.disabled }

func waypointServer(t *testing.T, store *fakeWaypoints) string {
	t.Helper()
	s, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if !s.MountWaypoints(mapToken, store, logging.New("error")) {
		t.Fatal("the waypoint route was not mounted")
	}
	go func() { _ = s.ListenAndServe() }()
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return "http://" + s.Addr().String()
}

func getWaypoints(t *testing.T, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

var twoPlayers = map[string][]waypoints.Waypoint{
	"2535400000000001": {{Name: "home", X: -12, Y: 64, Z: 300, Dimension: "overworld"}, {Name: "hub", X: 1, Y: 2, Z: 3, Dimension: "nether"}},
	"2535400000000002": {{Name: "vault", X: 7000, Y: 12, Z: -7000, Dimension: "end"}},
}

func TestWaypoints_ServesThePlayerNamedAndNobodyElse(t *testing.T) {
	store := &fakeWaypoints{by: twoPlayers}
	base := waypointServer(t, store)

	status, body := getWaypoints(t, base+"/v1/players/2535400000000001/waypoints", mapToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var got waypointsResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	want := []waypointJSON{{"home", -12, 64, 300, "overworld"}, {"hub", 1, 2, 3, "nether"}}
	if len(got.Waypoints) != 2 || got.Waypoints[0] != want[0] || got.Waypoints[1] != want[1] || got.More != 0 {
		t.Errorf("waypoints = %+v", got)
	}
	if strings.Contains(body, "vault") {
		t.Errorf("another player's waypoint was sent: %s", body)
	}

	// A player with none is an empty list, not an absent one.
	if status, body := getWaypoints(t, base+"/v1/players/99/waypoints", mapToken); status != http.StatusOK || strings.TrimSpace(body) != `{"waypoints":[],"more":0}` {
		t.Errorf("a player with none = %d %s", status, body)
	}
	if want := "2535400000000001,99"; strings.Join(store.asked, ",") != want {
		t.Errorf("the store was asked for %v, want %s", store.asked, want)
	}
}

func TestWaypoints_AreForTheMapOnly(t *testing.T) {
	store := &fakeWaypoints{by: twoPlayers}
	base := waypointServer(t, store)
	for name, token := range map[string]string{"no token": "", "a wrong token": "internal-token-9876543210", "a prefix of it": mapToken[:10]} {
		status, body := getWaypoints(t, base+"/v1/players/2535400000000001/waypoints", token)
		if status != http.StatusUnauthorized || strings.Contains(body, "home") {
			t.Errorf("with %s = %d %s, want 401 and nothing", name, status, body)
		}
	}
	// Refused before the path is looked at, so the answer does not say
	// whether the XUID was one.
	if status, _ := getWaypoints(t, base+"/v1/players/not-a-number/waypoints", ""); status != http.StatusUnauthorized {
		t.Errorf("a bad XUID with no token = %d, want 401", status)
	}
	if len(store.asked) != 0 {
		t.Errorf("the store was read for %v without the token", store.asked)
	}
}

func TestWaypoints_RefusesWhatIsNotAnXUID(t *testing.T) {
	store := &fakeWaypoints{by: twoPlayers}
	base := waypointServer(t, store)
	for _, xuid := range []string{"steve", "-1", "1.5", "%2e%2e", strings.Repeat("9", 21), "1%20OR%201=1"} {
		if status, _ := getWaypoints(t, base+"/v1/players/"+xuid+"/waypoints", mapToken); status != http.StatusBadRequest {
			t.Errorf("xuid %q = %d, want 400", xuid, status)
		}
	}
	if len(store.asked) != 0 {
		t.Errorf("the store was read for %v", store.asked)
	}
}

func TestWaypoints_SendsNoMoreThanTheCap(t *testing.T) {
	var many []waypoints.Waypoint
	for i := range maxWaypointsServed + 7 {
		many = append(many, waypoints.Waypoint{Name: fmt.Sprintf("w%04d", i), Dimension: "overworld"})
	}
	base := waypointServer(t, &fakeWaypoints{by: map[string][]waypoints.Waypoint{"1": many}})
	_, body := getWaypoints(t, base+"/v1/players/1/waypoints", mapToken)
	var got waypointsResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Waypoints) != maxWaypointsServed || got.More != 7 {
		t.Errorf("sent %d with %d more, want %d and 7", len(got.Waypoints), got.More, maxWaypointsServed)
	}
}

func TestWaypoints_AStoreFailureSaysNothingOfTheDatabase(t *testing.T) {
	base := waypointServer(t, &fakeWaypoints{err: errors.New("pq: password authentication failed for user agent")})
	status, body := getWaypoints(t, base+"/v1/players/1/waypoints", mapToken)
	if status != http.StatusServiceUnavailable || strings.Contains(body, "password") {
		t.Errorf("a failed read = %d %s", status, body)
	}
}

func TestWaypoints_UnmountedWithoutATokenOrAStore(t *testing.T) {
	s, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	log := logging.New("error")
	if s.MountWaypoints("", &fakeWaypoints{}, log) || s.MountWaypoints(mapToken, &fakeWaypoints{disabled: true}, log) || s.MountWaypoints(mapToken, nil, log) {
		t.Error("mounted with nothing to authenticate or nothing to serve")
	}
}

type presenceStub struct{}

func (presenceStub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusTeapot)
}
func (presenceStub) Enabled() bool { return true }

// The presence API owns everything else under /v1/. The two are mounted on
// one mux and neither may take the other's requests.
func TestWaypoints_ShareV1WithThePresenceAPI(t *testing.T) {
	s, err := New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if !s.MountPresence(presenceStub{}) || !s.MountWaypoints(mapToken, &fakeWaypoints{by: twoPlayers}, logging.New("error")) {
		t.Fatal("not mounted")
	}
	go func() { _ = s.ListenAndServe() }()
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	base := "http://" + s.Addr().String()
	if status, _ := getWaypoints(t, base+"/v1/players/2535400000000002/waypoints", mapToken); status != http.StatusOK {
		t.Errorf("waypoints = %d, want 200", status)
	}
	if status, _ := getWaypoints(t, base+"/v1/actors", mapToken); status != http.StatusTeapot {
		t.Errorf("presence route = %d, want the presence API's answer", status)
	}
}
