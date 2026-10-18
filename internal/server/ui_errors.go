package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/bradleymackey/track-slash/internal/store"
)

type uiErrorPanelData struct {
	Status  int
	Title   string
	Message string
}

// uiNotFound renders an unmatched portal URL inside the app shell instead of
// letting Go's plain-text default escape the layout.
func (s *Server) uiNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderUIErrorPanel(w, r, uiErrorPageFor(http.StatusNotFound))
}

// uiErrorPageFor is the error panel copy for a page load that failed with
// status.
func uiErrorPageFor(status int) uiErrorPanelData {
	data := uiErrorPanelData{Status: status}
	switch {
	case status == http.StatusNotFound:
		data.Title, data.Message = "Page not found", "That page does not exist, or you do not have access to it."
	case status == http.StatusForbidden:
		data.Title, data.Message = "No access", "You do not have permission to view this page."
	case status == http.StatusConflict:
		data.Title, data.Message = "Page changed", "Something on this page changed while it loaded. Reload to try again."
	case status == http.StatusBadRequest:
		data.Title, data.Message = "Bad request", "That link is not valid. Check it and try again."
	case status >= http.StatusInternalServerError:
		data.Title, data.Message = "Something went wrong", "trackslash could not load this page. Try again in a moment."
	default:
		data.Title, data.Message = "Page could not load", "This page could not be loaded. Go back and try again."
	}
	return data
}

// uiErrorPages gives a failed page load the branded error page. Portal
// handlers answer errors with short plain-text bodies (writeUIStoreError,
// http.Error), which suit htmx because it does not swap error responses, but
// show up raw when the browser itself navigated to the page. Only a browser
// navigation is rewritten; htmx, fetch, image, and websocket requests get the
// handler's response untouched.
func (s *Server) uiErrorPages(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isUIPageNavigation(r) {
			next.ServeHTTP(w, r)
			return
		}
		ew := &uiErrorPageWriter{ResponseWriter: w}
		next.ServeHTTP(ew, r)
		if ew.errorStatus != 0 {
			s.renderUIErrorPanel(w, r, uiErrorPageFor(ew.errorStatus))
		}
	})
}

// isUIPageNavigation reports whether the browser is loading r as a whole page:
// a GET that accepts HTML and did not come from htmx.
func isUIPageNavigation(r *http.Request) bool {
	return r.Method == http.MethodGet && !isHTMXRequest(r) && strings.Contains(r.Header.Get("Accept"), "text/html")
}

// uiErrorPageWriter holds back a plain-text error response so uiErrorPages
// can replace it, and passes every other response straight through.
type uiErrorPageWriter struct {
	http.ResponseWriter
	wroteHeader bool
	errorStatus int
}

func (w *uiErrorPageWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if status >= http.StatusBadRequest && strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		w.errorStatus = status
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *uiErrorPageWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.errorStatus != 0 {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer's Flush and
// Hijack.
func (w *uiErrorPageWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// uiMethodNotAllowed answers a known path addressed with the wrong method. Go's
// default writes a 405 with an empty body.
func (s *Server) uiMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	s.renderUIErrorPanel(w, r, uiErrorPanelData{
		Status:  http.StatusMethodNotAllowed,
		Title:   "Method not allowed",
		Message: "That page cannot be reached this way.",
	})
}

func (s *Server) renderUIErrorPanel(w http.ResponseWriter, r *http.Request, data uiErrorPanelData) {
	s.renderUIShell(w, r, data.Status, uiShellData{ErrorPanel: &data})
}

func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}

func apiMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// uiOptionalAuth resolves the session cookie when one is present so error pages
// render with the signed-in shell, and otherwise continues anonymously. Unlike
// uiAuthMiddleware it never redirects: an unauthenticated visitor who mistypes a
// URL should see the 404, not a login prompt for a page that does not exist.
func (s *Server) uiOptionalAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(uiAuthCookieName)
		// A store-less server still has to answer 404, so the shell degrades to
		// its anonymous form rather than dereferencing a nil store.
		if err != nil || strings.TrimSpace(cookie.Value) == "" || s.store == nil {
			next.ServeHTTP(w, r)
			return
		}
		auth, err := s.store.AuthenticateToken(r.Context(), cookie.Value)
		if err != nil {
			// An expired or revoked session simply renders anonymously; the
			// error page has nothing to protect. Any other error means the
			// database is unreachable, which only warrants a log line here.
			if !errors.Is(err, store.ErrUnauthorized) {
				logInternalError("ui optional auth authenticate token", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		// The signed-in shell names the user, as in uiAuthMiddleware.
		w.Header().Set("Cache-Control", "private, no-store")
		ctx := context.WithValue(r.Context(), authContextKey{}, authContext{User: auth.User, Token: auth.Token})
		ctx = store.WithActor(ctx, auth.User.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
