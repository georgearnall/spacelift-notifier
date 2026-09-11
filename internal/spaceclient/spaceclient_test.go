package spaceclient

import (
	"context"
	"encoding/json"
	"errors"
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

// withFakeSession substitutes newSession for the duration of the test so
// Reauth can be exercised against an httptest.Server instead of a real
// Spacelift profile on disk.
func withFakeSession(t *testing.T, sess session.Session, err error) {
	t.Helper()
	orig := newSession
	t.Cleanup(func() { newSession = orig })
	newSession = func(context.Context, *http.Client) (session.Session, error) { return sess, err }
}

func TestClient_Reauth_PreservesRequestCounters(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{}}`)
	})

	var out struct{}
	if err := c.Query(context.Background(), &out, nil); err != nil {
		t.Fatal(err)
	}
	if total, window := c.Stats(); total != 1 || window != 1 {
		t.Fatalf("Stats() before Reauth = (%d, %d), want (1, 1)", total, window)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{}}`)
	}))
	defer srv2.Close()
	withFakeSession(t, fakeSession{endpoint: srv2.URL}, nil)

	if err := c.Reauth(context.Background()); err != nil {
		t.Fatalf("Reauth() error = %v", err)
	}
	if err := c.Query(context.Background(), &out, nil); err != nil {
		t.Fatal(err)
	}
	if total, window := c.Stats(); total != 2 || window != 2 {
		t.Errorf("Stats() after Reauth = (%d, %d), want (2, 2) - Reauth must preserve the request budget, not reset it", total, window)
	}
}

func TestClient_Reauth_PropagatesSessionError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	wantErr := errors.New("no current profile is set")
	withFakeSession(t, nil, wantErr)

	err := c.Reauth(context.Background())
	if err == nil {
		t.Fatal("Reauth() error = nil, want the session error to propagate")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Reauth() error = %v, want it to wrap %v", err, wantErr)
	}
}

// withIsolatedProfileDir points spacectl's profile manager at a fresh temp
// directory for the duration of the test, so it never touches the real
// ~/.spacelift/config.json.
func withIsolatedProfileDir(t *testing.T) *session.ProfileManager {
	t.Helper()
	t.Setenv(session.EnvSpaceliftConfigDirectory, t.TempDir())
	manager, err := session.UserProfileManager()
	if err != nil {
		t.Fatalf("session.UserProfileManager() error = %v", err)
	}
	return manager
}

func TestReloginSupported_EnvCredentialsRejected(t *testing.T) {
	for _, envVar := range []string{
		session.EnvSpaceliftAPIToken,
		session.EnvSpaceliftAPIKeyID,
		session.EnvSpaceliftAPIGitHubToken,
	} {
		t.Run(envVar, func(t *testing.T) {
			t.Setenv(envVar, "some-value")
			if err := ReloginSupported(); err == nil {
				t.Errorf("ReloginSupported() = nil, want an error when %s is set", envVar)
			}
		})
	}
}

func TestReloginSupported_NoProfileSelected(t *testing.T) {
	withIsolatedProfileDir(t)

	if err := ReloginSupported(); err == nil {
		t.Error("ReloginSupported() = nil, want an error when no profile is selected")
	}
}

func TestReloginSupported_NonAPITokenProfileRejected(t *testing.T) {
	manager := withIsolatedProfileDir(t)
	profile := &session.Profile{
		Alias: "work",
		Credentials: &session.StoredCredentials{
			Type:      session.CredentialsTypeAPIKey,
			Endpoint:  "https://example.app.spacelift.io",
			KeyID:     "key-id",
			KeySecret: "key-secret",
		},
	}
	if err := manager.Create(profile); err != nil {
		t.Fatalf("manager.Create() error = %v", err)
	}
	if err := manager.Select(profile.Alias); err != nil {
		t.Fatalf("manager.Select() error = %v", err)
	}

	if err := ReloginSupported(); err == nil {
		t.Error("ReloginSupported() = nil, want an error for a non-API-Token profile")
	}
}

func TestReloginSupported_APITokenProfileAccepted(t *testing.T) {
	manager := withIsolatedProfileDir(t)
	profile := &session.Profile{
		Alias:       "work",
		Credentials: &session.StoredCredentials{Type: session.CredentialsTypeAPIToken, Endpoint: "https://example.app.spacelift.io"},
	}
	if err := manager.Create(profile); err != nil {
		t.Fatalf("manager.Create() error = %v", err)
	}
	if err := manager.Select(profile.Alias); err != nil {
		t.Fatalf("manager.Select() error = %v", err)
	}

	if err := ReloginSupported(); err != nil {
		t.Errorf("ReloginSupported() error = %v, want nil for an API Token profile", err)
	}
}
