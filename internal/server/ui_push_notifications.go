package server

import (
	"errors"
	"io/fs"
	"net/http"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type uiPushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s *Server) uiServiceWorker(w http.ResponseWriter, r *http.Request) {
	content, err := fs.ReadFile(uiStaticFS, "service-worker.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write(content)
}

func (s *Server) uiUpdatePushNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	_, err := s.store.UpdatePushNotificationPreferences(r.Context(), currentUser(r).ID, model.PushNotificationPreferences{
		Mentions:       r.Form.Has("mentions"),
		Assignments:    r.Form.Has("assignments"),
		Comments:       r.Form.Has("comments"),
		StatusChanges:  r.Form.Has("status_changes"),
		DueDateChanges: r.Form.Has("due_date_changes"),
	})
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	http.Redirect(w, r, uiNotificationsPath, http.StatusSeeOther)
}

func (s *Server) uiPushSubscriptionState(w http.ResponseWriter, r *http.Request) {
	endpoint := r.URL.Query().Get("endpoint")
	if endpoint == "" {
		writeError(w, http.StatusBadRequest, "endpoint required")
		return
	}
	active, err := s.store.PushSubscriptionActive(r.Context(), currentUser(r).ID, endpoint)
	if err != nil {
		writeInternalError(w, "push subscription state", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"subscribed": active})
}

func (s *Server) uiUpsertPushSubscription(w http.ResponseWriter, r *http.Request) {
	if s.webPushPublicKey == "" {
		writeError(w, http.StatusServiceUnavailable, "browser push is not configured")
		return
	}
	var request uiPushSubscriptionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err := s.store.UpsertPushSubscription(r.Context(), store.UpsertPushSubscriptionParams{
		UserID:     currentUser(r).ID,
		Endpoint:   request.Endpoint,
		P256DH:     request.Keys.P256DH,
		AuthSecret: request.Keys.Auth,
		UserAgent:  r.UserAgent(),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"subscribed": true})
}

func (s *Server) uiDisablePushSubscription(w http.ResponseWriter, r *http.Request) {
	var request uiPushSubscriptionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "endpoint required")
		return
	}
	err := s.store.DisablePushSubscription(r.Context(), currentUser(r).ID, request.Endpoint)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}
