package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/georgearnall/spacewatch/internal/teamconfig"
	"github.com/georgearnall/spacewatch/internal/teamdiscovery"
	"golang.org/x/term"
)

// resolveTeamLabels fills in cfg.teamLabels when the caller didn't pass
// --team-label explicitly - that flag is a full manual override and always
// wins outright, without touching the persisted team config at all.
// Otherwise it loads the persisted teams (replacing them first if --team
// was given), running pick to choose and persist some if none exist yet or
// --pick-team was given, then derives the owning/collaborator label pairs
// from them. pick receives the currently persisted teams to pre-select.
func resolveTeamLabels(cfg *config, pick func(current []string) ([]string, error)) error {
	if cfg.teamLabels.userSet {
		return nil
	}

	tc, err := teamconfig.Load()
	if err != nil {
		return fmt.Errorf("loading team config: %w", err)
	}

	switch {
	case cfg.team != "":
		tc.Teams = parseTeamList(cfg.team) // --team explicitly (re)configures
	case cfg.pickTeam || len(tc.Teams) == 0:
		teams, err := pick(tc.Teams)
		if err != nil {
			return err
		}
		tc.Teams = teams
	}
	if err := tc.Save(); err != nil {
		return fmt.Errorf("saving team config: %w", err)
	}

	cfg.team = strings.Join(tc.Teams, ", ")
	cfg.teamLabels.values = tc.Labels()
	return nil
}

// teamPicker returns a pick function for resolveTeamLabels that discovers
// the caller's teams through c and lets them choose interactively.
func teamPicker(ctx context.Context, c teamQueryer) func(current []string) ([]string, error) {
	return func(current []string) ([]string, error) {
		// Checked before discovery so a non-interactive run doesn't spend a
		// request on a picker it can't show.
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return nil, errors.New("no team configured, and stdin isn't a terminal to pick one - " +
				"run interactively once to configure, or pass --team <slug>[,<slug>...] or --team-label")
		}

		var note string
		cands, err := teamdiscovery.Discover(ctx, c)
		switch {
		case err != nil:
			note = fmt.Sprintf("Couldn't look up your teams (%v). Type your team slug manually below.", err)
		case len(cands) == 0:
			note = "None of your Spacelift groups match a team label. Type your team slug manually below."
		}
		return pickTeams(cands, current, note)
	}
}

// teamQueryer is the client surface team discovery needs.
type teamQueryer interface {
	Query(ctx context.Context, out any, vars map[string]any) error
}
