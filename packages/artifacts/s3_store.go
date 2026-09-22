package artifacts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

const emptySHA256Hex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// S3StoreConfig configures an S3-compatible content-addressed store.
//
// Cloudflare R2 should use its account S3 endpoint and Region "auto".
// AWS S3 can use an explicit regional endpoint or the default endpoint derived
// from Region when Endpoint is empty.
type S3StoreConfig struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	TempDir         string
	AllowInsecure   bool
	HTTPClient      *http.Client
}

// S3Store stores immutable CAS objects in an S3-compatible bucket using AWS
// Signature Version 4. It intentionally has no cloud SDK dependency so the
// artifact core stays small and can target AWS S3, R2, MinIO, or compatible
// providers with the same binary.
type S3Store struct {
	endpoint        *url.URL
	region          string
	bucket          string
	prefix          string
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
	tempDir         string
	client          *http.Client
	now             func() time.Time
}

func NewS3Store(cfg S3StoreConfig) (*S3Store, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("S3 artifact bucket is required")
	}
	if strings.TrimSpace(cfg.AccessKeyID) == "" || strings.TrimSpace(cfg.SecretAccessKey) == "" {
		return nil, fmt.Errorf("S3 artifact access key ID and secret are required")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "us-east-1"
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		if region == "us-east-1" {
			endpoint = "https://s3.amazonaws.com"
		} else {
			endpoint = "https://s3." + region + ".amazonaws.com"
		}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse S3 artifact endpoint: %w", err)
	}
	if parsed.Scheme != "https" && !(cfg.AllowInsecure && parsed.Scheme == "http") {
		return nil, fmt.Errorf("S3 artifact endpoint must use https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("S3 artifact endpoint host is required")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("S3 artifact endpoint must not contain query or fragment")
	}

	prefix := strings.Trim(strings.ReplaceAll(cfg.Prefix, "\\", "/"), "/")
	if prefix != "" {
		clean := path.Clean(prefix)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("invalid S3 artifact prefix %q", cfg.Prefix)
		}
		prefix = clean
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}

	return &S3Store{
		endpoint:        parsed,
		region:          region,
		bucket:          cfg.Bucket,
		prefix:          prefix,
		accessKeyID:     cfg.AccessKeyID,
		secretAccessKey: cfg.SecretAccessKey,
		sessionToken:    cfg.SessionToken,
		tempDir:         cfg.TempDir,
		client:          client,
		now:             func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *S3Store) Put(ctx context.Context, r io.Reader) (Descriptor, error) {
	if r == nil {
		return Descriptor{}, fmt.Errorf("artifact reader is required")
	}
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}

	tmp, err := os.CreateTemp(s.tempDir, "dev-plane-artifact-*")
	if err != nil {
		return Descriptor{}, fmt.Errorf("create S3 artifact temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(tmp, hasher), contextReader{ctx: ctx, r: r})
	if copyErr != nil {
		_ = tmp.Close()
		return Descriptor{}, fmt.Errorf("stage S3 artifact: %w", copyErr)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return Descriptor{}, fmt.Errorf("sync S3 artifact temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Descriptor{}, fmt.Errorf("close S3 artifact temp file: %w", err)
	}

	digest := Digest{Algorithm: AlgorithmSHA256, Hex: hex.EncodeToString(hasher.Sum(nil))}
	descriptor := Descriptor{Digest: digest, Size: size}

	exists, err := s.Has(ctx, digest)
	if err != nil {
		return Descriptor{}, err
	}
	if exists {
		return descriptor, nil
	}

	file, err := os.Open(tmpPath)
	if err != nil {
		return Descriptor{}, fmt.Errorf("reopen S3 artifact temp file: %w", err)
	}
	defer file.Close()

	resp, err := s.do(ctx, http.MethodPut, s.objectKey(digest), file, size, digest.Hex)
	if err != nil {
		return Descriptor{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Descriptor{}, s.responseError(resp, "put artifact")
	}
	return descriptor, nil
}

func (s *S3Store) Open(ctx context.Context, digest Digest) (io.ReadCloser, error) {
	if !digest.Valid() {
		return nil, fmt.Errorf("invalid artifact digest %q", digest.String())
	}
	resp, err := s.do(ctx, http.MethodGet, s.objectKey(digest), nil, 0, emptySHA256Hex)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, s.responseError(resp, "get artifact")
	}
	return resp.Body, nil
}

func (s *S3Store) Has(ctx context.Context, digest Digest) (bool, error) {
	if !digest.Valid() {
		return false, fmt.Errorf("invalid artifact digest %q", digest.String())
	}
	resp, err := s.do(ctx, http.MethodHead, s.objectKey(digest), nil, 0, emptySHA256Hex)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, s.responseError(resp, "head artifact")
	}
	return true, nil
}

func (s *S3Store) objectKey(digest Digest) string {
	key := path.Join(digest.Algorithm, digest.Hex[:2], digest.Hex[2:])
	if s.prefix == "" {
		return key
	}
	return path.Join(s.prefix, key)
}

func (s *S3Store) do(ctx context.Context, method, key string, body io.Reader, size int64, payloadHash string) (*http.Response, error) {
	u := *s.endpoint
	base := strings.TrimRight(u.Path, "/")
	u.Path = base + "/" + s.bucket + "/" + strings.TrimLeft(key, "/")
	u.RawPath = ""
	u.RawQuery = ""

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create S3 artifact request: %w", err)
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	s.sign(req, payloadHash, s.now())

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("S3 artifact request: %w", err)
	}
	return resp, nil
}

func (s *S3Store) sign(req *http.Request, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	shortDate := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if s.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", s.sessionToken)
	}

	headers := map[string]string{
		"host":                 req.URL.Host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	if s.sessionToken != "" {
		headers["x-amz-security-token"] = s.sessionToken
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.Join(strings.Fields(headers[name]), " "))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.Query().Encode(),
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := shortDate + "/" + s.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	dateKey := hmacSHA256([]byte("AWS4"+s.secretAccessKey), shortDate)
	regionKey := hmacSHA256(dateKey, s.region)
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.accessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func (s *S3Store) responseError(resp *http.Response, operation string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("S3 artifact %s failed with HTTP %d: %s", operation, resp.StatusCode, message)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
