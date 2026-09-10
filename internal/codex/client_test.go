package codex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/creds"
	"github.com/seosd97/cc-token-exposer/internal/usage"
)

const testToken = "synthetic-codex-token-DO-NOT-LEAK"

var testCreds = &creds.Credentials{AccessToken: testToken, AccountID: "acct-test"}

var captureTime = time.Unix(1789020458, 0).UTC()

func serve(t *testing.T, status int, body string, header http.Header) (*Client, *http.Request) {
	t.Helper()
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := New(WithEndpoint(srv.URL), WithHTTPClient(srv.Client()), WithClock(func() time.Time { return captureTime }))
	return c, got
}

func fetch(t *testing.T, status int, body string) (*usage.FetchedSnapshot, *http.Request, error) {
	t.Helper()
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := New(WithEndpoint(srv.URL), WithHTTPClient(srv.Client()), WithClock(func() time.Time { return captureTime }))
	snap, err := c.Fetch(context.Background(), testCreds)
	return snap, got, err
}

func TestFetchSendsCodexHeaders(t *testing.T) {
	_, req, err := fetch(t, http.StatusOK, `{"rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000,"reset_at":1789038434}}}`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for k, want := range map[string]string{
		"Authorization":      "Bearer " + testToken,
		"ChatGPT-Account-Id": "acct-test",
		"originator":         originator,
		"User-Agent":         DefaultUserAgent,
		"Accept":             "application/json",
	} {
		if got := req.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	if req.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", req.Method)
	}
}

func TestFetchOmitsAccountHeaderWhenUnknown(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000,"reset_at":1789038434}}}`))
	}))
	defer srv.Close()
	c := New(WithEndpoint(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.Fetch(context.Background(), &creds.Credentials{AccessToken: testToken}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, present := got.Header["Chatgpt-Account-Id"]; present {
		t.Fatalf("account header must be omitted without an account id: %v", got.Header)
	}
}

func TestFetchDecodesTheGoldenFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/usage_2026-09-10.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fetched, _, err := fetch(t, http.StatusOK, string(body))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	s := fetched.Snapshot
	if !s.FetchedAt.Equal(captureTime) {
		t.Fatalf("fetched_at = %v, want the clock's %v", s.FetchedAt, captureTime)
	}
	if s.FiveHour != nil {
		t.Fatalf("five_hour = %+v, want absent (idle plan reports only the weekly window)", s.FiveHour)
	}
	if s.SevenDay == nil || s.SevenDay.Utilization != 18 || !s.SevenDay.ResetsAt.Equal(time.Unix(1789465100, 0)) {
		t.Fatalf("seven_day = %+v, want 18%% resetting at 1789465100", s.SevenDay)
	}
	spark := s.ScopedLimits["GPT-5.3-Codex-Spark"]
	if spark == nil || spark.Utilization != 0 || !spark.ResetsAt.Equal(time.Unix(1789625234, 0)) {
		t.Fatalf("scoped Spark = %+v, want the bucket's weekly window", spark)
	}
	if len(s.ScopedLimits) != 1 {
		t.Fatalf("scoped_limits = %v, want only the Spark bucket", s.ScopedLimits)
	}
	if !fetched.ScopedProbed {
		t.Fatal("a decoded API response must count as scoped-probed")
	}
	if len(fetched.Drift) != 0 {
		t.Fatalf("golden fixture must decode without drift, got %v", fetched.Drift)
	}
}

func TestDecodeMapsWindowsByLengthNotPosition(t *testing.T) {
	fetched, _, err := fetch(t, http.StatusOK, `{"rate_limit":{
		"primary_window":   {"used_percent":40,"limit_window_seconds":604800,"reset_at":1789465100},
		"secondary_window": {"used_percent":12,"limit_window_seconds":18000,"reset_at":1789038434}}}`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	s := fetched.Snapshot
	if s.FiveHour == nil || s.FiveHour.Utilization != 12 || s.SevenDay == nil || s.SevenDay.Utilization != 40 {
		t.Fatalf("five=%+v seven=%+v, want swapped windows mapped by length", s.FiveHour, s.SevenDay)
	}
	if len(fetched.Drift) != 0 {
		t.Fatalf("unexpected drift: %v", fetched.Drift)
	}
}

func TestDecodeAcceptsProtocolShapedWindowMinutes(t *testing.T) {
	fetched, _, err := fetch(t, http.StatusOK, `{"rate_limit":{
		"primary_window":   {"used_percent":5,"window_minutes":300,"resets_at":1789038434},
		"secondary_window": {"used_percent":9,"window_minutes":10080,"resets_at":1789465100}}}`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	s := fetched.Snapshot
	if s.FiveHour == nil || s.FiveHour.Utilization != 5 || s.SevenDay == nil || s.SevenDay.Utilization != 9 {
		t.Fatalf("five=%+v seven=%+v, want window_minutes/resets_at accepted", s.FiveHour, s.SevenDay)
	}
	if len(fetched.Drift) != 0 {
		t.Fatalf("unexpected drift: %v", fetched.Drift)
	}
}

func TestDecodeUnknownLengthFallsBackToPositionWithDrift(t *testing.T) {
	fetched, _, err := fetch(t, http.StatusOK, `{"rate_limit":{
		"primary_window":   {"used_percent":7,"limit_window_seconds":86400,"reset_at":1789038434},
		"secondary_window": {"used_percent":8,"reset_at":1789465100}}}`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	s := fetched.Snapshot
	if s.FiveHour == nil || s.FiveHour.Utilization != 7 || s.SevenDay == nil || s.SevenDay.Utilization != 8 {
		t.Fatalf("five=%+v seven=%+v, want positional fallback", s.FiveHour, s.SevenDay)
	}
	want := []string{"primary_window unexpected length 86400s", "secondary_window missing length"}
	if strings.Join(fetched.Drift, "|") != strings.Join(want, "|") {
		t.Fatalf("drift = %v, want %v", fetched.Drift, want)
	}
}

func TestDecodeDuplicateWindowTargetIsDrift(t *testing.T) {
	fetched, _, err := fetch(t, http.StatusOK, `{"rate_limit":{
		"primary_window":   {"used_percent":7,"limit_window_seconds":604800,"reset_at":1789465100},
		"secondary_window": {"used_percent":8,"limit_window_seconds":604800,"reset_at":1789465100}}}`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if fetched.Snapshot.SevenDay.Utilization != 7 || fetched.Snapshot.FiveHour != nil {
		t.Fatalf("first window must win: %+v", fetched.Snapshot)
	}
	if len(fetched.Drift) != 1 || fetched.Drift[0] != "secondary_window duplicates an already mapped window" {
		t.Fatalf("drift = %v", fetched.Drift)
	}
}

func TestDecodeResetFallbacks(t *testing.T) {
	t.Run("reset_after_seconds counts from the clock", func(t *testing.T) {
		fetched, _, err := fetch(t, http.StatusOK, `{"rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000,"reset_after_seconds":900}}}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if want := captureTime.Add(15 * time.Minute); !fetched.Snapshot.FiveHour.ResetsAt.Equal(want) {
			t.Fatalf("resets_at = %v, want %v", fetched.Snapshot.FiveHour.ResetsAt, want)
		}
		if len(fetched.Drift) != 0 {
			t.Fatalf("unexpected drift: %v", fetched.Drift)
		}
	})
	t.Run("absolute reset wins over the relative one", func(t *testing.T) {
		fetched, _, err := fetch(t, http.StatusOK, `{"rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000,"reset_after_seconds":900,"reset_at":"2026-09-11T00:00:00Z"}}}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if want := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC); !fetched.Snapshot.FiveHour.ResetsAt.Equal(want) {
			t.Fatalf("resets_at = %v, want %v", fetched.Snapshot.FiveHour.ResetsAt, want)
		}
	})
	t.Run("no reset at all keeps the value and flags drift", func(t *testing.T) {
		fetched, _, err := fetch(t, http.StatusOK, `{"rate_limit":{"primary_window":{"used_percent":33,"limit_window_seconds":18000,"reset_at":null}}}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if fetched.Snapshot.FiveHour == nil || fetched.Snapshot.FiveHour.Utilization != 33 || !fetched.Snapshot.FiveHour.ResetsAt.IsZero() {
			t.Fatalf("five_hour = %+v, want 33%% with zero reset", fetched.Snapshot.FiveHour)
		}
		if len(fetched.Drift) != 1 || fetched.Drift[0] != "primary_window missing reset" {
			t.Fatalf("drift = %v", fetched.Drift)
		}
	})
}

