// Package pending finds Spacelift runs that are awaiting confirmation,
// belong to the caller's team, and that the caller is actually allowed to
// confirm.
package pending

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/georgearnall/spacewatch/internal/labels"
)

const pageSize = 50

// queryer is the minimal surface pending.Poll needs from a Spacelift
// client. *spaceclient.Client satisfies this directly; tests use
// hand-written fakes so pagination/filtering logic can be exercised
// without any HTTP involved.
type queryer interface {
	Query(ctx context.Context, out any, vars map[string]any) error
	RunURL(stackID, runID string) string
}

// PendingConfirmation describes one run awaiting confirmation that
// belongs to the caller's team and that the caller can act on.
type PendingConfirmation struct {
	RunID        string
	StackID      string
	StackName    string
	SpaceName    string
	Title        string
	Branch       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MatchedLabel string
	RunURL       string
	// Plan is the resource counts from the run's plan, or nil if the run
	// has no plan delta.
	Plan *Plan
}

// Plan summarizes the resources a run's plan will add, change and destroy.
type Plan struct {
	Add, Change, Delete int
}

// SearchInput mirrors Spacelift's SearchInput GraphQL input type. Its Go
// type name must be exactly "SearchInput": the graphql library derives the
// GraphQL type name for the $input query variable from this Go type's
// name via reflection, not from any struct tag, so renaming this type
// would silently break the query sent to the server.
type SearchInput struct {
	First      *int                   `json:"first,omitempty"`
	After      *string                `json:"after,omitempty"`
	Predicates []searchQueryPredicate `json:"predicates,omitempty"`
}

type searchQueryPredicate struct {
	Field      string                     `json:"field"`
	Constraint searchQueryFieldConstraint `json:"constraint"`
}

type searchQueryFieldConstraint struct {
	EnumEquals   []string `json:"enumEquals,omitempty"`
	StringEquals []string `json:"stringEquals,omitempty"`
}

// searchRunsQuery mirrors the fields of Query.searchRuns actually used
// here, confirmed live against a real Spacelift account during design.
// The nested types are named (rather than anonymous) purely so tests can
// construct fake responses without repeating the full field list.
type searchRunsQuery struct {
	SearchRuns struct {
		PageInfo pageInfo  `graphql:"pageInfo"`
		Edges    []runEdge `graphql:"edges"`
	} `graphql:"searchRuns(input: $input)"`
}

type pageInfo struct {
	EndCursor   string `graphql:"endCursor"`
	HasNextPage bool   `graphql:"hasNextPage"`
}

type runEdge struct {
	Node runNode `graphql:"node"`
}

type runNode struct {
	Run   runFields   `graphql:"run"`
	Stack stackFields `graphql:"stack"`
}

type runFields struct {
	ID         string `graphql:"id"`
	CanConfirm bool   `graphql:"canConfirm"`
	Title      string `graphql:"title"`
	Branch     string `graphql:"branch"`
	CreatedAt  int64  `graphql:"createdAt"`
	UpdatedAt  int64  `graphql:"updatedAt"`
	Delta      *delta `graphql:"delta"`
}

type delta struct {
	AddCount    int `graphql:"addCount"`
	ChangeCount int `graphql:"changeCount"`
	DeleteCount int `graphql:"deleteCount"`
}

type stackFields struct {
	ID           string      `graphql:"id"`
	Name         string      `graphql:"name"`
	Labels       []string    `graphql:"labels"`
	SpaceDetails spaceFields `graphql:"spaceDetails"`
}

type spaceFields struct {
	Name string `graphql:"name"`
}

// Poll queries runs currently in the UNCONFIRMED state on stacks carrying
// one of the configured team labels, paginating as needed, and returns
// only those the caller can actually confirm (Run.canConfirm).
func Poll(ctx context.Context, c queryer, cfg labels.Config) ([]PendingConfirmation, error) {
	var (
		results []PendingConfirmation
		after   *string
	)

	for {
		first := pageSize
		vars := map[string]any{
			"input": SearchInput{
				First: &first,
				After: after,
				Predicates: []searchQueryPredicate{
					{
						Field:      "state",
						Constraint: searchQueryFieldConstraint{EnumEquals: []string{"UNCONFIRMED"}},
					},
					// Filters server-side on the stack's labels: exact match,
					// OR across values (verified against the live API). Without
					// it every unconfirmed run in the account is paginated
					// through and filtered here. labels.Match below still runs
					// to pick MatchedLabel.
					{
						Field:      "labels",
						Constraint: searchQueryFieldConstraint{StringEquals: cfg.Labels},
					},
				},
			},
		}

		var q searchRunsQuery
		if err := c.Query(ctx, &q, vars); err != nil {
			return nil, fmt.Errorf("querying pending runs: %w", err)
		}

		for _, edge := range q.SearchRuns.Edges {
			run, stack := edge.Node.Run, edge.Node.Stack
			if !run.CanConfirm {
				continue
			}
			matched, matchedOn := labels.Match(stack.Labels, cfg)
			if !matched {
				continue
			}
			results = append(results, PendingConfirmation{
				RunID:        run.ID,
				StackID:      stack.ID,
				StackName:    sanitize(stack.Name),
				SpaceName:    sanitize(stack.SpaceDetails.Name),
				Title:        sanitize(run.Title),
				Branch:       sanitize(run.Branch),
				CreatedAt:    time.Unix(run.CreatedAt, 0),
				UpdatedAt:    time.Unix(run.UpdatedAt, 0),
				MatchedLabel: matchedOn,
				RunURL:       c.RunURL(stack.ID, run.ID),
				Plan:         planFrom(run.Delta),
			})
		}

		if !q.SearchRuns.PageInfo.HasNextPage || q.SearchRuns.PageInfo.EndCursor == "" {
			break
		}
		cursor := q.SearchRuns.PageInfo.EndCursor
		after = &cursor
	}

	return results, nil
}

func planFrom(d *delta) *Plan {
	if d == nil {
		return nil
	}
	return &Plan{Add: d.AddCount, Change: d.ChangeCount, Delete: d.DeleteCount}
}

// sanitize replaces control characters and bidi formatting characters
// with spaces. Run titles are commit messages, so anyone who can push
// controls them; left as-is, an embedded newline breaks the table and an
// escape sequence (OSC 52 clipboard write, OSC 8 link spoofing, or an
// early terminator inside the OSC 9 notification) would be interpreted
// by the user's terminal.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return ' '
		}
		return r
	}, s)
}
