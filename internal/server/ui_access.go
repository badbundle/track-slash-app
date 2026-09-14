package server

import (
	"context"
	"errors"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) uiVisibleProjects(ctx context.Context, user model.User) ([]model.Project, error) {
	var all []model.Project
	var cursor *store.ProjectsCursor
	for {
		projects, hasMore, err := s.store.ListProjects(ctx, store.ListProjectsParams{
			Cursor:        cursor,
			Limit:         MaxLimit,
			VisibleToUser: visibleProjectUser(user),
		})
		if err != nil {
			return nil, err
		}
		all = append(all, projects...)
		if !hasMore {
			return all, nil
		}
		last := projects[len(projects)-1]
		cursor = &store.ProjectsCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
}

func (s *Server) uiWritableProjects(ctx context.Context, user model.User) ([]model.Project, error) {
	var all []model.Project
	var cursor *store.ProjectsCursor
	for {
		params := store.ListProjectsParams{Cursor: cursor, Limit: MaxLimit}
		if !user.IsAdmin {
			params.WritableToUser = &user.ID
		}
		projects, hasMore, err := s.store.ListProjects(ctx, params)
		if err != nil {
			return nil, err
		}
		all = append(all, projects...)
		if !hasMore {
			return all, nil
		}
		last := projects[len(projects)-1]
		cursor = &store.ProjectsCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
}

func (s *Server) uiIssueCreatableProjects(ctx context.Context, user model.User) ([]model.Project, error) {
	var all []model.Project
	var cursor *store.ProjectsCursor
	for {
		params := store.ListProjectsParams{Cursor: cursor, Limit: MaxLimit}
		if !user.IsAdmin {
			params.IssueCreatableToUser = &user.ID
		}
		projects, hasMore, err := s.store.ListProjects(ctx, params)
		if err != nil {
			return nil, err
		}
		all = append(all, projects...)
		if !hasMore {
			return all, nil
		}
		last := projects[len(projects)-1]
		cursor = &store.ProjectsCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
}

func (s *Server) uiFavoriteProjects(ctx context.Context, user model.User) ([]model.Project, error) {
	return s.store.ListFavoriteProjects(ctx, store.ListFavoriteProjectsParams{
		User:  user,
		Limit: MaxLimit,
	})
}

func (s *Server) renderUIShell(w http.ResponseWriter, r *http.Request, status int, data uiShellData) {
	if data.CSRFToken == "" {
		data.CSRFToken = uiSessionCSRFToken(r)
	}
	if data.User.ID == uuid.Nil {
		data.User = currentUser(r)
	}
	data.Authenticated = data.User.ID != uuid.Nil
	data.Anonymous = !data.Authenticated
	var favorites []model.Project
	if data.Authenticated {
		var err error
		favorites, err = s.uiFavoriteProjects(r.Context(), data.User)
		if err != nil {
			writeUIInternalError(w, "ui shell favorites", err)
			return
		}
	}
	activeProjectID := uuid.Nil
	if data.SidebarActive.View == "project" {
		activeProjectID = data.SidebarActive.ProjectID
	}
	data.SidebarFavorites = uiSidebarFavoritesData{
		Projects:        favorites,
		ActiveProjectID: activeProjectID,
	}
	renderUITemplate(w, status, uiShellTemplateName(r), data)
}

// uiShellTemplateName picks the whole document or just the content that belongs
// inside #main.
//
// Every htmx control in the app targets a sub-element, and #main is a sibling of
// the sidebar inside .app-shell. Answering an htmx request with the whole
// document makes htmx swap the header, the sidebar, and a nested #main into the
// existing #main, so the user sees two sidebars side by side.
func uiShellTemplateName(r *http.Request) string {
	if isHTMXRequest(r) {
		return "shell-main-content"
	}
	return "shell"
}

func (s *Server) uiRequireProjectAccess(ctx context.Context, user model.User, projectID uuid.UUID) error {
	ok, err := s.store.UserCanAccessProject(ctx, user, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errUIForbidden
	}
	return nil
}

func (s *Server) uiRequireProjectWriteAccess(ctx context.Context, user model.User, projectID uuid.UUID) error {
	ok, err := s.store.UserCanWriteProject(ctx, user, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errUIForbidden
	}
	return nil
}

func (s *Server) uiRequireProjectIssueCreationAccess(ctx context.Context, user model.User, projectID uuid.UUID) error {
	ok, err := s.store.UserCanCreateProjectIssue(ctx, user, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errUIForbidden
	}
	return nil
}

func (s *Server) uiRequireProjectMemberManagement(ctx context.Context, user model.User, projectID uuid.UUID) error {
	ok, err := s.store.UserCanManageProjectMembers(ctx, user, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errUIForbidden
	}
	return nil
}

func (s *Server) uiRequireProjectDeletion(ctx context.Context, user model.User, projectID uuid.UUID) error {
	ok, err := s.store.UserCanDeleteProject(ctx, user, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errUIForbidden
	}
	return nil
}

func (s *Server) uiProjectPermissions(ctx context.Context, user model.User, projectID uuid.UUID) (store.ProjectPermissions, error) {
	return s.store.ProjectPermissionsForUser(ctx, user, projectID)
}

func (s *Server) uiProjectWriteHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := s.uiProjectFromRoute(w, r)
		if !ok {
			return
		}
		if err := s.uiRequireProjectWriteAccess(r.Context(), currentUser(r), project.ID); err != nil {
			writeUIStoreError(w, err)
			return
		}
		next(w, r)
	}
}

func (s *Server) uiIssueWriteHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issue, _, ok := s.uiIssueFromRouteIncludingDeleted(w, r)
		if !ok {
			return
		}
		if err := s.uiRequireProjectWriteAccess(r.Context(), currentUser(r), issue.ProjectID); err != nil {
			writeUIStoreError(w, err)
			return
		}
		next(w, r)
	}
}

func (s *Server) uiProjectIssueCreateHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).ID == uuid.Nil {
			redirectUILogin(w, r)
			return
		}
		project, ok := s.uiProjectFromRoute(w, r)
		if !ok {
			return
		}
		if err := s.uiRequireProjectIssueCreationAccess(r.Context(), currentUser(r), project.ID); err != nil {
			writeUIStoreError(w, err)
			return
		}
		next(w, r)
	}
}

func (s *Server) uiProjectFromNewIssueSelection(ctx context.Context, user model.User, raw string) (model.Project, bool, string, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return model.Project{}, false, "Choose a project.", nil
	}
	projectID, err := uuid.Parse(input)
	if err != nil {
		return model.Project{}, false, "Choose a project.", nil
	}
	project, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.Project{}, false, "Choose a project.", nil
		}
		return model.Project{}, false, "", err
	}
	if err := s.uiRequireProjectIssueCreationAccess(ctx, user, project.ID); err != nil {
		return model.Project{}, false, "", err
	}
	return project, true, "", nil
}

func (s *Server) uiProjectFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, bool) {
	owner, err := store.NormalizeUsername(chi.URLParam(r, "owner"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return model.Project{}, false
	}
	key := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "key")))
	if !projectKeyRe.MatchString(key) {
		http.Error(w, "invalid project key", http.StatusBadRequest)
		return model.Project{}, false
	}
	project, err := s.store.GetProjectByOwnerKey(r.Context(), owner, key)
	if err != nil {
		writeUIStoreError(w, err)
		return model.Project{}, false
	}
	return project, true
}

func (s *Server) uiProjectContextFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, model.ProjectContext, bool) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return model.Project{}, model.ProjectContext{}, false
	}
	number, err := parseTypedRef(chi.URLParam(r, "contextRef"), "context")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return model.Project{}, model.ProjectContext{}, false
	}
	contextItem, err := s.store.GetProjectContextByProjectNumber(r.Context(), project.ID, number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.Project{}, model.ProjectContext{}, false
	}
	if contextItem.Scope != model.ProjectContextScopeProject {
		writeUIStoreError(w, store.ErrNotFound)
		return model.Project{}, model.ProjectContext{}, false
	}
	return project, contextItem, true
}

