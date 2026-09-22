package artifacts

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS3StoreDirectMultipartUploadVerifyAndPromote(t *testing.T) {
	type uploadState struct {
		key   string
		parts map[int][]byte
	}
	var (
		mu      sync.Mutex
		objects = map[string][]byte{}
		uploads = map[string]*uploadState{}
		nextID  = 1
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method == http.MethodPost && r.URL.Query().Has("uploads") {
			id := "upload-" + strconv.Itoa(nextID)
			nextID++
			uploads[id] = &uploadState{key: r.URL.Path, parts: map[int][]byte{}}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, "<InitiateMultipartUploadResult><UploadId>"+id+"</UploadId></InitiateMultipartUploadResult>")
			return
		}

		if r.Method == http.MethodPut && r.URL.Query().Get("uploadId") != "" && r.URL.Query().Get("partNumber") != "" {
			id := r.URL.Query().Get("uploadId")
			part, _ := strconv.Atoi(r.URL.Query().Get("partNumber"))
			state := uploads[id]
			if state == nil {
				http.Error(w, "missing upload", http.StatusNotFound)
				return
			}
			payload, _ := io.ReadAll(r.Body)
			state.parts[part] = payload
			w.Header().Set("ETag", "\"etag-"+strconv.Itoa(part)+"\"")
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.Method == http.MethodPost && r.URL.Query().Get("uploadId") != "" {
			id := r.URL.Query().Get("uploadId")
			state := uploads[id]
			if state == nil {
				http.Error(w, "missing upload", http.StatusNotFound)
				return
			}
			var complete struct {
				Parts []struct {
					PartNumber int    `xml:"PartNumber"`
					ETag       string `xml:"ETag"`
				} `xml:"Part"`
			}
			if err := xml.NewDecoder(r.Body).Decode(&complete); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			numbers := make([]int, 0, len(state.parts))
			for n := range state.parts {
				numbers = append(numbers, n)
			}
			sort.Ints(numbers)
			var joined []byte
			for _, n := range numbers {
				joined = append(joined, state.parts[n]...)
			}
			objects[state.key] = joined
			delete(uploads, id)
			_, _ = io.WriteString(w, "<CompleteMultipartUploadResult/>")
			return
		}

		if r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") != "" {
			delete(uploads, r.URL.Query().Get("uploadId"))
			w.WriteHeader(http.StatusNoContent)
			return
		}

		switch r.Method {
		case http.MethodGet:
			payload, ok := objects[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(payload)
		case http.MethodHead:
			if _, ok := objects[r.URL.Path]; !ok {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			source := r.Header.Get("X-Amz-Copy-Source")
			if source == "" {
				http.Error(w, "copy source required", http.StatusBadRequest)
				return
			}
			sourcePath, _ := url.PathUnescape(source)
			payload, ok := objects[sourcePath]
			if !ok {
				http.Error(w, "source missing", http.StatusNotFound)
				return
			}
			objects[r.URL.Path] = append([]byte(nil), payload...)
			_, _ = io.WriteString(w, "<CopyObjectResult/>")
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store, err := NewS3Store(S3StoreConfig{
		Endpoint: server.URL, Region: "auto", Bucket: "artifacts", Prefix: "cas",
		AccessKeyID: "test-access", SecretAccessKey: "test-secret",
		AllowInsecure: true, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Date(2026, 9, 22, 16, 0, 0, 0, time.UTC) }

	stagingKey := "uploads/org/workspace/session"
	uploadID, err := store.BeginMultipart(context.Background(), stagingKey, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	if uploadID == "" {
		t.Fatal("empty upload id")
	}

	payloads := [][]byte{[]byte("hello "), []byte("world")}
	var completed []CompletedPart
	for i, payload := range payloads {
		part, err := store.PresignUploadPart(context.Background(), stagingKey, uploadID, i+1, 10*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(part.URL)
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("X-Amz-Signature") == "" || u.Query().Get("uploadId") != uploadID {
			t.Fatalf("presigned URL missing signature/upload id: %s", part.URL)
		}
		req, _ := http.NewRequest(http.MethodPut, part.URL, bytes.NewReader(payload))
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("part upload status = %d", resp.StatusCode)
		}
		completed = append(completed, CompletedPart{PartNumber: i + 1, ETag: resp.Header.Get("ETag")})
	}

	if err := store.CompleteMultipart(context.Background(), stagingKey, uploadID, completed); err != nil {
		t.Fatal(err)
	}
	full := []byte("hello world")
	desc := Descriptor{Digest: HashBytes(full), Size: int64(len(full)), MediaType: "video/mp4"}
	if err := store.VerifyObject(context.Background(), stagingKey, desc); err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteToCAS(context.Background(), stagingKey, desc); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteObject(context.Background(), stagingKey); err != nil {
		t.Fatal(err)
	}

	reader, err := store.Open(context.Background(), desc.Digest)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	_ = reader.Close()
	if !bytes.Equal(got, full) {
		t.Fatalf("CAS payload = %q, want %q", got, full)
	}
	mu.Lock()
	for objectPath := range objects {
		if strings.Contains(objectPath, "/uploads/") {
			mu.Unlock()
			t.Fatalf("staging object was not deleted: %s", objectPath)
		}
	}
	mu.Unlock()
}

func TestS3StoreVerifyObjectRejectsDigestMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "actual")
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store, err := NewS3Store(S3StoreConfig{
		Endpoint: server.URL, Region: "auto", Bucket: "artifacts",
		AccessKeyID: "key", SecretAccessKey: "secret",
		AllowInsecure: true, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte("expected")
	err = store.VerifyObject(context.Background(), "uploads/test", Descriptor{
		Digest: HashBytes(expected), Size: int64(len(expected)),
	})
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected integrity mismatch, got %v", err)
	}
}
