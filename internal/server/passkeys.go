package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/passkeys"
	"github.com/bradleymackey/track-slash/internal/store"
)

type passkeySignupOptionsReq struct {
	Username    string `json:"username"`
	Name        string `json:"name,omitempty"`
	AcceptTerms bool   `json:"accept_terms,omitempty"`
}

type passkeyFinishReq struct {
	CeremonyID  uuid.UUID       `json:"ceremony_id"`
	Credential  json.RawMessage `json:"credential"`
	Next        string          `json:"next,omitempty"`
	AcceptTerms bool            `json:"accept_terms,omitempty"`
}

type passkeyAddOptionsReq struct {
	Name        string `json:"name,omitempty"`
	ReauthToken string `json:"reauth_token"`
}

type passkeyReauthPasswordReq struct {
	CurrentPassword string `json:"current_password"`
}

type passkeyReauthTokenReq struct {
	ReauthToken string `json:"reauth_token"`
}

type passwordLoginReq struct {
	Enabled     *bool  `json:"enabled"`
	ReauthToken string `json:"reauth_token"`
}

type passkeyReauthResp struct {
	ReauthToken string `json:"reauth_token"`
}

type uiPasskeyRedirectResp struct {
	Next string `json:"next"`
}

func (s *Server) createPasskeyAccountOptions(w http.ResponseWriter, r *http.Request) {
	var req passkeySignupOptionsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.previewTermsVersion(req.AcceptTerms); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	username, err := store.NormalizeUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.allowAuthIdentifier(w, signupKey(username)) {
		return
	}
	options, err := s.passkeyOptions().BeginSignup(r.Context(), r, passkeys.SignupOptionsParams{
		Username: username,
		Name:     req.Name,
	})
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

func (s *Server) createPasskeyAccount(w http.ResponseWriter, r *http.Request) {
	var req passkeyFinishReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	previewTermsVersion, err := s.previewTermsVersion(req.AcceptTerms)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.passkeyOptions().FinishSignup(r.Context(), r, req.CeremonyID, req.Credential, previewTermsVersion)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) createPasskeySessionOptions(w http.ResponseWriter, r *http.Request) {
	options, err := s.passkeyOptions().BeginLogin(r.Context(), r)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

func (s *Server) createPasskeySession(w http.ResponseWriter, r *http.Request) {
	var req passkeyFinishReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.passkeyOptions().FinishLogin(r.Context(), r, req.CeremonyID, req.Credential)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	created, err := s.createSessionToken(r, u, "session")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createTokenResp{AuthToken: created.Token, Token: created.RawToken})
}

func (s *Server) listMyPasskeys(w http.ResponseWriter, r *http.Request) {
	passkeys, err := s.store.ListPasskeyCredentials(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, passkeys)
}

func (s *Server) getMyPasswordLogin(w http.ResponseWriter, r *http.Request) {
	state, err := s.store.PasswordLoginState(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) updateMyPasswordLogin(w http.ResponseWriter, r *http.Request) {
	var req passwordLoginReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled required")
		return
	}
	if err := s.store.ConsumePasskeyReauthToken(r.Context(), currentUser(r).ID, req.ReauthToken); err != nil {
		writeStoreError(w, err)
		return
	}
	state, err := s.store.SetPasswordLoginEnabled(r.Context(), currentUser(r).ID, *req.Enabled)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) createPasswordReauth(w http.ResponseWriter, r *http.Request) {
	var req passkeyReauthPasswordReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.VerifyUserPassword(r.Context(), currentUser(r).ID, req.CurrentPassword); err != nil {
		writeStoreError(w, err)
		return
	}
	s.writeReauthToken(w, r)
}

func (s *Server) createPasskeyReauthOptions(w http.ResponseWriter, r *http.Request) {
	options, err := s.passkeyOptions().BeginReauth(r.Context(), r, currentUser(r))
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

func (s *Server) createPasskeyReauth(w http.ResponseWriter, r *http.Request) {
	var req passkeyFinishReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.passkeyOptions().FinishReauth(r.Context(), r, req.CeremonyID, req.Credential)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	if u.ID != currentUser(r).ID {
		writeUnauthorized(w)
		return
	}
	s.writeReauthToken(w, r)
}

func (s *Server) createMyPasskeyOptions(w http.ResponseWriter, r *http.Request) {
	var req passkeyAddOptionsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.ConsumePasskeyReauthToken(r.Context(), currentUser(r).ID, req.ReauthToken); err != nil {
		writeStoreError(w, err)
		return
	}
	options, err := s.passkeyOptions().BeginAdd(r.Context(), r, passkeys.AddOptionsParams{User: currentUser(r), Name: req.Name})
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

func (s *Server) createMyPasskey(w http.ResponseWriter, r *http.Request) {
	var req passkeyFinishReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cred, err := s.passkeyOptions().FinishAdd(r.Context(), r, currentUser(r).ID, req.CeremonyID, req.Credential)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cred)
}

func (s *Server) revokeMyPasskey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req passkeyReauthTokenReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.ConsumePasskeyReauthToken(r.Context(), currentUser(r).ID, req.ReauthToken); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.store.RevokePasskeyCredentialForUser(r.Context(), currentUser(r).ID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) uiPasskeySignupOptions(w http.ResponseWriter, r *http.Request) {
	s.createPasskeyAccountOptions(w, r)
}

func (s *Server) uiPasskeySignup(w http.ResponseWriter, r *http.Request) {
	var req passkeyFinishReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	previewTermsVersion, err := s.previewTermsVersion(req.AcceptTerms)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.passkeyOptions().FinishSignup(r.Context(), r, req.CeremonyID, req.Credential, previewTermsVersion)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	created, err := s.createSessionToken(r, u, "web session")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.setUISessionCookie(w, r, created.RawToken, created.Token.ExpiresAt)
	writeJSON(w, http.StatusCreated, uiPasskeyRedirectResp{Next: safeUINext(req.Next)})
}

func (s *Server) uiPasskeyLoginOptions(w http.ResponseWriter, r *http.Request) {
	s.createPasskeySessionOptions(w, r)
}

func (s *Server) uiPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	var req passkeyFinishReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.passkeyOptions().FinishLogin(r.Context(), r, req.CeremonyID, req.Credential)
	if err != nil {
		writePasskeyError(w, err)
		return
	}
	created, err := s.createSessionToken(r, u, "web session")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.setUISessionCookie(w, r, created.RawToken, created.Token.ExpiresAt)
	writeJSON(w, http.StatusCreated, uiPasskeyRedirectResp{Next: safeUINext(req.Next)})
}

func (s *Server) uiPasskeyPasswordReauth(w http.ResponseWriter, r *http.Request) {
	s.createPasswordReauth(w, r)
}

func (s *Server) uiUpdatePasswordLogin(w http.ResponseWriter, r *http.Request) {
	s.updateMyPasswordLogin(w, r)
}

func (s *Server) uiPasskeyReauthOptions(w http.ResponseWriter, r *http.Request) {
	s.createPasskeyReauthOptions(w, r)
}

func (s *Server) uiPasskeyReauth(w http.ResponseWriter, r *http.Request) {
	s.createPasskeyReauth(w, r)
}

func (s *Server) uiPasskeyAddOptions(w http.ResponseWriter, r *http.Request) {
	s.createMyPasskeyOptions(w, r)
}

func (s *Server) uiPasskeyAdd(w http.ResponseWriter, r *http.Request) {
	s.createMyPasskey(w, r)
}

func (s *Server) uiPasskeyRevoke(w http.ResponseWriter, r *http.Request) {
	s.revokeMyPasskey(w, r)
}

func (s *Server) writeReauthToken(w http.ResponseWriter, r *http.Request) {
	token, err := s.store.CreatePasskeyReauthToken(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, passkeyReauthResp{ReauthToken: token})
}

func (s *Server) passkeyOptions() *passkeys.Service {
	return s.passkeys
}

func writePasskeyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrUnauthorized), errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrConflict):
		writeStoreError(w, err)
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
