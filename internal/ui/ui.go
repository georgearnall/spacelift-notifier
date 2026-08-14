// Package ui renders the pending-confirmations table to the terminal,
// including OSC 8 clickable hyperlinks where the terminal is known to
// support them.
package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// Row is one already-formatted table row.
type Row struct {
	Team  string
	Stack string
	Title string
	Age   string
	URL   string
}

const linkText = "open →"

// RenderTable renders rows as an aligned table. When linksSupported is
// true, the LINK column is an OSC 8 hyperlink; otherwise it falls back to
// printing the raw URL so it can still be copied.
func RenderTable(rows []Row, linksSupported bool) string {
	if len(rows) == 0 {
		return "no pending confirmations for your team\n"
	}

	headers := []string{"TEAM", "STACK", "TITLE", "AGE", "LINK"}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}

	cells := make([][]string, len(rows))
	for i, r := range rows {
		link := r.URL
		if linksSupported {
			link = Hyperlink(r.URL, linkText)
		}
		cells[i] = []string{r.Team, r.Stack, truncate(r.Title, 60), r.Age, link}
		// The LINK column is last and never padded, so its width (which
		// includes invisible escape bytes when hyperlinked) doesn't
		// affect alignment of the columns before it.
		for j := 0; j < len(headers)-1; j++ {
			if l := len(cells[i][j]); l > widths[j] {
				widths[j] = l
			}
		}
	}

	var b strings.Builder
	writeRow := func(row []string) {
		for i, c := range row {
			if i == len(row)-1 {
				b.WriteString(c)
				continue
			}
			fmt.Fprintf(&b, "%-*s  ", widths[i], c)
		}
		b.WriteByte('\n')
	}

	writeRow(headers)
	for _, row := range cells {
		writeRow(row)
	}
	return b.String()
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// FormatAge renders a duration as a short, human-scaled age like "3m",
// "2h", or "5d".
func FormatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// Hyperlink wraps text in an OSC 8 terminal hyperlink escape sequence.
func Hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// SupportsLinks reports whether the current terminal is known to render
// OSC 8 hyperlinks, based on common terminal-identifying environment
// variables. Terminals not in this list still work fine - they just show
// the plain URL fallback instead of a clickable link.
func SupportsLinks() bool {
	return isTTY(os.Stdout) && envSupportsLinks(os.Getenv)
}

func isTTY(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

func envSupportsLinks(getenv func(string) string) bool {
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "kitty", "ghostty", "Apple_Terminal":
		return true
	}
	if getenv("VTE_VERSION") != "" {
		return true // GNOME Terminal, Tilix, and other VTE-based terminals
	}
	if getenv("WT_SESSION") != "" {
		return true // Windows Terminal
	}
	if getenv("TERM") == "xterm-kitty" {
		return true
	}
	return false
}
