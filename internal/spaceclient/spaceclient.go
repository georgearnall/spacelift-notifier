// Package spaceclient wraps the Spacelift Go SDK (github.com/spacelift-io/spacectl/client)
// to add two things the SDK doesn't provide on its own: reuse of the
// existing spacectl CLI login, and tracking of how many GraphQL requests
// have been made, since Spacelift's API exposes no rate-limit headers of
// its own (confirmed empirically: no X-RateLimit-* or Retry-After headers
// are returned on any response).
package spaceclient

import (
	"context"
	"fmt"
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

// New builds a Client authenticated via whatever Spacelift profile is
// currently active for the spacectl CLI (environment variables take
// precedence if set, otherwise ~/.spacelift/config.json's selected
// profile - see session.New).
func New(ctx context.Context) (*Client, error) {
	hc := client.GetHTTPClient()
	sess, err := session.New(ctx, hc)
	if err != nil {
		return nil, fmt.Errorf("loading spacelift session (is `spacectl profile login` set up?): %w", err)
	}
	return &Client{sdk: client.New(hc, sess), now: time.Now}, nil
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
	return c.sdk.Query(ctx, out, vars)
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
	return c.sdk.URL("/stack/%s", stackID)
}

// RunURL returns a link directly to a specific run's page, confirmed
// against spacectl's own "stack confirm" command construction.
func (c *Client) RunURL(stackID, runID string) string {
	return c.sdk.URL("/stack/%s/run/%s", stackID, runID)
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
