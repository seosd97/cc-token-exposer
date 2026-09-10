package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/provider"
	"github.com/seosd97/cc-token-exposer/internal/schema"
)

const (
	DefaultEndpoint = "https://chatgpt.com/backend-api/wham/usage"

	DefaultUserAgent = "codex_cli_rs/0.153.4"

	originator = "codex_cli_rs"

	requestTimeout = 10 * time.Second

	maxBodyBytes = 1 << 20

	fiveHourWindowSeconds = 5 * 60 * 60

	sevenDayWindowSeconds = 7 * 24 * 60 * 60
)

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

func (c *Client) Fetch(ctx context.Context, cr *provider.Credentials) (*provider.FetchedSnapshot, error) {
	if cr == nil || cr.AccessToken == "" {
		return nil, fmt.Errorf("%w: empty token", provider.ErrAuth)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", provider.ErrTransient, err)
	}
	req.Header.Set("Authorization", "Bearer "+cr.AccessToken)
	if cr.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", cr.AccountID)
	}
	req.Header.Set("originator", originator)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", provider.ErrTransient, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return c.decode(resp.Body)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w (status %d)", provider.ErrAuth, resp.StatusCode)
	case http.StatusTooManyRequests:
		return nil, &provider.RateLimitError{
			RetryAfter: provider.ParseRetryAfter(resp.Header.Get("Retry-After"), c.now()),
			StatusCode: resp.StatusCode,
		}
	default:
		return nil, fmt.Errorf("%w: unexpected status %d", provider.ErrTransient, resp.StatusCode)
	}
}

type apiResponse struct {
	RateLimit            *rateLimitStatus  `json:"rate_limit"`
	AdditionalRateLimits []additionalLimit `json:"additional_rate_limits"`
}

type rateLimitStatus struct {
	PrimaryWindow   *windowSnapshot `json:"primary_window"`
	SecondaryWindow *windowSnapshot `json:"secondary_window"`
}

type windowSnapshot struct {
	UsedPercent        float64         `json:"used_percent"`
	LimitWindowSeconds int64           `json:"limit_window_seconds"`
	WindowMinutes      int64           `json:"window_minutes"`
	ResetAfterSeconds  *int64          `json:"reset_after_seconds"`
	ResetAt            json.RawMessage `json:"reset_at"`
	ResetsAt           json.RawMessage `json:"resets_at"`
}

type additionalLimit struct {
	LimitName      string           `json:"limit_name"`
	MeteredFeature string           `json:"metered_feature"`
	RateLimit      *rateLimitStatus `json:"rate_limit"`
}

func (w *windowSnapshot) lengthSeconds() int64 {
	if w.LimitWindowSeconds > 0 {
		return w.LimitWindowSeconds
	}
	return w.WindowMinutes * 60
}

func (w *windowSnapshot) resetTime(now time.Time) (time.Time, bool) {
	if t := provider.ParseTolerantTime(w.ResetAt); !t.IsZero() {
		return t, true
	}
	if t := provider.ParseTolerantTime(w.ResetsAt); !t.IsZero() {
		return t, true
	}
	if w.ResetAfterSeconds != nil && *w.ResetAfterSeconds > 0 {
		return now.Add(time.Duration(*w.ResetAfterSeconds) * time.Second), true
	}
	return time.Time{}, false
}

func (w *windowSnapshot) window(now time.Time) (*schema.Window, bool) {
	win := &schema.Window{Utilization: w.UsedPercent}
	t, ok := w.resetTime(now)
	if ok {
		win.ResetsAt = t
	}
	return win, ok
}

func (c *Client) decode(body io.Reader) (*provider.FetchedSnapshot, error) {
	var r apiResponse
	dec := json.NewDecoder(io.LimitReader(body, maxBodyBytes))
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%w: decode usage response: %w", provider.ErrTransient, err)
	}
	now := c.now()
	snap := &schema.Snapshot{FetchedAt: now}
	var drift []string
	if r.RateLimit != nil {
		drift = placePlanWindows(snap, r.RateLimit, now, drift)
	}
	for i, b := range r.AdditionalRateLimits {
		drift = placeBucket(snap, b, i, now, drift)
	}
	if snap.FiveHour == nil && snap.SevenDay == nil && len(snap.ScopedLimits) == 0 {
		drift = append(drift, "empty usage payload")
	}
	return &provider.FetchedSnapshot{Snapshot: snap, ScopedProbed: true, Drift: drift}, nil
}

func placePlanWindows(snap *schema.Snapshot, rl *rateLimitStatus, now time.Time, drift []string) []string {
	for _, e := range []struct {
		label      string
		w          *windowSnapshot
		byPosition **schema.Window
	}{
		{"primary_window", rl.PrimaryWindow, &snap.FiveHour},
		{"secondary_window", rl.SecondaryWindow, &snap.SevenDay},
	} {
		if e.w == nil {
			continue
		}
		win, hasReset := e.w.window(now)
		if !hasReset {
			drift = append(drift, e.label+" missing reset")
		}

		target := e.byPosition
		switch secs := e.w.lengthSeconds(); secs {
		case fiveHourWindowSeconds:
			target = &snap.FiveHour
		case sevenDayWindowSeconds:
			target = &snap.SevenDay
		case 0:
			drift = append(drift, e.label+" missing length")
		default:
			drift = append(drift, fmt.Sprintf("%s unexpected length %ds", e.label, secs))
		}
		if *target != nil {
			drift = append(drift, e.label+" duplicates an already mapped window")
			continue
		}
		*target = win
	}
	return drift
}

func placeBucket(snap *schema.Snapshot, b additionalLimit, index int, now time.Time, drift []string) []string {
	name := b.LimitName
	if name == "" {
		name = b.MeteredFeature
	}
	if name == "" {
		return append(drift, fmt.Sprintf("rate limit bucket #%d missing limit_name", index))
	}
	if b.RateLimit == nil {
		return drift
	}

	var weekly *windowSnapshot
	for _, w := range []*windowSnapshot{b.RateLimit.PrimaryWindow, b.RateLimit.SecondaryWindow} {
		if w != nil && w.lengthSeconds() == sevenDayWindowSeconds {
			weekly = w
			break
		}
	}
	if weekly == nil {
		if b.RateLimit.PrimaryWindow != nil || b.RateLimit.SecondaryWindow != nil {
			drift = append(drift, fmt.Sprintf("rate limit bucket %q has no weekly window", name))
		}
		return drift
	}

	win, hasReset := weekly.window(now)
	if !hasReset && weekly.UsedPercent > 0 {
		drift = append(drift, fmt.Sprintf("rate limit bucket %q missing reset", name))
	}
	if snap.ScopedLimits == nil {
		snap.ScopedLimits = make(map[string]*schema.Window)
	}
	if _, dup := snap.ScopedLimits[name]; dup {
		return append(drift, fmt.Sprintf("duplicate rate limit bucket %q", name))
	}
	snap.ScopedLimits[name] = win
	return drift
}
