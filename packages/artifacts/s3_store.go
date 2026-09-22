package artifacts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	return s.doRequest(ctx, method, key, nil, nil, body, size, payloadHash)
}

func (s *S3Store) doRequest(
	ctx context.Context,
	method, key string,
	query url.Values,
	headers http.Header,
	body io.Reader,
	size int64,
	payloadHash string,
) (*http.Response, error) {
	u := s.objectURL(key)
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create S3 artifact request: %w", err)
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	s.sign(req, payloadHash, s.now())

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("S3 artifact request: %w", err)
	}
	return resp, nil
}

func (s *S3Store) objectURL(key string) *url.URL {
	u := *s.endpoint
	base := strings.TrimRight(u.Path, "/")
	u.Path = base + "/" + s.bucket + "/" + strings.TrimLeft(key, "/")
	u.RawPath = ""
	u.RawQuery = ""
	return &u
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
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-amz-") || lower == "x-amz-content-sha256" || lower == "x-amz-date" || lower == "x-amz-security-token" {
			continue
		}
		headers[lower] = strings.Join(values, ",")
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

type initiateMultipartResult struct {
	UploadID string `xml:"UploadId"`
}

func (s *S3Store) BeginMultipart(ctx context.Context, stagingKey, mediaType string) (string, error) {
	if strings.TrimSpace(stagingKey) == "" {
		return "", fmt.Errorf("multipart staging key is required")
	}
	headers := make(http.Header)
	if strings.TrimSpace(mediaType) != "" {
		headers.Set("Content-Type", mediaType)
	}
	resp, err := s.doRequest(ctx, http.MethodPost, stagingKey, url.Values{"uploads": {""}}, headers, nil, 0, emptySHA256Hex)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", s.responseError(resp, "initiate multipart upload")
	}
	var result initiateMultipartResult
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode multipart initiation response: %w", err)
	}
	if strings.TrimSpace(result.UploadID) == "" {
		return "", fmt.Errorf("multipart initiation response did not include upload id")
	}
	return result.UploadID, nil
}

