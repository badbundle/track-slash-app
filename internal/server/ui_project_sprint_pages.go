package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func (s *Server) uiEditProjectName(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	view := uiProjectActionView(r, "sprint")
	s.renderUIProjectPanel(w, r, project.ID, view, func(panel *uiProjectPanelData) {
		panel.EditProjectName = true
		panel.ProjectNameInput = project.Name
	})
}

func (s *Server) uiUpdateProjectName(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	view := uiProjectActionView(r, "sprint")
	nameInput := r.Form.Get("name")
	name := strings.TrimSpace(nameInput)
	if name == "" || len(name) > 200 {
		s.renderUIProjectPanel(w, r, project.ID, view, func(panel *uiProjectPanelData) {
			panel.EditProjectName = true
			panel.ProjectNameInput = nameInput
			panel.ProjectNameError = "Name required, max 200 chars."
		})
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	updated, err := s.store.UpdateProject(r.Context(), project.ID, store.UpdateProjectParams{Name: &name})
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIProjectPanel(w, r, updated.ID, view, nil)
}

func (s *Server) uiEditProjectDescription(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, "about", func(panel *uiProjectPanelData) {
		panel.EditProjectDescription = true
		panel.ProjectDescriptionInput = project.Description
	})
}

func (s *Server) uiUpdateProjectDescription(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	description := r.Form.Get("description")
	if strings.TrimSpace(description) == "" {
		description = ""
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	updated, err := s.store.UpdateProject(r.Context(), project.ID, store.UpdateProjectParams{Description: &description})
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIProjectPanel(w, r, updated.ID, "about", nil)
}

func (s *Server) uiNewProjectSprint(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, "planned", func(panel *uiProjectPanelData) {
		panel.NewSprint = true
	})
}

func (s *Server) uiCreateProjectSprint(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	form, start, end, message := uiSprintFormFromRequest(r)
	if message != "" {
		form.Error = message
		s.renderUIProjectPanel(w, r, project.ID, "planned", func(panel *uiProjectPanelData) {
			panel.NewSprint = true
			panel.NewSprintForm = form
		})
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if _, err := s.store.CreateSprint(r.Context(), store.CreateSprintParams{
		ProjectID: project.ID,
		Name:      strings.TrimSpace(form.NameInput),
		Goal:      form.GoalInput,
		StartDate: start,
		EndDate:   end,
	}); err != nil {
		form.Error = uiSprintStoreMessage(err)
		s.renderUIProjectPanel(w, r, project.ID, "planned", func(panel *uiProjectPanelData) {
			panel.NewSprint = true
			panel.NewSprintForm = form
		})
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, "planned", nil)
}

func (s *Server) uiEditProjectSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	view := uiProjectSprintView(sprint)
	var attachments []model.SprintAttachment
	var attachmentsHasMore bool
	if sprint.Status == model.SprintStatusPlanned {
		var err error
		attachments, attachmentsHasMore, err = s.store.ListSprintAttachments(r.Context(), store.ListSprintAttachmentsParams{SprintID: sprint.ID, Limit: MaxLimit})
		if err != nil {
			writeUIStoreError(w, err)
			return
		}
	}
	s.renderUIProjectPanel(w, r, project.ID, view, func(panel *uiProjectPanelData) {
		uiMarkSprintEdit(panel, sprint, uiSprintFormFor(sprint))
		if sprint.Status == model.SprintStatusPlanned {
			panel.PlannedSprintAttachments = attachments
			panel.PlannedSprintAttachmentsHasMore = attachmentsHasMore
		}
	})
}

func (s *Server) uiProjectSprintDescription(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	attachments, hasMore, err := s.store.ListSprintAttachments(r.Context(), store.ListSprintAttachmentsParams{
		SprintID: sprint.ID,
		Limit:    MaxLimit,
	})
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	expanded := r.URL.Query().Get("expanded") != "0"
	item := uiSprintDescriptionData{
		Project:             project,
		Sprint:              sprint,
		AttachmentCount:     len(attachments),
		DescriptionHTML:     renderSprintDescriptionMarkdown(project, sprint, attachments),
		DescriptionExpanded: expanded,
	}
	if expanded {
		item.Attachments = attachments
		item.AttachmentsHasMore = hasMore
	}
	renderUITemplate(w, http.StatusOK, "sprint-description", item)
}

