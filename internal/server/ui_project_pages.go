package server

import (
	"context"
	"fmt"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
	"net/http"
	"strings"
)

func (s *Server) uiProjectPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	uiRedirectPreservingQuery(w, r, uiProjectHomePath(project))
}

// uiRedirectPreservingQuery sends the browser to a canonical view without
// dropping the filter and sort parameters it arrived with, so a bookmarked or
// shared filtered URL does not land on the unfiltered view.
func uiRedirectPreservingQuery(w http.ResponseWriter, r *http.Request, target string) {
	http.Redirect(w, r, uiWithRequestQuery(r, target), http.StatusSeeOther)
}

// uiWithRequestQuery appends the incoming query to target, choosing the
// separator by whether target already carries a query of its own.
func uiWithRequestQuery(r *http.Request, target string) string {
	if r.URL.RawQuery == "" {
		return target
	}
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	return target + separator + r.URL.RawQuery
}

func (s *Server) uiProjectWorkPage(w http.ResponseWriter, r *http.Request, view string) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if resolved := uiProjectViewFor(project, view); resolved != view {
		if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
			writeUIStoreError(w, err)
			return
		}
		uiRedirectPreservingQuery(w, r, uiProjectViewPath(project, resolved))
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui project visible projects", err)
		return
	}
	panel, err := s.uiBuildProjectPanel(r.Context(), r, project.ID, view)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		Projects:      projects,
		SidebarActive: uiSidebarState{View: "project", ProjectID: project.ID},
		ProjectPanel:  panel,
	})
}

func (s *Server) uiProjectWorkPanel(w http.ResponseWriter, r *http.Request, view string) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	panel, err := s.uiBuildProjectPanel(r.Context(), r, project.ID, view)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if panel.View != view {
		// The Sprint board was requested for a project without sprint mode and
		// the panel fell back to the landing view; keep the address bar honest.
		w.Header().Set("HX-Push-Url", uiWithRequestQuery(r, uiProjectViewPath(project, panel.View)))
	}
	renderUITemplate(w, http.StatusOK, "project-panel", panel)
}

func (s *Server) uiToggleProjectFavorite(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeUIStoreError(w, errUIBadRequest)
		return
	}
	favorite, err := s.store.IsProjectFavorite(r.Context(), currentUser(r).ID, project.ID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if favorite {
		err = s.store.UnfavoriteProject(r.Context(), currentUser(r).ID, project.ID)
	} else {
		err = s.store.FavoriteProject(r.Context(), currentUser(r).ID, project.ID)
	}
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	view := uiProjectPanelView(r.Form.Get("view"))
	if !isHTMXRequest(r) {
		http.Redirect(w, r, uiProjectViewPath(project, uiProjectViewFor(project, view)), http.StatusSeeOther)
		return
	}
	favorites, err := s.uiFavoriteProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "project-favorite-toggle-response", uiProjectFavoriteData{
		CSRFToken: uiSessionCSRFToken(r),
		Project:   project,
		View:      view,
		Favorite:  !favorite,
		Sidebar: uiSidebarFavoritesData{
			Projects:        favorites,
			ActiveProjectID: project.ID,
			OOB:             true,
		},
	})
}

func (s *Server) uiProjectAllIssuePage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	pageData, err := s.uiBuildProjectAllIssuePage(r.Context(), r, project)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "project-all-issue-page", pageData)
}

// uiProjectPanelView keeps an action on the project view it was triggered from,
// falling back to the default view when the caller names one that does not exist.
func uiProjectPanelView(raw string) string {
	switch raw {
	case "about", "sprint", "planned", "all", "context", "whiteboard", "sprints", "changelog", "insights", "members":
		return raw
	default:
		return "sprint"
	}
}

func (s *Server) uiProjectLegacyBacklog(w http.ResponseWriter, r *http.Request, panel bool) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	target := uiProjectViewPath(project, "all")
	if panel {
		target = uiProjectPanelPath(project, "all")
	}
	uiRedirectPreservingQuery(w, r, target)
}

func (s *Server) uiProjectDeletedPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	projects, err := s.uiVisibleProjects(r.Context(), currentUser(r))
	if err != nil {
		writeUIInternalError(w, "ui deleted issues visible projects", err)
		return
	}
	panel, err := s.uiBuildDeletedIssuesPanel(r.Context(), r, project.ID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		Projects:      projects,
		SidebarActive: uiSidebarState{View: "project", ProjectID: project.ID},
		DeletedPanel:  panel,
	})
}

