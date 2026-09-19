package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/store"
)

const (
	uiOAuthClientNameMaxLength = 100
	uiOAuthClientMaxRedirects  = 10
)

func (s *Server) uiCreateOAuthClient(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUIOAuthClientError(w, r, "Unable to read form.")
		return
	}
	name := strings.TrimSpace(r.Form.Get("name"))
	if name == "" || len(name) > uiOAuthClientNameMaxLength {
		s.renderUIOAuthClientError(w, r, "Name required, max 100 characters.")
		return
	}
	redirectURIs, err := uiParseOAuthRedirectURIs(r.Form.Get("redirect_uris"))
	if err != nil {
		s.renderUIOAuthClientError(w, r, err.Error())
		return
	}
	created, err := s.store.CreateOAuthClient(r.Context(), store.CreateOAuthClientParams{
		UserID:       currentUser(r).ID,
		Name:         name,
		RedirectURIs: redirectURIs,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.renderUIOAuthClientError(w, r, err.Error())
			return
		}
		s.renderUIOAuthClientError(w, r, "Unable to register connector.")
		return
	}
	// Post/Redirect/Get, for the same reason token creation uses it: a browser
	// reload of a form post would otherwise register a second connector.
	if !isHTMXRequest(r) {
		s.setUIOAuthSecretRevealCookie(w, r, created.Client.ClientID, created.RawSecret)
		http.Redirect(w, r, uiTokenRevealPath, http.StatusSeeOther)
		return
	}
	s.renderUITokenPanel(w, r, uiTokenPanelData{
		CreatedClientID:     created.Client.ClientID,
		CreatedClientSecret: created.RawSecret,
	})
}

func (s *Server) uiRevokeOAuthClient(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid client id", http.StatusBadRequest)
		return
	}
	if err := s.store.DisableOAuthClientForUser(r.Context(), currentUser(r).ID, id); err != nil {
		writeUIStoreError(w, err)
		return
	}
	http.Redirect(w, r, uiTokenRevealPath, http.StatusSeeOther)
}

func (s *Server) renderUIOAuthClientError(w http.ResponseWriter, r *http.Request, message string) {
	s.renderUITokenPanel(w, r, uiTokenPanelData{OAuthError: message})
}

// uiParseOAuthRedirectURIs reads one URI per line.
//
// Validation is strict because these addresses are where authorization codes get
// delivered, and they are matched exactly at authorization time. Rejecting a
// malformed one here is far kinder than letting it through to fail later as an
// unexplained "redirect address not registered".
func uiParseOAuthRedirectURIs(raw string) ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, uiOAuthClientMaxRedirects)
	for _, line := range strings.Split(raw, "\n") {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			continue
		}
		if err := uiValidateOAuthRedirectURI(candidate); err != nil {
			return nil, err
		}
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	if len(out) == 0 {
		return nil, errors.New("At least one redirect URI is required.")
	}
	if len(out) > uiOAuthClientMaxRedirects {
		return nil, errors.New("At most 10 redirect URIs are allowed.")
	}
	return out, nil
}

func uiValidateOAuthRedirectURI(candidate string) error {
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" {
		return errors.New("Redirect URIs must be absolute, for example https://claude.ai/api/mcp/auth_callback.")
	}
	// A fragment is never sent to the server and cannot be matched, and the
	// authorization response appends its own query, so both are refused rather
	// than silently ignored.
	if parsed.Fragment != "" || strings.Contains(candidate, "#") {
		return errors.New("Redirect URIs cannot contain a fragment.")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		// Plaintext is only safe where the code never leaves the machine.
		if host := parsed.Hostname(); host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		return errors.New("http redirect URIs are only allowed for localhost.")
	default:
		return errors.New("Redirect URIs must use https, or http for localhost.")
	}
}
