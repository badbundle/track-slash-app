package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type failingCSRFReader struct{}

func (failingCSRFReader) Read([]byte) (int, error) {
	return 0, errors.New("random unavailable")
}

func TestUIPreAuthCSRFTokenLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("creates strict host cookie", func(t *testing.T) {
		srv := &Server{csrfRandom: strings.NewReader(strings.Repeat("x", 32)), secureCookies: true}
		req := httptest.NewRequest(http.MethodGet, "https://track.test/login", nil)
		rec := httptest.NewRecorder()
		token, err := srv.ensureUIPreAuthCSRFToken(rec, req)
		if err != nil {
			t.Fatalf("ensureUIPreAuthCSRFToken: %v", err)
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("cookies = %v", cookies)
		}
		cookie := cookies[0]
		if cookie.Name != uiPreAuthCSRFCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != int(uiPreAuthCSRFMaxAge.Seconds()) {
			t.Fatalf("cookie = %+v", cookie)
		}
		if token != uiDerivedCSRFToken("pre-auth", cookie.Value) {
			t.Fatalf("token = %q, want token derived from cookie", token)
		}
	})

	t.Run("reuses valid cookie", func(t *testing.T) {
		srv := &Server{}
		secret := "eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg"
		req := httptest.NewRequest(http.MethodGet, "http://track.test/signup", nil)
		req.AddCookie(&http.Cookie{Name: uiPreAuthCSRFCookieName, Value: secret})
		rec := httptest.NewRecorder()
		token, err := srv.ensureUIPreAuthCSRFToken(rec, req)
		if err != nil {
			t.Fatalf("ensureUIPreAuthCSRFToken: %v", err)
		}
		if token != uiDerivedCSRFToken("pre-auth", secret) || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("token = %q cookies = %v", token, rec.Result().Cookies())
		}
	})

	t.Run("uses validated context token", func(t *testing.T) {
		srv := &Server{csrfRandom: failingCSRFReader{}}
		req := httptest.NewRequest(http.MethodPost, "http://track.test/login", nil)
		req = req.WithContext(context.WithValue(req.Context(), uiCSRFContextKey{}, "already-validated"))
		token, err := srv.ensureUIPreAuthCSRFToken(httptest.NewRecorder(), req)
		if err != nil || token != "already-validated" {
			t.Fatalf("token = %q err = %v", token, err)
		}
	})

	t.Run("reports random failure", func(t *testing.T) {
		srv := &Server{csrfRandom: failingCSRFReader{}}
		_, err := srv.ensureUIPreAuthCSRFToken(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://track.test/login", nil))
		if !errors.Is(err, errUICSRFRandom) {
			t.Fatalf("err = %v, want errUICSRFRandom", err)
		}
	})

	t.Run("defaults to crypto random", func(t *testing.T) {
		token, err := (&Server{}).ensureUIPreAuthCSRFToken(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://track.test/login", nil))
		if err != nil || token == "" {
			t.Fatalf("token = %q err = %v", token, err)
		}
	})

	t.Run("clears matching cookie", func(t *testing.T) {
		srv := &Server{secureCookies: true}
		rec := httptest.NewRecorder()
		srv.clearUIPreAuthCSRFCookie(rec, httptest.NewRequest(http.MethodPost, "https://track.test/login", nil))
		cookie := rec.Result().Cookies()[0]
		if cookie.Name != uiPreAuthCSRFCookieName || cookie.MaxAge != -1 || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie = %+v", cookie)
		}
	})
}

