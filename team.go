package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/georgearnall/spacelift-notifier/internal/teamconfig"
	"golang.org/x/term"
)

// resolveTeamLabels fills in cfg.teamLabels when the caller didn't pass
// --team-label explicitly - that flag is a full manual override and always
// wins outright, without touching the persisted team config at all.
// Otherwise it loads the persisted team name (updating it first if --team
// was given), prompting for and persisting one if none exists yet, then
// derives the owning/collaborator label pair from it.
func resolveTeamLabels(cfg *config, prompt func() (string, error)) error {
	if cfg.teamLabels.userSet {
		return nil
	}

	tc, err := teamconfig.Load()
	if err != nil {
		return fmt.Errorf("loading team config: %w", err)
	}

	if cfg.team != "" {
		tc.Team = cfg.team // --team explicitly (re)configures
	}
	if tc.Team == "" {
		team, err := prompt()
		if err != nil {
			return err
		}
		tc.Team = team
	}
	if err := tc.Save(); err != nil {
		return fmt.Errorf("saving team config: %w", err)
	}

	cfg.teamLabels.values = tc.Labels()
	return nil
}

// promptForTeam interactively asks the user for their Spacelift team name.
func promptForTeam() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("no team configured, and stdin isn't a terminal to prompt for one - " +
			"run interactively once to configure, or pass --team <name> or --team-label")
	}

	fmt.Fprint(os.Stderr, "No team configured. Enter your Spacelift team name (used in folder:owning-team/<name> and folder:collab-team/<name> labels): ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading team name: %w", err)
	}
	team := strings.TrimSpace(line)
	if team == "" {
		return "", fmt.Errorf("team name cannot be empty")
	}
	return team, nil
}
