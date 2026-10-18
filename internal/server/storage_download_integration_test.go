package server_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
	"github.com/bradleymackey/track-slash/internal/store"
)

// slowOpenBackend serves stored bytes a chunk at a time under the context it
// was opened with, the way the S3 backend reads a remote object body. With
// stallOpen, Open waits for its context to end; with stallAfter, reads do once
// that many bytes have been served.
type slowOpenBackend struct {
	objectstorage.Backend
	chunk      int
	delay      time.Duration
	stallOpen  bool
	stallAfter int
}

func (b *slowOpenBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if b.stallOpen {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	body, err := b.Backend.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	return &slowReader{ctx: ctx, body: body, chunk: b.chunk, delay: b.delay, stallAfter: b.stallAfter}, nil
}

type slowReader struct {
	ctx        context.Context
	body       io.ReadCloser
	chunk      int
	delay      time.Duration
	stallAfter int
	served     int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.stallAfter > 0 && r.served >= r.stallAfter {
		<-r.ctx.Done()
		return 0, r.ctx.Err()
	}
	select {
	case <-time.After(r.delay):
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
	if len(p) > r.chunk {
		p = p[:r.chunk]
	}
	n, err := r.body.Read(p)
	r.served += n
	return n, err
}

func (r *slowReader) Close() error {
	return r.body.Close()
}

func newSlowDownloadEnv(t *testing.T, options server.Options, backend *slowOpenBackend) *httpEnv {
	t.Helper()
	local, err := objectstorage.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBackend: %v", err)
	}
	backend.Backend = local
	storageSvc, err := objectstorage.NewService("local", "local", 1024*1024, backend)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	options.ObjectStorage = storageSvc
	return newStorageHTTPEnvWithOptions(t, options)
}

func (e *httpEnv) getObjectContent(t *testing.T, object model.StorageObject) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(e.ctx, http.MethodGet, e.ts.URL+apiPath(e.storageObjectPath(object)+"/content"), nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.authToken)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	return res
}

func (e *httpEnv) mustUploadObject(t *testing.T, filename string, content []byte) model.StorageObject {
	t.Helper()
	code, body := e.doMultipartObject(t, e.authToken, filename, content)
	if code != http.StatusCreated {
		t.Fatalf("upload code = %d body = %s", code, body)
	}
	return decode[model.StorageObject](t, body)
}

// A download outlives the ordinary request deadline: it is cut off only when
// it stops making progress.
func TestHTTPStorageObjectDownloadOutlivesRequestDeadline(t *testing.T) {
	t.Parallel()
	e := newSlowDownloadEnv(t, server.Options{RequestTimeout: 100 * time.Millisecond}, &slowOpenBackend{chunk: 1024, delay: 40 * time.Millisecond})
	content := bytes.Repeat([]byte("0123456789abcdef"), 512)
	object := e.mustUploadObject(t, "slow.bin", content)

	res := e.getObjectContent(t, object)
	defer res.Body.Close()
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("download read after %d of %d bytes: %v", len(got), len(content), err)
	}
	if res.StatusCode != http.StatusOK || !bytes.Equal(got, content) {
		t.Fatalf("download code = %d, %d bytes, want 200 and %d bytes", res.StatusCode, len(got), len(content))
	}
}

func TestHTTPStorageObjectDownloadStopsWhenBackendReadStalls(t *testing.T) {
	t.Parallel()
	e := newSlowDownloadEnv(t, server.Options{DownloadIdleTimeout: 100 * time.Millisecond}, &slowOpenBackend{chunk: 1024, stallAfter: 1024})
	content := bytes.Repeat([]byte("0123456789abcdef"), 512)
	object := e.mustUploadObject(t, "stall.bin", content)

	started := time.Now()
	res := e.getObjectContent(t, object)
	defer res.Body.Close()
	got, err := io.ReadAll(res.Body)
	if err == nil {
		t.Fatalf("stalled download read %d bytes without error", len(got))
	}
	if res.StatusCode != http.StatusOK || len(got) != 1024 {
		t.Fatalf("stalled download code = %d, %d bytes, want 200 and 1024 bytes", res.StatusCode, len(got))
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("stalled download ended after %v", elapsed)
	}
}

func TestHTTPStorageObjectDownloadStopsWhenBackendOpenStalls(t *testing.T) {
	t.Parallel()
	e := newSlowDownloadEnv(t, server.Options{DownloadIdleTimeout: 100 * time.Millisecond}, &slowOpenBackend{stallOpen: true})
	object := e.mustUploadObject(t, "stall.bin", []byte("stalled open"))

	started := time.Now()
	res := e.getObjectContent(t, object)
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("stalled open code = %d, want 500", res.StatusCode)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("stalled open ended after %v", elapsed)
	}
}

// endlessBackend serves size zero bytes for any key and reports when the
// server closes the body.
type endlessBackend struct {
	objectstorage.Backend
	size   int64
	closed chan struct{}
}

func (b *endlessBackend) Open(context.Context, string) (io.ReadCloser, error) {
	return &closeSignalReader{Reader: io.LimitReader(zeroReader{}, b.size), closed: b.closed}, nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type closeSignalReader struct {
	io.Reader
	closed chan struct{}
}

func (r *closeSignalReader) Close() error {
	close(r.closed)
	return nil
}

// A client that stops reading must not hold a download open: each write has
// to finish within the idle timeout.
func TestHTTPStorageObjectDownloadStopsWhenClientStopsReading(t *testing.T) {
	t.Parallel()
	const size = 64 << 20
	local, err := objectstorage.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBackend: %v", err)
	}
	backend := &endlessBackend{Backend: local, size: size, closed: make(chan struct{})}
	storageSvc, err := objectstorage.NewService("local", "local", 1024*1024, backend)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	e := newStorageHTTPEnvWithOptions(t, server.Options{ObjectStorage: storageSvc, DownloadIdleTimeout: 100 * time.Millisecond})
	objectID := uuid.New()
	object, err := e.store.CreateStorageObject(e.ctx, store.CreateStorageObjectParams{
		ID:          objectID,
		ProjectID:   e.projectID,
		Backend:     "local",
		Bucket:      "local",
		ObjectKey:   storageSvc.ObjectKey(e.projectID, objectID),
		Filename:    "large.bin",
		ContentType: "application/octet-stream",
		ByteSize:    size,
		SHA256:      strings.Repeat("0", 64),
		CreatedByID: e.adminID,
	})
	if err != nil {
		t.Fatalf("CreateStorageObject: %v", err)
	}

	res := e.getObjectContent(t, object)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("download code = %d", res.StatusCode)
	}
	select {
	case <-backend.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("download still open 10s after the client stopped reading")
	}
}
