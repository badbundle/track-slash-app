package store_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestGitHubConnectionAndIssueLinkLifecycle(t *testing.T) {
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	ctx := store.WithActor(env.ctx, project.OwnerID)
	connection, err := env.store.UpsertGitHubConnection(ctx, store.UpsertGitHubConnectionParams{
		ProjectID: env.projectID, RepositoryID: 9001, RepositoryOwner: "acme", RepositoryName: "private-repo",
		RepositoryURL: "https://github.com/acme/private-repo", Private: true,
		TokenCiphertext: bytes.Repeat([]byte{1}, 24), TokenNonce: bytes.Repeat([]byte{2}, 12), CreatedByID: project.OwnerID,
	})
	if err != nil {
		t.Fatalf("UpsertGitHubConnection: %v", err)
	}
	if !connection.Private || connection.FullName() != "acme/private-repo" || connection.RepositoryID != 9001 {
		t.Fatalf("connection = %+v", connection)
	}
	connections, err := env.store.ListGitHubConnections(env.ctx, env.projectID)
	if err != nil || len(connections) != 1 || connections[0].ID != connection.ID {
		t.Fatalf("ListGitHubConnections = %+v, %v", connections, err)
	}
	secret, err := env.store.GetGitHubConnectionSecret(env.ctx, connection.ID)
	if err != nil || !bytes.Equal(secret.Ciphertext, bytes.Repeat([]byte{1}, 24)) || !bytes.Equal(secret.Nonce, bytes.Repeat([]byte{2}, 12)) {
		t.Fatalf("GetGitHubConnectionSecret = %+v, %v", secret, err)
	}

	// Reconnecting the immutable repository ID rotates the credential and updates mutable text.
	reconnected, err := env.store.UpsertGitHubConnection(ctx, store.UpsertGitHubConnectionParams{
		ProjectID: env.projectID, RepositoryID: 9001, RepositoryOwner: "Acme", RepositoryName: "renamed",
		RepositoryURL: "https://github.com/Acme/renamed", TokenCiphertext: bytes.Repeat([]byte{3}, 24),
		TokenNonce: bytes.Repeat([]byte{4}, 12), CreatedByID: project.OwnerID,
	})
	if err != nil || reconnected.ID != connection.ID || reconnected.FullName() != "Acme/renamed" {
		t.Fatalf("reconnected = %+v, %v", reconnected, err)
	}
	replacementParams := store.UpsertGitHubConnectionParams{
		ProjectID: env.projectID, RepositoryID: 9002, RepositoryOwner: "Acme", RepositoryName: "renamed",
		RepositoryURL: "https://github.com/Acme/renamed", TokenCiphertext: bytes.Repeat([]byte{5}, 24),
		TokenNonce: bytes.Repeat([]byte{6}, 12), CreatedByID: project.OwnerID,
	}
	if _, err := env.store.UpsertGitHubConnection(ctx, replacementParams); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("active name collision error = %v", err)
	}

	issue := mustCreateIssue(t, env, "GitHub linked")
	branch := "feature/ship"
	now := time.Now().UTC()
	branchLink, err := env.store.CreateGitHubIssueLink(ctx, store.CreateGitHubIssueLinkParams{
		IssueID: issue.ID, ConnectionID: connection.ID, RepositoryID: 9001, RepositoryOwner: "Acme", RepositoryName: "renamed",
		ResourceType: model.GitHubResourceBranch, BranchName: &branch, Title: branch,
		HTMLURL: "https://github.com/Acme/renamed/tree/feature/ship", State: model.GitHubLinkStateBranch,
		RefreshedAt: now, NextRefreshAt: now.Add(-time.Minute), CreatedByID: project.OwnerID,
	})
	if err != nil {
		t.Fatalf("CreateGitHubIssueLink branch: %v", err)
	}
	if _, err := env.store.CreateGitHubIssueLink(ctx, store.CreateGitHubIssueLinkParams{
		IssueID: issue.ID, ConnectionID: connection.ID, RepositoryID: 9001, RepositoryOwner: "Acme", RepositoryName: "renamed",
		ResourceType: model.GitHubResourceBranch, BranchName: &branch, Title: branch,
		HTMLURL: branchLink.HTMLURL, State: model.GitHubLinkStateBranch, RefreshedAt: now, NextRefreshAt: now, CreatedByID: project.OwnerID,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate branch error = %v", err)
	}
	prID, prNumber := int64(777), 14
	prLink, err := env.store.CreateGitHubIssueLink(ctx, store.CreateGitHubIssueLinkParams{
		IssueID: issue.ID, ConnectionID: connection.ID, RepositoryID: 9001, RepositoryOwner: "Acme", RepositoryName: "renamed",
		ResourceType: model.GitHubResourcePullRequest, PullRequestID: &prID, PullRequestNumber: &prNumber,
		Title: "Ship it", HTMLURL: "https://github.com/Acme/renamed/pull/14", State: model.GitHubLinkStateDraft,
		RefreshedAt: now, NextRefreshAt: now.Add(time.Hour), CreatedByID: project.OwnerID,
	})
	if err != nil {
		t.Fatalf("CreateGitHubIssueLink PR: %v", err)
	}
	links, err := env.store.ListGitHubIssueLinks(env.ctx, issue.ID)
	if err != nil || len(links) != 2 || links[0].ID != branchLink.ID || links[1].ID != prLink.ID {
		t.Fatalf("ListGitHubIssueLinks = %+v, %v", links, err)
	}
	got, err := env.store.GetGitHubIssueLink(env.ctx, prLink.ID)
	if err != nil || got.PullRequestID == nil || *got.PullRequestID != prID {
		t.Fatalf("GetGitHubIssueLink = %+v, %v", got, err)
	}

	claimed, err := env.store.ClaimGitHubIssueLinks(env.ctx, 10, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != branchLink.ID {
		t.Fatalf("ClaimGitHubIssueLinks = %+v, %v", claimed, err)
	}
	updated, err := env.store.CompleteGitHubIssueLinkRefresh(env.ctx, branchLink.ID, store.UpdateGitHubIssueLinkSnapshotParams{
		Title: "feature/ship", HTMLURL: branchLink.HTMLURL, State: model.GitHubLinkStateBranch, NextRefreshAt: now.Add(time.Hour),
	})
	if err != nil || updated.LastRefreshedAt == nil || updated.LastError != "" {
		t.Fatalf("CompleteGitHubIssueLinkRefresh = %+v, %v", updated, err)
	}
	failed, err := env.store.FailGitHubIssueLinkRefresh(env.ctx, prLink.ID, stringsOfLength(600), now.Add(time.Minute))
	if err != nil || len(failed.LastError) != 500 {
		t.Fatalf("FailGitHubIssueLinkRefresh = error length %d, %v", len(failed.LastError), err)
	}

	if err := env.store.DisconnectGitHubConnection(ctx, env.projectID, connection.ID); err != nil {
		t.Fatalf("DisconnectGitHubConnection: %v", err)
	}
	if _, err := env.store.GetGitHubConnection(env.ctx, connection.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetGitHubConnection after disconnect = %v", err)
	}
	var ciphertext, nonce []byte
	if err := env.pool.QueryRow(env.ctx, `SELECT token_ciphertext, token_nonce FROM github_repository_connections WHERE id = $1`, connection.ID).Scan(&ciphertext, &nonce); err != nil {
		t.Fatalf("read scrubbed token: %v", err)
	}
	if ciphertext != nil || nonce != nil {
		t.Fatalf("token not scrubbed: %x %x", ciphertext, nonce)
	}
	replacement, err := env.store.UpsertGitHubConnection(ctx, replacementParams)
	if err != nil || replacement.RepositoryID != 9002 {
		t.Fatalf("reuse disconnected repository name = %+v, %v", replacement, err)
	}
	links, err = env.store.ListGitHubIssueLinks(env.ctx, issue.ID)
	if err != nil || len(links) != 2 || links[0].LastError == "" {
		t.Fatalf("historical links after disconnect = %+v, %v", links, err)
	}
	claimed, err = env.store.ClaimGitHubIssueLinks(env.ctx, 10, time.Minute)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("claim disconnected links = %+v, %v", claimed, err)
	}
	if err := env.store.DeleteGitHubIssueLink(ctx, issue.ID, branchLink.ID); err != nil {
		t.Fatalf("DeleteGitHubIssueLink: %v", err)
	}
	if _, err := env.store.GetGitHubIssueLink(env.ctx, branchLink.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetGitHubIssueLink after delete = %v", err)
	}
}

