package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RemoteStorageProvider is the abstraction for fetching XDR dumps from remote cloud storage.
type RemoteStorageProvider interface {
	// Scheme returns the URI scheme this provider handles ("s3" or "gs").
	Scheme() string
	// ReadObject downloads or retrieves the object at the given URI.
	ReadObject(ctx context.Context, uri string) ([]byte, error)
}

// ParseRemoteURI parses a cloud object storage URI into scheme, bucket, and key.
// E.g. "s3://my-bucket/path/to/dump.xdr" -> ("s3", "my-bucket", "path/to/dump.xdr").
func ParseRemoteURI(uri string) (scheme, bucket, key string, err error) {
	switch {
	case strings.HasPrefix(uri, "s3://"):
		trimmed := strings.TrimPrefix(uri, "s3://")
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", "", fmt.Errorf("invalid S3 URI %q: expected s3://<bucket>/<key>", uri)
		}
		return "s3", parts[0], parts[1], nil
	case strings.HasPrefix(uri, "gs://"):
		trimmed := strings.TrimPrefix(uri, "gs://")
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return "", "", "", fmt.Errorf("invalid GCS URI %q: expected gs://<bucket>/<key>", uri)
		}
		return "gs", parts[0], parts[1], nil
	default:
		return "", "", "", fmt.Errorf("unsupported remote storage scheme in URI %q: expected s3:// or gs://", uri)
	}
}

// S3Provider implements RemoteStorageProvider for Amazon S3.
type S3Provider struct {
	// BaseURL overrides the remote endpoint, primarily used for testing with httptest.Server.
	BaseURL string
	// HTTPClient is the HTTP client used for requests.
	HTTPClient *http.Client
}

// NewS3Provider creates a new S3Provider.
func NewS3Provider() *S3Provider {
	return &S3Provider{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Scheme implements RemoteStorageProvider.
func (p *S3Provider) Scheme() string { return "s3" }

// ReadObject fetches the object from S3.
func (p *S3Provider) ReadObject(ctx context.Context, uri string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	_, bucket, key, err := ParseRemoteURI(uri)
	if err != nil {
		return nil, err
	}

	var reqURL string
	if p.BaseURL != "" {
		reqURL = fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(p.BaseURL, "/"), bucket, key)
	} else {
		// Verify environment credentials for live calls.
		accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
		if accessKey == "" {
			return nil, fmt.Errorf("missing AWS credentials: AWS_ACCESS_KEY_ID is not set in environment (offline fallback)")
		}
		reqURL = fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucket, key)
	}

	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating S3 request for %s: %w", uri, err)
	}

	if p.BaseURL == "" {
		req.Header.Set("User-Agent", "stellar-focus")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s from S3: %w", uri, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", uri, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("S3 request for %s failed with status %d", uri, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// GCSProvider implements RemoteStorageProvider for Google Cloud Storage.
type GCSProvider struct {
	// BaseURL overrides the remote endpoint, primarily used for testing with httptest.Server.
	BaseURL string
	// HTTPClient is the HTTP client used for requests.
	HTTPClient *http.Client
}

// NewGCSProvider creates a new GCSProvider.
func NewGCSProvider() *GCSProvider {
	return &GCSProvider{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Scheme implements RemoteStorageProvider.
func (p *GCSProvider) Scheme() string { return "gs" }

// ReadObject fetches the object from Google Cloud Storage.
func (p *GCSProvider) ReadObject(ctx context.Context, uri string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	_, bucket, key, err := ParseRemoteURI(uri)
	if err != nil {
		return nil, err
	}

	var reqURL string
	if p.BaseURL != "" {
		reqURL = fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(p.BaseURL, "/"), bucket, key)
	} else {
		// Verify environment credentials for live calls.
		credPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
		if credPath == "" {
			return nil, fmt.Errorf("missing Google Cloud credentials: GOOGLE_APPLICATION_CREDENTIALS is not set in environment (offline fallback)")
		}
		reqURL = fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, key)
	}

	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating GCS request for %s: %w", uri, err)
	}

	if p.BaseURL == "" {
		req.Header.Set("User-Agent", "stellar-focus")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s from GCS: %w", uri, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", uri, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("GCS request for %s failed with status %d", uri, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// DispatchRemoteProvider returns the appropriate RemoteStorageProvider for uri.
func DispatchRemoteProvider(uri string) (RemoteStorageProvider, error) {
	switch {
	case strings.HasPrefix(uri, "s3://"):
		return NewS3Provider(), nil
	case strings.HasPrefix(uri, "gs://"):
		return NewGCSProvider(), nil
	default:
		return nil, fmt.Errorf("unsupported remote storage scheme in %s: expected s3:// or gs://", uri)
	}
}

// FetchRemoteWithCache retrieves an object via provider, saving it to cacheDir (.focus/cache) on demand.
func FetchRemoteWithCache(ctx context.Context, provider RemoteStorageProvider, uri string, cacheDir string) ([]byte, error) {
	if cacheDir == "" {
		cacheDir = DefaultCacheDir
	}

	// Stable cache file based on URI hash.
	sum := sha256.Sum256([]byte(uri))
	cacheKey := hex.EncodeToString(sum[:16])
	cacheFile := filepath.Join(cacheDir, "remote", cacheKey+".xdr")

	if cached, err := os.ReadFile(cacheFile); err == nil && len(cached) > 0 {
		return cached, nil
	}

	data, err := provider.ReadObject(ctx, uri)
	if err != nil {
		return nil, err
	}

	// Persist to local cache. Caching is best-effort: a failed write only
	// means the next read fetches the object from the remote again.
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0o755); err == nil {
		//nolint:errcheck // a cache write failure must not fail the read.
		os.WriteFile(cacheFile, data, 0o600)
	}

	return data, nil
}
