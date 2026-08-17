// Command spacelift-notifier watches a Spacelift account for stack runs
// awaiting confirmation, filtered to the caller's team and to runs the
// caller actually has permission to confirm. It is read-only: it never
// confirms, discards, or otherwise mutates anything in Spacelift. It only
// surfaces pending runs and prints a clickable link to the stack so the
// user can act on it themselves.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const helpText = `spacelift-notifier - watch Spacelift for stack runs awaiting confirmation

Usage:
  spacelift-notifier [flags]

Flags:
  --team value              Your Spacelift team name. Persisted to config.json
                            and used to watch both folder:owning-team/<value>
                            and folder:collab-team/<value> stack labels.
                            Updates the persisted team if one already exists.
                            If neither this nor --team-label is given and no
                            team is configured yet, you'll be prompted for one
                            (once) on startup.
  --team-label value       Stack label that must match exactly for a stack to be
                            considered yours (repeatable; supplying this flag
                            replaces the default rather than adding to it, and
                            bypasses --team/config.json entirely).
  --interval duration       Poll interval while there are pending confirmations (default 20s)
  --idle-interval duration  Poll interval while there are none (default 60s)
  --request-budget int      Self-imposed cap on API requests per hour (default 300)
  --once                    Run a single poll cycle, print results, and exit
  --no-notify                Suppress desktop notifications
  -h, --help                 Show this help text

spacelift-notifier never calls a Spacelift mutation - it only reads and links.
`

// stringList is a repeatable flag.Value that replaces its default contents
// the first time the user supplies a value, rather than appending to it.
// Without this, "--team-label foo" would silently still match the built-in
// default alongside foo, which is surprising.
type stringList struct {
	values  []string
	userSet bool
}

func (s *stringList) String() string {
	return fmt.Sprintf("%v", s.values)
}

func (s *stringList) Set(v string) error {
	if !s.userSet {
		s.values = nil
		s.userSet = true
	}
	s.values = append(s.values, v)
	return nil
}

type config struct {
	team           string
	teamLabels     stringList
	activeInterval time.Duration
	idleInterval   time.Duration
	requestBudget  int
	once           bool
	noNotify       bool
}

// parseFlags builds the default config and applies args on top of it.
// Factored out from main so flag-parsing behavior (in particular
// stringList's replace-on-first-set semantics) can be exercised directly
// in tests without going through os.Args/os.Exit.
func parseFlags(args []string) (config, error) {
	cfg := config{
		activeInterval: 20 * time.Second,
		idleInterval:   60 * time.Second,
		requestBudget:  300,
	}

	fs := flag.NewFlagSet("spacelift-notifier", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, helpText) }
	fs.StringVar(&cfg.team, "team", "", "your Spacelift team name (persisted; derives folder:owning-team/<name> and folder:collab-team/<name>)")
	fs.Var(&cfg.teamLabels, "team-label", "exact stack label to match as 'your team' (repeatable; bypasses --team/config.json)")
	fs.DurationVar(&cfg.activeInterval, "interval", cfg.activeInterval, "poll interval while runs are pending")
	fs.DurationVar(&cfg.idleInterval, "idle-interval", cfg.idleInterval, "poll interval while idle")
	fs.IntVar(&cfg.requestBudget, "request-budget", cfg.requestBudget, "self-imposed API request budget per hour")
	fs.BoolVar(&cfg.once, "once", false, "run a single poll cycle and exit")
	fs.BoolVar(&cfg.noNotify, "no-notify", false, "suppress desktop notifications")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}

	if err := resolveTeamLabels(&cfg, promptForTeam); err != nil {
		fail("%v", err)
	}

	if cfg.once {
		runOnce(cfg)
		return
	}
	runWatch(cfg)
}
