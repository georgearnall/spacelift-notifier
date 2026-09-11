// Package spaceclient wraps the Spacelift Go SDK (github.com/spacelift-io/spacectl/client)
// to add two things the SDK doesn't provide on its own: reuse of the
// existing spacectl CLI login, and tracking of how many GraphQL requests
// have been made, since Spacelift's API exposes no rate-limit headers of
// its own (confirmed empirically: no X-RateLimit-* or Retry-After headers
// are returned on any response).
package spaceclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/shurcooL/graphql"
	"github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"
)

// Client is a thin, request-counting wrapper around the Spacelift SDK's
// Client. All queries made by this tool should go through it rather than
// the raw SDK client, so request counts stay accurate.
type Client struct {
	sdk client.Client

	mu           sync.Mutex
	requestCount int
	windowStart  time.Time
	windowCount  int
	now          func() time.Time
}

// newSession loads the current spacectl session. Indirected through a var
// (rather than calling session.New directly) so tests can substitute a
// fake session - exercising Reauth's counter-preserving behavior - without
// a real Spacelift profile on disk.
var newSession = session.New

// New builds a Client authenticated via whatever Spacelift profile is
// currently active for the spacectl CLI (environment variables take
// precedence if set, otherwise ~/.spacelift/config.json's selected
// profile - see session.New).
func New(ctx context.Context) (*Client, error) {
	c := &Client{now: time.Now}
	if err := c.Reauth(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Reauth rebuilds the underlying SDK session from whatever Spacelift
// profile is currently active, e.g. after the caller has re-run `spacectl
// profile login` to recover from an expired session. Unlike calling New
// again, this preserves the Client's existing request-count/budget
// accounting rather than resetting it.
func (c *Client) Reauth(ctx context.Context) error {
	hc := client.GetHTTPClient()
	sess, err := newSession(ctx, hc)
	if err != nil {
		return fmt.Errorf("loading spacelift session (is `spacectl profile login` set up?): %w", err)
	}
	c.mu.Lock()
	c.sdk = client.New(hc, sess)
	c.mu.Unlock()
	return nil
}

// sdkClient returns the current SDK client under lock, so a Reauth call
// replacing it can never race with a concurrent read of the field.
func (c *Client) sdkClient() client.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sdk
}

// envAuthMethods mirrors, in precedence order, which environment
// variables each of session.FromEnvironment's auth methods requires (see
// spacectl's tryAuthMethod) - without actually constructing a session
// through it. Calling FromEnvironment directly would work too, but two of
// its methods (API key, GitHub token) perform an eager, unbounded network
// token exchange just to build a Session value, and its endpoint lookup
// can print a deprecation warning straight to stdout - neither acceptable
// from a capability check that runs synchronously in the watch loop
// before the terminal has even been paused for relogin. The method names
// themselves aren't exported by the session package, so they're just
// documentation here, not a compiled reference to it.
var envAuthMethods = []struct {
	name string
	vars []string // all required for this method to be "complete"
}{
	{"token", []string{session.EnvSpaceliftAPIToken}},
	{"github", []string{session.EnvSpaceliftAPIKeyEndpoint, session.EnvSpaceliftAPIGitHubToken}},
	{"apikey", []string{session.EnvSpaceliftAPIKeyEndpoint, session.EnvSpaceliftAPIKeyID, session.EnvSpaceliftAPIKeySecret}},
}

// envSessionActive reports whether session.New would select an
// environment-based session over the profile file, mirroring
// FromEnvironment's own precedence: a preferred method (if set) is
// checked on its own without falling back to the others, exactly as
// tryAuthMethod does; otherwise each method is checked in order and any
// one being complete is enough. envSpaceliftAPIEndpoint, the deprecated
// fallback for the endpoint variable, is deliberately not considered
// here - detecting it is what triggers the SDK's stdout warning above.
func envSessionActive() bool {
	complete := func(method string) bool {
		for _, m := range envAuthMethods {
			if m.name != method {
				continue
			}
			for _, v := range m.vars {
				if val, ok := os.LookupEnv(v); !ok || val == "" {
					return false
				}
			}
			return true
		}
		return false
	}

	if preferred, ok := os.LookupEnv(session.EnvSpaceliftAPIPreferredMethod); ok {
		return complete(strings.ToLower(strings.TrimSpace(preferred)))
	}
	for _, m := range envAuthMethods {
		if complete(m.name) {
			return true
		}
	}
	return false
}

// ReloginSupported reports why running `spacectl profile login` (with no
// arguments) would fail to recover the current session, or nil if it
// should work. spacectl's own `profile login` command only supports that
// no-argument form for a profile whose stored credentials are already a
// browser-issued API Token (see spacectl's getAliasWithAPITokenProfile) -
// for any other credential source it's guaranteed to fail, so this is
// checked up front rather than surfacing spacectl's own generic profile
// error after already pausing the terminal to run it.
func ReloginSupported() error {
	// session.New tries the environment before ever consulting the
	// profile file - if an env-based session would actually be selected
	// (not just some SPACELIFT_* variable incidentally set, but a method
	// with everything it needs present), relogging in the profile
	// wouldn't change what this tool actually authenticates with.
	if envSessionActive() {
		return errors.New("session is authenticated via environment variables, not a spacectl profile - `spacectl profile login` can't change that; update the environment and restart instead")
	}

	manager, err := session.UserProfileManager()
	if err != nil {
		return fmt.Errorf("checking spacectl profile: %w", err)
	}
	profile := manager.Current()
	if profile == nil {
		return errors.New("no spacectl profile is currently selected; run `spacectl profile login <alias>` manually")
	}
	if profile.Credentials.Type != session.CredentialsTypeAPIToken {
		return fmt.Errorf("current spacectl profile %q uses %s credentials, not a browser login; `spacectl profile login` (with no arguments) only supports API Token profiles - run `spacectl profile login %s` manually instead", profile.Alias, profile.Credentials.Type, profile.Alias)
	}
	return nil
}

// NewFromSDK builds a Client around an already-constructed SDK client.
// Used by tests to point at an httptest.Server via a fake session, and
// available for callers that need non-default session construction.
func NewFromSDK(sdk client.Client) *Client {
	return &Client{sdk: sdk, now: time.Now}
}

// Query executes a single GraphQL query and records it against the
// request budget. vars may be nil.
func (c *Client) Query(ctx context.Context, out any, vars map[string]any) error {
	c.recordRequest()
	return c.sdkClient().Query(ctx, out, vars)
}

func (c *Client) recordRequest() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.requestCount++
	if c.windowStart.IsZero() || now.Sub(c.windowStart) >= time.Hour {
		c.windowStart = now
		c.windowCount = 0
	}
	c.windowCount++
}

