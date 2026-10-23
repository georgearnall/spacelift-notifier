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
// pre-selecting any row covering one of current, and adding current slugs
// no candidate covers as already-selected manual rows.
func newPickerState(cands []teamdiscovery.Candidate, current []string, note string) pickerState {
	s := pickerState{note: note}
	covered := map[string]bool{}
	for _, c := range cands {
		it := pickerItem{name: c.Name, slugs: c.Slugs, stacks: c.Stacks}
		for _, slug := range c.Slugs {
			covered[slug] = true
			if slices.Contains(current, slug) {
				it.selected = true
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

// addManual selects each slug, adding a manual row for any not already
// listed, and moves the cursor back onto the list.
func (s *pickerState) addManual(slugs []string) {
	for _, slug := range slugs {
		i := slices.IndexFunc(s.items, func(it pickerItem) bool { return slices.Contains(it.slugs, slug) })
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
	lines := draw(os.Stderr, s, 0)
	buf := make([]byte, 64)
	for {
		n, err := in.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("reading keys: %w", err)
		}
		for _, k := range decodeKeys(buf[:n]) {
			switch s.apply(k) {
			case pickerDone:
				return s.selectedSlugs(), nil
			case pickerAbort:
				return nil, errPickerCancelled
			case pickerManual:
				term.Restore(fd, oldState)
				fmt.Fprint(os.Stderr, "Team slug(s), comma-separated (as in folder:owning-team/<slug>): ")
				line, err := in.ReadString('\n')
				if err != nil {
					return nil, fmt.Errorf("reading team slugs: %w", err)
				}
				if _, err := term.MakeRaw(fd); err != nil {
					return nil, fmt.Errorf("entering raw mode: %w", err)
				}
				s.addManual(parseTeamList(line))
				lines++ // the prompt line now sits below the picker
			}
		}
		lines = draw(os.Stderr, s, lines)
	}
}

// draw redraws the picker over the prevLines lines it last occupied and
// returns how many lines it occupies now.
func draw(w io.Writer, s pickerState, prevLines int) int {
	out := renderPicker(s)
	if prevLines > 0 {
		fmt.Fprintf(w, "\x1b[%dF\x1b[J", prevLines) // up to the picker's first line, clear below
	}
	fmt.Fprint(w, out)
	return strings.Count(out, "\n")
}
