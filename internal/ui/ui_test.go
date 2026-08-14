package ui

import (
	"strings"
	"testing"
	"time"
)

func TestFormatAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := FormatAge(c.d); got != c.want {
			t.Errorf("FormatAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestRenderTable_NoRows(t *testing.T) {
	got := RenderTable(nil, true)
	if !strings.Contains(got, "no pending confirmations") {
		t.Errorf("RenderTable(nil) = %q, want an empty-state message", got)
	}
}

func TestRenderTable_LinksSupported(t *testing.T) {
	rows := []Row{{Team: "ecommerce", Stack: "orders-api", Title: "apply", Age: "5m", URL: "https://example.test/stack/orders-api/run/r1"}}
	got := RenderTable(rows, true)
	if !strings.Contains(got, "\x1b]8;;https://example.test/stack/orders-api/run/r1\x1b\\") {
		t.Errorf("RenderTable() with links supported missing OSC 8 escape sequence: %q", got)
	}
	if !strings.Contains(got, "ecommerce") || !strings.Contains(got, "orders-api") {
		t.Errorf("RenderTable() = %q, missing expected cell content", got)
	}
}

func TestRenderTable_LinksUnsupported(t *testing.T) {
	rows := []Row{{Team: "ecommerce", Stack: "orders-api", Title: "apply", Age: "5m", URL: "https://example.test/stack/orders-api/run/r1"}}
	got := RenderTable(rows, false)
	if strings.Contains(got, "\x1b]8;;") {
		t.Errorf("RenderTable() with links unsupported should not contain OSC 8 escapes: %q", got)
	}
	if !strings.Contains(got, "https://example.test/stack/orders-api/run/r1") {
		t.Errorf("RenderTable() with links unsupported should print the raw URL: %q", got)
	}
}

func TestRenderTable_ColumnsAlign(t *testing.T) {
	rows := []Row{
		{Team: "e", Stack: "short", Title: "t", Age: "1m", URL: "u"},
		{Team: "ecommerce-long-name", Stack: "s", Title: "t", Age: "1m", URL: "u"},
	}
	got := RenderTable(rows, false)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("RenderTable() produced %d lines, want 3", len(lines))
	}
	// The STACK column should start at the same byte offset on every line.
	headerStackCol := strings.Index(lines[0], "STACK")
	row2StackCol := strings.Index(lines[2], "s")
	if headerStackCol != row2StackCol {
		t.Errorf("columns not aligned: header STACK at %d, row2's stack cell at %d", headerStackCol, row2StackCol)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate(short) = %q, want unchanged", got)
	}
	got := truncate("this is a long title that exceeds the limit", 10)
	if len([]rune(got)) > 10 {
		t.Errorf("truncate() = %q, exceeds max length 10 runes", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate() = %q, want ellipsis suffix", got)
	}
}

func TestHyperlink(t *testing.T) {
	got := Hyperlink("https://example.test", "click me")
	want := "\x1b]8;;https://example.test\x1b\\click me\x1b]8;;\x1b\\"
	if got != want {
		t.Errorf("Hyperlink() = %q, want %q", got, want)
	}
}

func TestEnvSupportsLinks(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"iTerm", map[string]string{"TERM_PROGRAM": "iTerm.app"}, true},
		{"kitty via TERM", map[string]string{"TERM": "xterm-kitty"}, true},
		{"VTE-based terminal", map[string]string{"VTE_VERSION": "6003"}, true},
		{"Windows Terminal", map[string]string{"WT_SESSION": "abc"}, true},
		{"unknown terminal", map[string]string{"TERM": "xterm"}, false},
		{"nothing set", map[string]string{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			if got := envSupportsLinks(getenv); got != c.want {
				t.Errorf("envSupportsLinks() = %v, want %v", got, c.want)
			}
		})
	}
}
