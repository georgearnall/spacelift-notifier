package teamconfig

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLabels_EmptyTeamReturnsNil(t *testing.T) {
	c := &Config{}
	if got := c.Labels(); got != nil {
		t.Errorf("Labels() with no team = %v, want nil", got)
	}
}

func TestLabels_DerivesOwningAndCollabLabelsPerTeam(t *testing.T) {
	c := &Config{Teams: []string{"ecommerce", "atlas"}}
	want := []string{
		"folder:owning-team/ecommerce", "folder:collab-team/ecommerce",
		"folder:owning-team/atlas", "folder:collab-team/atlas",
	}

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
	if len(c.Teams) != 0 {
		t.Errorf("Load() on missing file = %+v, want no Teams", c)
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c := &Config{Teams: []string{"ecommerce", "atlas"}}
	if err := c.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !slices.Equal(got.Teams, c.Teams) {
		t.Errorf("Load() Teams = %v, want %v", got.Teams, c.Teams)
	}
}

func TestLoad_ReadsLegacySingleTeam(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, dir, `{"team": "ecommerce"}`)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := []string{"ecommerce"}; !slices.Equal(got.Teams, want) {
		t.Errorf("Load() Teams = %v, want %v", got.Teams, want)
	}
}

func TestLoad_TeamsWinsOverLegacyTeam(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, dir, `{"team": "old", "teams": ["new"]}`)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := []string{"new"}; !slices.Equal(got.Teams, want) {
		t.Errorf("Load() Teams = %v, want %v", got.Teams, want)
	}
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	confDir := filepath.Join(dir, "spacewatch")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_CorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	writeConfig(t, dir, "not json")

	if _, err := Load(); err == nil {
		t.Fatal("Load() on a corrupt config file: got nil error, want one")
	}
}

func TestSave_WritesAtomically(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	c := &Config{Teams: []string{"ecommerce"}}
	if err := c.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "spacewatch", "config-*.json.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover temp files after Save(): %v", matches)
	}
}
