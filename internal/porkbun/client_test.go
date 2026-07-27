package porkbun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient returns a client pointed at h with retries that do not sleep.
func newTestClient(t *testing.T, h http.HandlerFunc, opts ...Option) *Client {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	base := append([]Option{
		WithBaseURL(srv.URL + "/api/json/v3/"),
		withSleep(func(context.Context, time.Duration) error { return nil }),
	}, opts...)

	return New("secret-key", "api-key", base...)
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading request body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decoding request body %q: %v", b, err)
	}
	return m
}

// A `null` body decodes without error but leaves a pointer target nil. The
// implementation this was forked from decoded into **T and then dereferenced,
// panicking the whole webhook process. Decoding into a value must not.
func TestNullBodyDoesNotPanic(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "null")
	})

	_, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "_acme-challenge")
	if err == nil {
		t.Fatal("expected an error for a null response body, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
}

func TestEmptyBodyDoesNotPanic(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if _, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "x"); err == nil {
		t.Fatal("expected an error for an empty response body, got nil")
	}
}

// A non-JSON error page must not be reported as a JSON decoding failure; the
// HTTP status is the useful signal.
func TestHTMLErrorPageReportsHTTPStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html><body>502 Bad Gateway</body></html>")
	}, WithRetry(1, time.Millisecond))

	_, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.HTTPStatus != http.StatusBadGateway {
		t.Errorf("HTTPStatus = %d, want %d", apiErr.HTTPStatus, http.StatusBadGateway)
	}
	if !strings.Contains(apiErr.Message, "502") {
		t.Errorf("message %q does not include the response excerpt", apiErr.Message)
	}
}

// The whole point of the fork: Porkbun's `message` field must reach the
// operator. Upstream discarded it and reported only `invalid status "ERROR"`.
func TestAPIErrorSurfacesMessage(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ERROR","message":"Invalid API key. (002)"}`)
	})

	_, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Invalid API key. (002)") {
		t.Errorf("error %q does not contain the API message", err)
	}
}

func TestCredentialsAreSentInBody(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = decodeBody(t, r)
		_, _ = io.WriteString(w, `{"status":"SUCCESS","records":[]}`)
	})

	if _, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got["apikey"] != "api-key" {
		t.Errorf("apikey = %v, want api-key", got["apikey"])
	}
	if got["secretapikey"] != "secret-key" {
		t.Errorf("secretapikey = %v, want secret-key", got["secretapikey"])
	}
}

func TestCreateRecordSendsFlattenedPayload(t *testing.T) {
	var got map[string]any
	var path string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		got = decodeBody(t, r)
		_, _ = io.WriteString(w, `{"status":"SUCCESS","id":123456789012}`)
	})

	id, err := c.CreateRecord(context.Background(), "example.com", Record{
		Name:    "_acme-challenge",
		Type:    "TXT",
		Content: "token",
		TTL:     "600",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "123456789012" {
		t.Errorf("id = %q, want 123456789012", id)
	}
	if want := "/api/json/v3/dns/create/example.com"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	// Credentials and record fields must sit at the same level; a nested
	// object is rejected by the API.
	for _, k := range []string{"apikey", "secretapikey", "name", "type", "content", "ttl"} {
		if _, ok := got[k]; !ok {
			t.Errorf("request body is missing top-level key %q; got %v", k, got)
		}
	}
}

// Record IDs are already large enough that a 32-bit conversion overflows. They
// must be carried as opaque strings end to end.
func TestLargeRecordIDSurvivesRoundTrip(t *testing.T) {
	const bigID = "9007199254740993" // > 2^53, far beyond int32
	var deletePath string

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/dns/delete/") {
			deletePath = r.URL.Path
			_, _ = io.WriteString(w, `{"status":"SUCCESS"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"SUCCESS","records":[{"id":"`+bigID+`","name":"_acme-challenge.example.com","type":"TXT","content":"tok"}]}`)
	})

	recs, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "_acme-challenge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 1 || recs[0].ID != bigID {
		t.Fatalf("records = %+v, want a single record with ID %s", recs, bigID)
	}

	if err := c.DeleteRecord(context.Background(), "example.com", recs[0].ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(deletePath, "/"+bigID) {
		t.Errorf("delete path = %q, want it to end with /%s", deletePath, bigID)
	}
}