func (s *Server) uiProjectDeletedPanel(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	panel, err := s.uiBuildDeletedIssuesPanel(r.Context(), r, project.ID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "deleted-issues-panel", panel)
}

func (s *Server) uiBuildWorkPanel(ctx context.Context, r *http.Request, user model.User, view string) (*uiWorkPanelData, error) {
	projects, err := s.uiVisibleProjects(ctx, user)
	if err != nil {
		return nil, err
	}
	query, err := uiParseIssueListQuery(r)
	if err != nil {
		return nil, err
	}
	query.TagNames = nil
	panel := &uiWorkPanelData{
		View:          view,
		Title:         "Me",
		ProjectCount:  len(projects),
		WorkTabs:      uiWorkTabs(view, query),
		IssueControls: uiWorkIssueControls(view, query),
	}
	switch view {
	case "active":
		panel.Subtitle = "Active sprint issues assigned to you across accessible projects."
		panel.IssueListLabel = "Active sprint issues"
		panel.Issues, panel.HasMore, err = s.uiAssignedActiveSprintIssues(ctx, projects, user.ID, query)
	case "all":
		panel.Subtitle = "Issues assigned to you across accessible projects."
		panel.IssueListLabel = "All assigned issues"
		panel.Issues, panel.HasMore, err = s.uiAssignedIssues(ctx, projects, user.ID, query)
	default:
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return panel, nil
}

func (s *Server) uiBuildProjectsPanel(ctx context.Context, user model.User) (*uiProjectsPanelData, error) {
	var all []model.Project
	var hasMore bool
	var cursor *store.ProjectsCursor
	for {
		projects, more, err := s.store.ListProjects(ctx, store.ListProjectsParams{
			Cursor:        cursor,
			Limit:         MaxLimit,
			VisibleToUser: visibleProjectUser(user),
		})
		if err != nil {
			return nil, err
		}
		all = append(all, projects...)
		hasMore = hasMore || more
		if !more {
			break
		}
		last := projects[len(projects)-1]
		cursor = &store.ProjectsCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	ownerIDs := make([]uuid.UUID, 0, len(all))
	seen := make(map[uuid.UUID]bool, len(all))
	for _, project := range all {
		if !seen[project.OwnerID] {
			seen[project.OwnerID] = true
			ownerIDs = append(ownerIDs, project.OwnerID)
		}
	}
	owners, err := s.store.ListUserProfilesByID(ctx, ownerIDs)
	if err != nil {
		// Defensive: the project listing just succeeded; this needs a DB outage.
		return nil, err
	}
	ownersByID := make(map[uuid.UUID]model.User, len(owners))
	for _, owner := range owners {
		ownersByID[owner.ID] = owner
	}
	return &uiProjectsPanelData{
		Projects: all,
		HasMore:  hasMore,
		Owners:   ownersByID,
	}, nil
}

func (s *Server) uiBuildOwnerProjectsPanel(ctx context.Context, user model.User, username string) (*uiProjectsPanelData, error) {
	// A malformed owner segment is plain user input. Without normalizing first,
	// GetUserByUsername returns a bare validation error that writeUIStoreError
	// cannot map, so the route answered 500 and logged an internal error.
	normalized, err := store.NormalizeUsername(username)
	if err != nil {
		return nil, errUIBadRequest
	}
	owner, err := s.store.GetUserByUsername(ctx, normalized)
	if err != nil {
		return nil, err
	}
	panel, err := s.uiBuildProjectsPanel(ctx, user)
	if err != nil {
		// Defensive: the live owner lookup succeeded; remaining failures require a DB outage.
		return nil, err
	}
	projects := make([]model.Project, 0, len(panel.Projects))
	for _, project := range panel.Projects {
		if project.OwnerID == owner.ID {
			projects = append(projects, project)
		}
	}
	panel.Projects = projects
	panel.Owner = &owner
	return panel, nil
}

func (s *Server) uiAssignedIssues(ctx context.Context, projects []model.Project, userID uuid.UUID, query uiIssueListQuery) ([]uiIssueItem, bool, error) {
	var out []uiIssueItem
	var hasMore bool
	for _, project := range projects {
		issues, more, err := s.store.ListIssues(ctx, store.ListIssuesParams{
			ProjectID:   project.ID,
			Statuses:    query.Statuses,
			Priorities:  query.Priorities,
			AssigneeIDs: []uuid.UUID{userID},
			Limit:       MaxLimit,
			Sort:        query.Sort,
			Direction:   query.Direction,
		})
		if err != nil {
			return nil, false, err
		}
		hasMore = hasMore || more
		items, err := s.uiIssueItemsWithSubIssueProgress(ctx, issues, project, nil)
		if err != nil {
			return nil, false, err
		}
		out = append(out, items...)
	}
	uiSortIssueItems(out, query.Sort, query.Direction)
	return out, hasMore, nil
}

func (s *Server) uiAssignedActiveSprintIssues(ctx context.Context, projects []model.Project, userID uuid.UUID, query uiIssueListQuery) ([]uiIssueItem, bool, error) {
	var out []uiIssueItem
	var hasMore bool
	for _, project := range projects {
		activeSprints, _, err := s.store.ListSprints(ctx, store.ListSprintsParams{
			ProjectID: project.ID,
			Status:    model.SprintStatusActive,
			Limit:     1,
		})
		if err != nil {
			return nil, false, err
		}
		if len(activeSprints) == 0 {
			continue
		}
		sprint := activeSprints[0]
		issues, more, err := s.store.ListIssues(ctx, store.ListIssuesParams{
			ProjectID:   project.ID,
			Statuses:    query.Statuses,
			Priorities:  query.Priorities,
			AssigneeIDs: []uuid.UUID{userID},
			SprintID:    &sprint.ID,
			Limit:       MaxLimit,
			Sort:        query.Sort,
			Direction:   query.Direction,
		})
		if err != nil {
			return nil, false, err
		}
		hasMore = hasMore || more
		items, err := s.uiIssueItemsWithSubIssueProgress(ctx, issues, project, &sprint)
		if err != nil {
			return nil, false, err
		}
		out = append(out, items...)
	}
	uiSortIssueItems(out, query.Sort, query.Direction)
	return out, hasMore, nil
}

func (s *Server) uiIssueItemsWithSubIssueProgress(ctx context.Context, issues []model.Issue, project model.Project, sprint *model.Sprint) ([]uiIssueItem, error) {
	parentIssueIDs := make([]uuid.UUID, 0, len(issues))
	for _, issue := range issues {
		parentIssueIDs = append(parentIssueIDs, issue.ID)
	}
	progressByIssue, err := s.store.ListSubIssueProgress(ctx, parentIssueIDs)
	if err != nil {
		return nil, err
	}
	items := make([]uiIssueItem, 0, len(issues))
	for _, issue := range issues {
		items = append(items, uiIssueItem{
			Issue:            issue,
			Project:          project,
			Sprint:           sprint,
			SubIssueProgress: progressByIssue[issue.ID],
		})
	}
	return items, nil
}

func (s *Server) uiBuildNewIssuePanel(ctx context.Context, r *http.Request, input uiNewIssuePanelData) (*uiNewIssuePanelData, error) {
	input.CSRFToken = uiSessionCSRFToken(r)
	user := currentUser(r)
	projects, err := s.uiIssueCreatableProjects(ctx, user)
	if err != nil {
		return nil, err
	}
	input.ProjectOptions = uiFilterNewIssueProjects(projects, input.ProjectInput)
	if strings.TrimSpace(input.ProjectID) != "" {
		project, ok, message, err := s.uiProjectFromNewIssueSelection(ctx, user, input.ProjectID)
		if err != nil {
			return nil, err
		}
		if ok && input.ProjectInput != "" && !strings.EqualFold(strings.TrimSpace(input.ProjectInput), uiNewIssueProjectLabel(project)) {
			ok = false
			input.ProjectID = ""
		}
		if ok {
			permissions, err := s.uiProjectPermissions(ctx, user, project.ID)
			if err != nil {
				return nil, err
			}
			input.Project = project
			input.HasProject = true
			input.ProjectID = project.ID.String()
			if input.ProjectInput == "" {
				input.ProjectInput = uiNewIssueProjectLabel(project)
			}
			input.PublicSubmission = !permissions.CanWrite
			if permissions.CanWrite {
				members, err := s.store.SearchProjectMembers(ctx, store.SearchProjectMembersParams{
					ProjectID:    project.ID,
					Limit:        MaxLimit,
					WritableOnly: true,
				})
				if err != nil {
					return nil, err
				}
				input.MemberOptions = members
			}
		} else if input.Error == "" {
			input.Error = message
		}
	}
	input.ProjectSearchOpen = input.ProjectInput != "" && !input.HasProject
	if input.BackHref == "" {
		if input.HasProject {
			input.BackHref = uiProjectViewPath(input.Project, "all")
		} else {
			input.BackHref = "/me"
		}
	}
	if input.BackHXGet == "" {
		if input.HasProject {
			input.BackHXGet = uiProjectPanelPath(input.Project, "all")
		} else {
			input.BackHXGet = "/me/panel"
		}
	}
	return &input, nil
}

// uiApplyProjectHeaderAccess sets the permission-dependent fields of the
// project header every project view shares. Views must not fill these by
// hand: one that skipped them lost New issue, Edit name, the Members and
// Delete project actions and the owner crumb, and offered anonymous readers a
// favourite button.
func uiApplyProjectHeaderAccess(panel *uiProjectPanelData, user model.User, permissions store.ProjectPermissions) {
	panel.Anonymous = user.ID == uuid.Nil
	panel.CanWrite = permissions.CanWrite
	panel.CanCreateIssues = permissions.CanCreateIssues
	panel.PublicIssueCreationEnabled = permissions.IsPublic && permissions.PublicIssueCreation && !permissions.IsBlocked
	panel.CanManageMembers = permissions.CanManageMembers
	panel.CanDeleteProject = permissions.CanDelete
	panel.OwnerCrumb = user.ID != panel.Project.OwnerID
}

func (s *Server) uiBuildProjectPanel(ctx context.Context, r *http.Request, projectID uuid.UUID, view string) (*uiProjectPanelData, error) {
	if err := s.uiRequireProjectAccess(ctx, currentUser(r), projectID); err != nil {
		return nil, err
	}
	permissions, err := s.uiProjectPermissions(ctx, currentUser(r), projectID)
	if err != nil {
		return nil, err
	}
	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	view = uiProjectViewFor(project, view)
	favorite := false
	if currentUser(r).ID != uuid.Nil {
		favorite, err = s.store.IsProjectFavorite(ctx, currentUser(r).ID, projectID)
		if err != nil {
			return nil, err
		}
	}
	deleteNotice, err := s.uiDeletedIssueNotice(ctx, r, project.OwnerUsername, projectID)
	if err != nil {
		return nil, err
	}
	var assigneeIDs []uuid.UUID
	var sprintQuery uiIssueListQuery
	var allQuery uiProjectAllQuery
	var assignees []model.ProjectAssignee
	var tags []model.IssueTag
	if view == "sprint" {
		sprintQuery, err = uiParseProjectSprintQuery(r)
		if err != nil {
			return nil, err
		}
		assigneeIDs = sprintQuery.AssigneeIDs
	} else if view == "all" {
		allQuery, err = uiParseProjectAllQuery(r)
		if err != nil {
			return nil, err
		}
		assigneeIDs = allQuery.AssigneeIDs
	}

	panel := &uiProjectPanelData{
		CSRFToken:            uiSessionCSRFToken(r),
		Project:              project,
		View:                 view,
		Favorite:             favorite,
		ProjectTabs:          uiProjectTabs(project, view, assigneeIDs),
		AssigneeFilterActive: len(assigneeIDs) > 0,
		ClearAssigneeHref:    uiProjectViewPath(project, view),
		ClearAssigneeHXGet:   uiProjectPanelPath(project, view),
		ClearAssigneeHXPush:  uiProjectViewPath(project, view),
		DeleteNotice:         deleteNotice,
	}
	uiApplyProjectHeaderAccess(panel, currentUser(r), permissions)
	// The permissions lookup already read both access columns, so About and
	// Members render them without a second round trip.
	panel.AccessSettings = model.ProjectAccessSettings{
		IsPublic:            permissions.IsPublic,
		PublicIssueCreation: permissions.PublicIssueCreation,
	}
	if view == "all" {
		assignees, err = s.store.ListProjectAssignees(ctx, projectID)
		if err != nil {
			return nil, err
		}
		tags, _, err = s.store.ListIssueTags(ctx, store.ListIssueTagsParams{
			ProjectID: projectID,
			Limit:     MaxLimit,
		})
		if err != nil {
			return nil, err
		}
		panel.AssigneeFilters = uiProjectAllAssigneeFilters(project, assignees, allQuery)
		clearAssigneeQuery := allQuery
		clearAssigneeQuery.AssigneeIDs = nil
		panel.ClearAssigneeHref = uiProjectAllViewPath(project, clearAssigneeQuery)
		panel.ClearAssigneeHXGet = uiProjectAllPanelPath(project, clearAssigneeQuery)
		panel.ClearAssigneeHXPush = panel.ClearAssigneeHref
	}

	switch view {
	case "about":
		panel.GitHubConfigured = s.githubIntegration != nil
		panel.GitHubConnections, err = s.store.ListGitHubConnections(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if user := currentUser(r); panel.GitHubConfigured && user.ID != uuid.Nil {
			panel.GitHubCredentials, err = s.store.ListGitHubCredentials(ctx, user.ID)
			if err != nil {
				return nil, err
			}
		}
		panel.GitHubTokenLabels = uiGitHubTokenLabels(panel.GitHubConnections, panel.GitHubCredentials)
		attachments, attachmentsHasMore, err := s.store.ListProjectAttachments(ctx, store.ListProjectAttachmentsParams{ProjectID: projectID, Limit: MaxLimit})
		if err != nil {
			return nil, err
		}
		panel.ProjectAttachments = attachments
		panel.ProjectAttachmentsHasMore = attachmentsHasMore
		panel.ProjectDescriptionHTML = renderProjectDescriptionMarkdown(project, attachments)
		stats, err := s.store.GetProjectStats(ctx, store.ProjectStatsParams{ProjectID: projectID})
		if err != nil {
			return nil, err
		}
		panel.ProjectStats = stats
		projectTags, _, err := s.store.ListIssueTags(ctx, store.ListIssueTagsParams{
			ProjectID: projectID,
			Limit:     MaxLimit,
		})
		if err != nil {
			return nil, err
		}
		panel.Tags = projectTags
		return panel, nil
	case "sprint":
		activeStatus := model.SprintStatusActive
		activeSprints, _, err := s.store.ListSprints(ctx, store.ListSprintsParams{
			ProjectID: projectID,
			Status:    activeStatus,
			Limit:     1,
		})
		if err != nil {
			return nil, err
		}
		if len(activeSprints) == 0 {
			return panel, nil
		}
		panel.ActiveSprint = &activeSprints[0]
		assignees, err = s.store.ListProjectAssignees(ctx, projectID)
		if err != nil {
			return nil, err
		}
		tags, _, err = s.store.ListIssueTags(ctx, store.ListIssueTagsParams{
			ProjectID: projectID,
			Limit:     MaxLimit,
		})
		if err != nil {
			return nil, err
		}
		panel.AssigneeFilters = uiProjectSprintAssigneeFilters(project, assignees, sprintQuery)
		clearAssigneeQuery := sprintQuery
		clearAssigneeQuery.AssigneeIDs = nil
		panel.ClearAssigneeHref = uiProjectSprintViewPath(project, clearAssigneeQuery)
		panel.ClearAssigneeHXGet = uiProjectSprintPanelPath(project, clearAssigneeQuery)
		panel.ClearAssigneeHXPush = panel.ClearAssigneeHref
		panel.SprintColumns = uiIssueColumns()
		panel.SprintControls = uiProjectSprintIssueControls(project, sprintQuery, tags, panel.AssigneeFilters, panel.AssigneeFilterActive, panel.ClearAssigneeHref, panel.ClearAssigneeHXGet, panel.ClearAssigneeHXPush)
		activeAttachments, _, err := s.store.ListSprintAttachments(ctx, store.ListSprintAttachmentsParams{
			SprintID: panel.ActiveSprint.ID,
			Limit:    MaxLimit,
		})
		if err != nil {
			return nil, err
		}
		panel.ActiveSprintDescription = uiSprintDescriptionData{
			Project:         project,
			Sprint:          *panel.ActiveSprint,
			AttachmentCount: len(activeAttachments),
			DescriptionHTML: renderSprintDescriptionMarkdown(project, *panel.ActiveSprint, activeAttachments),
		}
		sprintIssues, sprintHasMore, err := s.store.ListIssues(ctx, store.ListIssuesParams{
			ProjectID:   projectID,
			Statuses:    sprintQuery.Statuses,
			Priorities:  sprintQuery.Priorities,
			TagNames:    sprintQuery.TagNames,
			AssigneeIDs: assigneeIDs,
			SprintID:    &panel.ActiveSprint.ID,
			Limit:       MaxLimit,
			Sort:        sprintQuery.Sort,
			Direction:   sprintQuery.Direction,
		})
		if err != nil {
			return nil, err
		}
		panel.SprintIssuesHasMore = sprintHasMore
		sprintItems, err := s.uiIssueItemsWithSubIssueProgress(ctx, sprintIssues, project, panel.ActiveSprint)
		if err != nil {
			return nil, err
		}
		assigneesByID := uiProjectAssigneeMap(assignees)
		for _, item := range sprintItems {
			item.Assignee = uiIssueItemAssignee(item.Issue, assigneesByID)
			for i := range panel.SprintColumns {
				if panel.SprintColumns[i].Status == uiIssueColumnStatus(item.Issue.Status) {
					panel.SprintColumns[i].Issues = append(panel.SprintColumns[i].Issues, item)
					break
				}
			}
		}
	case "planned":
		plannedStatus := model.SprintStatusPlanned
		planned, plannedHasMore, err := s.store.ListSprints(ctx, store.ListSprintsParams{
			ProjectID: projectID,
			Status:    plannedStatus,
			Limit:     MaxLimit,
		})
		if err != nil {
			return nil, err
		}
		panel.PlannedHasMore = plannedHasMore
		panel.PlannedSprints = make([]uiPlannedSprint, 0, len(planned))
		for _, sprint := range planned {
			attachments, _, err := s.store.ListSprintAttachments(ctx, store.ListSprintAttachmentsParams{
				SprintID: sprint.ID,
				Limit:    MaxLimit,
			})
			if err != nil {
				return nil, err
			}
			issues, issuesHasMore, err := s.store.ListIssues(ctx, store.ListIssuesParams{
				ProjectID: projectID,
				SprintID:  &sprint.ID,
				Limit:     MaxLimit,
			})
			if err != nil {
				return nil, err
			}
			issueItems, err := s.uiIssueItemsWithSubIssueProgress(ctx, issues, project, &sprint)
			if err != nil {
				return nil, err
			}
			panel.PlannedSprints = append(panel.PlannedSprints, uiPlannedSprint{
				Project:         project,
				Sprint:          sprint,
				Issues:          issueItems,
				HasMore:         issuesHasMore,
				AttachmentCount: len(attachments),
				DescriptionHTML: renderSprintDescriptionMarkdown(project, sprint, attachments),
			})
		}
	case "all":
		pageData, err := s.uiBuildProjectAllIssuePage(ctx, r, project)
		if err != nil {
			return nil, err
		}
		panel.AllIssues = pageData.Issues
		panel.AllIssuePage = pageData
		panel.AllControls = uiProjectAllIssueControls(project, allQuery, tags, panel.AssigneeFilters, panel.AssigneeFilterActive, panel.ClearAssigneeHref, panel.ClearAssigneeHXGet, panel.ClearAssigneeHXPush)
	case "sprints":
		pageData, err := s.uiBuildProjectSprintHistoryPage(ctx, r, project)
		if err != nil {
			return nil, err
		}
		panel.SprintHistoryPage = pageData
	case "changelog":
		pageData, err := s.uiBuildProjectChangelogPage(ctx, r, project)
		if err != nil {
			return nil, err
		}
		panel.ChangelogPage = pageData
	case "insights":
		query, err := parseProjectInsightsValues(r.URL.Query())
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errUIBadRequest, err)
		}
		insights, err := s.projectInsights(ctx, project, query)
		if err != nil {
			return nil, err
		}
		panel.Insights = uiBuildProjectInsights(project, insights, query)
	case "context":
		manager, err := s.uiBuildProjectContextManager(ctx, r, projectID)
		if err != nil {
			return nil, err
		}
		if err := s.uiHydrateProjectContextManager(ctx, manager); err != nil {
			return nil, err
		}
		panel.ContextManager = manager
	case "whiteboard":
		panel.Whiteboard, err = s.uiBuildWhiteboard(ctx, r, project, permissions.CanWrite)
		if err != nil {
			return nil, err
		}
	case "members":
		if !permissions.CanManageMembers {
			return nil, errUIForbidden
		}
		panel.Members, err = s.store.ListProjectMembers(ctx, projectID)
		if err != nil {
			return nil, err
		}
		panel.BlockedUsers, err = s.store.ListProjectUserBlocks(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if project.SprintsEnabled {
			activeSprints, _, err := s.store.ListSprints(ctx, store.ListSprintsParams{
				ProjectID: projectID,
				Status:    model.SprintStatusActive,
				Limit:     1,
			})
			if err != nil {
				return nil, err
			}
			panel.SprintModeLocked = len(activeSprints) > 0
		}
		panel.MembersPage = true
		panel.MemberRoleInput = model.ProjectMemberRoleMember
	default:
		return nil, store.ErrNotFound
	}

	return panel, nil
}

func (s *Server) uiProjectSprintHistoryPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	pageData, err := s.uiBuildProjectSprintHistoryPage(r.Context(), r, project)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "project-sprint-history-page", pageData)
}

