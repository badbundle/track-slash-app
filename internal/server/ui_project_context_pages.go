package server

import (
	"context"
	"errors"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
	"strings"
)

func (s *Server) uiProjectContextPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	s.renderUIProjectContextManager(w, r, project.ID, nil)
}

func (s *Server) uiViewProjectContext(w http.ResponseWriter, r *http.Request) {
	project, contextItem, ok := s.uiProjectContextFromRoute(w, r)
	if !ok {
		return
	}
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "view"
		panel.ActiveContextID = contextItem.ID
	})
}

func (s *Server) uiNewProjectContext(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "create"
	})
}

func (s *Server) uiCreateProjectContext(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}

	var params store.CreateProjectContextParams
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxProjectContextUploadBytes+1024*1024)
		upload, err := readProjectContextUpload(r)
		if err != nil {
			s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
				panel.Action = "create"
				panel.ContextUploadError = err.Error()
			})
			return
		}
		params = store.CreateProjectContextParams{
			ProjectID:      project.ID,
			Title:          upload.Title,
			Kind:           model.ProjectContextKindText,
			ContentType:    upload.ContentType,
			Body:           upload.Body,
			SourceFilename: upload.SourceFilename,
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "unable to read form", http.StatusBadRequest)
			return
		}
		titleInput := r.Form.Get("title")
		bodyInput := r.Form.Get("body")
		title, err := validateProjectContextTitle(titleInput)
		if err != nil {
			s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
				panel.Action = "create"
				panel.ContextTitle = titleInput
				panel.ContextBody = bodyInput
				panel.ContextError = err.Error()
			})
			return
		}
		body, err := validateProjectContextBody(bodyInput)
		if err != nil {
			s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
				panel.Action = "create"
				panel.ContextTitle = titleInput
				panel.ContextBody = bodyInput
				panel.ContextError = err.Error()
			})
			return
		}
		params = store.CreateProjectContextParams{
			ProjectID:   project.ID,
			Title:       title,
			Kind:        model.ProjectContextKindText,
			ContentType: "text/markdown; charset=utf-8",
			Body:        body,
		}
	}
	params.CreatedByID = currentUser(r).ID
	created, err := s.store.CreateProjectContext(r.Context(), params)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	uiSetHXReplaceURL(w, r, uiProjectContextPath(project, created))
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "view"
		panel.ActiveContextID = created.ID
	})
}

func (s *Server) uiEditProjectContext(w http.ResponseWriter, r *http.Request) {
	project, contextItem, ok := s.uiProjectContextFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "edit"
		panel.ActiveContextID = contextItem.ID
		panel.ActiveContext = contextItem
		panel.ContextEditTitle = contextItem.Title
		panel.ContextEditBody = contextItem.Body
	})
}

func (s *Server) uiUpdateProjectContext(w http.ResponseWriter, r *http.Request) {
	project, contextItem, ok := s.uiProjectContextFromRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	titleInput := r.Form.Get("title")
	bodyInput := r.Form.Get("body")
	title, err := validateProjectContextTitle(titleInput)
	if err != nil {
		s.renderUIProjectContextEditError(w, r, project.ID, contextItem.ID, titleInput, bodyInput, err.Error())
		return
	}
	body, err := validateProjectContextBody(bodyInput)
	if err != nil {
		s.renderUIProjectContextEditError(w, r, project.ID, contextItem.ID, titleInput, bodyInput, err.Error())
		return
	}
	if _, err := s.store.UpdateProjectContext(r.Context(), store.UpdateProjectContextParams{
		ID:          contextItem.ID,
		Title:       &title,
		Body:        &body,
		UpdatedByID: currentUser(r).ID,
	}); err != nil {
		writeUIStoreError(w, err)
		return
	}
	uiSetHXReplaceURL(w, r, uiProjectContextPath(project, contextItem))
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "view"
		panel.ActiveContextID = contextItem.ID
	})
}

func (s *Server) uiDeleteProjectContext(w http.ResponseWriter, r *http.Request) {
	project, contextItem, ok := s.uiProjectContextFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := s.deleteProjectContextAndObjects(r.Context(), contextItem); err != nil {
		writeUIStoreError(w, err)
		return
	}
	uiSetHXReplaceURL(w, r, uiProjectContextsPath(project))
	s.renderUIProjectContextManager(w, r, project.ID, nil)
}

func (s *Server) uiMoveProjectContextUp(w http.ResponseWriter, r *http.Request) {
	s.uiMoveProjectContext(w, r, -1)
}

func (s *Server) uiMoveProjectContextDown(w http.ResponseWriter, r *http.Request) {
	s.uiMoveProjectContext(w, r, 1)
}

