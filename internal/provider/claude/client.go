package claude

import (
	"bytes"
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
	DefaultEndpoint = "https://api.anthropic.com/api/oauth/usage"

	DefaultUserAgent = "claude-code/2.1.217"

	betaVersion = "oauth-2025-04-20"

	requestTimeout = 10 * time.Second

	maxBodyBytes = 1 << 20

	scopedLimitKind = "weekly_scoped"
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
	token := cr.AccessToken

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", provider.ErrTransient, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", betaVersion)
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
	FiveHour     *apiWindow   `json:"five_hour"`
	SevenDay     *apiWindow   `json:"seven_day"`
	SevenDayOpus *apiWindow   `json:"seven_day_opus"`
	ExtraUsage   *apiExtra    `json:"extra_usage"`
	Limits       []limitEntry `json:"limits"`
}

type apiWindow struct {
	Utilization *float64        `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

type apiExtra struct {
	Utilization *float64 `json:"utilization"`
}

type limitEntry struct {
	Kind     string          `json:"kind"`
	Group    string          `json:"group"`
	Percent  *float64        `json:"percent"`
	ResetsAt json.RawMessage `json:"resets_at"`
	IsActive *bool           `json:"is_active"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

func (w *apiWindow) window() *schema.Window {
	if w == nil || w.Utilization == nil {
		return nil
	}
	return &schema.Window{Utilization: *w.Utilization, ResetsAt: provider.ParseTolerantTime(w.ResetsAt)}
}

func (x *apiExtra) extra() *schema.ExtraUsage {
	if x == nil {
		return nil
	}
	return &schema.ExtraUsage{Utilization: x.Utilization}
}

func (l *limitEntry) modelName() (string, bool) {
	if l.Scope == nil || l.Scope.Model == nil {
		return "", false
	}
	return l.Scope.Model.DisplayName, true
}

func (l *limitEntry) window() *schema.Window {
	if l.Percent == nil {
		return nil
	}
	return &schema.Window{Utilization: *l.Percent, ResetsAt: provider.ParseTolerantTime(l.ResetsAt)}
}

func (c *Client) decode(body io.Reader) (*provider.FetchedSnapshot, error) {
	var r apiResponse
	dec := json.NewDecoder(io.LimitReader(body, maxBodyBytes))
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%w: decode usage response: %w", provider.ErrTransient, err)
	}
	scoped := decodeScopedLimits(r.Limits)
	_, opusShadowed := scoped["Opus"]
	scoped, _ = backfillOpus(scoped, r.SevenDayOpus.window())
	snap := &schema.Snapshot{
		FetchedAt:    c.now(),
		FiveHour:     r.FiveHour.window(),
		SevenDay:     r.SevenDay.window(),
		ScopedLimits: scoped,
		ExtraUsage:   r.ExtraUsage.extra(),
	}
	return &provider.FetchedSnapshot{Snapshot: snap, ScopedProbed: true, Drift: driftIndicators(&r, snap, opusShadowed)}, nil
}

func driftIndicators(r *apiResponse, snap *schema.Snapshot, opusShadowed bool) []string {
	var d []string
	if snap.FiveHour == nil &&
		snap.SevenDay == nil &&
		len(snap.ScopedLimits) == 0 &&
		(snap.ExtraUsage == nil || snap.ExtraUsage.Utilization == nil) {
		d = append(d, "empty usage payload")
	}
	d = windowDrift(d, "five_hour", r.FiveHour)
	d = windowDrift(d, "seven_day", r.SevenDay)
	if !opusShadowed {
		d = windowDrift(d, "seven_day_opus", r.SevenDayOpus)
	}
	for i := range r.Limits {
		d = limitDrift(d, &r.Limits[i])
	}
	return d
}

func windowDrift(d []string, label string, w *apiWindow) []string {
	if w == nil {
		return d
	}
	if w.Utilization == nil {
		return append(d, label+" utilization is null")
	}
	return resetDrift(d, label, w.ResetsAt, true)
}

func limitDrift(d []string, l *limitEntry) []string {
	name, ok := l.modelName()
	if !ok {
		return d
	}
	switch {
	case name == "":
		return append(d, "limits entry missing model display_name")
	case l.Kind != scopedLimitKind:
		return append(d, fmt.Sprintf("unknown scoped limits kind %q", l.Kind))
	case l.Percent == nil:
		if l.IsActive != nil && !*l.IsActive {
			return d
		}
		return append(d, fmt.Sprintf("scoped limits %q percent is null", name))
	default:
		return resetDrift(d, fmt.Sprintf("scoped limits %q", name), l.ResetsAt, *l.Percent > 0)
	}
}

type resetShape int

const (
	resetRFC3339 resetShape = iota
	resetAbsent
	resetNumeric
	resetUnparseable
)

func classifyReset(raw json.RawMessage) resetShape {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return resetAbsent
	}
	if provider.ParseTolerantTime(raw).IsZero() {
		return resetUnparseable
	}
	if raw[0] == '"' {
		return resetRFC3339
	}
	return resetNumeric
}

func resetDrift(d []string, label string, raw json.RawMessage, requireReset bool) []string {
	switch classifyReset(raw) {
	case resetAbsent:
		if requireReset {
			d = append(d, label+" missing resets_at")
		}
	case resetNumeric:
		d = append(d, label+" resets_at is numeric")
	case resetUnparseable:
		d = append(d, label+" resets_at unparseable")
	}
	return d
}

func backfillOpus(scoped map[string]*schema.Window, w *schema.Window) (map[string]*schema.Window, bool) {
	if w == nil {
		return scoped, false
	}
	if _, ok := scoped["Opus"]; ok {
		return scoped, false
	}
	if scoped == nil {
		scoped = make(map[string]*schema.Window, 1)
	}
	scoped["Opus"] = w
	return scoped, true
}

func decodeScopedLimits(limits []limitEntry) map[string]*schema.Window {
	var m map[string]*schema.Window
	weekly := make(map[string]bool)
	for i := range limits {
		l := &limits[i]
		name, ok := l.modelName()
		w := l.window()
		if !ok || name == "" || w == nil {
			continue
		}
		known := l.Kind == scopedLimitKind
		if weekly[name] && !known {
			continue
		}
		if m == nil {
			m = make(map[string]*schema.Window)
		}
		m[name] = w
		if known {
			weekly[name] = true
		}
	}
	return m
}
