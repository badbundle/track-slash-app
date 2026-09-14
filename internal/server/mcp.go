package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bradleymackey/track-slash/internal/model"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
	"github.com/bradleymackey/track-slash/internal/store"
)

const (
	mcpAuthExtraKey = "track_slash_auth"
	mcpMaxBatchRefs = maxBatchIssues
)

type mcpToolOutput map[string]any

type mcpAppError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e mcpAppError) Error() string {
	return e.Code + ": " + e.Message
}

var (
	errMCPForbidden     = mcpAppError{Code: "forbidden", Message: "forbidden"}
	errMCPUnauthorized  = mcpAppError{Code: "unauthorized", Message: "unauthorized"}
	errMCPStorageAbsent = mcpAppError{Code: "unavailable", Message: "object storage unavailable"}
)

type mcpProjectInput struct {
	Owner string `json:"owner" jsonschema:"project owner username"`
	Key   string `json:"key" jsonschema:"project key"`
}

type mcpIssueInput struct {
	Owner string `json:"owner" jsonschema:"project owner username"`
	Issue string `json:"issue" jsonschema:"issue ref, for example TRACK-123"`
}

type mcpPageInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"max items to return, defaults to 50 and maxes at 200"`
	Cursor string `json:"cursor,omitempty" jsonschema:"opaque pagination cursor from previous response"`
}

type mcpProjectPageInput struct {
	mcpProjectInput
	mcpPageInput
}

type mcpIssuePageInput struct {
	mcpIssueInput
	mcpPageInput
}

type mcpCreateProjectInput struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type mcpCreateIssueInput struct {
	mcpProjectInput
	Title       string               `json:"title"`
	Description string               `json:"description,omitempty"`
	Priority    *model.IssuePriority `json:"priority,omitempty"`
	AssigneeID  *string              `json:"assignee_id,omitempty"`
	ReporterID  *string              `json:"reporter_id,omitempty"`
	DueDate     *model.Date          `json:"due_date,omitempty"`
}

type mcpListIssuesInput struct {
	mcpProjectInput
	mcpPageInput
	Status      model.Status                  `json:"status,omitempty"`
	Statuses    []model.Status                `json:"statuses,omitempty"`
	Priority    model.IssuePriority           `json:"priority,omitempty"`
	Priorities  []model.IssuePriority         `json:"priorities,omitempty"`
	AssigneeIDs []string                      `json:"assignee_ids,omitempty"`
	Tags        []string                      `json:"tags,omitempty"`
	Sort        store.ListIssuesSort          `json:"sort,omitempty" jsonschema:"one of number, created, updated, status, priority, due"`
	Direction   store.ListIssuesSortDirection `json:"direction,omitempty" jsonschema:"asc or desc"`
	Sprint      string                        `json:"sprint,omitempty" jsonschema:"sprint ref, or backlog"`
}

type mcpBatchIssuesInput struct {
	Owner string   `json:"owner"`
	Refs  []string `json:"refs"`
}

type mcpUpdateIssueInput struct {
	mcpIssueInput
	Title         *string                 `json:"title,omitempty"`
	Description   *string                 `json:"description,omitempty"`
	Status        *model.Status           `json:"status,omitempty"`
	CloseReason   *model.IssueCloseReason `json:"close_reason,omitempty"`
	Priority      *model.IssuePriority    `json:"priority,omitempty"`
	AssigneeID    *string                 `json:"assignee_id,omitempty"`
	ClearAssignee bool                    `json:"clear_assignee,omitempty"`
	ReporterID    *string                 `json:"reporter_id,omitempty"`
	ClearReporter bool                    `json:"clear_reporter,omitempty"`
	Sprint        *string                 `json:"sprint,omitempty"`
	ClearSprint   bool                    `json:"clear_sprint,omitempty"`
	DueDate       *model.Date             `json:"due_date,omitempty"`
	ClearDueDate  bool                    `json:"clear_due_date,omitempty"`
}

type mcpCreateSubIssueInput struct {
	mcpIssueInput
	Title       string               `json:"title"`
	Description string               `json:"description,omitempty"`
	Priority    *model.IssuePriority `json:"priority,omitempty"`
	AssigneeID  *string              `json:"assignee_id,omitempty"`
	ReporterID  *string              `json:"reporter_id,omitempty"`
	DueDate     *model.Date          `json:"due_date,omitempty"`
}

type mcpCommentInput struct {
	mcpIssueInput
	Comment string `json:"comment" jsonschema:"comment ref, for example comment-1"`
}

type mcpCreateCommentInput struct {
	mcpIssueInput
	Body string `json:"body"`
}

type mcpUpdateCommentInput struct {
	mcpCommentInput
	Body string `json:"body"`
}

type mcpSprintInput struct {
	mcpProjectInput
	Sprint string `json:"sprint" jsonschema:"sprint ref, for example sprint-1"`
}

type mcpCreateSprintInput struct {
	mcpProjectInput
	Name      string  `json:"name,omitempty"`
	Goal      string  `json:"goal,omitempty"`
	StartDate *string `json:"start_date,omitempty"`
	EndDate   *string `json:"end_date,omitempty"`
}

type mcpListSprintsInput struct {
	mcpProjectInput
	mcpPageInput
	Status model.SprintStatus    `json:"status,omitempty"`
	Sort   store.ListSprintsSort `json:"sort,omitempty" jsonschema:"completed"`
}

type mcpListSprintHistoryIssuesInput struct {
	mcpSprintInput
	mcpPageInput
}

type mcpUpdateSprintInput struct {
	mcpSprintInput
	Name       *string             `json:"name,omitempty"`
	Goal       *string             `json:"goal,omitempty"`
	StartDate  *string             `json:"start_date,omitempty"`
	EndDate    *string             `json:"end_date,omitempty"`
	ClearDates bool                `json:"clear_dates,omitempty"`
	Status     *model.SprintStatus `json:"status,omitempty"`
}

type mcpReorderSprintsInput struct {
	mcpProjectInput
	SprintRefs []string `json:"sprint_refs"`
}

type mcpTagInput struct {
	mcpProjectInput
	Tag string `json:"tag" jsonschema:"tag ref, for example tag-1"`
}

type mcpCreateTagInput struct {
	mcpProjectInput
	Name  string              `json:"name"`
	Color model.IssueTagColor `json:"color,omitempty"`
}

type mcpUpdateTagInput struct {
	mcpTagInput
	Name  *string              `json:"name,omitempty"`
	Color *model.IssueTagColor `json:"color,omitempty"`
}

type mcpAttachTagInput struct {
	mcpIssueInput
	Tag    string `json:"tag,omitempty" jsonschema:"tag name"`
	TagRef string `json:"tag_ref,omitempty" jsonschema:"tag ref, for example tag-1"`
}

type mcpIssueTagInput struct {
	mcpIssueInput
	Tag string `json:"tag" jsonschema:"tag ref, for example tag-1"`
}

type mcpLinkInput struct {
	mcpProjectInput
	Link string `json:"link" jsonschema:"link ref, for example link-1"`
}

type mcpCreateLinkInput struct {
	mcpIssueInput
	TargetIssue string         `json:"target_issue"`
	LinkType    model.LinkType `json:"link_type"`
}

type mcpUpdateLinkInput struct {
	mcpLinkInput
	SourceIssue string         `json:"source_issue"`
	TargetIssue string         `json:"target_issue"`
	LinkType    model.LinkType `json:"link_type"`
}

type mcpContextInput struct {
	mcpProjectInput
	Context string `json:"context" jsonschema:"context ref, for example context-1"`
}

type mcpCreateProjectContextInput struct {
	mcpProjectInput
	Title       string `json:"title"`
	Body        string `json:"body"`
	ContentType string `json:"content_type,omitempty"`
}

type mcpUpdateProjectContextInput struct {
	mcpContextInput
	Title       *string `json:"title,omitempty"`
	Body        *string `json:"body,omitempty"`
	ContentType *string `json:"content_type,omitempty"`
	Position    *int64  `json:"position,omitempty"`
}

type mcpCreateIssueContextInput struct {
	mcpIssueInput
	Context    string `json:"context,omitempty"`
	ContextRef string `json:"context_ref,omitempty"`
	Title      string `json:"title,omitempty"`
	Body       string `json:"body,omitempty"`
}

type mcpBulkLinkIssueContextsInput struct {
	mcpProjectInput
	Links []issueContextLinkReq `json:"links"`
}

type mcpIssueContextInput struct {
	mcpIssueInput
	Context string `json:"context" jsonschema:"context ref, for example context-1"`
}

type mcpContextAttachmentInput struct {
	mcpContextInput
	Object string `json:"object" jsonschema:"object ref, for example object-1"`
}

type mcpCreateContextAttachmentInput struct {
	mcpContextInput
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
}

type mcpContextAttachmentsPageInput struct {
	mcpContextInput
	mcpPageInput
}

type mcpObjectInput struct {
	mcpProjectInput
	Object string `json:"object" jsonschema:"object ref, for example object-1"`
}

type mcpCreateObjectInput struct {
	mcpProjectInput
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
}

type mcpAttachmentInput struct {
	mcpIssueInput
	Object string `json:"object" jsonschema:"object ref, for example object-1"`
}

type mcpCreateAttachmentInput struct {
	mcpIssueInput
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
}

type mcpCreateProjectAttachmentInput struct {
	mcpProjectInput
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
}

type mcpSprintAttachmentInput struct {
	mcpSprintInput
	Object string `json:"object" jsonschema:"object ref, for example object-1"`
}

type mcpCreateSprintAttachmentInput struct {
	mcpSprintInput
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64"`
}

type mcpSprintAttachmentsPageInput struct {
	mcpSprintInput
	mcpPageInput
}

type mcpMemberInput struct {
	mcpProjectInput
	Username string `json:"username"`
	Role     string `json:"role,omitempty" jsonschema:"member or readonly; defaults to member"`
}

type mcpUpdateProjectAccessInput struct {
	mcpProjectInput
	IsPublic            bool `json:"is_public"`
	PublicIssueCreation bool `json:"public_issue_creation"`
}

type mcpProjectBlockInput struct {
	mcpProjectInput
	Username string `json:"username"`
}

type mcpSearchMembersInput struct {
	mcpProjectInput
	Limit int    `json:"limit,omitempty"`
	Query string `json:"query,omitempty"`
}

type mcpUserInput struct {
	ID uuid.UUID `json:"id"`
}

type mcpCreateUserInput struct {
	Username string `json:"username,omitempty"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name"`
}

type mcpUpdateMySettingsInput struct {
	Name            *string `json:"name,omitempty"`
	Email           *string `json:"email,omitempty"`
	CurrentPassword string  `json:"current_password,omitempty"`
	NewPassword     string  `json:"new_password,omitempty"`
}

type mcpCreateTokenInput struct {
	Name      string               `json:"name"`
	Kind      *model.AuthTokenKind `json:"kind,omitempty"`
	ExpiresAt *time.Time           `json:"expires_at,omitempty"`
}

type mcpCreateUserTokenInput struct {
	UserID uuid.UUID `json:"user_id"`
	mcpCreateTokenInput
}

type mcpTokenInput struct {
	ID uuid.UUID `json:"id"`
}

type mcpListUserTokensInput struct {
	UserID uuid.UUID `json:"user_id"`
}

func (s *Server) mountMCPRoutes(r chi.Router) {
	mcpServer := s.newMCPServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		JSONResponse:   true,
		Stateless:      true,
		SessionTimeout: 30 * time.Minute,
	})
	authenticated := mcpauth.RequireBearerToken(s.verifyMCPBearerToken, nil)(handler)
	r.Handle("/mcp", s.mcpOriginMiddleware(mcpBearerChallengeMiddleware(authenticated)))
}

func (s *Server) mcpOriginMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" && len(s.corsAllowedOrigins) > 0 {
			allowed := false
			for _, candidate := range s.corsAllowedOrigins {
				if origin == candidate {
					allowed = true
					break
				}
			}
			if !allowed {
				writeForbidden(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) verifyMCPBearerToken(ctx context.Context, raw string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	auth, err := s.store.AuthenticateToken(ctx, raw)
	if err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			return nil, mcpauth.ErrInvalidToken
		}
		return nil, err
	}
	if auth.Token.Kind != model.AuthTokenKindAPI {
		return nil, mcpauth.ErrInvalidToken
	}
	expires := time.Now().Add(100 * 365 * 24 * time.Hour)
	if auth.Token.ExpiresAt != nil {
		expires = *auth.Token.ExpiresAt
	}
	return &mcpauth.TokenInfo{
		Expiration: expires,
		UserID:     auth.User.ID.String(),
		Extra: map[string]any{
			mcpAuthExtraKey: authContext{User: auth.User, Token: auth.Token},
		},
	}, nil
}

