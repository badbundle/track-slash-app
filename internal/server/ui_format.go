package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
)

func uiInitials(name, email string) string {
	source := strings.TrimSpace(name)
	if source == "" {
		source = strings.TrimSpace(email)
	}
	if source == "" {
		return "?"
	}
	parts := strings.Fields(source)
	if len(parts) == 1 {
		return strings.ToUpper(string([]rune(parts[0])[0]))
	}
	first := []rune(parts[0])
	last := []rune(parts[len(parts)-1])
	return strings.ToUpper(string(first[0]) + string(last[0]))
}

type uiIssueTitleParts struct {
	Leading  string
	Trailing string
}

func uiIssueTitlePartsForDisplay(title string) uiIssueTitleParts {
	runes := []rune(title)
	if len(runes) == 0 {
		return uiIssueTitleParts{}
	}
	return uiIssueTitleParts{
		Leading:  string(runes[:len(runes)-1]),
		Trailing: string(runes[len(runes)-1]),
	}
}

var uiProjectViewLabels = map[string]string{
	"sprint":     "Sprint",
	"planned":    "Planned",
	"all":        "All",
	"context":    "Context",
	"whiteboard": "Whiteboard",
	"about":      "About",
	"members":    "Members",
	"sprints":    "Sprint history",
	"changelog":  "Changelog",
}

func uiProjectBreadcrumb(project model.Project, view string, showOwner ...bool) uiBreadcrumbData {
	items := uiProjectBreadcrumbItems(project, showOwner...)
	items = append(items, uiBreadcrumbItem{Label: uiProjectViewLabels[view], Current: true})
	return uiBreadcrumbData{Items: items}
}

func uiProjectBreadcrumbItems(project model.Project, showOwner ...bool) []uiBreadcrumbItem {
	items := []uiBreadcrumbItem{
		{Label: "Projects", Href: "/projects", HXGet: "/projects/panel"},
	}
	if len(showOwner) > 0 && showOwner[0] {
		items = append(items, uiBreadcrumbItem{
			Label: "@" + project.OwnerUsername,
			Href:  uiOwnerProjectsPath(project.OwnerUsername),
			HXGet: uiOwnerProjectsPanelPath(project.OwnerUsername),
		})
	}
	return append(items, uiBreadcrumbItem{
		Label: project.Name,
		Href:  uiProjectViewPath(project, "all"),
		HXGet: uiProjectPanelPath(project, "all"),
	})
}

func uiIssueBreadcrumb(project model.Project, issue model.Issue, parentIssue *model.Issue, showOwner ...bool) uiBreadcrumbData {
	items := uiProjectBreadcrumbItems(project, showOwner...)
	if parentIssue != nil {
		items = append(items, uiBreadcrumbItem{
			Label:    parentIssue.Identifier,
			Href:     uiIssuePath(*parentIssue),
			HXGet:    uiIssuePanelPath(*parentIssue),
			IssueKey: true,
		})
	}
	items = append(items, uiBreadcrumbItem{Label: issue.Identifier, IssueKey: true, Current: true})
	return uiBreadcrumbData{Items: items}
}

func uiIssueContextBreadcrumb(project model.Project, issue model.Issue, parentIssue *model.Issue, showOwner ...bool) uiBreadcrumbData {
	breadcrumb := uiIssueBreadcrumb(project, issue, parentIssue, showOwner...)
	issueItem := &breadcrumb.Items[len(breadcrumb.Items)-1]
	issueItem.Href = uiIssuePath(issue)
	issueItem.HXGet = uiIssuePanelPath(issue)
	issueItem.Current = false
	breadcrumb.Items = append(breadcrumb.Items, uiBreadcrumbItem{Label: "Context", Current: true})
	return breadcrumb
}

func uiUserAvatar(value any, class string) uiUserAvatarData {
	switch v := value.(type) {
	case model.User:
		return uiUserAvatarFields(v.ID, v.Name, v.Username, v.Email, v.ProfileImageThumbnailObjectID, class)
	case *model.User:
		if v == nil {
			return uiUserAvatarFields(uuid.Nil, "", "", "", nil, class)
		}
		return uiUserAvatarFields(v.ID, v.Name, v.Username, v.Email, v.ProfileImageThumbnailObjectID, class)
	case model.ProjectAssignee:
		return uiUserAvatarFields(v.ID, v.Name, v.Username, "", v.ProfileImageThumbnailObjectID, class)
	case *model.ProjectAssignee:
		if v == nil {
			return uiUserAvatarFields(uuid.Nil, "", "", "", nil, class)
		}
		return uiUserAvatarFields(v.ID, v.Name, v.Username, "", v.ProfileImageThumbnailObjectID, class)
	case model.ProjectAssigneeIssueStats:
		return uiUserAvatarFields(v.UserID, v.Name, v.Username, "", v.ProfileImageThumbnailObjectID, class)
	case *model.ProjectAssigneeIssueStats:
		if v == nil {
			return uiUserAvatarFields(uuid.Nil, "", "", "", nil, class)
		}
		return uiUserAvatarFields(v.UserID, v.Name, v.Username, "", v.ProfileImageThumbnailObjectID, class)
	case model.ProjectMember:
		return uiUserAvatarFields(v.UserID, v.Name, v.Username, "", v.ProfileImageThumbnailObjectID, class)
	case *model.ProjectMember:
		if v == nil {
			return uiUserAvatarFields(uuid.Nil, "", "", "", nil, class)
		}
		return uiUserAvatarFields(v.UserID, v.Name, v.Username, "", v.ProfileImageThumbnailObjectID, class)
	case model.ProjectChangelogActor:
		return uiUserAvatarFields(v.ID, v.Name, v.Username, "", v.ProfileImageThumbnailObjectID, class)
	case *model.ProjectChangelogActor:
		if v == nil {
			return uiUserAvatarFields(uuid.Nil, "", "", "", nil, class)
		}
		return uiUserAvatarFields(v.ID, v.Name, v.Username, "", v.ProfileImageThumbnailObjectID, class)
	case uiIssueCommentItem:
		return uiUserAvatarFields(v.AuthorID, v.AuthorName, v.AuthorUsername, v.AuthorEmail, v.AuthorProfileImageThumbnailObjectID, class)
	case *uiIssueCommentItem:
		if v == nil {
			return uiUserAvatarFields(uuid.Nil, "", "", "", nil, class)
		}
		return uiUserAvatarFields(v.AuthorID, v.AuthorName, v.AuthorUsername, v.AuthorEmail, v.AuthorProfileImageThumbnailObjectID, class)
	default:
		return uiUserAvatarFields(uuid.Nil, "", "", "", nil, class)
	}
}

