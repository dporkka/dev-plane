package artifacts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS3StoreRoundTripAndDeduplicates(t *testing.T) {
	var (
		mu      sync.Mutex
		objects = map[string][]byte{}
		puts    int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Errorf("missing SigV4 authorization: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Amz-Date") == "" || r.Header.Get("X-Amz-Content-Sha256") == "" {
			t.Error("missing SigV4 headers")
		}

		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodHead:
			if _, ok := objects[r.URL.Path]; !ok {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			payload, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read put body: %v", err)
				http.Error(w, "read body", http.StatusInternalServerError)
				return
			}
			objects[r.URL.Path] = payload
			puts++
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			payload, ok := objects[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(payload)
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store, err := NewS3Store(S3StoreConfig{
		Endpoint:        server.URL,
		Region:          "auto",
		Bucket:          "artifacts",
		Prefix:          "cas",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		AllowInsecure:   true,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time {
		return time.Date(2026, 9, 22, 14, 0, 0, 0, time.UTC)
	}

	payload := []byte("large-media-payload")
	first, err := store.Put(context.Background(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Put(context.Background(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("descriptors differ: %+v != %+v", first, second)
	}
	if puts != 1 {
		t.Fatalf("PUT count = %d, want 1", puts)
	}

	wantSuffix := "/artifacts/cas/sha256/" + first.Digest.Hex[:2] + "/" + first.Digest.Hex[2:]
	found := false
	for key := range objects {
		if strings.HasSuffix(key, wantSuffix) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("CAS object path suffix %q not found in %+v", wantSuffix, objects)
	}

	ok, err := store.Has(context.Background(), first.Digest)
	if err != nil || !ok {
		t.Fatalf("Has() = %v, %v; want true, nil", ok, err)
	}
	reader, err := store.Open(context.Background(), first.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestS3StoreMissingObject(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	store, err := NewS3Store(S3StoreConfig{
		Endpoint:        server.URL,
		Region:          "auto",
		Bucket:          "artifacts",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		AllowInsecure:   true,
		HTTPClient:      server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	missing := HashBytes([]byte("missing"))
	ok, err := store.Has(context.Background(), missing)
	if err != nil || ok {
		t.Fatalf("Has() = %v, %v; want false, nil", ok, err)
	}
	_, err = store.Open(context.Background(), missing)
	if err != ErrNotFound {
		t.Fatalf("Open() error = %v, want ErrNotFound", err)
	}
}

func TestS3StoreRejectsInsecureEndpointByDefault(t *testing.T) {
	_, err := NewS3Store(S3StoreConfig{
		Endpoint:        "http://127.0.0.1:9000",
		Bucket:          "artifacts",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
	})
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("expected insecure endpoint rejection, got %v", err)
	}
}
