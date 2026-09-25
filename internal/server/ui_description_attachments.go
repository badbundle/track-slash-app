package server

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/bradleymackey/track-slash/internal/model"
)

func uiDescriptionAttachmentForObject(object model.StorageObject, contentHref, deleteHref, deleteJSONHref string) uiDescriptionAttachment {
	return uiDescriptionAttachment{
		Object:         object,
		ContentHref:    contentHref,
		InlineHref:     contentHref + "?inline=1",
		DeleteHref:     deleteHref,
		DeleteJSONHref: deleteJSONHref,
		Markdown:       uiStorageObjectMarkdown(object),
		InlineImage:    storageObjectSafeInlineImage(object),
	}
}

func uiIssueAttachmentListData(panel *uiIssuePanelData) uiAttachmentListData {
	items := make([]uiDescriptionAttachment, 0, len(panel.Attachments))
	for _, attachment := range panel.Attachments {
		base := uiIssueAttachmentsPath(panel.Issue) + "/" + attachment.Object.Ref
		items = append(items, uiDescriptionAttachmentForObject(
			attachment.Object,
			uiIssueAttachmentContentPath(panel.Issue, attachment.Object),
			uiIssueAttachmentDeletePath(panel.Issue, attachment.Object),
			base,
		))
	}
	return uiAttachmentListData{
		ID:        "issue-attachments-list",
		Items:     items,
		HasMore:   panel.AttachmentsHasMore,
		Editing:   panel.EditDescription,
		CanDelete: true,
		UploadURL: uiIssueAttachmentsPath(panel.Issue),
	}
}

func uiSprintAttachmentListData(project model.Project, sprint model.Sprint, attachments []model.SprintAttachment, hasMore, editing bool) uiAttachmentListData {
	items := make([]uiDescriptionAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		base := uiProjectSprintAttachmentsPath(project, sprint) + "/" + attachment.Object.Ref
		items = append(items, uiDescriptionAttachmentForObject(
			attachment.Object,
			uiProjectSprintAttachmentContentPath(project, sprint, attachment.Object),
			uiProjectSprintAttachmentDeletePath(project, sprint, attachment.Object),
			base,
		))
	}
	return uiAttachmentListData{
		ID:        "sprint-attachments-" + sprint.Ref,
		Items:     items,
		HasMore:   hasMore,
		Editing:   editing,
		CanDelete: sprint.Status != model.SprintStatusCompleted,
		UploadURL: uiProjectSprintAttachmentsPath(project, sprint),
	}
}

func uiProjectAttachmentListData(project model.Project, attachments []model.ProjectAttachment, hasMore, editing bool) uiAttachmentListData {
	items := make([]uiDescriptionAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		base := uiProjectAttachmentsPath(project) + "/" + attachment.Object.Ref
		items = append(items, uiDescriptionAttachmentForObject(
			attachment.Object,
			uiProjectAttachmentContentPath(project, attachment.Object),
			uiProjectAttachmentDeletePath(project, attachment.Object),
			base,
		))
	}
	return uiAttachmentListData{
		ID: "project-attachments-list", Items: items, HasMore: hasMore, Editing: editing, CanDelete: true,
		UploadURL: uiProjectAttachmentsPath(project),
	}
}

func uiContextAttachmentListData(panel *uiContextManagerData, editing bool) uiAttachmentListData {
	items := make([]uiDescriptionAttachment, 0, len(panel.Attachments))
	for _, attachment := range panel.Attachments {
		base := uiProjectContextAttachmentsPath(panel.Project, panel.ActiveContext) + "/" + attachment.Object.Ref
		items = append(items, uiDescriptionAttachmentForObject(
			attachment.Object,
			uiProjectContextAttachmentContentPath(panel.Project, panel.ActiveContext, attachment.Object),
			uiProjectContextAttachmentDeletePath(panel.Project, panel.ActiveContext, attachment.Object),
			base,
		))
	}
	return uiAttachmentListData{
		ID: "context-attachments-" + panel.ActiveContext.Ref, Items: items,
		HasMore: panel.AttachmentsHasMore, Editing: editing, CanDelete: true,
		UploadURL: uiProjectContextAttachmentsPath(panel.Project, panel.ActiveContext),
	}
}

