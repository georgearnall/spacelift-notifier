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
		RunOSAScript: func(string) error {
			osaCalled = true
			return nil
		},
	}

	if err := n.Send("title", "body"); err != nil {
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
		RunOSAScript: func(script string) error {
			gotScript = script
			return nil
		},
	}

	if err := n.Send("Spacelift: pending confirmation", "my-stack — apply"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(gotScript, `"my-stack — apply"`) || !strings.Contains(gotScript, `"Spacelift: pending confirmation"`) {
		t.Errorf("osascript script = %q, missing expected quoted title/body", gotScript)
	}
}

func TestSend_PropagatesOSAScriptError(t *testing.T) {
	n := &Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "" },
		RunOSAScript: func(string) error {
			return errors.New("osascript not found")
		},
	}
	if err := n.Send("t", "b"); err == nil {
		t.Fatal("Send() error = nil, want the osascript error to propagate")
	}
}

func TestPendingConfirmation_ComposesBody(t *testing.T) {
	var gotScript string
	n := &Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "" },
		RunOSAScript: func(script string) error {
			gotScript = script
			return nil
		},
	}

	if err := n.PendingConfirmation("orders-api", "apply main"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotScript, "orders-api — apply main") {
		t.Errorf("script = %q, want body to combine stack and run title", gotScript)
	}

	if err := n.PendingConfirmation("orders-api", ""); err != nil {
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
