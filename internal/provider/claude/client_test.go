package claude

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/provider"
)

const testToken = "synthetic-test-token-DO-NOT-LEAK"

var testCreds = &provider.Credentials{AccessToken: testToken}

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
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: unexpected error: %v", err)
	}

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

	if !snap.ScopedProbed {
		t.Errorf("ScopedProbed = false, want true (decode sets API provenance)")
	}
	if !snap.Snapshot.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", snap.Snapshot.FetchedAt, now)
	}
	if snap.Snapshot.FiveHour == nil || snap.Snapshot.FiveHour.Utilization != 23 {
		t.Errorf("five_hour = %+v, want utilization 23", snap.Snapshot.FiveHour)
	}
	wantReset := time.Date(2026, 6, 12, 18, 0, 0, 0, time.UTC)
	if snap.Snapshot.FiveHour == nil || !snap.Snapshot.FiveHour.ResetsAt.Equal(wantReset) {
		t.Errorf("five_hour resets_at = %v, want %v", snap.Snapshot.FiveHour.ResetsAt, wantReset)
	}
	if snap.Snapshot.SevenDay == nil || snap.Snapshot.SevenDay.Utilization != 41 {
		t.Errorf("seven_day = %+v, want utilization 41", snap.Snapshot.SevenDay)
	}
	if w := snap.Snapshot.ScopedLimits["Opus"]; w == nil || w.Utilization != 10 {
		t.Errorf("scoped_limits[Opus] = %+v, want utilization 10 (legacy top-level fallback)", w)
	}
	if snap.Snapshot.ExtraUsage == nil || snap.Snapshot.ExtraUsage.Utilization == nil || *snap.Snapshot.ExtraUsage.Utilization != 5 {
		t.Errorf("extra_usage = %+v, want utilization 5", snap.Snapshot.ExtraUsage)
	}
}

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
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(snap.Snapshot.ScopedLimits) != 2 {
		t.Fatalf("scoped_limits has %d entries, want 2", len(snap.Snapshot.ScopedLimits))
	}
	if w := snap.Snapshot.ScopedLimits["Fable"]; w == nil || w.Utilization != 94 {
		t.Errorf("scoped_limits[Fable] = %+v, want utilization 94 from limits[]", w)
	}
	wantReset := time.Date(2026, 7, 8, 20, 59, 59, 0, time.UTC)
	if w := snap.Snapshot.ScopedLimits["Fable"]; w == nil || !w.ResetsAt.Equal(wantReset) {
		t.Errorf("fable resets_at = %v, want %v", snap.Snapshot.ScopedLimits["Fable"], wantReset)
	}
	if w := snap.Snapshot.ScopedLimits["Opus"]; w == nil || w.Utilization != 30 {
		t.Errorf("scoped_limits[Opus] = %+v, want 30 sourced from limits[]", w)
	}
	if snap.Snapshot.FiveHour == nil || snap.Snapshot.FiveHour.Utilization != 19 {
		t.Errorf("five_hour = %+v, want 19", snap.Snapshot.FiveHour)
	}
	if snap.Snapshot.SevenDay == nil || snap.Snapshot.SevenDay.Utilization != 58 {
		t.Errorf("seven_day = %+v, want 58", snap.Snapshot.SevenDay)
	}
}

func TestFetchScopedLimitsAbsentLeavesWindowsNil(t *testing.T) {
	now := time.Date(2026, 7, 6, 5, 30, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleBody))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, now)
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, ok := snap.Snapshot.ScopedLimits["Fable"]; ok {
		t.Errorf("scoped_limits[Fable] should be absent, got %+v", snap.Snapshot.ScopedLimits["Fable"])
	}
	if w := snap.Snapshot.ScopedLimits["Opus"]; w == nil || w.Utilization != 10 {
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
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Snapshot.ScopedLimits) != 2 {
		t.Fatalf("scoped_limits has %d entries, want 2", len(snap.Snapshot.ScopedLimits))
	}
	if w := snap.Snapshot.ScopedLimits["Sonnet"]; w == nil || w.Utilization != 33 {
		t.Errorf("scoped_limits[Sonnet] = %+v, want 33", w)
	}
	if w := snap.Snapshot.ScopedLimits["Cowork"]; w == nil || w.Utilization != 88 {
		t.Errorf("scoped_limits[Cowork] = %+v, want 88", w)
	}
}

