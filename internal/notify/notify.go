// Package notify sends a desktop notification when a new run starts
// pending confirmation, using an OSC 9 terminal escape sequence where
// supported, falling back to macOS's osascript elsewhere.
package notify

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Notifier sends desktop notifications. The zero value is not usable;
// construct one with New(). Fields are exported so tests can substitute
// the output writer, the terminal-detection function, and the osascript
// runner.
type Notifier struct {
	Stdout       io.Writer
	TermProgram  func() string
	RunOSAScript func(script string) error
}

// New returns a Notifier wired up to the real terminal and osascript.
func New() *Notifier {
	return &Notifier{
		Stdout:      os.Stdout,
		TermProgram: func() string { return os.Getenv("TERM_PROGRAM") },
		RunOSAScript: func(script string) error {
			return exec.Command("osascript", "-e", script).Run()
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
func (n *Notifier) Send(title, body string) error {
	if oscCapableTerminals[n.TermProgram()] {
		fmt.Fprintf(n.Stdout, "\x1b]9;%s: %s\x1b\\", title, body)
		return nil
	}
	script := fmt.Sprintf("display notification %s with title %s", quote(body), quote(title))
	return n.RunOSAScript(script)
}

// PendingConfirmation sends a notification for a run that just started
// pending confirmation.
func (n *Notifier) PendingConfirmation(stackName, runTitle string) error {
	body := stackName
	if runTitle != "" {
		body = stackName + " — " + runTitle
	}
	return n.Send("Spacelift: pending confirmation", body)
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