func (s *S3Store) PresignUploadPart(ctx context.Context, stagingKey, uploadID string, partNumber int, ttl time.Duration) (PresignedPart, error) {
	if err := ctx.Err(); err != nil {
		return PresignedPart{}, err
	}
	if partNumber < 1 || partNumber > MaxMultipartParts {
		return PresignedPart{}, fmt.Errorf("multipart part number must be between 1 and %d", MaxMultipartParts)
	}
	if strings.TrimSpace(uploadID) == "" {
		return PresignedPart{}, fmt.Errorf("multipart upload id is required")
	}
	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}
	if ttl > 7*24*time.Hour {
		return PresignedPart{}, fmt.Errorf("presigned URL lifetime exceeds 7 days")
	}
	now := s.now().UTC()
	u := s.objectURL(stagingKey)
	query := url.Values{
		"partNumber":            {strconv.Itoa(partNumber)},
		"uploadId":              {uploadID},
		"X-Amz-Algorithm":       {"AWS4-HMAC-SHA256"},
		"X-Amz-Credential":      {s.accessKeyID + "/" + now.Format("20060102") + "/" + s.region + "/s3/aws4_request"},
		"X-Amz-Date":            {now.Format("20060102T150405Z")},
		"X-Amz-Expires":         {strconv.FormatInt(int64(ttl/time.Second), 10)},
		"X-Amz-SignedHeaders":   {"host"},
		"X-Amz-Content-Sha256":  {"UNSIGNED-PAYLOAD"},
	}
	if s.sessionToken != "" {
		query.Set("X-Amz-Security-Token", s.sessionToken)
	}
	u.RawQuery = query.Encode()
	canonicalHeaders := "host:" + u.Host + "\n"
	canonicalRequest := strings.Join([]string{
		http.MethodPut,
		u.EscapedPath(),
		u.RawQuery,
		canonicalHeaders,
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")
	shortDate := now.Format("20060102")
	scope := shortDate + "/" + s.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format("20060102T150405Z"),
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	dateKey := hmacSHA256([]byte("AWS4"+s.secretAccessKey), shortDate)
	regionKey := hmacSHA256(dateKey, s.region)
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	query.Set("X-Amz-Signature", signature)
	u.RawQuery = query.Encode()
	return PresignedPart{
		PartNumber: partNumber,
		URL:        u.String(),
		ExpiresAt:  now.Add(ttl),
	}, nil
}

func (s *S3Store) CompleteMultipart(ctx context.Context, stagingKey, uploadID string, parts []CompletedPart) error {
	if len(parts) == 0 {
		return fmt.Errorf("multipart completion requires at least one part")
	}
	var payload strings.Builder
	payload.WriteString("<CompleteMultipartUpload>")
	for i, part := range parts {
		if part.PartNumber != i+1 {
			return fmt.Errorf("multipart parts must be consecutive starting at 1")
		}
		etag := strings.TrimSpace(part.ETag)
		if etag == "" {
			return fmt.Errorf("multipart part %d ETag is required", part.PartNumber)
		}
		payload.WriteString("<Part><PartNumber>")
		payload.WriteString(strconv.Itoa(part.PartNumber))
		payload.WriteString("</PartNumber><ETag>")
		_ = xml.EscapeText(&payload, []byte(etag))
		payload.WriteString("</ETag></Part>")
	}
	payload.WriteString("</CompleteMultipartUpload>")
	body := []byte(payload.String())
	resp, err := s.doRequest(
		ctx, http.MethodPost, stagingKey,
		url.Values{"uploadId": {uploadID}},
		http.Header{"Content-Type": {"application/xml"}},
		strings.NewReader(string(body)), int64(len(body)), sha256Hex(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.responseError(resp, "complete multipart upload")
	}
	return nil
}

func (s *S3Store) AbortMultipart(ctx context.Context, stagingKey, uploadID string) error {
	resp, err := s.doRequest(ctx, http.MethodDelete, stagingKey, url.Values{"uploadId": {uploadID}}, nil, nil, 0, emptySHA256Hex)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.responseError(resp, "abort multipart upload")
	}
	return nil
}

func (s *S3Store) VerifyObject(ctx context.Context, key string, expected Descriptor) error {
	if !expected.Digest.Valid() {
		return fmt.Errorf("expected artifact digest is invalid")
	}
	resp, err := s.doRequest(ctx, http.MethodGet, key, nil, nil, nil, 0, emptySHA256Hex)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.responseError(resp, "verify multipart object")
	}
	h := sha256.New()
	size, err := io.Copy(h, contextReader{ctx: ctx, r: resp.Body})
	if err != nil {
		return fmt.Errorf("hash multipart object: %w", err)
	}
	if size != expected.Size {
		return fmt.Errorf("artifact size mismatch: got %d, want %d", size, expected.Size)
	}
	actual := Digest{Algorithm: AlgorithmSHA256, Hex: hex.EncodeToString(h.Sum(nil))}
	if actual != expected.Digest {
		return fmt.Errorf("artifact digest mismatch: got %s, want %s", actual.String(), expected.Digest.String())
	}
	return nil
}

func (s *S3Store) PromoteToCAS(ctx context.Context, stagingKey string, expected Descriptor) error {
	if !expected.Digest.Valid() {
		return fmt.Errorf("expected artifact digest is invalid")
	}
	exists, err := s.Has(ctx, expected.Digest)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	sourceURL := &url.URL{Path: "/" + s.bucket + "/" + strings.TrimLeft(stagingKey, "/")}
	headers := make(http.Header)
	headers.Set("X-Amz-Copy-Source", sourceURL.EscapedPath())
	if expected.MediaType != "" {
		headers.Set("X-Amz-Metadata-Directive", "REPLACE")
		headers.Set("Content-Type", expected.MediaType)
	}
	resp, err := s.doRequest(ctx, http.MethodPut, s.objectKey(expected.Digest), nil, headers, nil, 0, emptySHA256Hex)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.responseError(resp, "promote multipart object")
	}
	return nil
}

func (s *S3Store) DeleteObject(ctx context.Context, key string) error {
	resp, err := s.doRequest(ctx, http.MethodDelete, key, nil, nil, nil, 0, emptySHA256Hex)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return s.responseError(resp, "delete object")
	}
	return nil
}

func sha256Base64(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(sum[:])
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
