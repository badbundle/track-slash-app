package githubintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
)

const githubAPIVersion = "2026-03-10"

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	httpClient HTTPDoer
	baseURL    string
	now        func() time.Time
}

func NewClient(httpClient HTTPDoer, baseURL string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Client{httpClient: httpClient, baseURL: baseURL, now: time.Now}
}

type Repository struct {
	ID      int64
	Owner   string
	Name    string
	HTMLURL string
	Private bool
}

type Snapshot struct {
	ResourceType      model.GitHubResourceType
	BranchName        *string
	PullRequestID     *int64
	PullRequestNumber *int
	Title             string
	HTMLURL           string
	State             model.GitHubLinkState
}

// GetAuthenticatedUser returns the login a token acts as. GitHub answers this
// for any token, however narrowly scoped, so it is how a token is checked
// before it is saved.
func (c *Client) GetAuthenticatedUser(ctx context.Context, token string) (string, error) {
	var payload struct {
		Login string `json:"login"`
	}
	if err := c.get(ctx, token, "/user", &payload); err != nil {
		return "", err
	}
	if payload.Login == "" {
		return "", errors.New("GitHub returned incomplete user metadata")
	}
	return payload.Login, nil
}

func (c *Client) GetRepository(ctx context.Context, token, owner, name string) (Repository, error) {
	var payload struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		HTMLURL string `json:"html_url"`
		Private bool   `json:"private"`
		Owner   struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	err := c.get(ctx, token, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), &payload)
	if err != nil {
		return Repository{}, err
	}
	if payload.ID <= 0 || payload.Owner.Login == "" || payload.Name == "" || payload.HTMLURL == "" {
		return Repository{}, errors.New("GitHub returned incomplete repository metadata")
	}
	return Repository{ID: payload.ID, Owner: payload.Owner.Login, Name: payload.Name, HTMLURL: payload.HTMLURL, Private: payload.Private}, nil
}

func (c *Client) GetBranch(ctx context.Context, token, owner, name, branch string) (Snapshot, error) {
	var payload struct {
		Name string `json:"name"`
	}
	err := c.get(ctx, token, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/branches/"+url.PathEscape(branch), &payload)
	if err != nil {
		return Snapshot{}, err
	}
	if payload.Name == "" {
		return Snapshot{}, errors.New("GitHub returned incomplete branch metadata")
	}
	htmlURL := "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/tree/" + url.PathEscape(payload.Name)
	return Snapshot{
		ResourceType: model.GitHubResourceBranch,
		BranchName:   &payload.Name,
		Title:        payload.Name,
		HTMLURL:      htmlURL,
		State:        model.GitHubLinkStateBranch,
	}, nil
}

func (c *Client) GetPullRequest(ctx context.Context, token, owner, name string, number int) (Snapshot, error) {
	var payload struct {
		ID       int64      `json:"id"`
		Number   int        `json:"number"`
		Title    string     `json:"title"`
		HTMLURL  string     `json:"html_url"`
		State    string     `json:"state"`
		Draft    bool       `json:"draft"`
		MergedAt *time.Time `json:"merged_at"`
	}
	err := c.get(ctx, token, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/pulls/"+strconv.Itoa(number), &payload)
	if err != nil {
		return Snapshot{}, err
	}
	if payload.ID <= 0 || payload.Number <= 0 || payload.Title == "" || payload.HTMLURL == "" {
		return Snapshot{}, errors.New("GitHub returned incomplete pull request metadata")
	}
	state := model.GitHubLinkStateOpen
	switch {
	case payload.MergedAt != nil:
		state = model.GitHubLinkStateMerged
	case payload.State == "closed":
		state = model.GitHubLinkStateClosed
	case payload.Draft:
		state = model.GitHubLinkStateDraft
	}
	return Snapshot{
		ResourceType:      model.GitHubResourcePullRequest,
		PullRequestID:     &payload.ID,
		PullRequestNumber: &payload.Number,
		Title:             payload.Title,
		HTMLURL:           payload.HTMLURL,
		State:             state,
	}, nil
}

func (c *Client) get(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "track-slash")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return c.responseError(resp)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func (c *Client) responseError(resp *http.Response) error {
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden && (resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0") {
		return &RateLimitError{RetryAt: githubRetryAt(resp.Header, c.now())}
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound, http.StatusGone:
		return ErrUnavailable
	default:
		return fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
}

func githubRetryAt(header http.Header, now time.Time) time.Time {
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			return now.Add(time.Duration(seconds) * time.Second)
		}
		if retryAt, err := http.ParseTime(raw); err == nil {
			return retryAt
		}
	}
	if raw := strings.TrimSpace(header.Get("X-RateLimit-Reset")); raw != "" {
		if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return time.Unix(unix, 0)
		}
	}
	return now.Add(time.Minute)
}