func (s *Server) newMCPServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "track-slash", Version: "v1"}, &mcp.ServerOptions{
		Instructions: "Use trackslash tools to read and update issue-tracking data. Prefer refs like KEY-123, sprint-1, tag-1, link-1, context-1, object-1.",
	})

	readOnly := true
	write := false

	addMCPTool(srv, "track_get_me", "Return current MCP-authenticated user and token kind.", readOnly, s.mcpGetMe)
	addMCPTool(srv, "track_update_my_settings", "Update current user profile or password.", write, s.mcpUpdateMySettings)

	addMCPTool(srv, "track_create_project", "Create a project owned by current user.", write, s.mcpCreateProject)
	addMCPTool(srv, "track_list_projects", "List projects visible to current user.", readOnly, s.mcpListProjects)
	addMCPTool(srv, "track_get_project", "Get project by owner and key.", readOnly, s.mcpGetProject)
	addMCPTool(srv, "track_delete_project", "Soft-delete a project. Project owner or admin only.", write, s.mcpDeleteProject)
	addMCPTool(srv, "track_list_project_members", "List project members and roles.", readOnly, s.mcpListProjectMembers)
	addMCPTool(srv, "track_grant_project_member", "Add a project member or update their role. Project owner or admin only.", write, s.mcpGrantProjectMember)
	addMCPTool(srv, "track_revoke_project_member", "Remove a project member. Project owner or admin only.", write, s.mcpRevokeProjectMember)
	addMCPTool(srv, "track_get_project_access", "Get public access settings for project.", readOnly, s.mcpGetProjectAccess)
	addMCPTool(srv, "track_update_project_access", "Update public access settings. Project owner or admin only.", write, s.mcpUpdateProjectAccess)
	addMCPTool(srv, "track_list_project_blocks", "List users blocked from project. Project owner or admin only.", readOnly, s.mcpListProjectBlocks)
	addMCPTool(srv, "track_block_project_user", "Block user from project. Project owner or admin only.", write, s.mcpBlockProjectUser)
	addMCPTool(srv, "track_unblock_project_user", "Unblock user from project. Project owner or admin only.", write, s.mcpUnblockProjectUser)
	addMCPTool(srv, "track_search_project_members", "Search users with access to project.", readOnly, s.mcpSearchProjectMembers)
	addMCPTool(srv, "track_search_project_member_candidates", "Search existing users who can be added to a project. Queries shorter than two characters return no candidates. Project owner or admin only.", readOnly, s.mcpSearchProjectMemberCandidates)
	addMCPTool(srv, "track_list_project_assignees", "List assignable users for project.", readOnly, s.mcpListProjectAssignees)
	addMCPTool(srv, "track_get_project_stats", "Get project issue status stats.", readOnly, s.mcpGetProjectStats)
	addMCPTool(srv, "track_list_project_changelog", "List project changelog entries.", readOnly, s.mcpListProjectChangelog)

	addMCPTool(srv, "track_create_issue", "Create issue in project.", write, s.mcpCreateIssue)
	addMCPTool(srv, "track_list_issues", "List project issues.", readOnly, s.mcpListIssues)
	addMCPTool(srv, "track_list_deleted_issues", "List deleted project issues.", readOnly, s.mcpListDeletedIssues)
	addMCPTool(srv, "track_batch_get_issues", "Get visible issues by refs.", readOnly, s.mcpBatchIssues)
	addMCPTool(srv, "track_get_issue", "Get issue by ref.", readOnly, s.mcpGetIssue)
	addMCPTool(srv, "track_update_issue", "Update issue fields.", write, s.mcpUpdateIssue)
	addMCPTool(srv, "track_delete_issue", "Soft-delete issue.", write, s.mcpDeleteIssue)
	addMCPTool(srv, "track_restore_issue", "Restore deleted issue.", write, s.mcpRestoreIssue)
	addMCPTool(srv, "track_create_sub_issue", "Create sub-issue under an issue.", write, s.mcpCreateSubIssue)
	addMCPTool(srv, "track_list_sub_issues", "List sub-issues under an issue.", readOnly, s.mcpListSubIssues)

	addMCPTool(srv, "track_create_comment", "Create issue comment.", write, s.mcpCreateComment)
	addMCPTool(srv, "track_list_comments", "List issue comments.", readOnly, s.mcpListComments)
	addMCPTool(srv, "track_get_comment", "Get issue comment.", readOnly, s.mcpGetComment)
	addMCPTool(srv, "track_update_comment", "Update own issue comment.", write, s.mcpUpdateComment)
	addMCPTool(srv, "track_delete_comment", "Delete issue comment.", write, s.mcpDeleteComment)

	addMCPTool(srv, "track_create_sprint", "Create project sprint.", write, s.mcpCreateSprint)
	addMCPTool(srv, "track_list_sprints", "List project sprints.", readOnly, s.mcpListSprints)
	addMCPTool(srv, "track_list_sprint_history_issues", "List issues captured when a completed sprint finished.", readOnly, s.mcpListSprintHistoryIssues)
	addMCPTool(srv, "track_get_sprint", "Get project sprint.", readOnly, s.mcpGetSprint)
	addMCPTool(srv, "track_update_sprint", "Update project sprint.", write, s.mcpUpdateSprint)
	addMCPTool(srv, "track_delete_sprint", "Delete project sprint.", write, s.mcpDeleteSprint)
	addMCPTool(srv, "track_complete_sprint", "Complete project sprint.", write, s.mcpCompleteSprint)
	addMCPTool(srv, "track_reorder_planned_sprints", "Reorder planned sprints.", write, s.mcpReorderPlannedSprints)

	addMCPTool(srv, "track_create_tag", "Create project issue tag.", write, s.mcpCreateTag)
	addMCPTool(srv, "track_list_tags", "List project issue tags.", readOnly, s.mcpListTags)
	addMCPTool(srv, "track_get_tag", "Get project issue tag.", readOnly, s.mcpGetTag)
	addMCPTool(srv, "track_update_tag", "Update project issue tag.", write, s.mcpUpdateTag)
	addMCPTool(srv, "track_delete_tag", "Delete project issue tag.", write, s.mcpDeleteTag)
	addMCPTool(srv, "track_attach_issue_tag", "Attach tag to issue.", write, s.mcpAttachIssueTag)
	addMCPTool(srv, "track_list_issue_tags", "List tags attached to issue.", readOnly, s.mcpListIssueTags)
	addMCPTool(srv, "track_detach_issue_tag", "Detach tag from issue.", write, s.mcpDetachIssueTag)

	addMCPTool(srv, "track_create_link", "Create issue link.", write, s.mcpCreateLink)
	addMCPTool(srv, "track_list_issue_links", "List links touching issue.", readOnly, s.mcpListIssueLinks)
	addMCPTool(srv, "track_get_link", "Get project issue link.", readOnly, s.mcpGetLink)
	addMCPTool(srv, "track_update_link", "Update project issue link.", write, s.mcpUpdateLink)
	addMCPTool(srv, "track_delete_link", "Delete project issue link.", write, s.mcpDeleteLink)

	addMCPTool(srv, "track_create_project_context", "Create project context item.", write, s.mcpCreateProjectContext)
	addMCPTool(srv, "track_list_project_context", "List project context items.", readOnly, s.mcpListProjectContext)
	addMCPTool(srv, "track_get_project_context", "Get project context item.", readOnly, s.mcpGetProjectContext)
	addMCPTool(srv, "track_update_project_context", "Update project context item.", write, s.mcpUpdateProjectContext)
	addMCPTool(srv, "track_delete_project_context", "Delete project context item.", write, s.mcpDeleteProjectContext)
	addMCPTool(srv, "track_create_project_context_attachment", "Upload and attach file to project context.", write, s.mcpCreateContextAttachment)
	addMCPTool(srv, "track_list_project_context_attachments", "List files attached to project context.", readOnly, s.mcpListContextAttachments)
	addMCPTool(srv, "track_read_project_context_attachment_content", "Read project context attachment content as base64.", readOnly, s.mcpReadContextAttachmentContent)
	addMCPTool(srv, "track_delete_project_context_attachment", "Delete project context attachment and owned object.", write, s.mcpDeleteContextAttachment)
	addMCPTool(srv, "track_create_issue_context", "Create issue-scoped context or link project context to issue.", write, s.mcpCreateIssueContext)
	addMCPTool(srv, "track_bulk_link_issue_contexts", "Link up to 200 issue and project-context pairs atomically.", write, s.mcpBulkLinkIssueContexts)
	addMCPTool(srv, "track_list_issue_context", "List context items linked to issue.", readOnly, s.mcpListIssueContext)
	addMCPTool(srv, "track_delete_issue_context", "Delete issue-context link.", write, s.mcpDeleteIssueContext)

	addMCPTool(srv, "track_create_object", "Upload project storage object from base64 content.", write, s.mcpCreateObject)
	addMCPTool(srv, "track_list_objects", "List project storage objects.", readOnly, s.mcpListObjects)
	addMCPTool(srv, "track_get_object", "Get project storage object metadata.", readOnly, s.mcpGetObject)
	addMCPTool(srv, "track_read_object_content", "Read project storage object content as base64.", readOnly, s.mcpReadObjectContent)
	addMCPTool(srv, "track_delete_object", "Delete project storage object.", write, s.mcpDeleteObject)
	addMCPTool(srv, "track_create_attachment", "Upload and attach file to issue from base64 content.", write, s.mcpCreateAttachment)
	addMCPTool(srv, "track_list_attachments", "List issue attachments.", readOnly, s.mcpListAttachments)
	addMCPTool(srv, "track_read_attachment_content", "Read issue attachment content as base64.", readOnly, s.mcpReadAttachmentContent)
	addMCPTool(srv, "track_delete_attachment", "Delete issue attachment and owned object.", write, s.mcpDeleteAttachment)
	addMCPTool(srv, "track_create_project_attachment", "Upload and attach file to project description from base64 content.", write, s.mcpCreateProjectAttachment)
	addMCPTool(srv, "track_list_project_attachments", "List project description attachments.", readOnly, s.mcpListProjectAttachments)
	addMCPTool(srv, "track_read_project_attachment_content", "Read project description attachment content as base64.", readOnly, s.mcpReadProjectAttachmentContent)
	addMCPTool(srv, "track_delete_project_attachment", "Delete project description attachment and owned object.", write, s.mcpDeleteProjectAttachment)
	addMCPTool(srv, "track_create_sprint_attachment", "Upload and attach file to sprint from base64 content.", write, s.mcpCreateSprintAttachment)
	addMCPTool(srv, "track_list_sprint_attachments", "List sprint attachments.", readOnly, s.mcpListSprintAttachments)
	addMCPTool(srv, "track_read_sprint_attachment_content", "Read sprint attachment content as base64.", readOnly, s.mcpReadSprintAttachmentContent)
	addMCPTool(srv, "track_delete_sprint_attachment", "Delete sprint attachment and owned object.", write, s.mcpDeleteSprintAttachment)

	addMCPTool(srv, "track_create_user", "Create user profile. Admin only.", write, s.mcpCreateUser)
	addMCPTool(srv, "track_list_users", "List users. Admin only.", readOnly, s.mcpListUsers)
	addMCPTool(srv, "track_get_user", "Get user. Admin only.", readOnly, s.mcpGetUser)
	addMCPTool(srv, "track_delete_user", "Delete user. Admin only.", write, s.mcpDeleteUser)
	addMCPTool(srv, "track_create_my_token", "Create API token for current user.", write, s.mcpCreateMyToken)
	addMCPTool(srv, "track_list_my_tokens", "List auth tokens for current user.", readOnly, s.mcpListMyTokens)
	addMCPTool(srv, "track_revoke_my_token", "Revoke current user's token.", write, s.mcpRevokeMyToken)
	addMCPTool(srv, "track_create_user_token", "Create auth token for user. Admin only.", write, s.mcpCreateUserToken)
	addMCPTool(srv, "track_list_user_tokens", "List auth tokens for user. Admin only.", readOnly, s.mcpListUserTokens)
	addMCPTool(srv, "track_revoke_token", "Revoke any auth token. Admin only.", write, s.mcpRevokeToken)

	s.addMCPResources(srv)
	s.addMCPPrompts(srv)
	return srv
}

func addMCPTool[In any](srv *mcp.Server, name, description string, readOnly bool, handler func(context.Context, *mcp.CallToolRequest, In) (mcpToolOutput, error)) {
	openWorld := false
	destructive := !readOnly
	mcp.AddTool(srv, &mcp.Tool{
		Name:        name,
		Title:       strings.TrimPrefix(name, "track_"),
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    readOnly,
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, mcpToolOutput, error) {
		out, err := handler(ctx, req, input)
		if err != nil {
			result, errOut := mcpErrorResult(err)
			return result, errOut, nil
		}
		if out == nil {
			out = mcpToolOutput{"ok": true}
		}
		return nil, out, nil
	})
}

func mcpErrorResult(err error) (*mcp.CallToolResult, mcpToolOutput) {
	appErr := normalizeMCPError(err)
	data, _ := json.Marshal(appErr)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
		IsError: true,
	}, mcpToolOutput{"error": appErr}
}

