package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type meResp struct {
	User      model.User          `json:"user"`
	TokenKind model.AuthTokenKind `json:"token_kind"`
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	auth := currentAuth(r)
	writeJSON(w, http.StatusOK, meResp{User: auth.User, TokenKind: auth.Token.Kind})
}

type updateMySettingsReq struct {
	Name            *string `json:"name,omitempty"`
	Email           *string `json:"email,omitempty"`
	CurrentPassword string  `json:"current_password,omitempty"`
	NewPassword     string  `json:"new_password,omitempty"`
}

func (s *Server) updateMySettings(w http.ResponseWriter, r *http.Request) {
	var req updateMySettingsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	auth := currentAuth(r)
	changed := false
	user := auth.User
	if req.Name != nil || req.Email != nil {
		name := user.Name
		email := user.Email
		if req.Name != nil {
			name = *req.Name
		}
		if req.Email != nil {
			email = *req.Email
		}
		if strings.TrimSpace(name) == "" {
			writeError(w, http.StatusBadRequest, "name required")
			return
		}
		if err := store.ValidateEmail(email); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var err error
		user, err = s.store.UpdateUserProfile(r.Context(), user.ID, name, email)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		changed = true
	}
	if req.CurrentPassword != "" || req.NewPassword != "" {
		if req.CurrentPassword == "" || req.NewPassword == "" {
			writeError(w, http.StatusBadRequest, "current_password and new_password required")
			return
		}
		if err := store.ValidatePassword(req.NewPassword); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.ChangePassword(r.Context(), auth.User.ID, req.CurrentPassword, req.NewPassword); err != nil {
			writeStoreError(w, err)
			return
		}
		changed = true
	}
	if !changed {
		writeError(w, http.StatusBadRequest, "settings change required")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

type createAccountReq struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Name        string `json:"name,omitempty"`
	AcceptTerms bool   `json:"accept_terms,omitempty"`
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.allowAuthIdentifier(w, req.Username) {
		return
	}
	previewTermsVersion, err := s.previewTermsVersion(req.AcceptTerms)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := store.NormalizeUsername(req.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.store.CreateAccount(r.Context(), store.CreateAccountParams{
		Username:            req.Username,
		Password:            req.Password,
		Name:                req.Name,
		PreviewTermsVersion: previewTermsVersion,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

type createSessionReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.allowAuthIdentifier(w, req.Username) {
		return
	}
	u, err := s.store.AuthenticatePassword(r.Context(), req.Username, req.Password)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	created, err := s.createSessionToken(r, u, "session")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createTokenResp{AuthToken: created.Token, Token: created.RawToken})
}

type createTokenReq struct {
	Name      string               `json:"name"`
	Kind      *model.AuthTokenKind `json:"kind,omitempty"`
	ExpiresAt *time.Time           `json:"expires_at,omitempty"`
}

type createTokenResp struct {
	model.AuthToken
	Token string `json:"token"`
}

func (s *Server) createUserToken(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req createTokenReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "name required, max 200 chars")
		return
	}
	kind := model.AuthTokenKindAPI
	if req.Kind != nil {
		if !req.Kind.Valid() {
			writeError(w, http.StatusBadRequest, "invalid token kind")
			return
		}
		kind = *req.Kind
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "expires_at must be in the future")
		return
	}
	created, err := s.store.CreateAuthToken(r.Context(), store.CreateAuthTokenParams{
		UserID: userID, Kind: kind, Name: req.Name, ExpiresAt: s.authTokenExpiry(kind, req.ExpiresAt),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createTokenResp{AuthToken: created.Token, Token: created.RawToken})
}

func (s *Server) createMyToken(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	var req createTokenReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "name required, max 200 chars")
		return
	}
	kind := model.AuthTokenKindAPI
	if req.Kind != nil {
		if !req.Kind.Valid() {
			writeError(w, http.StatusBadRequest, "invalid token kind")
			return
		}
		kind = *req.Kind
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "expires_at must be in the future")
		return
	}
	created, err := s.store.CreateAuthToken(r.Context(), store.CreateAuthTokenParams{
		UserID: currentUser(r).ID, Kind: kind, Name: req.Name, ExpiresAt: s.authTokenExpiry(kind, req.ExpiresAt),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createTokenResp{AuthToken: created.Token, Token: created.RawToken})
}

func (s *Server) createSessionToken(r *http.Request, user model.User, name string) (store.CreatedAuthToken, error) {
	return s.store.CreateAuthToken(r.Context(), store.CreateAuthTokenParams{
		UserID:    user.ID,
		Kind:      model.AuthTokenKindSession,
		Name:      name,
		ExpiresAt: s.authTokenExpiry(model.AuthTokenKindSession, nil),
	})
}

func (s *Server) authTokenExpiry(kind model.AuthTokenKind, requested *time.Time) *time.Time {
	absolute := time.Now().Add(s.sessionTTL)
	return boundedSessionExpiry(kind, requested, &absolute)
}

func boundedSessionExpiry(kind model.AuthTokenKind, requested, absolute *time.Time) *time.Time {
	if kind != model.AuthTokenKindSession {
		return requested
	}
	if requested == nil || requested.After(*absolute) {
		return absolute
	}
	return requested
}

func (s *Server) listUserTokens(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tokens, err := s.store.ListAuthTokens(r.Context(), userID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) listMyTokens(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	tokens, err := s.store.ListAuthTokens(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.RevokeAuthToken(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeMyToken(w http.ResponseWriter, r *http.Request) {
	if !requireFirstPartyToken(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.store.RevokeAuthTokenForUser(r.Context(), currentUser(r).ID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
