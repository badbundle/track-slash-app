package server_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/server"
)

func TestAuthenticationRateLimits(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	t.Run("identifier across IPs", func(t *testing.T) {
		router := server.NewWithOptions(e.store, nil, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
			IPAttempts:         10,
			IPWindow:           time.Minute,
			IdentifierAttempts: 2,
			IdentifierWindow:   5 * time.Minute,
		}}).Router()
		for attempt, remote := range []string{"192.0.2.1:1001", "192.0.2.2:1002", "192.0.2.3:1003"} {
			response := serveUILogin(router, remote, "", "same-user")
			want := http.StatusUnauthorized
			if attempt == 2 {
				want = http.StatusTooManyRequests
			}
			if response.Code != want {
				t.Fatalf("attempt %d code = %d, want %d", attempt+1, response.Code, want)
			}
			// The window opened with the first attempt, and every sign-in
			// check costs a bcrypt comparison, so allow a few seconds.
			if want == http.StatusTooManyRequests {
				if retry, err := strconv.Atoi(response.Header().Get("Retry-After")); err != nil || retry < 290 || retry > 300 {
					t.Fatalf("Retry-After = %q, want about 300", response.Header().Get("Retry-After"))
				}
			}
		}
	})

	t.Run("IP across identifiers", func(t *testing.T) {
		router := server.NewWithOptions(e.store, nil, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
			IPAttempts:         2,
			IPWindow:           time.Minute,
			IdentifierAttempts: 10,
			IdentifierWindow:   5 * time.Minute,
		}}).Router()
		for attempt, username := range []string{"first-user", "second-user", "third-user"} {
			response := serveUILogin(router, "192.0.2.20:2000", "", username)
			want := http.StatusUnauthorized
			if attempt == 2 {
				want = http.StatusTooManyRequests
			}
			if response.Code != want {
				t.Fatalf("attempt %d code = %d, want %d", attempt+1, response.Code, want)
			}
		}
	})

	t.Run("forwarded IP ignored", func(t *testing.T) {
		router := server.NewWithOptions(e.store, nil, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
			IPAttempts:         2,
			IPWindow:           time.Minute,
			IdentifierAttempts: 10,
			IdentifierWindow:   5 * time.Minute,
		}}).Router()
		for attempt, forwarded := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
			response := serveUILogin(router, "192.0.2.30:3000", forwarded, "xff-user-"+forwarded)
			want := http.StatusUnauthorized
			if attempt == 2 {
				want = http.StatusTooManyRequests
			}
			if response.Code != want {
				t.Fatalf("attempt %d code = %d, want %d", attempt+1, response.Code, want)
			}
		}
	})

	t.Run("trusted proxy separates clients", func(t *testing.T) {
		_, proxyNetwork, err := net.ParseCIDR("192.0.2.0/24")
		if err != nil {
			t.Fatalf("ParseCIDR: %v", err)
		}
		router := server.NewWithOptions(e.store, nil, server.Options{
			TrustedProxyCIDRs: []net.IPNet{*proxyNetwork},
			AuthRateLimit: server.AuthRateLimitOptions{
				IPAttempts:         2,
				IPWindow:           time.Minute,
				IdentifierAttempts: 10,
				IdentifierWindow:   5 * time.Minute,
			},
		}).Router()
		for attempt, forwarded := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
			response := serveUILogin(router, "192.0.2.30:3000", forwarded, "proxy-user-"+forwarded)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d code = %d, want %d", attempt+1, response.Code, http.StatusUnauthorized)
			}
		}
	})

	t.Run("authenticated reauth account", func(t *testing.T) {
		router := server.NewWithOptions(e.store, nil, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
			IPAttempts:         10,
			IPWindow:           time.Minute,
			IdentifierAttempts: 2,
			IdentifierWindow:   5 * time.Minute,
		}}).Router()
		for attempt, remote := range []string{"192.0.2.41:4001", "192.0.2.42:4002", "192.0.2.43:4003"} {
			req := httptest.NewRequest(http.MethodPost, apiPath("/me/reauth/password"), strings.NewReader(`{"current_password":"wrong-password"}`))
			req.RemoteAddr = remote
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+e.authToken)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			want := http.StatusUnauthorized
			if attempt == 2 {
				want = http.StatusTooManyRequests
			}
			if response.Code != want {
				t.Fatalf("attempt %d code = %d body = %s, want %d", attempt+1, response.Code, response.Body.String(), want)
			}
		}
	})

	t.Run("passkey options IP", func(t *testing.T) {
		router := server.NewWithOptions(e.store, nil, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
			IPAttempts:         1,
			IPWindow:           time.Minute,
			IdentifierAttempts: 10,
			IdentifierWindow:   5 * time.Minute,
		}}).Router()
		for attempt := 0; attempt < 2; attempt++ {
			req := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/session/passkey/options", nil)
			req.RemoteAddr = "192.0.2.50:5000"
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			want := http.StatusOK
			if attempt == 1 {
				want = http.StatusTooManyRequests
			}
			if response.Code != want {
				t.Fatalf("attempt %d code = %d body = %s, want %d", attempt+1, response.Code, response.Body.String(), want)
			}
		}
	})
}