func TestValidUIPreAuthCSRFSecret(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		secret string
		want   bool
	}{
		{name: "valid", secret: "eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg", want: true},
		{name: "short", secret: "eA", want: false},
		{name: "invalid encoding", secret: "%%%", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := validUIPreAuthCSRFSecret(tt.secret); got != tt.want {
				t.Fatalf("validUIPreAuthCSRFSecret() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUICSRFMiddleware(t *testing.T) {
	t.Parallel()

	const expected = "expected-token"
	tests := []struct {
		name           string
		method         string
		body           io.Reader
		contentType    string
		headerToken    string
		expectedToken  string
		origin         string
		referer        string
		fetchSite      string
		htmx           bool
		publicOrigin   string
		requestURL     string
		wantStatusCode int
	}{
		{name: "safe method bypasses token", method: http.MethodGet, wantStatusCode: http.StatusNoContent},
		{name: "header token", method: http.MethodPost, headerToken: expected, expectedToken: expected, wantStatusCode: http.StatusNoContent},
		{name: "standard form token", method: http.MethodPost, body: strings.NewReader(url.Values{uiCSRFFormName: {expected}}.Encode()), contentType: "application/x-www-form-urlencoded", expectedToken: expected, wantStatusCode: http.StatusNoContent},
		{name: "htmx token", method: http.MethodPost, headerToken: expected, expectedToken: expected, htmx: true, wantStatusCode: http.StatusNoContent},
		{name: "multipart upload token", method: http.MethodPost, body: strings.NewReader("multipart body stays unread"), contentType: "multipart/form-data; boundary=test", headerToken: expected, expectedToken: expected, wantStatusCode: http.StatusNoContent},
		{name: "same origin", method: http.MethodDelete, headerToken: expected, expectedToken: expected, origin: "http://track.test", wantStatusCode: http.StatusNoContent},
		{name: "same HTTPS origin", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "https://track.test", requestURL: "https://track.test/mutation", wantStatusCode: http.StatusNoContent},
		{name: "same origin referer", method: http.MethodPatch, headerToken: expected, expectedToken: expected, referer: "http://track.test/settings", wantStatusCode: http.StatusNoContent},
		{name: "same origin fetch metadata", method: http.MethodPost, headerToken: expected, expectedToken: expected, fetchSite: "same-origin", wantStatusCode: http.StatusNoContent},
		{name: "browser initiated without origin", method: http.MethodPost, headerToken: expected, expectedToken: expected, fetchSite: "none", wantStatusCode: http.StatusNoContent},
		{name: "configured public origin", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "https://app.example.com", publicOrigin: "https://app.example.com", wantStatusCode: http.StatusNoContent},
		// Referrer-Policy: no-referrer makes a plain form submission arrive with
		// the opaque origin and no Referer, so these are the shapes every
		// non-htmx form posts in.
		{name: "opaque origin with fetch metadata", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "null", fetchSite: "same-origin", wantStatusCode: http.StatusNoContent},
		{name: "opaque origin alone", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "null", wantStatusCode: http.StatusNoContent},
		{name: "opaque origin with same origin referer", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "null", referer: "http://track.test/settings", wantStatusCode: http.StatusNoContent},
		{name: "missing expected token", method: http.MethodPost, headerToken: expected, wantStatusCode: http.StatusForbidden},
		{name: "missing provided token", method: http.MethodPost, expectedToken: expected, wantStatusCode: http.StatusForbidden},
		{name: "invalid token", method: http.MethodPost, headerToken: "wrong", expectedToken: expected, wantStatusCode: http.StatusForbidden},
		{name: "malformed form", method: http.MethodPost, body: strings.NewReader("csrf_token=%zz"), contentType: "application/x-www-form-urlencoded", expectedToken: expected, wantStatusCode: http.StatusForbidden},
		{name: "unsupported body token", method: http.MethodPost, body: strings.NewReader(`{"csrf_token":"expected-token"}`), contentType: "application/json", expectedToken: expected, wantStatusCode: http.StatusForbidden},
		{name: "invalid content type", method: http.MethodPost, body: strings.NewReader("csrf_token=expected-token"), contentType: "not a content type", expectedToken: expected, wantStatusCode: http.StatusForbidden},
		{name: "cross origin", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "https://evil.example", wantStatusCode: http.StatusForbidden},
		{name: "sibling origin", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "http://sibling.track.test", wantStatusCode: http.StatusForbidden},
		{name: "cross origin referer", method: http.MethodPost, headerToken: expected, expectedToken: expected, referer: "https://evil.example/form", wantStatusCode: http.StatusForbidden},
		// A sandboxed cross-site frame posts with the same opaque origin, so the
		// relaxation above must not be the whole check.
		{name: "opaque origin from a cross-site frame", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "null", fetchSite: "cross-site", wantStatusCode: http.StatusForbidden},
		{name: "opaque origin with cross origin referer", method: http.MethodPost, headerToken: expected, expectedToken: expected, origin: "null", referer: "https://evil.example/form", wantStatusCode: http.StatusForbidden},
		{name: "same-site fetch metadata", method: http.MethodPost, headerToken: expected, expectedToken: expected, fetchSite: "same-site", wantStatusCode: http.StatusForbidden},
		{name: "cross-site fetch metadata", method: http.MethodPost, headerToken: expected, expectedToken: expected, fetchSite: "cross-site", wantStatusCode: http.StatusForbidden},
		{name: "unknown fetch metadata", method: http.MethodPost, headerToken: expected, expectedToken: expected, fetchSite: "unexpected", wantStatusCode: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &Server{publicOrigin: tt.publicOrigin}
			nextCalled := false
			handler := srv.uiCSRFMiddleware(func(*http.Request) string { return tt.expectedToken })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				if tt.method != http.MethodGet {
					if token, _ := r.Context().Value(uiCSRFContextKey{}).(string); token != expected {
						t.Errorf("context token = %q, want %q", token, expected)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			requestURL := tt.requestURL
			if requestURL == "" {
				requestURL = "http://track.test/mutation"
			}
			req := httptest.NewRequest(tt.method, requestURL, tt.body)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}
			if tt.headerToken != "" {
				req.Header.Set(uiCSRFHeaderName, tt.headerToken)
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}
			if tt.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatusCode {
				t.Fatalf("status = %d body = %q, want %d", rec.Code, rec.Body.String(), tt.wantStatusCode)
			}
			if nextCalled != (tt.wantStatusCode == http.StatusNoContent) {
				t.Fatalf("nextCalled = %v", nextCalled)
			}
		})
	}
}

func TestUICSRFRouteSpecificTokenSources(t *testing.T) {
	t.Parallel()

	t.Run("pre-auth cookie", func(t *testing.T) {
		secret := "eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg"
		req := httptest.NewRequest(http.MethodPost, "http://track.test/login", nil)
		req.AddCookie(&http.Cookie{Name: uiPreAuthCSRFCookieName, Value: secret})
		req.Header.Set(uiCSRFHeaderName, uiDerivedCSRFToken("pre-auth", secret))
		rec := httptest.NewRecorder()
		(&Server{}).uiPreAuthCSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("session cookie", func(t *testing.T) {
		secret := "session-secret"
		req := httptest.NewRequest(http.MethodDelete, "http://track.test/object", nil)
		req.AddCookie(&http.Cookie{Name: uiAuthCookieName, Value: secret})
		req.Header.Set(uiCSRFHeaderName, uiDerivedCSRFToken("session", secret))
		rec := httptest.NewRecorder()
		(&Server{}).uiSessionCSRFMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing pre-auth cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://track.test/login", nil)
		req.Header.Set(uiCSRFHeaderName, "anything")
		rec := httptest.NewRecorder()
		(&Server{}).uiPreAuthCSRFMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler called") })).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d", rec.Code)
		}
	})
}

func TestUISessionCSRFToken(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "http://track.test", nil)
	if got := uiSessionCSRFToken(req); got != "" {
		t.Fatalf("token without cookie = %q", got)
	}
	req.AddCookie(&http.Cookie{Name: uiAuthCookieName, Value: "session-secret"})
	if got := uiSessionCSRFToken(req); got != uiDerivedCSRFToken("session", "session-secret") {
		t.Fatalf("cookie token = %q", got)
	}
	req = req.WithContext(context.WithValue(req.Context(), uiCSRFContextKey{}, "context-token"))
	if got := uiSessionCSRFToken(req); got != "context-token" {
		t.Fatalf("context token = %q", got)
	}
}

func TestUISameOrigin(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		candidate string
		expected  string
		want      bool
	}{
		{name: "case insensitive", candidate: "HTTPS://TRACK.EXAMPLE/settings", expected: "https://track.example", want: true},
		{name: "different port", candidate: "https://track.example:8443", expected: "https://track.example", want: false},
		{name: "credentials rejected", candidate: "https://user@track.example", expected: "https://track.example", want: false},
		{name: "candidate invalid", candidate: "://", expected: "https://track.example", want: false},
		{name: "expected invalid", candidate: "https://track.example", expected: "://", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := uiSameOrigin(tt.candidate, tt.expected); got != tt.want {
				t.Fatalf("uiSameOrigin(%q, %q) = %v, want %v", tt.candidate, tt.expected, got, tt.want)
			}
		})
	}
}

