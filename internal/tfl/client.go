package tfl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultBaseURL is the public TfL Unified API root. The disruption feed and
// Meta vocabularies are reachable without an app_key; a key only raises the
// rate limit, which matters for the scheduled snapshotter.
const DefaultBaseURL = "https://api.tfl.gov.uk"

// DefaultUserAgent identifies the project. TfL's WAF rejects the default Go
// User-Agent with a 403, and the OGL licence asks for attribution anyway.
const DefaultUserAgent = "traffic-forecaster (+https://github.com/umbralcalc/traffic-forecaster)"

// Client is a thin, retrying HTTP client for the TfL endpoints we use.
type Client struct {
	BaseURL    string
	AppKey     string
	UserAgent  string
	HTTPClient *http.Client

	// MaxRetries is the number of additional attempts after the first.
	MaxRetries int
	// BaseDelay is the initial backoff; it grows exponentially with jitter.
	BaseDelay time.Duration

	rng *rand.Rand
}

// NewClient returns a Client with sane defaults. appKey may be empty for local,
// low-volume use. seed lets callers make jitter deterministic in tests.
func NewClient(appKey string, seed int64) *Client {
	return &Client{
		BaseURL:    DefaultBaseURL,
		AppKey:     appKey,
		UserAgent:  DefaultUserAgent,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		MaxRetries: 4,
		BaseDelay:  500 * time.Millisecond,
		rng:        rand.New(rand.NewSource(seed)),
	}
}

// get fetches path with the given query, appending app_key, and returns the
// raw body. It retries on 429/5xx and transport errors with exponential backoff
// plus jitter, honouring a Retry-After header when present.
func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if query == nil {
		query = url.Values{}
	}
	if c.AppKey != "" {
		query.Set("app_key", c.AppKey)
	}
	full := c.BaseURL + path
	if enc := query.Encode(); enc != "" {
		full += "?" + enc
	}

	var lastErr error
	var retryAfter time.Duration
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, c.backoff(attempt, retryAfter)); err != nil {
				return nil, err
			}
		}
		retryAfter = 0

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
		if err != nil {
			return nil, err // non-retryable: malformed request
		}
		req.Header.Set("Accept", "application/json")
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue // transport error: retry
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusOK:
			if readErr != nil {
				lastErr = fmt.Errorf("reading body: %w", readErr)
				continue
			}
			return body, nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
			lastErr = fmt.Errorf("tfl GET %s: status %d", path, resp.StatusCode)
			continue // retryable
		default:
			return nil, fmt.Errorf("tfl GET %s: status %d: %s", path, resp.StatusCode, truncate(body, 256))
		}
	}
	return nil, fmt.Errorf("tfl GET %s: giving up after %d attempts: %w", path, c.MaxRetries+1, lastErr)
}

// backoff returns the delay before the given attempt (1-indexed). If the server
// suggested a Retry-After, that wins; otherwise exponential with full jitter.
func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	max := float64(c.BaseDelay) * math.Pow(2, float64(attempt-1))
	return time.Duration(c.rng.Int63n(int64(max) + 1))
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Disruptions fetches the full London road disruption set in a single call
// (the feed is not paginated) and returns the records verbatim as raw JSON so
// snapshots stay lossless. Decode into RoadDisruption for typed access.
func (c *Client) Disruptions(ctx context.Context) ([]json.RawMessage, error) {
	q := url.Values{}
	q.Set("stripContent", "true")
	body, err := c.get(ctx, "/Road/all/Disruption", q)
	if err != nil {
		return nil, err
	}
	var records []json.RawMessage
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Errorf("decoding disruption list: %w", err)
	}
	return records, nil
}

// Severities fetches the road severity vocabulary from /Road/Meta/Severities.
func (c *Client) Severities(ctx context.Context) ([]Severity, error) {
	body, err := c.get(ctx, "/Road/Meta/Severities", nil)
	if err != nil {
		return nil, err
	}
	var out []Severity
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding severities: %w", err)
	}
	return out, nil
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
