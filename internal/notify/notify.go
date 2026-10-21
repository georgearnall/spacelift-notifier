// Package notify sends a desktop notification when a new run starts
// pending confirmation. On macOS it prefers terminal-notifier when
// installed, since clicking its notifications opens the run; otherwise it
// uses an OSC 9 terminal escape sequence where supported, falling back to
// osascript on macOS and notify-send on Linux.
package notify

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Notifier sends desktop notifications. The zero value is not usable;
// construct one with New(). Fields are exported so tests can substitute
// the output writer, the terminal-detection function, the OS, the command
// lookup, and the command runner used for the non-OSC fallbacks.
type Notifier struct {
	Stdout      io.Writer
	TermProgram func() string
	GOOS        string
	LookPath    func(file string) (string, error) // nil means no extra commands are installed
	Run         func(name string, args ...string) error
}

// New returns a Notifier wired up to the real terminal and OS.
func New() *Notifier {
	return &Notifier{
		Stdout:      os.Stdout,
		TermProgram: func() string { return os.Getenv("TERM_PROGRAM") },
		GOOS:        runtime.GOOS,
		LookPath:    exec.LookPath,
		Run: func(name string, args ...string) error {
			return exec.Command(name, args...).Run()
		},
	}
}

// oscCapableTerminals lists TERM_PROGRAM values known to render the OSC 9
// notification escape sequence.
var oscCapableTerminals = map[string]bool{
	"iTerm.app": true,
	"ghostty":   true,
	"WezTerm":   true,
}

// Send delivers a desktop notification with the given title and body.
// url, if set, is opened when the notification is clicked (terminal-notifier
// only) and also groups notifications, so a reminder replaces the earlier
// notification for the same run.
func (n *Notifier) Send(title, body, url string) error {
	if n.hasTerminalNotifier() {
		args := []string{"-title", title, "-message", body}
		if url != "" {
			args = append(args, "-open", url, "-group", url)
		}
		return n.Run("terminal-notifier", args...)
	}
	if oscCapableTerminals[n.TermProgram()] {
		fmt.Fprintf(n.Stdout, "\x1b]9;%s: %s\x1b\\", title, body)
		return nil
	}
	switch n.GOOS {
	case "darwin":
		script := fmt.Sprintf("display notification %s with title %s", quote(body), quote(title))
		return n.Run("osascript", "-e", script)
	case "linux":
		// "--" so a title starting with "-" isn't parsed as an option.
		return n.Run("notify-send", "--", title, body)
	default:
		return fmt.Errorf("desktop notifications aren't supported on %s outside an OSC 9 terminal", n.GOOS)
	}
}

func (n *Notifier) hasTerminalNotifier() bool {
	if n.GOOS != "darwin" || n.LookPath == nil {
		return false
	}
	_, err := n.LookPath("terminal-notifier")
	return err == nil
}

// PendingConfirmation sends a notification for a run that just started
// pending confirmation. url is the run's page.
func (n *Notifier) PendingConfirmation(stackName, runTitle, url string) error {
	return n.Send("Spacelift: pending confirmation", runBody(stackName, runTitle), url)
}

// StillPending sends a reminder for a run that has been pending
// confirmation for age (e.g. "20m"). url is the run's page.
func (n *Notifier) StillPending(stackName, runTitle, age, url string) error {
	return n.Send("Spacelift: still pending confirmation ("+age+")", runBody(stackName, runTitle), url)
}

func runBody(stackName, runTitle string) string {
	if runTitle == "" {
		return stackName
	}
	return stackName + " - " + runTitle
}

// quote renders s as an AppleScript string literal, escaping quotes and
// backslashes so arbitrary run titles/stack names can't break out of the
// osascript -e argument.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