func TestGitHubStoreRejectsCrossProjectAndMissingResources(t *testing.T) {
	env := newSprintsEnv(t)
	project, _ := env.store.GetProject(env.ctx, env.projectID)
	ctx := store.WithActor(env.ctx, project.OwnerID)
	if _, err := env.store.ListGitHubConnections(env.ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListGitHubConnections missing project = %v", err)
	}
	connection, err := env.store.UpsertGitHubConnection(ctx, store.UpsertGitHubConnectionParams{
		ProjectID: env.projectID, RepositoryID: 1, RepositoryOwner: "acme", RepositoryName: "repo", RepositoryURL: "https://github.com/acme/repo",
		TokenCiphertext: bytes.Repeat([]byte{1}, 17), TokenNonce: bytes.Repeat([]byte{2}, 12), CreatedByID: project.OwnerID,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	other, err := env.store.CreateProjectForUser(env.ctx, project.OwnerID, uniqueProjectKey(t), "other", "")
	if err != nil {
		t.Fatalf("other project: %v", err)
	}
	otherIssue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: other.ID, Title: "other"})
	if err != nil {
		t.Fatalf("other issue: %v", err)
	}
	branch := "main"
	_, err = env.store.CreateGitHubIssueLink(ctx, store.CreateGitHubIssueLinkParams{
		IssueID: otherIssue.ID, ConnectionID: connection.ID, RepositoryID: 1, RepositoryOwner: "acme", RepositoryName: "repo",
		ResourceType: model.GitHubResourceBranch, BranchName: &branch, Title: branch, HTMLURL: "https://github.com/acme/repo/tree/main",
		State: model.GitHubLinkStateBranch, RefreshedAt: time.Now(), NextRefreshAt: time.Now(), CreatedByID: project.OwnerID,
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cross-project create error = %v", err)
	}
	if err := env.store.DisconnectGitHubConnection(ctx, env.projectID, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disconnect missing error = %v", err)
	}
	if err := env.store.DeleteGitHubIssueLink(ctx, otherIssue.ID, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete missing error = %v", err)
	}
	if got, err := env.store.ClaimGitHubIssueLinks(env.ctx, 0, time.Minute); err != nil || got != nil {
		t.Fatalf("claim zero = %+v, %v", got, err)
	}
}

func stringsOfLength(length int) string {
	return string(bytes.Repeat([]byte{'x'}, length))
}
