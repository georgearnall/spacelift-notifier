// Package teamconfig persists the caller's Spacelift team slugs, so they
// only have to be chosen once, and derives the exact-match stack labels for
// both the "owning" and "collaborator" label conventions from them.
package teamconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/georgearnall/spacewatch/internal/configdir"
)

// Config is the on-disk team configuration.
type Config struct {
	Teams []string `json:"teams"`
}

// fileFormat is what's actually read from disk: Teams plus the legacy
// single-team field written by earlier versions, folded into Teams on Load.
type fileFormat struct {
	Teams []string `json:"teams"`
	Team  string   `json:"team"`
}

// Labels derives the exact-match stack labels that mark a stack as
// belonging to one of Teams, covering both label conventions Spacelift
// uses: stacks a team owns, and stacks it merely collaborates on. Returns
// nil if no team is configured.
func (c *Config) Labels() []string {
	var out []string
	for _, t := range c.Teams {
		out = append(out, "folder:owning-team/"+t, "folder:collab-team/"+t)
	}
	return out
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

	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing team config file %s: %w", p, err)
	}
	if len(f.Teams) == 0 && f.Team != "" {
		f.Teams = []string{f.Team}
	}
	return &Config{Teams: f.Teams}, nil
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
