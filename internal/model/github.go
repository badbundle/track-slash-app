package model

import (
	"time"

	"github.com/google/uuid"
)

type GitHubResourceType string

const (
	GitHubResourceBranch      GitHubResourceType = "branch"
	GitHubResourcePullRequest GitHubResourceType = "pull_request"
)

func (t GitHubResourceType) Valid() bool {
	return t == GitHubResourceBranch || t == GitHubResourcePullRequest
}

type GitHubLinkState string

const (
	GitHubLinkStateBranch  GitHubLinkState = "branch"
	GitHubLinkStateDraft   GitHubLinkState = "draft"
	GitHubLinkStateOpen    GitHubLinkState = "open"
	GitHubLinkStateMerged  GitHubLinkState = "merged"
	GitHubLinkStateClosed  GitHubLinkState = "closed"
	GitHubLinkStateUnknown GitHubLinkState = "unknown"
)

func (s GitHubLinkState) Valid() bool {
	switch s {
	case GitHubLinkStateBranch, GitHubLinkStateDraft, GitHubLinkStateOpen, GitHubLinkStateMerged, GitHubLinkStateClosed, GitHubLinkStateUnknown:
		return true
	default:
		return false
	}
}

// GitHubConnection contains display-safe repository metadata. Credentials are
// intentionally excluded and can only be loaded through the store secret API.
type GitHubConnection struct {
	ID              uuid.UUID  `json:"id"`
	ProjectID       uuid.UUID  `json:"project_id"`
	RepositoryID    int64      `json:"repository_id"`
	RepositoryOwner string     `json:"repository_owner"`
	RepositoryName  string     `json:"repository_name"`
	RepositoryURL   string     `json:"repository_url"`
	Private         bool       `json:"private"`
	CredentialID    *uuid.UUID `json:"credential_id,omitempty"`
	CreatedByID     uuid.UUID  `json:"created_by_id"`
	LastValidatedAt time.Time  `json:"last_validated_at"`
	LastError       string     `json:"last_error,omitempty"`
	DisabledAt      *time.Time `json:"disabled_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (c GitHubConnection) FullName() string {
	return c.RepositoryOwner + "/" + c.RepositoryName
}

// GitHubCredential is a GitHub token saved on a user's account so it can back
// repository connections in any project that user manages. Like
// GitHubConnection it carries no secret material.
type GitHubCredential struct {
	ID              uuid.UUID `json:"id"`
	UserID          uuid.UUID `json:"user_id"`
	Name            string    `json:"name"`
	GitHubLogin     string    `json:"github_login"`
	ConnectionCount int       `json:"connection_count"`
	LastValidatedAt time.Time `json:"last_validated_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type GitHubIssueLink struct {
	ID                uuid.UUID          `json:"id"`
	ProjectID         uuid.UUID          `json:"project_id"`
	IssueID           uuid.UUID          `json:"issue_id"`
	ConnectionID      uuid.UUID          `json:"connection_id"`
	RepositoryID      int64              `json:"repository_id"`
	RepositoryOwner   string             `json:"repository_owner"`
	RepositoryName    string             `json:"repository_name"`
	ResourceType      GitHubResourceType `json:"resource_type"`
	BranchName        *string            `json:"branch_name,omitempty"`
	PullRequestID     *int64             `json:"pull_request_id,omitempty"`
	PullRequestNumber *int               `json:"pull_request_number,omitempty"`
	Title             string             `json:"title"`
	HTMLURL           string             `json:"html_url"`
	State             GitHubLinkState    `json:"state"`
	LastRefreshedAt   *time.Time         `json:"last_refreshed_at,omitempty"`
	LastError         string             `json:"last_error,omitempty"`
	NextRefreshAt     time.Time          `json:"-"`
	CreatedByID       uuid.UUID          `json:"created_by_id"`
	DeletedAt         *time.Time         `json:"deleted_at,omitempty"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

func (l GitHubIssueLink) RepositoryFullName() string {
	return l.RepositoryOwner + "/" + l.RepositoryName
}

func (l GitHubIssueLink) Stale(now time.Time, after time.Duration) bool {
	return l.LastRefreshedAt == nil || now.Sub(*l.LastRefreshedAt) > after
}
