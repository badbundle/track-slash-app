package server

import (
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestUIDescriptionEditorsShareFieldPlaceholders(t *testing.T) {
	t.Parallel()

	project := model.Project{Key: "TRACK", OwnerUsername: "owner"}
	sprint := model.Sprint{Number: 1}
	issuePanel := &uiIssuePanelData{Issue: model.Issue{ProjectKey: "TRACK", OwnerUsername: "owner", Number: 1}}

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{name: "issue editor", got: uiIssueDescriptionEditor(issuePanel).Placeholder, want: uiIssueDescriptionPlaceholder},
		{name: "new issue form", got: uiIssueDescriptionPlaceholderText(), want: uiIssueDescriptionPlaceholder},
		{name: "project editor", got: uiProjectDescriptionEditor(project, "").Placeholder, want: uiProjectDescriptionPlaceholder},
		{name: "new project form", got: uiProjectDescriptionPlaceholderText(), want: uiProjectDescriptionPlaceholder},
		{name: "sprint editor", got: uiSprintDescriptionEditor(project, sprint, "", false).Placeholder, want: uiSprintDescriptionPlaceholder},
		{name: "new sprint editor", got: uiNewSprintDescriptionEditor("").Placeholder, want: uiSprintDescriptionPlaceholder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("placeholder = %q, want %q", tc.got, tc.want)
			}
		})
	}
}
