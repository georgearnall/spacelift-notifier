// Package teamconfig persists the caller's Spacelift team name, so it only
// has to be entered once, and derives the exact-match stack labels for both
// the "owning" and "collaborator" label conventions from it.
package teamconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/georgearnall/spacelift-notifier/internal/configdir"
)

// Config is the on-disk team configuration.
type Config struct {
	Team string `json:"team"`
}

// Labels derives the exact-match stack labels that mark a stack as
// belonging to Team, covering both label conventions Spacelift uses: stacks
// the team owns, and stacks the team merely collaborates on. Returns nil if
// no team is configured.
func (c *Config) Labels() []string {
	if c.Team == "" {
		return nil
	}
	return []string{
		"folder:owning-team/" + c.Team,
		"folder:collab-team/" + c.Team,
	}
}

// path returns where config.json lives.
func path() (string, error) {
	dir, err := configdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the persisted team config from disk, returning a fresh
// unconfigured Config (not an error) if no config file exists yet.
func Load() (*Config, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading team config file %s: %w", p, err)
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing team config file %s: %w", p, err)
	}
	return &c, nil
}

// Save writes the team config to disk atomically, so a crash mid-write can
// never leave a corrupt config file.
func (c *Config) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := configdir.WriteJSON(p, c); err != nil {
		return fmt.Errorf("saving team config: %w", err)
	}
	return nil
}
