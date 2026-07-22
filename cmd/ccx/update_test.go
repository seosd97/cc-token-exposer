package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seosd97/cc-token-exposer/internal/selfupdate"
)

// latestServer serves only the release-metadata endpoint with the given tag.
func latestServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `","assets":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func updateClient(srv *httptest.Server) *selfupdate.Client {
	return selfupdate.New("o", "r",
		selfupdate.WithAPIBase(srv.URL),
		selfupdate.WithHTTPClient(srv.Client()),
		selfupdate.WithPlatform("darwin", "arm64"),
	)
}

func runUpdate(t *testing.T, up *selfupdate.Client, args ...string) (string, error) {
	t.Helper()
	cmd := newUpdateCmd(up)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestUpdateCheckReportsNewVersion(t *testing.T) {
	out, err := runUpdate(t, updateClient(latestServer(t, "v9.9.9")), "--check")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "new version available: v9.9.9") {
		t.Fatalf("output = %q, want new-version notice", out)
	}
}

func TestUpdateCheckAlreadyUpToDate(t *testing.T) {
	// The test binary's version parses to 0.0.0, so a v0.0.0 latest is equal.
	out, err := runUpdate(t, updateClient(latestServer(t, "v0.0.0")), "--check")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "already up to date") {
		t.Fatalf("output = %q, want up-to-date notice", out)
	}
}

func TestUpdateLatestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := runUpdate(t, updateClient(srv), "--check"); err == nil {
		t.Fatal("expected an error when the release lookup fails")
	}
}
