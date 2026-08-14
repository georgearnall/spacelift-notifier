# spacelift-notifier

Watches a Spacelift account for stack runs awaiting confirmation, filtered to
your team's stacks and to runs you actually have permission to confirm.
Prints a clickable terminal link straight to the run so you can go confirm it
in the browser.

Read-only: this tool never calls a Spacelift mutation. It only detects and
links; you do the confirming.

## Usage

```
spacelift-notifier [flags]
```

See `spacelift-notifier --help` for the full flag list. Notably:

- `--team-label` (repeatable) - exact stack label that marks a stack as
  yours. Default: `folder:owning-team/ecommerce`. Supplying this flag
  replaces the default rather than adding to it.
- `--once` - run a single poll cycle, print results, and exit.

Note: Spacelift stacks distinguish stacks your team *owns*
(`folder:owning-team/<team>`) from stacks your team *collaborates on*
(`folder:collab-team/<team>`). These are separate labels - if you want
collaborator stacks to show up too, add both, e.g.:

```
spacelift-notifier --team-label folder:owning-team/ecommerce --team-label folder:collab-team/ecommerce
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