func uiUserAvatarFields(id uuid.UUID, name, username, email string, thumbnailID *uuid.UUID, class string) uiUserAvatarData {
	label := uiUserLabel(name, username, email)
	out := uiUserAvatarData{
		ID:       id,
		Label:    label,
		Initials: uiInitials(name, uiUserFallback(username, email)),
		Class:    class,
	}
	if id != uuid.Nil && thumbnailID != nil {
		out.ThumbnailURL = uiUserProfileThumbnailPath(id, *thumbnailID)
	}
	return out
}

func uiUserLabel(name, username, email string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	if username = strings.TrimSpace(username); username != "" {
		return "@" + username
	}
	if email = strings.TrimSpace(email); email != "" {
		return email
	}
	return "User"
}

func uiUserFallback(username, email string) string {
	if username = strings.TrimSpace(username); username != "" {
		return username
	}
	return strings.TrimSpace(email)
}

func uiUserProfileThumbnailPath(userID, thumbnailID uuid.UUID) string {
	return fmt.Sprintf("/users/%s/profile-image/thumbnail/content?v=%s", userID, thumbnailID)
}

func uiProjectIcon(project model.Project, class string) uiProjectIconData {
	label := strings.TrimSpace(project.Name)
	if label == "" {
		label = strings.TrimSpace(project.Key)
	}
	if label == "" {
		label = "Project"
	}
	out := uiProjectIconData{
		Label:   label,
		Initial: uiProjectInitial(project.Name, project.Key),
		Class:   class,
	}
	if project.ImageThumbnailObjectID != nil {
		out.ThumbnailURL = uiProjectImageThumbnailContentPath(project) + "?v=" + project.ImageThumbnailObjectID.String()
	}
	return out
}

func uiProjectInitial(name, key string) string {
	source := strings.TrimSpace(name)
	if source == "" {
		source = strings.TrimSpace(key)
	}
	if source == "" {
		return "?"
	}
	return strings.ToUpper(string([]rune(source)[0]))
}

func uiProfileImagePicker(user model.User, csrfToken string) uiImagePickerData {
	avatar := uiUserAvatar(user, "")
	hasImage := user.ProfileImageThumbnailObjectID != nil
	return uiImagePickerData{
		CSRFToken: csrfToken,
		Modal: uiModalData{
			ID:               "profile-image-picker",
			Title:            "Profile image",
			Description:      "Choose an image to represent your account.",
			WidthClass:       "max-w-md",
			CancelLabel:      "Close profile image picker",
			ClientControlled: true,
		},
		Label:        avatar.Label,
		ThumbnailURL: avatar.ThumbnailURL,
		Fallback:     avatar.Initials,
		Circular:     true,
		TriggerLabel: uiImagePickerTriggerLabel(hasImage),
		UploadAction: "/settings/profile-image",
		DeleteAction: "/settings/profile-image/delete",
		HasImage:     hasImage,
	}
}

func uiProjectImagePicker(project model.Project, csrfToken string) uiImagePickerData {
	icon := uiProjectIcon(project, "")
	hasImage := project.ImageThumbnailObjectID != nil
	return uiImagePickerData{
		CSRFToken: csrfToken,
		Modal: uiModalData{
			ID:               "project-image-picker",
			Title:            "Project image",
			Description:      "Choose an image to identify this project.",
			WidthClass:       "max-w-md",
			CancelLabel:      "Close project image picker",
			ClientControlled: true,
		},
		Label:        icon.Label,
		ThumbnailURL: icon.ThumbnailURL,
		Fallback:     icon.Initial,
		TriggerLabel: uiImagePickerTriggerLabel(hasImage),
		UploadAction: uiProjectImagePath(project),
		DeleteAction: uiProjectImageDeletePath(project),
		HasImage:     hasImage,
		HXTarget:     "#main",
	}
}

func uiProjectGitHubConnectionModal(panel *uiProjectPanelData) uiModalData {
	return uiModalData{
		ID:               "project-github-connection",
		Title:            "Connect GitHub repository",
		Description:      "Add a repository that issues in this project can link to.",
		WidthClass:       "max-w-lg",
		CancelLabel:      "Cancel connecting GitHub repository",
		ClientControlled: true,
		Open:             panel.GitHubConnectionError != "",
	}
}

func uiTokenCreateModal(panel *uiTokenPanelData) uiModalData {
	return uiModalData{
		ID:               "token-create",
		Title:            "Create API token",
		Description:      "Name this token so you can identify and revoke it later.",
		WidthClass:       "max-w-md",
		CancelLabel:      "Cancel creating API token",
		ClientControlled: true,
		Open:             panel.Error != "" || panel.Created != "",
	}
}

