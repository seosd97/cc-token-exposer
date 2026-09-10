package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultAPIBase = "https://api.github.com"

	userAgent = "ccx-selfupdate"

	requestTimeout = 30 * time.Second

	maxDownloadBytes = 64 << 20

	binaryName = "ccx"

	ChecksumsAsset = "checksums.txt"
)

var ErrAssetMissing = errors.New("selfupdate: release asset not found")

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

func (r *Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

type Client struct {
	http    *http.Client
	apiBase string
	owner   string
	repo    string
	goos    string
	goarch  string
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

func WithAPIBase(base string) Option {
	return func(c *Client) {
		if base != "" {
			c.apiBase = strings.TrimRight(base, "/")
		}
	}
}

func WithPlatform(goos, goarch string) Option {
	return func(c *Client) {
		if goos != "" {
			c.goos = goos
		}
		if goarch != "" {
			c.goarch = goarch
		}
	}
}

func New(owner, repo string, opts ...Option) *Client {
	c := &Client{
		http:    &http.Client{Timeout: requestTimeout},
		apiBase: defaultAPIBase,
		owner:   owner,
		repo:    repo,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) BinaryAsset() string {
	return fmt.Sprintf("%s_%s_%s.tar.gz", binaryName, c.goos, c.goarch)
}

func (c *Client) Latest(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.apiBase, c.owner, c.repo)
	body, err := c.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var rel Release
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("selfupdate: decode release: %w", err)
	}
	if rel.Tag == "" {
		return nil, errors.New("selfupdate: release has no tag")
	}
	return &rel, nil
}

func (c *Client) FetchAsset(ctx context.Context, rel *Release, name string) ([]byte, error) {
	a, ok := rel.asset(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAssetMissing, name)
	}
	return c.get(ctx, a.URL, "application/octet-stream")
}

func (c *Client) get(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("selfupdate: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("selfupdate: %s: unexpected status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
}

func VerifyChecksum(data []byte, name string, checksums []byte) error {
	want := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("selfupdate: no checksum for %s", name)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("selfupdate: checksum mismatch for %s: got %s, want %s", name, got, want)
	}
	return nil
}

func VerifySignedChecksums(checksums, sig []byte, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("selfupdate: invalid public key length %d", len(pub))
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil {
		return fmt.Errorf("selfupdate: decode signature: %w", err)
	}
	if len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("selfupdate: signature has length %d, want %d", len(raw), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, checksums, raw) {
		return errors.New("selfupdate: checksums signature invalid")
	}
	return nil
}

func ExtractBinary(targz []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(targz))
	if err != nil {
		return nil, fmt.Errorf("selfupdate: gunzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("selfupdate: read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || pathBase(hdr.Name) != binaryName {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxDownloadBytes))
		if err != nil {
			return nil, fmt.Errorf("selfupdate: extract %s: %w", binaryName, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("selfupdate: %s not found in archive", binaryName)
}

func pathBase(name string) string {
	name = strings.TrimRight(name, "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}
