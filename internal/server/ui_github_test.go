package server

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/githubintegration"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestGitHubIssuePanelRendersStatesFreshnessAndControls(t *testing.T) {
	now := time.Now()
	prNumber := 12
	prID := int64(44)
	branch := "feature/ship"
	panel := &uiIssuePanelData{
		Issue:    model.Issue{ID: uuid.New(), OwnerUsername: "owner", ProjectKey: "TRACK", Identifier: "TRACK-29"},
		Project:  model.Project{ID: uuid.New(), OwnerUsername: "owner", Key: "TRACK"},
		CanWrite: true, GitHubConfigured: true,
		GitHubConnections: []model.GitHubConnection{{ID: uuid.New(), RepositoryOwner: "acme", RepositoryName: "private", Private: true}},
		GitHubLinks: []uiGitHubIssueLink{
			{Link: model.GitHubIssueLink{ID: uuid.New(), ResourceType: model.GitHubResourcePullRequest, RepositoryOwner: "acme", RepositoryName: "private", PullRequestID: &prID, PullRequestNumber: &prNumber, Title: "Closed PR", HTMLURL: "https://github.com/acme/private/pull/12", State: model.GitHubLinkStateClosed, LastRefreshedAt: &now, LastError: "GitHub resource is unavailable; showing the last known state"}, LastRefreshed: uiTokenTime(now), Stale: true},
			{Link: model.GitHubIssueLink{ID: uuid.New(), ResourceType: model.GitHubResourceBranch, RepositoryOwner: "acme", RepositoryName: "private", BranchName: &branch, Title: branch, HTMLURL: "https://github.com/acme/private/tree/feature/ship", State: model.GitHubLinkStateBranch, LastRefreshedAt: &now}, LastRefreshed: uiTokenTime(now)},
		},
	}
	var out bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&out, "issue-panel", panel); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := out.String()
	for _, want := range []string{"Closed, not merged", "stale", "unavailable", "feature/ship", "Refresh GitHub link", "Remove GitHub link", `data-modal-open="issue-github-link"`, `id="issue-github-link" data-client-modal class="fixed inset-0 z-50 hidden`, "Link GitHub branch or pull request"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "sm:grid-cols-[13rem_minmax(0,1fr)_auto]") {
		t.Fatal("rendered the GitHub linking form inline")
	}
}

func TestUIGitHubActionMessages(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errors.Join(errors.New("bad repository"), githubintegration.ErrInvalid), "bad repository"},
		{githubintegration.ErrUnauthorized, "does not allow access"},
		{githubintegration.ErrUnavailable, "could not find"},
		{githubintegration.ErrRateLimited, "rate limit"},
		{store.ErrConflict, "already linked"},
		{store.ErrNotFound, "no longer available"},
		{errors.New("network"), "could not be reached"},
	}
	for _, test := range tests {
		if got := uiGitHubActionMessage(test.err); !strings.Contains(got, test.want) {
			t.Errorf("uiGitHubActionMessage(%v) = %q", test.err, got)
		}
	}
}

func TestGitHubProjectPanelRendersPrivateRepositoryAndProtectedTokenField(t *testing.T) {
	panel := &uiProjectPanelData{
		Project: model.Project{ID: uuid.New(), OwnerUsername: "owner", Key: "TRACK", Name: "Track"},
		View:    "about", CanManageMembers: true, GitHubConfigured: true,
		GitHubConnections: []model.GitHubConnection{{ID: uuid.New(), RepositoryOwner: "acme", RepositoryName: "private", RepositoryURL: "https://github.com/acme/private", Private: true, LastValidatedAt: time.Now()}},
	}
	var out bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&out, "project-panel", panel); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := out.String()
	for _, want := range []string{"GitHub repositories", "acme/private", "Private", `type="password"`, `autocomplete="off"`, "Disconnect", `data-modal-open="project-github-connection"`, `id="project-github-connection" data-client-modal class="fixed inset-0 z-50 hidden`, "Connect GitHub repository"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]") {
		t.Fatal("rendered the GitHub connection form inline")
	}
	if strings.Contains(body, "private-token") {
		t.Fatal("rendered a token")
	}
}

