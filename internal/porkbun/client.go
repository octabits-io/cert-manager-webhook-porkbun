// Package porkbun implements a minimal, hardened client for the Porkbun DNS
// API (https://porkbun.com/api/json/v3/documentation).
//
// It exists rather than using github.com/nrdcg/porkbun because that client
// does not expose the dns/retrieveByNameType endpoint, which lets us fetch
// only the handful of TXT records we care about instead of listing an entire
// zone on every ACME challenge. Full-zone listings are both slow and a
// significant contributor to Porkbun API rate limiting.
//
// Compared to the client this project was forked from, it additionally:
//   - checks the HTTP status code before attempting to decode a response;
//   - decodes into values rather than pointers, so a `null` or truncated body
//     cannot produce a nil dereference;
//   - surfaces the API's `message` field, so failures are diagnosable;
//   - applies a request timeout and retries transient failures with
//     exponential backoff and jitter.
package porkbun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the production Porkbun API v3 endpoint.
const DefaultBaseURL = "https://api.porkbun.com/api/json/v3/"

// statusSuccess is the value Porkbun returns in the `status` field on success.
const statusSuccess = "SUCCESS"

// maxResponseBytes caps how much of a response body we buffer. Porkbun
// responses are small; a bounded read stops a misbehaving or hijacked endpoint
// from exhausting memory.
const maxResponseBytes = 4 << 20 // 4 MiB