func normalizeMCPError(err error) mcpAppError {
	var appErr mcpAppError
	if errors.As(err, &appErr) {
		return appErr
	}
	switch {
	case errors.Is(err, store.ErrUnauthorized):
		return errMCPUnauthorized
	case errors.Is(err, store.ErrNotFound), errors.Is(err, objectstorage.ErrNotFound):
		return mcpAppError{Code: "not_found", Message: "not found"}
	case errors.Is(err, store.ErrConflict), errors.Is(err, objectstorage.ErrExists), errors.Is(err, objectstorage.ErrInvalidKey):
		return mcpAppError{Code: "conflict", Message: err.Error()}
	case errors.Is(err, objectstorage.ErrTooLarge):
		return mcpAppError{Code: "too_large", Message: "file too large"}
	default:
		logInternalError("mcp tool", err)
		return mcpAppError{Code: "internal_error", Message: "internal error"}
	}
}

func validationError(msg string) error {
	return mcpAppError{Code: "validation_error", Message: msg}
}

func (s *Server) mcpAuth(ctx context.Context, req *mcp.CallToolRequest) (context.Context, authContext, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return ctx, authContext{}, errMCPUnauthorized
	}
	auth, ok := req.Extra.TokenInfo.Extra[mcpAuthExtraKey].(authContext)
	if !ok {
		return ctx, authContext{}, errMCPUnauthorized
	}
	ctx = store.WithActor(ctx, auth.User.ID)
	return ctx, auth, nil
}

func (s *Server) requireMCPAdmin(auth authContext) error {
	if !auth.User.IsAdmin {
		return errMCPForbidden
	}
	return nil
}

func (s *Server) requireMCPProjectAccess(ctx context.Context, auth authContext, projectID uuid.UUID) error {
	ok, err := s.store.UserCanAccessProject(ctx, auth.User, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errMCPForbidden
	}
	return nil
}

func (s *Server) requireMCPProjectWriteAccess(ctx context.Context, auth authContext, projectID uuid.UUID) error {
	ok, err := s.store.UserCanWriteProject(ctx, auth.User, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errMCPForbidden
	}
	return nil
}

func (s *Server) requireMCPProjectIssueCreation(ctx context.Context, auth authContext, projectID uuid.UUID) (store.ProjectPermissions, error) {
	permissions, err := s.store.ProjectPermissionsForUser(ctx, auth.User, projectID)
	if err != nil {
		return store.ProjectPermissions{}, err
	}
	if !permissions.CanCreateIssues {
		return store.ProjectPermissions{}, errMCPForbidden
	}
	return permissions, nil
}

func (s *Server) requireMCPProjectDeletion(ctx context.Context, auth authContext, projectID uuid.UUID) error {
	ok, err := s.store.UserCanDeleteProject(ctx, auth.User, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errMCPForbidden
	}
	return nil
}

func (s *Server) requireMCPProjectMemberManagement(ctx context.Context, auth authContext, projectID uuid.UUID) error {
	ok, err := s.store.UserCanManageProjectMembers(ctx, auth.User, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errMCPForbidden
	}
	return nil
}

func mcpLimit(raw int) (int, error) {
	if raw == 0 {
		return DefaultLimit, nil
	}
	if raw < 0 {
		return 0, errInvalidLimit
	}
	if raw > MaxLimit {
		return MaxLimit, nil
	}
	return raw, nil
}

func mcpPageOut(items any, next *string) mcpToolOutput {
	return mcpToolOutput{"items": items, "next_cursor": next}
}

func mcpOK() mcpToolOutput {
	return mcpToolOutput{"ok": true}
}

func normalizeMCPProjectKey(raw string) (string, error) {
	key := strings.ToUpper(strings.TrimSpace(raw))
	if !projectKeyRe.MatchString(key) {
		return "", validationError("invalid project key")
	}
	return key, nil
}

func normalizeMCPOwner(raw string) (string, error) {
	owner, err := store.NormalizeUsername(raw)
	if err != nil {
		return "", validationError(err.Error())
	}
	return owner, nil
}

func (s *Server) mcpProject(ctx context.Context, auth authContext, input mcpProjectInput) (model.Project, error) {
	owner, err := normalizeMCPOwner(input.Owner)
	if err != nil {
		return model.Project{}, err
	}
	key, err := normalizeMCPProjectKey(input.Key)
	if err != nil {
		return model.Project{}, err
	}
	project, err := s.store.GetProjectByOwnerKey(ctx, owner, key)
	if err != nil {
		return model.Project{}, err
	}
	if err := s.requireMCPProjectAccess(ctx, auth, project.ID); err != nil {
		return model.Project{}, err
	}
	return project, nil
}

func (s *Server) mcpIssue(ctx context.Context, auth authContext, input mcpIssueInput) (model.Issue, error) {
	owner, err := normalizeMCPOwner(input.Owner)
	if err != nil {
		return model.Issue{}, err
	}
	ref, err := parseIssueRef(input.Issue)
	if err != nil {
		return model.Issue{}, validationError(err.Error())
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(ctx, owner, ref.ProjectKey, ref.Number)
	if err != nil {
		return model.Issue{}, err
	}
	if err := s.requireMCPProjectAccess(ctx, auth, issue.ProjectID); err != nil {
		return model.Issue{}, err
	}
	return issue, nil
}

func (s *Server) mcpDeletedIssue(ctx context.Context, auth authContext, input mcpIssueInput) (model.Issue, error) {
	owner, err := normalizeMCPOwner(input.Owner)
	if err != nil {
		return model.Issue{}, err
	}
	ref, err := parseIssueRef(input.Issue)
	if err != nil {
		return model.Issue{}, validationError(err.Error())
	}
	issue, err := s.store.GetDeletedIssueByOwnerKeyNumber(ctx, owner, ref.ProjectKey, ref.Number)
	if err != nil {
		return model.Issue{}, err
	}
	if err := s.requireMCPProjectAccess(ctx, auth, issue.ProjectID); err != nil {
		return model.Issue{}, err
	}
	return issue, nil
}

func mcpTypedRef(raw, prefix string) (int, error) {
	n, err := parseTypedRef(raw, prefix)
	if err != nil {
		return 0, validationError(err.Error())
	}
	return n, nil
}

func validateMCPTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" || len(title) > 200 {
		return "", validationError("title required, max 200 chars")
	}
	return title, nil
}

func mcpPriority(raw *model.IssuePriority) (model.IssuePriority, error) {
	priority := model.PriorityP2
	if raw != nil {
		if !raw.Valid() {
			return "", validationError("invalid priority")
		}
		priority = *raw
	}
	return priority, nil
}

func mcpReporter(auth authContext, reporterID *uuid.UUID) (*uuid.UUID, error) {
	if reporterID == nil {
		id := auth.User.ID
		return &id, nil
	}
	if !auth.User.IsAdmin && *reporterID != auth.User.ID {
		return nil, errMCPForbidden
	}
	return reporterID, nil
}

func mcpOptionalUUID(raw *string, field string) (*uuid.UUID, error) {
	if raw == nil {
		return nil, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(*raw))
	if err != nil {
		return nil, validationError(field + " must be a UUID")
	}
	return &id, nil
}

func mcpUUIDs(raw []string, field string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		id, err := uuid.Parse(strings.TrimSpace(value))
		if err != nil {
			return nil, validationError(field + " must contain only UUIDs")
		}
		out = append(out, id)
	}
	return out, nil
}

func validateMCPTokenInput(name string, kind *model.AuthTokenKind, expiresAt *time.Time) (string, model.AuthTokenKind, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return "", "", validationError("name required, max 200 chars")
	}
	outKind := model.AuthTokenKindAPI
	if kind != nil {
		if !kind.Valid() {
			return "", "", validationError("invalid token kind")
		}
		outKind = *kind
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return "", "", validationError("expires_at must be in the future")
	}
	return name, outKind, nil
}

func (s *Server) mcpGetMe(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (mcpToolOutput, error) {
	_, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"user": auth.User, "token_kind": auth.Token.Kind}, nil
}

func (s *Server) mcpUpdateMySettings(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateMySettingsInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	changed := false
	user := auth.User
	if input.Name != nil || input.Email != nil {
		name := user.Name
		email := user.Email
		if input.Name != nil {
			name = *input.Name
		}
		if input.Email != nil {
			email = *input.Email
		}
		if strings.TrimSpace(name) == "" {
			return nil, validationError("name required")
		}
		if err := store.ValidateEmail(email); err != nil {
			return nil, validationError(err.Error())
		}
		user, err = s.store.UpdateUserProfile(ctx, user.ID, name, email)
		if err != nil {
			return nil, err
		}
		changed = true
	}
	if input.CurrentPassword != "" || input.NewPassword != "" {
		if input.CurrentPassword == "" || input.NewPassword == "" {
			return nil, validationError("current_password and new_password required")
		}
		if err := store.ValidatePassword(input.NewPassword); err != nil {
			return nil, validationError(err.Error())
		}
		if err := s.store.ChangePassword(ctx, auth.User.ID, input.CurrentPassword, input.NewPassword); err != nil {
			return nil, err
		}
		changed = true
	}
	if !changed {
		return nil, validationError("settings change required")
	}
	return mcpToolOutput{"user": user}, nil
}

func (s *Server) mcpCreateProject(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateProjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(input.Key)
	name := strings.TrimSpace(input.Name)
	if !projectKeyRe.MatchString(key) {
		return nil, validationError("key must match ^[A-Z][A-Z0-9]{1,9}$")
	}
	if name == "" {
		return nil, validationError("name required")
	}
	project, err := s.store.CreateProjectForUser(ctx, auth.User.ID, key, name, input.Description)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"project": project}, nil
}

func (s *Server) mcpListProjects(ctx context.Context, req *mcp.CallToolRequest, input mcpPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.ProjectsCursor
	if input.Cursor != "" {
		var c store.ProjectsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	projects, hasMore, err := s.store.ListProjects(ctx, store.ListProjectsParams{
		Cursor:        cursor,
		Limit:         limit,
		VisibleToUser: visibleProjectUser(auth.User),
	})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := projects[len(projects)-1]
		enc := encodeCursor(store.ProjectsCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		next = &enc
	}
	return mcpPageOut(projects, next), nil
}

func (s *Server) mcpGetProject(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"project": project}, nil
}

