package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestIsUnauthorizedErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"unauthorized error", errors.New("unauthorized: You can re-login using `spacectl profile login`"), true},
		{"lowercase unauthorized error", errors.New("unauthorized: you can re-login using `spacectl profile login`"), true},
		{"permission error is not relogin-able", errors.New("unauthorized: You're logged in. Maybe you don't have access to the resource?"), false},
		{"bare capitalized unauthorized (raw 401 pass-through)", errors.New(`non-200 OK status code: 401 Unauthorized body: ""`), true},
		{"unrelated error", errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isUnauthorizedErr(c.err); got != c.want {
				t.Errorf("isUnauthorizedErr(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestDoPoll_SetsAuthExpiredOnUnauthorizedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"message":"unauthorized"}]}`)
	}))
	defer srv.Close()

	sdk := client.New(srv.Client(), fakeSession{endpoint: srv.URL})
	c := spaceclient.NewFromSDK(sdk)
	st := state.New()

	res := doPoll(context.Background(), c, st, config{})
	if res.err == nil {
		t.Fatal("doPoll() error = nil, want an error")
	}
	if !res.authExpired {
		t.Errorf("doPoll() authExpired = false, want true for error %q", res.err)
	}
}

// TestDoPoll_SetsAuthExpiredOnBareUnauthorizedError covers a 401 whose body
// happens not to contain the word "unauthorized" at all - representative
// of a real Spacelift 401, as opposed to the previous test's mock body,
// which spells it out by coincidence. In this case the underlying
// spacelift-io/graphql client's own error text ("non-200 OK status code:
// 401 Unauthorized ...") is all there is to detect: capital-U
// "Unauthorized", not the lowercase text the SDK's own determineClientError
// looks for, so this exercises the fallback path where that upstream
// check never fires and the raw message passes straight through.
func TestDoPoll_SetsAuthExpiredOnBareUnauthorizedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // empty body - no lowercase "unauthorized" anywhere
	}))
	defer srv.Close()

	sdk := client.New(srv.Client(), fakeSession{endpoint: srv.URL})
	c := spaceclient.NewFromSDK(sdk)
	st := state.New()

	res := doPoll(context.Background(), c, st, config{})
	if res.err == nil {
		t.Fatal("doPoll() error = nil, want an error")
	}
	if !res.authExpired {
		t.Errorf("doPoll() authExpired = false, want true for error %q", res.err)
	}
}

// withFakeSpacectlLogin substitutes runSpacectlLogin for the duration of
// the test, so relogin's control flow can be exercised without actually
// shelling out to the spacectl binary.
func withFakeSpacectlLogin(t *testing.T, err error) {
	t.Helper()
	orig := runSpacectlLogin
	t.Cleanup(func() { runSpacectlLogin = orig })
	runSpacectlLogin = func() error { return err }
}

func alwaysSupported() error { return nil }

func TestApplyReloginKey_NotAuthExpiredIsNoOp(t *testing.T) {
	called := false
	last := pollResult{authExpired: false, err: errors.New("some other poll error")}

	result, resetTimer, handled := applyReloginKey(last, func() error { called = true; return nil })

	if called {
		t.Error("applyReloginKey() called relogin despite authExpired being false")
	}
	if handled {
		t.Error("applyReloginKey() handled = true, want false when authExpired is false")
	}
	if resetTimer {
		t.Error("applyReloginKey() resetTimer = true, want false when authExpired is false")
	}
	if !errors.Is(result.err, last.err) || result.authExpired != last.authExpired || result.reloginErr != last.reloginErr {
		t.Errorf("applyReloginKey() result = %+v, want the input unchanged: %+v", result, last)
	}
}

func TestApplyReloginKey_SuccessClearsReloginErrAndResetsTimer(t *testing.T) {
	last := pollResult{authExpired: true, reloginErr: errors.New("stale error from a previous attempt")}

	result, resetTimer, handled := applyReloginKey(last, func() error { return nil })

	if !handled {
		t.Error("applyReloginKey() handled = false, want true when authExpired is true")
	}
	if !resetTimer {
		t.Error("applyReloginKey() resetTimer = false, want true on a successful relogin")
	}
	if result.reloginErr != nil {
		t.Errorf("applyReloginKey() reloginErr = %v, want nil after a successful relogin", result.reloginErr)
	}
	if !result.authExpired {
		t.Error("applyReloginKey() cleared authExpired; it should only be cleared by the next real poll")
	}
}