// Record is a Porkbun DNS record.
//
// Every field is a string because that is how the Porkbun API represents them,
// including numeric IDs and TTLs. Notably, record IDs must not be parsed into
// a fixed-width integer: they are opaque, monotonically increasing, and
// already large enough that a 32-bit conversion is a live overflow risk.
type Record struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Type    string `json:"type,omitempty"`
	Content string `json:"content,omitempty"`
	TTL     string `json:"ttl,omitempty"`
	Prio    string `json:"prio,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// APIError is returned when Porkbun reports a failure, either via a non-2xx
// HTTP status or via a `status` field that is not "SUCCESS".
type APIError struct {
	// HTTPStatus is the HTTP status code of the response.
	HTTPStatus int
	// Status is the value of the API's `status` field, if present.
	Status string
	// Message is the value of the API's `message` field, if present. This is
	// the only place Porkbun explains *why* a call failed.
	Message string
}

func (e *APIError) Error() string {
	switch {
	case e.Message != "" && e.Status != "":
		return fmt.Sprintf("porkbun api: %s (status %s, http %d)", e.Message, e.Status, e.HTTPStatus)
	case e.Message != "":
		return fmt.Sprintf("porkbun api: %s (http %d)", e.Message, e.HTTPStatus)
	case e.Status != "":
		return fmt.Sprintf("porkbun api: status %s (http %d)", e.Status, e.HTTPStatus)
	default:
		return fmt.Sprintf("porkbun api: http %d", e.HTTPStatus)
	}
}

// Retryable reports whether the request that produced this error is worth
// repeating. Porkbun uses 503 for rate limiting, and its message text is the
// only signal for some throttling cases.
func (e *APIError) Retryable() bool {
	switch e.HTTPStatus {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}

	msg := strings.ToLower(e.Message)
	for _, s := range []string{"rate limit", "too many requests", "throttl", "try again"} {
		if strings.Contains(msg, s) {
			return true
		}
	}

	return false
}

// ErrRecordNotFound is returned by DeleteRecord when the target record no
// longer exists. Callers cleaning up ACME challenges should treat this as
// success: the desired end state has been reached.
var ErrRecordNotFound = errors.New("porkbun: record not found")

// Client is a Porkbun DNS API client. It is safe for concurrent use.
type Client struct {
	apiKey       string
	secretAPIKey string

	baseURL    *url.URL
	httpClient *http.Client

	maxAttempts int
	backoffBase time.Duration
	sleep       func(ctx context.Context, d time.Duration) error
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client used for API calls. The client must
// have a non-zero Timeout, or a stalled connection will hang the caller
// indefinitely.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) {
		if c != nil {
			cl.httpClient = c
		}
	}
}

// WithBaseURL overrides the API endpoint. Used by tests.
func WithBaseURL(raw string) Option {
	return func(cl *Client) {
		if u, err := url.Parse(raw); err == nil {
			cl.baseURL = u
		}
	}
}

// WithRetry configures the retry policy for transient failures. attempts is
// the total number of tries, so 1 disables retrying.
func WithRetry(attempts int, base time.Duration) Option {
	return func(cl *Client) {
		if attempts > 0 {
			cl.maxAttempts = attempts
		}
		if base > 0 {
			cl.backoffBase = base
		}
	}
}

// withSleep replaces the backoff sleep. Used by tests to avoid real delays.
func withSleep(f func(ctx context.Context, d time.Duration) error) Option {
	return func(cl *Client) { cl.sleep = f }
}

// New creates a Porkbun API client.
//
// Note the argument order: Porkbun's own documentation lists the secret key
// first, and the upstream Go clients follow suit. Swapping them yields an
// opaque authentication failure.
func New(secretAPIKey, apiKey string, opts ...Option) *Client {
	base, _ := url.Parse(DefaultBaseURL)

	c := &Client{
		apiKey:       apiKey,
		secretAPIKey: secretAPIKey,
		baseURL:      base,
		httpClient: &http.Client{
			// A bounded per-request timeout. The forked-from client used a
			// zero-value http.Client, which has no timeout at all.
			Timeout: 30 * time.Second,
		},
		maxAttempts: 4,
		backoffBase: 500 * time.Millisecond,
		sleep:       sleepCtx,
	}

	for _, o := range opts {
		o(c)
	}

	return c
}

// auth carries the credentials Porkbun requires in the body of every request.
// It is embedded anonymously so encoding/json inlines its fields alongside the
// request-specific ones.
type auth struct {
	APIKey       string `json:"apikey"`
	SecretAPIKey string `json:"secretapikey"`
}

type statusEnvelope struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type pingResponse struct {
	statusEnvelope
	YourIP string `json:"yourIp"`
}

type retrieveResponse struct {
	statusEnvelope
	Records []Record `json:"records"`
}

type createResponse struct {
	statusEnvelope
	// Porkbun returns the new record's ID as a JSON number here, unlike the
	// string IDs it returns from retrieve. json.Number accepts both.
	ID json.Number `json:"id"`
}

type createRequest struct {
	auth
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     string `json:"ttl,omitempty"`
	Prio    string `json:"prio,omitempty"`
}

// Ping verifies that the configured credentials are accepted, returning the
// caller's public IP as seen by Porkbun.
func (c *Client) Ping(ctx context.Context) (string, error) {
	var resp pingResponse
	if err := c.do(ctx, "ping", auth{}, &resp, &resp.statusEnvelope); err != nil {
		return "", err
	}
	return resp.YourIP, nil
}

// RetrieveRecordsByNameType returns the records of the given type at the given
// subdomain. An empty subdomain addresses the zone apex.
//
// This uses dns/retrieveByNameType rather than listing the whole zone, so the
// cost of a challenge does not scale with the size of the domain's record set.
func (c *Client) RetrieveRecordsByNameType(ctx context.Context, domain, recordType, subdomain string) ([]Record, error) {
	path, err := joinPath("dns", "retrieveByNameType", domain, recordType, subdomain)
	if err != nil {
		return nil, err
	}

	var resp retrieveResponse
	if err := c.do(ctx, path, auth{}, &resp, &resp.statusEnvelope); err != nil {
		return nil, err
	}

	return resp.Records, nil
}

// CreateRecord creates a DNS record and returns its ID.
//
// record.Name must be the subdomain only, without the domain suffix; an empty
// name creates a record at the apex.
func (c *Client) CreateRecord(ctx context.Context, domain string, record Record) (string, error) {
	path, err := joinPath("dns", "create", domain)
	if err != nil {
		return "", err
	}

	var resp createResponse
	req := createRequest{
		Name:    record.Name,
		Type:    record.Type,
		Content: record.Content,
		TTL:     record.TTL,
		Prio:    record.Prio,
	}
	if err := c.do(ctx, path, req, &resp, &resp.statusEnvelope); err != nil {
		return "", err
	}

	return resp.ID.String(), nil
}

// DeleteRecord deletes a record by its opaque ID. It returns ErrRecordNotFound
// if the record is already gone, so cleanup is idempotent.
func (c *Client) DeleteRecord(ctx context.Context, domain, id string) error {
	path, err := joinPath("dns", "delete", domain, id)
	if err != nil {
		return err
	}

	var resp statusEnvelope
	err = c.do(ctx, path, auth{}, &resp, &resp)

	var apiErr *APIError
	if errors.As(err, &apiErr) && isNotFound(apiErr) {
		return ErrRecordNotFound
	}

	return err
}

func isNotFound(e *APIError) bool {
	if e.HTTPStatus == http.StatusNotFound {
		return true
	}
	msg := strings.ToLower(e.Message)
	return strings.Contains(msg, "invalid record id") ||
		strings.Contains(msg, "record does not exist") ||
		strings.Contains(msg, "not found")
}

// do executes a single API call with retries.
//
// payload is marshalled with the credentials merged in. out receives the
// decoded body and env must point at the statusEnvelope embedded within it, so
// that the API-level status can be checked after decoding.
func (c *Client) do(ctx context.Context, path string, payload any, out any, env *statusEnvelope) error {
	body, err := c.marshalRequest(payload)
	if err != nil {
		return err
	}

	endpoint := c.baseURL.JoinPath(path).String()

	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if attempt > 1 {
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				// Context expired while backing off; report the underlying
				// failure rather than the context error, which is more useful.
				return fmt.Errorf("%w (giving up after %d attempts: %w)", lastErr, attempt-1, err)
			}
		}

		err := c.attempt(ctx, endpoint, body, out, env)
		if err == nil {
			return nil
		}
		lastErr = err

		if !retryable(err) {
			return err
		}
	}

	return fmt.Errorf("porkbun: giving up after %d attempts: %w", c.maxAttempts, lastErr)
}

func (c *Client) attempt(ctx context.Context, endpoint string, body []byte, out any, env *statusEnvelope) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("porkbun: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Transport-level failures (DNS, TCP, TLS, timeout) are transient
		// often enough to be worth another attempt.
		return &transportError{err: err}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return &transportError{err: fmt.Errorf("read response body: %w", err)}
	}

	// Check the transport status *before* trying to decode. Porkbun serves
	// HTML error pages and empty bodies for some failures, and decoding those
	// as JSON produces an error that hides the real cause.
	if resp.StatusCode != http.StatusOK {
		apiErr := &APIError{HTTPStatus: resp.StatusCode}
		// The body is often still JSON with a useful message; if not, fall
		// back to a truncated excerpt so the operator sees something.
		var e statusEnvelope
		if json.Unmarshal(raw, &e) == nil && (e.Status != "" || e.Message != "") {
			apiErr.Status, apiErr.Message = e.Status, e.Message
		} else {
			apiErr.Message = excerpt(raw)
		}
		return apiErr
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("porkbun: decode response (http %d, body %q): %w", resp.StatusCode, excerpt(raw), err)
	}

	if env.Status != statusSuccess {
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Status:     env.Status,
			Message:    env.Message,
		}
	}

	return nil
}

// marshalRequest merges the credentials into the request payload.
//
// Porkbun authenticates by carrying `apikey` and `secretapikey` in the JSON
// body of every request rather than in a header, so serialising credentials
// here is the API contract, not a leak. The bodies are sent over TLS to the
// Porkbun endpoint only, and are never logged: error paths deliberately quote
// the *response* body, never the request.
func (c *Client) marshalRequest(payload any) ([]byte, error) {
	creds := auth{APIKey: c.apiKey, SecretAPIKey: c.secretAPIKey}

	switch p := payload.(type) {
	case auth:
		return json.Marshal(creds) //nolint:gosec // G117: credentials in the body are required by the Porkbun API
	case createRequest:
		p.auth = creds
		return json.Marshal(p) //nolint:gosec // G117: see above
	default:
		return nil, fmt.Errorf("porkbun: unsupported request payload %T", payload)
	}
}

// backoff returns the delay before the given attempt (2-indexed), using
// exponential growth with full jitter to avoid synchronised retries when
// several challenges are solved at once.
func (c *Client) backoff(attempt int) time.Duration {
	const maxBackoff = 15 * time.Second

	// Cap the shift so a generous retry count cannot overflow the duration.
	shift := attempt - 2
	if shift > 20 {
		shift = 20
	}

	d := c.backoffBase << shift
	if d > maxBackoff || d <= 0 {
		d = maxBackoff
	}

	// Full jitter: sleep for a random duration in [d/2, d]. Without this,
	// concurrent challenges for the same certificate retry in lockstep and
	// keep colliding with the same rate limit.
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// transportError marks a network-level failure as retryable.
type transportError struct{ err error }

func (e *transportError) Error() string { return "porkbun: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func retryable(err error) bool {
	// A cancelled or expired context is never worth retrying.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var te *transportError
	if errors.As(err, &te) {
		return true
	}

	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Retryable()
	}

	return false
}

// joinPath validates and escapes user-controlled path segments. Domains and
// record IDs reach us from cluster configuration, so they are escaped rather
// than trusted; empty trailing segments (an apex subdomain) are dropped.
func joinPath(segments ...string) (string, error) {
	out := make([]string, 0, len(segments))
	for i, s := range segments {
		if s == "" {
			// Only the final segment may be empty, addressing the apex.
			if i == len(segments)-1 {
				continue
			}
			return "", fmt.Errorf("porkbun: empty path segment at position %d", i)
		}
		if strings.ContainsAny(s, "/\\") || s == "." || s == ".." {
			return "", fmt.Errorf("porkbun: invalid path segment %q", s)
		}
		out = append(out, url.PathEscape(s))
	}
	return strings.Join(out, "/"), nil
}

func excerpt(b []byte) string {
	const limit = 256
	s := strings.TrimSpace(string(b))
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