func serveUILogin(handler http.Handler, remote, forwarded, username string) *httptest.ResponseRecorder {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	form := url.Values{
		"username":   {username},
		"password":   {"wrong-password"},
		"csrf_token": {uiCSRFTokenForTest("pre-auth", secret)},
	}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: uiPreAuthCookieNameForTest, Value: secret, Path: "/"})
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

// Every budget lives in one table, so each purpose keeps its own key prefix and
// sign-up is budgeted by the username it would actually create. A sign-up
// whose "username" spells someone's user ID, or another budget's key, is just
// an invalid username: it spends nothing, least of all that person's budget.
func TestAuthenticationBudgetsAreSeparatePerPurpose(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	router := server.NewWithOptions(e.store, nil, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
		IPAttempts:         100,
		IPWindow:           time.Minute,
		IdentifierAttempts: 2,
		IdentifierWindow:   5 * time.Minute,
	}}).Router()

	for _, username := range []string{
		e.adminID.String(),
		"account:" + e.adminID.String(),
		"password-login:" + e.ownerUsername,
	} {
		for _, signup := range []func(http.Handler, string) *httptest.ResponseRecorder{serveAPISignup, servePasskeySignupOptions, serveUISignup} {
			for attempt := range 3 {
				if response := signup(router, username); response.Code != http.StatusBadRequest {
					t.Fatalf("sign-up %q attempt %d code = %d body = %s, want 400", username, attempt+1, response.Code, response.Body.String())
				}
			}
		}
	}

	// The account's own budgets are untouched: a wrong password is still just
	// wrong, not throttled.
	for attempt := range 2 {
		response := serveAuthJSON(router, "/api/v1/me/reauth/password", e.authToken, map[string]any{"current_password": "wrong-password"})
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("reauth attempt %d code = %d body = %s, want 401", attempt+1, response.Code, response.Body.String())
		}
	}
	if response := serveUILogin(router, "192.0.2.60:6000", "", e.ownerUsername); response.Code != http.StatusUnauthorized {
		t.Fatalf("password sign-in code = %d body = %s, want 401", response.Code, response.Body.String())
	}
}

// A username that is really being signed up for still has a budget of its own,
// on every way in.
func TestSignupBudgetCountsAttemptsPerUsername(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	for name, signup := range map[string]func(http.Handler, string) *httptest.ResponseRecorder{
		"api":     serveAPISignup,
		"passkey": servePasskeySignupOptions,
		"ui":      serveUISignup,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			router := server.NewWithOptions(e.store, nil, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
				IPAttempts:         100,
				IPWindow:           time.Minute,
				IdentifierAttempts: 2,
				IdentifierWindow:   5 * time.Minute,
			}}).Router()
			// The username is taken, so each attempt is refused without creating
			// anything, and every spelling of one name shares its budget.
			for attempt, username := range []string{e.ownerUsername, strings.ToUpper(e.ownerUsername), " " + e.ownerUsername + " "} {
				want := http.StatusConflict
				if attempt == 2 {
					want = http.StatusTooManyRequests
				}
				if response := signup(router, username); response.Code != want {
					t.Fatalf("attempt %d code = %d body = %s, want %d", attempt+1, response.Code, response.Body.String(), want)
				}
			}
		})
	}
}

