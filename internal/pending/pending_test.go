package pending

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/georgearnall/spacewatch/internal/labels"
	"github.com/georgearnall/spacewatch/internal/spaceclient"
	"github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"
)

// fakeClient is a hand-written, HTTP-free stand-in for *spaceclient.Client,
// used to test pagination and filtering logic in isolation.
type fakeClient struct {
	responses []searchRunsQuery
	calls     int
	gotAfter  []*string
	gotInput  []SearchInput
	queryErr  error
}

func (f *fakeClient) Query(_ context.Context, out any, vars map[string]any) error {
	if f.queryErr != nil {
		return f.queryErr
	}
	input, _ := vars["input"].(SearchInput)
	f.gotAfter = append(f.gotAfter, input.After)
	f.gotInput = append(f.gotInput, input)

	q, ok := out.(*searchRunsQuery)
	if !ok {
		return fmt.Errorf("unexpected out type %T", out)
	}
	*q = f.responses[f.calls]
	f.calls++
	return nil
}

func (f *fakeClient) RunURL(stackID, runID string) string {
	return "https://example.test/stack/" + stackID + "/run/" + runID
}

func edge(runID string, canConfirm bool, stackLabels []string) runEdge {
	var e runEdge
	e.Node.Run.ID = runID
	e.Node.Run.CanConfirm = canConfirm
	e.Node.Stack.ID = "stack-" + runID
	e.Node.Stack.Name = "stack-" + runID
	e.Node.Stack.Labels = stackLabels
	return e
}

func TestPoll_FiltersByCanConfirmAndLabel(t *testing.T) {
	cfg := labels.Config{Labels: []string{"folder:owning-team/ecommerce"}}

	var resp searchRunsQuery
	resp.SearchRuns.Edges = []runEdge{
		edge("r1", true, []string{"folder:owning-team/ecommerce"}),  // included
		edge("r2", false, []string{"folder:owning-team/ecommerce"}), // excluded: can't confirm
		edge("r3", true, []string{"folder:owning-team/atlas"}),      // excluded: wrong team
	}

	fc := &fakeClient{responses: []searchRunsQuery{resp}}
	got, err := Poll(context.Background(), fc, cfg)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(got) != 1 || got[0].RunID != "r1" {
		t.Fatalf("Poll() = %+v, want only r1", got)
	}
	if got[0].RunURL != "https://example.test/stack/stack-r1/run/r1" {
		t.Errorf("RunURL = %q", got[0].RunURL)
	}
	if got[0].MatchedLabel != "folder:owning-team/ecommerce" {
		t.Errorf("MatchedLabel = %q", got[0].MatchedLabel)
	}
}

func TestPoll_Pagination(t *testing.T) {
	cfg := labels.Config{Labels: []string{"folder:owning-team/ecommerce"}}

	var page1, page2 searchRunsQuery
	page1.SearchRuns.PageInfo.HasNextPage = true
	page1.SearchRuns.PageInfo.EndCursor = "cursor-1"
	page1.SearchRuns.Edges = append(page1.SearchRuns.Edges, edge("r1", true, []string{"folder:owning-team/ecommerce"}))

	page2.SearchRuns.PageInfo.HasNextPage = false
	page2.SearchRuns.Edges = append(page2.SearchRuns.Edges, edge("r2", true, []string{"folder:owning-team/ecommerce"}))

	fc := &fakeClient{responses: []searchRunsQuery{page1, page2}}
	got, err := Poll(context.Background(), fc, cfg)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Poll() returned %d results, want 2 (across both pages)", len(got))
	}
	if fc.calls != 2 {
		t.Fatalf("expected 2 calls (one per page), got %d", fc.calls)
	}
	if fc.gotAfter[0] != nil {
		t.Errorf("first call's after = %v, want nil", fc.gotAfter[0])
	}
	if fc.gotAfter[1] == nil || *fc.gotAfter[1] != "cursor-1" {
		t.Errorf("second call's after = %v, want \"cursor-1\"", fc.gotAfter[1])
	}
}

func TestPoll_QueryErrorIsWrapped(t *testing.T) {
	fc := &fakeClient{queryErr: errors.New("boom")}
	_, err := Poll(context.Background(), fc, labels.Config{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Poll() error = %v, want wrapped \"boom\"", err)
	}
}

// fakeSession points a real spaceclient.Client at an httptest.Server, to
// prove the actual GraphQL query text/variables sent over the wire, and
// the decoding of a realistic response, both work end to end.
type fakeSession struct{ endpoint string }

func (f fakeSession) BearerToken(context.Context) (string, error) { return "test-token", nil }
func (f fakeSession) Endpoint() string                            { return f.endpoint }
func (f fakeSession) Type() session.CredentialsType               { return session.CredentialsTypeAPIToken }

