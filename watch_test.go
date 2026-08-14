package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/georgearnall/spacelift-notifier/internal/notify"
	"github.com/georgearnall/spacelift-notifier/internal/pending"
	"github.com/georgearnall/spacelift-notifier/internal/spaceclient"
	"github.com/georgearnall/spacelift-notifier/internal/state"
	"github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"
)

func TestNextInterval(t *testing.T) {
	cfg := config{activeInterval: 20 * time.Second, idleInterval: 60 * time.Second, requestBudget: 300}

	cases := []struct {
		name         string
		pendingCount int
		reqWindow    int
		want         time.Duration
	}{
		{"idle, budget untouched", 0, 10, 60 * time.Second},
		{"active, budget untouched", 3, 10, 20 * time.Second},
		{"idle, over budget floors to low-budget floor", 0, 300, lowBudgetFloor},
		{"active, over budget floors to low-budget floor (floor exceeds active interval)", 3, 300, lowBudgetFloor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nextInterval(cfg, c.pendingCount, c.reqWindow); got != c.want {
				t.Errorf("nextInterval() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNextInterval_ZeroBudgetDisablesFloor(t *testing.T) {
	cfg := config{activeInterval: 20 * time.Second, idleInterval: 60 * time.Second, requestBudget: 0}
	if got := nextInterval(cfg, 0, 999999); got != cfg.idleInterval {
		t.Errorf("nextInterval() with requestBudget=0 = %v, want idleInterval (floor disabled)", got)
	}
}

func TestRenderRows(t *testing.T) {
	now := time.Now()
	items := []pending.PendingConfirmation{
		{RunID: "r1", StackName: "orders-api", SpaceName: "ecommerce", Title: "apply", CreatedAt: now.Add(-5 * time.Minute), RunURL: "https://x/1"},
		{RunID: "r2", StackName: "no-space", MatchedLabel: "folder:owning-team/ecommerce", Title: "apply", CreatedAt: now.Add(-time.Hour), RunURL: "https://x/2"},
	}

	rows := renderRows(items, 1)
	if len(rows) != 2 {
		t.Fatalf("renderRows() returned %d rows, want 2", len(rows))
	}
	if rows[0].Team != "ecommerce" {
		t.Errorf("rows[0].Team = %q, want SpaceName \"ecommerce\"", rows[0].Team)
	}
	if rows[1].Team != "folder:owning-team/ecommerce" {
		t.Errorf("rows[1].Team = %q, want MatchedLabel fallback when SpaceName is empty", rows[1].Team)
	}
	if rows[0].Selected {
		t.Errorf("rows[0].Selected = true, want only index 1 selected")
	}
	if !rows[1].Selected {
		t.Errorf("rows[1].Selected = false, want index 1 selected")
	}
	if rows[0].Age != "5m" || rows[1].Age != "1h" {
		t.Errorf("ages = %q, %q, want 5m, 1h", rows[0].Age, rows[1].Age)
	}
}

func testNotifier(t *testing.T, sent *[]string) *notify.Notifier {
	t.Helper()
	return &notify.Notifier{
		Stdout:      &bytes.Buffer{},
		TermProgram: func() string { return "" },
		RunOSAScript: func(script string) error {
			*sent = append(*sent, script)
			return nil
		},
	}
}

func TestNotifyNewlyPending(t *testing.T) {
	var sent []string
	n := testNotifier(t, &sent)

	res := pollResult{
		items: []pending.PendingConfirmation{
			{RunID: "r1", StackName: "orders-api", Title: "apply"},
			{RunID: "r2", StackName: "billing-api", Title: "apply"},
		},
		newlyPendingIDs: []string{"r2", "does-not-exist"},
	}

	notifyNewlyPending(n, res)

	if len(sent) != 1 || !strings.Contains(sent[0], "billing-api") {
		t.Errorf("sent notifications = %v, want exactly one for billing-api", sent)
	}
}

func TestNotifyNewlyPending_NoneNewSendsNothing(t *testing.T) {
	var sent []string
	n := testNotifier(t, &sent)

	notifyNewlyPending(n, pollResult{items: []pending.PendingConfirmation{{RunID: "r1"}}})
	if len(sent) != 0 {
		t.Errorf("notifyNewlyPending() sent %v when nothing was newly pending", sent)
	}
}

func TestOpenURL(t *testing.T) {
	var gotURL string
	old := runOpen
	runOpen = func(url string) error { gotURL = url; return nil }
	defer func() { runOpen = old }()

	openURL("https://example.test/stack/x/run/y")
	if gotURL != "https://example.test/stack/x/run/y" {
		t.Errorf("runOpen called with %q", gotURL)
	}
}

func TestOpenURL_ErrorDoesNotPanic(t *testing.T) {
	old := runOpen
	runOpen = func(string) error { return errors.New("boom") }
	defer func() { runOpen = old }()

	openURL("https://example.test") // should just log to stderr, not panic
}

// fakeSession points a real spaceclient.Client at an httptest.Server.
type fakeSession struct{ endpoint string }

func (f fakeSession) BearerToken(context.Context) (string, error) { return "test-token", nil }
func (f fakeSession) Endpoint() string                            { return f.endpoint }
func (f fakeSession) Type() session.CredentialsType               { return session.CredentialsTypeAPIToken }

func TestDoPoll_NewlyPendingAndStats(t *testing.T) {
	const responseJSON = `{
		"data": {
			"searchRuns": {
				"pageInfo": {"endCursor": "", "hasNextPage": false},
				"edges": [{
					"node": {
						"run": {"id": "r1", "canConfirm": true, "title": "apply", "branch": "main", "createdAt": 1000, "updatedAt": 2000},
						"stack": {"id": "s1", "name": "orders-api", "labels": ["folder:owning-team/ecommerce"], "spaceDetails": {"name": "ecommerce"}}
					}
				}]
			}
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, responseJSON)
	}))
	defer srv.Close()

	sdk := client.New(srv.Client(), fakeSession{endpoint: srv.URL})
	c := spaceclient.NewFromSDK(sdk)
	st := state.New()
	st.Observe([]string{}, time.Now().Add(-time.Hour)) // establish a warm (non-cold) LastPoll

	cfg := config{}
	cfg.teamLabels.values = []string{"folder:owning-team/ecommerce"}

	res := doPoll(context.Background(), c, st, cfg)
	if res.err != nil {
		t.Fatalf("doPoll() error = %v", res.err)
	}
	if len(res.items) != 1 || res.items[0].RunID != "r1" {
		t.Fatalf("doPoll() items = %+v, want [r1]", res.items)
	}
	if len(res.newlyPendingIDs) != 1 || res.newlyPendingIDs[0] != "r1" {
		t.Errorf("doPoll() newlyPendingIDs = %v, want [r1]", res.newlyPendingIDs)
	}
	if res.reqTotal != 1 || res.reqWindow != 1 {
		t.Errorf("doPoll() stats = (%d, %d), want (1, 1)", res.reqTotal, res.reqWindow)
	}
}

func TestDoPoll_PropagatesQueryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"errors":[{"message":"boom"}]}`)
	}))
	defer srv.Close()

	sdk := client.New(srv.Client(), fakeSession{endpoint: srv.URL})
	c := spaceclient.NewFromSDK(sdk)
	st := state.New()

	res := doPoll(context.Background(), c, st, config{})
	if res.err == nil {
		t.Fatal("doPoll() error = nil, want the query error to propagate")
	}
}

func TestCRLF(t *testing.T) {
	in := "no pending confirmations for your team\npolled 12:00:00 · 0 pending\n"
	want := "no pending confirmations for your team\r\npolled 12:00:00 · 0 pending\r\n"
	if got := crlf(in); got != want {
		t.Errorf("crlf() = %q, want %q", got, want)
	}
}

func TestCRLF_DoesNotDoubleUpExistingCR(t *testing.T) {
	// Guards against a naive fix that would turn an already-correct
	// "\r\n" into "\r\r\n".
	in := "already\r\ncorrect\n"
	want := "already\r\ncorrect\r\n"
	if got := crlf(in); got != want {
		t.Errorf("crlf() = %q, want %q", got, want)
	}
}

func TestFooter_ContainsKeyStats(t *testing.T) {
	res := pollResult{polledAt: time.Now(), items: make([]pending.PendingConfirmation, 2), reqTotal: 5, reqWindow: 3}
	cfg := config{requestBudget: 300, activeInterval: 20 * time.Second, idleInterval: 60 * time.Second}

	got := footer(res, cfg, " · q to quit")
	for _, want := range []string{"2 pending", "5 total", "3 this hour", "budget 300/hr", "q to quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("footer() = %q, missing %q", got, want)
		}
	}
}