// A passkey sign-in cannot be a guess, so a run of successful sign-ins never
// uses up the budget that reauthentication and password changes spend.
func TestSuccessfulPasskeySignInsAreNotThrottled(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	router := server.NewWithOptions(e.store, nil, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
		IPAttempts:         100,
		IPWindow:           time.Minute,
		IdentifierAttempts: 2,
		IdentifierWindow:   5 * time.Minute,
	}}).Router()
	user, _ := e.mustUserToken(t, "passkey-signin")
	passkey := newTestPasskey(t, e, user.ID)

	for _, ui := range []bool{false, true} {
		optionsPath, finishPath := "/api/v1/session/passkey/options", "/api/v1/session/passkey"
		if ui {
			optionsPath, finishPath = "/login/passkey/options", "/login/passkey"
		}
		for attempt := range 4 {
			response := servePasskeyJSON(router, ui, optionsPath, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("ui=%v options attempt %d code = %d body = %s", ui, attempt+1, response.Code, response.Body.String())
			}
			var options struct {
				CeremonyID uuid.UUID `json:"ceremony_id"`
				PublicKey  struct {
					Challenge string `json:"challenge"`
				} `json:"publicKey"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &options); err != nil {
				t.Fatalf("decode options: %v", err)
			}
			response = servePasskeyJSON(router, ui, finishPath, map[string]any{
				"ceremony_id": options.CeremonyID,
				"credential":  passkey.assert(t, options.PublicKey.Challenge),
			})
			if response.Code != http.StatusCreated {
				t.Fatalf("ui=%v sign-in attempt %d code = %d body = %s, want 201", ui, attempt+1, response.Code, response.Body.String())
			}
		}
	}
}

func serveAuthJSON(handler http.Handler, path, token string, payload any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "http://localhost"+path, bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.70:7000"
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func serveAPISignup(handler http.Handler, username string) *httptest.ResponseRecorder {
	return serveAuthJSON(handler, "/api/v1/accounts", "", map[string]any{"username": username, "password": "correct horse battery"})
}

func servePasskeySignupOptions(handler http.Handler, username string) *httptest.ResponseRecorder {
	return serveAuthJSON(handler, "/api/v1/accounts/passkey/options", "", map[string]any{"username": username})
}

func serveUISignup(handler http.Handler, username string) *httptest.ResponseRecorder {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	form := url.Values{
		"username":   {username},
		"password":   {"correct horse battery"},
		"csrf_token": {uiCSRFTokenForTest("pre-auth", secret)},
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost/signup", strings.NewReader(form.Encode()))
	req.RemoteAddr = "192.0.2.70:7000"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: uiPreAuthCookieNameForTest, Value: secret, Path: "/"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

// servePasskeyJSON posts one step of a passkey ceremony, through the signed-out
// UI and its CSRF check when ui is set.
func servePasskeyJSON(handler http.Handler, ui bool, path string, payload any) *httptest.ResponseRecorder {
	if !ui {
		return serveAuthJSON(handler, path, "", payload)
	}
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "http://localhost"+path, bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.70:7000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", uiCSRFTokenForTest("pre-auth", secret))
	req.AddCookie(&http.Cookie{Name: uiPreAuthCookieNameForTest, Value: secret, Path: "/"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

// testPasskey is a software authenticator holding one discoverable P-256
// credential for http://localhost, enough to complete a real passkey sign-in.
type testPasskey struct {
	key    *ecdsa.PrivateKey
	id     []byte
	handle []byte
	count  uint32
}

func newTestPasskey(t *testing.T, e *httpEnv, userID uuid.UUID) *testPasskey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	point, err := key.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("PublicKey.Bytes: %v", err)
	}
	publicKey, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         int64(webauthncose.P256),
		XCoord:        point[1:33],
		YCoord:        point[33:],
	})
	if err != nil {
		t.Fatalf("Marshal COSE key: %v", err)
	}
	id := []byte(uuid.NewString())
	if _, err := e.store.AddPasskeyCredential(e.ctx, userID, "localhost", "Test", webauthn.Credential{
		ID:        id,
		PublicKey: publicKey,
		Flags:     webauthn.CredentialFlags{UserPresent: true, UserVerified: true},
	}); err != nil {
		t.Fatalf("AddPasskeyCredential: %v", err)
	}
	handle, err := e.store.GetOrCreateWebAuthnHandle(e.ctx, userID, "localhost")
	if err != nil {
		t.Fatalf("GetOrCreateWebAuthnHandle: %v", err)
	}
	return &testPasskey{key: key, id: id, handle: handle}
}

// assert signs a challenge as a browser's authenticator would, with the user
// present and verified, and returns the credential JSON the server expects.
func (p *testPasskey) assert(t *testing.T, challenge string) json.RawMessage {
	t.Helper()
	p.count++
	clientData, err := json.Marshal(map[string]string{"type": "webauthn.get", "challenge": challenge, "origin": "http://localhost"})
	if err != nil {
		t.Fatalf("Marshal client data: %v", err)
	}
	rpIDHash := sha256.Sum256([]byte("localhost"))
	const userPresentAndVerified = 0x05
	authenticatorData := binary.BigEndian.AppendUint32(append(rpIDHash[:], userPresentAndVerified), p.count)
	clientDataHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authenticatorData...), clientDataHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, p.key, digest[:])
	if err != nil {
		t.Fatalf("SignASN1: %v", err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	credential, err := json.Marshal(map[string]any{
		"id":    encode(p.id),
		"rawId": encode(p.id),
		"type":  "public-key",
		"response": map[string]string{
			"clientDataJSON":    encode(clientData),
			"authenticatorData": encode(authenticatorData),
			"signature":         encode(signature),
			"userHandle":        encode(p.handle),
		},
	})
	if err != nil {
		t.Fatalf("Marshal credential: %v", err)
	}
	return credential
}
