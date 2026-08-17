// Package configdir locates the directory spacelift-notifier uses for all
// of its on-disk files (state, team config, ...), so that convention is
// defined in exactly one place.
package configdir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir returns the base directory for spacelift-notifier's on-disk files,
// following the $XDG_CONFIG_HOME convention with a ~/.config fallback.
func Dir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "spacelift-notifier"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "spacelift-notifier"), nil
}