func TestDecodeEmptyPayloadIsDrift(t *testing.T) {
	for _, body := range []string{`{}`, `{"rate_limit":null,"additional_rate_limits":[]}`, `{"rate_limit":{"primary_window":null,"secondary_window":null}}`, `{"plan_type":"pro","credits":{"balance":"0"}}`} {
		fetched, _, err := fetch(t, http.StatusOK, body)
		if err != nil {
			t.Fatalf("Fetch(%s): %v", body, err)
		}
		if len(fetched.Drift) != 1 || fetched.Drift[0] != "empty usage payload" {
			t.Fatalf("drift for %s = %v, want empty usage payload", body, fetched.Drift)
		}
		if fetched.Snapshot.FiveHour != nil || fetched.Snapshot.SevenDay != nil || len(fetched.Snapshot.ScopedLimits) != 0 {
			t.Fatalf("empty payload should carry no windows: %+v", fetched.Snapshot)
		}
	}
}

func TestDecodeBuckets(t *testing.T) {
	main := `"rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":18000,"reset_at":1789038434}}`

	t.Run("weekly window keyed by limit_name", func(t *testing.T) {
		fetched, _, err := fetch(t, http.StatusOK, `{`+main+`,"additional_rate_limits":[{"limit_name":"Spark","metered_feature":"codex_x","rate_limit":{
			"primary_window":{"used_percent":3,"limit_window_seconds":18000,"reset_at":1789038434},
			"secondary_window":{"used_percent":44,"limit_window_seconds":604800,"reset_at":1789625234}}}]}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		w := fetched.Snapshot.ScopedLimits["Spark"]
		if w == nil || w.Utilization != 44 || !w.ResetsAt.Equal(time.Unix(1789625234, 0)) {
			t.Fatalf("Spark = %+v, want the weekly window", w)
		}
		if len(fetched.Drift) != 0 {
			t.Fatalf("unexpected drift: %v", fetched.Drift)
		}
	})

	t.Run("metered_feature names a bucket without limit_name", func(t *testing.T) {
		fetched, _, err := fetch(t, http.StatusOK, `{`+main+`,"additional_rate_limits":[{"metered_feature":"codex_x","rate_limit":{
			"primary_window":{"used_percent":44,"limit_window_seconds":604800,"reset_at":1789625234}}}]}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if fetched.Snapshot.ScopedLimits["codex_x"] == nil || len(fetched.Drift) != 0 {
			t.Fatalf("scoped=%v drift=%v", fetched.Snapshot.ScopedLimits, fetched.Drift)
		}
	})

	t.Run("nameless bucket is drift", func(t *testing.T) {
		fetched, _, err := fetch(t, http.StatusOK, `{`+main+`,"additional_rate_limits":[{"rate_limit":{"primary_window":{"used_percent":44,"limit_window_seconds":604800,"reset_at":1}}}]}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if len(fetched.Snapshot.ScopedLimits) != 0 || len(fetched.Drift) != 1 || fetched.Drift[0] != "rate limit bucket #0 missing limit_name" {
			t.Fatalf("scoped=%v drift=%v", fetched.Snapshot.ScopedLimits, fetched.Drift)
		}
	})

	t.Run("bucket without a weekly window is drift", func(t *testing.T) {
		fetched, _, err := fetch(t, http.StatusOK, `{`+main+`,"additional_rate_limits":[{"limit_name":"Spark","rate_limit":{"primary_window":{"used_percent":3,"limit_window_seconds":18000,"reset_at":1}}}]}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if len(fetched.Snapshot.ScopedLimits) != 0 || len(fetched.Drift) != 1 || fetched.Drift[0] != `rate limit bucket "Spark" has no weekly window` {
			t.Fatalf("scoped=%v drift=%v", fetched.Snapshot.ScopedLimits, fetched.Drift)
		}
	})

	t.Run("inactive bucket without windows is silent", func(t *testing.T) {
		fetched, _, err := fetch(t, http.StatusOK, `{`+main+`,"additional_rate_limits":[{"limit_name":"Spark","rate_limit":null},{"limit_name":"Other","rate_limit":{"primary_window":null,"secondary_window":null}}]}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if len(fetched.Snapshot.ScopedLimits) != 0 || len(fetched.Drift) != 0 {
			t.Fatalf("scoped=%v drift=%v", fetched.Snapshot.ScopedLimits, fetched.Drift)
		}
	})

	t.Run("missing reset counts only while the bucket is in use", func(t *testing.T) {
		idle, _, err := fetch(t, http.StatusOK, `{`+main+`,"additional_rate_limits":[{"limit_name":"Spark","rate_limit":{"secondary_window":{"used_percent":0,"limit_window_seconds":604800}}}]}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if idle.Snapshot.ScopedLimits["Spark"] == nil || len(idle.Drift) != 0 {
			t.Fatalf("idle bucket: scoped=%v drift=%v", idle.Snapshot.ScopedLimits, idle.Drift)
		}
		busy, _, err := fetch(t, http.StatusOK, `{`+main+`,"additional_rate_limits":[{"limit_name":"Spark","rate_limit":{"secondary_window":{"used_percent":20,"limit_window_seconds":604800}}}]}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if len(busy.Drift) != 1 || busy.Drift[0] != `rate limit bucket "Spark" missing reset` {
			t.Fatalf("busy bucket drift = %v", busy.Drift)
		}
	})

	t.Run("duplicate bucket names are drift", func(t *testing.T) {
		bucket := `{"limit_name":"Spark","rate_limit":{"secondary_window":{"used_percent":1,"limit_window_seconds":604800,"reset_at":1}}}`
		fetched, _, err := fetch(t, http.StatusOK, `{`+main+`,"additional_rate_limits":[`+bucket+`,`+bucket+`]}`)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if len(fetched.Snapshot.ScopedLimits) != 1 || len(fetched.Drift) != 1 || fetched.Drift[0] != `duplicate rate limit bucket "Spark"` {
			t.Fatalf("scoped=%v drift=%v", fetched.Snapshot.ScopedLimits, fetched.Drift)
		}
	})
}

func TestFetchStatusMapping(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		_, _, err := fetch(t, status, `{"detail":"nope"}`)
		if !errors.Is(err, usage.ErrAuth) {
			t.Fatalf("status %d err = %v, want ErrAuth", status, err)
		}
	}

	c, _ := serve(t, http.StatusTooManyRequests, `{}`, http.Header{"Retry-After": {"45"}})
	_, err := c.Fetch(context.Background(), testCreds)
	var rl *usage.RateLimitError
	if !errors.As(err, &rl) || !errors.Is(err, usage.ErrRateLimited) || rl.RetryAfter != 45*time.Second {
		t.Fatalf("429 err = %v, want RateLimitError with 45s", err)
	}

	_, _, err = fetch(t, http.StatusInternalServerError, `oops`)
	if !errors.Is(err, usage.ErrTransient) {
		t.Fatalf("500 err = %v, want ErrTransient", err)
	}

	_, _, err = fetch(t, http.StatusOK, `not json`)
	if !errors.Is(err, usage.ErrTransient) {
		t.Fatalf("malformed body err = %v, want ErrTransient", err)
	}
}

func TestFetchRejectsEmptyToken(t *testing.T) {
	c := New()
	for _, cr := range []*creds.Credentials{nil, {}, {AccountID: "acct"}} {
		if _, err := c.Fetch(context.Background(), cr); !errors.Is(err, usage.ErrAuth) {
			t.Fatalf("err = %v, want ErrAuth", err)
		}
	}
}

func TestFetchErrorsNeverCarryTheToken(t *testing.T) {
	_, _, err := fetch(t, http.StatusInternalServerError, testToken)
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("error leaked the token: %v", err)
	}
	c := New(WithEndpoint("http://127.0.0.1:1"), WithHTTPClient(&http.Client{Timeout: 200 * time.Millisecond}))
	_, err = c.Fetch(context.Background(), testCreds)
	if err == nil || strings.Contains(err.Error(), testToken) || !errors.Is(err, usage.ErrTransient) {
		t.Fatalf("transport error = %v, want token-free ErrTransient", err)
	}
}
