package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/georgearnall/spacewatch/internal/teamdiscovery"
	"github.com/rivo/uniseg"
	"golang.org/x/term"
)

// pickerItem is one selectable row in the team picker.
type pickerItem struct {
	name     string   // group name, or the slug itself for a manual entry
	slugs    []string // team slugs this row selects
	stacks   int
	manual   bool
	selected bool
}

// pickerState is the team picker's display state. cursor ranges over
// items plus one extra row, the "type manually" entry at len(items).
type pickerState struct {
	items  []pickerItem
	cursor int
	note   string // shown above the list, e.g. why discovery found nothing
	hint   string // shown below the list after a rejected keypress
}

type pickerAction int

const (
	pickerContinue pickerAction = iota
	pickerDone
	pickerAbort
	pickerManual
)

// newPickerState builds the picker rows from discovered candidates,
// pre-selecting a row only when current holds all of its slugs (so
// confirming never adds a slug that wasn't chosen), and adding the rest of
// current as already-selected manual rows.
func newPickerState(cands []teamdiscovery.Candidate, current []string, note string) pickerState {
	s := pickerState{note: note}
	covered := map[string]bool{}
	for _, c := range cands {
		it := pickerItem{name: c.Name, slugs: c.Slugs, stacks: c.Stacks}
		it.selected = !slices.ContainsFunc(c.Slugs, func(slug string) bool { return !slices.Contains(current, slug) })
		if it.selected {
			for _, slug := range c.Slugs {
				covered[slug] = true
			}
		}
		s.items = append(s.items, it)
	}
	var extra []string
	for _, slug := range current {
		if !covered[slug] {
			extra = append(extra, slug)
		}
	}
	s.addManual(extra)
	return s
}

func (s *pickerState) manualRow() bool { return s.cursor == len(s.items) }

// apply handles one keypress and reports what the caller should do next.
func (s *pickerState) apply(k key) pickerAction {
	s.hint = ""
	switch k {
	case keyUp:
		if s.cursor > 0 {
			s.cursor--
		}
	case keyDown:
		if s.cursor < len(s.items) {
			s.cursor++
		}
	case keySpace:
		if s.manualRow() {
			return pickerManual
		}
		s.items[s.cursor].selected = !s.items[s.cursor].selected
	case keyEnter:
		if s.manualRow() {
			return pickerManual
		}
		if len(s.selectedSlugs()) == 0 {
			s.hint = "Select at least one team with space, or type one manually."
			return pickerContinue
		}
		return pickerDone
	case keyQuit:
		return pickerAbort
	}
	return pickerContinue
}

// addManual selects each slug, adding a manual row for any without a row
// of its own. A discovered row covering several slugs isn't reused, so
// typing one slug selects exactly that slug.
func (s *pickerState) addManual(slugs []string) {
	for _, slug := range slugs {
		i := slices.IndexFunc(s.items, func(it pickerItem) bool { return slices.Equal(it.slugs, []string{slug}) })
		if i >= 0 {
			s.items[i].selected = true
			continue
		}
		s.items = append(s.items, pickerItem{name: slug, slugs: []string{slug}, manual: true, selected: true})
	}
	if s.cursor > len(s.items) {
		s.cursor = len(s.items)
	}
}

// selectedSlugs returns the slugs of every selected row, deduplicated, in
// row order.
func (s *pickerState) selectedSlugs() []string {
	var out []string
	for _, it := range s.items {
		if !it.selected {
			continue
		}
		for _, slug := range it.slugs {
			if !slices.Contains(out, slug) {
				out = append(out, slug)
			}
		}
	}
	return out
}

// renderPicker draws the picker. Lines end in \r\n since it's drawn while
// the terminal is in raw mode.
func renderPicker(s pickerState) string {
	var b strings.Builder
	b.WriteString("Select the Spacelift teams to watch (↑/↓ move, space toggle, enter confirm, q quit)\r\n")
	if s.note != "" {
		b.WriteString(s.note + "\r\n")
	}
	for i, it := range s.items {
		cursor, box := "  ", "[ ]"
		if i == s.cursor {
			cursor = "> "
		}
		if it.selected {
			box = "[x]"
		}
		detail := fmt.Sprintf("%s · %d stacks", strings.Join(it.slugs, ", "), it.stacks)
		if it.manual {
			detail = "added manually"
		}
		fmt.Fprintf(&b, "%s%s %s  (%s)\r\n", cursor, box, it.name, detail)
	}
	cursor := "  "
	if s.manualRow() {
		cursor = "> "
	}
	b.WriteString(cursor + "+ Type team slugs manually...\r\n")
	if s.hint != "" {
		b.WriteString(s.hint + "\r\n")
	}
	return b.String()
}

