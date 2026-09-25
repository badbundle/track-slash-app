package server

import (
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
)

func uiProjectPath(project model.Project) string {
	return "/" + project.OwnerUsername + "/projects/" + project.Key
}

func uiOwnerProjectsPath(username string) string {
	return "/" + username + "/projects"
}

func uiOwnerProjectsPanelPath(username string) string {
	return uiOwnerProjectsPath(username) + "/panel"
}

func uiProjectViewPath(project model.Project, view string, assigneeIDs ...[]uuid.UUID) string {
	ids := []uuid.UUID(nil)
	if len(assigneeIDs) > 0 {
		ids = assigneeIDs[0]
	}
	return uiAppendAssigneeQuery(uiProjectPath(project)+"/"+view, ids)
}

func uiProjectPanelPath(project model.Project, view string, assigneeIDs ...[]uuid.UUID) string {
	ids := []uuid.UUID(nil)
	if len(assigneeIDs) > 0 {
		ids = assigneeIDs[0]
	}
	return uiAppendAssigneeQuery(uiProjectPath(project)+"/"+view+"/panel", ids)
}

func uiProjectNamePath(project model.Project) string {
	return uiProjectPath(project) + "/name"
}

func uiProjectNameEditPath(project model.Project) string {
	return uiProjectNamePath(project) + "/edit"
}

func uiProjectDescriptionPath(project model.Project) string {
	return uiProjectPath(project) + "/description"
}

func uiProjectDescriptionEditPath(project model.Project) string {
	return uiProjectDescriptionPath(project) + "/edit"
}

func uiProjectImagePath(project model.Project) string {
	return uiProjectPath(project) + "/image"
}

func uiProjectImageDeletePath(project model.Project) string {
	return uiProjectImagePath(project) + "/delete"
}

func uiProjectImageContentPath(project model.Project) string {
	return uiProjectImagePath(project) + "/content"
}

func uiProjectImageThumbnailContentPath(project model.Project) string {
	return uiProjectImagePath(project) + "/thumbnail/content"
}

func uiProjectAttachmentsPath(project model.Project) string {
	return uiProjectPath(project) + "/attachments"
}

func uiProjectAttachmentContentPath(project model.Project, object any) string {
	return uiProjectAttachmentsPath(project) + "/" + uiStorageObjectRef(object) + "/content"
}

func uiProjectAttachmentDeletePath(project model.Project, object any) string {
	return uiProjectAttachmentsPath(project) + "/" + uiStorageObjectRef(object) + "/delete"
}

func uiProjectFavoritePath(project model.Project) string {
	return uiProjectPath(project) + "/favorite"
}

// uiProjectDeletePath serves the confirmation dialog on GET and performs the
// deletion on POST, so the menu entry and the dialog's own form share one URL.
func uiProjectDeletePath(project model.Project) string {
	return uiProjectPath(project) + "/delete"
}

const uiSettingsGitHubTokensPath = "/settings/github-tokens"

func uiSettingsGitHubTokenPath(credential model.GitHubCredential) string {
	return uiSettingsGitHubTokensPath + "/" + credential.ID.String()
}

func uiSettingsGitHubTokenDeletePath(credential model.GitHubCredential) string {
	return uiSettingsGitHubTokenPath(credential) + "/delete"
}

func uiProjectGitHubConnectionsPath(project model.Project) string {
	return uiProjectPath(project) + "/github/connections"
}

func uiProjectGitHubConnectionDisconnectPath(project model.Project, connection model.GitHubConnection) string {
	return uiProjectGitHubConnectionsPath(project) + "/" + connection.ID.String() + "/disconnect"
}

func uiProjectMembersPath(project model.Project) string {
	return uiProjectPath(project) + "/members"
}

func uiProjectMembersPanelPath(project model.Project) string {
	return uiProjectMembersPath(project) + "/panel"
}

// Everything below /members/ is addressed by username. chi resolves a static
// segment before a param one, so any collection-level action parked in that
// namespace would permanently capture the member of the same name. Those actions
// live beside /members instead.
func uiProjectMemberCandidatesPath(project model.Project) string {
	return uiProjectPath(project) + "/member-candidates"
}

func uiProjectMemberPath(project model.Project, username string) string {
	return uiProjectMembersPath(project) + "/" + username
}

func uiProjectMemberDeletePath(project model.Project, username string) string {
	return uiProjectMemberPath(project, username) + "/delete"
}

func uiProjectAccessPath(project model.Project) string {
	return uiProjectPath(project) + "/member-access"
}

func uiProjectSprintModePath(project model.Project) string {
	return uiProjectPath(project) + "/sprint-mode"
}

func uiProjectBlocksPath(project model.Project) string {
	return uiProjectPath(project) + "/member-blocks"
}

