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

// newSessionFromEnvironment loads a session from the environment exactly
// as session.New itself does (see FromEnvironment), i.e. the *effective*
// selection - all of a method's required variables present and complete,
// not just one of them set. Indirected through a var so tests can
// exercise ReloginSupported's env-vs-profile branching without either a
// real environment-based session (some methods perform an eager network
// token exchange) or a real profile file.
var newSessionFromEnvironment = func() (session.Session, error) {
	return session.FromEnvironment(context.Background(), client.GetHTTPClient())(os.LookupEnv)
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
	if sess, err := newSessionFromEnvironment(); err == nil && sess != nil {
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
