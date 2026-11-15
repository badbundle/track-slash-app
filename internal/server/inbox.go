package server

import (
	"fmt"
	"net/http"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// parseInboxState reads the Inbox's state filter. Todo is the default.
func parseInboxState(raw string) (store.InboxState, error) {
	switch raw {
	case "", string(store.InboxTodo):
		return store.InboxTodo, nil
	case string(store.InboxDone):
		return store.InboxDone, nil
	}
	return "", fmt.Errorf("state must be todo or done")
}

func inboxNextCursor(state store.InboxState, forms []model.FormSummary, hasMore bool) *string {
	if !hasMore {
		return nil
	}
	next := encodeCursor(store.NextInboxCursor(state, forms[len(forms)-1]))
	return &next
}

// listInbox lists the forms addressed to the signed-in user across their
// projects.
func (s *Server) listInbox(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	state, err := parseInboxState(query.Get("state"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := parseLimit(query.Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params := store.ListInboxFormsParams{UserID: currentUser(r).ID, State: state, Limit: limit}
	if raw := query.Get("cursor"); raw != "" {
		var c store.InboxCursor
		if err := decodeCursor(raw, &c); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Cursor = &c
	}
	forms, hasMore, err := s.store.ListInboxForms(r.Context(), params)
	if err != nil {
		writeStoreError(w, err) // defensive: only a DB outage fails here
		return
	}
	writePage(w, s.formSummaryResponses(forms), inboxNextCursor(state, forms, hasMore))
}