func (s *Server) mcpDeleteProject(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectDeletion(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	if err := s.store.DeleteProject(ctx, project.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpListProjectMembers(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	members, err := s.store.ListProjectMembers(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"members": members}, nil
}

func (s *Server) mcpGrantProjectMember(ctx context.Context, req *mcp.CallToolRequest, input mcpMemberInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectMemberManagement(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	username, err := store.NormalizeUsername(input.Username)
	if err != nil {
		return nil, validationError(err.Error())
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	role := model.ProjectMemberRole(input.Role)
	if role == "" {
		role = model.ProjectMemberRoleMember
	}
	if !role.Valid() {
		return nil, validationError("role must be member or readonly")
	}
	member, err := s.store.SetProjectMemberRole(ctx, project.ID, user.ID, role)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"member": member}, nil
}

func (s *Server) mcpRevokeProjectMember(ctx context.Context, req *mcp.CallToolRequest, input mcpMemberInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectMemberManagement(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	username, err := store.NormalizeUsername(input.Username)
	if err != nil {
		return nil, validationError(err.Error())
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if err := s.store.RevokeProjectAccess(ctx, project.ID, user.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpGetProjectAccess(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	settings, err := s.store.GetProjectAccessSettings(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"access": settings}, nil
}

func (s *Server) mcpUpdateProjectAccess(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateProjectAccessInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectMemberManagement(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	settings, err := s.store.UpdateProjectAccessSettings(ctx, project.ID, model.ProjectAccessSettings{
		IsPublic:            input.IsPublic,
		PublicIssueCreation: input.PublicIssueCreation,
	})
	if err != nil {
		return nil, err
	}
	s.disconnectRealtimeClients()
	return mcpToolOutput{"access": settings}, nil
}

func (s *Server) mcpListProjectBlocks(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectMemberManagement(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	blocks, err := s.store.ListProjectUserBlocks(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"blocks": blocks}, nil
}

func (s *Server) mcpBlockProjectUser(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectBlockInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectMemberManagement(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	username, err := store.NormalizeUsername(input.Username)
	if err != nil {
		return nil, validationError(err.Error())
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	block, err := s.store.BlockProjectUser(ctx, project.ID, user.ID, auth.User.ID)
	if err != nil {
		return nil, err
	}
	s.disconnectRealtimeClients()
	return mcpToolOutput{"block": block}, nil
}

func (s *Server) mcpUnblockProjectUser(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectBlockInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectMemberManagement(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	username, err := store.NormalizeUsername(input.Username)
	if err != nil {
		return nil, validationError(err.Error())
	}
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if err := s.store.UnblockProjectUser(ctx, project.ID, user.ID); err != nil {
		return nil, err
	}
	s.disconnectRealtimeClients()
	return mcpOK(), nil
}

func (s *Server) mcpSearchProjectMembers(ctx context.Context, req *mcp.CallToolRequest, input mcpSearchMembersInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	users, err := s.store.SearchProjectMembers(ctx, store.SearchProjectMembersParams{
		ProjectID: project.ID,
		Query:     input.Query,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"users": safeProjectMemberIdentities(users)}, nil
}

func (s *Server) mcpSearchProjectMemberCandidates(ctx context.Context, req *mcp.CallToolRequest, input mcpSearchMembersInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectMemberManagement(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	candidates, err := s.store.SearchAvailableProjectMembers(ctx, store.SearchAvailableProjectMembersParams{
		ProjectID: project.ID,
		Query:     input.Query,
		Limit:     min(limit, store.ProjectMemberCandidateLimit),
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"users": candidates}, nil
}

func (s *Server) mcpListProjectAssignees(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	assignees, err := s.store.ListProjectAssignees(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"assignees": assignees}, nil
}

func (s *Server) mcpGetProjectStats(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	stats, err := s.store.GetProjectStats(ctx, store.ProjectStatsParams{ProjectID: project.ID})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"stats": stats}, nil
}

func (s *Server) mcpListProjectChangelog(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.ProjectChangelogCursor
	if input.Cursor != "" {
		var c store.ProjectChangelogCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	entries, hasMore, err := s.store.ListProjectChangelog(ctx, store.ListProjectChangelogParams{
		ProjectID: project.ID,
		Cursor:    cursor,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := entries[len(entries)-1]
		enc := encodeCursor(store.ProjectChangelogCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		next = &enc
	}
	return mcpPageOut(entries, next), nil
}

func (s *Server) mcpCreateIssue(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateIssueInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	permissions, err := s.requireMCPProjectIssueCreation(ctx, auth, project.ID)
	if err != nil {
		return nil, err
	}
	title, err := validateMCPTitle(input.Title)
	if err != nil {
		return nil, err
	}
	priority, err := mcpPriority(input.Priority)
	if err != nil {
		return nil, err
	}
	assigneeID, err := mcpOptionalUUID(input.AssigneeID, "assignee_id")
	if err != nil {
		return nil, err
	}
	if !permissions.CanWrite && assigneeID != nil {
		return nil, errMCPForbidden
	}
	inputReporterID, err := mcpOptionalUUID(input.ReporterID, "reporter_id")
	if err != nil {
		return nil, err
	}
	reporterID, err := mcpReporter(auth, inputReporterID)
	if err != nil {
		return nil, err
	}
	issue, err := s.store.CreateIssue(ctx, store.CreateIssueParams{
		ProjectID:   project.ID,
		Title:       title,
		Description: input.Description,
		Priority:    priority,
		AssigneeID:  assigneeID,
		ReporterID:  reporterID,
		DueDate:     input.DueDate,
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"issue": issue}, nil
}

func (s *Server) mcpCreateSubIssue(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateSubIssueInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	parent, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, parent.ProjectID); err != nil {
		return nil, err
	}
	title, err := validateMCPTitle(input.Title)
	if err != nil {
		return nil, err
	}
	priority, err := mcpPriority(input.Priority)
	if err != nil {
		return nil, err
	}
	assigneeID, err := mcpOptionalUUID(input.AssigneeID, "assignee_id")
	if err != nil {
		return nil, err
	}
	inputReporterID, err := mcpOptionalUUID(input.ReporterID, "reporter_id")
	if err != nil {
		return nil, err
	}
	reporterID, err := mcpReporter(auth, inputReporterID)
	if err != nil {
		return nil, err
	}
	issue, err := s.store.CreateSubIssue(ctx, store.CreateSubIssueParams{
		ParentIssueID: parent.ID,
		Title:         title,
		Description:   input.Description,
		Priority:      priority,
		AssigneeID:    assigneeID,
		ReporterID:    reporterID,
		DueDate:       input.DueDate,
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"issue": issue}, nil
}

func (s *Server) mcpListIssues(ctx context.Context, req *mcp.CallToolRequest, input mcpListIssuesInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.IssuesCursor
	if input.Cursor != "" {
		var c store.IssuesCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	assigneeIDs, err := mcpUUIDs(input.AssigneeIDs, "assignee_ids")
	if err != nil {
		return nil, err
	}
	rawStatuses := make([]string, 0, len(input.Statuses)+1)
	if input.Status != "" {
		rawStatuses = append(rawStatuses, string(input.Status))
	}
	for _, status := range input.Statuses {
		rawStatuses = append(rawStatuses, string(status))
	}
	statuses, err := parseIssueStatusFilters(rawStatuses)
	if err != nil {
		return nil, validationError(err.Error())
	}
	rawPriorities := make([]string, 0, len(input.Priorities)+1)
	if input.Priority != "" {
		rawPriorities = append(rawPriorities, string(input.Priority))
	}
	for _, priority := range input.Priorities {
		rawPriorities = append(rawPriorities, string(priority))
	}
	priorities, err := parseIssuePriorityFilters(rawPriorities)
	if err != nil {
		return nil, validationError(err.Error())
	}
	tagNames, err := parseIssueTagNames(input.Tags)
	if err != nil {
		return nil, validationError(err.Error())
	}
	sortBy, err := parseIssueListSort(string(input.Sort), store.ListIssuesSortNumber, true)
	if err != nil {
		return nil, validationError(err.Error())
	}
	direction, err := parseIssueListSortDirection(string(input.Direction), sortBy)
	if err != nil {
		return nil, validationError(err.Error())
	}
	params := store.ListIssuesParams{
		ProjectID:   project.ID,
		Statuses:    statuses,
		Priorities:  priorities,
		AssigneeIDs: assigneeIDs,
		TagNames:    tagNames,
		Cursor:      cursor,
		Limit:       limit,
		Sort:        sortBy,
		Direction:   direction,
	}
	switch {
	case input.Sprint == "backlog":
		params.Backlog = true
	case strings.TrimSpace(input.Sprint) != "":
		number, err := mcpTypedRef(input.Sprint, "sprint")
		if err != nil {
			return nil, err
		}
		sprint, err := s.store.GetSprintByProjectNumber(ctx, project.ID, number)
		if err != nil {
			return nil, err
		}
		params.SprintID = &sprint.ID
	}
	issues, hasMore, err := s.store.ListIssues(ctx, params)
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := issues[len(issues)-1]
		enc := encodeCursor(issueListCursor(last, sortBy))
		next = &enc
	}
	return mcpPageOut(issues, next), nil
}

func (s *Server) mcpListDeletedIssues(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.IssuesCursor
	if input.Cursor != "" {
		var c store.IssuesCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	issues, hasMore, err := s.store.ListDeletedIssues(ctx, store.ListDeletedIssuesParams{
		ProjectID: project.ID,
		Cursor:    cursor,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := issues[len(issues)-1]
		enc := encodeCursor(store.IssuesCursor{Number: last.Number})
		next = &enc
	}
	return mcpPageOut(issues, next), nil
}

func (s *Server) mcpListSubIssues(ctx context.Context, req *mcp.CallToolRequest, input mcpIssuePageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	parent, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.IssuesCursor
	if input.Cursor != "" {
		var c store.IssuesCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	issues, hasMore, err := s.store.ListSubIssuesForIssue(ctx, store.ListSubIssuesForIssueParams{
		ParentIssueID: parent.ID,
		Cursor:        cursor,
		Limit:         limit,
	})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := issues[len(issues)-1]
		enc := encodeCursor(store.IssuesCursor{Number: last.Number})
		next = &enc
	}
	return mcpPageOut(issues, next), nil
}

func (s *Server) mcpBatchIssues(ctx context.Context, req *mcp.CallToolRequest, input mcpBatchIssuesInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	owner, err := normalizeMCPOwner(input.Owner)
	if err != nil {
		return nil, err
	}
	if len(input.Refs) > mcpMaxBatchRefs {
		return nil, validationError("too many refs (max 200)")
	}
	out := make([]model.Issue, 0, len(input.Refs))
	seen := make(map[string]struct{}, len(input.Refs))
	for _, raw := range input.Refs {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		ref, err := parseIssueRef(raw)
		if err != nil {
			return nil, validationError(err.Error())
		}
		key := ref.ProjectKey + "-" + strconv.Itoa(ref.Number)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		issue, err := s.store.GetIssueByOwnerKeyNumber(ctx, owner, ref.ProjectKey, ref.Number)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if err := s.requireMCPProjectAccess(ctx, auth, issue.ProjectID); err != nil {
			return nil, err
		}
		out = append(out, issue)
	}
	return mcpToolOutput{"issues": out}, nil
}

func (s *Server) mcpGetIssue(ctx context.Context, req *mcp.CallToolRequest, input mcpIssueInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"issue": issue}, nil
}

func (s *Server) mcpUpdateIssue(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateIssueInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	if input.Title != nil {
		t, err := validateMCPTitle(*input.Title)
		if err != nil {
			return nil, validationError("title must be 1..200 chars")
		}
		input.Title = &t
	}
	if input.Status != nil && !input.Status.Valid() {
		return nil, validationError("invalid status")
	}
	if input.CloseReason != nil && !input.CloseReason.Valid() {
		return nil, validationError("invalid close_reason")
	}
	if input.Status != nil && *input.Status == model.StatusClosed && issue.Status != model.StatusClosed && input.CloseReason == nil {
		return nil, validationError("close_reason required when closing issue")
	}
	effectiveStatus := issue.Status
	if input.Status != nil {
		effectiveStatus = *input.Status
	}
	if input.CloseReason != nil && effectiveStatus != model.StatusClosed {
		return nil, validationError("close_reason only applies to closed issues")
	}
	if input.Priority != nil && !input.Priority.Valid() {
		return nil, validationError("invalid priority")
	}
	assigneeID, err := mcpOptionalUUID(input.AssigneeID, "assignee_id")
	if err != nil {
		return nil, err
	}
	reporterID, err := mcpOptionalUUID(input.ReporterID, "reporter_id")
	if err != nil {
		return nil, err
	}
	var sprintID *uuid.UUID
	if input.Sprint != nil && !input.ClearSprint {
		number, err := mcpTypedRef(*input.Sprint, "sprint")
		if err != nil {
			return nil, err
		}
		sprint, err := s.store.GetSprintByProjectNumber(ctx, issue.ProjectID, number)
		if err != nil {
			return nil, err
		}
		sprintID = &sprint.ID
	}
	updated, err := s.store.UpdateIssue(ctx, issue.ID, store.UpdateIssueParams{
		Title:         input.Title,
		Description:   input.Description,
		Status:        input.Status,
		CloseReason:   input.CloseReason,
		Priority:      input.Priority,
		AssigneeID:    assigneeID,
		ClearAssignee: input.ClearAssignee,
		ReporterID:    reporterID,
		ClearReporter: input.ClearReporter,
		SprintID:      sprintID,
		ClearSprint:   input.ClearSprint,
		DueDate:       input.DueDate,
		ClearDueDate:  input.ClearDueDate,
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"issue": updated}, nil
}

func (s *Server) mcpDeleteIssue(ctx context.Context, req *mcp.CallToolRequest, input mcpIssueInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	if err := s.store.DeleteIssue(ctx, issue.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpRestoreIssue(ctx context.Context, req *mcp.CallToolRequest, input mcpIssueInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpDeletedIssue(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	restored, err := s.store.RestoreIssue(ctx, issue.ID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"issue": restored}, nil
}

func validateMCPCommentBody(raw string) (string, error) {
	body := strings.TrimSpace(raw)
	if body == "" || len(body) > 10000 {
		return "", validationError("body required, max 10000 chars")
	}
	return body, nil
}

func (s *Server) mcpComment(ctx context.Context, auth authContext, input mcpCommentInput) (model.Issue, model.Comment, error) {
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return model.Issue{}, model.Comment{}, err
	}
	number, err := mcpTypedRef(input.Comment, "comment")
	if err != nil {
		return model.Issue{}, model.Comment{}, err
	}
	comment, err := s.store.GetCommentForIssueByNumber(ctx, issue.ID, number)
	if err != nil {
		return model.Issue{}, model.Comment{}, err
	}
	return issue, comment, nil
}

func (s *Server) mcpCreateComment(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateCommentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	body, err := validateMCPCommentBody(input.Body)
	if err != nil {
		return nil, err
	}
	comment, err := s.store.CreateComment(ctx, store.CreateCommentParams{IssueID: issue.ID, AuthorID: auth.User.ID, Body: body})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"comment": comment}, nil
}

func (s *Server) mcpListComments(ctx context.Context, req *mcp.CallToolRequest, input mcpIssuePageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.CommentsCursor
	if input.Cursor != "" {
		var c store.CommentsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	comments, hasMore, err := s.store.ListCommentsForIssue(ctx, store.ListCommentsForIssueParams{IssueID: issue.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := comments[len(comments)-1]
		enc := encodeCursor(store.CommentsCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		next = &enc
	}
	return mcpPageOut(comments, next), nil
}

func (s *Server) mcpGetComment(ctx context.Context, req *mcp.CallToolRequest, input mcpCommentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, comment, err := s.mcpComment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"comment": comment}, nil
}

func (s *Server) mcpUpdateComment(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateCommentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, comment, err := s.mcpComment(ctx, auth, input.mcpCommentInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	if comment.AuthorID != auth.User.ID {
		return nil, errMCPForbidden
	}
	body, err := validateMCPCommentBody(input.Body)
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateComment(ctx, store.UpdateCommentParams{ID: comment.ID, AuthorID: auth.User.ID, Body: body})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"comment": updated}, nil
}

func (s *Server) mcpDeleteComment(ctx context.Context, req *mcp.CallToolRequest, input mcpCommentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, comment, err := s.mcpComment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	if comment.AuthorID != auth.User.ID {
		return nil, errMCPForbidden
	}
	if err := s.store.DeleteComment(ctx, store.DeleteCommentParams{ID: comment.ID, AuthorID: auth.User.ID}); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func parseMCPDate(raw, field string) (time.Time, error) {
	d, err := time.Parse(dateLayout, raw)
	if err != nil {
		return time.Time{}, validationError(field + " must be YYYY-MM-DD")
	}
	return d, nil
}

func (s *Server) mcpSprint(ctx context.Context, auth authContext, input mcpSprintInput) (model.Project, model.Sprint, error) {
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.Project{}, model.Sprint{}, err
	}
	number, err := mcpTypedRef(input.Sprint, "sprint")
	if err != nil {
		return model.Project{}, model.Sprint{}, err
	}
	sprint, err := s.store.GetSprintByProjectNumber(ctx, project.ID, number)
	if err != nil {
		return model.Project{}, model.Sprint{}, err
	}
	return project, sprint, nil
}

func (s *Server) mcpCreateSprint(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateSprintInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(input.Name)
	if len(name) > 200 {
		return nil, validationError("name max 200 chars")
	}
	if len(input.Goal) > 2000 {
		return nil, validationError("goal max 2000 chars")
	}
	start, end, err := parseMCPCreateSprintDateRange(input.StartDate, input.EndDate)
	if err != nil {
		return nil, err
	}
	sprint, err := s.store.CreateSprint(ctx, store.CreateSprintParams{ProjectID: project.ID, Name: name, Goal: input.Goal, StartDate: start, EndDate: end})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"sprint": sprint}, nil
}

func parseMCPCreateSprintDateRange(startInput, endInput *string) (*time.Time, *time.Time, error) {
	if startInput == nil && endInput == nil {
		return nil, nil, nil
	}
	if startInput == nil || endInput == nil {
		return nil, nil, validationError("start_date and end_date must be provided together")
	}
	start, err := parseMCPDate(*startInput, "start_date")
	if err != nil {
		return nil, nil, err
	}
	end, err := parseMCPDate(*endInput, "end_date")
	if err != nil {
		return nil, nil, err
	}
	if end.Before(start) {
		return nil, nil, validationError("end_date must be on or after start_date")
	}
	return &start, &end, nil
}

func (s *Server) mcpListSprints(ctx context.Context, req *mcp.CallToolRequest, input mcpListSprintsInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if input.Status != "" && !input.Status.Valid() {
		return nil, validationError("invalid status")
	}
	sortBy, err := parseSprintListSort(string(input.Sort), input.Status)
	if err != nil {
		return nil, validationError(err.Error())
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.SprintsCursor
	if input.Cursor != "" {
		var c store.SprintsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	sprints, hasMore, err := s.store.ListSprints(ctx, store.ListSprintsParams{ProjectID: project.ID, Status: input.Status, Sort: sortBy, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		enc := encodeCursor(sprintListCursor(sprints[len(sprints)-1], input.Status, sortBy))
		next = &enc
	}
	return mcpPageOut(sprints, next), nil
}

func (s *Server) mcpListSprintHistoryIssues(ctx context.Context, req *mcp.CallToolRequest, input mcpListSprintHistoryIssuesInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, sprint, err := s.mcpSprint(ctx, auth, input.mcpSprintInput)
	if err != nil {
		return nil, err
	}
	if sprint.Status != model.SprintStatusCompleted {
		return nil, validationError("sprint must be completed")
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.IssuesCursor
	if input.Cursor != "" {
		var c store.IssuesCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	issues, hasMore, err := s.store.ListSprintSnapshotIssues(ctx, store.ListSprintSnapshotIssuesParams{
		ProjectID: project.ID,
		SprintID:  sprint.ID,
		Cursor:    cursor,
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		encoded := encodeCursor(sprintHistoryIssueCursor(issues[len(issues)-1]))
		next = &encoded
	}
	return mcpPageOut(issues, next), nil
}

func (s *Server) mcpGetSprint(ctx context.Context, req *mcp.CallToolRequest, input mcpSprintInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, sprint, err := s.mcpSprint(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"sprint": sprint}, nil
}

func (s *Server) mcpUpdateSprint(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateSprintInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, sprint, err := s.mcpSprint(ctx, auth, input.mcpSprintInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	params := store.UpdateSprintParams{}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if len(name) > 200 {
			return nil, validationError("name max 200 chars")
		}
		params.Name = &name
	}
	if input.Goal != nil {
		if len(*input.Goal) > 2000 {
			return nil, validationError("goal max 2000 chars")
		}
		params.Goal = input.Goal
	}
	if input.StartDate != nil {
		d, err := parseMCPDate(*input.StartDate, "start_date")
		if err != nil {
			return nil, err
		}
		params.StartDate = &d
	}
	if input.EndDate != nil {
		d, err := parseMCPDate(*input.EndDate, "end_date")
		if err != nil {
			return nil, err
		}
		params.EndDate = &d
	}
	params.ClearDates = input.ClearDates
	if input.Status != nil {
		if !input.Status.Valid() {
			return nil, validationError("invalid status")
		}
		if *input.Status == model.SprintStatusCompleted {
			return nil, validationError("use track_complete_sprint")
		}
		params.Status = input.Status
	}
	updated, err := s.store.UpdateSprint(ctx, sprint.ID, params)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"sprint": updated}, nil
}

func (s *Server) mcpDeleteSprint(ctx context.Context, req *mcp.CallToolRequest, input mcpSprintInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, sprint, err := s.mcpSprint(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	if err := s.store.DeleteSprint(ctx, sprint.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpCompleteSprint(ctx context.Context, req *mcp.CallToolRequest, input mcpSprintInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, sprint, err := s.mcpSprint(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	updated, err := s.store.CompleteSprint(ctx, sprint.ID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"sprint": updated}, nil
}

func (s *Server) mcpReorderPlannedSprints(ctx context.Context, req *mcp.CallToolRequest, input mcpReorderSprintsInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	sprintIDs := make([]uuid.UUID, 0, len(input.SprintRefs))
	for _, ref := range input.SprintRefs {
		number, err := mcpTypedRef(ref, "sprint")
		if err != nil {
			return nil, err
		}
		sprint, err := s.store.GetSprintByProjectNumber(ctx, project.ID, number)
		if err != nil {
			return nil, err
		}
		sprintIDs = append(sprintIDs, sprint.ID)
	}
	sprints, err := s.store.ReorderPlannedSprints(ctx, store.ReorderPlannedSprintsParams{ProjectID: project.ID, SprintIDs: sprintIDs})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"sprints": sprints}, nil
}

func (s *Server) mcpTag(ctx context.Context, auth authContext, input mcpTagInput) (model.Project, model.IssueTag, error) {
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.Project{}, model.IssueTag{}, err
	}
	number, err := mcpTypedRef(input.Tag, "tag")
	if err != nil {
		return model.Project{}, model.IssueTag{}, err
	}
	tag, err := s.store.GetIssueTagByProjectNumber(ctx, project.ID, number)
	if err != nil {
		return model.Project{}, model.IssueTag{}, err
	}
	return project, tag, nil
}

func (s *Server) mcpCreateTag(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateTagInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	name, err := model.NormalizeIssueTagName(input.Name)
	if err != nil {
		return nil, validationError(err.Error())
	}
	color := model.IssueTagColorOrDefault(input.Color)
	if !color.Valid() {
		return nil, validationError("invalid color")
	}
	tag, err := s.store.CreateIssueTag(ctx, store.CreateIssueTagParams{ProjectID: project.ID, Name: name, Color: color})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"tag": tag}, nil
}

func (s *Server) mcpListTags(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.IssueTagsCursor
	if input.Cursor != "" {
		var c store.IssueTagsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	tags, hasMore, err := s.store.ListIssueTags(ctx, store.ListIssueTagsParams{ProjectID: project.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := tags[len(tags)-1]
		enc := encodeCursor(store.IssueTagsCursor{Number: last.Number})
		next = &enc
	}
	return mcpPageOut(tags, next), nil
}

func (s *Server) mcpGetTag(ctx context.Context, req *mcp.CallToolRequest, input mcpTagInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, tag, err := s.mcpTag(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"tag": tag}, nil
}

func (s *Server) mcpUpdateTag(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateTagInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, tag, err := s.mcpTag(ctx, auth, input.mcpTagInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	var name *string
	if input.Name != nil {
		normalized, err := model.NormalizeIssueTagName(*input.Name)
		if err != nil {
			return nil, validationError(err.Error())
		}
		name = &normalized
	}
	if input.Color != nil {
		color := model.IssueTagColorOrDefault(*input.Color)
		if !color.Valid() {
			return nil, validationError("invalid color")
		}
		input.Color = &color
	}
	updated, err := s.store.UpdateIssueTag(ctx, store.UpdateIssueTagParams{ID: tag.ID, Name: name, Color: input.Color})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"tag": updated}, nil
}

func (s *Server) mcpDeleteTag(ctx context.Context, req *mcp.CallToolRequest, input mcpTagInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, tag, err := s.mcpTag(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	if err := s.store.DeleteIssueTag(ctx, tag.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpTagForIssue(ctx context.Context, issue model.Issue, rawTag, rawRef string) (model.IssueTag, error) {
	rawTag = strings.TrimSpace(rawTag)
	rawRef = strings.TrimSpace(rawRef)
	if rawTag != "" && rawRef != "" {
		return model.IssueTag{}, validationError("provide either tag or tag_ref")
	}
	if rawRef != "" {
		number, err := mcpTypedRef(rawRef, "tag")
		if err != nil {
			return model.IssueTag{}, err
		}
		return s.store.GetIssueTagByProjectNumber(ctx, issue.ProjectID, number)
	}
	if rawTag == "" {
		return model.IssueTag{}, validationError("tag or tag_ref required")
	}
	name, err := model.NormalizeIssueTagName(rawTag)
	if err != nil {
		return model.IssueTag{}, validationError(err.Error())
	}
	return s.store.GetIssueTagByProjectName(ctx, issue.ProjectID, name)
}

func (s *Server) mcpAttachIssueTag(ctx context.Context, req *mcp.CallToolRequest, input mcpAttachTagInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	tag, err := s.mcpTagForIssue(ctx, issue, input.Tag, input.TagRef)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.CreateIssueTagLink(ctx, store.CreateIssueTagLinkParams{IssueID: issue.ID, TagID: tag.ID}); err != nil {
		return nil, err
	}
	return mcpToolOutput{"tag": tag}, nil
}

func (s *Server) mcpListIssueTags(ctx context.Context, req *mcp.CallToolRequest, input mcpIssuePageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.IssueTagsCursor
	if input.Cursor != "" {
		var c store.IssueTagsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	tags, hasMore, err := s.store.ListTagsForIssue(ctx, store.ListTagsForIssueParams{IssueID: issue.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := tags[len(tags)-1]
		enc := encodeCursor(store.IssueTagsCursor{Number: last.Number})
		next = &enc
	}
	return mcpPageOut(tags, next), nil
}

func (s *Server) mcpDetachIssueTag(ctx context.Context, req *mcp.CallToolRequest, input mcpIssueTagInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	number, err := mcpTypedRef(input.Tag, "tag")
	if err != nil {
		return nil, err
	}
	tag, err := s.store.GetIssueTagByProjectNumber(ctx, issue.ProjectID, number)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteIssueTagLink(ctx, issue.ID, tag.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpLink(ctx context.Context, auth authContext, input mcpLinkInput) (model.Project, model.IssueLink, error) {
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.Project{}, model.IssueLink{}, err
	}
	number, err := mcpTypedRef(input.Link, "link")
	if err != nil {
		return model.Project{}, model.IssueLink{}, err
	}
	link, err := s.store.GetIssueLinkByProjectNumber(ctx, project.ID, number)
	if err != nil {
		return model.Project{}, model.IssueLink{}, err
	}
	return project, link, nil
}

func (s *Server) mcpCreateLink(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateLinkInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	source, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, source.ProjectID); err != nil {
		return nil, err
	}
	targetRef, err := parseIssueRef(input.TargetIssue)
	if err != nil {
		return nil, validationError(err.Error())
	}
	if !input.LinkType.Valid() {
		return nil, validationError("invalid link_type")
	}
	if err := requireIssueRefProject(targetRef, source.ProjectKey); err != nil {
		return nil, err
	}
	target, err := s.store.GetIssueByOwnerKeyNumber(ctx, source.OwnerUsername, targetRef.ProjectKey, targetRef.Number)
	if err != nil {
		return nil, err
	}
	link, err := s.store.CreateIssueLink(ctx, store.CreateIssueLinkParams{SourceID: source.ID, TargetID: target.ID, LinkType: input.LinkType})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"link": link}, nil
}

func (s *Server) mcpListIssueLinks(ctx context.Context, req *mcp.CallToolRequest, input mcpIssuePageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.IssueLinksCursor
	if input.Cursor != "" {
		var c store.IssueLinksCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	links, hasMore, err := s.store.ListIssueLinksForIssue(ctx, store.ListIssueLinksForIssueParams{IssueID: issue.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]issueLinkView, 0, len(links))
	for _, link := range links {
		view := issueLinkView{IssueLink: link}
		if link.SourceID == issue.ID {
			view.Direction = "outgoing"
			view.DisplayType = outgoingDisplayName(link.LinkType)
			view.OtherIssueID = link.TargetID
		} else {
			view.Direction = "incoming"
			view.DisplayType = incomingDisplayName(link.LinkType)
			view.OtherIssueID = link.SourceID
		}
		out = append(out, view)
	}
	var next *string
	if hasMore {
		last := links[len(links)-1]
		enc := encodeCursor(store.IssueLinksCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		next = &enc
	}
	return mcpPageOut(out, next), nil
}

func (s *Server) mcpGetLink(ctx context.Context, req *mcp.CallToolRequest, input mcpLinkInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, link, err := s.mcpLink(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"link": link}, nil
}

func (s *Server) mcpUpdateLink(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateLinkInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, link, err := s.mcpLink(ctx, auth, input.mcpLinkInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	sourceRef, err := parseIssueRef(input.SourceIssue)
	if err != nil {
		return nil, validationError(err.Error())
	}
	targetRef, err := parseIssueRef(input.TargetIssue)
	if err != nil {
		return nil, validationError(err.Error())
	}
	if !input.LinkType.Valid() {
		return nil, validationError("invalid link_type")
	}
	if err := requireIssueRefProject(sourceRef, project.Key); err != nil {
		return nil, err
	}
	if err := requireIssueRefProject(targetRef, project.Key); err != nil {
		return nil, err
	}
	source, err := s.store.GetIssueByOwnerKeyNumber(ctx, project.OwnerUsername, sourceRef.ProjectKey, sourceRef.Number)
	if err != nil {
		return nil, err
	}
	target, err := s.store.GetIssueByOwnerKeyNumber(ctx, project.OwnerUsername, targetRef.ProjectKey, targetRef.Number)
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateIssueLink(ctx, link.ID, store.UpdateIssueLinkParams{SourceID: source.ID, TargetID: target.ID, LinkType: input.LinkType})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"link": updated}, nil
}

func (s *Server) mcpDeleteLink(ctx context.Context, req *mcp.CallToolRequest, input mcpLinkInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, link, err := s.mcpLink(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	if err := s.store.DeleteIssueLink(ctx, link.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpContext(ctx context.Context, auth authContext, input mcpContextInput) (model.Project, model.ProjectContext, error) {
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.Project{}, model.ProjectContext{}, err
	}
	number, err := mcpTypedRef(input.Context, "context")
	if err != nil {
		return model.Project{}, model.ProjectContext{}, err
	}
	contextItem, err := s.store.GetProjectContextByProjectNumber(ctx, project.ID, number)
	if err != nil {
		return model.Project{}, model.ProjectContext{}, err
	}
	if contextItem.Scope != model.ProjectContextScopeProject {
		return model.Project{}, model.ProjectContext{}, store.ErrNotFound
	}
	return project, contextItem, nil
}

func (s *Server) mcpCreateProjectContext(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateProjectContextInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	title, err := validateProjectContextTitle(input.Title)
	if err != nil {
		return nil, validationError(err.Error())
	}
	body, err := validateProjectContextBody(input.Body)
	if err != nil {
		return nil, validationError(err.Error())
	}
	contentType, err := validateProjectContextContentType(input.ContentType, "text/markdown; charset=utf-8")
	if err != nil {
		return nil, validationError(err.Error())
	}
	contextItem, err := s.store.CreateProjectContext(ctx, store.CreateProjectContextParams{
		ProjectID:   project.ID,
		Title:       title,
		Kind:        model.ProjectContextKindText,
		ContentType: contentType,
		Body:        body,
		CreatedByID: auth.User.ID,
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"context": contextItem}, nil
}

func (s *Server) mcpListProjectContext(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.ProjectContextsCursor
	if input.Cursor != "" {
		var c store.ProjectContextsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	contexts, hasMore, err := s.store.ListProjectContexts(ctx, store.ListProjectContextsParams{ProjectID: project.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := contexts[len(contexts)-1]
		enc := encodeCursor(store.ProjectContextsCursor{Position: *last.Position})
		next = &enc
	}
	return mcpPageOut(contexts, next), nil
}

func (s *Server) mcpGetProjectContext(ctx context.Context, req *mcp.CallToolRequest, input mcpContextInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, contextItem, err := s.mcpContext(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"context": contextItem}, nil
}

func (s *Server) mcpUpdateProjectContext(ctx context.Context, req *mcp.CallToolRequest, input mcpUpdateProjectContextInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, contextItem, err := s.mcpContext(ctx, auth, input.mcpContextInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	var title *string
	if input.Title != nil {
		t, err := validateProjectContextTitle(*input.Title)
		if err != nil {
			return nil, validationError(err.Error())
		}
		title = &t
	}
	var body *string
	if input.Body != nil {
		b, err := validateProjectContextBody(*input.Body)
		if err != nil {
			return nil, validationError(err.Error())
		}
		body = &b
	}
	var contentType *string
	if input.ContentType != nil {
		value, err := validateProjectContextContentType(*input.ContentType, "")
		if err != nil {
			return nil, validationError(err.Error())
		}
		contentType = &value
	}
	if input.Position != nil && *input.Position < 1 {
		return nil, validationError("position must be at least 1")
	}
	updated, err := s.store.UpdateProjectContext(ctx, store.UpdateProjectContextParams{
		ID:          contextItem.ID,
		Title:       title,
		Body:        body,
		ContentType: contentType,
		Position:    input.Position,
		UpdatedByID: auth.User.ID,
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"context": updated}, nil
}

func (s *Server) mcpDeleteProjectContext(ctx context.Context, req *mcp.CallToolRequest, input mcpContextInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, contextItem, err := s.mcpContext(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	if err := s.deleteProjectContextAndObjects(ctx, contextItem); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpContextAttachment(ctx context.Context, auth authContext, input mcpContextAttachmentInput) (model.ProjectContext, model.ContextAttachment, error) {
	_, contextItem, err := s.mcpContext(ctx, auth, input.mcpContextInput)
	if err != nil {
		return model.ProjectContext{}, model.ContextAttachment{}, err
	}
	number, err := mcpTypedRef(input.Object, "object")
	if err != nil {
		return model.ProjectContext{}, model.ContextAttachment{}, err
	}
	attachment, err := s.store.GetContextAttachmentByObjectNumber(ctx, contextItem.ID, number)
	if err != nil {
		return model.ProjectContext{}, model.ContextAttachment{}, err
	}
	return contextItem, attachment, nil
}

func (s *Server) mcpCreateContextAttachment(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateContextAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, contextItem, err := s.mcpContext(ctx, auth, input.mcpContextInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	attachment, err := mcpCreateDescriptionAttachment(s, ctx, project.ID, auth.User.ID, input.Filename, input.ContentType, input.ContentBase64, func(object model.StorageObject) (model.ContextAttachment, error) {
		return s.store.CreateContextAttachment(ctx, store.CreateContextAttachmentParams{
			ProjectID: project.ID, ContextID: contextItem.ID, StorageObjectID: object.ID, CreatedByID: auth.User.ID,
		})
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"attachment": attachment}, nil
}

func (s *Server) mcpListContextAttachments(ctx context.Context, req *mcp.CallToolRequest, input mcpContextAttachmentsPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, contextItem, err := s.mcpContext(ctx, auth, input.mcpContextInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.ContextAttachmentsCursor
	if input.Cursor != "" {
		var value store.ContextAttachmentsCursor
		if err := decodeCursor(input.Cursor, &value); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &value
	}
	attachments, hasMore, err := s.store.ListContextAttachments(ctx, store.ListContextAttachmentsParams{ContextID: contextItem.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := attachments[len(attachments)-1]
		encoded := encodeCursor(store.ContextAttachmentsCursor{Number: last.Object.Number})
		next = &encoded
	}
	return mcpPageOut(attachments, next), nil
}

func (s *Server) mcpReadContextAttachmentContent(ctx context.Context, req *mcp.CallToolRequest, input mcpContextAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, attachment, err := s.mcpContextAttachment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	data, err := s.mcpObjectContent(ctx, attachment.Object)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"attachment": attachment, "content_base64": base64.StdEncoding.EncodeToString(data)}, nil
}

func (s *Server) mcpDeleteContextAttachment(ctx context.Context, req *mcp.CallToolRequest, input mcpContextAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	contextItem, attachment, err := s.mcpContextAttachment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, contextItem.ProjectID); err != nil {
		return nil, err
	}
	if _, err := mcpDeleteDescriptionAttachment(s, ctx, func() (model.ContextAttachment, error) {
		return s.store.DeleteContextAttachment(ctx, contextItem.ID, attachment.StorageObjectID)
	}, func(deleted model.ContextAttachment) string { return deleted.Object.ObjectKey }); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpCreateIssueContext(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateIssueContextInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(input.Context)
	if raw == "" {
		raw = strings.TrimSpace(input.ContextRef)
	}
	if raw != "" && (strings.TrimSpace(input.Title) != "" || strings.TrimSpace(input.Body) != "") {
		return nil, validationError("provide either context or title/body")
	}
	if raw == "" {
		title, err := validateProjectContextTitle(input.Title)
		if err != nil {
			return nil, validationError(err.Error())
		}
		body, err := validateIssueContextBody(input.Body)
		if err != nil {
			return nil, validationError(err.Error())
		}
		contextItem, err := s.store.CreateIssueContext(ctx, store.CreateIssueContextParams{
			IssueID:     issue.ID,
			Title:       title,
			Kind:        model.ProjectContextKindText,
			ContentType: "text/plain; charset=utf-8",
			Body:        body,
			CreatedByID: auth.User.ID,
		})
		if err != nil {
			return nil, err
		}
		return mcpToolOutput{"context": contextItem}, nil
	}
	number, err := mcpTypedRef(raw, "context")
	if err != nil {
		return nil, err
	}
	contextItem, err := s.store.GetProjectContextByProjectNumber(ctx, issue.ProjectID, number)
	if err != nil {
		return nil, err
	}
	if contextItem.Scope != model.ProjectContextScopeProject {
		return nil, store.ErrNotFound
	}
	if _, err := s.store.CreateIssueContextLink(ctx, issue.ID, contextItem.ID); err != nil {
		return nil, err
	}
	return mcpToolOutput{"context": contextItem}, nil
}

func (s *Server) mcpBulkLinkIssueContexts(ctx context.Context, req *mcp.CallToolRequest, input mcpBulkLinkIssueContextsInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	links, err := parseIssueContextLinkPairs(project.Key, input.Links)
	if err != nil {
		return nil, validationError(err.Error())
	}
	result, err := s.store.CreateIssueContextLinks(ctx, store.CreateIssueContextLinksParams{
		ProjectID: project.ID,
		Links:     links,
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{
		"requested": result.Requested,
		"created":   result.Created,
		"unchanged": result.Unchanged,
	}, nil
}

func (s *Server) mcpListIssueContext(ctx context.Context, req *mcp.CallToolRequest, input mcpIssuePageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.ProjectContextsCursor
	if input.Cursor != "" {
		var c store.ProjectContextsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	contexts, hasMore, err := s.store.ListContextsForIssue(ctx, store.ListContextsForIssueParams{IssueID: issue.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := contexts[len(contexts)-1]
		enc := encodeCursor(store.ProjectContextsCursor{Number: last.Number})
		next = &enc
	}
	return mcpPageOut(contexts, next), nil
}

func (s *Server) mcpDeleteIssueContext(ctx context.Context, req *mcp.CallToolRequest, input mcpIssueContextInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	number, err := mcpTypedRef(input.Context, "context")
	if err != nil {
		return nil, err
	}
	contextItem, err := s.store.GetProjectContextByProjectNumber(ctx, issue.ProjectID, number)
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteIssueContextLink(ctx, issue.ID, contextItem.ID); err != nil {
		return nil, err
	}
	if contextItem.Scope == model.ProjectContextScopeIssue {
		if err := s.deleteProjectContextAndObjects(ctx, contextItem); err != nil {
			return nil, err
		}
	}
	return mcpOK(), nil
}

func (s *Server) requireMCPObjectStorage() error {
	if s.objectStorage == nil {
		return errMCPStorageAbsent
	}
	return nil
}

func (s *Server) mcpCreateStorageObjectRecord(ctx context.Context, projectID, userID uuid.UUID, filename, contentType, contentBase64 string) (model.StorageObject, error) {
	if err := s.requireMCPObjectStorage(); err != nil {
		return model.StorageObject{}, err
	}
	filename, err := normalizeStorageObjectFilename(filename)
	if err != nil {
		return model.StorageObject{}, validationError(err.Error())
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(contentBase64))
	if err != nil {
		return model.StorageObject{}, validationError("content_base64 must be valid base64")
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	contentType = normalizeStorageObjectContentType(contentType, sample)
	objectID := uuid.New()
	stored, err := s.objectStorage.Put(ctx, projectID, objectID, bytes.NewReader(data))
	if err != nil {
		return model.StorageObject{}, err
	}
	object, err := s.store.CreateStorageObject(ctx, store.CreateStorageObjectParams{
		ID:          objectID,
		ProjectID:   projectID,
		Backend:     stored.Backend,
		Bucket:      stored.Bucket,
		ObjectKey:   stored.ObjectKey,
		Filename:    filename,
		ContentType: contentType,
		ByteSize:    stored.ByteSize,
		SHA256:      stored.SHA256,
		CreatedByID: userID,
	})
	if err != nil {
		_ = s.deleteStorageBackendObject(ctx, stored.ObjectKey)
		return model.StorageObject{}, err
	}
	return object, nil
}

func (s *Server) mcpObject(ctx context.Context, auth authContext, input mcpObjectInput) (model.Project, model.StorageObject, error) {
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.Project{}, model.StorageObject{}, err
	}
	number, err := mcpTypedRef(input.Object, "object")
	if err != nil {
		return model.Project{}, model.StorageObject{}, err
	}
	object, err := s.store.GetStorageObjectByProjectNumber(ctx, project.ID, number)
	if err != nil {
		return model.Project{}, model.StorageObject{}, err
	}
	return project, object, nil
}

func (s *Server) mcpCreateObject(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateObjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	object, err := s.mcpCreateStorageObjectRecord(ctx, project.ID, auth.User.ID, input.Filename, input.ContentType, input.ContentBase64)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"object": object}, nil
}

func (s *Server) mcpListObjects(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.StorageObjectsCursor
	if input.Cursor != "" {
		var c store.StorageObjectsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	objects, hasMore, err := s.store.ListStorageObjects(ctx, store.ListStorageObjectsParams{ProjectID: project.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := objects[len(objects)-1]
		enc := encodeCursor(store.StorageObjectsCursor{Number: last.Number})
		next = &enc
	}
	return mcpPageOut(objects, next), nil
}

func (s *Server) mcpGetObject(ctx context.Context, req *mcp.CallToolRequest, input mcpObjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, object, err := s.mcpObject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"object": object}, nil
}

func (s *Server) mcpObjectContent(ctx context.Context, object model.StorageObject) ([]byte, error) {
	if err := s.requireMCPObjectStorage(); err != nil {
		return nil, err
	}
	body, err := s.objectStorage.Open(ctx, object.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Server) mcpReadObjectContent(ctx context.Context, req *mcp.CallToolRequest, input mcpObjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, object, err := s.mcpObject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	data, err := s.mcpObjectContent(ctx, object)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{
		"object":         object,
		"content_base64": base64.StdEncoding.EncodeToString(data),
	}, nil
}

func (s *Server) mcpDeleteObject(ctx context.Context, req *mcp.CallToolRequest, input mcpObjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, object, err := s.mcpObject(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	deleted, err := s.store.DeleteStorageObject(ctx, object.ID)
	if err != nil {
		return nil, err
	}
	if s.objectStorage != nil {
		_ = s.deleteStorageBackendObject(ctx, deleted.ObjectKey)
	}
	return mcpOK(), nil
}

func (s *Server) mcpAttachment(ctx context.Context, auth authContext, input mcpAttachmentInput) (model.Issue, model.IssueAttachment, error) {
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return model.Issue{}, model.IssueAttachment{}, err
	}
	number, err := mcpTypedRef(input.Object, "object")
	if err != nil {
		return model.Issue{}, model.IssueAttachment{}, err
	}
	attachment, err := s.store.GetIssueAttachmentByObjectNumber(ctx, issue.ID, number)
	if err != nil {
		return model.Issue{}, model.IssueAttachment{}, err
	}
	return issue, attachment, nil
}

func (s *Server) mcpCreateAttachment(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	attachment, err := mcpCreateDescriptionAttachment(s, ctx, issue.ProjectID, auth.User.ID, input.Filename, input.ContentType, input.ContentBase64, func(object model.StorageObject) (model.IssueAttachment, error) {
		return s.store.CreateIssueAttachment(ctx, store.CreateIssueAttachmentParams{IssueID: issue.ID, StorageObjectID: object.ID, CreatedByID: auth.User.ID})
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"attachment": attachment}, nil
}

func (s *Server) mcpListAttachments(ctx context.Context, req *mcp.CallToolRequest, input mcpIssuePageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, err := s.mcpIssue(ctx, auth, input.mcpIssueInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.IssueAttachmentsCursor
	if input.Cursor != "" {
		var c store.IssueAttachmentsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	attachments, hasMore, err := s.store.ListIssueAttachments(ctx, store.ListIssueAttachmentsParams{IssueID: issue.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := attachments[len(attachments)-1]
		enc := encodeCursor(store.IssueAttachmentsCursor{Number: last.Object.Number})
		next = &enc
	}
	return mcpPageOut(attachments, next), nil
}

func (s *Server) mcpReadAttachmentContent(ctx context.Context, req *mcp.CallToolRequest, input mcpAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, attachment, err := s.mcpAttachment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	data, err := s.mcpObjectContent(ctx, attachment.Object)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{
		"attachment":     attachment,
		"content_base64": base64.StdEncoding.EncodeToString(data),
	}, nil
}

func (s *Server) mcpDeleteAttachment(ctx context.Context, req *mcp.CallToolRequest, input mcpAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	issue, attachment, err := s.mcpAttachment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, issue.ProjectID); err != nil {
		return nil, err
	}
	if _, err := mcpDeleteDescriptionAttachment(s, ctx, func() (model.IssueAttachment, error) {
		return s.store.DeleteIssueAttachment(ctx, issue.ID, attachment.StorageObjectID)
	}, func(deleted model.IssueAttachment) string { return deleted.Object.ObjectKey }); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func mcpCreateDescriptionAttachment[T any](s *Server, ctx context.Context, projectID, userID uuid.UUID, filename, contentType, contentBase64 string, link func(model.StorageObject) (T, error)) (T, error) {
	var zero T
	object, err := s.mcpCreateStorageObjectRecord(ctx, projectID, userID, filename, contentType, contentBase64)
	if err != nil {
		return zero, err
	}
	attachment, err := link(object)
	if err != nil {
		s.cleanupStorageObject(ctx, object)
		return zero, err
	}
	return attachment, nil
}

func mcpDeleteDescriptionAttachment[T any](s *Server, ctx context.Context, unlink func() (T, error), objectKey func(T) string) (T, error) {
	var zero T
	deleted, err := unlink()
	if err != nil {
		return zero, err
	}
	if s.objectStorage != nil {
		_ = s.deleteStorageBackendObject(ctx, objectKey(deleted))
	}
	return deleted, nil
}

func (s *Server) mcpProjectAttachment(ctx context.Context, auth authContext, input mcpObjectInput) (model.Project, model.ProjectAttachment, error) {
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return model.Project{}, model.ProjectAttachment{}, err
	}
	number, err := mcpTypedRef(input.Object, "object")
	if err != nil {
		return model.Project{}, model.ProjectAttachment{}, err
	}
	attachment, err := s.store.GetProjectAttachmentByObjectNumber(ctx, project.ID, number)
	if err != nil {
		return model.Project{}, model.ProjectAttachment{}, err
	}
	return project, attachment, nil
}

func (s *Server) mcpCreateProjectAttachment(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateProjectAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	attachment, err := mcpCreateDescriptionAttachment(s, ctx, project.ID, auth.User.ID, input.Filename, input.ContentType, input.ContentBase64, func(object model.StorageObject) (model.ProjectAttachment, error) {
		return s.store.CreateProjectAttachment(ctx, store.CreateProjectAttachmentParams{ProjectID: project.ID, StorageObjectID: object.ID, CreatedByID: auth.User.ID})
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"attachment": attachment}, nil
}

func (s *Server) mcpListProjectAttachments(ctx context.Context, req *mcp.CallToolRequest, input mcpProjectPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, err := s.mcpProject(ctx, auth, input.mcpProjectInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.ProjectAttachmentsCursor
	if input.Cursor != "" {
		var c store.ProjectAttachmentsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	attachments, hasMore, err := s.store.ListProjectAttachments(ctx, store.ListProjectAttachmentsParams{ProjectID: project.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := attachments[len(attachments)-1]
		encoded := encodeCursor(store.ProjectAttachmentsCursor{Number: last.Object.Number})
		next = &encoded
	}
	return mcpPageOut(attachments, next), nil
}

func (s *Server) mcpReadProjectAttachmentContent(ctx context.Context, req *mcp.CallToolRequest, input mcpObjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, attachment, err := s.mcpProjectAttachment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	data, err := s.mcpObjectContent(ctx, attachment.Object)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"attachment": attachment, "content_base64": base64.StdEncoding.EncodeToString(data)}, nil
}

func (s *Server) mcpDeleteProjectAttachment(ctx context.Context, req *mcp.CallToolRequest, input mcpObjectInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, attachment, err := s.mcpProjectAttachment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	if _, err := mcpDeleteDescriptionAttachment(s, ctx, func() (model.ProjectAttachment, error) {
		return s.store.DeleteProjectAttachment(ctx, project.ID, attachment.StorageObjectID)
	}, func(deleted model.ProjectAttachment) string { return deleted.Object.ObjectKey }); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpSprintAttachment(ctx context.Context, auth authContext, input mcpSprintAttachmentInput) (model.Sprint, model.SprintAttachment, error) {
	_, sprint, err := s.mcpSprint(ctx, auth, input.mcpSprintInput)
	if err != nil {
		return model.Sprint{}, model.SprintAttachment{}, err
	}
	number, err := mcpTypedRef(input.Object, "object")
	if err != nil {
		return model.Sprint{}, model.SprintAttachment{}, err
	}
	attachment, err := s.store.GetSprintAttachmentByObjectNumber(ctx, sprint.ID, number)
	if err != nil {
		return model.Sprint{}, model.SprintAttachment{}, err
	}
	return sprint, attachment, nil
}

func (s *Server) mcpCreateSprintAttachment(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateSprintAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	project, sprint, err := s.mcpSprint(ctx, auth, input.mcpSprintInput)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, project.ID); err != nil {
		return nil, err
	}
	attachment, err := mcpCreateDescriptionAttachment(s, ctx, sprint.ProjectID, auth.User.ID, input.Filename, input.ContentType, input.ContentBase64, func(object model.StorageObject) (model.SprintAttachment, error) {
		return s.store.CreateSprintAttachment(ctx, store.CreateSprintAttachmentParams{SprintID: sprint.ID, StorageObjectID: object.ID, CreatedByID: auth.User.ID})
	})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"attachment": attachment}, nil
}

func (s *Server) mcpListSprintAttachments(ctx context.Context, req *mcp.CallToolRequest, input mcpSprintAttachmentsPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, sprint, err := s.mcpSprint(ctx, auth, input.mcpSprintInput)
	if err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.SprintAttachmentsCursor
	if input.Cursor != "" {
		var c store.SprintAttachmentsCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	attachments, hasMore, err := s.store.ListSprintAttachments(ctx, store.ListSprintAttachmentsParams{SprintID: sprint.ID, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := attachments[len(attachments)-1]
		enc := encodeCursor(store.SprintAttachmentsCursor{Number: last.Object.Number})
		next = &enc
	}
	return mcpPageOut(attachments, next), nil
}

func (s *Server) mcpReadSprintAttachmentContent(ctx context.Context, req *mcp.CallToolRequest, input mcpSprintAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	_, attachment, err := s.mcpSprintAttachment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	data, err := s.mcpObjectContent(ctx, attachment.Object)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"attachment": attachment, "content_base64": base64.StdEncoding.EncodeToString(data)}, nil
}

func (s *Server) mcpDeleteSprintAttachment(ctx context.Context, req *mcp.CallToolRequest, input mcpSprintAttachmentInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	sprint, attachment, err := s.mcpSprintAttachment(ctx, auth, input)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPProjectWriteAccess(ctx, auth, sprint.ProjectID); err != nil {
		return nil, err
	}
	if _, err := mcpDeleteDescriptionAttachment(s, ctx, func() (model.SprintAttachment, error) {
		return s.store.DeleteSprintAttachment(ctx, sprint.ID, attachment.StorageObjectID)
	}, func(deleted model.SprintAttachment) string { return deleted.Object.ObjectKey }); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpCreateUser(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateUserInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPAdmin(auth); err != nil {
		return nil, err
	}
	email := strings.TrimSpace(input.Email)
	username := strings.TrimSpace(input.Username)
	name := strings.TrimSpace(input.Name)
	if username == "" && email == "" {
		return nil, validationError("username required")
	}
	if username == "" {
		username = store.UsernameFromEmail(email)
	}
	if _, err := store.NormalizeUsername(username); err != nil {
		return nil, validationError(err.Error())
	}
	if email != "" && !strings.Contains(email, "@") {
		return nil, validationError("invalid email")
	}
	if name == "" {
		return nil, validationError("name required")
	}
	user, err := s.store.CreateUserProfile(ctx, username, email, name)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"user": user}, nil
}

func (s *Server) mcpListUsers(ctx context.Context, req *mcp.CallToolRequest, input mcpPageInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPAdmin(auth); err != nil {
		return nil, err
	}
	limit, err := mcpLimit(input.Limit)
	if err != nil {
		return nil, validationError(err.Error())
	}
	var cursor *store.UsersCursor
	if input.Cursor != "" {
		var c store.UsersCursor
		if err := decodeCursor(input.Cursor, &c); err != nil {
			return nil, validationError(err.Error())
		}
		cursor = &c
	}
	users, hasMore, err := s.store.ListUsers(ctx, store.ListUsersParams{Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var next *string
	if hasMore {
		last := users[len(users)-1]
		enc := encodeCursor(store.UsersCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		next = &enc
	}
	return mcpPageOut(users, next), nil
}

func (s *Server) mcpGetUser(ctx context.Context, req *mcp.CallToolRequest, input mcpUserInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPAdmin(auth); err != nil {
		return nil, err
	}
	user, err := s.store.GetUser(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"user": user}, nil
}

func (s *Server) mcpDeleteUser(ctx context.Context, req *mcp.CallToolRequest, input mcpUserInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPAdmin(auth); err != nil {
		return nil, err
	}
	if err := s.store.DeleteUser(ctx, input.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpCreateMyToken(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateTokenInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	name, kind, err := validateMCPTokenInput(input.Name, input.Kind, input.ExpiresAt)
	if err != nil {
		return nil, err
	}
	created, err := s.store.CreateAuthToken(ctx, store.CreateAuthTokenParams{UserID: auth.User.ID, Kind: kind, Name: name, ExpiresAt: s.authTokenExpiry(kind, input.ExpiresAt)})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"token": createTokenResp{AuthToken: created.Token, Token: created.RawToken}}, nil
}

func (s *Server) mcpListMyTokens(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	tokens, err := s.store.ListAuthTokens(ctx, auth.User.ID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"tokens": tokens}, nil
}

func (s *Server) mcpRevokeMyToken(ctx context.Context, req *mcp.CallToolRequest, input mcpTokenInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.store.RevokeAuthTokenForUser(ctx, auth.User.ID, input.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) mcpCreateUserToken(ctx context.Context, req *mcp.CallToolRequest, input mcpCreateUserTokenInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPAdmin(auth); err != nil {
		return nil, err
	}
	name, kind, err := validateMCPTokenInput(input.Name, input.Kind, input.ExpiresAt)
	if err != nil {
		return nil, err
	}
	created, err := s.store.CreateAuthToken(ctx, store.CreateAuthTokenParams{UserID: input.UserID, Kind: kind, Name: name, ExpiresAt: s.authTokenExpiry(kind, input.ExpiresAt)})
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"token": createTokenResp{AuthToken: created.Token, Token: created.RawToken}}, nil
}

func (s *Server) mcpListUserTokens(ctx context.Context, req *mcp.CallToolRequest, input mcpListUserTokensInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPAdmin(auth); err != nil {
		return nil, err
	}
	tokens, err := s.store.ListAuthTokens(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	return mcpToolOutput{"tokens": tokens}, nil
}

func (s *Server) mcpRevokeToken(ctx context.Context, req *mcp.CallToolRequest, input mcpTokenInput) (mcpToolOutput, error) {
	ctx, auth, err := s.mcpAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.requireMCPAdmin(auth); err != nil {
		return nil, err
	}
	if err := s.store.RevokeAuthToken(ctx, input.ID); err != nil {
		return nil, err
	}
	return mcpOK(), nil
}

func (s *Server) addMCPResources(srv *mcp.Server) {
	handler := s.readMCPResource
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "track_project",
		Title:       "track project",
		Description: "Project metadata by owner and key.",
		MIMEType:    "application/json",
		URITemplate: "track://project/{owner}/{key}",
	}, handler)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "track_issue",
		Title:       "track issue",
		Description: "Issue metadata by owner and issue ref.",
		MIMEType:    "application/json",
		URITemplate: "track://issue/{owner}/{issue}",
	}, handler)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "track_context",
		Title:       "track context",
		Description: "Project context body by owner, key, and context ref.",
		MIMEType:    "application/json",
		URITemplate: "track://context/{owner}/{key}/{context}",
	}, handler)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "track_object",
		Title:       "track object metadata",
		Description: "Storage object metadata by owner, key, and object ref.",
		MIMEType:    "application/json",
		URITemplate: "track://object/{owner}/{key}/{object}",
	}, handler)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "track_object_content",
		Title:       "track object content",
		Description: "Storage object bytes by owner, key, and object ref.",
		URITemplate: "track://object-content/{owner}/{key}/{object}",
	}, handler)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "track_attachment_content",
		Title:       "track attachment content",
		Description: "Issue attachment bytes by owner, issue ref, and object ref.",
		URITemplate: "track://attachment-content/{owner}/{issue}/{object}",
	}, handler)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "track_sprint_attachment_content",
		Title:       "track sprint attachment content",
		Description: "Sprint attachment bytes by owner, project key, sprint ref, and object ref.",
		URITemplate: "track://sprint-attachment-content/{owner}/{key}/{sprint}/{object}",
	}, handler)
}

func (s *Server) readMCPResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	ctx, auth, err := s.mcpResourceAuth(ctx, req)
	if err != nil {
		return nil, err
	}
	uri := req.Params.URI
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "track" {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	parts, err := mcpURIPathParts(u)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	switch u.Host {
	case "project":
		if len(parts) != 2 {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		project, err := s.mcpProject(ctx, auth, mcpProjectInput{Owner: parts[0], Key: parts[1]})
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		return mcpJSONResource(uri, project)
	case "issue":
		if len(parts) != 2 {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		issue, err := s.mcpIssue(ctx, auth, mcpIssueInput{Owner: parts[0], Issue: parts[1]})
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		return mcpJSONResource(uri, issue)
	case "context":
		if len(parts) != 3 {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		_, contextItem, err := s.mcpContext(ctx, auth, mcpContextInput{mcpProjectInput: mcpProjectInput{Owner: parts[0], Key: parts[1]}, Context: parts[2]})
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		return mcpJSONResource(uri, contextItem)
	case "object":
		if len(parts) != 3 {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		_, object, err := s.mcpObject(ctx, auth, mcpObjectInput{mcpProjectInput: mcpProjectInput{Owner: parts[0], Key: parts[1]}, Object: parts[2]})
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		return mcpJSONResource(uri, object)
	case "object-content":
		if len(parts) != 3 {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		_, object, err := s.mcpObject(ctx, auth, mcpObjectInput{mcpProjectInput: mcpProjectInput{Owner: parts[0], Key: parts[1]}, Object: parts[2]})
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		data, err := s.mcpObjectContent(ctx, object)
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: object.ContentType,
			Blob:     data,
		}}}, nil
	case "attachment-content":
		if len(parts) != 3 {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		_, attachment, err := s.mcpAttachment(ctx, auth, mcpAttachmentInput{mcpIssueInput: mcpIssueInput{Owner: parts[0], Issue: parts[1]}, Object: parts[2]})
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		data, err := s.mcpObjectContent(ctx, attachment.Object)
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: attachment.Object.ContentType,
			Blob:     data,
		}}}, nil
	case "sprint-attachment-content":
		if len(parts) != 4 {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		_, attachment, err := s.mcpSprintAttachment(ctx, auth, mcpSprintAttachmentInput{
			mcpSprintInput: mcpSprintInput{mcpProjectInput: mcpProjectInput{Owner: parts[0], Key: parts[1]}, Sprint: parts[2]},
			Object:         parts[3],
		})
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		data, err := s.mcpObjectContent(ctx, attachment.Object)
		if err != nil {
			return nil, mcpResourceError(uri, err)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: attachment.Object.ContentType,
			Blob:     data,
		}}}, nil
	default:
		return nil, mcp.ResourceNotFoundError(uri)
	}
}

func (s *Server) mcpResourceAuth(ctx context.Context, req *mcp.ReadResourceRequest) (context.Context, authContext, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return ctx, authContext{}, errMCPUnauthorized
	}
	auth, ok := req.Extra.TokenInfo.Extra[mcpAuthExtraKey].(authContext)
	if !ok {
		return ctx, authContext{}, errMCPUnauthorized
	}
	return store.WithActor(ctx, auth.User.ID), auth, nil
}

func mcpURIPathParts(u *url.URL) ([]string, error) {
	rawParts := strings.Split(strings.Trim(u.Path, "/"), "/")
	out := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if part == "" {
			continue
		}
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

func mcpResourceError(uri string, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, objectstorage.ErrNotFound):
		return mcp.ResourceNotFoundError(uri)
	default:
		return err
	}
}

func mcpJSONResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      uri,
		MIMEType: "application/json",
		Text:     string(data),
	}}}, nil
}

func (s *Server) addMCPPrompts(srv *mcp.Server) {
	srv.AddPrompt(&mcp.Prompt{
		Name:        "summarize_issue",
		Title:       "summarize issue",
		Description: "Summarize an issue and its related comments/context.",
		Arguments: []*mcp.PromptArgument{
			{Name: "owner", Required: true},
			{Name: "issue", Required: true},
		},
	}, staticMCPPrompt("Summarize the trackslash issue identified by owner={{owner}} issue={{issue}}. Use track_get_issue, track_list_comments, track_list_issue_context, track_list_issue_tags, track_list_issue_links, and track_list_attachments before answering."))
	srv.AddPrompt(&mcp.Prompt{
		Name:        "triage_project",
		Title:       "triage project",
		Description: "Review project health and identify work needing attention.",
		Arguments: []*mcp.PromptArgument{
			{Name: "owner", Required: true},
			{Name: "key", Required: true},
		},
	}, staticMCPPrompt("Triage trackslash project owner={{owner}} key={{key}}. Use stats, changelog, open issues, tags, and sprints. Return prioritized findings and suggested next actions."))
	srv.AddPrompt(&mcp.Prompt{
		Name:        "plan_sprint",
		Title:       "plan sprint",
		Description: "Plan or refine a sprint using project backlog and priorities.",
		Arguments: []*mcp.PromptArgument{
			{Name: "owner", Required: true},
			{Name: "key", Required: true},
			{Name: "sprint", Required: false},
		},
	}, staticMCPPrompt("Plan sprint={{sprint}} for trackslash project owner={{owner}} key={{key}}. Use backlog issues, current sprints, tags, priorities, and due dates. Suggest scoped sprint contents and risks."))
	srv.AddPrompt(&mcp.Prompt{
		Name:        "draft_changelog",
		Title:       "draft changelog",
		Description: "Draft a project changelog from recent activity.",
		Arguments: []*mcp.PromptArgument{
			{Name: "owner", Required: true},
			{Name: "key", Required: true},
		},
	}, staticMCPPrompt("Draft a concise changelog for trackslash project owner={{owner}} key={{key}}. Use track_list_project_changelog and inspect linked issues when needed."))
}

func staticMCPPrompt(template string) mcp.PromptHandler {
	return func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		text := template
		for key, value := range req.Params.Arguments {
			text = strings.ReplaceAll(text, "{{"+key+"}}", value)
		}
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{{
				Role:    mcp.Role("user"),
				Content: &mcp.TextContent{Text: text},
			}},
		}, nil
	}
}
