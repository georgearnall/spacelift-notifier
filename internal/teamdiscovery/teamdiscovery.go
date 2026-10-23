// Package teamdiscovery works out which Spacelift team folders the caller
// belongs to, by matching their identity-provider groups (viewer.teams)
// against the folder:owning-team/<slug> and folder:collab-team/<slug>
// labels actually in use on stacks.
package teamdiscovery

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// labelPrefixes are the two team label conventions, in the same order
// teamconfig.Labels derives them.
var labelPrefixes = []string{"folder:owning-team/", "folder:collab-team/"}

// queryer is the minimal surface Discover needs from a Spacelift client.
// *spaceclient.Client satisfies this directly.
type queryer interface {
	Query(ctx context.Context, out any, vars map[string]any) error
}

// Candidate is one of the caller's groups that matches at least one team
// label in use.
type Candidate struct {
	Name   string   // identity-provider group name, e.g. "New Verticals & Insurance"
	Slugs  []string // matching label slugs, e.g. ["agenttools", "agent-tools"]
	Stacks int      // total stacks carrying any of Slugs' labels
}

// SearchSuggestionsInput mirrors Spacelift's GraphQL input type of the
// same name. As with pending.SearchInput, the Go type name must match
// exactly: the graphql library derives the $input variable's type from it.
type SearchSuggestionsInput struct {
	Fields []string `json:"fields"`
}

// discoverQuery fetches the viewer's groups and every stack label with its
// stack count in a single request.
type discoverQuery struct {
	Viewer struct {
		Teams []string `graphql:"teams"`
	} `graphql:"viewer"`
	Suggestions struct {
		Fields []suggestionField `graphql:"fields"`
	} `graphql:"searchStacksSuggestions(input: $input)"`
}

type suggestionField struct {
	Name                 string        `graphql:"name"`
	PossibleValuesString *stringValues `graphql:"possibleValuesString"`
}

type stringValues struct {
	Values []string `graphql:"values"`
	Counts []int    `graphql:"counts"`
}

// Discover returns the caller's groups that match a team label in use,
// most stacks first.
func Discover(ctx context.Context, c queryer) ([]Candidate, error) {
	var q discoverQuery
	vars := map[string]any{"input": SearchSuggestionsInput{Fields: []string{"label"}}}
	if err := c.Query(ctx, &q, vars); err != nil {
		return nil, fmt.Errorf("discovering teams: %w", err)
	}

	for _, f := range q.Suggestions.Fields {
		if f.Name == "label" && f.PossibleValuesString != nil {
			v := f.PossibleValuesString
			return match(q.Viewer.Teams, v.Values, v.Counts), nil
		}
	}
	return nil, nil
}

// match pairs each group with the team label slugs whose normalised form
// equals the group's. counts[i] is the stack count for labels[i].
func match(teams, labels []string, counts []int) []Candidate {
	type slugInfo struct {
		slug   string
		stacks int
	}
	byKey := map[string][]slugInfo{}
	for i, l := range labels {
		for _, p := range labelPrefixes {
			slug, ok := strings.CutPrefix(l, p)
			if !ok || slug == "" {
				continue
			}
			key := normalise(slug)
			n := 0
			if i < len(counts) {
				n = counts[i]
			}
			infos := byKey[key]
			j := slices.IndexFunc(infos, func(s slugInfo) bool { return s.slug == slug })
			if j < 0 {
				infos = append(infos, slugInfo{slug: slug})
				j = len(infos) - 1
			}
			infos[j].stacks += n
			byKey[key] = infos
		}
	}

	var out []Candidate
	seen := map[string]bool{}
	for _, t := range teams {
		key := normalise(t)
		infos := byKey[key]
		if key == "" || seen[key] || len(infos) == 0 {
			continue
		}
		seen[key] = true
		slices.SortStableFunc(infos, func(a, b slugInfo) int { return cmp.Compare(b.stacks, a.stacks) })
		c := Candidate{Name: t}
		for _, s := range infos {
			c.Slugs = append(c.Slugs, s.slug)
			c.Stacks += s.stacks
		}
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b Candidate) int { return cmp.Compare(b.Stacks, a.Stacks) })
	return out
}

// normalise lowercases s and drops everything but letters and digits, so
// "New Verticals & Insurance" and "new-verticals-insurance" compare equal.
func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
