package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/georgearnall/spacewatch/internal/labels"
	"github.com/georgearnall/spacewatch/internal/notify"
	"github.com/georgearnall/spacewatch/internal/pending"
	"github.com/georgearnall/spacewatch/internal/spaceclient"
	"github.com/georgearnall/spacewatch/internal/state"
	"github.com/georgearnall/spacewatch/internal/ui"
)

const (
	ansiAltScreenOn  = "\x1b[?1049h\x1b[H\x1b[?25l"
	ansiAltScreenOff = "\x1b[?25h\x1b[?1049l"

	// ansiClearScreen clears the entire screen and homes the cursor. Only
	// used after a resize, when lines may have wrapped differently; normal
	// redraws overwrite in place (see frame) so the once-a-second
	// countdown doesn't flicker.
	ansiClearScreen = "\x1b[2J\x1b[H"
	ansiHome        = "\x1b[H"
	ansiClearEOL    = "\x1b[K"
	ansiClearBelow  = "\x1b[J"

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
	remindIDs           []string // still pending and due a reminder
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
	fmt.Fprintf(os.Stderr, "spacewatch: "+format+"\n", args...)
	os.Exit(1)
}

// doPoll runs one poll cycle: query pending confirmations, run them
// through the dedup state to find which are newly pending, and capture
// the request-count stats as of this poll.
//
// viewer caches the authenticated user's login; if it's empty it is
// looked up first, and a failure is reported like any other poll error
// (so an expired session still offers relogin).
func doPoll(ctx context.Context, c *spaceclient.Client, st *state.State, cfg config, viewer *string) pollResult {
	now := time.Now()
	if *viewer == "" {
		id, err := c.ViewerID(ctx)
		if err != nil {
			res := pollResult{polledAt: now, err: fmt.Errorf("identifying current user: %w", err), authExpired: isUnauthorizedErr(err)}
			res.reqTotal, res.reqWindow = c.Stats()
			return res
		}
		*viewer = id
	}
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
	res.remindIDs = st.DueReminders(now, cfg.remind)
	res.items = items
	res.reqTotal, res.reqWindow = c.Stats()
	return res
}

// visibleItems returns the runs shown in the current view: all of them
// when showAll is set, otherwise only the viewer's own.
func visibleItems(items []pending.PendingConfirmation, viewer string, showAll bool) []pending.PendingConfirmation {
	if showAll {
		return items
	}
	var mine []pending.PendingConfirmation
	for _, it := range items {
		if it.IsMine(viewer) {
			mine = append(mine, it)
		}
	}
	return mine
}

// renderTable renders the visible runs, with an empty-state message that
// says which view is active and how to switch (toggleHint, e.g. "a" or
// "--all").
func renderTable(items []pending.PendingConfirmation, showAll bool, toggleHint string, selected int, linksSupported, colorEnabled bool) string {
	if len(items) == 0 && !showAll {
		return fmt.Sprintf("no pending confirmations from your commits (%s: show all team runs)\n", toggleHint)
	}
	return ui.RenderTable(renderRows(items, selected), linksSupported, colorEnabled)
}