func uiOAuthClientCreateModal(panel *uiTokenPanelData) uiModalData {
	return uiModalData{
		ID:               "oauth-client-create",
		Title:            "Register a connector",
		Description:      "Give the connector a name and the exact redirect URI it will use.",
		WidthClass:       "max-w-lg",
		CancelLabel:      "Cancel registering connector",
		ClientControlled: true,
		Open:             panel.OAuthError != "" || panel.CreatedClientSecret != "",
	}
}

func uiPasskeyCreateModal(uiLoginPanelData) uiModalData {
	return uiModalData{
		ID:               "passkey-create",
		Title:            "Add a passkey",
		Description:      "Choose a label that identifies this device or password manager.",
		WidthClass:       "max-w-md",
		CancelLabel:      "Cancel adding passkey",
		ClientControlled: true,
	}
}

func uiProjectMemberCreateModal(panel *uiProjectPanelData) uiModalData {
	return uiModalData{
		ID:               "project-member-create",
		Title:            "Add project member",
		Description:      "Choose a project user and the access they should receive.",
		WidthClass:       "max-w-lg",
		CancelLabel:      "Cancel adding project member",
		ClientControlled: true,
		Open:             panel.MemberError != "",
	}
}

func uiProjectDeleteModal(panel *uiProjectPanelData) uiModalData {
	return uiModalData{
		ID:               "project-delete",
		Title:            "Delete project",
		Description:      "This removes " + panel.Project.Name + " and every issue in it from the workspace.",
		WidthClass:       "max-w-md",
		CancelLabel:      "Cancel deleting project",
		CancelHXGet:      uiProjectPanelPath(panel.Project, panel.View),
		CancelHXPushURL:  "false",
		Badges:           []uiModalBadge{{Label: panel.Project.Key, Class: uiProjectDeleteBadgeClass}},
		ClientControlled: true,
		Open:             true,
	}
}

const uiProjectDeleteBadgeClass = "border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900 dark:bg-rose-950/40 dark:text-rose-200"

func uiProjectBlockCreateModal(panel *uiProjectPanelData) uiModalData {
	return uiModalData{
		ID:               "project-block-create",
		Title:            "Block project user",
		Description:      "Blocking removes project membership and overrides public access.",
		WidthClass:       "max-w-md",
		CancelLabel:      "Cancel blocking project user",
		ClientControlled: true,
		Open:             panel.BlockError != "",
	}
}

func uiTagCreateModal(data *uiTagManagerData) uiModalData {
	return uiModalData{
		ID:               "project-tag-create",
		Title:            "Create project tag",
		Description:      "Add a reusable label for issues in this project.",
		WidthClass:       "max-w-md",
		CancelLabel:      "Cancel creating project tag",
		ClientControlled: true,
		Open:             data.TagError != "",
	}
}

func uiTagAttachModal(data *uiTagManagerData) uiModalData {
	return uiModalData{
		ID:               "issue-tag-attach",
		Title:            "Attach tag",
		Description:      fmt.Sprintf("Choose a project tag for %s.", data.Issue.Identifier),
		WidthClass:       "max-w-md",
		CancelLabel:      "Cancel attaching tag",
		ClientControlled: true,
		Open:             data.AttachError != "",
	}
}

func uiIssueCommentCreateModal(panel *uiIssuePanelData) uiModalData {
	return uiModalData{
		ID:               "issue-comment-create",
		Title:            "Add comment",
		Description:      fmt.Sprintf("Post a comment on %s.", panel.Issue.Identifier),
		WidthClass:       "max-w-lg",
		CancelLabel:      "Cancel adding comment",
		ClientControlled: true,
		Open:             panel.CommentError != "",
	}
}

func uiIssueSubIssueCreateModal(panel *uiIssuePanelData) uiModalData {
	return uiModalData{
		ID:               "issue-sub-issue-create",
		Title:            "Add sub-issue",
		Description:      fmt.Sprintf("Create a child issue under %s.", panel.Issue.Identifier),
		WidthClass:       "max-w-lg",
		CancelLabel:      "Cancel adding sub-issue",
		CancelHXGet:      uiIssuePanelPath(panel.Issue),
		CancelHXPushURL:  "false",
		ClientControlled: true,
		Open:             true,
	}
}

func uiIssueLinkCreateModal(panel *uiIssuePanelData) uiModalData {
	return uiModalData{
		ID:               "issue-link-create",
		Title:            "Add issue link",
		Description:      fmt.Sprintf("Link another issue to %s.", panel.Issue.Identifier),
		WidthClass:       "max-w-lg",
		CancelLabel:      "Cancel adding issue link",
		CancelHXGet:      uiIssuePanelPath(panel.Issue),
		CancelHXPushURL:  "false",
		ClientControlled: true,
		Open:             true,
	}
}

func uiNewSprintCreateModal(panel *uiProjectPanelData) uiModalData {
	return uiModalData{
		ID:               "planned-sprint-create",
		Title:            "Create planned sprint",
		Description:      "Add a sprint to the project plan.",
		WidthClass:       "max-w-2xl",
		CancelLabel:      "Cancel creating planned sprint",
		CancelHXGet:      uiProjectPanelPath(panel.Project, "planned"),
		CancelHXPushURL:  "false",
		ClientControlled: true,
		Open:             true,
	}
}