func TestGitHubFormErrorsKeepClientModalsOpen(t *testing.T) {
	projectPanel := &uiProjectPanelData{
		Project:               model.Project{ID: uuid.New(), OwnerUsername: "owner", Key: "TRACK", Name: "Track"},
		View:                  "about",
		CanManageMembers:      true,
		GitHubConfigured:      true,
		GitHubConnectionError: "Repository is unavailable.",
	}
	var out bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&out, "project-panel", projectPanel); err != nil {
		t.Fatalf("render project: %v", err)
	}
	for _, want := range []string{`id="project-github-connection" data-client-modal class="fixed inset-0 z-50 grid`, "Repository is unavailable."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("project modal missing %q", want)
		}
	}

	issuePanel := &uiIssuePanelData{
		Issue:             model.Issue{ID: uuid.New(), OwnerUsername: "owner", ProjectKey: "TRACK", Identifier: "TRACK-31"},
		Project:           model.Project{ID: uuid.New(), OwnerUsername: "owner", Key: "TRACK"},
		CanWrite:          true,
		GitHubConfigured:  true,
		GitHubConnections: []model.GitHubConnection{{ID: uuid.New(), RepositoryOwner: "acme", RepositoryName: "private"}},
		GitHubReference:   "feature/modal",
		GitHubError:       "Choose a repository.",
	}
	out.Reset()
	if err := uiTemplates.ExecuteTemplate(&out, "issue-panel", issuePanel); err != nil {
		t.Fatalf("render issue: %v", err)
	}
	for _, want := range []string{`id="issue-github-link" data-client-modal class="fixed inset-0 z-50 grid`, "Choose a repository.", `value="feature/modal"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("issue modal missing %q", want)
		}
	}
}

func TestUIGitHubTokenMessagesAndLabels(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{githubintegration.ErrUnauthorized, "did not accept that token"},
		{store.ErrConflict, "already have a saved token with that name"},
		{store.ErrNotFound, "no longer exists"},
		{githubintegration.ErrRateLimited, "rate limit"},
	} {
		if got := uiGitHubTokenMessage(test.err); !strings.Contains(got, test.want) {
			t.Errorf("uiGitHubTokenMessage(%v) = %q", test.err, got)
		}
	}
	if got := uiGitHubConnectMessage(store.ErrNotFound); !strings.Contains(got, "saved token no longer exists") {
		t.Errorf("connect not found = %q", got)
	}
	if got := uiGitHubConnectMessage(store.ErrConflict); !strings.Contains(got, "already linked") {
		t.Errorf("connect conflict = %q", got)
	}

	mine, theirs := uuid.New(), uuid.New()
	legacy := model.GitHubConnection{ID: uuid.New()}
	own := model.GitHubConnection{ID: uuid.New(), CredentialID: &mine}
	other := model.GitHubConnection{ID: uuid.New(), CredentialID: &theirs}
	labels := uiGitHubTokenLabels([]model.GitHubConnection{legacy, own, other}, []model.GitHubCredential{{ID: mine, Name: "Personal"}})
	if labels[legacy.ID] != "project token" || labels[own.ID] != "your token “Personal”" || labels[other.ID] != "saved token" {
		t.Fatalf("labels = %v", labels)
	}
}

func TestGitHubProjectPanelOffersSavedTokens(t *testing.T) {
	credential := model.GitHubCredential{ID: uuid.New(), Name: "Personal", GitHubLogin: "octocat"}
	connection := model.GitHubConnection{ID: uuid.New(), RepositoryOwner: "acme", RepositoryName: "private", RepositoryURL: "https://github.com/acme/private", CredentialID: &credential.ID, LastValidatedAt: time.Now()}
	panel := &uiProjectPanelData{
		Project: model.Project{ID: uuid.New(), OwnerUsername: "owner", Key: "TRACK", Name: "Track"},
		View:    "about", CanManageMembers: true, GitHubConfigured: true,
		GitHubConnections:     []model.GitHubConnection{connection},
		GitHubCredentials:     []model.GitHubCredential{{ID: uuid.New(), Name: "Other", GitHubLogin: "hubot"}, credential},
		GitHubTokenLabels:     map[uuid.UUID]string{connection.ID: "your token “Personal”"},
		GitHubCredentialInput: credential.ID.String(),
		GitHubNewTokenOpen:    true,
	}
	var out bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&out, "project-panel", panel); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := out.String()
	for _, want := range []string{`name="credential_id"`, `<option value="` + credential.ID.String() + `" selected>Personal (@octocat)</option>`, "Other (@hubot)", `<details open class=`, "Use a new token instead", `name="token_name"`, `href="/tokens#github-tokens"`, "your token “Personal”"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	panel.GitHubCredentials, panel.GitHubNewTokenOpen = nil, false
	out.Reset()
	if err := uiTemplates.ExecuteTemplate(&out, "project-panel", panel); err != nil {
		t.Fatalf("render without saved tokens: %v", err)
	}
	body = out.String()
	if strings.Contains(body, `name="credential_id"`) || strings.Contains(body, "Use a new token instead") || !strings.Contains(body, `name="token_name"`) || !strings.Contains(body, "Saved to your account") {
		t.Fatal("without saved tokens the modal should ask for a new one directly")
	}
}

func TestTokenPanelGitHubTokenForms(t *testing.T) {
	credential := model.GitHubCredential{ID: uuid.New(), Name: "Personal", GitHubLogin: "octocat", ConnectionCount: 2, LastValidatedAt: time.Now()}
	panel := &uiTokenPanelData{
		GitHubConfigured:     true,
		GitHubTokens:         []model.GitHubCredential{credential, {ID: uuid.New(), Name: "Single", GitHubLogin: "hubot", ConnectionCount: 1}},
		GitHubTokenFormFor:   credential.ID.String(),
		GitHubTokenError:     "You already have a saved token with that name.",
		GitHubTokenNameInput: "Typed",
	}
	var out bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&out, "token-panel", panel); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := out.String()
	for _, want := range []string{
		"Used by 2 repositories", "Used by 1 repository", "@octocat",
		`id="github-token-edit-` + credential.ID.String() + `" data-client-modal class="fixed inset-0 z-50 grid`,
		`id="github-token-create" data-client-modal class="fixed inset-0 z-50 hidden`,
		`value="Typed"`, `value="Single"`, "You already have a saved token with that name.",
		"Removing it disconnects the 2 repositories that use it.", "Removing it disconnects the 1 repository that uses it.",
		"Repositories that use it will be disconnected.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Count(body, "You already have a saved token with that name.") != 1 {
		t.Error("the error rendered outside the form it belongs to")
	}
}