func TestBearerAPIPostDoesNotRequireUICSRFToken(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	New(nil, nil, nil).Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %q, want API validation response", rec.Code, rec.Body.String())
	}
}

func TestUIAuthRoutesRequirePreAuthCSRF(t *testing.T) {
	t.Parallel()
	router := New(nil, nil, nil).Router()
	seedRequest := httptest.NewRequest(http.MethodGet, "/login", nil)
	seedResponse := httptest.NewRecorder()
	router.ServeHTTP(seedResponse, seedRequest)
	if seedResponse.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d body = %q", seedResponse.Code, seedResponse.Body.String())
	}
	var seedCookie *http.Cookie
	for _, cookie := range seedResponse.Result().Cookies() {
		if cookie.Name == uiPreAuthCSRFCookieName {
			seedCookie = cookie
			break
		}
	}
	if seedCookie == nil {
		t.Fatal("GET /login did not set pre-auth CSRF cookie")
	}
	validToken := uiDerivedCSRFToken("pre-auth", seedCookie.Value)
	for _, tt := range []struct {
		name       string
		token      string
		origin     string
		withCookie bool
		want       int
	}{
		{name: "missing cookie", token: validToken, want: http.StatusForbidden},
		{name: "missing token", withCookie: true, want: http.StatusForbidden},
		{name: "invalid token", withCookie: true, token: "wrong", want: http.StatusForbidden},
		{name: "cross origin", withCookie: true, token: validToken, origin: "https://evil.example", want: http.StatusForbidden},
		{name: "valid standard form", withCookie: true, token: validToken, origin: "http://example.com", want: http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{uiCSRFFormName: {tt.token}}
			req := httptest.NewRequest(http.MethodPost, "http://example.com/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.withCookie {
				req.AddCookie(seedCookie)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d body = %q, want %d", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}
}

func TestUIAuthRenderCSRFErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		render func(*Server, http.ResponseWriter, *http.Request)
	}{
		{name: "login", render: func(s *Server, w http.ResponseWriter, r *http.Request) {
			s.renderUILogin(w, r, http.StatusOK, uiLoginData{})
		}},
		{name: "signup", render: func(s *Server, w http.ResponseWriter, r *http.Request) {
			s.renderUISignup(w, r, http.StatusOK, uiSignupData{})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.render(&Server{csrfRandom: failingCSRFReader{}}, rec, httptest.NewRequest(http.MethodGet, "http://track.test/"+tt.name, nil))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUIClientAssetsSendCSRFTokens(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		path string
		want []string
	}{
		{path: "static/auth.js", want: []string{`meta[name="csrf-token"]`, `"X-CSRF-Token": csrfToken`}},
		{path: "static/app.js", want: []string{`meta[name="csrf-token"]`, `htmx:configRequest`, `ensureCSRFFormToken`, `headers: csrfHeaders`, `method: "DELETE", credentials: "same-origin", headers: csrfHeaders()`}},
	} {
		body, err := uiTemplateFS.ReadFile(tt.path)
		if err != nil {
			t.Fatalf("read %s: %v", tt.path, err)
		}
		for _, want := range tt.want {
			if !strings.Contains(string(body), want) {
				t.Fatalf("%s missing %q", tt.path, want)
			}
		}
	}
}
