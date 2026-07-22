package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testToken is a synthetic, non-secret value used purely to assert it never
// leaks into errors. It is not a real credential.
const testToken = "synthetic-test-token-DO-NOT-LEAK"

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func newTestClient(t *testing.T, srv *httptest.Server, now time.Time) *Client {
	t.Helper()
	return New(
		WithEndpoint(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(fixedClock(now)),
	)
}

// sampleBody mirrors the live endpoint, which sends utilization as a JSON
// float (e.g. 23.0) rather than an int (regression guard for the #5
// live-checkpoint decode bug).
const sampleBody = `{
  "five_hour":       {"utilization": 23.0, "resets_at": "2026-06-12T18:00:00Z"},
  "seven_day":       {"utilization": 41.0, "resets_at": "2026-06-18T00:00:00Z"},
  "seven_day_opus":  {"utilization": 10.0, "resets_at": "2026-06-18T00:00:00Z"},
  "extra_usage":     {"utilization": 5.0},
  "some_future_field": {"nested": true},
  "another_unknown": 42
}`

func TestFetchOK(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	var gotReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleBody))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, now)
	snap, err := c.Fetch(context.Background(), testToken)
	if err != nil {
		t.Fatalf("Fetch: unexpected error: %v", err)
	}

	// Required headers.
	if got := gotReq.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("Authorization header = %q, want bearer token", got)
	}
	if got := gotReq.Header.Get("anthropic-beta"); got != betaVersion {
		t.Errorf("anthropic-beta = %q, want %q", got, betaVersion)
	}
	if got := gotReq.Header.Get("User-Agent"); got != DefaultUserAgent {
		t.Errorf("User-Agent = %q, want %q", got, DefaultUserAgent)
	}
	if !strings.HasPrefix(gotReq.Header.Get("User-Agent"), "claude-code/") {
		t.Errorf("User-Agent must mimic claude-code, got %q", gotReq.Header.Get("User-Agent"))
	}
	if got := gotReq.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}

	if !snap.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", snap.FetchedAt, now)
	}
	if snap.FiveHour == nil || snap.FiveHour.Utilization != 23 {
		t.Errorf("five_hour = %+v, want utilization 23", snap.FiveHour)
	}
	wantReset := time.Date(2026, 6, 12, 18, 0, 0, 0, time.UTC)
	if snap.FiveHour == nil || !snap.FiveHour.ResetsAt.Equal(wantReset) {
		t.Errorf("five_hour resets_at = %v, want %v", snap.FiveHour.ResetsAt, wantReset)
	}
	if snap.SevenDay == nil || snap.SevenDay.Utilization != 41 {
		t.Errorf("seven_day = %+v, want utilization 41", snap.SevenDay)
	}
	if w := snap.ScopedLimits["Opus"]; w == nil || w.Utilization != 10 {
		t.Errorf("scoped_limits[Opus] = %+v, want utilization 10 (legacy top-level fallback)", w)
	}
	if snap.ExtraUsage == nil || snap.ExtraUsage.Utilization == nil || *snap.ExtraUsage.Utilization != 5 {
		t.Errorf("extra_usage = %+v, want utilization 5", snap.ExtraUsage)
	}
}

// scopedLimitsBody mirrors the current live endpoint, which no longer fills the
// top-level seven_day_opus (it sends null) and instead carries per-model weekly
// limits in a limits[] array keyed by scope.model.display_name. Fable has no
// top-level field at all — limits[] is its only source.
const scopedLimitsBody = `{
  "five_hour":      {"utilization": 19.0, "resets_at": "2026-07-06T07:19:59Z"},
  "seven_day":      {"utilization": 58.0, "resets_at": "2026-07-08T20:59:59Z"},
  "seven_day_opus": null,
  "seven_day_fable": null,
  "extra_usage":    {"is_enabled": false, "utilization": null},
  "limits": [
    {"kind": "session",        "group": "session", "percent": 19, "severity": "normal",   "resets_at": "2026-07-06T07:19:59Z", "scope": null, "is_active": false},
    {"kind": "weekly_all",     "group": "weekly",  "percent": 58, "severity": "normal",   "resets_at": "2026-07-08T20:59:59Z", "scope": null, "is_active": false},
    {"kind": "weekly_scoped",  "group": "weekly",  "percent": 30, "severity": "normal",   "resets_at": "2026-07-08T20:59:59Z", "scope": {"model": {"display_name": "Opus"}}, "is_active": true},
    {"kind": "weekly_scoped",  "group": "weekly",  "percent": 94, "severity": "critical", "resets_at": "2026-07-08T20:59:59Z", "scope": {"model": {"display_name": "Fable"}}, "is_active": true}
  ]
}`

func TestFetchParsesScopedLimits(t *testing.T) {
	now := time.Date(2026, 7, 6, 5, 30, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(scopedLimitsBody))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, now)
	snap, err := c.Fetch(context.Background(), testToken)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(snap.ScopedLimits) != 2 {
		t.Fatalf("scoped_limits has %d entries, want 2", len(snap.ScopedLimits))
	}
	if w := snap.ScopedLimits["Fable"]; w == nil || w.Utilization != 94 {
		t.Errorf("scoped_limits[Fable] = %+v, want utilization 94 from limits[]", w)
	}
	wantReset := time.Date(2026, 7, 8, 20, 59, 59, 0, time.UTC)
	if w := snap.ScopedLimits["Fable"]; w == nil || !w.ResetsAt.Equal(wantReset) {
		t.Errorf("fable resets_at = %v, want %v", snap.ScopedLimits["Fable"], wantReset)
	}
	if w := snap.ScopedLimits["Opus"]; w == nil || w.Utilization != 30 {
		t.Errorf("scoped_limits[Opus] = %+v, want 30 sourced from limits[]", w)
	}
	if snap.FiveHour == nil || snap.FiveHour.Utilization != 19 {
		t.Errorf("five_hour = %+v, want 19", snap.FiveHour)
	}
	if snap.SevenDay == nil || snap.SevenDay.Utilization != 58 {
		t.Errorf("seven_day = %+v, want 58", snap.SevenDay)
	}
}

