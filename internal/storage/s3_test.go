package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3BackendPutOpenDelete(t *testing.T) {
	fake := newFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)

	info, err := backend.Put(context.Background(), "projects/p1/objects/o1", strings.NewReader("hello"), 10)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	wantHash := sha256.Sum256([]byte("hello"))
	if info.Size != 5 || info.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("info = %+v", info)
	}

	rc, err := backend.Open(context.Background(), "projects/p1/objects/o1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}

	if err := backend.Delete(context.Background(), "projects/p1/objects/o1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := backend.Open(context.Background(), "projects/p1/objects/o1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open deleted err = %v, want ErrNotFound", err)
	}
}

func TestS3BackendValidation(t *testing.T) {
	if _, err := NewS3Backend(context.Background(), "", S3Config{Endpoint: "http://example.com"}); err == nil {
		t.Fatal("NewS3Backend empty bucket err = nil, want error")
	}
	if _, err := NewS3Backend(context.Background(), "bucket", S3Config{}); err == nil {
		t.Fatal("NewS3Backend empty endpoint err = nil, want error")
	}

	fake := newFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)
	for _, key := range []string{"", "/absolute", "../escape", "projects\\bad"} {
		if _, err := backend.Put(context.Background(), key, strings.NewReader("x"), 10); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("Put key %q err = %v, want ErrInvalidKey", key, err)
		}
		if _, err := backend.Open(context.Background(), key); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("Open key %q err = %v, want ErrInvalidKey", key, err)
		}
		if err := backend.Delete(context.Background(), key); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("Delete key %q err = %v, want ErrInvalidKey", key, err)
		}
	}
	if _, err := backend.Put(context.Background(), "projects/p1/objects/o1", strings.NewReader("x"), 0); err == nil {
		t.Fatal("Put max 0 err = nil, want error")
	}
}

func TestS3BackendTooLargeDoesNotStore(t *testing.T) {
	fake := newFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)
	key := "projects/p1/objects/large"
	if _, err := backend.Put(context.Background(), key, strings.NewReader("toolong"), 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put too large err = %v, want ErrTooLarge", err)
	}
	if fake.has("bucket", key) {
		t.Fatal("too-large object was stored")
	}
}

func TestS3BackendReadErrorDoesNotStore(t *testing.T) {
	fake := newFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)
	key := "projects/p1/objects/read-error"
	if _, err := backend.Put(context.Background(), key, errReader{}, 10); err == nil || !strings.Contains(err.Error(), "read storage source") {
		t.Fatalf("Put read error = %v, want wrapped read error", err)
	}
	if fake.has("bucket", key) {
		t.Fatal("read-error object was stored")
	}
}

// A seekable source, such as an upload spooled to a temp file, is streamed
// to S3 with a known length rather than copied into memory first.
func TestS3BackendPutStreamsSeekableSource(t *testing.T) {
	fake := newCountingFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)
	const size = 16 << 20
	key := "projects/p1/objects/large"

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	info, err := backend.Put(context.Background(), key, &zeroSource{size: size}, size)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > size/4 {
		t.Fatalf("Put allocated %d bytes for a %d-byte source, want it streamed", allocated, size)
	}
	wantHash := sha256.Sum256(make([]byte, size))
	if info.Size != size || info.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("info = %+v", info)
	}
	if got := fake.length("bucket", key); got != size {
		t.Fatalf("fake S3 received %d bytes, want %d", got, size)
	}
}

func TestS3BackendPutBuffersUnseekableSource(t *testing.T) {
	fake := newFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)
	key := "projects/p1/objects/unseekable"
	info, err := backend.Put(context.Background(), key, io.MultiReader(strings.NewReader("hel"), strings.NewReader("lo")), 10)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	wantHash := sha256.Sum256([]byte("hello"))
	if info.Size != 5 || info.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("info = %+v", info)
	}
	if got := fake.get("bucket", key); string(got) != "hello" {
		t.Fatalf("stored body = %q, want hello", got)
	}
	if _, err := backend.Put(context.Background(), "projects/p1/objects/unseekable-large", io.MultiReader(strings.NewReader("toolong")), 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put too large err = %v, want ErrTooLarge", err)
	}
}

