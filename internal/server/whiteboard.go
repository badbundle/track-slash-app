package server

import (
	"net/http"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// Whiteboard pages share the Context page limits: a 200-character title and a
// Markdown body of at most 100000 characters, which may be empty.

type createWhiteboardPageReq struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type updateWhiteboardPageReq struct {
	Title *string `json:"title,omitempty"`
	Body  *string `json:"body,omitempty"`
}

func (s *Server) createWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	var req createWhiteboardPageReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title, err := validateProjectContextTitle(req.Title)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body, err := validateProjectContextBody(req.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateWhiteboardPage(r.Context(), store.CreateWhiteboardPageParams{
		ProjectID:   project.ID,
		Title:       title,
		Body:        body,
		CreatedByID: currentUser(r).ID,
	})
	if err != nil {
		writeStoreError(w, err) // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listWhiteboardPages(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var cursor *store.WhiteboardPagesCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.WhiteboardPagesCursor
		if err := decodeCursor(raw, &c); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cursor = &c
	}
	pages, hasMore, err := s.store.ListWhiteboardPages(r.Context(), store.ListWhiteboardPagesParams{
		ProjectID: project.ID,
		Cursor:    cursor,
		Limit:     limit,
	})
	if err != nil {
		writeStoreError(w, err) // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	writePage(w, pages, whiteboardPagesNextCursor(pages, hasMore))
}

func (s *Server) getWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	page, ok := s.whiteboardPageFromRoute(w, r, project)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) updateWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	page, ok := s.whiteboardPageFromRoute(w, r, project)
	if !ok {
		return
	}
	var req updateWhiteboardPageReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params := store.UpdateWhiteboardPageParams{ID: page.ID, UpdatedByID: currentUser(r).ID}
	if req.Title != nil {
		title, err := validateProjectContextTitle(*req.Title)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Title = &title
	}
	if req.Body != nil {
		body, err := validateProjectContextBody(*req.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		params.Body = &body
	}
	updated, err := s.store.UpdateWhiteboardPage(r.Context(), params)
	if err != nil {
		writeStoreError(w, err) // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	page, ok := s.whiteboardPageFromRoute(w, r, project)
	if !ok {
		return
	}
	if err := s.store.DeleteWhiteboardPage(r.Context(), page.ID); err != nil {
		writeStoreError(w, err) // defensive: the project or page was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// whiteboardPageFromRoute resolves {pageRef} within an already-authorized
// project, so callers check access before a page's existence is revealed.
func (s *Server) whiteboardPageFromRoute(w http.ResponseWriter, r *http.Request, project model.Project) (model.WhiteboardPage, bool) {
	number, ok := parseTypedRefParam(w, r, "pageRef", "whiteboard")
	if !ok {
		return model.WhiteboardPage{}, false
	}
	page, err := s.store.GetWhiteboardPageByProjectNumber(r.Context(), project.ID, number)
	if err != nil {
		writeStoreError(w, err)
		return model.WhiteboardPage{}, false
	}
	return page, true
}

func whiteboardPagesNextCursor(pages []model.WhiteboardPageSummary, hasMore bool) *string {
	if !hasMore {
		return nil
	}
	last := pages[len(pages)-1]
	next := encodeCursor(store.WhiteboardPagesCursor{UpdatedAt: last.UpdatedAt, ID: last.ID})
	return &next
}