func (s *Server) uiIssueFromRoute(w http.ResponseWriter, r *http.Request) (model.Issue, bool) {
	owner, ref, ok := uiIssueRouteOwnerRef(w, r)
	if !ok {
		return model.Issue{}, false
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(r.Context(), owner, ref.ProjectKey, ref.Number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.Issue{}, false
	}
	return issue, true
}

func (s *Server) uiDeletedIssueFromRoute(w http.ResponseWriter, r *http.Request) (model.Issue, bool) {
	owner, ref, ok := uiIssueRouteOwnerRef(w, r)
	if !ok {
		return model.Issue{}, false
	}
	issue, err := s.store.GetDeletedIssueByOwnerKeyNumber(r.Context(), owner, ref.ProjectKey, ref.Number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.Issue{}, false
	}
	return issue, true
}

func (s *Server) uiIssueFromRouteIncludingDeleted(w http.ResponseWriter, r *http.Request) (model.Issue, bool, bool) {
	owner, ref, ok := uiIssueRouteOwnerRef(w, r)
	if !ok {
		return model.Issue{}, false, false
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(r.Context(), owner, ref.ProjectKey, ref.Number)
	if err == nil {
		return issue, false, true
	}
	if !errors.Is(err, store.ErrNotFound) {
		writeUIStoreError(w, err)
		return model.Issue{}, false, false
	}
	issue, err = s.store.GetDeletedIssueByOwnerKeyNumber(r.Context(), owner, ref.ProjectKey, ref.Number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.Issue{}, false, false
	}
	return issue, true, true
}

func uiIssueRouteOwnerRef(w http.ResponseWriter, r *http.Request) (string, issueRef, bool) {
	owner, err := store.NormalizeUsername(chi.URLParam(r, "owner"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", issueRef{}, false
	}
	ref, err := parseIssueRef(chi.URLParam(r, "issueRef"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", issueRef{}, false
	}
	return owner, ref, true
}

func (s *Server) uiDeletedIssueNotice(ctx context.Context, r *http.Request, owner string, projectID uuid.UUID) (*uiIssueDeleteNotice, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("deleted_issue"))
	if raw == "" {
		return nil, nil
	}
	ref, err := parseIssueRef(raw)
	if err != nil {
		return nil, nil
	}
	issue, err := s.store.GetDeletedIssueByOwnerKeyNumber(ctx, owner, ref.ProjectKey, ref.Number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if issue.ProjectID != projectID {
		return nil, nil
	}
	permissions, err := s.uiProjectPermissions(ctx, currentUser(r), projectID)
	if err != nil {
		return nil, err
	}
	return &uiIssueDeleteNotice{CSRFToken: uiSessionCSRFToken(r), Issue: issue, CanWrite: permissions.CanWrite}, nil
}

func (s *Server) uiIssueLinkFromRoute(w http.ResponseWriter, r *http.Request, issue model.Issue) (model.IssueLink, bool) {
	number, err := parseTypedRef(chi.URLParam(r, "linkRef"), "link")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return model.IssueLink{}, false
	}
	link, err := s.store.GetIssueLinkByProjectNumber(r.Context(), issue.ProjectID, number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.IssueLink{}, false
	}
	if link.SourceID != issue.ID && link.TargetID != issue.ID {
		writeUIStoreError(w, store.ErrNotFound)
		return model.IssueLink{}, false
	}
	return link, true
}

func (s *Server) uiCommentFromRoute(w http.ResponseWriter, r *http.Request, issue model.Issue) (model.Comment, bool) {
	number, err := parseTypedRef(chi.URLParam(r, "commentRef"), "comment")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return model.Comment{}, false
	}
	comment, err := s.store.GetCommentForIssueByNumber(r.Context(), issue.ID, number)
	if err != nil {
		writeUIStoreError(w, err)
		return model.Comment{}, false
	}
	return comment, true
}

func (s *Server) uiIssueLinkFormParams(ctx context.Context, issue model.Issue, targetRaw, relation string) (store.UpdateIssueLinkParams, string, error) {
	if targetRaw == "" {
		return store.UpdateIssueLinkParams{}, "Linked issue required.", nil
	}
	targetRef, err := parseIssueRef(targetRaw)
	if err != nil {
		return store.UpdateIssueLinkParams{}, err.Error(), nil
	}
	if err := requireIssueRefProject(targetRef, issue.ProjectKey); err != nil {
		return store.UpdateIssueLinkParams{}, "Linked issue must be in this project.", nil
	}
	target, err := s.store.GetIssueByOwnerKeyNumber(ctx, issue.OwnerUsername, targetRef.ProjectKey, targetRef.Number)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.UpdateIssueLinkParams{}, "Linked issue not found.", nil
		}
		return store.UpdateIssueLinkParams{}, "", err
	}
	if target.ProjectID != issue.ProjectID {
		return store.UpdateIssueLinkParams{}, "Linked issue must be in this project.", nil
	}
	if target.ID == issue.ID {
		return store.UpdateIssueLinkParams{}, "Choose a different issue.", nil
	}
	sourceID, targetID, linkType, ok := uiIssueLinkRelationParams(issue.ID, target.ID, relation)
	if !ok {
		return store.UpdateIssueLinkParams{}, "Choose a valid relationship.", nil
	}
	return store.UpdateIssueLinkParams{SourceID: sourceID, TargetID: targetID, LinkType: linkType}, "", nil
}

