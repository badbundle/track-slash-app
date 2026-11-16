package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestFormWaitConfiguration(t *testing.T) {
	t.Parallel()
	defaults := NewWithOptions(nil, nil, Options{})
	if defaults.formWaitMax != 50*time.Second || defaults.formWaitPoll != 2*time.Second {
		t.Fatalf("default wait = %v every %v", defaults.formWaitMax, defaults.formWaitPoll)
	}
	if defaults.formWaits.perForm != 4 || defaults.formWaits.total != 256 {
		t.Fatalf("default caps = %d per form, %d in total", defaults.formWaits.perForm, defaults.formWaits.total)
	}

	configured := NewWithOptions(nil, nil, Options{FormWait: FormWaitOptions{
		Max: 3 * time.Second, Poll: time.Millisecond, PerForm: 2, Total: 5,
	}})
	if configured.formWaitMax != 3*time.Second || configured.formWaitPoll != time.Millisecond {
		t.Fatalf("configured wait = %v every %v", configured.formWaitMax, configured.formWaitPoll)
	}
	if configured.formWaits.perForm != 2 || configured.formWaits.total != 5 {
		t.Fatalf("configured caps = %d per form, %d in total", configured.formWaits.perForm, configured.formWaits.total)
	}
}

func TestFormWaitDuration(t *testing.T) {
	t.Parallel()
	srv := NewWithOptions(nil, nil, Options{})
	tests := []struct {
		seconds int
		want    time.Duration
	}{
		{0, 0},
		{1, time.Second},
		{50, 50 * time.Second},
		{51, 50 * time.Second},
		{1 << 40, 50 * time.Second}, // far past what a Duration holds in seconds
	}
	for _, tt := range tests {
		got, err := srv.formWaitDuration(tt.seconds)
		if err != nil || got != tt.want {
			t.Fatalf("formWaitDuration(%d) = %v, %v; want %v", tt.seconds, got, err, tt.want)
		}
	}
	if _, err := srv.formWaitDuration(-1); !errors.Is(err, errFormWaitSeconds) {
		t.Fatalf("formWaitDuration(-1) err = %v", err)
	}

	// A server that waits less than a second still waits when asked to, and
	// still doesn't when not.
	brief := NewWithOptions(nil, nil, Options{FormWait: FormWaitOptions{Max: 300 * time.Millisecond}})
	if got, _ := brief.formWaitDuration(1); got != 300*time.Millisecond {
		t.Fatalf("brief wait = %v, want 300ms", got)
	}
	if got, _ := brief.formWaitDuration(0); got != 0 {
		t.Fatalf("brief no-wait = %v, want 0", got)
	}
}

func TestFormWaitLimiter(t *testing.T) {
	t.Parallel()
	limiter := newFormWaitLimiter(2, 3)
	a, b := uuid.New(), uuid.New()

	releaseA1, ok := limiter.acquire(a)
	if !ok {
		t.Fatal("first wait on a form refused")
	}
	releaseA2, ok := limiter.acquire(a)
	if !ok {
		t.Fatal("second wait on a form refused")
	}
	if _, ok := limiter.acquire(a); ok {
		t.Fatal("third wait on a form allowed past its cap of 2")
	}
	releaseB, ok := limiter.acquire(b)
	if !ok {
		t.Fatal("another form's wait refused while under the total")
	}
	// Three wait in total now, which is the cap, whatever the form.
	if _, ok := limiter.acquire(uuid.New()); ok {
		t.Fatal("wait allowed past the total cap of 3")
	}

	releaseA1()
	if _, ok := limiter.acquire(b); !ok {
		t.Fatal("wait refused after another gave its place back")
	}
	releaseA2()
	releaseB()
	if got := limiter.waiting[a]; got != 0 {
		t.Fatalf("form a still counts %d waits", got)
	}
	if _, kept := limiter.waiting[a]; kept {
		t.Fatal("a form nobody waits on is still in the table")
	}
	if limiter.waitingN != 1 {
		t.Fatalf("waiting in total = %d, want 1", limiter.waitingN)
	}
}

func TestFormWaitKeysAreSecretAndHashed(t *testing.T) {
	t.Parallel()
	key, hash := newFormWaitKey()
	other, otherHash := newFormWaitKey()
	if len(key) < 26 || key == other {
		t.Fatalf("keys = %q, %q: want two different keys of at least 128 bits", key, other)
	}
	if len(hash) != 32 || string(hash) == string(otherHash) || string(hash) != string(formWaitKeyHash(key)) {
		t.Fatalf("hash of %q is not its SHA-256", key)
	}

	local := NewWithOptions(nil, nil, Options{})
	if got := local.formWaitURL("abc"); got != "/api/v1/form-waits/abc" {
		t.Fatalf("wait URL without an origin = %q", got)
	}
	hosted := NewWithOptions(nil, nil, Options{PublicOrigin: "https://track.example/"})
	if got := hosted.formWaitURL("abc"); got != "https://track.example/api/v1/form-waits/abc" {
		t.Fatalf("wait URL with an origin = %q", got)
	}
}

