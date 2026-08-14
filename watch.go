package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/georgearnall/spacelift-notifier/internal/labels"
	"github.com/georgearnall/spacelift-notifier/internal/notify"
	"github.com/georgearnall/spacelift-notifier/internal/pending"
	"github.com/georgearnall/spacelift-notifier/internal/spaceclient"
	"github.com/georgearnall/spacelift-notifier/internal/state"
	"github.com/georgearnall/spacelift-notifier/internal/ui"
)

const (
	ansiAltScreenOn  = "\x1b[?1049h\x1b[H\x1b[?25l"
	ansiAltScreenOff = "\x1b[?25h\x1b[?1049l"

	// ansiClearScreen clears the entire screen and homes the cursor. A
	// full clear (rather than just homing the cursor and overwriting)
	// avoids ghosting: if a new frame is narrower or shorter than the
	// previous one (e.g. the pending count just dropped to zero, or the
	// terminal was resized), simply overwriting from the top leaves
	// stale characters from the old, wider frame visible past the end of
	// the new, shorter lines.
	ansiClearScreen = "\x1b[2J\x1b[H"

	// lowBudgetFloor is the poll delay once the self-imposed request
	// budget for the current hour has been used up.
	lowBudgetFloor = 5 * time.Minute
)

// pollResult holds the outcome of a single poll cycle.
type pollResult struct {
	items               []pending.PendingConfirmation
	newlyPendingIDs     []string
	err                 error
	polledAt            time.Time
	reqTotal, reqWindow int
}

// fail prints an error and exits. Used for startup failures that leave
// nothing sensible to run (e.g. no Spacelift session available).
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "spacelift-notifier: "+format+"\n", args...)
	os.Exit(1)
}

// doPoll runs one poll cycle: query pending confirmations, run them
// through the dedup state to find which are newly pending, and capture
// the request-count stats as of this poll.
func doPoll(ctx context.Context, c *spaceclient.Client, st *state.State, cfg config) pollResult {
	now := time.Now()
	items, err := pending.Poll(ctx, c, labels.Config{Labels: cfg.teamLabels.values})
	res := pollResult{polledAt: now, err: err}
	if err != nil {
		res.reqTotal, res.reqWindow = c.Stats()
		return res
	}

	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.RunID
	}
	res.newlyPendingIDs = st.Observe(ids, now)
	res.items = items
	res.reqTotal, res.reqWindow = c.Stats()
	return res
}

// notifyNewlyPending sends a desktop notification for each run that just
// started pending confirmation this poll cycle.
func notifyNewlyPending(n *notify.Notifier, res pollResult) {
	if len(res.newlyPendingIDs) == 0 {
		return
	}
	byID := make(map[string]pending.PendingConfirmation, len(res.items))
	for _, it := range res.items {
		byID[it.RunID] = it
	}
	for _, id := range res.newlyPendingIDs {
		it, ok := byID[id]
		if !ok {
			continue
		}
		if err := n.PendingConfirmation(it.StackName, it.Title); err != nil {
			fmt.Fprintln(os.Stderr, "spacelift-notifier: notify:", err)
		}
	}
}

// renderRows converts pending confirmations into table rows, marking
// selected as the current keyboard-navigation target (-1 for none).
func renderRows(items []pending.PendingConfirmation, selected int) []ui.Row {
	now := time.Now()
	rows := make([]ui.Row, len(items))
	for i, it := range items {
		team := it.SpaceName
		if team == "" {
			team = it.MatchedLabel
		}
		rows[i] = ui.Row{
			Team:     team,
			Stack:    it.StackName,
			Title:    it.Title,
			Age:      ui.FormatAge(now.Sub(it.CreatedAt)),
			URL:      it.RunURL,
			Selected: i == selected,
		}
	}
	return rows
}

// nextInterval picks the poll delay: the active interval while there are
// pending confirmations, the idle interval otherwise, floored to a longer
// backoff once the self-imposed request budget for this hour is spent.
func nextInterval(cfg config, pendingCount, reqWindow int) time.Duration {
	d := cfg.idleInterval
	if pendingCount > 0 {
		d = cfg.activeInterval
	}
	if cfg.requestBudget > 0 && reqWindow >= cfg.requestBudget && lowBudgetFloor > d {
		d = lowBudgetFloor
	}
	return d
}