func (s *Server) uiBuildProjectSprintHistoryPage(ctx context.Context, r *http.Request, project model.Project) (uiProjectSprintHistoryPageData, error) {
	var cursor *store.SprintsCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.SprintsCursor
		if err := decodeCursor(raw, &c); err != nil {
			return uiProjectSprintHistoryPageData{}, fmt.Errorf("invalid sprint history cursor: %w", errUIBadRequest)
		}
		cursor = &c
	}
	sprints, hasMore, err := s.store.ListSprints(ctx, store.ListSprintsParams{
		ProjectID: project.ID,
		Status:    model.SprintStatusCompleted,
		Sort:      store.ListSprintsSortCompleted,
		Cursor:    cursor,
		Limit:     DefaultLimit,
	})
	if err != nil {
		return uiProjectSprintHistoryPageData{}, err
	}
	sprintIDs := make([]uuid.UUID, 0, len(sprints))
	for _, sprint := range sprints {
		sprintIDs = append(sprintIDs, sprint.ID)
	}
	statusCounts, err := s.store.CountSprintSnapshotIssuesByStatus(ctx, store.CountSprintSnapshotIssuesByStatusParams{
		ProjectID: project.ID,
		SprintIDs: sprintIDs,
	})
	if err != nil {
		return uiProjectSprintHistoryPageData{}, err
	}
	attachmentsBySprint, err := s.store.ListSprintAttachmentsForSprints(ctx, store.ListSprintAttachmentsForSprintsParams{
		ProjectID: project.ID,
		SprintIDs: sprintIDs,
		Limit:     MaxLimit,
	})
	if err != nil {
		return uiProjectSprintHistoryPageData{}, err
	}
	descriptions := make(map[uuid.UUID]uiSprintDescriptionData, len(sprints))
	for _, sprint := range sprints {
		attachments := attachmentsBySprint[sprint.ID]
		descriptions[sprint.ID] = uiSprintDescriptionData{
			Project:         project,
			Sprint:          sprint,
			AttachmentCount: len(attachments),
			DescriptionHTML: renderSprintDescriptionMarkdown(project, sprint, attachments),
		}
	}
	pageData := uiProjectSprintHistoryPageData{
		Project:      project,
		Sprints:      sprints,
		Descriptions: descriptions,
		StatusCounts: statusCounts,
		HasMore:      hasMore,
	}
	if hasMore {
		pageData.NextCursor = encodeCursor(sprintListCursor(sprints[len(sprints)-1], model.SprintStatusCompleted, store.ListSprintsSortCompleted))
	}
	return pageData, nil
}

