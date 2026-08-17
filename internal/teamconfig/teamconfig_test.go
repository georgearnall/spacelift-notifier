package teamconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLabels_EmptyTeamReturnsNil(t *testing.T) {
	c := &Config{}
	if got := c.Labels(); got != nil {
		t.Errorf("Labels() with no team = %v, want nil", got)
	}
}

func TestLabels_DerivesOwningAndCollabLabels(t *testing.T) {
	c := &Config{Team: "ecommerce"}
	want := []string{"folder:owning-team/ecommerce", "folder:collab-team/ecommerce"}

	got := c.Labels()
	if len(got) != len(want) {
		t.Fatalf("Labels() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Labels()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoad_MissingFileReturnsUnconfigured(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.Team != "" {
		t.Errorf("Load() on missing file = %+v, want empty Team", c)
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c := &Config{Team: "ecommerce"}
	if err := c.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Team != "ecommerce" {
		t.Errorf("Load() Team = %q, want %q", got.Team, "ecommerce")
	}
}

func TestLoad_CorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	confDir := filepath.Join(dir, "spacelift-notifier")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "config.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load() on a corrupt config file: got nil error, want one")
	}
}

func TestSave_WritesAtomically(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c := &Config{Team: "ecommerce"}
	if err := c.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "spacelift-notifier", "config-*.json.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover temp files after Save(): %v", matches)
	}
}
