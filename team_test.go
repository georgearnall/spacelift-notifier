package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/georgearnall/spacewatch/internal/teamconfig"
)

func failIfCalled(t *testing.T) func([]string) ([]string, error) {
	return func([]string) ([]string, error) {
		t.Fatal("pick should not have been called")
		return nil, nil
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

	if err := (&teamconfig.Config{Teams: []string{"ecommerce"}}).Save(); err != nil {
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
	prompt := func([]string) ([]string, error) {
		prompted = true
		return []string{"ecommerce"}, nil
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
	if !slices.Equal(tc.Teams, []string{"ecommerce"}) {
		t.Errorf("persisted Teams = %v, want [ecommerce]", tc.Teams)
	}
}

func TestResolveTeamLabels_TeamFlagOverwritesPersisted(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := (&teamconfig.Config{Teams: []string{"ecommerce"}}).Save(); err != nil {
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
	if !slices.Equal(tc.Teams, []string{"atlas"}) {
		t.Errorf("persisted Teams = %v, want [atlas] (--team should overwrite)", tc.Teams)
	}
}

func TestResolveTeamLabels_PromptErrorPropagatesAndSavesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cfg := config{}
	wantErr := errors.New("boom")
	prompt := func([]string) ([]string, error) { return nil, wantErr }

	if err := resolveTeamLabels(&cfg, prompt); err != wantErr {
		t.Fatalf("resolveTeamLabels() error = %v, want %v", err, wantErr)
	}

	tc, err := teamconfig.Load()
	if err != nil {
		t.Fatalf("teamconfig.Load() error = %v", err)
	}
	if len(tc.Teams) != 0 {
		t.Errorf("Teams = %v, want nothing persisted after a prompt error", tc.Teams)
	}
}

func TestResolveTeamLabels_TeamFlagAcceptsList(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg := config{team: "atlas, ecommerce"}
	if err := resolveTeamLabels(&cfg, failIfCalled(t)); err != nil {
		t.Fatalf("resolveTeamLabels() error = %v", err)
	}

	want := []string{
		"folder:owning-team/atlas", "folder:collab-team/atlas",
		"folder:owning-team/ecommerce", "folder:collab-team/ecommerce",
	}
	if !slices.Equal(cfg.teamLabels.values, want) {
		t.Errorf("teamLabels.values = %v, want %v", cfg.teamLabels.values, want)
	}
}

func TestResolveTeamLabels_PickTeamRepicksWithCurrentTeams(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := (&teamconfig.Config{Teams: []string{"ecommerce"}}).Save(); err != nil {
		t.Fatalf("seeding team config: %v", err)
	}

	var gotCurrent []string
	pick := func(current []string) ([]string, error) {
		gotCurrent = current
		return []string{"atlas"}, nil
	}
	cfg := config{pickTeam: true}
	if err := resolveTeamLabels(&cfg, pick); err != nil {
		t.Fatalf("resolveTeamLabels() error = %v", err)
	}
	if !slices.Equal(gotCurrent, []string{"ecommerce"}) {
		t.Errorf("pick got current = %v, want [ecommerce]", gotCurrent)
	}

	tc, err := teamconfig.Load()
	if err != nil {
		t.Fatalf("teamconfig.Load() error = %v", err)
	}
	if !slices.Equal(tc.Teams, []string{"atlas"}) {
		t.Errorf("persisted Teams = %v, want [atlas]", tc.Teams)
	}
}

type nopQueryer struct{}

func (nopQueryer) Query(context.Context, any, map[string]any) error { return nil }

func TestTeamPicker_NonTerminalStdinFailsBeforeBuildingClient(t *testing.T) {
	// Under `go test`, stdin is not a terminal, so the picker should return
	// an error rather than blocking, and before building a client.
	built := false
	newClient := func() (teamQueryer, error) {
		built = true
		return nopQueryer{}, nil
	}
	if _, err := teamPicker(context.Background(), newClient)(nil); err == nil {
		t.Error("teamPicker() on non-terminal stdin: got nil error, want one")
	}
	if built {
		t.Error("teamPicker() built a client, want it to fail before any session setup")
	}
}