func uiProjectBlockDeletePath(project model.Project, username string) string {
	return uiProjectBlocksPath(project) + "/" + username + "/delete"
}

func uiProjectSprintsPath(project model.Project) string {
	return uiProjectPath(project) + "/sprints"
}

func uiProjectSprintNewPath(project model.Project) string {
	return uiProjectSprintsPath(project) + "/new"
}

func uiProjectSprintPath(project model.Project, sprint any) string {
	return uiProjectSprintsPath(project) + "/" + uiIssueSprintRef(uiSprintValue(sprint))
}

func uiProjectSprintEditPath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/edit"
}

func uiProjectSprintDescriptionPath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/description"
}

func uiProjectSprintAttachmentsPath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/attachments"
}

func uiProjectSprintAttachmentContentPath(project model.Project, sprint any, object any) string {
	return uiProjectSprintAttachmentsPath(project, sprint) + "/" + uiStorageObjectRef(object) + "/content"
}

func uiProjectSprintAttachmentInlineContentPath(project model.Project, sprint any, object any) string {
	return uiProjectSprintAttachmentContentPath(project, sprint, object) + "?inline=1"
}

func uiProjectSprintAttachmentDeletePath(project model.Project, sprint any, object any) string {
	return uiProjectSprintAttachmentsPath(project, sprint) + "/" + uiStorageObjectRef(object) + "/delete"
}

func uiProjectSprintActivatePath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/activate"
}

func uiProjectSprintCompletePath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/complete"
}

func uiProjectSprintDeletePath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/delete"
}

func uiProjectSprintMoveUpPath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/move-up"
}

func uiProjectSprintMoveDownPath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/move-down"
}

func uiProjectSprintIssuesPath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/issues"
}

func uiProjectSprintHistoryIssuesPath(project model.Project, sprint any) string {
	return uiProjectSprintPath(project, sprint) + "/history/issues"
}

func uiProjectSprintIssueNewPath(project model.Project, sprint any) string {
	return uiProjectSprintIssuesPath(project, sprint) + "/new"
}

func uiIssuesPath() string {
	return "/issues"
}

func uiIssueGitHubLinksPath(issue model.Issue) string {
	return uiIssuePath(issue) + "/github-links"
}

func uiIssueGitHubLinkDeletePath(issue model.Issue, link model.GitHubIssueLink) string {
	return uiIssueGitHubLinksPath(issue) + "/" + link.ID.String() + "/delete"
}

func uiIssueGitHubLinkRefreshPath(issue model.Issue, link model.GitHubIssueLink) string {
	return uiIssueGitHubLinksPath(issue) + "/" + link.ID.String() + "/refresh"
}

func uiIssueNewPath() string {
	return uiIssuesPath() + "/new"
}

func uiIssueNewPanelPath() string {
	return uiIssueNewPath() + "/panel"
}

func uiIssueNewProjectOptionsPath() string {
	return uiIssueNewPath() + "/projects"
}

func uiProjectIssuesPath(project model.Project) string {
	return uiProjectPath(project) + "/issues"
}

func uiProjectIssueNewPath(project model.Project) string {
	return uiProjectIssuesPath(project) + "/new"
}

func uiProjectIssueNewPanelPath(project model.Project) string {
	return uiProjectIssueNewPath(project) + "/panel"
}

func uiProjectContextsPath(project model.Project) string {
	return uiProjectPath(project) + "/context"
}

func uiProjectTagsPath(project model.Project) string {
	return uiProjectPath(project) + "/tags"
}

func uiProjectTagPath(project model.Project, tag any) string {
	return uiProjectTagsPath(project) + "/" + uiIssueTagRef(tag)
}

func uiProjectTagDeletePath(project model.Project, tag any) string {
	return uiProjectTagPath(project, tag) + "/delete"
}

func uiProjectContextPath(project model.Project, contextItem any) string {
	return uiProjectContextsPath(project) + "/" + uiProjectContextRef(contextItem)
}

func uiProjectContextPanelPath(project model.Project, contextItem any) string {
	return uiProjectContextPath(project, contextItem) + "/panel"
}

func uiProjectContextNewPath(project model.Project) string {
	return uiProjectContextsPath(project) + "/new"
}

func uiProjectContextEditPath(project model.Project, contextItem any) string {
	return uiProjectContextPath(project, contextItem) + "/edit"
}

func uiProjectContextDeletePath(project model.Project, contextItem any) string {
	return uiProjectContextPath(project, contextItem) + "/delete"
}

func uiProjectContextMoveUpPath(project model.Project, contextItem any) string {
	return uiProjectContextPath(project, contextItem) + "/move-up"
}

func uiProjectContextMoveDownPath(project model.Project, contextItem any) string {
	return uiProjectContextPath(project, contextItem) + "/move-down"
}

func uiProjectContextAttachmentsPath(project model.Project, contextItem any) string {
	return uiProjectContextPath(project, contextItem) + "/attachments"
}