func uiActiveSprintIssueCreateModal(panel *uiProjectPanelData) uiModalData {
	return uiModalData{
		ID:               "active-sprint-issue-create",
		Title:            "Add issue to sprint",
		Description:      fmt.Sprintf("Move an issue into %s.", uiSprintLabel(*panel.ActiveSprint)),
		WidthClass:       "max-w-md",
		CancelLabel:      "Cancel adding issue to sprint",
		CancelHXGet:      uiProjectPanelPath(panel.Project, "sprint"),
		CancelHXPushURL:  "false",
		ClientControlled: true,
		Open:             true,
	}
}

func uiPlannedSprintIssueCreateModal(panel *uiProjectPanelData, sprint model.Sprint) uiModalData {
	return uiModalData{
		ID:               "planned-sprint-issue-create",
		Title:            "Add issue to sprint",
		Description:      fmt.Sprintf("Move an issue into %s.", uiSprintLabel(sprint)),
		WidthClass:       "max-w-md",
		CancelLabel:      "Cancel adding issue to sprint",
		CancelHXGet:      uiProjectPanelPath(panel.Project, "planned"),
		CancelHXPushURL:  "false",
		ClientControlled: true,
		Open:             true,
	}
}

func uiImagePickerTriggerLabel(hasImage bool) string {
	if hasImage {
		return "Change image"
	}
	return "Add image"
}

func uiChangelogActor(entry model.ProjectChangelogEntry) string {
	if entry.Actor == nil {
		return "System"
	}
	if strings.TrimSpace(entry.Actor.Name) != "" {
		return entry.Actor.Name
	}
	if strings.TrimSpace(entry.Actor.Username) != "" {
		return "@" + entry.Actor.Username
	}
	return "Unknown"
}

func uiChangelogIcon(entry model.ProjectChangelogEntry) string {
	switch entry.Entity {
	case "comment":
		return "message-square"
	case "issue_link":
		return "link"
	case "issue_attachment", "sprint_attachment", "project_attachment", "context_attachment":
		return "paperclip"
	case "issue_tag", "issue_tag_link":
		return "tag"
	case "project_context", "issue_context_link":
		return "book-open"
	case "whiteboard_page":
		return "presentation"
	case "sprint":
		return "calendar-range"
	case "project_member":
		return "users"
	case "project":
		return "folder"
	default:
		return "history"
	}
}

func uiChangelogTargetHref(project model.Project, entry model.ProjectChangelogEntry) string {
	if entry.IssueID == nil || strings.TrimSpace(entry.TargetRef) == "" {
		return ""
	}
	return "/" + project.OwnerUsername + "/issues/" + entry.TargetRef
}

func uiSprintDate(t time.Time) string {
	return t.Format("Jan 2")
}

func uiSprintDateRange(sprint model.Sprint) string {
	if sprint.StartDate == nil && sprint.EndDate == nil {
		return ""
	}
	if sprint.StartDate == nil {
		return uiSprintDate(*sprint.EndDate)
	}
	if sprint.EndDate == nil {
		return uiSprintDate(*sprint.StartDate)
	}
	return uiSprintDate(*sprint.StartDate) + "-" + uiSprintDate(*sprint.EndDate)
}

func uiSprintHistoryDateRange(sprint model.Sprint) string {
	if sprint.StartDate == nil || sprint.EndDate == nil {
		return "No scheduled dates"
	}
	start := sprint.StartDate.Format("Jan 2")
	if sprint.StartDate.Year() != sprint.EndDate.Year() {
		start = sprint.StartDate.Format("Jan 2, 2006")
	}
	return start + "-" + sprint.EndDate.Format("Jan 2, 2006")
}

func uiSprintLabel(sprint model.Sprint) string {
	name := strings.TrimSpace(sprint.Name)
	if name != "" {
		return name
	}
	return uiIssueSprintRef(sprint)
}

func uiCanEditIssueSprint(issue model.Issue) bool {
	return issue.ParentIssueID == nil && !issue.Status.CountsAsDone()
}

func uiDueDateValue(d *model.Date) string {
	if d == nil {
		return ""
	}
	return d.String()
}

func uiDueDateShort(d *model.Date) string {
	if d == nil {
		return ""
	}
	return d.Time().Format("Jan 2")
}

func uiDueDateFull(d *model.Date) string {
	if d == nil {
		return ""
	}
	return d.Time().Format("Jan 2, 2006")
}

func uiTagColors() []model.IssueTagColor {
	return []model.IssueTagColor{
		model.TagColorSlate,
		model.TagColorRed,
		model.TagColorOrange,
		model.TagColorAmber,
		model.TagColorYellow,
		model.TagColorGreen,
		model.TagColorTeal,
		model.TagColorCyan,
		model.TagColorBlue,
		model.TagColorViolet,
		model.TagColorPink,
	}
}

func uiTagClass(color model.IssueTagColor) string {
	switch model.IssueTagColorOrDefault(color) {
	case model.TagColorSlate:
		return "border-slate-200 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"
	case model.TagColorRed:
		return "border-red-200 bg-red-50 text-red-700 dark:border-red-900/70 dark:bg-red-950/30 dark:text-red-200"
	case model.TagColorOrange:
		return "border-orange-200 bg-orange-50 text-orange-700 dark:border-orange-900/70 dark:bg-orange-950/30 dark:text-orange-200"
	case model.TagColorAmber:
		return "border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-200"
	case model.TagColorYellow:
		return "border-yellow-200 bg-yellow-50 text-yellow-800 dark:border-yellow-900/70 dark:bg-yellow-950/30 dark:text-yellow-200"
	case model.TagColorGreen:
		return "border-green-200 bg-green-50 text-green-700 dark:border-green-900/70 dark:bg-green-950/30 dark:text-green-200"
	case model.TagColorTeal:
		return "border-teal-200 bg-teal-50 text-teal-700 dark:border-teal-900/70 dark:bg-teal-950/30 dark:text-teal-200"
	case model.TagColorCyan:
		return "border-cyan-200 bg-cyan-50 text-cyan-700 dark:border-cyan-900/70 dark:bg-cyan-950/30 dark:text-cyan-200"
	case model.TagColorViolet:
		return "border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-900/70 dark:bg-violet-950/30 dark:text-violet-200"
	case model.TagColorPink:
		return "border-pink-200 bg-pink-50 text-pink-700 dark:border-pink-900/70 dark:bg-pink-950/30 dark:text-pink-200"
	default:
		return "border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-900/70 dark:bg-blue-950/30 dark:text-blue-200"
	}
}