func (s *Server) uiProjectSprintHistoryIssues(w http.ResponseWriter, r *http.Request) {
	project, sprint, ok := s.uiProjectSprintFromRoute(w, r)
	if !ok {
		return
	}
	if sprint.Status != model.SprintStatusCompleted {
		writeUIStoreError(w, store.ErrNotFound)
		return
	}
	pageData, err := s.uiBuildProjectSprintHistoryIssuePage(r.Context(), r, project, sprint)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	templateName := "project-sprint-history-issues"
	if r.URL.Query().Get("cursor") != "" {
		templateName = "project-sprint-history-issue-page"
	}
	renderUITemplate(w, http.StatusOK, templateName, pageData)
}

func (s *Server) uiBuildProjectSprintHistoryIssuePage(ctx context.Context, r *http.Request, project model.Project, sprint model.Sprint) (uiProjectSprintHistoryIssuePageData, error) {
	var cursor *store.IssuesCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.IssuesCursor
		if err := decodeCursor(raw, &c); err != nil {
			return uiProjectSprintHistoryIssuePageData{}, fmt.Errorf("invalid sprint history issue cursor: %w", errUIBadRequest)
		}
		cursor = &c
	}
	issues, hasMore, err := s.store.ListSprintSnapshotIssues(ctx, store.ListSprintSnapshotIssuesParams{
		ProjectID: project.ID,
		SprintID:  sprint.ID,
		Cursor:    cursor,
		Limit:     DefaultLimit,
	})
	if err != nil {
		return uiProjectSprintHistoryIssuePageData{}, err
	}
	issueItems, err := s.uiIssueItemsWithSubIssueProgress(ctx, issues, project, &sprint)
	if err != nil {
		return uiProjectSprintHistoryIssuePageData{}, err
	}
	pageData := uiProjectSprintHistoryIssuePageData{
		Project: project,
		Sprint:  sprint,
		Issues:  issueItems,
	}
	if hasMore {
		cursor := encodeCursor(sprintHistoryIssueCursor(issues[len(issues)-1]))
		pageData.NextHXGet = uiProjectSprintHistoryIssuesPath(project, sprint) + "?cursor=" + cursor
	}
	return pageData, nil
}