func TestApplyReloginKey_FailureSetsReloginErrWithoutResettingTimer(t *testing.T) {
	last := pollResult{authExpired: true}
	wantErr := errors.New("spacectl not found")

	result, resetTimer, handled := applyReloginKey(last, func() error { return wantErr })

	if !handled {
		t.Error("applyReloginKey() handled = false, want true when authExpired is true")
	}
	if resetTimer {
		t.Error("applyReloginKey() resetTimer = true, want false when relogin fails")
	}
	if !errors.Is(result.reloginErr, wantErr) {
		t.Errorf("applyReloginKey() reloginErr = %v, want %v", result.reloginErr, wantErr)
	}
	if !result.authExpired {
		t.Error("applyReloginKey() authExpired = false, want it to stay true so the user can retry with l")
	}
}

func TestRelogin_Success(t *testing.T) {
	withFakeSpacectlLogin(t, nil)

	var restored, reauthed bool
	err := relogin(context.Background(), alwaysSupported, func(context.Context) error {
		reauthed = true
		return nil
	}, func() { restored = true })

	if err != nil {
		t.Fatalf("relogin() error = %v", err)
	}
	if !restored {
		t.Error("relogin() did not call restoreTerminal")
	}
	if !reauthed {
		t.Error("relogin() did not call reauth after a successful login")
	}
}

func TestRelogin_NotSupportedSkipsLoginAndReauth(t *testing.T) {
	wantErr := errors.New("session is authenticated via SPACELIFT_API_TOKEN")
	loginRan, restored, reauthed := false, false, false
	withFakeSpacectlLogin(t, nil)
	orig := runSpacectlLogin
	t.Cleanup(func() { runSpacectlLogin = orig })
	runSpacectlLogin = func() error { loginRan = true; return nil }

	err := relogin(context.Background(), func() error { return wantErr }, func(context.Context) error {
		reauthed = true
		return nil
	}, func() { restored = true })

	if !errors.Is(err, wantErr) {
		t.Errorf("relogin() error = %v, want %v", err, wantErr)
	}
	if loginRan {
		t.Error("relogin() shelled out to spacectl despite checkSupported rejecting it")
	}
	if restored {
		t.Error("relogin() touched the terminal despite checkSupported rejecting it")
	}
	if reauthed {
		t.Error("relogin() called reauth despite checkSupported rejecting it")
	}
}

func TestRelogin_LoginCommandFails(t *testing.T) {
	wantErr := errors.New("spacectl not found")
	withFakeSpacectlLogin(t, wantErr)

	reauthed := false
	err := relogin(context.Background(), alwaysSupported, func(context.Context) error {
		reauthed = true
		return nil
	}, func() {})

	if err == nil {
		t.Fatal("relogin() error = nil, want the login command's error to propagate")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("relogin() error = %v, want it to wrap %v", err, wantErr)
	}
	if reauthed {
		t.Error("relogin() called reauth despite the login command failing")
	}
}

func TestRelogin_ReauthFails(t *testing.T) {
	withFakeSpacectlLogin(t, nil)

	wantErr := errors.New("loading spacelift session: boom")
	err := relogin(context.Background(), alwaysSupported, func(context.Context) error {
		return wantErr
	}, func() {})

	if !errors.Is(err, wantErr) {
		t.Errorf("relogin() error = %v, want %v", err, wantErr)
	}
}

func TestRunSpacectlLogin_KilledOnInterrupt(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	orig := spacectlLoginCommand
	t.Cleanup(func() { spacectlLoginCommand = orig })
	spacectlLoginCommand = func() *exec.Cmd { return exec.Command("sleep", "30") }

	// Synchronize on the interrupt handler actually being registered,
	// rather than guessing with a sleep: signalling too early would hit
	// the process's default disposition for the signal instead of
	// runSpacectlLogin's handler, which - for an unhandled os.Interrupt -
	// would terminate the entire test binary rather than just failing
	// this test.
	registered := make(chan struct{})
	origHook := afterSignalRegistered
	t.Cleanup(func() { afterSignalRegistered = origHook })
	afterSignalRegistered = func() { close(registered) }

	done := make(chan error, 1)
	go func() { done <- runSpacectlLogin() }()

	select {
	case <-registered:
	case <-time.After(5 * time.Second):
		t.Fatal("runSpacectlLogin() never registered its interrupt handler")
	}

	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("os.FindProcess() error = %v", err)
	}
	if err := self.Signal(os.Interrupt); err != nil {
		t.Fatalf("signalling self: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("runSpacectlLogin() error = nil, want an interrupted error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runSpacectlLogin() did not return after an interrupt; subprocess likely left running")
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
