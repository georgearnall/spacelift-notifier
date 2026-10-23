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
	resp discoverQuery
	err  error
	vars map[string]any
}

func (f *fakeClient) Query(_ context.Context, out any, vars map[string]any) error {
	f.vars = vars
	if f.err != nil {
		return f.err
	}
	*out.(*discoverQuery) = f.resp
	return nil
}

func TestDiscover(t *testing.T) {
	var resp discoverQuery
	resp.Viewer.Teams = []string{"Hydra"}
	resp.Suggestions.Fields = []suggestionField{
		{Name: "name"},
		{Name: "label", PossibleValuesString: &stringValues{
			Values: []string{"folder:owning-team/hydra"},
			Counts: []int{2},
		}},
	}
	fc := &fakeClient{resp: resp}

	got, err := Discover(context.Background(), fc)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	want := []Candidate{{Name: "Hydra", Slugs: []string{"hydra"}, Stacks: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover() = %+v, want %+v", got, want)
	}
	in, ok := fc.vars["input"].(SearchSuggestionsInput)
	if !ok || !reflect.DeepEqual(in.Fields, []string{"label"}) {
		t.Errorf("Discover() input = %#v, want fields [label]", fc.vars["input"])
	}
}

func TestDiscover_QueryError(t *testing.T) {
	if _, err := Discover(context.Background(), &fakeClient{err: errors.New("boom")}); err == nil {
		t.Fatal("Discover() error = nil, want one")
	}
}
