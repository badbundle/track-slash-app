package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"time"
)

func (s *Server) renderUIIssuePanelWithSubIssueError(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, title, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.SubIssueTitle = title
	panel.SubIssueError = message
	panel.AddSubIssue = true
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func (s *Server) renderUIIssuePanelWithCommentError(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, body, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.CommentBody = body
	panel.CommentError = message
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func (s *Server) renderUIIssuePanelWithCommentEditError(w http.ResponseWriter, r *http.Request, issueID, commentID uuid.UUID, body, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.EditCommentID = commentID
	panel.CommentEditBody = body
	panel.CommentEditError = message
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func (s *Server) renderUIIssuePanelWithAssigneeError(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, input, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.EditAssignee = true
	panel.AssigneeInput = input
	panel.AssigneeError = message
	if err := s.uiPopulateIssueMemberOptions(r.Context(), panel); err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func (s *Server) renderUIIssuePanelWithReporterError(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, input, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.EditReporter = true
	panel.ReporterInput = input
	panel.ReporterError = message
	if err := s.uiPopulateIssueMemberOptions(r.Context(), panel); err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func (s *Server) renderUIIssuePanelWithSprintError(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, input, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.EditSprint = true
	panel.SprintInput = input
	panel.SprintError = message
	if err := s.uiPopulateIssueSprintOptions(r.Context(), panel); err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func (s *Server) renderUIIssuePanelWithDueDateError(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, input, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	panel.EditDueDate = true
	panel.DueDateInput = input
	panel.DueDateError = message
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func (s *Server) renderUIIssuePanelWithCloseReasonError(w http.ResponseWriter, r *http.Request, issueID uuid.UUID, input, message string) {
	panel, err := s.uiBuildIssuePanel(r.Context(), r, issueID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	if panel.Issue.Status == model.StatusClosed {
		panel.EditCloseReason = true
	} else {
		panel.PendingCloseReason = true
		panel.Issue.Status = model.StatusClosed
		panel.CanEditSprint = false
	}
	panel.CloseReasonInput = input
	panel.CloseReasonError = message
	renderUITemplate(w, http.StatusOK, "issue-panel", panel)
}

func (s *Server) uiPopulateIssueMemberOptions(ctx context.Context, panel *uiIssuePanelData) error {
	users, err := s.store.SearchProjectMembers(ctx, store.SearchProjectMembersParams{
		ProjectID:    panel.Project.ID,
		Limit:        MaxLimit,
		WritableOnly: true,
	})
	if err != nil {
		return err
	}
	panel.MemberOptions = users
	return nil
}

func (s *Server) uiPopulateIssueSprintOptions(ctx context.Context, panel *uiIssuePanelData) error {
	active, _, err := s.store.ListSprints(ctx, store.ListSprintsParams{
		ProjectID: panel.Project.ID,
		Status:    model.SprintStatusActive,
		Limit:     MaxLimit,
	})
	if err != nil {
		return err
	}
	planned, _, err := s.store.ListSprints(ctx, store.ListSprintsParams{
		ProjectID: panel.Project.ID,
		Status:    model.SprintStatusPlanned,
		Limit:     MaxLimit,
	})
	if err != nil {
		return err
	}
	panel.SprintOptions = make([]uiIssueSprintOption, 0, len(active)+len(planned))
	for _, sprint := range active {
		panel.SprintOptions = append(panel.SprintOptions, uiIssueSprintOptionFor(sprint, "Active"))
	}
	for _, sprint := range planned {
		panel.SprintOptions = append(panel.SprintOptions, uiIssueSprintOptionFor(sprint, "Planned"))
	}
	return nil
}

func (s *Server) uiIssuePersonID(ctx context.Context, projectID uuid.UUID, raw string) (*uuid.UUID, bool, string, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return nil, true, "", nil
	}
	username, err := store.NormalizeUsername(strings.TrimPrefix(input, "@"))
	if err != nil {
		return nil, false, "Choose a project member.", nil
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, false, "Choose a project member.", nil
		}
		return nil, false, "", err
	}
	users, err := s.store.SearchProjectMembers(ctx, store.SearchProjectMembersParams{
		ProjectID:    projectID,
		Query:        username,
		Limit:        MaxLimit,
		WritableOnly: true,
	})
	if err != nil {
		return nil, false, "", err
	}
	for _, member := range users {
		if member.ID == user.ID {
			return &user.ID, false, "", nil
		}
	}
	return nil, false, "Choose a project member.", nil
}

func (s *Server) uiIssueCreateUserID(ctx context.Context, raw string) (*uuid.UUID, string, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return nil, "", nil
	}
	username, err := store.NormalizeUsername(strings.TrimPrefix(input, "@"))
	if err != nil {
		return nil, "Choose a user.", nil
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, "Choose a user.", nil
		}
		return nil, "", err
	}
	return &user.ID, "", nil
}

func (s *Server) uiIssueSprintID(ctx context.Context, projectID uuid.UUID, raw string) (*uuid.UUID, bool, string, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return nil, true, "", nil
	}
	number, err := parseTypedRef(input, "sprint")
	if err != nil {
		return nil, false, "Choose an active or planned sprint.", nil
	}
	sprint, err := s.store.GetSprintByProjectNumber(ctx, projectID, number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, false, "Choose an active or planned sprint.", nil
		}
		return nil, false, "", err
	}
	if sprint.Status == model.SprintStatusCompleted {
		return nil, false, "Choose an active or planned sprint.", nil
	}
	return &sprint.ID, false, "", nil
}

func uiIssueSprintInput(sprint *model.Sprint) string {
	if sprint == nil {
		return ""
	}
	return uiIssueSprintRef(*sprint)
}

func uiIssueSprintOptionFor(sprint model.Sprint, status string) uiIssueSprintOption {
	ref := uiIssueSprintRef(sprint)
	name := strings.TrimSpace(sprint.Name)
	if name == "" {
		name = ref
	}
	label := fmt.Sprintf("%s - %s", status, name)
	if dateRange := uiSprintDateRange(sprint); dateRange != "" {
		label += " - " + dateRange
	}
	return uiIssueSprintOption{
		Value: ref,
		Label: label,
	}
}

func uiIssueAssigneeAutocomplete(panel *uiIssuePanelData) uiAutocompleteEditData {
	return uiIssueMemberAutocomplete(panel, "Assignee", uiIssueAssigneePath(panel.Issue), "assignee", panel.AssigneeInput, "Unassigned", "Save assignee", "Cancel editing assignee", panel.AssigneeError)
}

func uiIssueReporterAutocomplete(panel *uiIssuePanelData) uiAutocompleteEditData {
	return uiIssueMemberAutocomplete(panel, "Reporter", uiIssueReporterPath(panel.Issue), "reporter", panel.ReporterInput, "No reporter", "Save reporter", "Cancel editing reporter", panel.ReporterError)
}

func uiIssueMemberAutocomplete(panel *uiIssuePanelData, label, action, name, value, placeholder, saveLabel, cancelLabel, message string) uiAutocompleteEditData {
	return uiAutocompleteEditData{
		CSRFToken:   panel.CSRFToken,
		Label:       label,
		Action:      action,
		PanelPath:   uiIssuePanelPath(panel.Issue),
		IssueHref:   uiIssuePath(panel.Issue),
		Name:        name,
		Value:       value,
		Placeholder: placeholder,
		SaveLabel:   saveLabel,
		CancelLabel: cancelLabel,
		Error:       message,
		Autofocus:   true,
		Options:     uiMemberAutocompleteOptions(panel.MemberOptions),
	}
}

func uiIssueSprintAutocomplete(panel *uiIssuePanelData) uiAutocompleteEditData {
	return uiAutocompleteEditData{
		CSRFToken:   panel.CSRFToken,
		Label:       "Sprint",
		Action:      uiIssueSprintPath(panel.Issue),
		PanelPath:   uiIssuePanelPath(panel.Issue),
		IssueHref:   uiIssuePath(panel.Issue),
		Name:        "sprint",
		Value:       panel.SprintInput,
		Placeholder: "None",
		SaveLabel:   "Save sprint",
		CancelLabel: "Cancel editing sprint",
		Error:       panel.SprintError,
		Autofocus:   true,
		Options:     uiSprintAutocompleteOptions(panel.SprintOptions),
	}
}

func uiNewIssueProjectAutocomplete(panel *uiNewIssuePanelData) uiAutocompleteEditData {
	return uiAutocompleteEditData{
		CSRFToken:         panel.CSRFToken,
		ID:                "issue-project",
		Label:             "Project",
		Name:              "project",
		Value:             uiNewIssueProjectInput(panel),
		HiddenName:        "project_id",
		HiddenValue:       panel.ProjectID,
		TargetName:        "project_id",
		Placeholder:       "Search projects",
		Autofocus:         !panel.HasProject,
		Collapsible:       true,
		OptionsOpen:       panel.ProjectSearchOpen,
		InputHXGet:        uiIssueNewProjectOptionsPath(),
		InputHXTrigger:    "input changed delay:300ms",
		InputHXTarget:     "#new-issue-project-options",
		InputHXSwap:       "outerHTML",
		InputHXPushURL:    "false",
		InputHXInclude:    "#new-issue-project-form",
		SearchClearTarget: "project_id",
		OptionsID:         "new-issue-project-options",
		EmptyLabel:        "No projects found.",
		Options:           uiProjectAutocompleteOptions(panel.ProjectOptions),
	}
}

func uiMemberAutocompleteOptions(users []model.User) []uiAutocompleteOption {
	options := make([]uiAutocompleteOption, 0, len(users))
	for _, user := range users {
		label := strings.TrimSpace(user.Name)
		if user.Email != "" {
			if label == "" {
				label = user.Email
			} else {
				label += " - " + user.Email
			}
		}
		if label == "" {
			label = "@" + user.Username
		}
		options = append(options, uiAutocompleteOption{
			Value:      "@" + user.Username,
			Label:      label,
			SearchText: "@" + user.Username + " " + label,
		})
	}
	return options
}

func uiSprintAutocompleteOptions(sprints []uiIssueSprintOption) []uiAutocompleteOption {
	options := make([]uiAutocompleteOption, 0, len(sprints))
	for _, sprint := range sprints {
		options = append(options, uiAutocompleteOption{
			Value:      sprint.Value,
			Label:      sprint.Label,
			SearchText: sprint.Value + " " + sprint.Label,
		})
	}
	return options
}

func uiProjectAutocompleteOptions(projects []model.Project) []uiAutocompleteOption {
	options := make([]uiAutocompleteOption, 0, len(projects))
	for _, project := range projects {
		options = append(options, uiAutocompleteOption{
			Value:       uiNewIssueProjectLabel(project),
			Label:       project.Name,
			Badge:       project.Key,
			SearchText:  project.Key + " " + project.Name + " " + project.OwnerUsername,
			TargetValue: project.ID.String(),
		})
	}
	return options
}

func uiAutocompleteOptionSearchText(option uiAutocompleteOption) string {
	if option.SearchText != "" {
		return option.SearchText
	}
	return strings.TrimSpace(option.Value + " " + option.Label)
}

func uiIssueSprintRef(sprint model.Sprint) string {
	if sprint.Ref != "" {
		return sprint.Ref
	}
	if sprint.Number > 0 {
		return model.SprintRef(sprint.Number)
	}
	return ""
}

func (s *Server) uiBuildIssuePanel(ctx context.Context, r *http.Request, issueID uuid.UUID) (*uiIssuePanelData, error) {
	projectID, err := s.store.ProjectIDForIssue(ctx, issueID)
	if err != nil {
		return nil, err
	}
	if err := s.uiRequireProjectAccess(ctx, currentUser(r), projectID); err != nil {
		return nil, err
	}
	permissions, err := s.uiProjectPermissions(ctx, currentUser(r), projectID)
	if err != nil {
		return nil, err
	}
	issue, err := s.store.GetIssue(ctx, issueID)
	if err != nil {
		return nil, err
	}
	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	deleteNotice, err := s.uiDeletedIssueNotice(ctx, r, project.OwnerUsername, projectID)
	if err != nil {
		return nil, err
	}
	assignee, err := s.uiOptionalUser(ctx, issue.AssigneeID)
	if err != nil {
		return nil, err
	}
	reporter, err := s.uiOptionalUser(ctx, issue.ReporterID)
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
	var sprint *model.Sprint
	if issue.ParentIssueID == nil {
		sprint, err = s.uiOptionalSprint(ctx, issue.SprintID)
		if err != nil {
			return nil, err
		}
	}
	var subIssues []model.Issue
	var subIssuesHasMore bool
	if issue.ParentIssueID == nil {
		subIssues, subIssuesHasMore, err = s.store.ListSubIssuesForIssue(ctx, store.ListSubIssuesForIssueParams{
			ParentIssueID: issueID,
			Limit:         MaxLimit,
		})
		if err != nil {
			return nil, err
		}
	}
	attachments, attachmentsHasMore, err := s.store.ListIssueAttachments(ctx, store.ListIssueAttachmentsParams{
		IssueID: issueID,
		Limit:   MaxLimit,
	})
	if err != nil {
		return nil, err
	}
	comments, commentsHasMore, err := s.store.ListCommentsForIssue(ctx, store.ListCommentsForIssueParams{
		IssueID:     issueID,
		Limit:       MaxLimit,
		NewestFirst: true,
	})
	if err != nil {
		return nil, err
	}
	commentItems := make([]uiIssueCommentItem, 0, len(comments))
	for _, comment := range comments {
		author, err := s.uiOptionalUser(ctx, &comment.AuthorID)
		if err != nil {
			return nil, err
		}
		item := uiIssueCommentItem{
			Comment:    comment,
			BodyHTML:   renderIssueCommentMarkdown(issue, comment, attachments),
			AuthorID:   comment.AuthorID,
			AuthorName: "Unknown user",
			CanEdit:    permissions.CanWrite && comment.AuthorID == currentUser(r).ID,
		}
		if author != nil {
			item.AuthorUsername = author.Username
			item.AuthorName = author.Name
			item.AuthorEmail = author.Email
			item.AuthorProfileImageThumbnailObjectID = author.ProfileImageThumbnailObjectID
		}
		commentItems = append(commentItems, item)
	}
	links, linksHasMore, err := s.store.ListIssueLinksForIssue(ctx, store.ListIssueLinksForIssueParams{
		IssueID: issueID,
		Limit:   MaxLimit,
	})
	if err != nil {
		return nil, err
	}
	linkedIssues, err := s.uiLinkedIssues(ctx, issueID, links)
	if err != nil {
		return nil, err
	}
	linkItems := make([]uiIssueLinkItem, 0, len(links))
	for _, link := range links {
		otherID := link.SourceID
		if otherID == issueID {
			otherID = link.TargetID
		}
		item := uiIssueLinkItem{Link: link}
		if linked, ok := linkedIssues[otherID]; ok {
			item.LinkedIssue = linked
			item.HasIssue = true
		}
		linkItems = append(linkItems, item)
	}
	contexts, contextsHasMore, err := s.store.ListContextsForIssue(ctx, store.ListContextsForIssueParams{
		IssueID: issueID,
		Limit:   MaxLimit,
	})
	if err != nil {
		return nil, err
	}

	githubConnections, err := s.store.ListGitHubConnections(ctx, projectID)
	if err != nil {
		return nil, err
	}
	githubLinks, err := s.store.ListGitHubIssueLinks(ctx, issueID)
	if err != nil {
		return nil, err
	}
	githubItems := make([]uiGitHubIssueLink, 0, len(githubLinks))
	for _, link := range githubLinks {
		item := uiGitHubIssueLink{Link: link, Stale: link.Stale(time.Now(), 30*time.Minute)}
		if link.LastRefreshedAt != nil {
			item.LastRefreshed = uiTokenTime(*link.LastRefreshedAt)
		}
		githubItems = append(githubItems, item)
	}

	backHref, backHXGet, backLabel := uiIssueBackLink(project, issue, parentIssue, sprint)
	return &uiIssuePanelData{
		CSRFToken:          uiSessionCSRFToken(r),
		Issue:              issue,
		Project:            project,
		CanWrite:           permissions.CanWrite,
		GitHubConfigured:   s.githubIntegration != nil,
		GitHubConnections:  githubConnections,
		GitHubLinks:        githubItems,
		OwnerCrumb:         currentUser(r).ID != project.OwnerID,
		ParentIssue:        parentIssue,
		Sprint:             sprint,
		Assignee:           assignee,
		Reporter:           reporter,
		CanEditSprint:      permissions.CanWrite && issue.ParentIssueID == nil && !issue.Status.CountsAsDone(),
		DescriptionHTML:    renderIssueDescriptionMarkdown(issue, attachments),
		Attachments:        attachments,
		AttachmentsHasMore: attachmentsHasMore,
		SubIssues:          subIssues,
		SubIssuesHasMore:   subIssuesHasMore,
		Comments:           commentItems,
		CommentsHasMore:    commentsHasMore,
		Links:              linkItems,
		LinksHasMore:       linksHasMore,
		Contexts:           contexts,
		ContextsHasMore:    contextsHasMore,
		BackHref:           backHref,
		BackHXGet:          backHXGet,
		BackLabel:          backLabel,
		DeleteNotice:       deleteNotice,
	}, nil
}

func (s *Server) uiOptionalUser(ctx context.Context, id *uuid.UUID) (*model.User, error) {
	if id == nil {
		return nil, nil
	}
	user, err := s.store.GetUser(ctx, *id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (s *Server) uiOptionalSprint(ctx context.Context, id *uuid.UUID) (*model.Sprint, error) {
	if id == nil {
		return nil, nil
	}
	sprint, err := s.store.GetSprint(ctx, *id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &sprint, nil
}

func (s *Server) uiLinkedIssues(ctx context.Context, issueID uuid.UUID, links []model.IssueLink) (map[uuid.UUID]model.Issue, error) {
	seen := map[uuid.UUID]struct{}{}
	ids := make([]uuid.UUID, 0, len(links))
	for _, link := range links {
		otherID := link.SourceID
		if otherID == issueID {
			otherID = link.TargetID
		}
		if _, ok := seen[otherID]; ok {
			continue
		}
		seen[otherID] = struct{}{}
		ids = append(ids, otherID)
	}
	issues, err := s.store.ListIssuesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]model.Issue, len(issues))
	for _, issue := range issues {
		out[issue.ID] = issue
	}
	return out, nil
}

type uiIssueBackDestination struct {
	Href      string
	HXGet     string
	Label     string
	View      string
	HasParent bool
	ParentID  uuid.UUID
}

func uiIssueBackDestinationFor(project model.Project, issue model.Issue, parent *model.Issue, sprint *model.Sprint) uiIssueBackDestination {
	if parent != nil {
		base := uiIssuePath(*parent)
		return uiIssueBackDestination{
			Href:      base,
			HXGet:     base + "/panel",
			Label:     "Parent issue",
			HasParent: true,
			ParentID:  parent.ID,
		}
	}

	view := "all"
	label := "All issues"
	if issue.SprintID != nil && sprint != nil {
		switch sprint.Status {
		case model.SprintStatusActive:
			view = "sprint"
			label = "Sprint"
		case model.SprintStatusPlanned:
			if project.SprintsEnabled {
				view = "planned"
				label = "Planned"
			}
		}
	}
	base := uiProjectViewPath(project, view)
	return uiIssueBackDestination{
		Href:  base,
		HXGet: base + "/panel",
		Label: label,
		View:  view,
	}
}

func uiIssueBackLink(project model.Project, issue model.Issue, parent *model.Issue, sprint *model.Sprint) (href, hxGet, label string) {
	target := uiIssueBackDestinationFor(project, issue, parent, sprint)
	return target.Href, target.HXGet, target.Label
}

func (s *Server) renderUIIssueBackTarget(w http.ResponseWriter, r *http.Request, panel *uiIssuePanelData, notice *uiIssueDeleteNotice) {
	target := uiIssueBackDestinationFor(panel.Project, panel.Issue, panel.ParentIssue, panel.Sprint)
	if target.HasParent {
		parentPanel, err := s.uiBuildIssuePanel(r.Context(), r, target.ParentID)
		if err != nil {
			writeUIStoreError(w, err)
			return
		}
		parentPanel.DeleteNotice = notice
		renderUITemplate(w, http.StatusOK, "issue-panel", parentPanel)
		return
	}
	view := target.View
	if view == "" {
		view = "all"
	}
	projectPanel, err := s.uiBuildProjectPanel(r.Context(), r, panel.Project.ID, view)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	projectPanel.DeleteNotice = notice
	renderUITemplate(w, http.StatusOK, "project-panel", projectPanel)
}