func uiProjectContextAttachmentContentPath(project model.Project, contextItem any, object any) string {
	return uiProjectContextAttachmentsPath(project, contextItem) + "/" + uiStorageObjectRef(object) + "/content"
}

func uiProjectContextAttachmentDeletePath(project model.Project, contextItem any, object any) string {
	return uiProjectContextAttachmentsPath(project, contextItem) + "/" + uiStorageObjectRef(object) + "/delete"
}

func uiProjectContextIssuesPath(project model.Project, contextItem any) string {
	return uiProjectContextPath(project, contextItem) + "/issues"
}

func uiProjectContextIssueNewPath(project model.Project, contextItem any) string {
	return uiProjectContextIssuesPath(project, contextItem) + "/new"
}

func uiProjectContextIssueDeletePath(project model.Project, contextItem any, issue any) string {
	return uiProjectContextIssuesPath(project, contextItem) + "/" + uiIssueValue(issue).Identifier + "/delete"
}

func uiIssuePath(v any) string {
	issue := uiIssueValue(v)
	return "/" + issue.OwnerUsername + "/issues/" + issue.Identifier
}

func uiIssuePanelPath(issue any) string {
	return uiIssuePath(issue) + "/panel"
}

func uiIssueDeletePath(issue any) string {
	return uiIssuePath(issue) + "/delete"
}

func uiIssueRestorePath(issue any) string {
	return uiIssuePath(issue) + "/restore"
}

func uiIssueCommentsPath(issue any) string {
	return uiIssuePath(issue) + "/comments"
}

func uiIssueAttachmentsPath(issue any) string {
	return uiIssuePath(issue) + "/attachments"
}

func uiIssueAttachmentContentPath(issue any, object any) string {
	return uiIssueAttachmentsPath(issue) + "/" + uiStorageObjectRef(object) + "/content"
}

func uiIssueAttachmentInlineContentPath(issue any, object any) string {
	return uiIssueAttachmentContentPath(issue, object) + "?inline=1"
}

func uiIssueAttachmentDeletePath(issue any, object any) string {
	return uiIssueAttachmentsPath(issue) + "/" + uiStorageObjectRef(object) + "/delete"
}

func uiIssueContextPath(issue any) string {
	return uiIssuePath(issue) + "/context"
}

func uiIssueContextItemPath(issue any, contextItem any) string {
	return uiIssueContextPath(issue) + "/" + uiProjectContextRef(contextItem)
}

func uiIssueContextEditPath(issue any, contextItem any) string {
	return uiIssueContextItemPath(issue, contextItem) + "/edit"
}

func uiIssueContextNewPath(issue any) string {
	return uiIssueContextPath(issue) + "/new"
}

func uiIssueContextLinkNewPath(issue any) string {
	return uiIssueContextPath(issue) + "/link"
}

func uiIssueContextDeletePath(issue any, contextItem any) string {
	return uiIssueContextItemPath(issue, contextItem) + "/delete"
}

func uiIssueTagsPath(issue any) string {
	return uiIssuePath(issue) + "/tags"
}

func uiIssueTagDeletePath(issue any, tag any) string {
	return uiIssueTagsPath(issue) + "/" + uiIssueTagRef(tag) + "/delete"
}

func uiIssueCommentPath(issue any, comment any) string {
	return uiIssueCommentsPath(issue) + "/" + uiCommentRef(comment)
}

func uiIssueCommentEditPath(issue any, comment any) string {
	return uiIssueCommentPath(issue, comment) + "/edit"
}

func uiIssueTitlePath(issue any) string {
	return uiIssuePath(issue) + "/title"
}

func uiIssueTitleEditPath(issue any) string {
	return uiIssueTitlePath(issue) + "/edit"
}

func uiIssueDescriptionPath(issue any) string {
	return uiIssuePath(issue) + "/description"
}

func uiIssueDescriptionEditPath(issue any) string {
	return uiIssueDescriptionPath(issue) + "/edit"
}

func uiStorageObjectRef(v any) string {
	switch item := v.(type) {
	case model.StorageObject:
		return item.Ref
	case *model.StorageObject:
		if item == nil {
			return ""
		}
		return item.Ref
	case model.IssueAttachment:
		return item.Object.Ref
	case *model.IssueAttachment:
		if item == nil {
			return ""
		}
		return item.Object.Ref
	case string:
		return item
	default:
		return ""
	}
}

func uiIssueStatusPath(issue any) string {
	return uiIssuePath(issue) + "/status"
}

func uiIssueStatusEditPath(issue any) string {
	return uiIssueStatusPath(issue) + "/edit"
}

func uiIssueCloseReasonPath(issue any) string {
	return uiIssuePath(issue) + "/close-reason"
}

