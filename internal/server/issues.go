package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type createIssueReq struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Priority    *model.IssuePriority `json:"priority,omitempty"`
	Worker      *model.IssueWorker   `json:"worker,omitempty"`
	Private     bool                 `json:"private,omitempty"`
	AssigneeID  *uuid.UUID           `json:"assignee_id,omitempty"`
	ReporterID  *uuid.UUID           `json:"reporter_id,omitempty"`
	DueDate     *model.Date          `json:"due_date,omitempty"`
	issueRepeatInput
}

func (s *Server) createIssue(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectIssueCreation(w, r, project.ID) {
		return
	}
	permissions, err := s.store.ProjectPermissionsForUser(r.Context(), currentUser(r), project.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var req createIssueReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 200 {
		writeError(w, http.StatusBadRequest, "title required, max 200 chars")
		return
	}
	priority := model.PriorityP2
	if req.Priority != nil {
		if !req.Priority.Valid() {
			writeError(w, http.StatusBadRequest, "invalid priority")
			return
		}
		priority = *req.Priority
	}
	if req.Worker != nil && !req.Worker.Valid() {
		writeError(w, http.StatusBadRequest, "invalid worker")
		return
	}
	// Assignee, worker and repeat are triage decisions for members.
	if !permissions.CanWrite && (req.AssigneeID != nil || req.Worker != nil || req.issueRepeatInput.given()) {
		writeForbidden(w)
		return
	}
	repeat, err := req.issueRepeatInput.setting(currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reporter := !permissions.CanRead && permissions.HelpDeskReporter
	if reporter && (req.Priority != nil || req.DueDate != nil) {
		writeError(w, http.StatusBadRequest, helpDeskIssueFieldsError)
		return
	}
	reporterID := req.ReporterID
	if reporterID == nil {
		id := currentUser(r).ID
		reporterID = &id
	} else if !currentUser(r).IsAdmin && *reporterID != currentUser(r).ID {
		writeForbidden(w)
		return
	}

	iss, err := s.store.CreateIssue(r.Context(), store.CreateIssueParams{
		ProjectID:   project.ID,
		Title:       req.Title,
		Description: req.Description,
		Priority:    priority,
		Worker:      req.Worker,
		Private:     req.Private,
		AssigneeID:  req.AssigneeID,
		ReporterID:  reporterID,
		DueDate:     req.DueDate,
		Repeat:      repeat,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Someone outside the project follows a private issue they filed as its
	// reporter, as a help-desk reporter does.
	if reporter || permissions.HidesIssue(iss) {
		writeJSON(w, http.StatusCreated, model.NewReporterIssue(iss))
		return
	}
	writeJSON(w, http.StatusCreated, iss)
}

// listReportedIssues is the issue list a help-desk reporter gets: only the
// issues they reported, newest first, as they see them. Filters that would
// need hidden fields are ignored.
func (s *Server) listReportedIssues(w http.ResponseWriter, r *http.Request, project model.Project) {
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
	issues, next, err := s.reportedIssuesPage(r.Context(), project.ID, currentUser(r).ID, cursor, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writePage(w, issues, next)
}

func (s *Server) createSubIssue(w http.ResponseWriter, r *http.Request) {
	parent, ok := s.issueFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, parent.ProjectID) {
		return
	}
	var req createIssueReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 200 {
		writeError(w, http.StatusBadRequest, "title required, max 200 chars")
		return
	}
	priority := model.PriorityP2
	if req.Priority != nil {
		if !req.Priority.Valid() {
			writeError(w, http.StatusBadRequest, "invalid priority")
			return
		}
		priority = *req.Priority
	}
	if req.Worker != nil && !req.Worker.Valid() {
		writeError(w, http.StatusBadRequest, "invalid worker")
		return
	}
	repeat, err := req.issueRepeatInput.setting(currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reporterID := req.ReporterID
	if reporterID == nil {
		id := currentUser(r).ID
		reporterID = &id
	} else if !currentUser(r).IsAdmin && *reporterID != currentUser(r).ID {
		writeForbidden(w)
		return
	}

	iss, err := s.store.CreateSubIssue(r.Context(), store.CreateSubIssueParams{
		ParentIssueID: parent.ID,
		Title:         req.Title,
		Description:   req.Description,
		Priority:      priority,
		Worker:        req.Worker,
		Private:       req.Private,
		AssigneeID:    req.AssigneeID,
		ReporterID:    reporterID,
		DueDate:       req.DueDate,
		Repeat:        repeat,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, iss)
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	permissions, err := s.store.ProjectPermissionsForUser(r.Context(), currentUser(r), project.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !permissions.CanRead && permissions.HelpDeskReporter {
		s.listReportedIssues(w, r, project)
		return
	}
	if !permissions.CanRead {
		writeForbidden(w)
		return
	}

	query, err := parseIssueListQueryValues(r.URL.Query(), issueListQueryOptions{
		DefaultSort:      store.ListIssuesSortNumber,
		IncludeAssignees: true,
		IncludeWorkers:   true,
		AllowNumberSort:  true,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	private, err := parsePrivateFilter(r.URL.Query().Get("private"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params := store.ListIssuesParams{
		ProjectID:      project.ID,
		Statuses:       query.Statuses,
		Priorities:     query.Priorities,
		Workers:        query.Workers,
		UnsetWorker:    query.UnsetWorker,
		AssigneeIDs:    query.AssigneeIDs,
		TagNames:       query.TagNames,
		Cursor:         cursor,
		Limit:          limit,
		Sort:           query.Sort,
		Direction:      query.Direction,
		IncludePrivate: permissions.CanReadMembersOnly,
		Private:        private,
	}

	sprintParam := r.URL.Query().Get("sprint")
	sprintIDParam := r.URL.Query().Get("sprint_id")
	if sprintIDParam != "" {
		writeError(w, http.StatusBadRequest, "use sprint=sprint-N")
		return
	}
	switch {
	case sprintParam == "backlog":
		params.Backlog = true
	case sprintParam != "":
		number, err := parseTypedRef(sprintParam, "sprint")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sprint, err := s.store.GetSprintByProjectNumber(r.Context(), project.ID, number)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		params.SprintID = &sprint.ID
	}

	out, hasMore, err := s.store.ListIssues(r.Context(), params)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var next *string
	if hasMore {
		last := out[len(out)-1]
		enc := encodeCursor(issueListCursor(last, query.Sort))
		next = &enc
	}
	writePage(w, out, next)
}

func (s *Server) listDeletedIssues(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	permissions, ok := s.requireProjectReadPermissions(w, r, project.ID)
	if !ok {
		return
	}
	// Deleted issues are for members, not everyone who can read the project.
	if !permissions.CanReadMembersOnly {
		writeForbidden(w)
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

	out, hasMore, err := s.store.ListDeletedIssues(r.Context(), store.ListDeletedIssuesParams{
		ProjectID: project.ID,
		Cursor:    cursor,
		Limit:     limit,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var next *string
	if hasMore {
		last := out[len(out)-1]
		enc := encodeCursor(store.IssuesCursor{Number: last.Number})
		next = &enc
	}
	writePage(w, out, next)
}

func (s *Server) listSubIssues(w http.ResponseWriter, r *http.Request) {
	parent, permissions, ok := s.issueWithAccessFromRoute(w, r)
	if !ok {
		return
	}
	if !permissions.CanRead {
		writeForbidden(w)
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

	out, hasMore, err := s.store.ListSubIssuesForIssue(r.Context(), store.ListSubIssuesForIssueParams{
		ParentIssueID:  parent.ID,
		Cursor:         cursor,
		Limit:          limit,
		IncludePrivate: permissions.CanReadMembersOnly,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var next *string
	if hasMore {
		last := out[len(out)-1]
		enc := encodeCursor(store.IssuesCursor{Number: last.Number})
		next = &enc
	}
	writePage(w, out, next)
}

// maxBatchIssues caps the number of refs accepted by /{owner}/issues?refs= to keep
// query cost bounded and prevent unbounded URL length abuse.
const maxBatchIssues = 200

func (s *Server) batchIssues(w http.ResponseWriter, r *http.Request) {
	owner, ok := normalizeOwnerParam(w, r)
	if !ok {
		return
	}
	raw := r.URL.Query().Get("refs")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "refs query param required (comma-separated issue refs)")
		return
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxBatchIssues {
		writeError(w, http.StatusBadRequest, "too many refs (max 200)")
		return
	}
	out := make([]model.Issue, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ref, err := parseIssueRef(p)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		key := ref.ProjectKey + "-" + strconv.Itoa(ref.Number)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		iss, err := s.store.GetIssueByOwnerKeyNumber(r.Context(), owner, ref.ProjectKey, ref.Number)
		if err == nil {
			var permissions store.ProjectPermissions
			// A help-desk reporter's view of another reporter's issue is
			// missing, so it is skipped like one, as is a private issue to
			// anyone outside the project. Its own reporter reads it only as
			// a reporter, so it is left out of a batch of full issues.
			permissions, err = s.issueRouteAccess(r.Context(), currentUser(r), iss)
			if err == nil && permissions.PrivateIssueReporter {
				continue
			}
			if err == nil && !permissions.CanRead {
				writeForbidden(w)
				return
			}
		}
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			writeIssueRouteError(w, err)
			return
		}
		out = append(out, iss)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	iss, permissions, ok := s.issueWithAccessFromRoute(w, r)
	if !ok {
		return
	}
	if followsIssue(permissions, currentUser(r), iss) {
		writeJSON(w, http.StatusOK, model.NewReporterIssue(iss))
		return
	}
	if !permissions.CanRead {
		writeForbidden(w)
		return
	}
	writeJSON(w, http.StatusOK, iss)
}

type updateIssueReq struct {
	Title       *string                 `json:"title,omitempty"`
	Description *string                 `json:"description,omitempty"`
	Status      *model.Status           `json:"status,omitempty"`
	CloseReason *model.IssueCloseReason `json:"close_reason,omitempty"`
	Priority    *model.IssuePriority    `json:"priority,omitempty"`
	Worker      *model.IssueWorker      `json:"worker,omitempty"`
	ClearWorker bool                    `json:"clear_worker,omitempty"`
	Private     *bool                   `json:"private,omitempty"`
	// The worker and the fields below are cleared by their clear_ flag, or by
	// sending them as null: decoding can't tell null from a field left out, so
	// updateIssue reads the nulls from the body.
	AssigneeID    *uuid.UUID  `json:"assignee_id,omitempty"`
	ClearAssignee bool        `json:"clear_assignee,omitempty"`
	ReporterID    *uuid.UUID  `json:"reporter_id,omitempty"`
	ClearReporter bool        `json:"clear_reporter,omitempty"`
	Sprint        *string     `json:"sprint,omitempty"`
	ClearSprint   bool        `json:"clear_sprint,omitempty"`
	DueDate       *model.Date `json:"due_date,omitempty"`
	ClearDueDate  bool        `json:"clear_due_date,omitempty"`
	issueRepeatInput
	ClearRepeat bool `json:"clear_repeat,omitempty"`
}

func (s *Server) updateIssue(w http.ResponseWriter, r *http.Request) {
	issue, ok := s.issueFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, issue.ProjectID) {
		return
	}
	var req updateIssueReq
	nulls, err := decodeJSONNulls(r, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" || len(t) > 200 {
			writeError(w, http.StatusBadRequest, "title must be 1..200 chars")
			return
		}
		req.Title = &t
	}
	if req.Status != nil && !req.Status.Valid() {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if req.CloseReason != nil && !req.CloseReason.Valid() {
		writeError(w, http.StatusBadRequest, "invalid close_reason")
		return
	}
	if req.Status != nil && *req.Status == model.StatusClosed && issue.Status != model.StatusClosed && req.CloseReason == nil {
		writeError(w, http.StatusBadRequest, "close_reason required when closing issue")
		return
	}
	effectiveStatus := issue.Status
	if req.Status != nil {
		effectiveStatus = *req.Status
	}
	if req.CloseReason != nil && effectiveStatus != model.StatusClosed {
		writeError(w, http.StatusBadRequest, "close_reason only applies to closed issues")
		return
	}
	if req.Priority != nil && !req.Priority.Valid() {
		writeError(w, http.StatusBadRequest, "invalid priority")
		return
	}
	if req.Worker != nil && !req.Worker.Valid() {
		writeError(w, http.StatusBadRequest, "invalid worker")
		return
	}
	repeat, err := req.issueRepeatInput.setting(currentUser(r).ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var sprintID *uuid.UUID
	if req.Sprint != nil && !req.ClearSprint {
		number, err := parseTypedRef(*req.Sprint, "sprint")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sprint, err := s.store.GetSprintByProjectNumber(r.Context(), issue.ProjectID, number)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		sprintID = &sprint.ID
	}

	iss, err := s.store.UpdateIssue(r.Context(), issue.ID, store.UpdateIssueParams{
		Title:         req.Title,
		Description:   req.Description,
		Status:        req.Status,
		CloseReason:   req.CloseReason,
		Priority:      req.Priority,
		Worker:        req.Worker,
		ClearWorker:   req.ClearWorker || nulls["worker"],
		Private:       req.Private,
		AssigneeID:    req.AssigneeID,
		ClearAssignee: req.ClearAssignee || nulls["assignee_id"],
		ReporterID:    req.ReporterID,
		ClearReporter: req.ClearReporter || nulls["reporter_id"],
		SprintID:      sprintID,
		ClearSprint:   req.ClearSprint || nulls["sprint"],
		DueDate:       req.DueDate,
		ClearDueDate:  req.ClearDueDate || nulls["due_date"],
		Repeat:        repeat,
		ClearRepeat:   req.ClearRepeat || nulls["repeat"],
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, iss)
}

func (s *Server) deleteIssue(w http.ResponseWriter, r *http.Request) {
	issue, ok := s.issueFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, issue.ProjectID) {
		return
	}
	if err := s.store.DeleteIssue(r.Context(), issue.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restoreIssue(w http.ResponseWriter, r *http.Request) {
	issue, ok := s.deletedIssueFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, issue.ProjectID) {
		return
	}
	restored, err := s.store.RestoreIssue(r.Context(), issue.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, restored)
}