func TestRetrieveByNameTypePathOrder(t *testing.T) {
	tests := []struct {
		name      string
		subdomain string
		want      string
	}{
		{"subdomain", "_acme-challenge", "/api/json/v3/dns/retrieveByNameType/example.com/TXT/_acme-challenge"},
		{"apex", "", "/api/json/v3/dns/retrieveByNameType/example.com/TXT"},
		{"nested", "_acme-challenge.stg", "/api/json/v3/dns/retrieveByNameType/example.com/TXT/_acme-challenge.stg"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.Path
				_, _ = io.WriteString(w, `{"status":"SUCCESS","records":[]}`)
			})

			if _, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", tc.subdomain); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRetriesTransientFailures(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"status":"ERROR","message":"rate limit exceeded"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"SUCCESS","records":[]}`)
	})

	if _, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "x"); err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("made %d calls, want 3", got)
	}
}

func TestDoesNotRetryPermanentFailures(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"status":"ERROR","message":"Invalid API key."}`)
	})

	if _, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "x"); err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d calls, want 1 (auth failures must not be retried)", got)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}, WithRetry(3, time.Millisecond))

	if _, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "x"); err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("made %d calls, want exactly 3", got)
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.RetrieveRecordsByNameType(ctx, "example.com", "TXT", "x"); err == nil {
		t.Fatal("expected an error")
	}
	if got := calls.Load(); got > 1 {
		t.Errorf("made %d calls with a cancelled context, want at most 1", got)
	}
}

func TestDeleteRecordTreatsMissingAsSuccess(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ERROR","message":"Invalid record ID."}`)
	})

	err := c.DeleteRecord(context.Background(), "example.com", "1")
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("err = %v, want ErrRecordNotFound", err)
	}
}

// A domain or record ID that reached us from cluster config must never be able
// to redirect the request to a different API endpoint.
func TestPathTraversalIsRejected(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the server must not be reached for an invalid path segment")
		_, _ = io.WriteString(w, `{"status":"SUCCESS"}`)
	})

	for _, bad := range []string{"../ssl/retrieve/victim.com", "example.com/..", "..", "."} {
		t.Run(bad, func(t *testing.T) {
			if _, err := c.RetrieveRecordsByNameType(context.Background(), bad, "TXT", "x"); err == nil {
				t.Errorf("expected an error for domain %q", bad)
			}
		})
	}
}

func TestPingReturnsIP(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"SUCCESS","yourIp":"203.0.113.7"}`)
	})

	ip, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "203.0.113.7" {
		t.Errorf("ip = %q, want 203.0.113.7", ip)
	}
}

// The credentials are in the request body, so following a redirect would
// re-POST them to the redirect target. A 3xx must surface as an error and the
// target must never be contacted.
func TestRedirectsAreNotFollowed(t *testing.T) {
	var leaked atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		leaked.Add(1)
		_, _ = io.WriteString(w, `{"status":"SUCCESS","records":[]}`)
	}))
	defer attacker.Close()

	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", attacker.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}, WithRetry(1, time.Millisecond))

	_, err := c.RetrieveRecordsByNameType(context.Background(), "example.com", "TXT", "x")
	if err == nil {
		t.Fatal("expected an error for a redirect response")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusTemporaryRedirect {
		t.Fatalf("err = %v, want *APIError with http 307", err)
	}
	if got := leaked.Load(); got != 0 {
		t.Errorf("redirect target received %d requests; credentials were forwarded", got)
	}
}

func TestDefaultClientHasATimeout(t *testing.T) {
	c := New("s", "a")
	if c.httpClient.Timeout == 0 {
		t.Error("the default HTTP client must have a timeout, or a stalled connection hangs the solver forever")
	}
}

func TestBackoffIsBoundedAndJittered(t *testing.T) {
	c := New("s", "a", WithRetry(40, time.Second))

	seen := map[time.Duration]bool{}
	for attempt := 2; attempt <= 40; attempt++ {
		d := c.backoff(attempt)
		if d <= 0 {
			t.Fatalf("backoff(%d) = %v, want a positive duration", attempt, d)
		}
		if d > 15*time.Second {
			t.Fatalf("backoff(%d) = %v, want at most 15s", attempt, d)
		}
		seen[d] = true
	}

	if len(seen) < 2 {
		t.Error("backoff produced no variation; jitter is not being applied")
	}
}
