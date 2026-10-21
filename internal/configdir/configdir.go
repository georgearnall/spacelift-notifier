// Package configdir locates the directory spacewatch uses for all
// of its on-disk files (state, team config, ...), so that convention is
// defined in exactly one place.
package configdir

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Dir returns the base directory for spacewatch's on-disk files,
// following the $XDG_CONFIG_HOME convention with a ~/.config fallback.
func Dir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "spacewatch"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "spacewatch"), nil
}

// WriteJSON writes v as indented JSON to path atomically (write to a temp
// file in the same directory, then rename), creating the directory if
// needed, so a crash mid-write can never leave a corrupt file.
func WriteJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", filepath.Base(path), err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("renaming temp file into place: %w", err)
	}
	return nil
}
