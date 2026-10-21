package notify

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSend_OSC9ForKnownTerminal(t *testing.T) {
	var out bytes.Buffer
	osaCalled := false
	n := &Notifier{
		Stdout:      &out,
		TermProgram: func() string { return "iTerm.app" },
		GOOS:        "darwin",
		Run: func(string, ...string) error {
			osaCalled = true
			return nil
		},
	}

	if err := n.Send("title", "body", ""); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if osaCalled {
		t.Error("Send() called osascript when the terminal supports OSC 9")
	}
	if got := out.String(); got != "\x1b]9;title: body\x1b\\" {
		t.Errorf("Send() wrote %q", got)
	}
}

func TestSend_FallsBackToOSAScriptForUnknownTerminal(t *testing.T) {
	var gotScript string
	n := &Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "Apple_Terminal" },
		GOOS:        "darwin",
		Run: func(_ string, args ...string) error {
			gotScript = args[len(args)-1]
			return nil
		},
	}

	if err := n.Send("Spacelift: pending confirmation", "my-stack - apply", ""); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(gotScript, `"my-stack - apply"`) || !strings.Contains(gotScript, `"Spacelift: pending confirmation"`) {
		t.Errorf("osascript script = %q, missing expected quoted title/body", gotScript)
	}
}

func TestSend_PropagatesOSAScriptError(t *testing.T) {
	n := &Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "" },
		GOOS:        "darwin",
		Run: func(string, ...string) error {
			return errors.New("osascript not found")
		},
	}
	if err := n.Send("t", "b", ""); err == nil {
		t.Fatal("Send() error = nil, want the osascript error to propagate")
	}
}

func TestPendingConfirmation_ComposesBody(t *testing.T) {
	var gotScript string
	n := &Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "" },
		GOOS:        "darwin",
		Run: func(_ string, args ...string) error {
			gotScript = args[len(args)-1]
			return nil
		},
	}

	if err := n.PendingConfirmation("orders-api", "apply main", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotScript, "orders-api - apply main") {
		t.Errorf("script = %q, want body to combine stack and run title", gotScript)
	}

	if err := n.PendingConfirmation("orders-api", "", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotScript, `"orders-api"`) {
		t.Errorf("script = %q, want body to be just the stack name when title is empty", gotScript)
	}
}

func TestQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain`, `"plain"`},
		{`has "quotes"`, `"has \"quotes\""`},
		{`back\slash`, `"back\\slash"`},
	}
	for _, c := range cases {
		if got := quote(c.in); got != c.want {
			t.Errorf("quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSend_UsesNotifySendOnLinux(t *testing.T) {
	var gotName string
	var gotArgs []string
	n := &Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "" },
		GOOS:        "linux",
		Run: func(name string, args ...string) error {
			gotName, gotArgs = name, args
			return nil
		},
	}
	if err := n.Send("title", "body", ""); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotName != "notify-send" || strings.Join(gotArgs, "|") != "--|title|body" {
		t.Errorf("Send() ran %q %q, want notify-send -- title body", gotName, gotArgs)
	}
}

func TestSend_UnsupportedOSReturnsError(t *testing.T) {
	n := &Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "" },
		GOOS:        "windows",
		Run:         func(string, ...string) error { t.Fatal("Run called on unsupported OS"); return nil },
	}
	if err := n.Send("t", "b", ""); err == nil {
		t.Fatal("Send() error = nil, want an unsupported-OS error")
	}
}

func TestSend_PrefersTerminalNotifierOnMac(t *testing.T) {
	var out bytes.Buffer
	var gotName string
	var gotArgs []string
	n := &Notifier{
		Stdout:      &out,
		TermProgram: func() string { return "iTerm.app" }, // even OSC 9 terminals: their notifications can't open the run
		GOOS:        "darwin",
		LookPath:    func(string) (string, error) { return "/opt/homebrew/bin/terminal-notifier", nil },
		Run: func(name string, args ...string) error {
			gotName, gotArgs = name, args
			return nil
		},
	}
	if err := n.Send("title", "body", "https://example.test/run"); err != nil {
		t.Fatal(err)
	}
	want := "-title|title|-message|body|-open|https://example.test/run|-group|https://example.test/run"
	if gotName != "terminal-notifier" || strings.Join(gotArgs, "|") != want {
		t.Errorf("Send() ran %q %q, want terminal-notifier with %s", gotName, gotArgs, want)
	}
	if out.Len() != 0 {
		t.Errorf("Send() also wrote an OSC 9 sequence: %q", out.String())
	}
}

func TestSend_TerminalNotifierMissingFallsBack(t *testing.T) {
	var gotName string
	n := &Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "" },
		GOOS:        "darwin",
		LookPath:    func(string) (string, error) { return "", errors.New("not found") },
		Run:         func(name string, _ ...string) error { gotName = name; return nil },
	}
	if err := n.Send("t", "b", "u"); err != nil {
		t.Fatal(err)
	}
	if gotName != "osascript" {
		t.Errorf("Send() ran %q, want the osascript fallback", gotName)
	}
}
