package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
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

// pollResult holds the outcome of a single poll cycle, plus any outcome of
// a relogin attempt the user triggered in response to it (reloginErr is
// not itself part of polling - it's carried on the same struct because
// runWatch keeps only a single "last" pollResult as its display state).
type pollResult struct {
	items               []pending.PendingConfirmation
	newlyPendingIDs     []string
	err                 error
	authExpired         bool
	reloginErr          error
	polledAt            time.Time
	reqTotal, reqWindow int
}

// isUnauthorizedErr reports whether err is the SDK's session-expired /
// logged-out error, as opposed to a permission error on an otherwise-valid
// session.
//
// Matching is necessarily fuzzy: the vendored client returns plain
// fmt.Errorf strings, not a typed/status-coded error, and the exact text
// varies by path. determineClientError's GraphQL path only recognizes an
// underlying error as auth-related at all via a *lowercase*
// strings.Contains(err.Error(), "unauthorized") check - a raw 401 from the
// spacelift-io/graphql client actually surfaces as e.g. "non-200 OK status
// code: 401 Unauthorized ...", capital U, which fails that check and
// passes the raw message straight through untouched. Once
// determineClientError *does* recognize it, it produces one of two
// messages: a permission problem on an otherwise-valid session
// ("unauthorized: You're logged in. Maybe you don't have access..."), or
// an actually-expired session ("unauthorized: You can re-login using
// `spacectl profile login`" - client.Do's raw-HTTP path uses the same
// wording, lowercase). Only the last of these is something relogin can
// fix, so this matches "unauthorized" case-insensitively (to catch the
// pass-through capital-U case too) while explicitly excluding the
// permission-denied wording.
func isUnauthorizedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unauthorized") && !strings.Contains(msg, "don't have access")
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
	res := pollResult{polledAt: now, err: err, authExpired: isUnauthorizedErr(err)}
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

// crlf normalizes line breaks to "\r\n" for output written while the tty
// is in raw mode (see redraw's call site for why). It only inserts "\r"
// before a "\n" that doesn't already have one, so it's safe to call on
// text that might already contain a correct "\r\n".
func crlf(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' && (i == 0 || s[i-1] != '\r') {
			b.WriteByte('\r')
		}
		b.WriteByte(s[i])
	}
	return b.String()
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
	keys, restoreTerminal := readKeys(done)
	// Deferred here at the top level rather than left to readKeys' own
	// goroutine (which spends its life blocked on a read syscall that
	// can't be interrupted) - see readKeys' doc comment. This runs on
	// every return from runWatch, including panics.
	defer restoreTerminal()
	resized := watchResize(done)
	defer close(done)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	var (
		last     pollResult
		selected int
	)

	redraw := func() {
		var b strings.Builder
		b.WriteString(ansiClearScreen)
		switch {
		case last.reloginErr != nil:
			b.WriteString(ui.Style(fmt.Sprintf("spacelift-notifier: relogin failed: %v (press l to retry)\n\n", last.reloginErr), ui.BoldRed, colorEnabled))
		case last.authExpired:
			b.WriteString(ui.Style("spacelift-notifier: session expired - press l to relogin\n\n", ui.BoldRed, colorEnabled))
		case last.err != nil:
			b.WriteString(ui.Style(fmt.Sprintf("spacelift-notifier: poll error (retrying): %v\n\n", last.err), ui.BoldRed, colorEnabled))
		}
		b.WriteString(ui.RenderTable(renderRows(last.items, selected), linksSupported, colorEnabled))
		quitHint := " · q to quit"
		if last.authExpired {
			quitHint = " · l to relogin" + quitHint
		}
		b.WriteString(ui.Style(footer(last, cfg, quitHint), ui.Dim, colorEnabled))
		// readKeys puts the tty into raw mode, which on Unix also clears
		// the OPOST output flag for the whole tty (stdin/stdout share
		// one underlying device) - without it, a bare "\n" no longer
		// implies a carriage return, so every line after the first
		// continues from whatever column the previous line ended at
		// instead of resetting to column 0. Normalize to "\r\n" so this
		// renders correctly regardless of OPOST state.
		fmt.Print(crlf(b.String()))
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
			case keyRelogin:
				var resetTimer, handled bool
				last, resetTimer, handled = applyReloginKey(last, func() error {
					return relogin(ctx, spaceclient.ReloginSupported, c.Reauth, restoreTerminal)
				})
				if resetTimer {
					timer.Reset(0) // poll again immediately with the fresh session
				}
				if handled {
					redraw()
				}
			}
		case <-resized:
			redraw()
		case <-sig:
			return // let the deferred restoreTerminal/ansiAltScreenOff run before exiting
		}
	}
}

