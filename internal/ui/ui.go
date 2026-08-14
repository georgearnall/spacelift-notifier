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

// Row is one already-formatted table row. Selected marks the row as the
// keyboard-navigation cursor's current target, shown as a leading marker.
type Row struct {
	Team     string
	Stack    string
	Title    string
	Age      string
	URL      string
	Selected bool
}

const linkText = "open →"

// SGR color/style codes used to highlight the table. Combined codes
// (e.g. BoldYellow) are single escape sequences rather than two Style()
// calls, so nesting them inside an already-styled cell can't produce a
// stray extra Reset in the middle.
const (
	Reset      = "\x1b[0m"
	Bold       = "\x1b[1m"
	Dim        = "\x1b[2m"
	Cyan       = "\x1b[36m"
	BoldRed    = "\x1b[1;31m"
	BoldYellow = "\x1b[1;33m"
)

// Style wraps s in the given SGR code if enabled is true and s is
// non-empty; otherwise it returns s unchanged.
func Style(s, code string, enabled bool) string {
	if !enabled || s == "" {
		return s
	}
	return code + s + Reset
}

// RenderTable renders rows as an aligned table. When linksSupported is
// true, the LINK column is an OSC 8 hyperlink; otherwise it falls back to
// printing the raw URL so it can still be copied. When colorEnabled is
// true, the header is bold, the TEAM column is cyan, the AGE column is
// dim, and the selected row's marker is highlighted.
func RenderTable(rows []Row, linksSupported, colorEnabled bool) string {
	if len(rows) == 0 {
		return "no pending confirmations for your team\n"
	}

	headers := []string{"", "TEAM", "STACK", "TITLE", "AGE", "LINK"}
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
		marker := " "
		if r.Selected {
			marker = ">"
		}
		cells[i] = []string{marker, r.Team, r.Stack, truncate(r.Title, 60), r.Age, link}
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

	writeHeader := func() {
		for i, h := range headers {
			cell := h
			if i != len(headers)-1 {
				cell = fmt.Sprintf("%-*s", widths[i], h)
			}
			b.WriteString(Style(cell, Bold, colorEnabled))
			if i != len(headers)-1 {
				b.WriteString("  ")
			}
		}
		b.WriteByte('\n')
	}

	writeDataRow := func(row []string, r Row) {
		for i, c := range row {
			if i == len(row)-1 {
				b.WriteString(styleCell(i, c, r, colorEnabled))
				continue
			}
			padded := fmt.Sprintf("%-*s", widths[i], c)
			b.WriteString(styleCell(i, padded, r, colorEnabled))
			b.WriteString("  ")
		}
		b.WriteByte('\n')
	}

	writeHeader()
	for i, row := range cells {
		writeDataRow(row, rows[i])
	}
	return b.String()
}

// styleCell applies per-column coloring: TEAM is cyan, AGE is dim, the
// marker is highlighted when its row is selected, and the remaining
// columns are bolded on the selected row for a full-row highlight.
func styleCell(col int, s string, r Row, colorEnabled bool) string {
	if !colorEnabled {
		return s
	}
	switch col {
	case 0: // marker
		if r.Selected {
			return Style(s, BoldYellow, true)
		}
		return s
	case 1: // team
		return Style(s, Cyan, true)
	case 4: // age
		return Style(s, Dim, true)
	default:
		if r.Selected {
			return Style(s, Bold, true)
		}
		return s
	}
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

// ColorEnabled reports whether ANSI color output should be used: it's
// suppressed when stdout isn't a terminal (e.g. output is piped/redirected)
// or when NO_COLOR is set, per the https://no-color.org convention.
func ColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTTY(os.Stdout)
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