func (s *Server) uiUpdateProjectSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	view := uiProjectSprintView(sprint)
	form, start, end, message := uiSprintFormFromRequest(r)
	if message != "" {
		form.Error = message
		s.renderUIProjectPanel(w, r, project.ID, view, func(panel *uiProjectPanelData) {
			uiMarkSprintEdit(panel, sprint, form)
		})
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	name := strings.TrimSpace(form.NameInput)
	goal := form.GoalInput
	updated, err := s.store.UpdateSprint(r.Context(), sprint.ID, store.UpdateSprintParams{
		Name:       &name,
		Goal:       &goal,
		StartDate:  start,
		EndDate:    end,
		ClearDates: start == nil && end == nil,
	})
	if err != nil {
		form.Error = uiSprintStoreMessage(err)
		s.renderUIProjectPanel(w, r, project.ID, view, func(panel *uiProjectPanelData) {
			uiMarkSprintEdit(panel, sprint, form)
		})
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, uiProjectSprintView(updated), nil)
}

func (s *Server) uiActivateProjectSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	status := model.SprintStatusActive
	updated, err := s.store.UpdateSprint(r.Context(), sprint.ID, store.UpdateSprintParams{Status: &status})
	if err != nil {
		form := uiSprintFormFor(sprint)
		form.Error = uiSprintStoreMessage(err)
		s.renderUIProjectPanel(w, r, project.ID, "planned", func(panel *uiProjectPanelData) {
			panel.PlannedSprintActionID = sprint.ID
			panel.PlannedSprintAction = "error"
			panel.PlannedSprintForm = form
		})
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, uiProjectSprintView(updated), nil)
}

func (s *Server) uiCompleteProjectSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if _, err := s.store.CompleteSprint(r.Context(), sprint.ID); err != nil {
		form := uiSprintFormFor(sprint)
		form.Error = uiSprintStoreMessage(err)
		s.renderUIProjectPanel(w, r, project.ID, "sprint", func(panel *uiProjectPanelData) {
			panel.ActiveSprintAction = "error"
			panel.ActiveSprintForm = form
		})
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, "sprint", nil)
}

func (s *Server) uiDeleteProjectSprint(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := s.store.DeleteSprint(r.Context(), sprint.ID); err != nil {
		form := uiSprintFormFor(sprint)
		form.Error = uiSprintStoreMessage(err)
		s.renderUIProjectPanel(w, r, project.ID, "planned", func(panel *uiProjectPanelData) {
			panel.PlannedSprintActionID = sprint.ID
			panel.PlannedSprintAction = "error"
			panel.PlannedSprintForm = form
		})
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, "planned", nil)
}

func (s *Server) uiMoveProjectSprintUp(w http.ResponseWriter, r *http.Request) {
	s.uiMoveProjectSprint(w, r, -1)
}

func (s *Server) uiMoveProjectSprintDown(w http.ResponseWriter, r *http.Request) {
	s.uiMoveProjectSprint(w, r, 1)
}

func (s *Server) uiMoveProjectSprint(w http.ResponseWriter, r *http.Request, delta int) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	planned, err := s.uiAllPlannedSprints(r.Context(), project.ID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	index := -1
	ids := make([]uuid.UUID, 0, len(planned))
	for i, item := range planned {
		ids = append(ids, item.ID)
		if item.ID == sprint.ID {
			index = i
		}
	}
	next := index + delta
	if index >= 0 && next >= 0 && next < len(ids) {
		ids[index], ids[next] = ids[next], ids[index]
		if _, err := s.store.ReorderPlannedSprints(r.Context(), store.ReorderPlannedSprintsParams{
			ProjectID: project.ID,
			SprintIDs: ids,
		}); err != nil {
			writeUIStoreError(w, err)
			return
		}
	}
	s.renderUIProjectPanel(w, r, project.ID, "planned", nil)
}

func (s *Server) uiNewProjectSprintIssue(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	view := uiProjectSprintView(sprint)
	s.renderUIProjectPanel(w, r, project.ID, view, func(panel *uiProjectPanelData) {
		uiMarkSprintIssue(panel, sprint, uiSprintIssueFormData{})
	})
}

