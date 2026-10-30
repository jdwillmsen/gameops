package squadclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Duel states, as the squad service carries them.
const (
	DuelIdle  = "idle"
	DuelArmed = "armed"
	DuelGo    = "go"
	DuelOver  = "over"
)

// Duel is what the squad says about the fight a unit is in: its own side,
// the side it may hunt, and who is already out. It mirrors the squad
// service's duel shape, which this package cannot import.
type Duel struct {
	State      string   `json:"state"`
	Round      string   `json:"round,omitempty"`
	Fighters   []string `json:"fighters,omitempty"`
	Enemies    []string `json:"enemies,omitempty"`
	Eliminated []string `json:"eliminated,omitempty"`
	// Crew is the in-game names the squad service vouches for as lab crew
	// units. Enemies outside it are not hunted, whatever the duel says.
	Crew []string `json:"crew,omitempty"`
}

// FetchDuel returns the squad's current duel. One fetch serves every call
// inside duelEvery, and a duel revision the poll has moved earns one fetch
// at once. ok is false while the squad serves none and whenever the read
// fails: unlike a profile, the last good answer never serves past its
// interval, so a unit that cannot hear the squad stops hunting.
func (c *Client) FetchDuel(ctx context.Context) (Duel, bool) {
	seq := c.streamDuelSeq()
	c.duelMu.Lock()
	defer c.duelMu.Unlock()
	moved := seq != c.duelTrySeq
	if !moved && seq == c.duelFrom && time.Since(c.duelAt) < c.duelEvery {
		return c.duel, c.hasDuel
	}
	if !moved && time.Since(c.duelTry) < c.duelEvery {
		// The last attempt failed a moment ago; wait out the interval
		// rather than hammer a server that is down.
		return Duel{}, false
	}
	c.duelTry, c.duelTrySeq = time.Now(), seq
	d, ok, err := c.fetchDuel(ctx)
	if err != nil {
		c.log.Error("squad_duel_failed", map[string]any{"error": err.Error()})
		c.duel, c.hasDuel, c.duelAt, c.duelFrom = Duel{}, false, time.Time{}, 0
		return Duel{}, false
	}
	c.duel, c.hasDuel = d, ok
	c.duelAt, c.duelFrom = c.duelTry, seq
	return d, ok
}

func (c *Client) streamDuelSeq() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.duelSeq
}

// fetchDuel reads the duel route once. A 404 is an answer: no duel.
func (c *Client) fetchDuel(ctx context.Context) (Duel, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, postTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.base.JoinPath("v1", "squads", c.squad, "duel").String(), nil)
	if err != nil {
		return Duel{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return Duel{}, false, err
	}
	defer closeBody(resp)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return Duel{}, false, nil
	case http.StatusOK:
		var d Duel
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxAnswerByte)).Decode(&d); err != nil {
			return Duel{}, false, fmt.Errorf("squad duel: %w", err)
		}
		return d, true, nil
	default:
		return Duel{}, false, fmt.Errorf("squad duel: status %d", resp.StatusCode)
	}
}
