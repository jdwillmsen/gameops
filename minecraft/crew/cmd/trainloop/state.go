package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// version is the schema of the state file; a bump means Load refuses old
// files instead of resuming with a wrong notion of the remaining budget.
const stateVersion = 1

// RoundState is one finished family run of an overnight campaign.
type RoundState struct {
	Family      string `json:"family"`
	RunID       string `json:"runId"`
	Scenario    string `json:"scenario"`
	StopReason  string `json:"stopReason"`
	Generations int    `json:"generations"`
	Fights      int    `json:"fights"`
	Promotions  int    `json:"promotions"`
	Champion    string `json:"champion"`
	ElapsedMs   int64  `json:"elapsedMs"`
}

// State is what survives a crash: the budget the campaign has already spent.
// A restart reads this file and continues with what is left, so the caps are
// never reset by running the command again.
type State struct {
	Version    int          `json:"version"`
	StartedAt  string       `json:"startedAt"`
	ConsumedMs int64        `json:"consumedMs"`
	FightsUsed int          `json:"fightsUsed"`
	Rounds     []RoundState `json:"rounds"`
}

// loadState reads the session file. A missing file starts a fresh campaign; a
// malformed one is an error, since silently ignoring it would reset the caps.
func loadState(path string) (State, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("trainloop: reading %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, false, fmt.Errorf("trainloop: %s: %w", path, err)
	}
	if s.Version != stateVersion {
		return State{}, false, fmt.Errorf("trainloop: %s: state version %d is not %d; move it aside and start a fresh campaign", path, s.Version, stateVersion)
	}
	if s.ConsumedMs < 0 || s.FightsUsed < 0 {
		return State{}, false, fmt.Errorf("trainloop: %s: negative budget", path)
	}
	return s, true, nil
}

// save writes the state file atomically: the temp file is synced and renamed
// into place, so a kill between rounds leaves the old or the new state, never
// a torn one.
func (s State) save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".trainloop-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// stamp is the campaign's start time as stored in the state file.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