func TestFetchScopedLimitsAbsentLeavesWindowsNil(t *testing.T) {
	now := time.Date(2026, 7, 6, 5, 30, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleBody))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, now)
	snap, err := c.Fetch(context.Background(), testToken)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, ok := snap.ScopedLimits["Fable"]; ok {
		t.Errorf("scoped_limits[Fable] should be absent, got %+v", snap.ScopedLimits["Fable"])
	}
	if w := snap.ScopedLimits["Opus"]; w == nil || w.Utilization != 10 {
		t.Errorf("scoped_limits[Opus] = %+v, want 10 from top-level fallback", w)
	}
}

func TestFetchDynamicScopedModels(t *testing.T) {
	now := time.Date(2026, 7, 6, 5, 30, 0, 0, time.UTC)
	body := `{
	  "five_hour": {"utilization": 5.0, "resets_at": "2026-07-06T07:00:00Z"},
	  "seven_day": {"utilization": 10.0, "resets_at": "2026-07-08T20:59:59Z"},
	  "limits": [
	    {"kind": "weekly_scoped", "group": "weekly", "percent": 33, "resets_at": "2026-07-08T20:59:59Z", "scope": {"model": {"display_name": "Sonnet"}}, "is_active": true},
	    {"kind": "weekly_scoped", "group": "weekly", "percent": 88, "resets_at": "2026-07-08T20:59:59Z", "scope": {"model": {"display_name": "Cowork"}}, "is_active": true}
	  ]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, now)
	snap, err := c.Fetch(context.Background(), testToken)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.ScopedLimits) != 2 {
		t.Fatalf("scoped_limits has %d entries, want 2", len(snap.ScopedLimits))
	}
	if w := snap.ScopedLimits["Sonnet"]; w == nil || w.Utilization != 33 {
		t.Errorf("scoped_limits[Sonnet] = %+v, want 33", w)
	}
	if w := snap.ScopedLimits["Cowork"]; w == nil || w.Utilization != 88 {
		t.Errorf("scoped_limits[Cowork] = %+v, want 88", w)
	}
}

func TestFetchEmptyToken(t *testing.T) {
	c := New()
	_, err := c.Fetch(context.Background(), "")
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestFetchAuthErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		c := newTestClient(t, srv, time.Now())
		_, err := c.Fetch(context.Background(), testToken)
		if !errors.Is(err, ErrAuth) {
			t.Errorf("status %d: err = %v, want ErrAuth", status, err)
		}
		assertNoTokenLeak(t, err)
		srv.Close()
	}
}

func TestFetchRateLimitedSeconds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	_, err := c.Fetch(context.Background(), testToken)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rle.RetryAfter != 42*time.Second {
		t.Errorf("RetryAfter = %v, want 42s", rle.RetryAfter)
	}
	assertNoTokenLeak(t, err)
}

func TestFetchRateLimitedHTTPDate(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	retryAt := now.Add(90 * time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", retryAt.UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, now)
	_, err := c.Fetch(context.Background(), testToken)
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rle.RetryAfter != 90*time.Second {
		t.Errorf("RetryAfter = %v, want 90s", rle.RetryAfter)
	}
}

func TestFetchRateLimitedNoHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	_, err := c.Fetch(context.Background(), testToken)
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rle.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 when header absent", rle.RetryAfter)
	}
}

func TestFetchServerErrorsTransient(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusTeapot} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		c := newTestClient(t, srv, time.Now())
		_, err := c.Fetch(context.Background(), testToken)
		if !errors.Is(err, ErrTransient) {
			t.Errorf("status %d: err = %v, want ErrTransient", status, err)
		}
		srv.Close()
	}
}

func TestFetchMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"five_hour": {"utilization": 23,`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	_, err := c.Fetch(context.Background(), testToken)
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v, want ErrTransient for malformed JSON", err)
	}
}

func TestFetchNetworkErrorTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	client := srv.Client()
	srv.Close() // server is down: Do will fail at the transport layer.

	c := New(WithEndpoint(url), WithHTTPClient(client))
	_, err := c.Fetch(context.Background(), testToken)
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v, want ErrTransient for network failure", err)
	}
	assertNoTokenLeak(t, err)
}

func TestFetchContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newTestClient(t, srv, time.Now())
	_, err := c.Fetch(ctx, testToken)
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v, want ErrTransient for canceled context", err)
	}
}

// assertNoTokenLeak ensures the token never appears in an error string.
func assertNoTokenLeak(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), testToken) {
		t.Fatalf("token leaked into error: %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"  ", 0},
		{"0", 0},
		{"-5", 0},
		{"30", 30 * time.Second},
		{" 30 ", 30 * time.Second},
		{"not-a-number", 0},
		{now.Add(60 * time.Second).UTC().Format(http.TimeFormat), 60 * time.Second},
		{now.Add(-60 * time.Second).UTC().Format(http.TimeFormat), 0}, // past date
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.in, now); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
