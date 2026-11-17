package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestFormMediaOptionsDefaults(t *testing.T) {
	t.Parallel()
	got := FormMediaOptions{}.withDefaults()
	if got.MaxFileBytes != 25<<20 || got.GrantTTL != 30*time.Minute || got.GrantMaxFiles != 20 {
		t.Fatalf("defaults = %+v, want 25 MiB, 30 minutes and 20 files", got)
	}
	set := FormMediaOptions{MaxFileBytes: 1, GrantTTL: time.Second, GrantMaxFiles: 2}.withDefaults()
	if set.MaxFileBytes != 1 || set.GrantTTL != time.Second || set.GrantMaxFiles != 2 {
		t.Fatalf("set options = %+v, want them kept", set)
	}
}

func TestFormMediaMaxFileBytesWithoutStorage(t *testing.T) {
	t.Parallel()
	srv := NewWithOptions(nil, nil, Options{FormMedia: FormMediaOptions{MaxFileBytes: 7}})
	if got := srv.formMediaMaxFileBytes(); got != 7 {
		t.Fatalf("max file bytes = %d, want the setting when there is no storage to cap it", got)
	}
}

func TestFormUploadKeys(t *testing.T) {
	t.Parallel()
	key, hash := newFormUploadKey()
	again, _ := newFormUploadKey()
	if len(key) < 26 || key == again || string(hash) != string(formUploadKeyHash(key)) || len(hash) != 32 {
		t.Fatalf("key = %q hash = %x", key, hash)
	}
	srv := NewWithOptions(nil, nil, Options{PublicOrigin: "https://track.example.com/"})
	if got := srv.formUploadURL("KEY"); got != "https://track.example.com/api/v1/form-uploads/KEY" {
		t.Fatalf("upload URL = %q", got)
	}
	if got := NewWithOptions(nil, nil, Options{}).formUploadURL("KEY"); got != "/api/v1/form-uploads/KEY" {
		t.Fatalf("upload URL without an origin = %q", got)
	}
}

func TestFormAttachmentKind(t *testing.T) {
	t.Parallel()
	for contentType, want := range map[string]string{
		"image/png": "image", "image/jpeg": "image", "IMAGE/WEBP": "image", " image/gif ": "image",
		// Only the safe inline types are images: SVG is shown as a file.
		"image/svg+xml": "file", "image/tiff": "file",
		"audio/mpeg": "audio", "audio/x-m4a": "audio", "Audio/Wav": "audio",
		"video/mp4": "video", "video/quicktime": "video", "video/webm": "video",
		// Audio and video the content route doesn't play are downloads.
		"audio/flac": "file", "video/x-matroska": "file",
		"text/plain": "file", "application/pdf": "file", "application/octet-stream": "file", "": "file",
	} {
		if got := formAttachmentKind(model.StorageObject{ContentType: contentType}); got != want {
			t.Fatalf("kind of %q = %q, want %q", contentType, got, want)
		}
	}
}

func TestRequestLogLeavesOutUploadKeys(t *testing.T) {
	t.Parallel()
	for target, want := range map[string]string{
		"/api/v1/form-uploads/SECRETKEY":      "/api/v1/form-uploads/[upload-key]",
		"/api/v1/form-uploads/SECRETKEY?x=1":  "/api/v1/form-uploads/[upload-key]?x=1",
		"/api/v1/form-waits/SECRETKEY?wait=5": "/api/v1/form-waits/[wait-key]?wait=5",
		"/api/v1/form-files/SECRETKEY":        "/api/v1/form-files/[file-key]",
		"/api/v1/form-files/SECRETKEY?x=1":    "/api/v1/form-files/[file-key]?x=1",
	} {
		got, secret := redactedSecretRouteURI(httptest.NewRequest(http.MethodPost, target, nil))
		if !secret || got != want {
			t.Fatalf("%s: redacted = %q, secret = %v, want %q", target, got, secret, want)
		}
	}
	if got, secret := redactedSecretRouteURI(httptest.NewRequest(http.MethodGet, "/api/v1/acme/projects/APP/forms/upload-urls", nil)); secret || got != "" {
		t.Fatalf("an ordinary route was taken for a secret one: %q", got)
	}

	// The handler still gets the real request, with the log entry in its context.
	for _, target := range []string{"/api/v1/form-uploads/SECRETKEY?x=1", "/api/v1/form-uploads/SECRETKEY"} {
		var seen *http.Request
		handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r
			w.WriteHeader(http.StatusNoContent)
		}))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, target, nil))
		if recorder.Code != http.StatusNoContent || seen == nil || seen.URL.RequestURI() != target || seen.RequestURI != target {
			t.Fatalf("%s: code = %d, handler saw %+v", target, recorder.Code, seen)
		}
		if middleware.GetLogEntry(seen) == nil {
			t.Fatalf("%s: handler's request has no log entry", target)
		}
	}
}

func TestUploadURLsGetTheUploadDeadline(t *testing.T) {
	t.Parallel()
	srv := NewWithOptions(nil, nil, Options{RequestTimeout: 15 * time.Second, UploadTimeout: 2 * time.Minute})
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/form-uploads/KEY", nil)
	upload.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if got := srv.requestTimeoutFor(upload); got != 2*time.Minute {
		t.Fatalf("upload URL timeout = %v, want the upload timeout", got)
	}
}
