package teamdiscovery

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestMatch(t *testing.T) {
	teams := []string{"AgentTools", "New Verticals & Insurance", "Trainline", "Hydra", "agent-tools"}
	labels := []string{
		"folder:owning-team/new-verticals-insurance",
		"folder:collab-team/new-verticals-insurance",
		"folder:owning-team/agent-tools",
		"folder:owning-team/agenttools",
		"folder:collab-team/agenttools",
		"folder:owning-team/hydra",
		"folder:owning-team/atlas",
		"trainline", // not a team label, must not match the Trainline group
		"folder:owning-team/",
	}
	counts := []int{24, 19, 39, 14, 54, 2, 100, 500, 3}

	got := match(teams, labels, counts)
	want := []Candidate{
		{Name: "AgentTools", Slugs: []string{"agenttools", "agent-tools"}, Stacks: 107},
		{Name: "New Verticals & Insurance", Slugs: []string{"new-verticals-insurance"}, Stacks: 43},
		{Name: "Hydra", Slugs: []string{"hydra"}, Stacks: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("match() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestMatch_NoTeams(t *testing.T) {
	if got := match(nil, []string{"folder:owning-team/atlas"}, []int{1}); got != nil {
		t.Errorf("match() with no teams = %+v, want nil", got)
	}
}

type fakeClient struct {
	resp   discoverQuery
	counts map[string]int // first label of a count predicate -> filteredCount
	err    error
	inputs []SearchSuggestionsInput
}

func (f *fakeClient) Query(_ context.Context, out any, vars map[string]any) error {
	in := vars["input"].(SearchSuggestionsInput)
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return f.err
	}
	switch q := out.(type) {
	case *discoverQuery:
		*q = f.resp
	case *countQuery:
		q.Suggestions.FilteredCount = f.counts[in.Predicates[0].Constraint.StringEquals[0]]
	}
	return nil
}

func TestDiscover(t *testing.T) {
	var resp discoverQuery
	resp.Viewer.Teams = []string{"Hydra", "Prometheus"}
	resp.Suggestions.Fields = []suggestionField{
		{Name: "name"},
		{Name: "label", PossibleValuesString: &stringValues{
			Values: []string{"folder:owning-team/hydra", "folder:owning-team/prometheus", "folder:collab-team/prometheus"},
			Counts: []int{25, 16, 15},
		}},
	}
	// Prometheus' labels overlap: 31 label matches, 20 distinct stacks,
	// which drops it below Hydra.
	fc := &fakeClient{resp: resp, counts: map[string]int{
		"folder:owning-team/hydra":      25,
		"folder:owning-team/prometheus": 20,
	}}

	got, err := Discover(context.Background(), fc)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	want := []Candidate{
		{Name: "Hydra", Slugs: []string{"hydra"}, Stacks: 25},
		{Name: "Prometheus", Slugs: []string{"prometheus"}, Stacks: 20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover() = %+v, want %+v", got, want)
	}

	if len(fc.inputs) != 3 || !reflect.DeepEqual(fc.inputs[0].Fields, []string{"label"}) {
		t.Fatalf("Discover() inputs = %+v, want a label suggestions query then one count per candidate", fc.inputs)
	}
	wantLabels := []string{"folder:owning-team/prometheus", "folder:collab-team/prometheus"}
	if got := fc.inputs[1].Predicates[0].Constraint.StringEquals; !reflect.DeepEqual(got, wantLabels) {
		t.Errorf("count predicate = %v, want %v", got, wantLabels)
	}
}

func TestDiscover_QueryError(t *testing.T) {
	if _, err := Discover(context.Background(), &fakeClient{err: errors.New("boom")}); err == nil {
		t.Fatal("Discover() error = nil, want one")
	}
}