func TestFetchEmptyToken(t *testing.T) {
	c := New()
	_, err := c.Fetch(context.Background(), &provider.Credentials{})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want provider.ErrAuth", err)
	}
}

const driftBody = `{
  "five_hour": {"utilization": 23.0},
  "seven_day": {"utilization": 41.0, "resets_at": "2026-06-18T00:00:00Z"},
  "limits": [
    {"kind": "weekly_scoped", "group": "weekly", "percent": 30, "resets_at": "2026-06-18T00:00:00Z", "scope": {"model": {}}},
    {"kind": "weekly_scoped_v2", "group": "weekly", "percent": 50, "resets_at": "2026-06-18T00:00:00Z", "scope": {"model": {"display_name": "Mystery"}}}
  ]
}`

func TestFetchReportsDriftIndicators(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(driftBody))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	want := []string{
		"five_hour missing resets_at",
		"limits entry missing model display_name",
		`unknown scoped limits kind "weekly_scoped_v2"`,
	}
	if len(snap.Drift) != len(want) {
		t.Fatalf("Drift = %v, want %v", snap.Drift, want)
	}
	for i, w := range want {
		if snap.Drift[i] != w {
			t.Errorf("Drift[%d] = %q, want %q", i, snap.Drift[i], w)
		}
	}
	if snap.Snapshot.FiveHour == nil || snap.Snapshot.FiveHour.Utilization != 23 {
		t.Errorf("drift must not block decoding: five_hour = %+v", snap.Snapshot.FiveHour)
	}
	if w := snap.Snapshot.ScopedLimits["Mystery"]; w == nil || w.Utilization != 50 {
		t.Errorf("drift must not drop decodable scoped data: Mystery = %+v", w)
	}
}

func TestFetchEmptyPayloadFlagsDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"seven_day_opus": null, "limits": []}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Drift) != 1 || snap.Drift[0] != "empty usage payload" {
		t.Fatalf("Drift = %v, want [empty usage payload]", snap.Drift)
	}
}

func TestFetchCleanResponseHasNoDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(scopedLimitsBody))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Drift) != 0 {
		t.Fatalf("Drift = %v, want none on the documented shape", snap.Drift)
	}
}

func TestFetchGroupScopedOnlyPayloadFlagsEmptyDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"limits": [
			{"kind": "session",    "group": "session", "percent": 19, "resets_at": "2026-07-06T07:19:59Z", "scope": null},
			{"kind": "weekly_all", "group": "weekly",  "percent": 58, "resets_at": "2026-07-08T20:59:59Z", "scope": null}
		]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Snapshot.ScopedLimits) != 0 {
		t.Fatalf("scoped_limits = %+v, want none (group-scoped entries are not decodable)", snap.Snapshot.ScopedLimits)
	}
	if len(snap.Drift) != 1 || snap.Drift[0] != "empty usage payload" {
		t.Fatalf("Drift = %v, want [empty usage payload]", snap.Drift)
	}
}

func TestFetchEmptyExtraUsageFlagsEmptyDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"extra_usage": {}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Drift) != 1 || snap.Drift[0] != "empty usage payload" {
		t.Fatalf("Drift = %v, want [empty usage payload]", snap.Drift)
	}
}

func TestFetchLegacyOpusMissingResetsFlagsDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"seven_day_opus": {"utilization": 10.0}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Drift) != 1 || snap.Drift[0] != "seven_day_opus missing resets_at" {
		t.Fatalf("Drift = %v, want [seven_day_opus missing resets_at]", snap.Drift)
	}
	if w := snap.Snapshot.ScopedLimits["Opus"]; w == nil || w.Utilization != 10 {
		t.Fatalf("drift must not block decoding: scoped_limits[Opus] = %+v, want utilization 10", w)
	}
}