func TestS3BackendPutSeekableSourceFailuresDoNotStore(t *testing.T) {
	fake := newFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)
	key := "projects/p1/objects/seek-error"
	for _, tc := range []struct {
		name   string
		source io.Reader
		want   string
	}{
		{name: "position", source: &failingSeeker{Reader: strings.NewReader("hello"), failOn: 1}, want: "seek failed"},
		{name: "rewind", source: &failingSeeker{Reader: strings.NewReader("hello"), failOn: 2}, want: "seek failed"},
		{name: "read", source: &failingSeeker{Reader: errReader{}}, want: "read storage source"},
	} {
		if _, err := backend.Put(context.Background(), key, tc.source, 10); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: Put err = %v, want %q", tc.name, err, tc.want)
		}
		if fake.has("bucket", key) {
			t.Fatalf("%s: object was stored", tc.name)
		}
	}
}

// zeroSource is a seekable source of size zero bytes that holds none of them.
type zeroSource struct {
	size int64
	pos  int64
}

func (z *zeroSource) Read(p []byte) (int, error) {
	if z.pos >= z.size {
		return 0, io.EOF
	}
	if remaining := z.size - z.pos; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	clear(p)
	z.pos += int64(len(p))
	return len(p), nil
}

func (z *zeroSource) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		z.pos = offset
	case io.SeekCurrent:
		z.pos += offset
	case io.SeekEnd:
		z.pos = z.size + offset
	}
	return z.pos, nil
}

// failingSeeker fails its failOn'th Seek; earlier ones report position 0.
type failingSeeker struct {
	io.Reader
	failOn int
	calls  int
}

func (s *failingSeeker) Seek(int64, int) (int64, error) {
	s.calls++
	if s.calls == s.failOn {
		return 0, errors.New("seek failed")
	}
	return 0, nil
}

func TestS3BackendPreventsOverwrite(t *testing.T) {
	fake := newFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)
	key := "projects/p1/objects/o1"
	if _, err := backend.Put(context.Background(), key, strings.NewReader("first"), 10); err != nil {
		t.Fatalf("Put first: %v", err)
	}
	if _, err := backend.Put(context.Background(), key, strings.NewReader("second"), 10); !errors.Is(err, ErrExists) {
		t.Fatalf("Put overwrite err = %v, want ErrExists", err)
	}
	if got := fake.get("bucket", key); string(got) != "first" {
		t.Fatalf("stored body = %q, want first", got)
	}
}

func TestS3BackendOpenDeleteMissing(t *testing.T) {
	fake := newFakeS3(t)
	backend := newTestS3Backend(t, fake.URL)
	if _, err := backend.Open(context.Background(), "projects/p1/objects/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open missing err = %v, want ErrNotFound", err)
	}
	if err := backend.Delete(context.Background(), "projects/p1/objects/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing err = %v, want ErrNotFound", err)
	}
}