func uiContextEditor(panel *uiContextManagerData) uiDescriptionEditorData {
	return uiDescriptionEditorData{
		Name: "body", Source: panel.ContextEditBody, Rows: 12, Autofocus: false,
		UploadURL:  uiProjectContextAttachmentsPath(panel.Project, panel.ActiveContext),
		ListTarget: "#context-attachments-" + panel.ActiveContext.Ref, Placeholder: "Markdown",
	}
}

func uiContextBody(panel *uiContextManagerData) uiDescriptionBodyData {
	return uiDescriptionBodyData{Source: panel.ActiveContext.Body, HTML: panel.ActiveHTML, EmptyLabel: "No content yet."}
}

// Placeholders hint at what belongs in an empty Markdown description. The
// shared editors and the standalone create forms use the same text so an
// empty field reads the same wherever it is written.
const (
	uiIssueDescriptionPlaceholder   = "Describe the issue: context, steps to reproduce, acceptance criteria… Markdown is supported."
	uiProjectDescriptionPlaceholder = "What is this project for? Markdown is supported."
	uiSprintDescriptionPlaceholder  = "What should this sprint achieve? Markdown is supported."
)

func uiIssueDescriptionPlaceholderText() string   { return uiIssueDescriptionPlaceholder }
func uiProjectDescriptionPlaceholderText() string { return uiProjectDescriptionPlaceholder }

func uiIssueDescriptionEditor(panel *uiIssuePanelData) uiDescriptionEditorData {
	return uiDescriptionEditorData{
		Name:        "description",
		Source:      panel.Issue.Description,
		Rows:        7,
		Autofocus:   true,
		UploadURL:   uiIssueAttachmentsPath(panel.Issue),
		ListTarget:  "#issue-attachments-list",
		Placeholder: uiIssueDescriptionPlaceholder,
	}
}

func uiSprintDescriptionEditor(project model.Project, sprint model.Sprint, source string, autofocus bool) uiDescriptionEditorData {
	return uiDescriptionEditorData{
		Name:        "goal",
		Source:      source,
		Rows:        4,
		Autofocus:   autofocus,
		UploadURL:   uiProjectSprintAttachmentsPath(project, sprint),
		ListTarget:  "#sprint-attachments-" + sprint.Ref,
		Placeholder: uiSprintDescriptionPlaceholder,
	}
}

func uiProjectDescriptionEditor(project model.Project, source string) uiDescriptionEditorData {
	return uiDescriptionEditorData{
		Name: "description", Source: source, Rows: 7, Autofocus: true,
		UploadURL: uiProjectAttachmentsPath(project), ListTarget: "#project-attachments-list", Placeholder: uiProjectDescriptionPlaceholder,
	}
}

func uiNewSprintDescriptionEditor(source string) uiDescriptionEditorData {
	return uiDescriptionEditorData{Name: "goal", Source: source, Rows: 4, Placeholder: uiSprintDescriptionPlaceholder}
}

func uiStorageObjectMarkdown(object model.StorageObject) string {
	label := strings.ReplaceAll(object.Filename, `\`, `\\`)
	label = strings.ReplaceAll(label, "]", `\]`)
	if strings.TrimSpace(label) == "" {
		label = object.Ref
	}
	if storageObjectSafeInlineImage(object) {
		return fmt.Sprintf("![%s](%s)", label, object.Ref)
	}
	return fmt.Sprintf("[%s](%s)", label, object.Ref)
}

func uiIssueDescriptionBody(panel *uiIssuePanelData) uiDescriptionBodyData {
	return uiDescriptionBodyData{Source: panel.Issue.Description, HTML: panel.DescriptionHTML, EmptyLabel: "No description."}
}

func uiSprintDescriptionBody(sprint model.Sprint, html template.HTML) uiDescriptionBodyData {
	return uiDescriptionBodyData{Source: sprint.Goal, HTML: html}
}

func uiProjectDescriptionBody(project model.Project, html template.HTML) uiDescriptionBodyData {
	return uiDescriptionBodyData{Source: project.Description, HTML: html, EmptyLabel: "No description."}
}
