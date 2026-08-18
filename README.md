# spacelift-notifier

Watches a Spacelift account for stack runs awaiting confirmation, filtered to
your team's stacks and to runs you actually have permission to confirm.
Prints a clickable terminal link straight to the run so you can go confirm it
in the browser.

Read-only: this tool never calls a Spacelift mutation. It only detects and
links; you do the confirming.

## Installation

```
brew install georgearnall/tap/spacelift-notifier
```

## Usage

```
spacelift-notifier [flags]
```

See `spacelift-notifier --help` for the full flag list. Notably:

- `--team` - your Spacelift team name. Persisted to
  `~/.config/spacelift-notifier/config.json` so you only enter it once, and
  used to watch both label conventions Spacelift has for team ownership:
  stacks your team *owns* (`folder:owning-team/<team>`) and stacks it
  *collaborates on* (`folder:collab-team/<team>`).
- `--team-label` (repeatable) - exact stack label to match instead, for full
  manual control. Bypasses `--team`/`config.json` entirely.
- `--once` - run a single poll cycle, print results, and exit.

If you haven't configured a team yet and don't pass `--team`/`--team-label`,
the tool prompts for your team name interactively the first time it runs,
then remembers it for next time:

```
$ spacelift-notifier --once
No team configured. Enter your Spacelift team name (used in folder:owning-team/<name> and folder:collab-team/<name> labels): ecommerce
```

To change the stored team later, pass `--team` again:

```
spacelift-notifier --team atlas
```

## Keyboard shortcuts (watch mode)

- `↑` / `↓` - move the selection cursor
- `Enter` - open the selected run in your browser (fallback for terminals
  that don't render the clickable OSC 8 links directly in the table)
- `q` / `Ctrl-C` - quit

## Authentication

Uses whatever Spacelift profile is currently selected via the `spacectl` CLI
(`~/.spacelift/config.json`). Run `spacectl whoami` to check which
account/profile is active before running this tool.

## Rate limits

Spacelift's GraphQL API does not expose rate-limit headers, so this tool
tracks and displays its own request count and self-imposed budget
(`--request-budget`, default 300/hour) rather than a server-reported quota.