func uiTagDotClass(color model.IssueTagColor) string {
	switch model.IssueTagColorOrDefault(color) {
	case model.TagColorSlate:
		return "bg-slate-500"
	case model.TagColorRed:
		return "bg-red-500"
	case model.TagColorOrange:
		return "bg-orange-500"
	case model.TagColorAmber:
		return "bg-amber-500"
	case model.TagColorYellow:
		return "bg-yellow-500"
	case model.TagColorGreen:
		return "bg-green-500"
	case model.TagColorTeal:
		return "bg-teal-500"
	case model.TagColorCyan:
		return "bg-cyan-500"
	case model.TagColorViolet:
		return "bg-violet-500"
	case model.TagColorPink:
		return "bg-pink-500"
	default:
		return "bg-blue-500"
	}
}

func uiIssueVisibleTags(tags []model.IssueTag) []model.IssueTag {
	if len(tags) <= 3 {
		return tags
	}
	return tags[:3]
}

func uiIssueAttachmentIcon(attachment model.IssueAttachment) string {
	if storageObjectSafeInlineImage(attachment.Object) {
		return "image"
	}
	return "paperclip"
}

func uiIssueAttachmentImage(attachment model.IssueAttachment) bool {
	return storageObjectSafeInlineImage(attachment.Object)
}

func uiIssueAttachmentMarkdown(attachment model.IssueAttachment) string {
	return uiStorageObjectMarkdown(attachment.Object)
}

func uiByteSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(n)
	for _, unit := range units {
		value /= 1024
		if value < 1024 {
			if value >= 10 {
				return fmt.Sprintf("%.0f %s", value, unit)
			}
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%.0f PB", value/1024)
}

func uiIssueHiddenTagCount(tags []model.IssueTag) int {
	if len(tags) <= 3 {
		return 0
	}
	return len(tags) - 3
}

func uiDueBadgeClass(issue model.Issue) string {
	if uiIssueOverdue(issue, time.Now()) {
		return "border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900/70 dark:bg-rose-950/30 dark:text-rose-200"
	}
	if uiIssueDueSoon(issue, time.Now()) {
		return "border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-200"
	}
	return "border-slate-200 bg-white text-slate-600 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-300"
}

func uiDueBadgeIcon(issue model.Issue) string {
	if uiIssueDueSoon(issue, time.Now()) {
		return "clock"
	}
	return "calendar"
}

func uiDueBadgeLabel(issue model.Issue) string {
	if issue.DueDate == nil {
		return ""
	}
	if days, ok := uiIssueDueDays(issue, time.Now()); ok && days >= 0 && days < 7 {
		if days == 0 {
			return "Today"
		}
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return uiDueDateShort(issue.DueDate)
}

func uiIssueOverdue(issue model.Issue, now time.Time) bool {
	days, ok := uiIssueDueDays(issue, now)
	return ok && days < 0
}

func uiIssueDueSoon(issue model.Issue, now time.Time) bool {
	days, ok := uiIssueDueDays(issue, now)
	return ok && days >= 0 && days < 7
}

func uiIssueDueDays(issue model.Issue, now time.Time) (int, bool) {
	if issue.DueDate == nil || issue.Status.CountsAsDone() {
		return 0, false
	}
	current := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	due := issue.DueDate.Time()
	due = time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, current.Location())
	return int(due.Sub(current).Hours() / 24), true
}

func uiStatusLabel(s model.Status) string {
	switch s {
	case model.StatusTodo:
		return "To do"
	case model.StatusInProgress:
		return "In progress"
	case model.StatusDone:
		return "Done"
	case model.StatusClosed:
		return "Closed"
	default:
		return string(s)
	}
}

func uiStatusValue(raw string) model.Status {
	return model.Status(raw)
}

func uiStatusClass(s model.Status) string {
	switch s {
	case model.StatusTodo:
		return "border-slate-300 bg-slate-100 text-slate-700 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
	case model.StatusInProgress:
		return "border-blue-300 bg-blue-50 text-blue-800 dark:border-blue-500/40 dark:bg-blue-950/40 dark:text-blue-200"
	case model.StatusDone:
		return "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-500/40 dark:bg-emerald-950/40 dark:text-emerald-200"
	case model.StatusClosed:
		return "border-zinc-300 bg-zinc-100 text-zinc-700 dark:border-zinc-600 dark:bg-zinc-900 dark:text-zinc-200"
	default:
		return "border-slate-300 bg-slate-50 text-slate-700 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200"
	}
}

type uiStatusOption struct {
	Status model.Status
	Label  string
}

func uiStatusOptions() []uiStatusOption {
	return []uiStatusOption{
		{Status: model.StatusTodo, Label: uiStatusLabel(model.StatusTodo)},
		{Status: model.StatusInProgress, Label: uiStatusLabel(model.StatusInProgress)},
		{Status: model.StatusDone, Label: uiStatusLabel(model.StatusDone)},
		{Status: model.StatusClosed, Label: uiStatusLabel(model.StatusClosed)},
	}
}

func uiIssueStatusDropdown(panel *uiIssuePanelData) uiOptionDropdownData {
	options := make([]uiOptionDropdownOption, 0, len(uiStatusOptions()))
	for _, option := range uiStatusOptions() {
		options = append(options, uiOptionDropdownOption{
			Value: string(option.Status),
			Label: option.Label,
			Class: uiStatusClass(option.Status),
		})
	}
	return uiOptionDropdownData{
		CSRFToken:    panel.CSRFToken,
		Action:       uiIssueStatusPath(panel.Issue),
		HXTarget:     "#main",
		HXPushURL:    "false",
		CancelHXGet:  uiIssuePanelPath(panel.Issue),
		ToggleLabel:  "Change status",
		ListLabel:    "Issue status",
		Name:         "status",
		CurrentValue: string(panel.Issue.Status),
		CurrentLabel: uiStatusLabel(panel.Issue.Status),
		CurrentClass: uiStatusClass(panel.Issue.Status),
		Options:      options,
	}
}

func uiCloseReasonLabel(v any) string {
	var reason model.IssueCloseReason
	switch r := v.(type) {
	case model.IssueCloseReason:
		reason = r
	case *model.IssueCloseReason:
		if r == nil {
			return ""
		}
		reason = *r
	default:
		return fmt.Sprint(v)
	}
	switch reason {
	case model.CloseReasonDuplicate:
		return "Duplicate"
	case model.CloseReasonWontDo:
		return "Won't Do"
	case model.CloseReasonInvalid:
		return "Invalid"
	default:
		return string(reason)
	}
}

type uiCloseReasonOption struct {
	Reason model.IssueCloseReason
	Label  string
}

func uiCloseReasonOptions() []uiCloseReasonOption {
	return []uiCloseReasonOption{
		{Reason: model.CloseReasonDuplicate, Label: uiCloseReasonLabel(model.CloseReasonDuplicate)},
		{Reason: model.CloseReasonWontDo, Label: uiCloseReasonLabel(model.CloseReasonWontDo)},
		{Reason: model.CloseReasonInvalid, Label: uiCloseReasonLabel(model.CloseReasonInvalid)},
	}
}

func uiIssueCloseReasonDropdown(panel *uiIssuePanelData) uiOptionDropdownData {
	currentValue := strings.TrimSpace(panel.CloseReasonInput)
	if currentValue == "" && panel.Issue.CloseReason != nil {
		currentValue = string(*panel.Issue.CloseReason)
	}
	currentReason := model.IssueCloseReason(currentValue)
	currentLabel := "Close reason"
	currentClass := "border-slate-200 bg-white text-slate-700 dark:border-slate-800 dark:bg-slate-950 dark:text-slate-200"
	if currentReason.Valid() {
		currentLabel = uiCloseReasonLabel(currentReason)
		currentClass = uiCloseReasonClass(currentReason)
	}
	options := make([]uiOptionDropdownOption, 0, len(uiCloseReasonOptions()))
	for _, option := range uiCloseReasonOptions() {
		options = append(options, uiOptionDropdownOption{
			Value: string(option.Reason),
			Label: option.Label,
			Class: uiCloseReasonClass(option.Reason),
		})
	}
	return uiOptionDropdownData{
		CSRFToken:    panel.CSRFToken,
		Action:       uiIssueCloseReasonPath(panel.Issue),
		HXTarget:     "#main",
		HXPushURL:    "false",
		CancelHXGet:  uiIssuePanelPath(panel.Issue),
		ToggleLabel:  "Choose close reason",
		ListLabel:    "Close reason",
		Name:         "close_reason",
		CurrentValue: currentValue,
		CurrentLabel: currentLabel,
		CurrentClass: currentClass,
		Error:        panel.CloseReasonError,
		Options:      options,
	}
}

func uiCloseReasonClass(model.IssueCloseReason) string {
	return "border-zinc-300 bg-white text-zinc-700 dark:border-zinc-700 dark:bg-slate-950 dark:text-zinc-200"
}

func uiCloseReasonModal(panel *uiIssuePanelData) uiModalData {
	return uiModalData{
		ID:              "close-reason",
		Title:           "Close issue",
		Description:     fmt.Sprintf("Choose a reason to close %s.", panel.Issue.Identifier),
		WidthClass:      "max-w-sm",
		CancelLabel:     "Cancel editing close reason",
		CancelHXGet:     uiIssuePanelPath(panel.Issue),
		CancelHXPushURL: "false",
		Badges: []uiModalBadge{
			{
				Label: uiStatusLabel(model.StatusClosed),
				Class: uiStatusClass(model.StatusClosed),
			},
		},
	}
}

func uiIssueTagsModal(panel *uiIssuePanelData) uiModalData {
	return uiModalData{
		ID:              "issue-tags",
		Title:           "Manage tags",
		Description:     fmt.Sprintf("Search project tags for %s.", panel.Issue.Identifier),
		WidthClass:      "max-w-lg",
		CancelLabel:     "Close tag manager",
		CancelHXGet:     uiIssuePanelPath(panel.Issue),
		CancelHXPushURL: "false",
		Badges: []uiModalBadge{
			{
				Label: panel.Issue.Identifier,
				Class: "border-slate-300 bg-white font-mono text-[11px] font-semibold uppercase leading-4 text-slate-600 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-300",
			},
		},
	}
}

func uiIssueGitHubLinkModal(panel *uiIssuePanelData) uiModalData {
	return uiModalData{
		ID:               "issue-github-link",
		Title:            "Link GitHub branch or pull request",
		Description:      fmt.Sprintf("Choose a connected repository and reference for %s.", panel.Issue.Identifier),
		WidthClass:       "max-w-lg",
		CancelLabel:      "Cancel linking GitHub resource",
		ClientControlled: true,
		Open:             panel.GitHubError != "",
		Badges: []uiModalBadge{
			{
				Label: panel.Issue.Identifier,
				Class: "border-slate-300 bg-white font-mono text-[11px] font-semibold uppercase leading-4 text-slate-600 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-300",
			},
		},
	}
}

type uiSubIssueProgressData struct {
	Total             int
	Todo              int
	InProgress        int
	Done              int
	DonePercent       string
	InProgressPercent string
	TodoPercent       string
	Label             string
}

func uiIssueItemFromIssue(issue model.Issue) uiIssueItem {
	return uiIssueItem{Issue: issue}
}

func uiSubIssueProgress(issues []model.Issue) uiSubIssueProgressData {
	out := uiSubIssueProgressData{Total: len(issues)}
	for _, issue := range issues {
		switch {
		case issue.Status.CountsAsDone():
			out.Done++
		case issue.Status == model.StatusInProgress:
			out.InProgress++
		default:
			out.Todo++
		}
	}
	out.DonePercent = uiPercent(out.Done, out.Total)
	out.InProgressPercent = uiPercent(out.InProgress, out.Total)
	out.TodoPercent = uiPercent(out.Todo, out.Total)
	if out.Total == 0 {
		out.Label = "Sub-issue progress: no sub-issues"
	} else {
		out.Label = fmt.Sprintf("Sub-issue progress: %d done, %d in progress, %d to do", out.Done, out.InProgress, out.Todo)
	}
	return out
}

func uiLinkedIssueProgress(links []uiIssueLinkItem) uiSubIssueProgressData {
	out := uiSubIssueProgressData{}
	for _, link := range links {
		if !link.HasIssue {
			continue
		}
		out.Total++
		switch {
		case link.LinkedIssue.Status.CountsAsDone():
			out.Done++
		case link.LinkedIssue.Status == model.StatusInProgress:
			out.InProgress++
		default:
			out.Todo++
		}
	}
	out.DonePercent = uiPercent(out.Done, out.Total)
	out.InProgressPercent = uiPercent(out.InProgress, out.Total)
	out.TodoPercent = uiPercent(out.Todo, out.Total)
	if out.Total == 0 {
		if len(links) == 0 {
			out.Label = "Linked issue progress: no linked issues"
		} else {
			out.Label = "Linked issue progress: no available linked issues"
		}
	} else {
		out.Label = fmt.Sprintf("Linked issue progress: %d done, %d in progress, %d to do", out.Done, out.InProgress, out.Todo)
	}
	return out
}

func uiIssueColumnCount(columns []uiIssueColumn) int {
	total := 0
	for _, column := range columns {
		total += len(column.Issues)
	}
	return total
}

func uiSprintIssueCount(count int) string {
	label := "Issues"
	if count == 1 {
		label = "Issue"
	}
	return fmt.Sprintf("%d %s", count, label)
}

func uiPercent(part, total int) string {
	if total <= 0 || part <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.2f%%", (float64(part)/float64(total))*100)
}

func uiPriorityClass(priority model.IssuePriority) string {
	switch priority {
	case model.PriorityP0:
		return "bg-red-600"
	case model.PriorityP1:
		return "bg-orange-500"
	case model.PriorityP2:
		return "bg-amber-500"
	case model.PriorityP3:
		return "bg-yellow-500"
	case model.PriorityP4:
		return "bg-gray-500"
	case "":
		return "bg-amber-500"
	default:
		return "bg-gray-500"
	}
}

func uiPriorityLabel(priority model.IssuePriority) string {
	if priority == "" {
		return string(model.PriorityP2)
	}
	return string(priority)
}

type uiPriorityOption struct {
	Priority model.IssuePriority
}

func uiPriorityOptions() []uiPriorityOption {
	return []uiPriorityOption{
		{Priority: model.PriorityP0},
		{Priority: model.PriorityP1},
		{Priority: model.PriorityP2},
		{Priority: model.PriorityP3},
		{Priority: model.PriorityP4},
	}
}

func uiNewIssueSelectedPriority(data *uiNewIssuePanelData) model.IssuePriority {
	if data == nil {
		return model.PriorityP2
	}
	priority := model.IssuePriority(data.Priority)
	if priority.Valid() {
		return priority
	}
	return model.PriorityP2
}

func uiNewIssueProjectSelected(data *uiNewIssuePanelData, project model.Project) bool {
	if data == nil {
		return false
	}
	return data.ProjectID == project.ID.String()
}

func uiNewIssueProjectInput(data *uiNewIssuePanelData) string {
	if data == nil {
		return ""
	}
	if data.ProjectInput != "" {
		return data.ProjectInput
	}
	if data.HasProject {
		return uiNewIssueProjectLabel(data.Project)
	}
	return ""
}

func uiNewIssueProjectLabel(project model.Project) string {
	if project.Name == "" {
		return project.Key
	}
	return project.Key + " - " + project.Name
}

func uiFilterNewIssueProjects(projects []model.Project, raw string) []model.Project {
	query := strings.ToLower(strings.TrimSpace(raw))
	if query == "" {
		return projects
	}
	terms := strings.Fields(query)
	out := make([]model.Project, 0, len(projects))
	for _, project := range projects {
		haystack := strings.ToLower(project.Key + " " + project.Name + " " + project.OwnerUsername)
		matches := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matches = false
				break
			}
		}
		if matches {
			out = append(out, project)
		}
	}
	return out
}

