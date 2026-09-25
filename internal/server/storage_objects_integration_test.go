package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

func newStorageHTTPEnv(t *testing.T, maxUploadBytes int64) (*httpEnv, string) {
	t.Helper()
	storageSvc, root := newLocalStorageService(t, maxUploadBytes)
	return newStorageHTTPEnvWithService(t, storageSvc), root
}

func newLocalStorageService(t *testing.T, maxUploadBytes int64) (*objectstorage.Service, string) {
	t.Helper()
	root := t.TempDir()
	storageSvc, err := objectstorage.NewLocalService(root, "local", maxUploadBytes)
	if err != nil {
		t.Fatalf("NewLocalService: %v", err)
	}
	return storageSvc, root
}

func newStorageHTTPEnvWithService(t *testing.T, storageSvc *objectstorage.Service) *httpEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	db := testutil.NewMigratedDatabase(t)
	st := store.New(db.Pool)
	srv := server.NewWithOptions(st, nil, server.Options{ObjectStorage: storageSvc})
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	key := uniqueProjectKey(t)
	admin, err := st.CreateOrUpdateAdminUser(ctx, "admin-"+key+"@example.com", "Admin")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	proj, err := st.CreateProjectForUser(ctx, admin.ID, key, "storage-test", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	enableFixtureSprintMode(t, ctx, db.Pool, proj.ID)
	token, err := st.CreateAuthToken(ctx, store.CreateAuthTokenParams{
		UserID: admin.ID,
		Kind:   model.AuthTokenKindAPI,
		Name:   "test",
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}

	return &httpEnv{
		ctx: ctx, ts: ts, pool: db.Pool, store: st, projectID: proj.ID, projKey: key, ownerUsername: admin.Username,
		adminID: admin.ID, authToken: token.RawToken,
	}
}

func (e *httpEnv) projectObjectsPath() string {
	return e.projectPath() + "/objects"
}

func (e *httpEnv) storageObjectPath(object model.StorageObject) string {
	return e.projectObjectsPath() + "/" + object.Ref
}

func (e *httpEnv) doMultipartObject(t *testing.T, token, filename string, content []byte) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+apiPath(e.projectObjectsPath()), &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do multipart: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, body
}

func (e *httpEnv) doMultipartObjectWithoutFile(t *testing.T, token string) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("title", "no file"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+apiPath(e.projectObjectsPath()), &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do multipart: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, body
}

