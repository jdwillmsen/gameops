package markers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

const (
	// MaxWaypoints is the most of one player's waypoints sent to their
	// browser.
	MaxWaypoints = 500
	// maxWaypointReply is the most of the agent's answer that is read:
	// room for MaxWaypoints with the longest names and nothing more.
	maxWaypointReply = 512 << 10
	// waypointTimeout is how long the agent has to answer. The page asks
	// again by itself, so a slow answer is given up rather than waited on.
	waypointTimeout = 3 * time.Second
	// maxWaypointCalls is how many requests to the agent may be open at
	// once. Any logged-in browser can cause one, and each is a database
	// query on the agent.
	maxWaypointCalls = 4
)

// ErrBusy means too many waypoint requests are already open.
var ErrBusy = errors.New("too many waypoint requests in flight")

// Waypoint is one of a player's own named coordinates.
type Waypoint struct {
	Name      string `json:"name"`
	X         int32  `json:"x"`
	Y         int32  `json:"y"`
	Z         int32  `json:"z"`
	Dimension string `json:"dimension"`
}

// Agent reads waypoints from the server agent, which is where players save
// them. The agent already holds this service's internal token, to report
// logins; the same token, presented the other way, is how it knows this
// request is the map's.
type Agent struct {
	url   string
	token string
	http  *http.Client
	slots chan struct{}
}

func NewAgent(url, token string) *Agent {
	return &Agent{
		url: strings.TrimRight(url, "/"), token: token,
		http: &http.Client{
			// The agent never redirects, and following one would send the
			// token to wherever it pointed.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		slots: make(chan struct{}, maxWaypointCalls),
	}
}

// Waypoints is the waypoints of the player with this XUID and nobody
// else's, and how many more they have than are returned.
func (a *Agent) Waypoints(ctx context.Context, xuid string) ([]Waypoint, int, error) {
	// The XUID becomes part of a path.
	if !auth.IsXUID(xuid) {
		return nil, 0, errors.New("waypoints: not a player's XUID")
	}
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		return nil, 0, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, waypointTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url+"/v1/players/"+xuid+"/waypoints", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("waypoints: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("waypoints: the agent answered %d", resp.StatusCode)
	}
	var reply struct {
		Waypoints []Waypoint `json:"waypoints"`
		More      int        `json:"more"`
	}
	// An answer cut off at the limit fails to decode, which is the right
	// outcome for one that large.
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxWaypointReply)).Decode(&reply); err != nil {
		return nil, 0, fmt.Errorf("waypoints: %w", err)
	}

	// The agent is trusted with who the player is, not with what fits on
	// a map: every waypoint is checked again here.
	out, more := make([]Waypoint, 0, min(len(reply.Waypoints), MaxWaypoints)), max(reply.More, 0)
	for _, w := range reply.Waypoints {
		w.Name = CleanName(w.Name)
		if w.Name == "" || !knownDimension(w.Dimension) || !inWorld(float64(w.X)) || !inWorld(float64(w.Y)) || !inWorld(float64(w.Z)) {
			continue
		}
		if len(out) == MaxWaypoints {
			more++
			continue
		}
		out = append(out, w)
	}
	return out, more, nil
}

func knownDimension(name string) bool {
	return slices.ContainsFunc(chunks.Dimensions, func(d chunks.Dimension) bool { return d.Name() == name })
}