func (s *Server) uiIssueLinkTargetIdentifier(ctx context.Context, issueID uuid.UUID, link model.IssueLink) string {
	otherID := link.SourceID
	if otherID == issueID {
		otherID = link.TargetID
	}
	linked, err := s.store.GetIssue(ctx, otherID)
	if err != nil {
		return ""
	}
	return linked.Identifier
}

func redirectUILogin(w http.ResponseWriter, r *http.Request) {
	target := "/login?next=" + url.QueryEscape(uiLoginNext(r))
	if isHTMXRequest(r) {
		// htmx follows a 303 transparently and swaps the login document into
		// #main, which is a sibling of the sidebar inside .app-shell, so the
		// login card renders inside the app shell while the URL bar still
		// shows the old page. HX-Redirect makes the browser navigate instead.
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// uiLoginNext picks the address to return to after signing in. For an htmx
// request the request URI is a panel fragment such as /me/panel, which would
// leave the user staring at a fragment rendered as a whole page, so the
// browser's own address is preferred when htmx reports it.
func uiLoginNext(r *http.Request) string {
	if isHTMXRequest(r) {
		if current := strings.TrimSpace(r.Header.Get("HX-Current-URL")); current != "" {
			if parsed, err := url.Parse(current); err == nil && parsed.Path != "" {
				return safeUINext(parsed.RequestURI())
			}
		}
	}
	return safeUINext(r.URL.RequestURI())
}

func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}

func uiSetHXPushURL(w http.ResponseWriter, r *http.Request, path string) {
	uiSetHXHistoryURL(w, r, "HX-Push-Url", path)
}

func uiSetHXReplaceURL(w http.ResponseWriter, r *http.Request, path string) {
	uiSetHXHistoryURL(w, r, "HX-Replace-Url", path)
}

func uiSetHXHistoryURL(w http.ResponseWriter, r *http.Request, header, path string) {
	if !isHTMXRequest(r) {
		return
	}
	path = strings.TrimSpace(path)
	if path == "" || path == uiHTMXCurrentPath(r) {
		return
	}
	w.Header().Set(header, path)
}

func uiHTMXCurrentPath(r *http.Request) string {
	if raw := strings.TrimSpace(r.Header.Get("HX-Current-URL")); raw != "" {
		if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
			if parsed.RawQuery != "" {
				return parsed.Path + "?" + parsed.RawQuery
			}
			return parsed.Path
		}
	}
	if r.URL == nil {
		return ""
	}
	return r.URL.RequestURI()
}

func uiAppendDeletedIssueQuery(path, issueRef string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "deleted_issue=" + url.QueryEscape(issueRef)
}

func safeUINext(raw string) string {
	if raw == "" {
		return "/"
	}
	if strings.HasPrefix(raw, "//") || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "/api/v1") {
		return "/"
	}
	path, _, _ := strings.Cut(raw, "?")
	switch {
	case path == "/", path == "/me", path == "/me/panel", path == "/me/all", path == "/me/all/panel", path == "/projects", path == "/projects/panel", path == "/projects/new", path == "/projects/new/panel", path == "/issues/new", path == "/issues/new/panel", path == "/issues/new/projects", path == "/settings", path == "/tokens":
		return raw
	case safeUIIssuePath(path):
		return raw
	case safeUIProjectPath(path):
		return raw
	default:
		return "/"
	}
}

func uiIssueUserInput(user *model.User) string {
	if user == nil {
		return ""
	}
	return "@" + user.Username
}