func TestWaitWhileFormOpen(t *testing.T) {
	t.Parallel()
	srv := NewWithOptions(nil, nil, Options{FormWait: FormWaitOptions{Poll: time.Millisecond, PerForm: 1}})
	formID := uuid.New()
	open := func(context.Context) (model.FormStatus, error) { return model.FormStatusOpen, nil }

	// It returns the status as soon as the form is no longer open.
	reads := 0
	status, err := srv.waitWhileFormOpen(context.Background(), formID, time.Minute, func(context.Context) (model.FormStatus, error) {
		reads++
		if reads < 3 {
			return model.FormStatusOpen, nil
		}
		return model.FormStatusSubmitted, nil
	})
	if err != nil || status != model.FormStatusSubmitted || reads != 3 {
		t.Fatalf("status = %q, err = %v after %d reads", status, err, reads)
	}

	// It gives up, still open, when the wait runs out.
	status, err = srv.waitWhileFormOpen(context.Background(), formID, 20*time.Millisecond, open)
	if err != nil || status != model.FormStatusOpen {
		t.Fatalf("timed out wait = %q, %v", status, err)
	}

	// A read that fails ends the wait with its error.
	failed := errors.New("read failed")
	if _, err = srv.waitWhileFormOpen(context.Background(), formID, time.Minute, func(context.Context) (model.FormStatus, error) {
		return "", failed
	}); !errors.Is(err, failed) {
		t.Fatalf("failed read err = %v", err)
	}

	// A caller who goes away ends it too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := NewWithOptions(nil, nil, Options{FormWait: FormWaitOptions{Poll: time.Hour}})
	if _, err = slow.waitWhileFormOpen(ctx, formID, time.Hour, open); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait err = %v", err)
	}

	// Each of those gave its place back; a wait past the cap is refused
	// without reading the form.
	release, ok := srv.formWaits.acquire(formID)
	if !ok {
		t.Fatal("a finished wait kept its place")
	}
	defer release()
	if _, err = srv.waitWhileFormOpen(context.Background(), formID, time.Minute, func(context.Context) (model.FormStatus, error) {
		t.Error("read the form for a wait that was refused")
		return model.FormStatusOpen, nil
	}); !errors.Is(err, errFormWaitsFull) {
		t.Fatalf("wait past the cap err = %v", err)
	}
}

func TestFormWaitRequestsGetLongerDeadlines(t *testing.T) {
	t.Parallel()
	srv := NewWithOptions(nil, nil, Options{RequestTimeout: 15 * time.Second})
	wait := httptest.NewRequest(http.MethodGet, "/api/v1/form-waits/abc?wait=50", nil)
	if !isFormWaitRequest(wait) {
		t.Fatal("wait URL not recognised")
	}
	if got := srv.requestTimeoutFor(wait); got != 65*time.Second {
		t.Fatalf("wait URL timeout = %v, want 65s", got)
	}
	// The form's own routes keep the ordinary deadline, whatever their query.
	form := httptest.NewRequest(http.MethodGet, "/api/v1/acme/projects/APP/forms/form-1?wait=50", nil)
	if isFormWaitRequest(form) {
		t.Fatal("an ordinary form route was taken for a wait URL")
	}
	if got := srv.requestTimeoutFor(form); got != 15*time.Second {
		t.Fatalf("form route timeout = %v, want 15s", got)
	}
}

func TestRequestLogLeavesOutWaitKeys(t *testing.T) {
	t.Parallel()
	for target, want := range map[string]string{
		"/api/v1/form-waits/SECRETKEY":         "/api/v1/form-waits/[wait-key]",
		"/api/v1/form-waits/SECRETKEY?wait=50": "/api/v1/form-waits/[wait-key]?wait=50",
		"/api/v1/form-uploads/SECRETKEY":       "/api/v1/form-uploads/[upload-key]",
	} {
		got, secret := redactedSecretRouteURI(httptest.NewRequest(http.MethodGet, target, nil))
		if !secret || got != want {
			t.Fatalf("redacted %s = %q, %v; want %q", target, got, secret, want)
		}
	}
	if got, secret := redactedSecretRouteURI(httptest.NewRequest(http.MethodGet, "/api/v1/healthz", nil)); secret || got != "" {
		t.Fatalf("an ordinary route was redacted: %q", got)
	}

	// The handler still gets the real request, with the log entry in its
	// context, whether or not the log hides part of it.
	for _, target := range []string{"/api/v1/form-waits/SECRETKEY?wait=50", "/api/v1/healthz?x=1"} {
		var seen *http.Request
		handler := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodGet, target, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("%s: code = %d", target, recorder.Code)
		}
		if seen == nil || seen.URL.RequestURI() != target || seen.RequestURI != target {
			t.Fatalf("%s: handler saw %+v", target, seen)
		}
		if middleware.GetLogEntry(seen) == nil {
			t.Fatalf("%s: handler's request has no log entry", target)
		}
	}
}