func (e *httpEnv) doRaw(t *testing.T, token, method, path string, body io.Reader, contentType string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(e.ctx, method, e.ts.URL+apiPath(path), body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do raw: %v", err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res, out
}

func TestHTTPStorageObjectCRUD(t *testing.T) {
	t.Parallel()
	e, root := newStorageHTTPEnv(t, 1024)
	content := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("image-bytes")...)
	sum := sha256.Sum256(content)
	wantSHA := hex.EncodeToString(sum[:])

	code, body := e.doMultipartObject(t, e.authToken, "photo.png", content)
	if code != http.StatusCreated {
		t.Fatalf("upload code = %d body = %s", code, body)
	}
	object := decode[model.StorageObject](t, body)
	if object.Ref != "object-1" || object.ProjectID != e.projectID || object.Backend != "local" || object.Bucket != "local" || object.Filename != "photo.png" || object.ContentType != "image/png" || object.ByteSize != int64(len(content)) || object.SHA256 != wantSHA || object.CreatedByID != e.adminID {
		t.Fatalf("object = %+v", object)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(object.ObjectKey))); err != nil {
		t.Fatalf("stored file stat: %v", err)
	}

	code, body = e.doMultipartObject(t, e.authToken, "notes.txt", []byte("second"))
	if code != http.StatusCreated {
		t.Fatalf("second upload code = %d body = %s", code, body)
	}
	second := decode[model.StorageObject](t, body)
	if second.Ref != "object-2" {
		t.Fatalf("second.Ref = %q, want object-2", second.Ref)
	}

	code, body = e.do(t, http.MethodGet, e.projectObjectsPath()+"?limit=1", nil)
	if code != http.StatusOK {
		t.Fatalf("list code = %d body = %s", code, body)
	}
	page := decodePage[model.StorageObject](t, body)
	if len(page.Items) != 1 || page.Items[0].ID != object.ID || page.NextCursor == nil {
		t.Fatalf("page = %+v", page)
	}
	code, body = e.do(t, http.MethodGet, e.projectObjectsPath()+"?cursor="+*page.NextCursor+"&limit=1", nil)
	if code != http.StatusOK {
		t.Fatalf("list page2 code = %d body = %s", code, body)
	}
	page = decodePage[model.StorageObject](t, body)
	if len(page.Items) != 1 || page.Items[0].ID != second.ID || page.NextCursor != nil {
		t.Fatalf("page2 = %+v", page)
	}

	code, body = e.do(t, http.MethodGet, e.storageObjectPath(object), nil)
	if code != http.StatusOK {
		t.Fatalf("get code = %d body = %s", code, body)
	}
	got := decode[model.StorageObject](t, body)
	if got.ID != object.ID || got.ObjectKey != object.ObjectKey {
		t.Fatalf("got = %+v, want %+v", got, object)
	}

	res, body := e.doRaw(t, e.authToken, http.MethodGet, e.storageObjectPath(object)+"/content", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("content code = %d body = %s", res.StatusCode, body)
	}
	if !bytes.Equal(body, content) {
		t.Fatalf("content body = %q, want upload bytes", body)
	}
	if got := res.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := res.Header.Get("Content-Length"); got != strconv.Itoa(len(content)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(content))
	}
	if got := res.Header.Get("ETag"); got != `"`+wantSHA+`"` {
		t.Fatalf("ETag = %q, want sha", got)
	}
	if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, "photo.png") {
		t.Fatalf("Content-Disposition = %q, want filename", got)
	}

	code, body = e.do(t, http.MethodDelete, e.storageObjectPath(object), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete code = %d body = %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(object.ObjectKey))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted file stat err = %v, want not exist", err)
	}
	code, body = e.do(t, http.MethodGet, e.storageObjectPath(object), nil)
	if code != http.StatusNotFound {
		t.Fatalf("get deleted code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.storageObjectPath(object)+"/content", nil)
	if code != http.StatusNotFound {
		t.Fatalf("content deleted code = %d body = %s", code, body)
	}
}

func TestHTTPStorageObjectAccessAndValidation(t *testing.T) {
	t.Parallel()
	e, _ := newStorageHTTPEnv(t, 8)
	content := []byte("12345678")
	code, body := e.doMultipartObject(t, e.authToken, "photo.png", content)
	if code != http.StatusCreated {
		t.Fatalf("admin upload code = %d body = %s", code, body)
	}
	object := decode[model.StorageObject](t, body)

	code, body = e.doUnauth(t, http.MethodGet, e.projectObjectsPath(), nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("unauth list code = %d body = %s", code, body)
	}
	code, body = e.doMultipartObject(t, "", "photo.png", content)
	if code != http.StatusUnauthorized {
		t.Fatalf("unauth upload code = %d body = %s", code, body)
	}
	_, outsiderToken := e.mustUserToken(t, "storage-outsider")
	code, body = e.doWithToken(t, outsiderToken, http.MethodGet, e.projectObjectsPath(), nil)
	if code != http.StatusForbidden {
		t.Fatalf("outsider list code = %d body = %s", code, body)
	}
	code, body = e.doWithToken(t, outsiderToken, http.MethodGet, e.storageObjectPath(object), nil)
	if code != http.StatusForbidden {
		t.Fatalf("outsider get code = %d body = %s", code, body)
	}
	code, body = e.doWithToken(t, outsiderToken, http.MethodGet, e.storageObjectPath(object)+"/content", nil)
	if code != http.StatusForbidden {
		t.Fatalf("outsider content code = %d body = %s", code, body)
	}
	code, body = e.doWithToken(t, outsiderToken, http.MethodDelete, e.storageObjectPath(object), nil)
	if code != http.StatusForbidden {
		t.Fatalf("outsider delete code = %d body = %s", code, body)
	}
	code, body = e.doMultipartObject(t, outsiderToken, "photo.png", content)
	if code != http.StatusForbidden {
		t.Fatalf("outsider upload code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPost, e.projectObjectsPath(), map[string]any{"file": "nope"})
	if code != http.StatusBadRequest {
		t.Fatalf("json upload code = %d body = %s", code, body)
	}
	code, body = e.doMultipartObjectWithoutFile(t, e.authToken)
	if code != http.StatusBadRequest {
		t.Fatalf("missing file code = %d body = %s", code, body)
	}
	code, body = e.doMultipartObject(t, e.authToken, "big.bin", []byte("123456789"))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload code = %d body = %s", code, body)
	}
	code, body = e.doMultipartObject(t, e.authToken, "../", content)
	if code != http.StatusBadRequest {
		t.Fatalf("bad filename code = %d body = %s", code, body)
	}
	code, body = e.doMultipartObject(t, e.authToken, strings.Repeat("x", 256), content)
	if code != http.StatusBadRequest {
		t.Fatalf("long filename code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.projectObjectsPath()+"/not-an-object", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("bad ref code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.projectObjectsPath()+"?cursor=not-base64", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("bad cursor code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.projectObjectsPath()+"?limit=0", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("bad limit code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.projectObjectsPath()+"/object-9999", nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing ref code = %d body = %s", code, body)
	}

	missingBackend, err := e.store.CreateStorageObject(e.ctx, store.CreateStorageObjectParams{
		ID:          uuid.New(),
		ProjectID:   e.projectID,
		Backend:     "local",
		Bucket:      "local",
		ObjectKey:   "projects/missing/objects/missing",
		Filename:    "missing.bin",
		ContentType: "application/octet-stream",
		ByteSize:    1,
		SHA256:      strings.Repeat("b", 64),
		CreatedByID: e.adminID,
	})
	if err != nil {
		t.Fatalf("CreateStorageObject missing backend: %v", err)
	}
	code, body = e.do(t, http.MethodGet, e.storageObjectPath(missingBackend)+"/content", nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing backend content code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodDelete, e.storageObjectPath(missingBackend), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete missing backend code = %d body = %s", code, body)
	}

	disabled := newHTTPEnv(t)
	disabledObject, err := disabled.store.CreateStorageObject(disabled.ctx, store.CreateStorageObjectParams{
		ID:          uuid.New(),
		ProjectID:   disabled.projectID,
		Backend:     "local",
		Bucket:      "local",
		ObjectKey:   "projects/disabled/objects/1",
		Filename:    "disabled.bin",
		ContentType: "application/octet-stream",
		ByteSize:    1,
		SHA256:      strings.Repeat("c", 64),
		CreatedByID: disabled.adminID,
	})
	if err != nil {
		t.Fatalf("CreateStorageObject disabled: %v", err)
	}
	code, body = disabled.doMultipartObject(t, disabled.authToken, "photo.png", content)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("disabled storage upload code = %d body = %s", code, body)
	}
	code, body = disabled.do(t, http.MethodGet, disabled.storageObjectPath(disabledObject)+"/content", nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("disabled storage content code = %d body = %s", code, body)
	}
	code, body = disabled.do(t, http.MethodDelete, disabled.storageObjectPath(disabledObject), nil)
	if code != http.StatusNoContent {
		t.Fatalf("disabled storage delete code = %d body = %s", code, body)
	}
	if job, err := disabled.store.GetStorageObjectDeletion(disabled.ctx, disabledObject.ID); err != nil || job.Status != store.StorageObjectDeletionPending {
		t.Fatalf("disabled storage deletion job = %+v, %v", job, err)
	}
}

func TestHTTPStorageObjectDeleteBackendFailureIsEventuallyReconciled(t *testing.T) {
	t.Parallel()
	backend := &flakyDeleteBackend{objects: make(map[string][]byte), deleteFailures: 1}
	storageSvc, err := objectstorage.NewService("local", "local", 1024, backend)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	e := newStorageHTTPEnvWithService(t, storageSvc)
	code, body := e.doMultipartObject(t, e.authToken, "delete-error.bin", []byte("eventually deleted"))
	if code != http.StatusCreated {
		t.Fatalf("upload code = %d body = %s", code, body)
	}
	object := decode[model.StorageObject](t, body)

	code, body = e.do(t, http.MethodDelete, e.storageObjectPath(object), nil)
	if code != http.StatusNoContent {
		t.Fatalf("logical delete code = %d body = %s", code, body)
	}
	if _, err := e.store.GetStorageObjectByProjectNumber(e.ctx, e.projectID, object.Number); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("logically deleted metadata err = %v, want ErrNotFound", err)
	}
	if _, err := backend.Open(e.ctx, object.ObjectKey); err != nil {
		t.Fatalf("backend bytes after transient failure: %v", err)
	}
	job, err := e.store.GetStorageObjectDeletion(e.ctx, object.ID)
	if err != nil || job.Status != store.StorageObjectDeletionPending || job.AttemptCount != 0 {
		t.Fatalf("pending deletion job = %+v, %v", job, err)
	}

	worker := objectstorage.NewDeletionWorker(e.store, storageSvc, objectstorage.DeletionWorkerOptions{})
	result, err := worker.RunOnce(e.ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Claimed != 1 || result.Deleted != 1 || result.Retried != 0 || result.Failed != 0 {
		t.Fatalf("worker result = %+v", result)
	}
	if _, err := backend.Open(e.ctx, object.ObjectKey); !errors.Is(err, objectstorage.ErrNotFound) {
		t.Fatalf("backend bytes after reconciliation err = %v, want ErrNotFound", err)
	}
	if _, err := e.store.GetStorageObjectDeletion(e.ctx, object.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("completed deletion job err = %v, want ErrNotFound", err)
	}
}

func TestStorageDeletionWorkerRetriesAndSurfacesTerminalFailure(t *testing.T) {
	t.Parallel()
	backend := &flakyDeleteBackend{objects: make(map[string][]byte), deleteFailures: 10}
	storageSvc, err := objectstorage.NewService("local", "local", 1024, backend)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	e := newStorageHTTPEnvWithService(t, storageSvc)
	code, body := e.doMultipartObject(t, e.authToken, "terminal.bin", []byte("retained after terminal failure"))
	if code != http.StatusCreated {
		t.Fatalf("upload code = %d body = %s", code, body)
	}
	object := decode[model.StorageObject](t, body)
	code, body = e.do(t, http.MethodDelete, e.storageObjectPath(object), nil)
	if code != http.StatusNoContent {
		t.Fatalf("logical delete code = %d body = %s", code, body)
	}

	var logs bytes.Buffer
	worker := objectstorage.NewDeletionWorker(e.store, storageSvc, objectstorage.DeletionWorkerOptions{
		MaxAttempts: 2,
		Logger:      log.New(&logs, "", 0),
	})
	first, err := worker.RunOnce(e.ctx)
	if err != nil || first.Retried != 1 || first.Failed != 0 {
		t.Fatalf("first RunOnce = %+v, %v", first, err)
	}
	retried, err := e.store.GetStorageObjectDeletion(e.ctx, object.ID)
	if err != nil || retried.Status != store.StorageObjectDeletionPending || retried.AttemptCount != 1 ||
		retried.LastError != "delete failed" || retried.NextAttemptAt == nil {
		t.Fatalf("retried job = %+v, %v", retried, err)
	}
	if _, err := e.pool.Exec(e.ctx, "UPDATE storage_object_deletions SET next_attempt_at = now() WHERE storage_object_id = $1", object.ID); err != nil {
		t.Fatalf("make retry due: %v", err)
	}
	second, err := worker.RunOnce(e.ctx)
	if err != nil || second.Failed != 1 || second.Retried != 0 {
		t.Fatalf("second RunOnce = %+v, %v", second, err)
	}
	failed, err := e.store.GetStorageObjectDeletion(e.ctx, object.ID)
	if err != nil || failed.Status != store.StorageObjectDeletionFailed || failed.AttemptCount != 2 ||
		failed.FailedAt == nil || failed.LastError != "delete failed" {
		t.Fatalf("failed job = %+v, %v", failed, err)
	}
	if !strings.Contains(logs.String(), "storage deletion terminal failure") || !strings.Contains(logs.String(), object.ID.String()) {
		t.Fatalf("terminal log = %q", logs.String())
	}
	if _, err := backend.Open(e.ctx, object.ObjectKey); err != nil {
		t.Fatalf("terminal failure should retain backend bytes for operator retry: %v", err)
	}
}

func TestStorageDeletionWorkerCompletesMissingBytesAndRejectsWrongBackend(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 1024)
	e := newStorageHTTPEnvWithService(t, storageSvc)

	code, body := e.doMultipartObject(t, e.authToken, "already-gone.bin", []byte("delete immediately"))
	if code != http.StatusCreated {
		t.Fatalf("upload code = %d body = %s", code, body)
	}
	gone := decode[model.StorageObject](t, body)
	code, body = e.do(t, http.MethodDelete, e.storageObjectPath(gone), nil)
	if code != http.StatusNoContent {
		t.Fatalf("logical delete code = %d body = %s", code, body)
	}
	worker := objectstorage.NewDeletionWorker(e.store, storageSvc, objectstorage.DeletionWorkerOptions{})
	result, err := worker.RunOnce(e.ctx)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("missing-byte RunOnce = %+v, %v", result, err)
	}
	if _, err := e.store.GetStorageObjectDeletion(e.ctx, gone.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing-byte completed job err = %v", err)
	}

	wrongBackend, err := e.store.CreateStorageObject(e.ctx, store.CreateStorageObjectParams{
		ID: uuid.New(), ProjectID: e.projectID, Backend: "retired", Bucket: "old-bucket",
		ObjectKey: "projects/retired/objects/1", Filename: "retired.bin", ContentType: "application/octet-stream",
		ByteSize: 1, SHA256: strings.Repeat("f", 64), CreatedByID: e.adminID,
	})
	if err != nil {
		t.Fatalf("CreateStorageObject wrong backend: %v", err)
	}
	if _, err := e.store.DeleteStorageObject(e.ctx, wrongBackend.ID); err != nil {
		t.Fatalf("DeleteStorageObject wrong backend: %v", err)
	}
	var logs bytes.Buffer
	worker = objectstorage.NewDeletionWorker(e.store, storageSvc, objectstorage.DeletionWorkerOptions{Logger: log.New(&logs, "", 0)})
	result, err = worker.RunOnce(e.ctx)
	if err != nil || result.Failed != 1 {
		t.Fatalf("wrong-backend RunOnce = %+v, %v", result, err)
	}
	failed, err := e.store.GetStorageObjectDeletion(e.ctx, wrongBackend.ID)
	if err != nil || failed.Status != store.StorageObjectDeletionFailed ||
		!strings.Contains(failed.LastError, "job requires retired/old-bucket") {
		t.Fatalf("wrong-backend job = %+v, %v", failed, err)
	}
	if !strings.Contains(logs.String(), "terminal failure") {
		t.Fatalf("wrong-backend log = %q", logs.String())
	}
}

func TestHTTPStorageObjectDeletesBackendOnMetadataFailure(t *testing.T) {
	t.Parallel()
	backend := &invalidHashBackend{}
	storageSvc, err := objectstorage.NewService("local", "local", 1024, backend)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	e := newStorageHTTPEnvWithService(t, storageSvc)

	code, body := e.doMultipartObject(t, e.authToken, "photo.png", []byte("image"))
	if code != http.StatusConflict {
		t.Fatalf("metadata failure upload code = %d body = %s", code, body)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.putKey == "" || backend.deletedKey != backend.putKey {
		t.Fatalf("backend cleanup put=%q deleted=%q", backend.putKey, backend.deletedKey)
	}
}

type invalidHashBackend struct {
	mu         sync.Mutex
	putKey     string
	deletedKey string
}

func (b *invalidHashBackend) Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (objectstorage.WrittenObject, error) {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return objectstorage.WrittenObject{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.putKey = key
	return objectstorage.WrittenObject{Size: 1, SHA256: "not-a-sha"}, nil
}

func (b *invalidHashBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return nil, objectstorage.ErrNotFound
}

func (b *invalidHashBackend) Delete(ctx context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletedKey = key
	return nil
}

type flakyDeleteBackend struct {
	mu             sync.Mutex
	objects        map[string][]byte
	deleteFailures int
}

func (b *flakyDeleteBackend) Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (objectstorage.WrittenObject, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return objectstorage.WrittenObject{}, err
	}
	if int64(len(data)) > maxBytes {
		return objectstorage.WrittenObject{}, objectstorage.ErrTooLarge
	}
	sum := sha256.Sum256(data)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = append([]byte(nil), data...)
	return objectstorage.WrittenObject{Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func (b *flakyDeleteBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[key]
	if !ok {
		return nil, objectstorage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), data...))), nil
}

func (b *flakyDeleteBackend) Delete(ctx context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.deleteFailures > 0 {
		b.deleteFailures--
		return errors.New("delete failed")
	}
	if _, ok := b.objects[key]; !ok {
		return objectstorage.ErrNotFound
	}
	delete(b.objects, key)
	return nil
}
