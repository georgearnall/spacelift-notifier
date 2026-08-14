package spaceclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"
)

// fakeSession implements session.Session, pointing requests at an
// httptest.Server instead of a real Spacelift account.
type fakeSession struct {
	endpoint string
}

func (f fakeSession) BearerToken(context.Context) (string, error) { return "test-token", nil }
func (f fakeSession) Endpoint() string                            { return f.endpoint }
func (f fakeSession) Type() session.CredentialsType               { return session.CredentialsTypeAPIToken }

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	sdk := client.New(srv.Client(), fakeSession{endpoint: srv.URL})
	return NewFromSDK(sdk)
}

func TestClient_Viewer(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"viewer":{"id":"georgearnall","name":"George Arnall","admin":false}}}`)
	})

	v, err := c.Viewer(context.Background())
	if err != nil {
		t.Fatalf("Viewer() error = %v", err)
	}
	want := Viewer{ID: "georgearnall", Name: "George Arnall", Admin: false}
	if v != want {
		t.Errorf("Viewer() = %+v, want %+v", v, want)
	}
}

func TestClient_Viewer_PropagatesError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"errors":[{"message":"boom"}]}`)
	})

	if _, err := c.Viewer(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClient_URLs(t *testing.T) {
	sdk := client.New(http.DefaultClient, fakeSession{endpoint: "https://trainline-private.app.spacelift.io/"})
	c := NewFromSDK(sdk)

	if got, want := c.StackURL("applepay-api-prod"), "https://trainline-private.app.spacelift.io/stack/applepay-api-prod"; got != want {
		t.Errorf("StackURL() = %q, want %q", got, want)
	}
	if got, want := c.RunURL("applepay-api-prod", "01KZZTZETM9BP9E3XHNH0813NE"), "https://trainline-private.app.spacelift.io/stack/applepay-api-prod/run/01KZZTZETM9BP9E3XHNH0813NE"; got != want {
		t.Errorf("RunURL() = %q, want %q", got, want)
	}
}

func TestClient_Query_CountsRequests(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"data":{}}`)
	})

	for i := 0; i < 3; i++ {
		var out struct{}
		if err := c.Query(context.Background(), &out, nil); err != nil {
			t.Fatalf("Query() error = %v", err)
		}
	}

	total, window := c.Stats()
	if total != 3 || window != 3 {
		t.Errorf("Stats() = (%d, %d), want (3, 3)", total, window)
	}
	if calls != 3 {
		t.Errorf("server saw %d requests, want 3", calls)
	}
}

func TestClient_Query_WindowResetsAfterAnHour(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{}}`)
	})

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	var out struct{}
	if err := c.Query(context.Background(), &out, nil); err != nil {
		t.Fatal(err)
	}
	if _, window := c.Stats(); window != 1 {
		t.Fatalf("window after first query = %d, want 1", window)
	}

	// Still within the same hour: window count keeps accumulating.
	now = now.Add(30 * time.Minute)
	if err := c.Query(context.Background(), &out, nil); err != nil {
		t.Fatal(err)
	}
	if total, window := c.Stats(); total != 2 || window != 2 {
		t.Fatalf("Stats() after second query = (%d, %d), want (2, 2)", total, window)
	}

	// An hour has elapsed: window resets, but the lifetime total keeps growing.
	now = now.Add(time.Hour)
	if err := c.Query(context.Background(), &out, nil); err != nil {
		t.Fatal(err)
	}
	if total, window := c.Stats(); total != 3 || window != 1 {
		t.Fatalf("Stats() after window reset = (%d, %d), want (3, 1)", total, window)
	}
}

func TestClient_Query_MarshalsVariables(t *testing.T) {
	var gotBody map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		fmt.Fprint(w, `{"data":{}}`)
	})

	var out struct{}
	vars := map[string]any{"input": map[string]any{"first": 50}}
	if err := c.Query(context.Background(), &out, vars); err != nil {
		t.Fatal(err)
	}

	gotVars, ok := gotBody["variables"].(map[string]any)
	if !ok {
		t.Fatalf("request body had no variables field: %v", gotBody)
	}
	gotInput, ok := gotVars["input"].(map[string]any)
	if !ok || gotInput["first"] != float64(50) {
		t.Errorf("variables.input = %v, want {first: 50}", gotVars["input"])
	}
}