func (s *Server) uiProjectChangelogPage(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return
	}
	if err := s.uiRequireProjectAccess(r.Context(), currentUser(r), project.ID); err != nil {
		writeUIStoreError(w, err)
		return
	}
	pageData, err := s.uiBuildProjectChangelogPage(r.Context(), r, project)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "project-changelog-page", pageData)
}

func (s *Server) uiBuildProjectChangelogPage(ctx context.Context, r *http.Request, project model.Project) (uiProjectChangelogPageData, error) {
	var cursor *store.ProjectChangelogCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.ProjectChangelogCursor
		if err := decodeCursor(raw, &c); err != nil {
			return uiProjectChangelogPageData{}, fmt.Errorf("invalid changelog cursor: %w", errUIBadRequest)
		}
		cursor = &c
	}
	entries, hasMore, err := s.store.ListProjectChangelog(ctx, store.ListProjectChangelogParams{
		ProjectID: project.ID,
		Cursor:    cursor,
		Limit:     DefaultLimit,
	})
	if err != nil {
		return uiProjectChangelogPageData{}, err
	}
	pageData := uiProjectChangelogPageData{
		Project: project,
		Entries: entries,
		HasMore: hasMore,
	}
	if hasMore {
		last := entries[len(entries)-1]
		pageData.NextCursor = encodeCursor(store.ProjectChangelogCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return pageData, nil
}

func (s *Server) uiBuildProjectAllIssuePage(ctx context.Context, r *http.Request, project model.Project) (uiProjectAllIssuePageData, error) {
	if err := s.uiRequireProjectAccess(ctx, currentUser(r), project.ID); err != nil {
		return uiProjectAllIssuePageData{}, err
	}
	allQuery, err := uiParseProjectAllQuery(r)
	if err != nil {
		return uiProjectAllIssuePageData{}, err
	}
	var cursor *store.IssuesCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.IssuesCursor
		if err := decodeCursor(raw, &c); err != nil {
			return uiProjectAllIssuePageData{}, fmt.Errorf("invalid all issues cursor: %w", errUIBadRequest)
		}
		cursor = &c
	}
	issues, hasMore, err := s.store.ListIssues(ctx, store.ListIssuesParams{
		ProjectID:   project.ID,
		Statuses:    allQuery.Statuses,
		Priorities:  allQuery.Priorities,
		TagNames:    allQuery.TagNames,
		AssigneeIDs: allQuery.AssigneeIDs,
		Cursor:      cursor,
		Limit:       DefaultLimit,
		Sort:        allQuery.Sort,
		Direction:   allQuery.Direction,
	})
	if err != nil {
		return uiProjectAllIssuePageData{}, err
	}
	issueItems, err := s.uiIssueItemsWithSubIssueProgress(ctx, issues, project, nil)
	if err != nil {
		return uiProjectAllIssuePageData{}, err
	}
	pageData := uiProjectAllIssuePageData{Issues: issueItems}
	if hasMore {
		last := issues[len(issues)-1]
		nextQuery := allQuery
		nextQuery.Cursor = encodeCursor(issueListCursor(last, allQuery.Sort))
		pageData.NextHXGet = uiProjectAllPagePath(project, nextQuery)
	}
	return pageData, nil
}

