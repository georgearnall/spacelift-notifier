package main

import (
	"bufio"
	"slices"
	"strings"
	"testing"

	"github.com/georgearnall/spacewatch/internal/teamdiscovery"
)

var testCandidates = []teamdiscovery.Candidate{
	{Name: "AgentTools", Slugs: []string{"agenttools", "agent-tools"}, Stacks: 115},
	{Name: "New Verticals & Insurance", Slugs: []string{"new-verticals-insurance"}, Stacks: 43},
}

func TestNewPickerState_PreselectsCurrentTeams(t *testing.T) {
	s := newPickerState(testCandidates, []string{"agent-tools", "agenttools", "atlas"}, "")

	if got, want := s.selectedSlugs(), []string{"agenttools", "agent-tools", "atlas"}; !slices.Equal(got, want) {
		t.Errorf("selectedSlugs() = %v, want %v", got, want)
	}
	if !s.items[0].selected {
		t.Error("AgentTools row not pre-selected, want it selected since all its slugs are current")
	}
	if last := s.items[len(s.items)-1]; !last.manual || last.name != "atlas" {
		t.Errorf("last item = %+v, want manual row for atlas", last)
	}
}

func TestNewPickerState_PartialMatchKeepsExactSlugs(t *testing.T) {
	s := newPickerState(testCandidates, []string{"agent-tools"}, "")

	if got, want := s.selectedSlugs(), []string{"agent-tools"}; !slices.Equal(got, want) {
		t.Errorf("selectedSlugs() = %v, want %v (confirming must not add agenttools)", got, want)
	}
	if s.items[0].selected {
		t.Error("AgentTools row pre-selected, want only a manual agent-tools row")
	}
}

func TestPickerApply_ToggleAndConfirm(t *testing.T) {
	s := newPickerState(testCandidates, nil, "")

	s.apply(keyDown)
	s.apply(keySpace)
	if got := s.apply(keyEnter); got != pickerDone {
		t.Fatalf("apply(enter) = %v, want pickerDone", got)
	}
	if got, want := s.selectedSlugs(), []string{"new-verticals-insurance"}; !slices.Equal(got, want) {
		t.Errorf("selectedSlugs() = %v, want %v", got, want)
	}
}

func TestPickerApply_EnterWithNothingSelectedIsRejected(t *testing.T) {
	s := newPickerState(testCandidates, nil, "")

	if got := s.apply(keyEnter); got != pickerContinue {
		t.Fatalf("apply(enter) = %v, want pickerContinue", got)
	}
	if s.hint == "" {
		t.Error("hint is empty, want a nudge to select something")
	}
	s.apply(keySpace)
	if s.hint != "" {
		t.Errorf("hint = %q, want it cleared by the next key", s.hint)
	}
}

func TestPickerApply_CursorClampsAndReachesManualRow(t *testing.T) {
	s := newPickerState(testCandidates, nil, "")

	s.apply(keyUp)
	if s.cursor != 0 {
		t.Errorf("cursor after up at top = %d, want 0", s.cursor)
	}
	for range 5 {
		s.apply(keyDown)
	}
	if !s.manualRow() {
		t.Fatalf("cursor = %d, want the manual row (%d)", s.cursor, len(s.items))
	}
	if got := s.apply(keyEnter); got != pickerManual {
		t.Errorf("apply(enter) on manual row = %v, want pickerManual", got)
	}
	if got := s.apply(keySpace); got != pickerManual {
		t.Errorf("apply(space) on manual row = %v, want pickerManual", got)
	}
}

func TestPickerApply_QuitAborts(t *testing.T) {
	s := newPickerState(testCandidates, nil, "")
	if got := s.apply(keyQuit); got != pickerAbort {
		t.Errorf("apply(quit) = %v, want pickerAbort", got)
	}
}

func TestPickerAddManual(t *testing.T) {
	s := newPickerState(testCandidates, nil, "")
	s.cursor = len(s.items)

	s.addManual([]string{"agent-tools", "new-verticals-insurance", "atlas", "atlas"})

	if got, want := s.selectedSlugs(), []string{"new-verticals-insurance", "agent-tools", "atlas"}; !slices.Equal(got, want) {
		t.Errorf("selectedSlugs() = %v, want %v", got, want)
	}
	if s.items[0].selected {
		t.Error("AgentTools row selected, want typing agent-tools to select only that slug")
	}
	if len(s.items) != 4 {
		t.Errorf("items = %+v, want manual rows added for agent-tools and atlas only", s.items)
	}
}

func TestReadKey_LeavesFollowingBytesBuffered(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\x1b[B\ratlas\n"))

	for _, want := range []key{keyDown, keyEnter} {
		got, err := readKey(r)
		if err != nil || got != want {
			t.Fatalf("readKey() = %v, %v; want %v", got, err, want)
		}
	}
	line, err := r.ReadString('\n')
	if err != nil || line != "atlas\n" {
		t.Errorf("rest = %q, %v; want the pasted slug still buffered", line, err)
	}
}

func TestReadLine(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("atlas\rnext\r\nlast\n"))
	for _, want := range []string{"atlas", "next", "last"} {
		got, err := readLine(r)
		if err != nil || got != want {
			t.Fatalf("readLine() = %q, %v; want %q", got, err, want)
		}
	}
}

func TestReadKey_LoneEscDoesNotBlock(t *testing.T) {
	// An io.Reader that fails the test if read again after the Esc.
	r := bufio.NewReader(&oneShotReader{data: []byte{0x1b}, t: t})
	if got, err := readKey(r); err != nil || got != keyNone {
		t.Errorf("readKey() = %v, %v; want keyNone", got, err)
	}
}

type oneShotReader struct {
	data []byte
	t    *testing.T
	done bool
}

func (o *oneShotReader) Read(p []byte) (int, error) {
	if o.done {
		o.t.Fatal("readKey read past a lone Esc, which would block on a terminal")
	}
	o.done = true
	return copy(p, o.data), nil
}

func TestPhysicalRows(t *testing.T) {
	cases := []struct {
		line string
		cols int
		want int
	}{
		{"", 80, 1},
		{"abc", 3, 1},
		{"abcd", 3, 2},
		{"abcdefg", 3, 3},
		{"abcd", 0, 1},
		{"·→↓", 2, 2},
	}
	for _, c := range cases {
		if got := physicalRows(c.line, c.cols); got != c.want {
			t.Errorf("physicalRows(%q, %d) = %d, want %d", c.line, c.cols, got, c.want)
		}
	}
}

func TestRenderPicker(t *testing.T) {
	s := newPickerState(testCandidates, []string{"atlas"}, "Couldn't discover your teams.")
	s.apply(keySpace)

	got := renderPicker(s)
	for _, want := range []string{
		"Couldn't discover your teams.\r\n",
		"> [x] AgentTools  (agenttools, agent-tools · 115 stacks)\r\n",
		"  [ ] New Verticals & Insurance  (new-verticals-insurance · 43 stacks)\r\n",
		"  [x] atlas  (added manually)\r\n",
		"  + Type team slugs manually...\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderPicker() missing %q in:\n%s", want, got)
		}
	}
}

func TestParseTeamList(t *testing.T) {
	got := parseTeamList(" atlas, ,ecommerce,atlas ")
	if want := []string{"atlas", "ecommerce"}; !slices.Equal(got, want) {
		t.Errorf("parseTeamList() = %v, want %v", got, want)
	}
}
