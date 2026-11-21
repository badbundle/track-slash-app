package server

import (
	"errors"
	"net/http"

	"github.com/bradleymackey/track-slash/internal/store"
)

type deviceReq struct {
	// Token is the device's APNs token, as hex.
	Token string `json:"token"`
	// Name is shown nowhere yet; it helps someone reading the database tell
	// two phones apart.
	Name string `json:"name"`
}

// registerMyDevice records the signed-in user's iOS device so a form or
// mention reaches it as a push. The app calls it on every launch: APNs hands
// out a new token when it chooses to, and a token moves to whoever
// registered it last.
func (s *Server) registerMyDevice(w http.ResponseWriter, r *http.Request) {
	if !s.apnsEnabled {
		writeError(w, http.StatusServiceUnavailable, "iOS push is not configured")
		return
	}
	var req deviceReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.store.UpsertAPNsDevice(r.Context(), currentUser(r).ID, req.Token, req.Name); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusBadRequest, "invalid device token")
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"registered": true})
}

// unregisterMyDevice stops pushes to a device, as the app does on sign-out.
// Unknown tokens succeed: the end state is the one asked for.
func (s *Server) unregisterMyDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token required")
		return
	}
	err := s.store.DisablePushSubscription(r.Context(), currentUser(r).ID, req.Token)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"registered": false})
}
