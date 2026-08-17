package main

import (
	"errors"
	"testing"

	"github.com/georgearnall/spacelift-notifier/internal/teamconfig"
)

func failIfCalled(t *testing.T) func() (string, error) {
	return func() (string, error) {
		t.Fatal("prompt should not have been called")
		return "", nil
	}
}

func TestResolveTeamLabels_ExplicitTeamLabelSkipsConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := config{}
	cfg.teamLabels.values = []string{"folder:owning-team/atlas"}
	cfg.teamLabels.userSet = true

	if err := resolveTeamLabels(&cfg, failIfCalled(t)); err != nil {
		t.Fatalf("resolveTeamLabels() error = %v", err)
	}
	if len(cfg.teamLabels.values) != 1 || cfg.teamLabels.values[0] != "folder:owning-team/atlas" {
		t.Errorf("teamLabels.values = %v, want unchanged explicit override", cfg.teamLabels.values)
	}
}

func TestResolveTeamLabels_LoadsPersistedTeam(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := (&teamconfig.Config{Team: "ecommerce"}).Save(); err != nil {
		t.Fatalf("seeding team config: %v", err)
	}

	cfg := config{}
	if err := resolveTeamLabels(&cfg, failIfCalled(t)); err != nil {
		t.Fatalf("resolveTeamLabels() error = %v", err)
	}

	want := []string{"folder:owning-team/ecommerce", "folder:collab-team/ecommerce"}
	if len(cfg.teamLabels.values) != len(want) {
		t.Fatalf("teamLabels.values = %v, want %v", cfg.teamLabels.values, want)
	}
	for i := range want {
		if cfg.teamLabels.values[i] != want[i] {
			t.Errorf("teamLabels.values[%d] = %q, want %q", i, cfg.teamLabels.values[i], want[i])
		}
	}
}

func TestResolveTeamLabels_PromptsAndPersistsOnFirstRun(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := config{}
	prompted := false
	prompt := func() (string, error) {
		prompted = true
		return "ecommerce", nil
	}

	if err := resolveTeamLabels(&cfg, prompt); err != nil {
		t.Fatalf("resolveTeamLabels() error = %v", err)
	}
	if !prompted {
		t.Error("resolveTeamLabels() did not call prompt when nothing was configured")
	}

	want := []string{"folder:owning-team/ecommerce", "folder:collab-team/ecommerce"}
	if len(cfg.teamLabels.values) != len(want) {
		t.Fatalf("teamLabels.values = %v, want %v", cfg.teamLabels.values, want)
	}

	tc, err := teamconfig.Load()
	if err != nil {
		t.Fatalf("teamconfig.Load() error = %v", err)
	}
	if tc.Team != "ecommerce" {
		t.Errorf("persisted Team = %q, want %q", tc.Team, "ecommerce")
	}
}

func TestResolveTeamLabels_TeamFlagOverwritesPersisted(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := (&teamconfig.Config{Team: "ecommerce"}).Save(); err != nil {
		t.Fatalf("seeding team config: %v", err)
	}

	cfg := config{team: "atlas"}
	if err := resolveTeamLabels(&cfg, failIfCalled(t)); err != nil {
		t.Fatalf("resolveTeamLabels() error = %v", err)
	}

	want := []string{"folder:owning-team/atlas", "folder:collab-team/atlas"}
	if len(cfg.teamLabels.values) != len(want) || cfg.teamLabels.values[0] != want[0] {
		t.Errorf("teamLabels.values = %v, want %v", cfg.teamLabels.values, want)
	}

	tc, err := teamconfig.Load()
	if err != nil {
		t.Fatalf("teamconfig.Load() error = %v", err)
	}
	if tc.Team != "atlas" {
		t.Errorf("persisted Team = %q, want %q (--team should overwrite)", tc.Team, "atlas")
	}
}

func TestResolveTeamLabels_PromptErrorPropagatesAndSavesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := config{}
	wantErr := errors.New("boom")
	prompt := func() (string, error) { return "", wantErr }

	if err := resolveTeamLabels(&cfg, prompt); err != wantErr {
		t.Fatalf("resolveTeamLabels() error = %v, want %v", err, wantErr)
	}

	tc, err := teamconfig.Load()
	if err != nil {
		t.Fatalf("teamconfig.Load() error = %v", err)
	}
	if tc.Team != "" {
		t.Errorf("Team = %q, want nothing persisted after a prompt error", tc.Team)
	}
}

func TestPromptForTeam_NonTerminalStdinReturnsError(t *testing.T) {
	// Under `go test`, stdin is not a terminal, so promptForTeam should
	// return an error rather than blocking on a read that will never come.
	if _, err := promptForTeam(); err == nil {
		t.Error("promptForTeam() on non-terminal stdin: got nil error, want one")
	}
}