func uiStatusRowClass(s model.Status) string {
	switch s {
	case model.StatusTodo:
		return "bg-slate-50/70 hover:bg-slate-100/80 dark:bg-slate-900/30 dark:hover:bg-slate-800/70"
	case model.StatusInProgress:
		return "bg-blue-50/45 hover:bg-blue-50 dark:bg-blue-950/15 dark:hover:bg-blue-950/30"
	case model.StatusDone:
		return "bg-emerald-50/45 hover:bg-emerald-50 dark:bg-emerald-950/15 dark:hover:bg-emerald-950/30"
	case model.StatusClosed:
		return "bg-zinc-50/70 hover:bg-zinc-100/80 dark:bg-zinc-900/35 dark:hover:bg-zinc-800/70"
	default:
		return "bg-white hover:bg-slate-50 dark:bg-slate-900 dark:hover:bg-slate-800/60"
	}
}

func uiStatusSurfaceClass(s model.Status) string {
	switch s {
	case model.StatusTodo:
		return "bg-slate-50/70 dark:bg-slate-900/30"
	case model.StatusInProgress:
		return "bg-blue-50/45 dark:bg-blue-950/15"
	case model.StatusDone:
		return "bg-emerald-50/45 dark:bg-emerald-950/15"
	case model.StatusClosed:
		return "bg-zinc-50/70 dark:bg-zinc-900/35"
	default:
		return "bg-white dark:bg-slate-900"
	}
}

