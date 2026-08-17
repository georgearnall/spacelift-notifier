package configdir

import (
	"path/filepath"
	"testing"
)

func TestDir_UsesXDGConfigHomeWhenSet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	want := filepath.Join("/xdg", "spacelift-notifier")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestDir_FallsBackToHomeConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/tester")

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	want := filepath.Join("/home/tester", ".config", "spacelift-notifier")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}
