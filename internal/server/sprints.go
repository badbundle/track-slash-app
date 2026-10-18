package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

const dateLayout = "2006-01-02"

type createSprintReq struct {
	Name      string  `json:"name"`
	Goal      string  `json:"goal"`
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
}

type updateSprintReq struct {
	Name       *string             `json:"name,omitempty"`
	Goal       *string             `json:"goal,omitempty"`
	StartDate  *string             `json:"start_date,omitempty"`
	EndDate    *string             `json:"end_date,omitempty"`
	ClearDates bool                `json:"clear_dates,omitempty"`
	Status     *model.SprintStatus `json:"status,omitempty"`
}

type reorderPlannedSprintsReq struct {
	SprintRefs []string `json:"sprint_refs"`
}

func (s *Server) createSprint(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}

	var req createSprintReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "name max 200 chars")
		return
	}
	if len(req.Goal) > 2000 {
		writeError(w, http.StatusBadRequest, "goal max 2000 chars")
		return
	}

	start, end, message := parseCreateSprintDateRange(req.StartDate, req.EndDate)
	if message != "" {
		writeError(w, http.StatusBadRequest, message)
		return
	}

	sp, err := s.store.CreateSprint(r.Context(), store.CreateSprintParams{
		ProjectID: project.ID,
		Name:      req.Name,
		Goal:      req.Goal,
		StartDate: start,
		EndDate:   end,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

func (s *Server) listProjectSprints(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}

	statusFilter := model.SprintStatus(r.URL.Query().Get("status"))
	if statusFilter != "" && !statusFilter.Valid() {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	sortBy, err := parseSprintListSort(r.URL.Query().Get("sort"), statusFilter)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var cursor *store.SprintsCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.SprintsCursor
		if err := decodeCursor(raw, &c); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cursor = &c
	}

	out, hasMore, err := s.store.ListSprints(r.Context(), store.ListSprintsParams{
		ProjectID: project.ID,
		Status:    statusFilter,
		Sort:      sortBy,
		Cursor:    cursor,
		Limit:     limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var next *string
	if hasMore {
		enc := encodeCursor(sprintListCursor(out[len(out)-1], statusFilter, sortBy))
		next = &enc
	}
	writePage(w, out, next)
}

func parseSprintListSort(raw string, status model.SprintStatus) (store.ListSprintsSort, error) {
	sortBy := store.ListSprintsSort(strings.TrimSpace(raw))
	switch sortBy {
	case store.ListSprintsSortStartDate:
		return sortBy, nil
	case store.ListSprintsSortCompleted:
		if status != model.SprintStatusCompleted {
			return "", fmt.Errorf("sort=completed requires status=completed")
		}
		return sortBy, nil
	default:
		return "", fmt.Errorf("invalid sort")
	}
}

func sprintListCursor(sprint model.Sprint, status model.SprintStatus, sortBy store.ListSprintsSort) store.SprintsCursor {
	cursor := store.SprintsCursor{
		StartDate: sprint.StartDate,
		CreatedAt: sprint.CreatedAt,
		ID:        sprint.ID,
	}
	if sortBy == store.ListSprintsSortCompleted {
		cursor.CompletedAt = sprint.CompletedAt
	} else if status == model.SprintStatusPlanned && sprint.PlannedOrder != nil {
		cursor.PlannedOrder = *sprint.PlannedOrder
	}
	return cursor
}

func sprintHistoryIssueCursor(issue model.Issue) store.IssuesCursor {
	return store.IssuesCursor{Number: issue.Number}
}

func (s *Server) reorderPlannedSprints(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}

	var req reorderPlannedSprintsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sprintIDs := make([]uuid.UUID, 0, len(req.SprintRefs))
	for _, ref := range req.SprintRefs {
		number, err := parseTypedRef(ref, "sprint")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sprint, err := s.store.GetSprintByProjectNumber(r.Context(), project.ID, number)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		sprintIDs = append(sprintIDs, sprint.ID)
	}

	out, err := s.store.ReorderPlannedSprints(r.Context(), store.ReorderPlannedSprintsParams{
		ProjectID: project.ID,
		SprintIDs: sprintIDs,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.sprintFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	writeJSON(w, http.StatusOK, sprint)
}

func (s *Server) listSprintHistoryIssues(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.sprintFromRoute(w, r)
	if !ok {
		return
	}
	permissions, ok := s.requireProjectReadPermissions(w, r, project.ID)
	if !ok {
		return
	}
	if sprint.Status != model.SprintStatusCompleted {
		writeError(w, http.StatusConflict, "sprint must be completed")
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var cursor *store.IssuesCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.IssuesCursor
		if err := decodeCursor(raw, &c); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cursor = &c
	}
	issues, hasMore, err := s.store.ListSprintSnapshotIssues(r.Context(), store.ListSprintSnapshotIssuesParams{
		ProjectID:      project.ID,
		SprintID:       sprint.ID,
		Cursor:         cursor,
		Limit:          limit,
		IncludePrivate: permissions.CanReadMembersOnly,
		IncludeDeleted: permissions.CanReadMembersOnly,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var next *string
	if hasMore {
		encoded := encodeCursor(sprintHistoryIssueCursor(issues[len(issues)-1]))
		next = &encoded
	}
	writePage(w, issues, next)
}

func (s *Server) updateSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.sprintFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	var req updateSprintReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	params := store.UpdateSprintParams{}

	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if len(n) > 200 {
			writeError(w, http.StatusBadRequest, "name max 200 chars")
			return
		}
		params.Name = &n
	}
	if req.Goal != nil {
		if len(*req.Goal) > 2000 {
			writeError(w, http.StatusBadRequest, "goal max 2000 chars")
			return
		}
		params.Goal = req.Goal
	}
	if req.StartDate != nil {
		d, err := time.Parse(dateLayout, *req.StartDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "start_date must be YYYY-MM-DD")
			return
		}
		params.StartDate = &d
	}
	if req.EndDate != nil {
		d, err := time.Parse(dateLayout, *req.EndDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "end_date must be YYYY-MM-DD")
			return
		}
		params.EndDate = &d
	}
	params.ClearDates = req.ClearDates
	if req.Status != nil {
		if !req.Status.Valid() {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		if *req.Status == model.SprintStatusCompleted {
			writeError(w, http.StatusBadRequest, "use POST /sprints/sprint-N/complete")
			return
		}
		params.Status = req.Status
	}

	sp, err := s.store.UpdateSprint(r.Context(), sprint.ID, params)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

func parseCreateSprintDateRange(startInput, endInput *string) (*time.Time, *time.Time, string) {
	if startInput == nil && endInput == nil {
		return nil, nil, ""
	}
	if startInput == nil || endInput == nil {
		return nil, nil, "start_date and end_date must be provided together"
	}
	start, err := time.Parse(dateLayout, *startInput)
	if err != nil {
		return nil, nil, "start_date must be YYYY-MM-DD"
	}
	end, err := time.Parse(dateLayout, *endInput)
	if err != nil {
		return nil, nil, "end_date must be YYYY-MM-DD"
	}
	if end.Before(start) {
		return nil, nil, "end_date must be on or after start_date"
	}
	return &start, &end, ""
}

func (s *Server) completeSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.sprintFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	sp, err := s.store.CompleteSprint(r.Context(), sprint.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

func (s *Server) deleteSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.sprintFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	if err := s.store.DeleteSprint(r.Context(), sprint.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sprintFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, model.Sprint, bool) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return model.Project{}, model.Sprint{}, false
	}
	number, ok := parseTypedRefParam(w, r, "sprintRef", "sprint")
	if !ok {
		return model.Project{}, model.Sprint{}, false
	}
	sprint, err := s.store.GetSprintByProjectNumber(r.Context(), project.ID, number)
	if err != nil {
		writeStoreError(w, err)
		return model.Project{}, model.Sprint{}, false
	}
	return project, sprint, true
}