func (s *Server) uiBuildDeletedIssuesPanel(ctx context.Context, r *http.Request, projectID uuid.UUID) (*uiDeletedIssuesPanelData, error) {
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
	deleted, hasMore, err := s.store.ListDeletedIssues(ctx, store.ListDeletedIssuesParams{
		ProjectID: projectID,
		Limit:     MaxLimit,
	})
	if err != nil {
		return nil, err
	}
	return &uiDeletedIssuesPanelData{
		CSRFToken: uiSessionCSRFToken(r),
		Project:   project,
		Issues:    deleted,
		HasMore:   hasMore,
		CanWrite:  permissions.CanWrite,
	}, nil
}

func (s *Server) uiBuildDeletedIssuePanel(ctx context.Context, r *http.Request, issue model.Issue) (*uiDeletedIssuePanelData, error) {
	if err := s.uiRequireProjectAccess(ctx, currentUser(r), issue.ProjectID); err != nil {
		return nil, err
	}
	project, err := s.store.GetProject(ctx, issue.ProjectID)
	if err != nil {
		return nil, err
	}
	permissions, err := s.uiProjectPermissions(ctx, currentUser(r), issue.ProjectID)
	if err != nil {
		return nil, err
	}
	return &uiDeletedIssuePanelData{
		CSRFToken: uiSessionCSRFToken(r),
		Issue:     issue,
		Project:   project,
		CanWrite:  permissions.CanWrite,
		BackHref:  uiProjectViewPath(project, "deleted"),
		BackHXGet: uiProjectPanelPath(project, "deleted"),
	}, nil
}