// Stats returns the total number of requests made since this Client was
// created, and the number made within the current rolling hour-long
// window (used to enforce --request-budget).
func (c *Client) Stats() (total, windowCount int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requestCount, c.windowCount
}

// StackURL returns a link directly to a stack's page in the Spacelift web
// UI, confirmed against spacectl's own "stack open" command.
func (c *Client) StackURL(stackID string) string {
	return c.sdkClient().URL("/stack/%s", stackID)
}

// RunURL returns a link directly to a specific run's page, confirmed
// against spacectl's own "stack confirm" command construction.
func (c *Client) RunURL(stackID, runID string) string {
	return c.sdkClient().URL("/stack/%s/run/%s", stackID, runID)
}

// Viewer identifies who the tool is authenticated as.
type Viewer struct {
	ID    string
	Name  string
	Admin bool
}

// Viewer queries the identity of the currently authenticated user.
func (c *Client) Viewer(ctx context.Context) (Viewer, error) {
	var q struct {
		Viewer struct {
			ID    graphql.String  `graphql:"id"`
			Name  graphql.String  `graphql:"name"`
			Admin graphql.Boolean `graphql:"admin"`
		} `graphql:"viewer"`
	}
	if err := c.Query(ctx, &q, nil); err != nil {
		return Viewer{}, err
	}
	return Viewer{
		ID:    string(q.Viewer.ID),
		Name:  string(q.Viewer.Name),
		Admin: bool(q.Viewer.Admin),
	}, nil
}