// notifyNewlyPending sends a desktop notification for each run in
// res.items that just started pending confirmation this poll cycle, and a
// reminder for each that has been pending long enough to be due one.
// sent is called with each run ID whose notification was delivered.
func notifyNewlyPending(n *notify.Notifier, res pollResult, sent func(runID string)) {
	if len(res.newlyPendingIDs) == 0 && len(res.remindIDs) == 0 {
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
		if err := n.PendingConfirmation(it.StackName, it.Title, it.RunURL); err != nil {
			fmt.Fprintln(os.Stderr, "spacewatch: notify:", err)
			continue
		}
		sent(id)
	}
	for _, id := range res.remindIDs {
		it, ok := byID[id]
		if !ok {
			continue
		}
		if err := n.StillPending(it.StackName, it.Title, ui.FormatAge(time.Since(it.CreatedAt)), it.RunURL); err != nil {
			fmt.Fprintln(os.Stderr, "spacewatch: notify:", err)
			continue
		}
		sent(id)
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
		var plan string
		if it.Plan != nil {
			plan = fmt.Sprintf("+%d ~%d -%d", it.Plan.Add, it.Plan.Change, it.Plan.Delete)
		}
		rows[i] = ui.Row{
			Team:     team,
			Stack:    it.StackName,
			Title:    it.Title,
			Plan:     plan,
			Destroys: it.Plan != nil && it.Plan.Delete > 0,
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

// footer renders the status line: when the last poll ran, how many runs
// are shown, and the time until the next poll. next <= 0 omits the
// countdown (e.g. --once, where there is no next poll).
func footer(polledAt time.Time, shown int, next time.Duration) string {
	line := fmt.Sprintf("polled %s · %d pending", polledAt.Format("15:04:05"), shown)
	if next > 0 {
		line += " · next poll in " + next.Round(time.Second).String()
	}
	return line + "\n"
}

// keyHints renders the watch-mode key legend, in the same bracketed layout
// as gh-monitor.
func keyHints(showAll, authExpired bool) string {
	toggle := "[a] all team runs"
	if showAll {
		toggle = "[a] my runs"
	}
	hints := "[↑↓] move  [↵] open  " + toggle + "  [r] refresh  "
	if authExpired {
		hints += "[l] relogin  "
	}
	return hints + "[?] config  [q] quit\n"
}

// viewTitle is the bold header line, naming the active view.
func viewTitle(showAll bool) string {
	if showAll {
		return "spacewatch · all team runs"
	}
	return "spacewatch · your runs"
}

// renderConfig renders the ? pane, which replaces the main view while
// open: the effective settings, and the request budget usage that used to
// clutter the main footer.
func renderConfig(cfg config, viewer string, showAll bool, notifier *notify.Notifier, res pollResult, colorEnabled bool) string {
	dim := func(s string) string { return ui.Style(s, ui.Dim, colorEnabled) }
	view := "your runs"
	if showAll {
		view = "all team runs"
	}
	remind := "off"
	if cfg.remind > 0 {
		remind = "every " + cfg.remind.String()
	}
	notifications := notifier.Method()
	if cfg.noNotify {
		notifications = "off (--no-notify)"
	}
	team := cfg.team
	if team == "" {
		team = "(custom --team-label)"
	}

	const width = len("Notifications") // longest label, so both sections align
	section := func(b *strings.Builder, title string, rows [][2]string) {
		b.WriteString(dim(title) + "\n")
		for _, r := range rows {
			b.WriteString("  " + dim(fmt.Sprintf("%-*s", width, r[0])) + "  " + r[1] + "\n")
		}
		b.WriteString("\n")
	}

	var b strings.Builder
	b.WriteString(ui.Style("spacewatch", ui.Bold, colorEnabled) + " - config\n\n")
	section(&b, "SETTINGS", [][2]string{
		{"Viewer", viewer},
		{"Team", team},
		{"Labels", strings.Join(cfg.teamLabels.values, ", ")},
		{"View", view},
		{"Active poll", cfg.activeInterval.String()},
		{"Idle poll", cfg.idleInterval.String()},
		{"Reminders", remind},
		{"Notifications", notifications},
		{"Version", version},
	})
	section(&b, "REQUESTS", [][2]string{
		{"This hour", fmt.Sprintf("%d of %d budget", res.reqWindow, cfg.requestBudget)},
		{"Total", fmt.Sprintf("%d since start", res.reqTotal)},
	})
	b.WriteString(dim("[?] close  [q] quit") + "\n")
	return b.String()
}

// frame turns rendered output into an in-place redraw: home the cursor,
// clear each line before writing it, and clear anything below the last
// line. This avoids ghosting when a frame is narrower or shorter than the
// previous one, without the flicker of clearing the whole screen first.
//
// Each line is also cleared after it's written, to remove stale text to
// the right of a wrapped line's tail, except when the line ends exactly at
// the right margin: there the terminal defers the wrap and leaves the
// cursor on the last cell, so clearing would erase the final character.
// cols is the terminal width; 0 (unknown) skips the trailing clear.
func frame(s string, cols int) string {
	var b strings.Builder
	b.WriteString(ansiHome)
	lines := strings.SplitAfter(s, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		text, nl := strings.CutSuffix(line, "\n")
		b.WriteString(ansiClearEOL)
		b.WriteString(text)
		if w := ui.VisibleWidth(text); cols > 0 && w%cols != 0 {
			b.WriteString(ansiClearEOL)
		}
		if nl {
			b.WriteByte('\n')
		}
	}
	b.WriteString(ansiClearBelow)
	return b.String()
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
	st.UseLabels(cfg.teamLabels.values)

	var viewer string
	res := doPoll(ctx, c, st, cfg, &viewer)
	if res.err != nil {
		fail("%v", res.err)
	}
	res.items = visibleItems(res.items, viewer, cfg.all)

	if !cfg.noNotify {
		notifyNewlyPending(notify.New(), res, func(id string) { st.MarkNotified(id, time.Now()) })
	}
	if err := st.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "spacewatch: saving state:", err)
	}

	linksSupported := ui.SupportsLinks()
	colorEnabled := ui.ColorEnabled()
	fmt.Print(renderTable(res.items, cfg.all, "--all", -1, linksSupported, colorEnabled))
	fmt.Print(ui.Style(footer(res.polledAt, len(res.items), 0), ui.Dim, colorEnabled))
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
	st.UseLabels(cfg.teamLabels.values)
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
		last       pollResult
		viewer     string
		showAll    = cfg.all
		selected   int
		nextPollAt time.Time // when the scheduled poll fires, for the footer countdown
		fullClear  bool      // clear the whole screen on the next redraw (after a resize)
		configOpen bool      // the ? pane is showing instead of the main view
	)

	redraw := func() {
		var b strings.Builder
		if configOpen {
			b.WriteString(renderConfig(cfg, viewer, showAll, notifier, last, colorEnabled))
		} else {
			view := visibleItems(last.items, viewer, showAll)
			b.WriteString(ui.Style(viewTitle(showAll), ui.Bold, colorEnabled) + "\n\n")
			switch {
			case last.reloginErr != nil:
				b.WriteString(ui.Style(fmt.Sprintf("spacewatch: relogin failed: %v (press l to retry)\n\n", last.reloginErr), ui.BoldRed, colorEnabled))
			case last.authExpired:
				b.WriteString(ui.Style("spacewatch: session expired - press l to relogin\n\n", ui.BoldRed, colorEnabled))
			case last.err != nil:
				b.WriteString(ui.Style(fmt.Sprintf("spacewatch: poll error (retrying): %v\n\n", last.err), ui.BoldRed, colorEnabled))
			}
			b.WriteString(renderTable(view, showAll, "a", selected, linksSupported, colorEnabled))
			b.WriteString("\n")
			b.WriteString(ui.Style(keyHints(showAll, last.authExpired), ui.Dim, colorEnabled))
			if !last.polledAt.IsZero() {
				b.WriteString(ui.Style(footer(last.polledAt, len(view), max(time.Until(nextPollAt), 0)), ui.Dim, colorEnabled))
			}
		}
		// readKeys puts the tty into raw mode, which on Unix also clears
		// the OPOST output flag for the whole tty (stdin/stdout share
		// one underlying device) - without it, a bare "\n" no longer
		// implies a carriage return, so every line after the first
		// continues from whatever column the previous line ended at
		// instead of resetting to column 0. Normalize to "\r\n" so this
		// renders correctly regardless of OPOST state.
		out := frame(b.String(), ui.TerminalWidth())
		if fullClear {
			out = ansiClearScreen + out
			fullClear = false
		}
		fmt.Print(crlf(out))
	}

	clampSelection := func() {
		n := len(visibleItems(last.items, viewer, showAll))
		if selected >= n {
			selected = n - 1
		}
		if selected < 0 {
			selected = 0
		}
	}

	poll := func() time.Duration {
		last = doPoll(ctx, c, st, cfg, &viewer)
		if last.err == nil {
			if !cfg.noNotify {
				view := last
				view.items = visibleItems(last.items, viewer, showAll)
				notifyNewlyPending(notifier, view, func(id string) { st.MarkNotified(id, time.Now()) })
			}
			if err := st.Save(); err != nil {
				last.err = fmt.Errorf("saving state: %w", err)
			}
		}
		clampSelection()
		nextDelay := nextInterval(cfg, len(visibleItems(last.items, viewer, showAll)), last.reqWindow)
		if last.err != nil && nextDelay < cfg.idleInterval {
			nextDelay = cfg.idleInterval // back off on any poll error rather than hammering a failing API
		}
		nextPollAt = time.Now().Add(nextDelay)
		redraw()
		return nextDelay
	}

	timer := time.NewTimer(0) // fire immediately for the first poll
	defer timer.Stop()
	countdown := time.NewTicker(time.Second)
	defer countdown.Stop()

	for {
		select {
		case <-timer.C:
			timer.Reset(poll())
		case k := <-keys:
			if configOpen && k != keyQuit && k != keyConfig {
				continue // the pane only responds to ? and q
			}
			switch k {
			case keyQuit:
				return
			case keyConfig:
				configOpen = !configOpen
				redraw()
			case keyUp:
				if selected > 0 {
					selected--
					redraw()
				}
			case keyDown:
				if selected < len(visibleItems(last.items, viewer, showAll))-1 {
					selected++
					redraw()
				}
			case keyEnter:
				if view := visibleItems(last.items, viewer, showAll); selected >= 0 && selected < len(view) {
					openURL(view[selected].RunURL)
				}
			case keyRefresh:
				timer.Reset(0)
			case keyToggleAll:
				showAll = !showAll
				clampSelection()
				redraw()
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
		case <-countdown.C:
			if !last.polledAt.IsZero() {
				redraw()
			}
		case <-resized:
			fullClear = true
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

	fmt.Println("spacewatch: running `spacectl profile login`...")
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
	return exec.Command(openCommand(runtime.GOOS), url).Start()
}

// openCommand returns the command that opens a URL in the default browser
// on goos.
func openCommand(goos string) string {
	if goos == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// openURL opens a URL in the user's default browser. Used as the
// keyboard-driven fallback for terminals that don't render OSC 8
// hyperlinks.
func openURL(url string) {
	if err := runOpen(url); err != nil {
		fmt.Fprintln(os.Stderr, "spacewatch: open:", err)
	}
}