func (s *Server) uiAddProjectSprintIssue(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	view := uiProjectSprintView(sprint)
	input := r.Form.Get("issue")
	issue, message, err := s.uiSprintIssueFromInput(r, project, sprint, input)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if message != "" {
		s.renderUIProjectPanel(w, r, project.ID, view, func(panel *uiProjectPanelData) {
			uiMarkSprintIssue(panel, sprint, uiSprintIssueFormData{IssueInput: input, Error: message})
		})
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if _, err := s.store.UpdateIssue(r.Context(), issue.ID, store.UpdateIssueParams{SprintID: &sprint.ID}); err != nil {
		s.renderUIProjectPanel(w, r, project.ID, view, func(panel *uiProjectPanelData) {
			uiMarkSprintIssue(panel, sprint, uiSprintIssueFormData{IssueInput: input, Error: uiSprintIssueStoreMessage(err)})
		})
		return
	}
	s.renderUIProjectPanel(w, r, project.ID, view, nil)
}

func (s *Server) renderUIProjectPanel(w http.ResponseWriter, r *http.Request, projectID uuid.UUID, view string, mutate func(*uiProjectPanelData)) {
	panel, err := s.uiBuildProjectPanel(r.Context(), r, projectID, view)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if mutate != nil {
		mutate(panel)
	}
	if panel.ActiveSprint != nil && panel.ActiveSprintAction == "edit" {
		attachments, attachmentsHasMore, err := s.store.ListSprintAttachments(r.Context(), store.ListSprintAttachmentsParams{
			SprintID: panel.ActiveSprint.ID,
			Limit:    MaxLimit,
		})
		if err != nil {
			writeUIStoreError(w, err)
			return
		}
		panel.ActiveSprintDescription.Attachments = attachments
		panel.ActiveSprintDescription.AttachmentsHasMore = attachmentsHasMore
	}
	renderUITemplate(w, http.StatusOK, "project-panel", panel)
}

func (s *Server) uiProjectSprintFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, model.Sprint, bool) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return model.Project{}, model.Sprint{}, false
	}
	number, err := parseTypedRef(chi.URLParam(r, "sprintRef"), "sprint")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return model.Project{}, model.Sprint{}, false
	}
	sprint, err := s.store.GetSprintByProjectNumber(r.Context(), project.ID, number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.Project{}, model.Sprint{}, false
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return model.Project{}, model.Sprint{}, false
	}
	return project, sprint, true
}

