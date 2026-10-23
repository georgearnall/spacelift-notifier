# spacewatch

A watcher for [Spacelift](https://spacelift.io): watches your account for
stack runs awaiting confirmation, filtered to your team's stacks and to runs
you actually have permission to confirm. Prints a clickable terminal link
straight to the run so you can go confirm it in the browser.

Read-only: this tool never calls a Spacelift mutation. It only detects and
links; you do the confirming.

## Installation

```
brew install georgearnall/tap/spacewatch
```

If Homebrew refuses with `Refusing to load formula ... from untrusted tap`,
trust the tap first: `brew trust georgearnall/tap`.

On macOS, install `terminal-notifier` (`brew install terminal-notifier`) so
clicking a notification opens the run. Without it, notifications still
appear but don't link anywhere. On Linux, desktop notifications use
`notify-send` and opening runs uses `xdg-open`, unless your terminal
supports OSC 9 notifications.

## Usage

```
spacewatch [flags]
```

See `spacewatch --help` for the full flag list. Notably:

- `--team` - comma-separated Spacelift team slugs, e.g. `--team atlas,ecommerce`.
  Persisted to `~/.config/spacewatch/config.json` (replacing whatever was
  stored) and used to watch both label conventions Spacelift has for team
  ownership: stacks a team *owns* (`folder:owning-team/<slug>`) and stacks
  it *collaborates on* (`folder:collab-team/<slug>`).
- `--pick-team` - open the team picker again to change the stored teams.
- `--team-label` (repeatable) - exact stack label to match instead, for full
  manual control. Bypasses `--team`/`config.json` entirely.
- `--all` - show every pending run on your team's stacks. By default only
  runs from your own commits (or that you triggered) are shown and notified.
- `--remind` - re-notify about a run still pending this long after the last
  notification (default `10m`, `0` disables).
- `--once` - run a single poll cycle, print results, and exit.
- `--version` - print the version.

If you haven't configured a team yet and don't pass `--team`/`--team-label`,
the tool shows a picker the first time it runs, then remembers your choice.
It lists your Spacelift identity-provider groups that match a team label on
at least one stack, with the matching slugs and stack counts:

```
$ spacewatch --once
Select the Spacelift teams to watch (↑/↓ move, space toggle, enter confirm, q quit)
> [ ] AgentTools  (agenttools, agent-tools · 115 stacks)
  [x] New Verticals & Insurance  (new-verticals-insurance · 43 stacks)
  [ ] Hydra  (hydra · 2 stacks)
  + Type team slugs manually...
```

Groups are matched to labels ignoring case and punctuation, so one group can
cover several slug spellings. If your team is missing or matched wrongly,
choose the last row to type slugs in yourself.

To change the stored teams later, run `spacewatch --pick-team`, or set them
directly:

```
spacewatch --team atlas
```

## Keyboard shortcuts (watch mode)

- `↑` / `↓` - move the selection cursor
- `Enter` - open the selected run in your browser (fallback for terminals
  that don't render the clickable OSC 8 links directly in the table)
- `a` - toggle between your runs and all team runs
- `r` - poll now
- `?` - open/close the config pane (settings and request budget usage)
- `l` - relogin via `spacectl profile login` (only offered once a poll
  detects your session has expired; only works for a browser-login profile
  - not environment-variable credentials or an API key/GitHub token
  profile, which `spacectl profile login` can't refresh non-interactively)
- `q` / `Ctrl-C` - quit

## Authentication

Uses whatever Spacelift profile is currently selected via the `spacectl` CLI
(`~/.spacelift/config.json`). Run `spacectl whoami` to check which
account/profile is active before running this tool.

## Rate limits

Spacelift's GraphQL API does not expose rate-limit headers, so this tool
tracks and displays its own request count and self-imposed budget
(`--request-budget`, default 300/hour) rather than a server-reported quota.
Both are shown in the `?` pane.

## Releasing

Push a semver tag (`git tag v1.2.3 && git push origin v1.2.3`). The release
workflow runs the tests, builds darwin/linux binaries with GoReleaser, attaches
them to a GitHub release and pushes an updated cask to
[georgearnall/homebrew-tap](https://github.com/georgearnall/homebrew-tap).
It needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret with contents write access to
that tap repo.
