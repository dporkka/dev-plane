package workloadauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderWorkload  = "X-Dev-Plane-Workload"
	HeaderTimestamp = "X-Dev-Plane-Timestamp"
	HeaderSignature = "X-Dev-Plane-Signature"
)

var (
	ErrInvalidConfiguration = errors.New("invalid workload auth configuration")
	ErrInvalidWorkload      = errors.New("invalid workload identity")
	ErrInvalidTimestamp     = errors.New("invalid workload timestamp")
	ErrStaleRequest         = errors.New("stale workload request")
	ErrInvalidSignature     = errors.New("invalid workload signature")
)

type Verifier struct {
	workload string
	secret   []byte
	maxSkew  time.Duration
	Now      func() time.Time
}

func NewVerifier(workload, secret string, maxSkew time.Duration) (*Verifier, error) {
	workload = strings.TrimSpace(workload)
	if workload == "" {
		return nil, fmt.Errorf("%w: workload id is required", ErrInvalidConfiguration)
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("%w: secret must be at least 32 bytes", ErrInvalidConfiguration)
	}
	if maxSkew <= 0 {
		return nil, fmt.Errorf("%w: max skew must be positive", ErrInvalidConfiguration)
	}
	return &Verifier{
		workload: workload,
		secret:   []byte(secret),
		maxSkew:  maxSkew,
		Now:      time.Now,
	}, nil
}

func Sign(req *http.Request, workload, secret string, timestamp time.Time, body []byte) {
	unix := strconv.FormatInt(timestamp.Unix(), 10)
	req.Header.Set(HeaderWorkload, workload)
	req.Header.Set(HeaderTimestamp, unix)
	req.Header.Set(HeaderSignature, signature(workload, unix, req.Method, req.URL.RequestURI(), body, []byte(secret)))
}

func (v *Verifier) Verify(req *http.Request, body []byte) error {
	if req == nil {
		return ErrInvalidSignature
	}
	workload := strings.TrimSpace(req.Header.Get(HeaderWorkload))
	if workload == "" || !hmac.Equal([]byte(workload), []byte(v.workload)) {
		return ErrInvalidWorkload
	}

	rawTimestamp := strings.TrimSpace(req.Header.Get(HeaderTimestamp))
	unix, err := strconv.ParseInt(rawTimestamp, 10, 64)
	if err != nil {
		return ErrInvalidTimestamp
	}
	timestamp := time.Unix(unix, 0)
	now := v.Now()
	if timestamp.Before(now.Add(-v.maxSkew)) || timestamp.After(now.Add(v.maxSkew)) {
		return ErrStaleRequest
	}

	provided := strings.TrimSpace(req.Header.Get(HeaderSignature))
	expected := signature(workload, rawTimestamp, req.Method, req.URL.RequestURI(), body, v.secret)
	if !hmac.Equal([]byte(provided), []byte(expected)) {
		return ErrInvalidSignature
	}
	return nil
}

func signature(workload, timestamp, method, requestURI string, body, secret []byte) string {
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{
		workload,
		timestamp,
		strings.ToUpper(method),
		requestURI,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
