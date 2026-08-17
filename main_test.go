package main

import (
	"flag"
	"testing"
	"time"
)

func TestStringList_DefaultReplacedOnFirstSet(t *testing.T) {
	s := &stringList{values: []string{"default-a", "default-b"}}

	if err := s.Set("custom"); err != nil {
		t.Fatal(err)
	}
	if len(s.values) != 1 || s.values[0] != "custom" {
		t.Errorf("values = %v, want the default replaced by [custom]", s.values)
	}
}

func TestStringList_RepeatedFlagAppendsAfterFirstSet(t *testing.T) {
	s := &stringList{values: []string{"default"}}

	if err := s.Set("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b"); err != nil {
		t.Fatal(err)
	}
	if len(s.values) != 2 || s.values[0] != "a" || s.values[1] != "b" {
		t.Errorf("values = %v, want [a b]", s.values)
	}
}

func TestStringList_UntouchedKeepsDefault(t *testing.T) {
	s := &stringList{values: []string{"default"}}
	if got := s.String(); got != "[default]" {
		t.Errorf("String() = %q, want [default]", got)
	}
}

func TestParseFlags_Defaults(t *testing.T) {
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags(nil) error = %v", err)
	}
	if len(cfg.teamLabels.values) != 0 {
		t.Errorf("default team labels = %v, want none (resolved later by resolveTeamLabels)", cfg.teamLabels.values)
	}
	if cfg.team != "" {
		t.Errorf("default team = %q, want empty", cfg.team)
	}
	if cfg.activeInterval != 20*time.Second || cfg.idleInterval != 60*time.Second {
		t.Errorf("default intervals = %v/%v, want 20s/60s", cfg.activeInterval, cfg.idleInterval)
	}
	if cfg.requestBudget != 300 {
		t.Errorf("default request budget = %d, want 300", cfg.requestBudget)
	}
	if cfg.once || cfg.noNotify {
		t.Errorf("once/noNotify should default to false, got once=%v noNotify=%v", cfg.once, cfg.noNotify)
	}
}

func TestParseFlags_OverridesReplaceDefaultTeamLabels(t *testing.T) {
	cfg, err := parseFlags([]string{"--team-label", "folder:owning-team/atlas", "--team-label", "folder:collab-team/ecommerce"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	want := []string{"folder:owning-team/atlas", "folder:collab-team/ecommerce"}
	if len(cfg.teamLabels.values) != len(want) {
		t.Fatalf("teamLabels = %v, want %v", cfg.teamLabels.values, want)
	}
	for i := range want {
		if cfg.teamLabels.values[i] != want[i] {
			t.Errorf("teamLabels[%d] = %q, want %q", i, cfg.teamLabels.values[i], want[i])
		}
	}
}

func TestParseFlags_OnceAndNoNotify(t *testing.T) {
	cfg, err := parseFlags([]string{"--once", "--no-notify", "--request-budget", "50"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if !cfg.once || !cfg.noNotify {
		t.Errorf("once/noNotify = %v/%v, want both true", cfg.once, cfg.noNotify)
	}
	if cfg.requestBudget != 50 {
		t.Errorf("requestBudget = %d, want 50", cfg.requestBudget)
	}
}

func TestParseFlags_HelpReturnsErrHelp(t *testing.T) {
	_, err := parseFlags([]string{"--help"})
	if err != flag.ErrHelp {
		t.Errorf("parseFlags([--help]) error = %v, want flag.ErrHelp", err)
	}
}

func TestParseFlags_UnknownFlagReturnsError(t *testing.T) {
	_, err := parseFlags([]string{"--not-a-real-flag"})
	if err == nil {
		t.Error("parseFlags() with an unknown flag: got nil error, want one")
	}
}