func TestFetchShadowedLegacyOpusNotFlagged(t *testing.T) {
	body := `{
	  "seven_day_opus": {"utilization": 10.0},
	  "limits": [
	    {"kind": "weekly_scoped", "group": "weekly", "percent": 30, "resets_at": "2026-07-08T20:59:59Z", "scope": {"model": {"display_name": "Opus"}}}
	  ]
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Drift) != 0 {
		t.Fatalf("Drift = %v, want none (legacy field is shadowed by limits[])", snap.Drift)
	}
	if w := snap.Snapshot.ScopedLimits["Opus"]; w == nil || w.Utilization != 30 {
		t.Fatalf("scoped_limits[Opus] = %+v, want utilization 30 from limits[]", w)
	}
}

func TestFetchAuthErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		c := newTestClient(t, srv, time.Now())
		_, err := c.Fetch(context.Background(), testCreds)
		if !errors.Is(err, provider.ErrAuth) {
			t.Errorf("status %d: err = %v, want provider.ErrAuth", status, err)
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
	_, err := c.Fetch(context.Background(), testCreds)
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Fatalf("err = %v, want provider.ErrRateLimited", err)
	}
	var rle *provider.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *provider.RateLimitError", err)
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
	_, err := c.Fetch(context.Background(), testCreds)
	var rle *provider.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *provider.RateLimitError", err)
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
	_, err := c.Fetch(context.Background(), testCreds)
	var rle *provider.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *provider.RateLimitError", err)
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
		_, err := c.Fetch(context.Background(), testCreds)
		if !errors.Is(err, provider.ErrTransient) {
			t.Errorf("status %d: err = %v, want provider.ErrTransient", status, err)
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
	_, err := c.Fetch(context.Background(), testCreds)
	if !errors.Is(err, provider.ErrTransient) {
		t.Fatalf("err = %v, want provider.ErrTransient for malformed JSON", err)
	}
}

func TestFetchNetworkErrorTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	client := srv.Client()
	srv.Close()

	c := New(WithEndpoint(url), WithHTTPClient(client))
	_, err := c.Fetch(context.Background(), testCreds)
	if !errors.Is(err, provider.ErrTransient) {
		t.Fatalf("err = %v, want provider.ErrTransient for network failure", err)
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
	_, err := c.Fetch(ctx, testCreds)
	if !errors.Is(err, provider.ErrTransient) {
		t.Fatalf("err = %v, want provider.ErrTransient for canceled context", err)
	}
}

func assertNoTokenLeak(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), testToken) {
		t.Fatalf("token leaked into error: %v", err)
	}
}

func TestFetchNumericResetsAtDecodesWithDrift(t *testing.T) {
	reset := time.Date(2026, 6, 12, 18, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"five_hour": {"utilization": 23, "resets_at": ` + strconv.FormatInt(reset.Unix(), 10) + `}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("an epoch resets_at must decode, not fail the response: %v", err)
	}
	if snap.Snapshot.FiveHour == nil || !snap.Snapshot.FiveHour.ResetsAt.Equal(reset) {
		t.Fatalf("five_hour = %+v, want the epoch reset decoded", snap.Snapshot.FiveHour)
	}
	if len(snap.Drift) != 1 || snap.Drift[0] != "five_hour resets_at is numeric" {
		t.Fatalf("Drift = %v, want the numeric fallback flagged", snap.Drift)
	}
}

func TestFetchUnparseableResetsAtKeepsValueAndFlagsDrift(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"seven_day": {"utilization": 41, "resets_at": "tomorrow"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv, time.Now())
	snap, err := c.Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("an unparseable resets_at must not fail the response: %v", err)
	}
	if snap.Snapshot.SevenDay == nil || snap.Snapshot.SevenDay.Utilization != 41 || !snap.Snapshot.SevenDay.ResetsAt.IsZero() {
		t.Fatalf("seven_day = %+v, want 41%% with a zero reset", snap.Snapshot.SevenDay)
	}
	if len(snap.Drift) != 1 || snap.Drift[0] != "seven_day resets_at unparseable" {
		t.Fatalf("Drift = %v, want the unparseable reset flagged", snap.Drift)
	}
}

func fetchBody(t *testing.T, body string) *provider.FetchedSnapshot {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	snap, err := newTestClient(t, srv, time.Now()).Fetch(context.Background(), testCreds)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return snap
}

func assertDrift(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Drift = %q, want %q", got, want)
	}
}

func TestFetchNullUtilizationDropsWindowAndFlagsDrift(t *testing.T) {
	snap := fetchBody(t, `{
	  "five_hour": {"utilization": null, "resets_at": "2026-06-12T18:00:00Z"},
	  "seven_day": {"utilization": 41, "resets_at": "2026-06-18T00:00:00Z"},
	  "seven_day_opus": {"utilization": null, "resets_at": null}
	}`)
	if snap.Snapshot.FiveHour != nil {
		t.Fatalf("five_hour = %+v, want dropped: a null utilization is unknown, not 0", snap.Snapshot.FiveHour)
	}
	if snap.Snapshot.SevenDay == nil || snap.Snapshot.SevenDay.Utilization != 41 {
		t.Fatalf("seven_day = %+v, want 41 kept", snap.Snapshot.SevenDay)
	}
	if _, ok := snap.Snapshot.ScopedLimits["Opus"]; ok {
		t.Fatalf("a null legacy seven_day_opus must not backfill Opus, got %+v", snap.Snapshot.ScopedLimits["Opus"])
	}
	assertDrift(t, snap.Drift, []string{"five_hour utilization is null", "seven_day_opus utilization is null"})
}

func TestFetchNullScopedPercentIsNotLifted(t *testing.T) {
	snap := fetchBody(t, `{
	  "five_hour": {"utilization": 5, "resets_at": "2026-07-06T07:00:00Z"},
	  "limits": [
	    {"kind": "weekly_scoped", "group": "weekly", "percent": null, "resets_at": null, "scope": {"model": {"display_name": "Opus"}}, "is_active": false},
	    {"kind": "weekly_scoped", "group": "weekly", "percent": null, "resets_at": "2026-07-08T20:59:59Z", "scope": {"model": {"display_name": "Sonnet"}}, "is_active": true},
	    {"kind": "weekly_scoped", "group": "weekly", "percent": null, "resets_at": null, "scope": {"model": {"display_name": "Haiku"}}},
	    {"kind": "weekly_scoped", "group": "weekly", "percent": 13, "resets_at": "2026-07-08T20:59:59Z", "scope": {"model": {"display_name": "Fable"}}, "is_active": true}
	  ]
	}`)
	if len(snap.Snapshot.ScopedLimits) != 1 || snap.Snapshot.ScopedLimits["Fable"] == nil {
		t.Fatalf("scoped_limits = %+v, want only Fable: a null percent is unknown, not 0", snap.Snapshot.ScopedLimits)
	}
	assertDrift(t, snap.Drift, []string{`scoped limits "Sonnet" percent is null`, `scoped limits "Haiku" percent is null`})
}

func TestFetchKnownScopedKindWinsNameCollision(t *testing.T) {
	weekly := `{"kind": "weekly_scoped", "group": "weekly", "percent": 12, "resets_at": "2026-07-08T20:59:59Z", "scope": {"model": {"display_name": "Opus"}}}`
	daily := `{"kind": "daily_scoped", "group": "daily", "percent": 99, "resets_at": "2026-07-07T00:00:00Z", "scope": {"model": {"display_name": "Opus"}}}`
	for name, limits := range map[string]string{"weekly first": weekly + "," + daily, "unknown first": daily + "," + weekly} {
		t.Run(name, func(t *testing.T) {
			snap := fetchBody(t, `{"five_hour": {"utilization": 5, "resets_at": "2026-07-06T07:00:00Z"}, "limits": [`+limits+`]}`)
			if w := snap.Snapshot.ScopedLimits["Opus"]; w == nil || w.Utilization != 12 {
				t.Fatalf("scoped_limits[Opus] = %+v, want the weekly_scoped entry regardless of array order", w)
			}
			assertDrift(t, snap.Drift, []string{`unknown scoped limits kind "daily_scoped"`})
		})
	}
}