func (s *Server) uiMoveProjectContext(w http.ResponseWriter, r *http.Request, delta int64) {
	project, contextItem, ok := s.uiProjectContextFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if contextItem.Position != nil {
		target := *contextItem.Position + delta
		if target >= 1 {
			contexts, _, err := s.store.ListProjectContexts(r.Context(), store.ListProjectContextsParams{ProjectID: project.ID, Limit: MaxLimit})
			if err != nil {
				writeUIStoreError(w, err)
				return
			}
			if target <= int64(len(contexts)) {
				if _, err := s.store.UpdateProjectContext(r.Context(), store.UpdateProjectContextParams{
					ID: contextItem.ID, Position: &target, UpdatedByID: currentUser(r).ID,
				}); err != nil {
					writeUIStoreError(w, err)
					return
				}
			}
		}
	}
	uiSetHXReplaceURL(w, r, uiProjectContextPath(project, contextItem))
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "view"
		panel.ActiveContextID = contextItem.ID
	})
}

func (s *Server) uiNewProjectContextIssueLink(w http.ResponseWriter, r *http.Request) {
	project, contextItem, ok := s.uiProjectContextFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "link"
		panel.ActiveContextID = contextItem.ID
		panel.ActiveContext = contextItem
	})
}

func (s *Server) uiCreateProjectContextIssueLink(w http.ResponseWriter, r *http.Request) {
	project, contextItem, ok := s.uiProjectContextFromRoute(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unable to read form", http.StatusBadRequest)
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	input := strings.TrimSpace(r.Form.Get("issue"))
	issue, message, err := s.uiProjectContextIssueInput(r.Context(), project, input)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if message != "" {
		s.renderUIProjectContextLinkError(w, r, project.ID, contextItem.ID, input, message)
		return
	}
	if _, err := s.store.CreateIssueContextLink(r.Context(), issue.ID, contextItem.ID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.renderUIProjectContextLinkError(w, r, project.ID, contextItem.ID, input, "Issue already linked.")
			return
		}
		writeUIStoreError(w, err)
		return
	}
	uiSetHXReplaceURL(w, r, uiProjectContextPath(project, contextItem))
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "link"
		panel.ActiveContextID = contextItem.ID
	})
}

