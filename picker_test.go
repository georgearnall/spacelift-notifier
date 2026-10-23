package main

import (
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
	s := newPickerState(testCandidates, []string{"agent-tools", "atlas"}, "")

	if got, want := s.selectedSlugs(), []string{"agenttools", "agent-tools", "atlas"}; !slices.Equal(got, want) {
		t.Errorf("selectedSlugs() = %v, want %v", got, want)
	}
	if last := s.items[len(s.items)-1]; !last.manual || last.name != "atlas" {
		t.Errorf("last item = %+v, want manual row for atlas", last)
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

	s.addManual([]string{"agent-tools", "atlas", "atlas"})

	if got, want := s.selectedSlugs(), []string{"agenttools", "agent-tools", "atlas"}; !slices.Equal(got, want) {
		t.Errorf("selectedSlugs() = %v, want %v", got, want)
	}
	if len(s.items) != 3 {
		t.Errorf("items = %+v, want one manual row added for atlas", s.items)
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