// applyReloginKey handles a keyRelogin press against the current poll
// state, and is the extracted, directly-testable form of the state
// transition the watch loop's select case applies inline (the loop itself
// can't be unit tested: it closes over per-iteration locals like c, timer
// and ctx). If the last poll didn't detect an expired session, this is a
// no-op - handled is false, and result/resetTimer are the input
// unchanged. Otherwise it calls relogin (a closure the caller builds
// around the real relogin function, ctx, and the current client/terminal
// state) and returns the updated pollResult - reloginErr set on failure
// so the banner explains what went wrong while still offering a retry
// (authExpired is left true either way), or cleared on success, alongside
// whether the caller should trigger an immediate re-poll.
func applyReloginKey(last pollResult, relogin func() error) (result pollResult, resetTimer, handled bool) {
	if !last.authExpired {
		return last, false, false
	}
	if err := relogin(); err != nil {
		last.reloginErr = err
		return last, false, true
	}
	last.reloginErr = nil
	return last, true, true
}

// relogin pauses the TUI, runs `spacectl profile login` interactively so
// the user can complete the browser-based re-auth flow, then calls reauth
// (normally (*spaceclient.Client).Reauth) to rebuild the SDK session from
// the now-refreshed profile in place - preserving that client's
// request-count/budget accounting, unlike building a brand new Client
// would. reauth is a parameter (rather than calling the method directly)
// so tests can exercise relogin's control flow without a real Spacelift
// profile on disk.
func relogin(ctx context.Context, checkSupported func() error, reauth func(context.Context) error, restoreTerminal func()) error {
	// Checked before touching the terminal at all: spacectl's no-argument
	// `profile login` only works for a profile whose stored credentials
	// are already a browser-issued API Token, and does nothing to help an
	// environment-variable-authenticated session (see
	// spaceclient.ReloginSupported) - so there's no point pausing the TUI
	// for a command that's guaranteed to fail.
	if err := checkSupported(); err != nil {
		return err
	}

	fmt.Print(ansiAltScreenOff)
	restoreTerminal()
	defer func() {
		reenterRawMode()
		fmt.Print(ansiAltScreenOn)
	}()

	fmt.Println("spacelift-notifier: running `spacectl profile login`...")
	if err := runSpacectlLogin(); err != nil {
		return fmt.Errorf("spacectl profile login: %w", err)
	}
	return reauth(ctx)
}

// runSpacectlLogin shells out to the real spacectl binary to run its
// interactive, browser-based re-auth flow (spacectl's login internals live
// in an internal/ package of that module and can't be called directly).
// Stdin is deliberately left unset: the login flow needs no keyboard
// input, and wiring up stdin here would race with the key-reader goroutine
// that's permanently blocked reading os.Stdin (see readKeys' doc comment).
//
// The browser-callback wait can take up to spacectl's own 2-minute
// timeout, during which runWatch's select loop is blocked inside this
// call and can't act on a queued Ctrl-C/SIGTERM itself. To stay
// responsive, this installs its own signal watch for the duration of the
// subprocess and kills it on an interrupt rather than leaving the tool
// (and the orphaned subprocess) stuck until spacectl's own timeout
// elapses. signal.Notify supports multiple simultaneous listeners for the
// same signal, so this doesn't steal the delivery runWatch's own signal
// channel is waiting on - that channel still receives its own copy and
// fires normally once this call returns.
//
// Overridden in tests so relogin's control flow can be exercised without
// actually shelling out.
var runSpacectlLogin = func() error {
	cmd := spacectlLoginCommand()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Registered before Start so there's no window in which an interrupt
	// arriving right after the process starts could be missed.
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	afterSignalRegistered()

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-interrupt:
		_ = cmd.Process.Kill()
		<-done // reap the process so it doesn't linger
		return errors.New("interrupted")
	}
}

// spacectlLoginCommand builds the command runSpacectlLogin runs. Indirected
// through a var so tests can substitute a short-lived stand-in process
// instead of actually shelling out to spacectl.
var spacectlLoginCommand = func() *exec.Cmd {
	return exec.Command("spacectl", "profile", "login")
}

// afterSignalRegistered is called the instant runSpacectlLogin's interrupt
// handler is installed. It exists purely so a test can synchronize on
// that registration instead of guessing with a sleep before delivering a
// signal to itself - a race that could otherwise deliver the signal
// before anything is listening for it, falling back to the process's
// default disposition (i.e. terminating the test binary) rather than
// exercising the child-kill path. No-op in production.
var afterSignalRegistered = func() {}

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
