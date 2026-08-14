// Package state persists which runs this tool has already seen pending
// confirmation, so restarts don't re-announce runs the user has already
// been notified about, and the very first run against an account with
// existing pending confirmations doesn't fire a wall of notifications.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunRecord tracks when a run was first observed pending confirmation.
type RunRecord struct {
	Seen time.Time `json:"seen"`
}

// State is the on-disk dedup record. Runs holds exactly the set of runs
// that were pending confirmation as of the last poll - entries for runs
// that are no longer pending (confirmed, discarded, etc.) are dropped by
// Observe, so this never grows unbounded.
type State struct {
	Runs     map[string]RunRecord `json:"runs"`
	LastPoll time.Time            `json:"last_poll,omitempty"`
}

// New returns an empty, never-yet-polled state.
func New() *State {
	return &State{Runs: map[string]RunRecord{}}
}

// Observe compares the given set of currently pending run IDs against
// what was recorded on the previous poll, updates the state to reflect
// the current set, and returns the subset that are newly pending - i.e.
// worth notifying about.
//
// On the very first poll ever (LastPoll is zero), nothing is reported as
// newly pending: every currently pending run is simply recorded. Without
// this, pointing the tool at an account that already has N runs pending
// confirmation would fire N notifications immediately on startup.
func (s *State) Observe(pendingIDs []string, now time.Time) (newlyPending []string) {
	coldStart := s.LastPoll.IsZero()

	next := make(map[string]RunRecord, len(pendingIDs))
	for _, id := range pendingIDs {
		if rec, known := s.Runs[id]; known {
			next[id] = rec
			continue
		}
		next[id] = RunRecord{Seen: now}
		if !coldStart {
			newlyPending = append(newlyPending, id)
		}
	}

	s.Runs = next
	s.LastPoll = now
	return newlyPending
}

// statePath returns where state.json lives, following the
// $XDG_CONFIG_HOME convention with a ~/.config fallback.
func statePath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "spacelift-notifier", "state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "spacelift-notifier", "state.json"), nil
}

// Load reads the persisted state from disk, returning a fresh empty State
// (not an error) if no state file exists yet.
func Load() (*State, error) {
	path, err := statePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state file %s: %w", path, err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file %s: %w", path, err)
	}
	if s.Runs == nil {
		s.Runs = map[string]RunRecord{}
	}
	return &s, nil
}

// Save writes the state to disk atomically (write to a temp file, then
// rename), so a crash mid-write can never leave a corrupt state file.
func (s *State) Save() error {
	path, err := statePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating state directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temp state file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp state file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("renaming temp state file into place: %w", err)
	}
	return nil
}
