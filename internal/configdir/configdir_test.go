package configdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDir_UsesXDGConfigHomeWhenSet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	want := filepath.Join("/xdg", "spacewatch")
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
	want := filepath.Join("/home/tester", ".config", "spacewatch")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestWriteJSON_CreatesDirAndLeavesNoTempFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(dir, "state.json")

	if err := WriteJSON(path, map[string]string{"team": "a"}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\n  \"team\": \"a\"\n}" {
		t.Errorf("file contents = %q", data)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want only state.json (temp file left behind?)", len(entries))
	}
}