func safeUIProjectPath(path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 || len(parts) > 8 {
		return false
	}
	if _, err := store.NormalizeUsername(parts[0]); err != nil {
		return false
	}
	if parts[1] != "projects" {
		return false
	}
	key := strings.ToUpper(parts[2])
	if !projectKeyRe.MatchString(key) {
		return false
	}
	if len(parts) == 3 {
		return true
	}
	if parts[3] == "context" {
		if len(parts) == 4 {
			return true
		}
		if len(parts) == 5 && (parts[4] == "new" || parts[4] == "panel") {
			return true
		}
		if _, err := parseTypedRef(parts[4], "context"); err != nil {
			return false
		}
		if len(parts) == 5 {
			return true
		}
		if len(parts) == 6 {
			return parts[5] == "panel" || parts[5] == "edit" || parts[5] == "delete" || parts[5] == "move-up" || parts[5] == "move-down" || parts[5] == "issues" || parts[5] == "attachments"
		}
		if len(parts) == 7 {
			if parts[5] == "issues" {
				return parts[6] == "new"
			}
			if parts[5] == "attachments" {
				_, err := parseTypedRef(parts[6], "object")
				return err == nil
			}
		}
		if len(parts) == 8 && parts[5] == "attachments" {
			if _, err := parseTypedRef(parts[6], "object"); err != nil {
				return false
			}
			return parts[7] == "content" || parts[7] == "delete"
		}
		return false
	}
	if parts[3] == "tags" {
		if len(parts) == 4 {
			return true
		}
		if _, err := parseTypedRef(parts[4], "tag"); err != nil {
			return false
		}
		if len(parts) == 5 {
			return false
		}
		return len(parts) == 6 && (parts[5] == "edit" || parts[5] == "delete")
	}
	if parts[3] == "issues" {
		if len(parts) == 5 {
			return parts[4] == "new"
		}
		return len(parts) == 6 && parts[4] == "new" && parts[5] == "panel"
	}
	if parts[3] == "name" || parts[3] == "description" {
		return len(parts) == 5 && parts[4] == "edit"
	}
	if parts[3] == "sprints" {
		if len(parts) == 5 {
			return parts[4] == "new"
		}
		if len(parts) < 5 {
			return false
		}
		if _, err := parseTypedRef(parts[4], "sprint"); err != nil {
			return false
		}
		if len(parts) == 6 {
			return parts[5] == "edit" || parts[5] == "activate" || parts[5] == "complete" || parts[5] == "delete" || parts[5] == "move-up" || parts[5] == "move-down" || parts[5] == "issues"
		}
		if len(parts) == 7 && parts[5] == "issues" {
			return parts[6] == "new"
		}
		return false
	}
	if parts[3] != "about" && parts[3] != "sprint" && parts[3] != "planned" && parts[3] != "all" && parts[3] != "changelog" && parts[3] != "backlog" && parts[3] != "deleted" {
		return false
	}
	if len(parts) == 4 {
		return true
	}
	return parts[4] == "panel" || (parts[3] == "all" && parts[4] == "page") || (parts[3] == "changelog" && parts[4] == "page")
}

func safeUIIssuePath(path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 || len(parts) > 6 {
		return false
	}
	if _, err := store.NormalizeUsername(parts[0]); err != nil {
		return false
	}
	if parts[1] != "issues" {
		return false
	}
	if _, err := parseIssueRef(parts[2]); err != nil {
		return false
	}
	if len(parts) == 3 {
		return true
	}
	if len(parts) == 4 {
		return parts[3] == "panel" || parts[3] == "links" || parts[3] == "context" || parts[3] == "tags" || parts[3] == "attachments" || parts[3] == "delete" || parts[3] == "restore"
	}
	if len(parts) == 5 {
		if parts[3] == "context" {
			if parts[4] == "new" || parts[4] == "link" {
				return true
			}
			_, err := parseTypedRef(parts[4], "context")
			return err == nil
		}
		return ((parts[3] == "title" || parts[3] == "description" || parts[3] == "status" || parts[3] == "close-reason" || parts[3] == "priority" || parts[3] == "due-date" || parts[3] == "assignee" || parts[3] == "reporter" || parts[3] == "sprint") && parts[4] == "edit") ||
			(parts[3] == "links" && parts[4] == "new") ||
			(parts[3] == "sub-issues" && parts[4] == "new")
	}
	if parts[3] == "tags" && parts[5] == "delete" {
		_, err := parseTypedRef(parts[4], "tag")
		return err == nil
	}
	if parts[3] == "attachments" && (parts[5] == "content" || parts[5] == "delete") {
		_, err := parseTypedRef(parts[4], "object")
		return err == nil
	}
	if parts[3] != "links" || parts[5] != "edit" {
		if parts[3] == "context" && (parts[5] == "delete" || parts[5] == "edit") {
			_, err := parseTypedRef(parts[4], "context")
			return err == nil
		}
		return false
	}
	_, err := parseTypedRef(parts[4], "link")
	return err == nil
}