func TestIsGCSXMLAPIEndpoint(t *testing.T) {
	tests := []struct {
		endpoint string
		want     bool
	}{
		{endpoint: "https://storage.googleapis.com", want: true},
		{endpoint: "https://STORAGE.GOOGLEAPIS.COM/", want: true},
		{endpoint: "https://bucket.storage.googleapis.com", want: false},
		{endpoint: "http://garage:3900", want: false},
		{endpoint: "://invalid", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			if got := isGCSXMLAPIEndpoint(tt.endpoint); got != tt.want {
				t.Fatalf("isGCSXMLAPIEndpoint(%q) = %v, want %v", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestNewS3BackendSelectsGCSInteropSigner(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	backend, err := NewS3Backend(context.Background(), "bucket", S3Config{
		Endpoint:       "https://storage.googleapis.com",
		Region:         "auto",
		ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3Backend: %v", err)
	}
	if _, ok := backend.client.Options().HTTPSignerV4.(*gcsV4Signer); !ok {
		t.Fatalf("HTTPSignerV4 = %T, want *gcsV4Signer", backend.client.Options().HTTPSignerV4)
	}

	standard := newTestS3Backend(t, "http://example.com")
	if _, ok := standard.client.Options().HTTPSignerV4.(*gcsV4Signer); ok {
		t.Fatalf("standard HTTPSignerV4 = %T, want default signer", standard.client.Options().HTTPSignerV4)
	}
}

func TestGCSV4SignerNormalizesRequests(t *testing.T) {
	payloadHash := sha256.Sum256([]byte("hello"))
	tests := []struct {
		name             string
		method           string
		acceptEncoding   string
		ifNoneMatch      string
		wantIfNoneMatch  string
		wantAcceptSigned bool
	}{
		{
			name:             "create-only put",
			method:           http.MethodPut,
			acceptEncoding:   "identity",
			ifNoneMatch:      "*",
			wantAcceptSigned: false,
		},
		{
			name:            "ordinary put",
			method:          http.MethodPut,
			wantIfNoneMatch: "",
		},
		{
			name:            "get precondition remains unchanged",
			method:          http.MethodGet,
			ifNoneMatch:     `"etag"`,
			wantIfNoneMatch: `"etag"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequest(tt.method, "https://storage.googleapis.com/bucket/key", strings.NewReader("hello"))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if tt.acceptEncoding != "" {
				request.Header.Set("Accept-Encoding", tt.acceptEncoding)
			}
			if tt.ifNoneMatch != "" {
				request.Header.Set("If-None-Match", tt.ifNoneMatch)
			}
			request.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(payloadHash[:]))

			signer := newGCSV4Signer(s3.Options{})
			err = signer.SignHTTP(
				context.Background(),
				aws.Credentials{AccessKeyID: "access", SecretAccessKey: "secret"},
				request,
				hex.EncodeToString(payloadHash[:]),
				"s3",
				"auto",
				time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC),
			)
			if err != nil {
				t.Fatalf("SignHTTP: %v", err)
			}

			if got := request.Header.Get("Accept-Encoding"); got != tt.acceptEncoding {
				t.Fatalf("Accept-Encoding = %q, want %q", got, tt.acceptEncoding)
			}
			if got := request.Header.Get("If-None-Match"); got != tt.wantIfNoneMatch {
				t.Fatalf("If-None-Match = %q, want %q", got, tt.wantIfNoneMatch)
			}
			if got := request.Header.Get("X-Goog-If-Generation-Match"); got != "" {
				t.Fatalf("X-Goog-If-Generation-Match = %q, want empty", got)
			}

			authorization := request.Header.Get("Authorization")
			acceptSigned := strings.Contains(authorization, "accept-encoding")
			if acceptSigned != tt.wantAcceptSigned {
				t.Fatalf("Authorization = %q, accept-encoding signed = %v, want %v", authorization, acceptSigned, tt.wantAcceptSigned)
			}
			generationSigned := strings.Contains(authorization, "x-goog-if-generation-match")
			if generationSigned {
				t.Fatalf("Authorization = %q, generation header must not be signed", authorization)
			}
		})
	}
}

func newTestS3Backend(t *testing.T, endpoint string) *S3Backend {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	backend, err := NewS3Backend(context.Background(), "bucket", S3Config{
		Endpoint:       endpoint,
		ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3Backend: %v", err)
	}
	return backend
}

type fakeS3 struct {
	*httptest.Server
	mu      sync.Mutex
	objects map[string][]byte
	// lengths, when set, records each stored body's length and the body
	// itself is discarded.
	lengths map[string]int64
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	return startFakeS3(t, &fakeS3{objects: make(map[string][]byte)})
}

// newCountingFakeS3 keeps only the length of each body, so a large upload
// allocates nothing for it in the test process.
func newCountingFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	return startFakeS3(t, &fakeS3{objects: make(map[string][]byte), lengths: make(map[string]int64)})
}

func startFakeS3(t *testing.T, f *fakeS3) *fakeS3 {
	t.Helper()
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeS3) handle(w http.ResponseWriter, r *http.Request) {
	bucket, key, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if !ok || bucket == "" || key == "" {
		writeFakeS3Error(w, http.StatusNotFound, "NoSuchKey")
		return
	}
	storeKey := bucket + "/" + key

	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		if _, exists := f.objects[storeKey]; exists && r.Header.Get("If-None-Match") == "*" {
			writeFakeS3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")
			return
		}
		if f.lengths != nil {
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil {
				writeFakeS3Error(w, http.StatusInternalServerError, "InternalError")
				return
			}
			f.objects[storeKey] = nil
			f.lengths[storeKey] = n
			w.WriteHeader(http.StatusOK)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeFakeS3Error(w, http.StatusInternalServerError, "InternalError")
			return
		}
		f.objects[storeKey] = body
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		body, exists := f.objects[storeKey]
		if !exists {
			writeFakeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case http.MethodHead:
		body, exists := f.objects[storeKey]
		if !exists {
			writeFakeS3Error(w, http.StatusNotFound, "NotFound")
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if _, exists := f.objects[storeKey]; !exists {
			writeFakeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		delete(f.objects, storeKey)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeFakeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func (f *fakeS3) has(bucket, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[bucket+"/"+key]
	return ok
}

func (f *fakeS3) length(bucket, key string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lengths[bucket+"/"+key]
}

func (f *fakeS3) get(bucket, key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.objects[bucket+"/"+key]...)
}

func writeFakeS3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>%s</Message></Error>", code, code)
}