func uiIssueCloseReasonEditPath(issue any) string {
	return uiIssueCloseReasonPath(issue) + "/edit"
}

func uiIssuePriorityPath(issue any) string {
	return uiIssuePath(issue) + "/priority"
}

func uiIssuePriorityEditPath(issue any) string {
	return uiIssuePriorityPath(issue) + "/edit"
}

func uiIssueDueDatePath(issue any) string {
	return uiIssuePath(issue) + "/due-date"
}

func uiIssueDueDateEditPath(issue any) string {
	return uiIssueDueDatePath(issue) + "/edit"
}

func uiIssueAssigneePath(issue any) string {
	return uiIssuePath(issue) + "/assignee"
}

func uiIssueAssigneeEditPath(issue any) string {
	return uiIssueAssigneePath(issue) + "/edit"
}

func uiIssueReporterPath(issue any) string {
	return uiIssuePath(issue) + "/reporter"
}

func uiIssueReporterEditPath(issue any) string {
	return uiIssueReporterPath(issue) + "/edit"
}

func uiIssueSprintPath(issue any) string {
	return uiIssuePath(issue) + "/sprint"
}

func uiIssueSprintEditPath(issue any) string {
	return uiIssueSprintPath(issue) + "/edit"
}

func uiIssueLinksPath(issue any) string {
	return uiIssuePath(issue) + "/links"
}

func uiIssueLinkNewPath(issue any) string {
	return uiIssueLinksPath(issue) + "/new"
}

func uiIssueLinkPath(issue any, link any) string {
	return uiIssueLinksPath(issue) + "/" + uiIssueLinkRef(link)
}

func uiIssueLinkEditPath(issue any, link any) string {
	return uiIssueLinkPath(issue, link) + "/edit"
}

func uiIssueLinkDeletePath(issue any, link any) string {
	return uiIssueLinkPath(issue, link) + "/delete"
}

func uiIssueSubIssuesPath(issue any) string {
	return uiIssuePath(issue) + "/sub-issues"
}

func uiIssueSubIssuesNewPath(issue any) string {
	return uiIssueSubIssuesPath(issue) + "/new"
}

func uiIssueValue(v any) model.Issue {
	switch issue := v.(type) {
	case model.Issue:
		return issue
	case *model.Issue:
		if issue != nil {
			return *issue
		}
	}
	return model.Issue{}
}

func uiSprintValue(v any) model.Sprint {
	switch sprint := v.(type) {
	case model.Sprint:
		return sprint
	case *model.Sprint:
		if sprint != nil {
			return *sprint
		}
	}
	return model.Sprint{}
}

func uiIssueLinkRef(v any) string {
	var link model.IssueLink
	switch l := v.(type) {
	case model.IssueLink:
		link = l
	case *model.IssueLink:
		if l != nil {
			link = *l
		}
	}
	if link.Ref != "" {
		return link.Ref
	}
	if link.Number > 0 {
		return model.IssueLinkRef(link.Number)
	}
	return "link-0"
}

func uiIssueTagRef(v any) string {
	switch tag := v.(type) {
	case model.IssueTag:
		if tag.Ref != "" {
			return tag.Ref
		}
		if tag.Number > 0 {
			return model.IssueTagRef(tag.Number)
		}
	case *model.IssueTag:
		if tag != nil {
			return uiIssueTagRef(*tag)
		}
	}
	return "tag-0"
}

func uiProjectContextRef(v any) string {
	switch contextItem := v.(type) {
	case model.ProjectContext:
		if contextItem.Ref != "" {
			return contextItem.Ref
		}
		if contextItem.Number > 0 {
			return model.ProjectContextRef(contextItem.Number)
		}
	case *model.ProjectContext:
		if contextItem != nil {
			return uiProjectContextRef(*contextItem)
		}
	case model.ProjectContextSummary:
		if contextItem.Ref != "" {
			return contextItem.Ref
		}
		if contextItem.Number > 0 {
			return model.ProjectContextRef(contextItem.Number)
		}
	case *model.ProjectContextSummary:
		if contextItem != nil {
			return uiProjectContextRef(*contextItem)
		}
	case uiContextManagerItem:
		if contextItem.Ref != "" {
			return contextItem.Ref
		}
		if contextItem.Number > 0 {
			return model.ProjectContextRef(contextItem.Number)
		}
	case *uiContextManagerItem:
		if contextItem != nil {
			return uiProjectContextRef(*contextItem)
		}
	}
	return "context-0"
}

func uiCommentRef(v any) string {
	var comment model.Comment
	switch c := v.(type) {
	case model.Comment:
		comment = c
	case *model.Comment:
		if c != nil {
			comment = *c
		}
	}
	if comment.Ref != "" {
		return comment.Ref
	}
	if comment.Number > 0 {
		return model.CommentRef(comment.Number)
	}
	return "comment-0"
}
