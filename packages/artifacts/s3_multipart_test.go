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


func TestS3StoreProviderSHA256CopyAvoidsGET(t *testing.T) {
	payload := []byte("native-checksum-payload")
	checksum := &MultipartChecksum{
		Algorithm: MultipartChecksumCRC64NVME,
		Base64:    CRC64NVMEBase64(payload),
	}
	desc := Descriptor{Digest: HashBytes(payload), Size: int64(len(payload)), MediaType: "application/octet-stream"}
	expectedSHA256, err := digestBase64(desc.Digest)
	if err != nil {
		t.Fatal(err)
	}
	var getCalls, shaCopyCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			if got := r.Header.Get("X-Amz-Checksum-Algorithm"); got != MultipartChecksumCRC64NVME {
				http.Error(w, "wrong checksum algorithm: "+got, http.StatusBadRequest)
				return
			}
			if got := r.Header.Get("X-Amz-Checksum-Type"); got != "FULL_OBJECT" {
				http.Error(w, "wrong checksum type: "+got, http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, "<InitiateMultipartUploadResult><UploadId>native-upload</UploadId></InitiateMultipartUploadResult>")
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "native-upload":
			if got := r.Header.Get("X-Amz-Checksum-CRC64NVME"); got != checksum.Base64 {
				http.Error(w, "wrong completion checksum: "+got, http.StatusBadRequest)
				return
			}
			if got := r.Header.Get("X-Amz-Mp-Object-Size"); got != strconv.FormatInt(desc.Size, 10) {
				http.Error(w, "wrong multipart object size: "+got, http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, "<CompleteMultipartUploadResult/>")
		case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, ".sha256-verify"):
			w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/uploads/"):
			if got := r.Header.Get("X-Amz-Checksum-Mode"); got != "ENABLED" {
				http.Error(w, "wrong checksum mode: "+got, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))
			w.Header().Set("X-Amz-Checksum-CRC64NVME", checksum.Base64)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead:
			http.NotFound(w, r)
		case r.Method == http.MethodPut:
			if r.Header.Get("X-Amz-Copy-Source") == "" {
				http.Error(w, "copy source required", http.StatusBadRequest)
				return
			}
			if r.Header.Get("X-Amz-Checksum-Algorithm") == "SHA256" {
				shaCopyCalls++
				_, _ = io.WriteString(w, "<CopyObjectResult><ChecksumSHA256>"+expectedSHA256+"</ChecksumSHA256></CopyObjectResult>")
				return
			}
			_, _ = io.WriteString(w, "<CopyObjectResult/>")
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet:
			getCalls++
			http.Error(w, "provider SHA-256 verification should not GET staging", http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
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
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	uploadID, native, err := manager.BeginMultipartWithChecksum(
		context.Background(), "uploads/org/ws/native", "application/octet-stream", checksum,
	)
	if err != nil {
		t.Fatal(err)
	}
	if uploadID != "native-upload" || !native {
		t.Fatalf("uploadID=%q native=%v", uploadID, native)
	}
	if err := manager.CompleteMultipartWithChecksum(
		context.Background(),
		"uploads/org/ws/native",
		uploadID,
		[]CompletedPart{{PartNumber: 1, ETag: "\"etag-1\""}},
		desc,
		checksum,
		native,
	); err != nil {
		t.Fatal(err)
	}
	mode, err := manager.VerifyAndPromoteMultipartWithChecksum(
		context.Background(), "uploads/org/ws/native", desc, checksum, native,
	)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "native_copy_sha256" {
		t.Fatalf("verification mode = %q", mode)
	}
	if shaCopyCalls != 1 {
		t.Fatalf("provider SHA-256 copy calls = %d, want 1", shaCopyCalls)
	}
	if getCalls != 0 {
		t.Fatalf("GET calls = %d, want 0", getCalls)
	}
}

func TestS3StoreNativeChecksumFallsBackToStreamingWhenHEADDoesNotExposeChecksum(t *testing.T) {
	payload := []byte("fallback-payload")
	checksum := &MultipartChecksum{
		Algorithm: MultipartChecksumCRC64NVME,
		Base64:    CRC64NVMEBase64(payload),
	}
	desc := Descriptor{Digest: HashBytes(payload), Size: int64(len(payload))}
	var getCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/uploads/"):
			w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/uploads/"):
			getCalls++
			_, _ = w.Write(payload)
		case r.Method == http.MethodHead:
			http.NotFound(w, r)
		case r.Method == http.MethodPut:
			_, _ = io.WriteString(w, "<CopyObjectResult/>")
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
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
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := manager.VerifyAndPromoteMultipartWithChecksum(
		context.Background(), "uploads/org/ws/fallback", desc, checksum, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "stream_sha256" {
		t.Fatalf("verification mode = %q", mode)
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1", getCalls)
	}
}

func TestS3StoreProviderSHA256MismatchDoesNotFallbackToStreaming(t *testing.T) {
	payload := []byte("expected-bytes")
	desc := Descriptor{Digest: HashBytes(payload), Size: int64(len(payload))}
	wrongSHA256, err := digestBase64(HashBytes([]byte("different-bytes")))
	if err != nil {
		t.Fatal(err)
	}
	var getCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			http.NotFound(w, r)
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Checksum-Algorithm") == "SHA256":
			_, _ = io.WriteString(w, "<CopyObjectResult><ChecksumSHA256>"+wrongSHA256+"</ChecksumSHA256></CopyObjectResult>")
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet:
			getCalls++
			_, _ = w.Write(payload)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
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
	manager, err := NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.VerifyAndPromoteMultipartWithChecksum(
		context.Background(), "uploads/test/mismatch", desc, nil, false,
	)
	if err == nil || !strings.Contains(err.Error(), "provider-computed SHA-256 mismatch") {
		t.Fatalf("expected provider SHA-256 mismatch, got %v", err)
	}
	if getCalls != 0 {
		t.Fatalf("GET calls = %d, want 0 after authoritative SHA-256 mismatch", getCalls)
	}
}

func TestS3StoreBadDigestDoesNotFallbackToUncheckedCompletion(t *testing.T) {
	var completeCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("uploadId") != "" {
			completeCalls++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "<Error><Code>BadDigest</Code><Message>checksum mismatch</Message></Error>")
			return
		}
		http.Error(w, "unexpected request", http.StatusBadRequest)
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
	checksum := &MultipartChecksum{
		Algorithm: MultipartChecksumCRC64NVME,
		Base64:    "rosUhgp5mIg=",
	}
	err = store.CompleteMultipartWithChecksum(
		context.Background(),
		"uploads/test",
		"upload-1",
		[]CompletedPart{{PartNumber: 1, ETag: "\"etag\""}},
		Descriptor{Digest: HashBytes([]byte("123456789")), Size: 9},
		checksum,
	)
	if err == nil || !strings.Contains(err.Error(), "BadDigest") {
		t.Fatalf("expected BadDigest, got %v", err)
	}
	if completeCalls != 1 {
		t.Fatalf("completion calls = %d, want 1", completeCalls)
	}
}
