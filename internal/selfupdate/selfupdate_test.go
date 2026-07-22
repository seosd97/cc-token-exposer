package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// makeTarGz builds a gzip-compressed tar containing the named files.
func makeTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// releaseServer serves a GitHub-style latest-release endpoint plus asset
// downloads, with asset URLs pointing back at itself.
func releaseServer(t *testing.T, tag string, assets map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	for name, data := range assets {
		name, data := name, data
		mux.HandleFunc("/download/"+name, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(data)
		})
	}
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		var b bytes.Buffer
		fmt.Fprintf(&b, `{"tag_name":%q,"assets":[`, tag)
		first := true
		for name := range assets {
			if !first {
				b.WriteByte(',')
			}
			first = false
			fmt.Fprintf(&b, `{"name":%q,"browser_download_url":%q}`, name, srv.URL+"/download/"+name)
		}
		b.WriteString("]}")
		_, _ = w.Write(b.Bytes())
	})
	return srv
}

func testClient(t *testing.T, srv *httptest.Server) *Client {
	return New("o", "r",
		WithAPIBase(srv.URL),
		WithHTTPClient(srv.Client()),
		WithPlatform("darwin", "arm64"),
	)
}

func TestLatestAndFetchRoundTrip(t *testing.T) {
	binData := []byte("#!/fake ccx binary\n")
	targz := makeTarGz(t, map[string][]byte{"ccx": binData, "LICENSE": []byte("MIT")})
	assetName := "ccx_darwin_arm64.tar.gz"
	checksums := []byte(sha256hex(targz) + "  " + assetName + "\n" +
		"deadbeef  some_other_file\n")

	srv := releaseServer(t, "v1.2.3", map[string][]byte{
		assetName:       targz,
		"checksums.txt": checksums,
	})
	c := testClient(t, srv)

	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Tag != "v1.2.3" {
		t.Errorf("tag = %q, want v1.2.3", rel.Tag)
	}
	if c.BinaryAsset() != assetName {
		t.Errorf("BinaryAsset = %q, want %q", c.BinaryAsset(), assetName)
	}

	gotTar, err := c.FetchAsset(context.Background(), rel, assetName)
	if err != nil {
		t.Fatalf("FetchAsset: %v", err)
	}
	if !bytes.Equal(gotTar, targz) {
		t.Fatalf("fetched tarball mismatch")
	}
	gotSums, err := c.FetchAsset(context.Background(), rel, "checksums.txt")
	if err != nil {
		t.Fatalf("FetchAsset checksums: %v", err)
	}
	if err := VerifyChecksum(gotTar, assetName, gotSums); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}
	bin, err := ExtractBinary(gotTar)
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if !bytes.Equal(bin, binData) {
		t.Fatalf("extracted binary mismatch: %q", bin)
	}
}

func TestFetchAssetMissing(t *testing.T) {
	srv := releaseServer(t, "v1.0.0", map[string][]byte{"ccx_darwin_arm64.tar.gz": {1}})
	c := testClient(t, srv)
	rel, err := c.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if _, err := c.FetchAsset(context.Background(), rel, "ccx_linux_amd64.tar.gz"); !errors.Is(err, ErrAssetMissing) {
		t.Fatalf("err = %v, want ErrAssetMissing", err)
	}
}

func TestLatestNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := New("o", "r", WithAPIBase(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("payload")
	name := "ccx_darwin_arm64.tar.gz"
	good := []byte(sha256hex(data) + "  " + name + "\n")

	if err := VerifyChecksum(data, name, good); err != nil {
		t.Errorf("valid checksum rejected: %v", err)
	}
	if err := VerifyChecksum([]byte("tampered"), name, good); err == nil {
		t.Error("mismatched checksum accepted")
	}
	if err := VerifyChecksum(data, "absent.tar.gz", good); err == nil {
		t.Error("missing checksum entry accepted")
	}
}

func TestExtractBinaryAbsent(t *testing.T) {
	targz := makeTarGz(t, map[string][]byte{"README.md": []byte("hi"), "LICENSE": []byte("MIT")})
	if _, err := ExtractBinary(targz); err == nil {
		t.Fatal("expected error when ccx binary is absent from archive")
	}
}

func TestExtractBinaryBadGzip(t *testing.T) {
	if _, err := ExtractBinary([]byte("not a gzip stream")); err == nil {
		t.Fatal("expected error on malformed archive")
	}
}