func (s *Server) uiDeleteProjectContextIssueLink(w http.ResponseWriter, r *http.Request) {
	project, contextItem, ok := s.uiProjectContextFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	ref, err := parseIssueRef(chi.URLParam(r, "issueRef"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(r.Context(), project.OwnerUsername, ref.ProjectKey, ref.Number)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if issue.ProjectID != project.ID {
		writeUIStoreError(w, store.ErrNotFound)
		return
	}
	if err := s.store.DeleteIssueContextLink(r.Context(), issue.ID, contextItem.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	uiSetHXReplaceURL(w, r, uiProjectContextPath(project, contextItem))
	s.renderUIProjectContextManager(w, r, project.ID, func(panel *uiContextManagerData) {
		panel.Action = "link"
		panel.ActiveContextID = contextItem.ID
	})
}

func (s *Server) renderUIProjectContextManager(w http.ResponseWriter, r *http.Request, projectID uuid.UUID, mutate func(*uiContextManagerData)) {
	panel, err := s.uiBuildProjectContextManager(r.Context(), r, projectID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if mutate != nil {
		mutate(panel)
	}
	if err := s.uiHydrateProjectContextManager(r.Context(), panel); err != nil {
		writeUIStoreError(w, err)
		return
	}
	favorite, err := s.store.IsProjectFavorite(r.Context(), currentUser(r).ID, projectID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	projectPanel := &uiProjectPanelData{
		CSRFToken: uiSessionCSRFToken(r),
		Project:   panel.Project, View: "context", Favorite: favorite,
		ProjectTabs: uiProjectTabs(panel.Project, "context", nil), ContextManager: panel,
	}
	uiApplyProjectHeaderAccess(projectPanel, currentUser(r), panel.permissions)
	if isHTMXRequest(r) {
		renderUITemplate(w, http.StatusOK, "project-panel", projectPanel)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui context visible projects", err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User: currentUser(r), Projects: projects,
		SidebarActive: uiSidebarState{View: "project", ProjectID: panel.Project.ID}, ProjectPanel: projectPanel,
	})
}

func (s *Server) renderUIIssueContextManager(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, mutate func(*uiContextManagerData)) {
	panel, err := s.uiBuildIssueContextManager(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if mutate != nil {
		mutate(panel)
	}
	if panel.ActiveContextID == uuid.Nil && panel.Action == "" && len(panel.Items) > 0 {
		panel.Action = "view"
		panel.ActiveContextID = panel.Items[0].ID
	}
	if panel.ActiveContextID != uuid.Nil {
		contextItem, err := s.store.GetProjectContext(r.Context(), panel.ActiveContextID)
		if err != nil {
			writeUIStoreError(w, err)
			return
		}
		panel.ActiveContext = contextItem
		panel.HasActiveContext = true
		if contextItem.Scope == model.ProjectContextScopeProject {
			attachments, hasMore, err := s.store.ListContextAttachments(r.Context(), store.ListContextAttachmentsParams{ContextID: contextItem.ID, Limit: MaxLimit})
			if err != nil {
				writeUIStoreError(w, err)
				return
			}
			panel.Attachments = attachments
			panel.AttachmentsHasMore = hasMore
			if contextItem.ContentType == "text/markdown; charset=utf-8" {
				panel.ActiveHTML = renderProjectContextMarkdown(panel.Project, contextItem, attachments)
			}
		}
	}
	s.renderUIContextManager(w, r, panel)
}

func (s *Server) renderUIContextManager(w http.ResponseWriter, r *http.Request, panel *uiContextManagerData) {
	if isHTMXRequest(r) {
		renderUITemplate(w, http.StatusOK, "context-manager-panel", panel)
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui context manager visible projects", err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:           currentUser(r),
		Projects:       projects,
		SidebarActive:  uiSidebarState{View: "project", ProjectID: panel.Project.ID},
		ContextManager: panel,
	})
}

func (s *Server) uiBuildProjectContextManager(ctx context.Context, r *http.Request, projectID uuid.UUID) (*uiContextManagerData, error) {
	if err := s.uiRequireProjectAccess(ctx, currentUser(r), projectID); err != nil {
		return nil, err
	}
	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	permissions, err := s.uiProjectPermissions(ctx, currentUser(r), projectID)
	if err != nil {
		return nil, err
	}
	contexts, hasMore, err := s.store.ListProjectContexts(ctx, store.ListProjectContextsParams{
		ProjectID: projectID,
		Limit:     MaxLimit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]uiContextManagerItem, 0, len(contexts))
	for _, contextItem := range contexts {
		items = append(items, uiContextManagerItemFromSummary(contextItem))
	}
	return &uiContextManagerData{
		CSRFToken:   uiSessionCSRFToken(r),
		Mode:        "project",
		Project:     project,
		CanWrite:    permissions.CanWrite,
		Items:       items,
		HasMore:     hasMore,
		permissions: permissions,
	}, nil
}

func (s *Server) uiHydrateProjectContextManager(ctx context.Context, panel *uiContextManagerData) error {
	if panel.ActiveContextID == uuid.Nil && len(panel.Items) > 0 {
		panel.ActiveContextID = panel.Items[0].ID
		if panel.Action == "" {
			panel.Action = "view"
		}
	}
	if panel.ActiveContextID == uuid.Nil {
		return nil
	}
	contextItem, err := s.store.GetProjectContext(ctx, panel.ActiveContextID)
	if err != nil {
		return err
	}
	if contextItem.ProjectID != panel.Project.ID || contextItem.Scope != model.ProjectContextScopeProject {
		return store.ErrNotFound
	}
	var issueCursor *store.IssuesCursor
	for {
		issues, hasMore, err := s.store.ListIssuesForContext(ctx, store.ListIssuesForContextParams{
			ContextID: contextItem.ID,
			Cursor:    issueCursor,
			Limit:     MaxLimit,
		})
		if err != nil {
			return err // defensive: context was validated above; remaining failures require a DB outage or concurrent delete
		}
		panel.LinkedIssues = append(panel.LinkedIssues, issues...)
		if !hasMore {
			break
		}
		issueCursor = &store.IssuesCursor{Number: issues[len(issues)-1].Number}
	}
	panel.LinkedIssueCount = len(panel.LinkedIssues)
	attachments, hasMore, err := s.store.ListContextAttachments(ctx, store.ListContextAttachmentsParams{
		ContextID: contextItem.ID, Limit: MaxLimit,
	})
	if err != nil {
		return err
	}
	panel.ActiveContext = contextItem
	panel.HasActiveContext = true
	panel.Attachments = attachments
	panel.AttachmentsHasMore = hasMore
	if contextItem.ContentType == "text/markdown; charset=utf-8" {
		panel.ActiveHTML = renderProjectContextMarkdown(panel.Project, contextItem, attachments)
	} else {
		panel.ActiveHTML = ""
	}
	return nil
}

func (s *Server) uiBuildIssueContextManager(ctx context.Context, r *http.Request, issueID uuid.UUID) (*uiContextManagerData, error) {
	projectID, err := s.store.ProjectIDForIssue(ctx, issueID)
	if err != nil {
		return nil, err
	}
	if err := s.uiRequireProjectAccess(ctx, currentUser(r), projectID); err != nil {
		return nil, err
	}
	issue, err := s.store.GetIssue(ctx, issueID)
	if err != nil {
		return nil, err
	}
	var parentIssue *model.Issue
	if issue.ParentIssueID != nil {
		parent, err := s.store.GetIssue(ctx, *issue.ParentIssueID)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		} else {
			parentIssue = &parent
		}
	}
	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	permissions, err := s.uiProjectPermissions(ctx, currentUser(r), projectID)
	if err != nil {
		return nil, err
	}
	contexts, hasMore, err := s.store.ListContextsForIssue(ctx, store.ListContextsForIssueParams{
		IssueID: issueID,
		Limit:   MaxLimit,
	})
	if err != nil {
		return nil, err
	}
	contextSummaries, _, err := s.store.ListProjectContexts(ctx, store.ListProjectContextsParams{
		ProjectID: projectID,
		Limit:     MaxLimit,
	})
	if err != nil {
		return nil, err
	}
	contextOptions := make([]uiProjectContextOption, 0, len(contextSummaries))
	for _, contextItem := range contextSummaries {
		contextOptions = append(contextOptions, uiProjectContextOption{
			Value: contextItem.Title,
			Label: uiProjectContextOptionLabel(contextItem),
		})
	}
	items := make([]uiContextManagerItem, 0, len(contexts))
	for _, contextItem := range contexts {
		items = append(items, uiContextManagerItemFromContext(contextItem))
	}
	return &uiContextManagerData{
		CSRFToken:      uiSessionCSRFToken(r),
		Mode:           "issue",
		Project:        project,
		Issue:          issue,
		ParentIssue:    parentIssue,
		HasIssue:       true,
		CanWrite:       permissions.CanWrite,
		OwnerCrumb:     currentUser(r).ID != project.OwnerID,
		Items:          items,
		HasMore:        hasMore,
		ContextOptions: contextOptions,
	}, nil
}

func uiContextManagerItemFromSummary(contextItem model.ProjectContextSummary) uiContextManagerItem {
	return uiContextManagerItem{
		ID:             contextItem.ID,
		Ref:            contextItem.Ref,
		Number:         contextItem.Number,
		Scope:          contextItem.Scope,
		Position:       contextItem.Position,
		Title:          contextItem.Title,
		ContentType:    contextItem.ContentType,
		SourceFilename: contextItem.SourceFilename,
		UpdatedAt:      contextItem.UpdatedAt,
	}
}

func uiContextManagerItemFromContext(contextItem model.ProjectContext) uiContextManagerItem {
	return uiContextManagerItem{
		ID:             contextItem.ID,
		Ref:            contextItem.Ref,
		Number:         contextItem.Number,
		Scope:          contextItem.Scope,
		Position:       contextItem.Position,
		Title:          contextItem.Title,
		ContentType:    contextItem.ContentType,
		SourceFilename: contextItem.SourceFilename,
		UpdatedAt:      contextItem.UpdatedAt,
	}
}

func uiProjectContextOptionLabel(contextItem model.ProjectContextSummary) string {
	if contextItem.SourceFilename != nil && strings.TrimSpace(*contextItem.SourceFilename) != "" {
		return *contextItem.SourceFilename
	}
	return ""
}

func (s *Server) renderUIProjectContextEditError(w http.ResponseWriter, r *http.Request, projectID, contextID uuid.UUID, title, body, message string) {
	s.renderUIProjectContextManager(w, r, projectID, func(panel *uiContextManagerData) {
		contextItem, err := s.store.GetProjectContext(r.Context(), contextID)
		if err == nil {
			panel.ActiveContext = contextItem
		}
		panel.Action = "edit"
		panel.ActiveContextID = contextID
		panel.ContextEditTitle = title
		panel.ContextEditBody = body
		panel.ContextEditError = message
	})
}

func (s *Server) renderUIProjectContextLinkError(w http.ResponseWriter, r *http.Request, projectID, contextID uuid.UUID, input, message string) {
	s.renderUIProjectContextManager(w, r, projectID, func(panel *uiContextManagerData) {
		contextItem, err := s.store.GetProjectContext(r.Context(), contextID)
		if err == nil {
			panel.ActiveContext = contextItem
		}
		panel.Action = "link"
		panel.ActiveContextID = contextID
		panel.LinkIssueInput = input
		panel.LinkIssueError = message
	})
}

func (s *Server) uiProjectContextIssueInput(ctx context.Context, project model.Project, raw string) (model.Issue, string, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return model.Issue{}, "Issue required.", nil
	}
	ref, err := parseIssueRef(input)
	if err != nil {
		return model.Issue{}, "Choose an issue in this project.", nil
	}
	if err := requireIssueRefProject(ref, project.Key); err != nil {
		return model.Issue{}, "Issue must be in this project.", nil
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(ctx, project.OwnerUsername, ref.ProjectKey, ref.Number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.Issue{}, "Issue not found.", nil
		}
		return model.Issue{}, "", err
	}
	if issue.ProjectID != project.ID {
		return model.Issue{}, "Issue must be in this project.", nil
	}
	return issue, "", nil
}
