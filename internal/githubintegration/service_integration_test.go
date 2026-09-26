package githubintegration

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

type stubProvider struct {
	login      string
	loginErr   error
	repository Repository
	snapshot   Snapshot
	err        error
	tokens     []string
	branches   []string
	pulls      []int
}

func (p *stubProvider) GetAuthenticatedUser(_ context.Context, token string) (string, error) {
	p.tokens = append(p.tokens, token)
	return p.login, p.loginErr
}

func (p *stubProvider) GetRepository(_ context.Context, token, _, _ string) (Repository, error) {
	p.tokens = append(p.tokens, token)
	return p.repository, p.err
}

func (p *stubProvider) GetBranch(_ context.Context, token, _, _, branch string) (Snapshot, error) {
	p.tokens = append(p.tokens, token)
	p.branches = append(p.branches, branch)
	return p.snapshot, p.err
}

func (p *stubProvider) GetPullRequest(_ context.Context, token, _, _ string, number int) (Snapshot, error) {
	p.tokens = append(p.tokens, token)
	p.pulls = append(p.pulls, number)
	return p.snapshot, p.err
}

func TestServiceEncryptsPrivateTokenCreatesRefreshesAndRetries(t *testing.T) {
	db := testutil.NewMigratedDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st := store.New(db.Pool)
	owner, err := st.CreateOrUpdateAdminUser(ctx, "github-service@example.com", "GitHub Service")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	project, err := st.CreateProjectForUser(ctx, owner.ID, "GHINT", "GitHub", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	issue, err := st.CreateIssue(ctx, store.CreateIssueParams{ProjectID: project.ID, Title: "Link me"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	cryptor, _ := NewCryptor(bytes.Repeat([]byte{9}, 32))
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	prID, prNumber := int64(88), 12
	provider := &stubProvider{
		repository: Repository{ID: 44, Owner: "acme", Name: "private", HTMLURL: "https://github.com/acme/private", Private: true},
		snapshot:   Snapshot{ResourceType: model.GitHubResourcePullRequest, PullRequestID: &prID, PullRequestNumber: &prNumber, Title: "Draft", HTMLURL: "https://github.com/acme/private/pull/12", State: model.GitHubLinkStateDraft},
	}
	service := NewService(st, provider, cryptor, ServiceOptions{Now: func() time.Time { return now }, RefreshInterval: time.Hour, ErrorRetry: 10 * time.Minute})
	if _, err := service.ConnectRepository(ctx, ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/private", CreatedByID: owner.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing token error = %v", err)
	}
	connection, err := service.ConnectRepository(store.WithActor(ctx, owner.ID), ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/private", Token: "private-token", CreatedByID: owner.ID})
	if err != nil || !connection.Private {
		t.Fatalf("ConnectRepository = %+v, %v", connection, err)
	}
	var ciphertext []byte
	if err := db.Pool.QueryRow(ctx, `SELECT token_ciphertext FROM github_repository_connections WHERE id = $1`, connection.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("private-token")) {
		t.Fatal("token stored in plaintext")
	}
	otherProject, err := st.CreateProjectForUser(ctx, owner.ID, "GHOTHER", "Other", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser other: %v", err)
	}
	otherIssue, err := st.CreateIssue(ctx, store.CreateIssueParams{ProjectID: otherProject.ID, Title: "Other"})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	providerCalls := len(provider.tokens)
	if _, err := service.CreateLink(ctx, CreateLinkParams{IssueID: otherIssue.ID, ConnectionID: connection.ID, Reference: "#12", CreatedByID: owner.ID}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cross-project CreateLink error = %v", err)
	}
	if len(provider.tokens) != providerCalls {
		t.Fatal("cross-project CreateLink called GitHub")
	}
	link, err := service.CreateLink(store.WithActor(ctx, owner.ID), CreateLinkParams{IssueID: issue.ID, ConnectionID: connection.ID, Reference: "#12", CreatedByID: owner.ID})
	if err != nil || link.State != model.GitHubLinkStateDraft || len(provider.pulls) != 1 || provider.tokens[len(provider.tokens)-1] != "private-token" {
		t.Fatalf("CreateLink = %+v, %v, provider=%+v", link, err, provider)
	}

	provider.snapshot.Title = "Merged"
	provider.snapshot.State = model.GitHubLinkStateMerged
	refreshed, err := service.RefreshLink(ctx, link.ID)
	if err != nil || refreshed.State != model.GitHubLinkStateMerged || refreshed.LastError != "" {
		t.Fatalf("RefreshLink = %+v, %v", refreshed, err)
	}
	provider.err = errors.New("network down")
	failed, err := service.RefreshLink(ctx, link.ID)
	if err == nil || failed.LastError != "GitHub refresh failed; showing the last known state" {
		t.Fatalf("generic failed refresh = %+v, %v", failed, err)
	}
	provider.err = &RateLimitError{RetryAt: now.Add(20 * time.Minute)}
	failed, err = service.RefreshLink(ctx, link.ID)
	if !errors.Is(err, ErrRateLimited) || failed.LastError == "" || !failed.NextRefreshAt.Equal(now.Add(20*time.Minute)) {
		t.Fatalf("rate-limited refresh = %+v, %v", failed, err)
	}
	provider.err = nil
	wrongID := int64(999)
	provider.snapshot.PullRequestID = &wrongID
	failed, err = service.RefreshLink(ctx, link.ID)
	if !errors.Is(err, ErrUnavailable) || failed.LastError == "" {
		t.Fatalf("identity-changing refresh = %+v, %v", failed, err)
	}

	provider.snapshot.PullRequestID = &prID
	provider.snapshot.Title = "Worker refreshed"
	if _, err := db.Pool.Exec(ctx, `UPDATE issue_github_links SET next_refresh_at = now() - interval '1 minute', refresh_locked_at = NULL WHERE id = $1`, link.ID); err != nil {
		t.Fatalf("make refresh due: %v", err)
	}
	worker := NewWorker(st, service, WorkerOptions{BatchSize: 1, Lease: time.Minute})
	worker.process(ctx)
	workerUpdated, err := st.GetGitHubIssueLink(ctx, link.ID)
	if err != nil || workerUpdated.Title != "Worker refreshed" || workerUpdated.LastError != "" {
		t.Fatalf("worker refresh = %+v, %v", workerUpdated, err)
	}
}

func TestServiceMarksTamperedAndDisconnectedCredentialsUnavailable(t *testing.T) {
	db := testutil.NewMigratedDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st := store.New(db.Pool)
	owner, _ := st.CreateOrUpdateAdminUser(ctx, "github-tamper@example.com", "GitHub Tamper")
	project, _ := st.CreateProjectForUser(ctx, owner.ID, "GHTAMP", "GitHub", "")
	issue, _ := st.CreateIssue(ctx, store.CreateIssueParams{ProjectID: project.ID, Title: "Link"})
	cryptor, _ := NewCryptor(bytes.Repeat([]byte{5}, 32))
	branch := "main"
	provider := &stubProvider{
		repository: Repository{ID: 51, Owner: "acme", Name: "repo", HTMLURL: "https://github.com/acme/repo"},
		snapshot:   Snapshot{ResourceType: model.GitHubResourceBranch, BranchName: &branch, Title: branch, HTMLURL: "https://github.com/acme/repo/tree/main", State: model.GitHubLinkStateBranch},
	}
	service := NewService(st, provider, cryptor, ServiceOptions{})
	connection, _ := service.ConnectRepository(ctx, ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/repo", Token: "token", CreatedByID: owner.ID})
	providerCalls := len(provider.tokens)
	if _, err := service.CreateLink(ctx, CreateLinkParams{IssueID: issue.ID, ConnectionID: connection.ID, Reference: "bad branch", CreatedByID: owner.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid reference error = %v", err)
	}
	if len(provider.tokens) != providerCalls {
		t.Fatal("invalid reference called GitHub")
	}
	link, _ := service.CreateLink(ctx, CreateLinkParams{IssueID: issue.ID, ConnectionID: connection.ID, Reference: "main", CreatedByID: owner.ID})
	if _, err := db.Pool.Exec(ctx, `UPDATE github_repository_connections SET token_ciphertext = decode(repeat('ff', 17), 'hex') WHERE id = $1`, connection.ID); err != nil {
		t.Fatalf("tamper token: %v", err)
	}
	failed, err := service.RefreshLink(ctx, link.ID)
	if !errors.Is(err, ErrUnauthorized) || failed.LastError == "" {
		t.Fatalf("tampered refresh = %+v, %v", failed, err)
	}
	if err := st.DisconnectGitHubConnection(ctx, project.ID, connection.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	failed, err = service.RefreshLink(ctx, link.ID)
	if !errors.Is(err, ErrUnavailable) || failed.LastError == "" {
		t.Fatalf("disconnected refresh = %+v, %v", failed, err)
	}
	if _, err := service.fetch(ctx, "token", connection, Reference{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid fetch error = %v", err)
	}
	if _, err := service.RefreshLink(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing link refresh error = %v", err)
	}
	cancelled, cancelWorker := context.WithCancel(ctx)
	cancelWorker()
	done := make(chan struct{})
	go func() {
		NewWorker(st, service, WorkerOptions{}).Run(cancelled)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop on cancellation")
	}
}

func TestServiceSavedCredentialsBackConnectionsAndRotate(t *testing.T) {
	db := testutil.NewMigratedDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st := store.New(db.Pool)
	owner, err := st.CreateOrUpdateAdminUser(ctx, "github-saved@example.com", "GitHub Saved")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	other, err := st.CreateUserProfile(ctx, "github-saved-other", "github-saved-other@example.com", "Other")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	ctx = store.WithActor(ctx, owner.ID)
	project, err := st.CreateProjectForUser(ctx, owner.ID, "GHSAVED", "GitHub", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	issue, err := st.CreateIssue(ctx, store.CreateIssueParams{ProjectID: project.ID, Title: "Link me"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	cryptor, _ := NewCryptor(bytes.Repeat([]byte{4}, 32))
	branch := "main"
	provider := &stubProvider{
		login:      "octocat",
		repository: Repository{ID: 61, Owner: "acme", Name: "repo", HTMLURL: "https://github.com/acme/repo", Private: true},
		snapshot:   Snapshot{ResourceType: model.GitHubResourceBranch, BranchName: &branch, Title: branch, HTMLURL: "https://github.com/acme/repo/tree/main", State: model.GitHubLinkStateBranch},
	}
	service := NewService(st, provider, cryptor, ServiceOptions{})

	for name, params := range map[string]CreateCredentialParams{
		"blank name":    {UserID: owner.ID, Name: "   ", Token: "saved-token"},
		"long name":     {UserID: owner.ID, Name: strings.Repeat("é", 101), Token: "saved-token"},
		"missing token": {UserID: owner.ID, Name: "Personal"},
	} {
		if _, err := service.CreateCredential(ctx, params); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	if len(provider.tokens) != 0 {
		t.Fatal("invalid credential called GitHub")
	}
	provider.loginErr = ErrUnauthorized
	if _, err := service.CreateCredential(ctx, CreateCredentialParams{UserID: owner.ID, Name: "Personal", Token: "expired-token"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("rejected token error = %v", err)
	}
	provider.loginErr = nil
	credential, err := service.CreateCredential(ctx, CreateCredentialParams{UserID: owner.ID, Name: "  Personal  ", Token: "saved-token"})
	if err != nil || credential.Name != "Personal" || credential.GitHubLogin != "octocat" {
		t.Fatalf("CreateCredential = %+v, %v", credential, err)
	}
	var ciphertext []byte
	if err := db.Pool.QueryRow(ctx, `SELECT token_ciphertext FROM github_credentials WHERE id = $1`, credential.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("saved-token")) {
		t.Fatal("saved token stored in plaintext")
	}

	if _, err := service.ConnectRepository(ctx, ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/repo", CredentialID: credential.ID, Token: "pasted", CreatedByID: owner.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("both token sources error = %v", err)
	}
	if _, err := service.ConnectRepository(ctx, ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/repo", CredentialID: credential.ID, CreatedByID: other.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another user's credential error = %v", err)
	}
	provider.err = ErrUnavailable
	if _, err := service.ConnectRepository(ctx, ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/repo", CredentialID: credential.ID, CreatedByID: owner.ID}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unreachable repository error = %v", err)
	}
	provider.err = nil
	connection, err := service.ConnectRepository(ctx, ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/repo", CredentialID: credential.ID, CreatedByID: owner.ID})
	if err != nil || connection.CredentialID == nil || *connection.CredentialID != credential.ID || provider.tokens[len(provider.tokens)-1] != "saved-token" {
		t.Fatalf("ConnectRepository with credential = %+v, %v, tokens=%v", connection, err, provider.tokens)
	}
	link, err := service.CreateLink(ctx, CreateLinkParams{IssueID: issue.ID, ConnectionID: connection.ID, Reference: "main", CreatedByID: owner.ID})
	if err != nil || provider.tokens[len(provider.tokens)-1] != "saved-token" {
		t.Fatalf("CreateLink through credential = %+v, %v", link, err)
	}

	if _, err := service.UpdateCredential(ctx, UpdateCredentialParams{ID: credential.ID, UserID: owner.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty update error = %v", err)
	}
	blank := ""
	if _, err := service.UpdateCredential(ctx, UpdateCredentialParams{ID: credential.ID, UserID: owner.ID, Name: &blank}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank rename error = %v", err)
	}
	calls := len(provider.tokens)
	renamed := "Work"
	updated, err := service.UpdateCredential(ctx, UpdateCredentialParams{ID: credential.ID, UserID: owner.ID, Name: &renamed})
	if err != nil || updated.Name != "Work" || len(provider.tokens) != calls {
		t.Fatalf("rename = %+v, %v (GitHub calls %d -> %d)", updated, err, calls, len(provider.tokens))
	}
	provider.loginErr = ErrUnauthorized
	if _, err := service.UpdateCredential(ctx, UpdateCredentialParams{ID: credential.ID, UserID: owner.ID, Token: "bad-token"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("rejected rotation error = %v", err)
	}
	provider.loginErr = nil
	provider.login = "octocat-2"
	rotated, err := service.UpdateCredential(ctx, UpdateCredentialParams{ID: credential.ID, UserID: owner.ID, Token: "rotated-token"})
	if err != nil || rotated.GitHubLogin != "octocat-2" || rotated.Name != "Work" {
		t.Fatalf("rotate = %+v, %v", rotated, err)
	}
	// The connection picks up the rotated token without being reconnected.
	if _, err := service.RefreshLink(ctx, link.ID); err != nil || provider.tokens[len(provider.tokens)-1] != "rotated-token" {
		t.Fatalf("refresh after rotation = %v, tokens=%v", err, provider.tokens)
	}
	// Ciphertext moved onto another user's credential does not decrypt there.
	otherCredential, err := service.CreateCredential(ctx, CreateCredentialParams{UserID: other.ID, Name: "Theirs", Token: "their-token"})
	if err != nil {
		t.Fatalf("other CreateCredential: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE github_credentials AS g SET token_ciphertext = src.token_ciphertext, token_nonce = src.token_nonce
		FROM github_credentials src WHERE g.id = $1 AND src.id = $2
	`, otherCredential.ID, credential.ID); err != nil {
		t.Fatalf("copy ciphertext: %v", err)
	}
	if _, err := service.ConnectRepository(ctx, ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/repo", CredentialID: otherCredential.ID, CreatedByID: other.ID}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("transplanted ciphertext error = %v", err)
	}

	cryptor.rand = failingReader{}
	if _, err := service.CreateCredential(ctx, CreateCredentialParams{UserID: owner.ID, Name: "Entropy", Token: "token"}); err == nil {
		t.Fatal("CreateCredential ignored an encryption failure")
	}
	if _, err := service.UpdateCredential(ctx, UpdateCredentialParams{ID: credential.ID, UserID: owner.ID, Token: "token"}); err == nil {
		t.Fatal("UpdateCredential ignored an encryption failure")
	}
	if _, err := service.ConnectRepository(ctx, ConnectRepositoryParams{ProjectID: project.ID, Repository: "acme/repo", Token: "pasted", CreatedByID: owner.ID}); err == nil {
		t.Fatal("ConnectRepository ignored an encryption failure")
	}
}
