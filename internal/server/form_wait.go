package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

// An agent that asks a person something with a form wants to know when they
// have answered, without the person going back to say so. Two things let it
// wait:
//
//   - track_get_form takes wait_seconds and holds the call while the form is
//     still open.
//   - Creating a form returns a wait URL. It holds a secret wait key, so a
//     background command can call it with no account token, and it answers
//     with the form's status and nothing else. The answers are still read
//     through the usual routes, which check membership.
//
// Neither holds a database connection while it waits: each reads the form
// again every formWaitPoll. See SECURITY_MODEL.md.

const (
	// defaultFormWaitMax is the longest one request waits. It stays under the
	// 60 seconds at which many clients and proxies give up on a request, so
	// the caller sees an answer and asks again rather than an error.
	defaultFormWaitMax  = 50 * time.Second
	defaultFormWaitPoll = 2 * time.Second
	// A form has one agent waiting on it, and perhaps a retry that overlaps.
	// The caps keep someone who holds a wait key, or a member's token, from
	// parking requests on the server without limit.
	defaultFormWaitsPerForm = 4
	defaultFormWaitsTotal   = 256

	formWaitRoutePrefix = "/api/v1/form-waits/"
)

var (
	errFormWaitsFull   = errors.New("too many requests are already waiting on this form; try again shortly")
	errFormWaitSeconds = errors.New("wait must be a whole number of seconds, 0 or more")
)

// formWaitLimiter caps the requests waiting at once, for one form and in
// total.
type formWaitLimiter struct {
	mu       sync.Mutex
	perForm  int
	total    int
	waiting  map[uuid.UUID]int
	waitingN int
}

func newFormWaitLimiter(perForm, total int) *formWaitLimiter {
	if perForm <= 0 {
		perForm = defaultFormWaitsPerForm
	}
	if total <= 0 {
		total = defaultFormWaitsTotal
	}
	return &formWaitLimiter{perForm: perForm, total: total, waiting: make(map[uuid.UUID]int)}
}

// acquire takes a place for a request waiting on a form. The caller calls
// release once, when the request stops waiting.
func (l *formWaitLimiter) acquire(formID uuid.UUID) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.waitingN >= l.total || l.waiting[formID] >= l.perForm {
		return nil, false
	}
	l.waiting[formID]++
	l.waitingN++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.waitingN--
		if l.waiting[formID]--; l.waiting[formID] == 0 {
			delete(l.waiting, formID)
		}
	}, true
}

// newFormWaitKey makes a wait key and the hash the store keeps for it.
func newFormWaitKey() (key string, hash []byte) {
	key = rand.Text()
	return key, formWaitKeyHash(key)
}

func formWaitKeyHash(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// formWaitURL is the link a wait key is handed out as: absolute when the
// server knows its public origin, and otherwise the path alone.
func (s *Server) formWaitURL(key string) string {
	return strings.TrimSuffix(s.publicOrigin, "/") + formWaitRoutePrefix + key
}

// isFormWaitRequest reports whether a request is for a wait URL, which may be
// held open for longer than an ordinary request.
func isFormWaitRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, formWaitRoutePrefix)
}

// formWaitDuration turns a number of seconds to wait into a duration. Zero
// is no wait, and a wait longer than the server allows is cut to what it
// allows, because the caller asks again either way.
func (s *Server) formWaitDuration(seconds int) (time.Duration, error) {
	if seconds < 0 {
		return 0, errFormWaitSeconds
	}
	// Compared as a count of seconds, so a huge number can't overflow.
	if time.Duration(seconds) > s.formWaitMax/time.Second {
		return s.formWaitMax, nil
	}
	return time.Duration(seconds) * time.Second, nil
}

// waitWhileFormOpen reads a form's status every formWaitPoll until it is no
// longer open, the wait runs out or the caller goes away. Between reads it
// holds nothing but a timer. It returns errFormWaitsFull without waiting when
// too many requests already wait on the form.
func (s *Server) waitWhileFormOpen(ctx context.Context, formID uuid.UUID, wait time.Duration, read func(context.Context) (model.FormStatus, error)) (model.FormStatus, error) {
	release, ok := s.formWaits.acquire(formID)
	if !ok {
		return "", errFormWaitsFull
	}
	defer release()

	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	poll := time.NewTicker(s.formWaitPoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return model.FormStatusOpen, nil
		case <-poll.C:
			status, err := read(ctx)
			if err != nil || status != model.FormStatusOpen {
				return status, err
			}
		}
	}
}

type formWaitResponse struct {
	Status model.FormStatus `json:"status"`
}

// getFormWait answers a wait URL with the form's status. It takes no account
// credentials: the wait key in the path is the credential, and all it buys is
// the status. With ?wait=N it holds the request for up to N seconds while the
// form is open, and answers as soon as the form is submitted or cancelled. A
// key that doesn't exist, and one whose creator can no longer read the form,
// are both not found.
func (s *Server) getFormWait(w http.ResponseWriter, r *http.Request) {
	seconds := 0
	if raw := r.URL.Query().Get("wait"); raw != "" {
		var err error
		if seconds, err = strconv.Atoi(raw); err != nil {
			writeError(w, http.StatusBadRequest, errFormWaitSeconds.Error())
			return
		}
	}
	wait, err := s.formWaitDuration(seconds)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	hash := formWaitKeyHash(chi.URLParam(r, "waitKey"))
	current, err := s.store.GetFormWait(r.Context(), hash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	status := current.Status
	if wait > 0 && status == model.FormStatusOpen {
		status, err = s.waitWhileFormOpen(r.Context(), current.FormID, wait, func(ctx context.Context) (model.FormStatus, error) {
			latest, err := s.store.GetFormWait(ctx, hash)
			return latest.Status, err
		})
		switch {
		case errors.Is(err, errFormWaitsFull):
			w.Header().Set("Retry-After", strconv.Itoa(int(s.formWaitPoll/time.Second)+1))
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		case err != nil && r.Context().Err() != nil:
			// The caller went away, or the request ran out of time. Nothing
			// was learned about the form, so say so rather than guess.
			writeError(w, http.StatusServiceUnavailable, "the wait was interrupted; try again")
			return
		case err != nil:
			writeStoreError(w, err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, formWaitResponse{Status: status})
}

// logRequests is the request log, with the wait key of a wait URL left out: a
// secret in a URL should not be written to the log.
func logRequests(next http.Handler) http.Handler {
	logged := middleware.Logger(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isFormWaitRequest(r) {
			logged.ServeHTTP(w, r)
			return
		}
		shown := r.Clone(r.Context())
		shown.RequestURI = redactedFormWaitURI(r)
		middleware.Logger(http.HandlerFunc(func(w http.ResponseWriter, entry *http.Request) {
			// The handler gets the real request, with the log entry's context.
			next.ServeHTTP(w, r.WithContext(entry.Context()))
		})).ServeHTTP(w, shown)
	})
}

// redactedFormWaitURI is a wait URL's request line without its wait key.
func redactedFormWaitURI(r *http.Request) string {
	uri := formWaitRoutePrefix + "[wait-key]"
	if r.URL.RawQuery != "" {
		uri += "?" + r.URL.RawQuery
	}
	return uri
}
