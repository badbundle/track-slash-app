package store_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestGitHubCredentialLifecycleAcrossProjects(t *testing.T) {
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	ownerID := project.OwnerID
	ctx := store.WithActor(env.ctx, ownerID)
	other, err := env.store.CreateUserProfile(env.ctx, "gh-other-"+strings.ToLower(uniqueProjectKey(t)), "gh-other-"+uniqueProjectKey(t)+"@example.com", "Other")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}

	credentialID := uuid.New()
	credential, err := env.store.CreateGitHubCredential(env.ctx, store.CreateGitHubCredentialParams{
		ID: credentialID, UserID: ownerID, Name: "Personal", GitHubLogin: "octocat",
		TokenCiphertext: bytes.Repeat([]byte{1}, 24), TokenNonce: bytes.Repeat([]byte{2}, 12),
	})
	if err != nil || credential.ID != credentialID || credential.Name != "Personal" || credential.GitHubLogin != "octocat" || credential.ConnectionCount != 0 {
		t.Fatalf("CreateGitHubCredential = %+v, %v", credential, err)
	}
	// Names are unique per user regardless of case, but free across users.
	if _, err := env.store.CreateGitHubCredential(env.ctx, store.CreateGitHubCredentialParams{
		ID: uuid.New(), UserID: ownerID, Name: "PERSONAL", GitHubLogin: "octocat",
		TokenCiphertext: bytes.Repeat([]byte{1}, 24), TokenNonce: bytes.Repeat([]byte{2}, 12),
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate name error = %v", err)
	}
	otherCredential, err := env.store.CreateGitHubCredential(env.ctx, store.CreateGitHubCredentialParams{
		ID: uuid.New(), UserID: other.ID, Name: "Personal", GitHubLogin: "hubot",
		TokenCiphertext: bytes.Repeat([]byte{7}, 24), TokenNonce: bytes.Repeat([]byte{8}, 12),
	})
	if err != nil {
		t.Fatalf("same name for another user: %v", err)
	}

	secret, err := env.store.GetGitHubCredentialSecret(env.ctx, ownerID, credentialID)
	if err != nil || secret.Credential.ID != credentialID || !bytes.Equal(secret.Ciphertext, bytes.Repeat([]byte{1}, 24)) {
		t.Fatalf("GetGitHubCredentialSecret = %+v, %v", secret, err)
	}
	if _, err := env.store.GetGitHubCredentialSecret(env.ctx, other.ID, credentialID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another user's secret error = %v", err)
	}

	// One saved credential backs connections in two projects.
	secondProject, err := env.store.CreateProjectForUser(env.ctx, ownerID, uniqueProjectKey(t), "second", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	connect := func(projectID uuid.UUID, repositoryID int64, name string) model.GitHubConnection {
		t.Helper()
		connection, err := env.store.UpsertGitHubConnection(ctx, store.UpsertGitHubConnectionParams{
			ProjectID: projectID, RepositoryID: repositoryID, RepositoryOwner: "acme", RepositoryName: name,
			RepositoryURL: "https://github.com/acme/" + name, CredentialID: &credentialID, CreatedByID: ownerID,
		})
		if err != nil || connection.CredentialID == nil || *connection.CredentialID != credentialID {
			t.Fatalf("UpsertGitHubConnection with credential = %+v, %v", connection, err)
		}
		return connection
	}
	first := connect(env.projectID, 501, "one")
	second := connect(secondProject.ID, 502, "two")
	connectionSecret, err := env.store.GetGitHubConnectionSecret(env.ctx, first.ID)
	if err != nil || connectionSecret.CredentialUserID != ownerID || !bytes.Equal(connectionSecret.Ciphertext, bytes.Repeat([]byte{1}, 24)) || !bytes.Equal(connectionSecret.Nonce, bytes.Repeat([]byte{2}, 12)) {
		t.Fatalf("credential-backed connection secret = %+v, %v", connectionSecret, err)
	}
	credentials, err := env.store.ListGitHubCredentials(env.ctx, ownerID)
	if err != nil || len(credentials) != 1 || credentials[0].ConnectionCount != 2 {
		t.Fatalf("ListGitHubCredentials = %+v, %v", credentials, err)
	}

	// Only the credential's owner can connect with it.
	if _, err := env.store.UpsertGitHubConnection(ctx, store.UpsertGitHubConnectionParams{
		ProjectID: env.projectID, RepositoryID: 503, RepositoryOwner: "acme", RepositoryName: "three",
		RepositoryURL: "https://github.com/acme/three", CredentialID: &credentialID, CreatedByID: other.ID,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("another user's credential error = %v", err)
	}
	// Every active connection needs exactly one token source.
	if _, err := env.store.UpsertGitHubConnection(ctx, store.UpsertGitHubConnectionParams{
		ProjectID: env.projectID, RepositoryID: 504, RepositoryOwner: "acme", RepositoryName: "four",
		RepositoryURL: "https://github.com/acme/four", CreatedByID: ownerID,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("connection without a token error = %v", err)
	}

	// Renaming leaves the token alone; replacing it reaches every connection.
	renamed := "Work"
	updated, err := env.store.UpdateGitHubCredential(env.ctx, store.UpdateGitHubCredentialParams{ID: credentialID, UserID: ownerID, Name: &renamed})
	if err != nil || updated.Name != "Work" || updated.GitHubLogin != "octocat" || updated.ConnectionCount != 2 || !updated.LastValidatedAt.Equal(credential.LastValidatedAt) {
		t.Fatalf("rename = %+v, %v", updated, err)
	}
	if secret, _ := env.store.GetGitHubCredentialSecret(env.ctx, ownerID, credentialID); !bytes.Equal(secret.Ciphertext, bytes.Repeat([]byte{1}, 24)) {
		t.Fatalf("rename changed the token: %x", secret.Ciphertext)
	}
	time.Sleep(10 * time.Millisecond)
	rotated, err := env.store.UpdateGitHubCredential(env.ctx, store.UpdateGitHubCredentialParams{
		ID: credentialID, UserID: ownerID, GitHubLogin: "octocat-renamed",
		TokenCiphertext: bytes.Repeat([]byte{3}, 24), TokenNonce: bytes.Repeat([]byte{4}, 12),
	})
	if err != nil || rotated.Name != "Work" || rotated.GitHubLogin != "octocat-renamed" || !rotated.LastValidatedAt.After(credential.LastValidatedAt) {
		t.Fatalf("rotate = %+v, %v", rotated, err)
	}
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		secret, err := env.store.GetGitHubConnectionSecret(env.ctx, id)
		if err != nil || !bytes.Equal(secret.Ciphertext, bytes.Repeat([]byte{3}, 24)) || !bytes.Equal(secret.Nonce, bytes.Repeat([]byte{4}, 12)) {
			t.Fatalf("connection %s after rotation = %+v, %v", id, secret, err)
		}
	}
	otherName := "Personal"
	if _, err := env.store.UpdateGitHubCredential(env.ctx, store.UpdateGitHubCredentialParams{ID: otherCredential.ID, UserID: other.ID, Name: &renamed}); err != nil {
		t.Fatalf("rename another user's credential to a name only the owner uses: %v", err)
	}
	if _, err := env.store.CreateGitHubCredential(env.ctx, store.CreateGitHubCredentialParams{
		ID: uuid.New(), UserID: ownerID, Name: otherName, GitHubLogin: "octocat",
		TokenCiphertext: bytes.Repeat([]byte{1}, 24), TokenNonce: bytes.Repeat([]byte{2}, 12),
	}); err != nil {
		t.Fatalf("reuse a freed name: %v", err)
	}
	if _, err := env.store.UpdateGitHubCredential(env.ctx, store.UpdateGitHubCredentialParams{ID: credentialID, UserID: ownerID, Name: &otherName}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("rename collision error = %v", err)
	}
	if _, err := env.store.UpdateGitHubCredential(env.ctx, store.UpdateGitHubCredentialParams{ID: credentialID, UserID: other.ID, Name: &renamed}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update another user's credential error = %v", err)
	}

	// A manual disconnect releases the credential reference.
	if err := env.store.DisconnectGitHubConnection(ctx, secondProject.ID, second.ID); err != nil {
		t.Fatalf("DisconnectGitHubConnection: %v", err)
	}
	var releasedCredential *uuid.UUID
	if err := env.pool.QueryRow(env.ctx, `SELECT credential_id FROM github_repository_connections WHERE id = $1`, second.ID).Scan(&releasedCredential); err != nil || releasedCredential != nil {
		t.Fatalf("disconnected credential reference = %v, %v", releasedCredential, err)
	}

	// Deleting the credential disconnects what still uses it and keeps its
	// issue links' history.
	issue := mustCreateIssue(t, env, "Linked through a saved token")
	branch := "main"
	now := time.Now().UTC()
	link, err := env.store.CreateGitHubIssueLink(ctx, store.CreateGitHubIssueLinkParams{
		IssueID: issue.ID, ConnectionID: first.ID, RepositoryID: 501, RepositoryOwner: "acme", RepositoryName: "one",
		ResourceType: model.GitHubResourceBranch, BranchName: &branch, Title: branch,
		HTMLURL: "https://github.com/acme/one/tree/main", State: model.GitHubLinkStateBranch,
		RefreshedAt: now, NextRefreshAt: now, CreatedByID: ownerID,
	})
	if err != nil {
		t.Fatalf("CreateGitHubIssueLink: %v", err)
	}
	if err := env.store.DeleteGitHubCredential(ctx, other.ID, credentialID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete another user's credential error = %v", err)
	}
	if err := env.store.DeleteGitHubCredential(ctx, ownerID, credentialID); err != nil {
		t.Fatalf("DeleteGitHubCredential: %v", err)
	}
	if _, err := env.store.GetGitHubConnection(env.ctx, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("connection after credential delete = %v", err)
	}
	if _, err := env.store.GetGitHubCredentialSecret(env.ctx, ownerID, credentialID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("credential after delete = %v", err)
	}
	links, err := env.store.ListGitHubIssueLinks(env.ctx, issue.ID)
	if err != nil || len(links) != 1 || links[0].ID != link.ID || links[0].LastError == "" {
		t.Fatalf("links after credential delete = %+v, %v", links, err)
	}
	var summary string
	if err := env.pool.QueryRow(env.ctx, `
		SELECT summary FROM project_changelog_entries
		WHERE project_id = $1 AND entity = 'github_connection' AND op = 'delete'
	`, env.projectID).Scan(&summary); err != nil || !strings.Contains(summary, "acme/one") || !strings.Contains(summary, "saved token was removed") || strings.Contains(summary, "Work") {
		t.Fatalf("changelog summary = %q, %v", summary, err)
	}
	if err := env.store.DeleteGitHubCredential(ctx, ownerID, credentialID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
}