func (s *Server) uiIssueFromProjectSprintRoute(w http.ResponseWriter, r *http.Request, project model.Project) (model.Issue, bool) {
	ref, err := parseIssueRef(chi.URLParam(r, "issueRef"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return model.Issue{}, false
	}
	if err := requireIssueRefProject(ref, project.Key); err != nil {
		writeUIStoreError(w, err)
		return model.Issue{}, false
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(r.Context(), project.OwnerUsername, ref.ProjectKey, ref.Number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.Issue{}, false
	}
	return issue, true
}

func (s *Server) uiSprintIssueFromInput(r *http.Request, project model.Project, sprint model.Sprint, raw string) (model.Issue, string, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return model.Issue{}, "Choose an issue.", nil
	}
	ref, err := parseIssueRef(input)
	if err != nil {
		return model.Issue{}, "Use an issue key like " + project.Key + "-12.", nil
	}
	if err := requireIssueRefProject(ref, project.Key); err != nil {
		return model.Issue{}, "Choose an issue from this project.", nil
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(r.Context(), project.OwnerUsername, ref.ProjectKey, ref.Number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.Issue{}, "Choose an existing issue.", nil
		}
		return model.Issue{}, "", err
	}
	if !uiCanEditIssueSprint(issue) {
		return model.Issue{}, "Only top-level open issues can be scheduled.", nil
	}
	if issue.SprintID != nil && *issue.SprintID == sprint.ID {
		return model.Issue{}, "Issue is already in this sprint.", nil
	}
	return issue, "", nil
}

func uiProjectActionView(r *http.Request, fallback string) string {
	view := strings.TrimSpace(r.URL.Query().Get("view"))
	if view == "" {
		view = strings.TrimSpace(r.Form.Get("view"))
	}
	switch view {
	case "about", "sprint", "planned", "all", "changelog":
		return view
	default:
		return fallback
	}
}

func uiProjectSprintView(sprint model.Sprint) string {
	if sprint.Status == model.SprintStatusActive {
		return "sprint"
	}
	return "planned"
}

func uiSprintFormFor(sprint model.Sprint) uiSprintFormData {
	form := uiSprintFormData{
		NameInput: sprint.Name,
		GoalInput: sprint.Goal,
	}
	if sprint.StartDate != nil {
		form.StartDateInput = sprint.StartDate.Format(dateLayout)
	}
	if sprint.EndDate != nil {
		form.EndDateInput = sprint.EndDate.Format(dateLayout)
	}
	return form
}

func uiSprintFormFromRequest(r *http.Request) (uiSprintFormData, *time.Time, *time.Time, string) {
	form := uiSprintFormData{
		NameInput:      r.Form.Get("name"),
		GoalInput:      r.Form.Get("goal"),
		StartDateInput: r.Form.Get("start_date"),
		EndDateInput:   r.Form.Get("end_date"),
	}
	name := strings.TrimSpace(form.NameInput)
	if len(name) > 200 {
		return form, nil, nil, "Name max 200 chars."
	}
	if len(form.GoalInput) > 2000 {
		return form, nil, nil, "Description max 2000 chars."
	}
	startInput := strings.TrimSpace(form.StartDateInput)
	endInput := strings.TrimSpace(form.EndDateInput)
	if startInput == "" && endInput == "" {
		return form, nil, nil, ""
	}
	if startInput == "" || endInput == "" {
		return form, nil, nil, "Start and end dates must both be set, or both left blank."
	}
	start, err := time.Parse(dateLayout, startInput)
	if err != nil {
		return form, nil, nil, "Start date must be YYYY-MM-DD."
	}
	end, err := time.Parse(dateLayout, endInput)
	if err != nil {
		return form, nil, nil, "End date must be YYYY-MM-DD."
	}
	if end.Before(start) {
		return form, nil, nil, "End date must be on or after start date."
	}
	return form, &start, &end, ""
}

func uiMarkSprintEdit(panel *uiProjectPanelData, sprint model.Sprint, form uiSprintFormData) {
	if sprint.Status == model.SprintStatusActive {
		panel.ActiveSprintAction = "edit"
		panel.ActiveSprintForm = form
		return
	}
	panel.PlannedSprintActionID = sprint.ID
	panel.PlannedSprintAction = "edit"
	panel.PlannedSprintForm = form
}

func uiMarkSprintIssue(panel *uiProjectPanelData, sprint model.Sprint, form uiSprintIssueFormData) {
	if sprint.Status == model.SprintStatusActive {
		panel.ActiveSprintAction = "add-issue"
		panel.ActiveSprintIssueForm = form
		return
	}
	panel.PlannedSprintActionID = sprint.ID
	panel.PlannedSprintAction = "add-issue"
	panel.PlannedSprintIssueForm = form
}

func uiSprintStoreMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrSprintsDisabled):
		return "Sprints are disabled for this project. Enable sprints to start one."
	case errors.Is(err, store.ErrConflict):
		return "Sprint change conflicts with current sprint state."
	case errors.Is(err, store.ErrNotFound):
		return "Sprint not found."
	default:
		return "Unable to save sprint."
	}
}

func uiSprintIssueStoreMessage(err error) string {
	if errors.Is(err, store.ErrConflict) {
		return "Only top-level open issues can be scheduled."
	}
	return "Unable to update sprint issues."
}

func (s *Server) uiAllPlannedSprints(ctx context.Context, projectID uuid.UUID) ([]model.Sprint, error) {
	var out []model.Sprint
	var cursor *store.SprintsCursor
	for {
		page, hasMore, err := s.store.ListSprints(ctx, store.ListSprintsParams{
			ProjectID: projectID,
			Status:    model.SprintStatusPlanned,
			Cursor:    cursor,
			Limit:     MaxLimit,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if !hasMore {
			return out, nil
		}
		last := page[len(page)-1]
		next := store.SprintsCursor{ID: last.ID}
		if last.PlannedOrder != nil {
			next.PlannedOrder = *last.PlannedOrder
		}
		cursor = &next
	}
}
