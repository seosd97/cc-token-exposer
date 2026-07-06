package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultEndpoint = "https://api.anthropic.com/api/oauth/usage"

	DefaultUserAgent = "claude-code/2.0.14"

	betaVersion = "oauth-2025-04-20"

	requestTimeout = 10 * time.Second

	maxBodyBytes = 1 << 20
)

var (
	ErrAuth        = errors.New("usage: authentication failed")
	ErrRateLimited = errors.New("usage: rate limited")
	ErrTransient   = errors.New("usage: transient error")
)

type RateLimitError struct {
	RetryAfter time.Duration
	StatusCode int
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("usage: rate limited (retry after %s)", e.RetryAfter)
	}
	return "usage: rate limited"
}

func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

type Client struct {
	http      *http.Client
	endpoint  string
	userAgent string
	now       func() time.Time
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

func WithEndpoint(endpoint string) Option {
	return func(c *Client) {
		if endpoint != "" {
			c.endpoint = endpoint
		}
	}
}

func WithClock(now func() time.Time) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

func New(opts ...Option) *Client {
	c := &Client{
		http:      &http.Client{Timeout: requestTimeout},
		endpoint:  DefaultEndpoint,
		userAgent: DefaultUserAgent,
		now:       func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Fetch(ctx context.Context, token string) (*Snapshot, error) {
	if token == "" {
		return nil, fmt.Errorf("%w: empty token", ErrAuth)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrTransient, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", betaVersion)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransient, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return c.decode(resp.Body)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w (status %d)", ErrAuth, resp.StatusCode)
	case http.StatusTooManyRequests:
		return nil, &RateLimitError{
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), c.now()),
			StatusCode: resp.StatusCode,
		}
	default:
		return nil, fmt.Errorf("%w: unexpected status %d", ErrTransient, resp.StatusCode)
	}
}

type apiResponse struct {
	FiveHour      *Window      `json:"five_hour"`
	SevenDay      *Window      `json:"seven_day"`
	SevenDayOpus  *Window      `json:"seven_day_opus"`
	SevenDayFable *Window      `json:"seven_day_fable"`
	ExtraUsage    *ExtraUsage  `json:"extra_usage"`
	Limits        []limitEntry `json:"limits"`
}

type limitEntry struct {
	Kind     string    `json:"kind"`
	Group    string    `json:"group"`
	Percent  float64   `json:"percent"`
	ResetsAt time.Time `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

func (c *Client) decode(body io.Reader) (*Snapshot, error) {
	var r apiResponse
	dec := json.NewDecoder(io.LimitReader(body, maxBodyBytes))
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%w: decode usage response: %v", ErrTransient, err)
	}
	snap := &Snapshot{
		FetchedAt:     c.now(),
		FiveHour:      r.FiveHour,
		SevenDay:      r.SevenDay,
		SevenDayOpus:  r.SevenDayOpus,
		SevenDayFable: r.SevenDayFable,
		ExtraUsage:    r.ExtraUsage,
	}
	if w := scopedWindow(r.Limits, "Opus"); w != nil {
		snap.SevenDayOpus = w
	}
	if w := scopedWindow(r.Limits, "Fable"); w != nil {
		snap.SevenDayFable = w
	}
	return snap, nil
}

// scopedWindow extracts a per-model weekly limit from the limits[] array,
// matching the scope model display name (case-insensitive); nil if absent.
func scopedWindow(limits []limitEntry, model string) *Window {
	for _, l := range limits {
		if l.Scope == nil || l.Scope.Model == nil {
			continue
		}
		if strings.EqualFold(l.Scope.Model.DisplayName, model) {
			return &Window{Utilization: l.Percent, ResetsAt: l.ResetsAt}
		}
	}
	return nil
}

// parseRetryAfter interprets a Retry-After header (seconds or HTTP-date);
// returns 0 when absent or unparseable.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