func footer(res pollResult, cfg config, quitHint string) string {
	next := nextInterval(cfg, len(res.items), res.reqWindow)
	return fmt.Sprintf("polled %s · %d pending · requests: %d total, %d this hour (budget %d/hr) · next poll in %s%s\n",
		res.polledAt.Format("15:04:05"), len(res.items), res.reqTotal, res.reqWindow, cfg.requestBudget, next.Round(time.Second), quitHint)
}

func runOnce(cfg config) {
	ctx := context.Background()
	c, err := spaceclient.New(ctx)
	if err != nil {
		fail("%v", err)
	}
	st, err := state.Load()
	if err != nil {
		fail("loading state: %v", err)
	}

	res := doPoll(ctx, c, st, cfg)
	if res.err != nil {
		fail("%v", res.err)
	}

	if !cfg.noNotify {
		notifyNewlyPending(notify.New(), res)
	}
	if err := st.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "spacelift-notifier: saving state:", err)
	}

	linksSupported := ui.SupportsLinks()
	colorEnabled := ui.ColorEnabled()
	fmt.Print(ui.RenderTable(renderRows(res.items, -1), linksSupported, colorEnabled))
	fmt.Print(ui.Style(footer(res, cfg, ""), ui.Dim, colorEnabled))
}

func runWatch(cfg config) {
	ctx := context.Background()
	c, err := spaceclient.New(ctx)
	if err != nil {
		fail("%v", err)
	}
	st, err := state.Load()
	if err != nil {
		fail("loading state: %v", err)
	}
	notifier := notify.New()
	linksSupported := ui.SupportsLinks()
	colorEnabled := ui.ColorEnabled()

	fmt.Print(ansiAltScreenOn)
	defer fmt.Print(ansiAltScreenOff)

	done := make(chan struct{})
	keys := readKeys(done)
	resized := watchResize(done)
	defer close(done)

	var (
		last     pollResult
		selected int
	)

	redraw := func() {
		var b strings.Builder
		b.WriteString(ansiClearScreen)
		if last.err != nil {
			b.WriteString(ui.Style(fmt.Sprintf("spacelift-notifier: poll error (retrying): %v\n\n", last.err), ui.BoldRed, colorEnabled))
		}
		b.WriteString(ui.RenderTable(renderRows(last.items, selected), linksSupported, colorEnabled))
		b.WriteString(ui.Style(footer(last, cfg, " · q to quit"), ui.Dim, colorEnabled))
		fmt.Print(b.String())
	}

	poll := func() time.Duration {
		last = doPoll(ctx, c, st, cfg)
		if last.err == nil {
			if !cfg.noNotify {
				notifyNewlyPending(notifier, last)
			}
			if err := st.Save(); err != nil {
				last.err = fmt.Errorf("saving state: %w", err)
			}
		}
		if selected >= len(last.items) {
			selected = len(last.items) - 1
		}
		if selected < 0 {
			selected = 0
		}
		redraw()
		if last.err != nil {
			return cfg.idleInterval // back off on any poll error rather than hammering a failing API
		}
		return nextInterval(cfg, len(last.items), last.reqWindow)
	}

	timer := time.NewTimer(0) // fire immediately for the first poll
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			timer.Reset(poll())
		case k := <-keys:
			switch k {
			case keyQuit:
				return
			case keyUp:
				if selected > 0 {
					selected--
					redraw()
				}
			case keyDown:
				if selected < len(last.items)-1 {
					selected++
					redraw()
				}
			case keyEnter:
				if selected >= 0 && selected < len(last.items) {
					openURL(last.items[selected].RunURL)
				}
			}
		case <-resized:
			redraw()
		}
	}
}

// runOpen launches the OS's "open a URL" command. Overridden in tests so
// openURL's behavior can be verified without actually launching a
// browser.
var runOpen = func(url string) error {
	return exec.Command("open", url).Start()
}

// openURL opens a URL in the user's default browser. Used as the
// keyboard-driven fallback for terminals that don't render OSC 8
// hyperlinks.
func openURL(url string) {
	if err := runOpen(url); err != nil {
		fmt.Fprintln(os.Stderr, "spacelift-notifier: open:", err)
	}
}