func uiIssueColumnStatus(s model.Status) model.Status {
	if s.CountsAsDone() {
		return model.StatusDone
	}
	return s
}

func uiIssueColumns() []uiIssueColumn {
	return []uiIssueColumn{
		{Status: model.StatusTodo, Label: uiStatusLabel(model.StatusTodo)},
		{Status: model.StatusInProgress, Label: uiStatusLabel(model.StatusInProgress)},
		{Status: model.StatusDone, Label: uiStatusLabel(model.StatusDone)},
	}
}

type uiIssueLinkOption struct {
	Value string
	Label string
}

func uiIssueLinkOptions() []uiIssueLinkOption {
	return []uiIssueLinkOption{
		{Value: string(model.LinkTypeRelatesTo), Label: "Relates to"},
		{Value: string(model.LinkTypeBlocks), Label: "Blocks"},
		{Value: "blocked_by", Label: "Blocked by"},
		{Value: string(model.LinkTypeDuplicates), Label: "Duplicates"},
		{Value: "duplicated_by", Label: "Duplicated by"},
		{Value: string(model.LinkTypeClones), Label: "Clones"},
		{Value: "cloned_by", Label: "Cloned by"},
	}
}

func uiIssueLinkRelation(link model.IssueLink, issueID uuid.UUID) string {
	if link.SourceID == issueID {
		return string(link.LinkType)
	}
	switch link.LinkType {
	case model.LinkTypeBlocks:
		return "blocked_by"
	case model.LinkTypeDuplicates:
		return "duplicated_by"
	case model.LinkTypeClones:
		return "cloned_by"
	default:
		return string(link.LinkType)
	}
}

func uiIssueLinkRelationParams(issueID, otherID uuid.UUID, relation string) (uuid.UUID, uuid.UUID, model.LinkType, bool) {
	switch model.LinkType(relation) {
	case model.LinkTypeRelatesTo:
		return issueID, otherID, model.LinkTypeRelatesTo, true
	case model.LinkTypeBlocks:
		return issueID, otherID, model.LinkTypeBlocks, true
	case model.LinkTypeDuplicates:
		return issueID, otherID, model.LinkTypeDuplicates, true
	case model.LinkTypeClones:
		return issueID, otherID, model.LinkTypeClones, true
	}
	switch relation {
	case "blocked_by":
		return otherID, issueID, model.LinkTypeBlocks, true
	case "duplicated_by":
		return otherID, issueID, model.LinkTypeDuplicates, true
	case "cloned_by":
		return otherID, issueID, model.LinkTypeClones, true
	default:
		return uuid.Nil, uuid.Nil, "", false
	}
}

func uiIssueLinkLabel(link model.IssueLink, issueID uuid.UUID) string {
	outgoing := link.SourceID == issueID
	switch link.LinkType {
	case model.LinkTypeBlocks:
		if outgoing {
			return "Blocks"
		}
		return "Blocked by"
	case model.LinkTypeDuplicates:
		if outgoing {
			return "Duplicates"
		}
		return "Duplicated by"
	case model.LinkTypeRelatesTo:
		return "Relates to"
	case model.LinkTypeClones:
		if outgoing {
			return "Clones"
		}
		return "Cloned by"
	default:
		return string(link.LinkType)
	}
}

type uiLocalTimeData struct {
	Valid    bool
	Datetime string
	Fallback string
}

func uiTokenTime(v any) uiLocalTimeData {
	if v == nil {
		return uiLocalTimeData{}
	}
	switch t := v.(type) {
	case time.Time:
		return uiLocalTime(t)
	case *time.Time:
		if t == nil {
			return uiLocalTimeData{}
		}
		return uiLocalTime(*t)
	default:
		return uiLocalTimeData{}
	}
}

func uiLocalTime(t time.Time) uiLocalTimeData {
	utc := t.UTC()
	return uiLocalTimeData{
		Valid:    true,
		Datetime: utc.Format(time.RFC3339Nano),
		Fallback: utc.Format("Jan 2, 2006 15:04 UTC"),
	}
}