func TestPoll_RealQueryShapeAgainstHTTPServer(t *testing.T) {
	const responseJSON = `{
		"data": {
			"searchRuns": {
				"pageInfo": {"endCursor": "", "hasNextPage": false},
				"edges": [{
					"node": {
						"run": {"id": "01K...RUN", "canConfirm": true, "title": "apply", "branch": "main", "createdAt": 1000, "updatedAt": 2000},
						"stack": {"id": "my-stack", "name": "my-stack", "labels": ["folder:owning-team/ecommerce"], "spaceDetails": {"name": "ecommerce"}}
					}
				}]
			}
		}
	}`

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		fmt.Fprint(w, responseJSON)
	}))
	defer srv.Close()

	sdk := client.New(srv.Client(), fakeSession{endpoint: srv.URL})
	c := spaceclient.NewFromSDK(sdk)

	got, err := Poll(context.Background(), c, labels.Config{Labels: []string{"folder:owning-team/ecommerce"}})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Poll() = %+v, want 1 result", got)
	}
	if got[0].StackName != "my-stack" || got[0].SpaceName != "ecommerce" {
		t.Errorf("Poll() result = %+v, missing expected stack/space name", got[0])
	}

	query, _ := gotBody["query"].(string)
	if !strings.Contains(query, "$input:SearchInput!") {
		t.Errorf("query %q missing $input:SearchInput! variable declaration", query)
	}
	if !strings.Contains(query, "searchRuns(input: $input)") {
		t.Errorf("query %q missing searchRuns(input: $input) call", query)
	}
	if !strings.Contains(query, "delta{addCount,changeCount,deleteCount}") {
		t.Errorf("query %q missing delta{addCount,changeCount,deleteCount}", query)
	}

	vars, _ := gotBody["variables"].(map[string]any)
	input, _ := vars["input"].(map[string]any)
	predicates, _ := input["predicates"].([]any)
	if len(predicates) != 2 {
		t.Fatalf("variables.input.predicates = %v, want 2 entries", input["predicates"])
	}
	predicate, _ := predicates[0].(map[string]any)
	if predicate["field"] != "state" {
		t.Errorf("predicate field = %v, want \"state\"", predicate["field"])
	}
	predicate, _ = predicates[1].(map[string]any)
	constraint, _ := predicate["constraint"].(map[string]any)
	if predicate["field"] != "labels" || fmt.Sprint(constraint["stringEquals"]) != "[folder:owning-team/ecommerce]" {
		t.Errorf("second predicate = %v, want labels stringEquals [folder:owning-team/ecommerce]", predicate)
	}
}

func TestPoll_SanitizesUntrustedText(t *testing.T) {
	cfg := labels.Config{Labels: []string{"team"}}
	e := edge("r1", true, []string{"team"})
	e.Node.Run.Title = "fix\nbug \x1b]52;c;ZXZpbA==\x07 ‮evil"
	e.Node.Stack.Name = "stack\x1b[31m"
	var page searchRunsQuery
	page.SearchRuns.Edges = []runEdge{e}

	got, err := Poll(context.Background(), &fakeClient{responses: []searchRunsQuery{page}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{got[0].Title, got[0].StackName} {
		for _, r := range s {
			if r < 0x20 || r == 0x7f || r == '‮' {
				t.Errorf("Poll() left control character %U in %q", r, s)
			}
		}
	}
	if got[0].Title != "fix bug  ]52;c;ZXZpbA==   evil" {
		t.Errorf("Title = %q", got[0].Title)
	}
}

func TestPoll_FiltersLabelsServerSide(t *testing.T) {
	cfg := labels.Config{Labels: []string{"folder:owning-team/a", "folder:collab-team/a"}}
	fc := &fakeClient{responses: []searchRunsQuery{{}}}
	if _, err := Poll(context.Background(), fc, cfg); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range fc.gotInput[0].Predicates {
		if p.Field == "labels" {
			got = p.Constraint.StringEquals
		}
	}
	if strings.Join(got, ",") != strings.Join(cfg.Labels, ",") {
		t.Errorf("labels predicate = %v, want stringEquals %v", got, cfg.Labels)
	}
}

func TestPoll_MapsPlanDelta(t *testing.T) {
	cfg := labels.Config{Labels: []string{"team"}}
	withDelta := edge("r1", true, []string{"team"})
	withDelta.Node.Run.Delta = &delta{AddCount: 2, ChangeCount: 3, DeleteCount: 1}
	noDelta := edge("r2", true, []string{"team"})
	var page searchRunsQuery
	page.SearchRuns.Edges = []runEdge{withDelta, noDelta}

	got, err := Poll(context.Background(), &fakeClient{responses: []searchRunsQuery{page}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Plan == nil || *got[0].Plan != (Plan{Add: 2, Change: 3, Delete: 1}) {
		t.Errorf("Plan = %+v, want +2 ~3 -1", got[0].Plan)
	}
	if got[1].Plan != nil {
		t.Errorf("Plan = %+v for a run without a delta, want nil", got[1].Plan)
	}
}

func TestIsMine(t *testing.T) {
	cases := []struct {
		name string
		p    PendingConfirmation
		want bool
	}{
		{"commit author", PendingConfirmation{AuthorLogin: "GeorgeArnall", TriggeredBy: "api::oidc::t::commerce"}, true},
		{"triggered directly", PendingConfirmation{AuthorLogin: "someone", TriggeredBy: "georgearnall"}, true},
		{"someone else", PendingConfirmation{AuthorLogin: "someone", TriggeredBy: "api::oidc::t::commerce"}, false},
		{"no author info", PendingConfirmation{}, false},
	}
	for _, c := range cases {
		if got := c.p.IsMine("georgearnall"); got != c.want {
			t.Errorf("%s: IsMine() = %v, want %v", c.name, got, c.want)
		}
	}
	if (PendingConfirmation{}).IsMine("") {
		t.Error("IsMine(\"\") = true for empty fields, want false")
	}
}