// parseTeamList splits a comma-separated list of team slugs, dropping
// blanks.
func parseTeamList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

var errPickerCancelled = errors.New("team selection cancelled")

// pickTeams runs the interactive team picker on stdin/stderr and returns
// the chosen team slugs. Stdin must be a terminal.
func pickTeams(cands []teamdiscovery.Candidate, current []string, note string) ([]string, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("entering raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	in := bufio.NewReader(os.Stdin)
	s := newPickerState(cands, current, note)
	rows := draw(os.Stderr, s, 0)
	for {
		// One key per read, so bytes typed straight after the key that opens
		// manual entry stay buffered for the line prompt below.
		k, err := readKey(in)
		if err != nil {
			return nil, fmt.Errorf("reading keys: %w", err)
		}
		switch s.apply(k) {
		case pickerDone:
			return s.selectedSlugs(), nil
		case pickerAbort:
			return nil, errPickerCancelled
		case pickerManual:
			term.Restore(fd, oldState)
			const prompt = "Team slug(s), comma-separated (as in folder:owning-team/<slug>): "
			fmt.Fprint(os.Stderr, prompt)
			line, err := readLine(in)
			if err != nil {
				return nil, fmt.Errorf("reading team slugs: %w", err)
			}
			if _, err := term.MakeRaw(fd); err != nil {
				return nil, fmt.Errorf("entering raw mode: %w", err)
			}
			s.addManual(parseTeamList(line))
			// The prompt and its echoed input now sit below the picker.
			rows += physicalRows(prompt+line, stderrWidth())
		}
		rows = draw(os.Stderr, s, rows)
	}
}

// readLine reads up to a line ending, accepting \r as well as \n: input
// typed before the terminal left raw mode is still buffered with the raw
// \r Enter sends, rather than the \n the terminal translates it to.
func readLine(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		switch c {
		case '\r':
			if r.Buffered() > 0 {
				if next, _ := r.Peek(1); next[0] == '\n' {
					r.Discard(1)
				}
			}
			return b.String(), nil
		case '\n':
			return b.String(), nil
		}
		b.WriteByte(c)
	}
}

// readKey reads one keypress: a single byte, or a three-byte CSI sequence
// such as an arrow key.
func readKey(r *bufio.Reader) (key, error) {
	b, err := r.ReadByte()
	if err != nil {
		return keyNone, err
	}
	seq := []byte{b}
	// Only bytes already read are checked: an arrow key's sequence arrives
	// in one read, and peeking further would block on a lone Esc.
	if b == 0x1b && r.Buffered() >= 2 {
		if next, _ := r.Peek(2); next[0] == '[' {
			seq = append(seq, next...)
			r.Discard(2)
		}
	}
	return decodeKey(seq), nil
}

// draw redraws the picker over the prevRows terminal rows it last
// occupied and returns how many it occupies now.
func draw(w io.Writer, s pickerState, prevRows int) int {
	out := renderPicker(s)
	if prevRows > 0 {
		fmt.Fprintf(w, "\x1b[%dF\x1b[J", prevRows) // up to the picker's first row, clear below
	}
	fmt.Fprint(w, out)
	cols := stderrWidth()
	rows := 0
	for _, line := range strings.SplitAfter(out, "\n") {
		if line != "" {
			rows += physicalRows(strings.TrimRight(line, "\r\n"), cols)
		}
	}
	return rows
}

// physicalRows returns how many terminal rows line takes when written
// from column 0 of a terminal cols wide, counting wrapping. cols <= 0
// means unknown, treated as no wrapping.
func physicalRows(line string, cols int) int {
	w := uniseg.StringWidth(line)
	if cols <= 0 || w <= cols {
		return 1
	}
	return (w + cols - 1) / cols
}

// stderrWidth returns stderr's width in columns, or 0 if unknown. The
// picker draws on stderr, so stdout's width (ui.TerminalWidth) may not
// apply.
func stderrWidth() int {
	w, _, err := term.GetSize(int(os.Stderr.Fd()))
	if err != nil {
		return 0
	}
	return w
}
