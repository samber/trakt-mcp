// Package trakt wraps the generated Trakt API client with authentication
// headers, a single refresh-and-retry on 401, a single retry of idempotent
// reads on 5xx, and mapping of failures to typed errors.
package trakt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// defaultBaseURL is Trakt's production API.
	defaultBaseURL = "https://api.trakt.tv"
	// defaultUserAgent names the project when the caller passes none; Trakt
	// asks every application to identify itself.
	defaultUserAgent = "trakt-mcp"
	// defaultHTTPTimeout bounds one round trip, headers and body included, so a
	// stalled Trakt never pins a tool call; Trakt answers in well under a second.
	defaultHTTPTimeout = 30 * time.Second
	// apiVersion is the only Trakt API version.
	apiVersion = "2"
	// jsonContentType is the only body format Trakt accepts and returns.
	jsonContentType = "application/json"
	// maxDrainBytes bounds how much of a discarded response is read so the
	// connection can be reused; larger bodies just close the connection.
	maxDrainBytes = 64 << 10

	headerAPIKey     = "trakt-api-key" //nolint:gosec // G101: a header name, not a credential
	headerAPIVersion = "trakt-api-version"
	headerAuth       = "Authorization"
	headerCType      = "Content-Type"
	headerUserAgent  = "User-Agent"
	bearerPrefix     = "Bearer "
)

// Config configures a Client.
type Config struct {
	// BaseURL defaults to https://api.trakt.tv; tests point it at httptest.
	BaseURL string
	// APIKey is the Trakt application's client id.
	APIKey string
	// UserAgent identifies the project and version, never the user.
	UserAgent string
	// HTTP defaults to a client with defaultHTTPTimeout.
	HTTP *http.Client
}

// TokenSource supplies one user's Trakt access token.
type TokenSource interface {
	// AccessToken returns the current access token, refreshing it if expired.
	AccessToken(ctx context.Context) (string, error)
	// Rejected tells the source Trakt refused rejected. It forces one refresh
	// and returns the new access token.
	Rejected(ctx context.Context, rejected string) (string, error)
}

// Client holds the configuration shared by every user. It is safe for
// concurrent use.
type Client struct {
	baseURL   string
	apiKey    string
	userAgent string
	http      *http.Client
	now       func() time.Time
}

// New builds a Client from cfg, filling defaults for empty fields.
func New(cfg Config) *Client {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}

	ua := cfg.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}

	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}

	return &Client{
		// The generated client resolves operation paths against the server URL,
		// which therefore needs exactly one trailing slash.
		baseURL:   strings.TrimRight(base, "/") + "/",
		apiKey:    cfg.APIKey,
		userAgent: ua,
		http:      hc,
		now:       time.Now,
	}
}

// UserClient calls Trakt on behalf of one user.
type UserClient struct {
	api *api.ClientWithResponses
}

// For returns a client authenticating with ts. It is cheap; build one per
// tool call.
func (c *Client) For(ts TokenSource) *UserClient {
	d := &doer{http: c.http, tokens: ts, now: c.now}

	return &UserClient{api: &api.ClientWithResponses{ClientInterface: &api.Client{
		Server:         c.baseURL,
		Client:         d,
		RequestEditors: []api.RequestEditorFn{c.staticHeaders},
	}}}
}

// API returns the generated client. Every call it makes carries the Trakt
// headers, and every non-2xx answer or transport failure comes back as an
// *Error (or the token source's error when a refresh fails).
func (u *UserClient) API() *api.ClientWithResponses {
	return u.api
}

// staticHeaders sets the headers that do not depend on the user.
func (c *Client) staticHeaders(_ context.Context, req *http.Request) error {
	req.Header.Set(headerAPIKey, c.apiKey)
	req.Header.Set(headerAPIVersion, apiVersion)
	req.Header.Set(headerCType, jsonContentType)
	req.Header.Set(headerUserAgent, c.userAgent)

	return nil
}

// doer is the generated client's HttpRequestDoer. It adds the bearer token,
// retries once after a 401 with a refreshed token, retries idempotent
// requests once on 5xx, and turns failures into *Error.
type doer struct {
	http   *http.Client
	tokens TokenSource
	now    func() time.Time
}

// Do implements api.HttpRequestDoer.
func (d *doer) Do(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	// The body is buffered so a retry after 401 sends the same bytes; Trakt
	// request bodies are small JSON documents built by the tools.
	body, err := readRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("trakt: buffer request body: %w", err)
	}

	token, err := d.tokens.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("trakt: access token: %w", err)
	}

	var refreshed, retriedServer bool

	for {
		// The URL comes from the generated client: the configured base URL plus a
		// fixed operation path, never from user input.
		resp, err := d.http.Do(attempt(ctx, req, body, token)) //nolint:gosec // G704: see above
		if err != nil {
			return nil, transportError(ctx, err)
		}

		switch {
		case resp.StatusCode < http.StatusBadRequest:
			return resp, nil
		case resp.StatusCode == http.StatusUnauthorized && !refreshed:
			discard(resp)

			refreshed = true

			token, err = d.tokens.Rejected(ctx, token)
			if err != nil {
				return nil, fmt.Errorf("trakt: refresh after 401: %w", err)
			}

			continue
		case resp.StatusCode >= http.StatusInternalServerError && idempotent(req.Method) && !retriedServer:
			// Writes are never retried on 5xx: Trakt may have applied them.
			discard(resp)

			retriedServer = true

			continue
		}

		e := errorFromResponse(resp, d.now())
		discard(resp)

		return nil, e
	}
}

// readRequestBody reads and closes req.Body. It returns nil when there is none.
func readRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}

	b, err := io.ReadAll(req.Body)
	closeErr := req.Body.Close()

	if err != nil {
		return nil, err
	}

	if closeErr != nil {
		return nil, closeErr
	}

	return b, nil
}

// attempt clones req with a fresh copy of body and the given bearer token.
func attempt(ctx context.Context, req *http.Request, body []byte, token string) *http.Request {
	r := req.Clone(ctx)
	r.Header.Set(headerAuth, bearerPrefix+token)

	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		r.ContentLength = int64(len(body))
	}

	return r
}

func idempotent(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// discard drains a bounded part of the body and closes it. Errors are dropped
// on purpose: they only decide whether the connection is reused, and the
// response is already handled.
func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	_ = resp.Body.Close()
}
