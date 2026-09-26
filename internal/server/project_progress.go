package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

var errCompletionWindow = errors.New("completed_within must be one of 1d, 7d, 14d, 30d")

// parseCompletionWindow reads the completed_within value shared by the HTTP
// API, MCP, and the In progress view. Empty means the default window.
func parseCompletionWindow(raw string) (model.CompletionWindow, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return model.DefaultCompletionWindow, nil
	}
	window := model.CompletionWindow(raw)
	if !window.Valid() {
		return "", errCompletionWindow
	}
	return window, nil
}

// projectProgress loads what a project the caller may read is working on now:
// its top-level issues in progress, highest priority first, and those
// completed (Done or Closed) within the window, most recently completed first.
func (s *Server) projectProgress(ctx context.Context, project model.Project, window model.CompletionWindow, now time.Time) (model.ProjectProgress, error) {
	since := now.Add(-window.Duration()).UTC()
	inProgress, inProgressHasMore, err := s.store.ListIssues(ctx, store.ListIssuesParams{
		ProjectID: project.ID,
		Status:    model.StatusInProgress,
		Sort:      store.ListIssuesSortPriority,
		Limit:     MaxLimit,
	})
	if err != nil {
		return model.ProjectProgress{}, err
	}
	completed, completedHasMore, err := s.store.ListRecentlyCompletedIssues(ctx, store.ListRecentlyCompletedIssuesParams{
		ProjectID: project.ID,
		Since:     since,
		Limit:     MaxLimit,
	})
	if err != nil {
		return model.ProjectProgress{}, err
	}
	return model.ProjectProgress{
		CompletedWithin:          window,
		CompletedSince:           since,
		InProgress:               inProgress,
		InProgressHasMore:        inProgressHasMore,
		RecentlyCompleted:        completed,
		RecentlyCompletedHasMore: completedHasMore,
	}, nil
}

func (s *Server) getProjectProgress(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	window, err := parseCompletionWindow(r.URL.Query().Get("completed_within"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	progress, err := s.projectProgress(r.Context(), project, window, time.Now())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, progress)
}
