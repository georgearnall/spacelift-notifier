// Package pending finds Spacelift runs that are awaiting confirmation,
// belong to the caller's team, and that the caller is actually allowed to
// confirm.
package pending

import (
	"context"
	"fmt"
	"time"

	"github.com/georgearnall/spacelift-notifier/internal/labels"
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
	EnumEquals []string `json:"enumEquals,omitempty"`
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

// Poll queries every run currently in the UNCONFIRMED state, paginating as
// needed, and returns only those the caller can actually confirm
// (Run.canConfirm) and whose stack carries one of the configured team
// labels.
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
				Predicates: []searchQueryPredicate{{
					Field:      "state",
					Constraint: searchQueryFieldConstraint{EnumEquals: []string{"UNCONFIRMED"}},
				}},
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
				StackName:    stack.Name,
				SpaceName:    stack.SpaceDetails.Name,
				Title:        run.Title,
				Branch:       run.Branch,
				CreatedAt:    time.Unix(run.CreatedAt, 0),
				UpdatedAt:    time.Unix(run.UpdatedAt, 0),
				MatchedLabel: matchedOn,
				RunURL:       c.RunURL(stack.ID, run.ID),
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
