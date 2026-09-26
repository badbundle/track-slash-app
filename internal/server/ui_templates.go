package server

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/bradleymackey/track-slash/internal/store"
)

//go:embed templates/*.html static/*
var uiTemplateFS embed.FS

var uiStaticFS = func() fs.FS {
	staticFS, err := fs.Sub(uiTemplateFS, "static")
	if err != nil {
		panic(err)
	}
	return staticFS
}()

const uiAuthCookieName = "track_slash_ui_token"

var errUIForbidden = errors.New("forbidden")
var errUIBadRequest = errors.New("bad request")

var uiTemplates = template.Must(template.New("ui").Funcs(template.FuncMap{
	"initials":                       uiInitials,
	"projectBreadcrumb":              uiProjectBreadcrumb,
	"issueBreadcrumb":                uiIssueBreadcrumb,
	"issueContextBreadcrumb":         uiIssueContextBreadcrumb,
	"userAvatar":                     uiUserAvatar,
	"profileImagePicker":             uiProfileImagePicker,
	"tokenCreateModal":               uiTokenCreateModal,
	"oauthClientCreateModal":         uiOAuthClientCreateModal,
	"passkeyCreateModal":             uiPasskeyCreateModal,
	"accountPages":                   uiAccountPageLinks,
	"byteSize":                       uiByteSize,
	"issueAssignee":                  uiIssueAssigneePath,
	"issueAssigneeEdit":              uiIssueAssigneeEditPath,
	"issueAttachments":               uiIssueAttachmentsPath,
	"issueAttachmentContent":         uiIssueAttachmentContentPath,
	"issueAttachmentDelete":          uiIssueAttachmentDeletePath,
	"issueAttachmentImage":           uiIssueAttachmentImage,
	"issueAttachmentInlineContent":   uiIssueAttachmentInlineContentPath,
	"issueAttachmentMarkdown":        uiIssueAttachmentMarkdown,
	"issueAttachmentListData":        uiIssueAttachmentListData,
	"projectAttachmentListData":      uiProjectAttachmentListData,
	"projectDescriptionBody":         uiProjectDescriptionBody,
	"projectDescriptionEditor":       uiProjectDescriptionEditor,
	"projectDescriptionPlaceholder":  uiProjectDescriptionPlaceholderText,
	"issueDescriptionEditor":         uiIssueDescriptionEditor,
	"issueDescriptionPlaceholder":    uiIssueDescriptionPlaceholderText,
	"issueDescriptionBody":           uiIssueDescriptionBody,
	"sprintAttachmentListData":       uiSprintAttachmentListData,
	"sprintDescriptionEditor":        uiSprintDescriptionEditor,
	"newSprintDescriptionEditor":     uiNewSprintDescriptionEditor,
	"sprintDescriptionBody":          uiSprintDescriptionBody,
	"issueComment":                   uiIssueCommentPath,
	"issueCommentCreateModal":        uiIssueCommentCreateModal,
	"issueSubIssueCreateModal":       uiIssueSubIssueCreateModal,
	"issueLinkCreateModal":           uiIssueLinkCreateModal,
	"issueCommentEdit":               uiIssueCommentEditPath,
	"issueComments":                  uiIssueCommentsPath,
	"issueContext":                   uiIssueContextPath,
	"issueContextDelete":             uiIssueContextDeletePath,
	"issueContextEdit":               uiIssueContextEditPath,
	"issueContextItem":               uiIssueContextItemPath,
	"issueContextLinkNew":            uiIssueContextLinkNewPath,
	"issueContextNew":                uiIssueContextNewPath,
	"issueCloseReason":               uiIssueCloseReasonPath,
	"issueCloseReasonEdit":           uiIssueCloseReasonEditPath,
	"issueDelete":                    uiIssueDeletePath,
	"issueDescription":               uiIssueDescriptionPath,
	"issueDescriptionEdit":           uiIssueDescriptionEditPath,
	"issueHref":                      uiIssuePath,
	"issueGitHubLinks":               uiIssueGitHubLinksPath,
	"issueGitHubLinkDelete":          uiIssueGitHubLinkDeletePath,
	"issueGitHubLinkRefresh":         uiIssueGitHubLinkRefreshPath,
	"issueLink":                      uiIssueLinkPath,
	"issueLinkDelete":                uiIssueLinkDeletePath,
	"issueLinkEdit":                  uiIssueLinkEditPath,
	"issueLinkNew":                   uiIssueLinkNewPath,
	"issueLinks":                     uiIssueLinksPath,
	"issuePanel":                     uiIssuePanelPath,
	"issuePriority":                  uiIssuePriorityPath,
	"issuePriorityEdit":              uiIssuePriorityEditPath,
	"issueDueDate":                   uiIssueDueDatePath,
	"issueDueDateEdit":               uiIssueDueDateEditPath,
	"issueReporter":                  uiIssueReporterPath,
	"issueReporterEdit":              uiIssueReporterEditPath,
	"issueRestore":                   uiIssueRestorePath,
	"issueSprint":                    uiIssueSprintPath,
	"issueSprintEdit":                uiIssueSprintEditPath,
	"issueStatus":                    uiIssueStatusPath,
	"issueStatusEdit":                uiIssueStatusEditPath,
	"issueSubIssues":                 uiIssueSubIssuesPath,
	"issueSubIssuesNew":              uiIssueSubIssuesNewPath,
	"issueTags":                      uiIssueTagsPath,
	"issueTagDelete":                 uiIssueTagDeletePath,
	"issueTitle":                     uiIssueTitlePath,
	"issueTitleEdit":                 uiIssueTitleEditPath,
	"issueTitleParts":                uiIssueTitlePartsForDisplay,
	"issueAssigneeAutocomplete":      uiIssueAssigneeAutocomplete,
	"issueReporterAutocomplete":      uiIssueReporterAutocomplete,
	"issueSprintAutocomplete":        uiIssueSprintAutocomplete,
	"newIssueProjectAutocomplete":    uiNewIssueProjectAutocomplete,
	"autocompleteOptionSearchText":   uiAutocompleteOptionSearchText,
	"linkLabel":                      uiIssueLinkLabel,
	"linkOptions":                    uiIssueLinkOptions,
	"linkedIssueProgress":            uiLinkedIssueProgress,
	"closeReasonLabel":               uiCloseReasonLabel,
	"closeReasonModal":               uiCloseReasonModal,
	"issueTagsModal":                 uiIssueTagsModal,
	"issueGitHubLinkModal":           uiIssueGitHubLinkModal,
	"issueCloseReasonDropdown":       uiIssueCloseReasonDropdown,
	"issueStatusDropdown":            uiIssueStatusDropdown,
	"closeReasonOptions":             uiCloseReasonOptions,
	"issueColumnCount":               uiIssueColumnCount,
	"sprintIssueCount":               uiSprintIssueCount,
	"priorityClass":                  uiPriorityClass,
	"priorityLabel":                  uiPriorityLabel,
	"priorityOptions":                uiPriorityOptions,
	"newIssueSelectedPriority":       uiNewIssueSelectedPriority,
	"issues":                         uiIssuesPath,
	"issueNew":                       uiIssueNewPath,
	"issueNewPanel":                  uiIssueNewPanelPath,
	"issueNewProjectOptions":         uiIssueNewProjectOptionsPath,
	"newIssueProjectSelected":        uiNewIssueProjectSelected,
	"newIssueProjectInput":           uiNewIssueProjectInput,
	"newIssueProjectLabel":           uiNewIssueProjectLabel,
	"canEditIssueSprint":             uiCanEditIssueSprint,
	"projectIssues":                  uiProjectIssuesPath,
	"projectIssueNew":                uiProjectIssueNewPath,
	"projectIssueNewPanel":           uiProjectIssueNewPanelPath,
	"projectName":                    uiProjectNamePath,
	"projectNameEdit":                uiProjectNameEditPath,
	"projectDescription":             uiProjectDescriptionPath,
	"projectDescriptionEdit":         uiProjectDescriptionEditPath,
	"projectImage":                   uiProjectImagePath,
	"projectImageDelete":             uiProjectImageDeletePath,
	"projectFavorite":                uiProjectFavoritePath,
	"projectDelete":                  uiProjectDeletePath,
	"projectDeleteModal":             uiProjectDeleteModal,
	"projectGitHubConnections":       uiProjectGitHubConnectionsPath,
	"projectGitHubDisconnect":        uiProjectGitHubConnectionDisconnectPath,
	"projectMembers":                 uiProjectMembersPath,
	"projectMembersPanel":            uiProjectMembersPanelPath,
	"projectMember":                  uiProjectMemberPath,
	"projectMemberDelete":            uiProjectMemberDeletePath,
	"projectAccess":                  uiProjectAccessPath,
	"projectSprintMode":              uiProjectSprintModePath,
	"projectBlocks":                  uiProjectBlocksPath,
	"projectBlockDelete":             uiProjectBlockDeletePath,
	"projectMemberAutocomplete":      uiProjectMemberAutocomplete,
	"projectMemberCreateModal":       uiProjectMemberCreateModal,
	"projectBlockCreateModal":        uiProjectBlockCreateModal,
	"projectPanel":                   uiProjectPanelPath,
	"projectHome":                    uiProjectHomePath,
	"projectHomePanel":               uiProjectHomePanelPath,
	"projectSprint":                  uiProjectSprintPath,
	"projectSprintActivate":          uiProjectSprintActivatePath,
	"projectSprintComplete":          uiProjectSprintCompletePath,
	"projectSprintDelete":            uiProjectSprintDeletePath,
	"projectSprintEdit":              uiProjectSprintEditPath,
	"projectSprintDescription":       uiProjectSprintDescriptionPath,
	"projectSprintIssueNew":          uiProjectSprintIssueNewPath,
	"projectSprintIssues":            uiProjectSprintIssuesPath,
	"projectSprintHistoryIssues":     uiProjectSprintHistoryIssuesPath,
	"projectSprintMoveDown":          uiProjectSprintMoveDownPath,
	"projectSprintMoveUp":            uiProjectSprintMoveUpPath,
	"projectSprintNew":               uiProjectSprintNewPath,
	"newSprintCreateModal":           uiNewSprintCreateModal,
	"activeSprintIssueCreateModal":   uiActiveSprintIssueCreateModal,
	"plannedSprintIssueCreateModal":  uiPlannedSprintIssueCreateModal,
	"projectSprints":                 uiProjectSprintsPath,
	"projectContext":                 uiProjectContextPath,
	"projectContextPanel":            uiProjectContextPanelPath,
	"projectContextDelete":           uiProjectContextDeletePath,
	"projectContextEdit":             uiProjectContextEditPath,
	"projectContextMoveUp":           uiProjectContextMoveUpPath,
	"projectContextMoveDown":         uiProjectContextMoveDownPath,
	"contextAttachmentListData":      uiContextAttachmentListData,
	"contextEditor":                  uiContextEditor,
	"contextBody":                    uiContextBody,
	"issueContextEditor":             uiContextEditor,
	"issueContextBody":               uiContextBody,
	"issueContextAttachmentListData": uiContextAttachmentListData,
	"projectContextIssueDelete":      uiProjectContextIssueDeletePath,
	"projectContextIssueNew":         uiProjectContextIssueNewPath,
	"projectContextIssues":           uiProjectContextIssuesPath,
	"projectContextNew":              uiProjectContextNewPath,
	"projectContexts":                uiProjectContextsPath,
	"whiteboardPages":                uiProjectWhiteboardPath,
	"whiteboardNew":                  uiProjectWhiteboardNewPath,
	"whiteboardPage":                 uiProjectWhiteboardPagePath,
	"whiteboardPagePanel":            uiProjectWhiteboardPagePanelPath,
	"whiteboardPageEdit":             uiProjectWhiteboardPageEditPath,
	"whiteboardPageDelete":           uiProjectWhiteboardPageDeletePath,
	"whiteboardEditor":               uiWhiteboardEditor,
	"whiteboardBody":                 uiWhiteboardBody,
	"projectTags":                    uiProjectTagsPath,
	"projectTag":                     uiProjectTagPath,
	"projectTagDelete":               uiProjectTagDeletePath,
	"projectView":                    uiProjectViewPath,
	"projectIcon":                    uiProjectIcon,
	"projectImagePicker":             uiProjectImagePicker,
	"projectGitHubConnectionModal":   uiProjectGitHubConnectionModal,
	"tagCreateModal":                 uiTagCreateModal,
	"tagAttachModal":                 uiTagAttachModal,
	"changelogActor":                 uiChangelogActor,
	"changelogIcon":                  uiChangelogIcon,
	"changelogTargetHref":            uiChangelogTargetHref,
	"dueBadgeClass":                  uiDueBadgeClass,
	"dueBadgeIcon":                   uiDueBadgeIcon,
	"dueBadgeLabel":                  uiDueBadgeLabel,
	"dueDateFull":                    uiDueDateFull,
	"dueDateShort":                   uiDueDateShort,
	"dueDateValue":                   uiDueDateValue,
	"sprintDate":                     uiSprintDate,
	"sprintDateRange":                uiSprintDateRange,
	"sprintHistoryDateRange":         uiSprintHistoryDateRange,
	"sprintLabel":                    uiSprintLabel,
	"statusClass":                    uiStatusClass,
	"statusLabel":                    uiStatusLabel,
	"statusOptions":                  uiStatusOptions,
	"statusRow":                      uiStatusRowClass,
	"statusSurface":                  uiStatusSurfaceClass,
	"statusValue":                    uiStatusValue,
	"subIssueProgress":               uiSubIssueProgress,
	"tagClass":                       uiTagClass,
	"tagColors":                      uiTagColors,
	"tagDotClass":                    uiTagDotClass,
	"issueVisibleTags":               uiIssueVisibleTags,
	"issueHiddenTagCount":            uiIssueHiddenTagCount,
	"issueItem":                      uiIssueItemFromIssue,
	"issueAttachmentIcon":            uiIssueAttachmentIcon,
	"tokenTime":                      uiTokenTime,
}).ParseFS(uiTemplateFS, "templates/*.html"))

func renderUITemplate(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		logInternalError("ui template", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeUIStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUIBadRequest):
		http.Error(w, "bad request", http.StatusBadRequest)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, store.ErrConflict):
		http.Error(w, "conflict", http.StatusConflict)
	case errors.Is(err, errUIForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		writeUIInternalError(w, "ui store", err)
	}
}
