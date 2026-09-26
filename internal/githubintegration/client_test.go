package githubintegration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestClientReadsRepositoryBranchAndPullRequestStates(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-token" || r.Header.Get("X-GitHub-Api-Version") != githubAPIVersion || r.Header.Get("User-Agent") != "track-slash" {
			t.Errorf("unexpected headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/widgets":
			_, _ = w.Write([]byte(`{"id":99,"name":"widgets","html_url":"https://github.com/acme/widgets","private":true,"owner":{"login":"acme"}}`))
		case "/repos/acme/widgets/branches/feature/one":
			_, _ = w.Write([]byte(`{"name":"feature/one"}`))
		case "/repos/acme/widgets/pulls/1":
			_, _ = w.Write([]byte(`{"id":101,"number":1,"title":"Draft PR","html_url":"https://github.com/acme/widgets/pull/1","state":"open","draft":true,"merged_at":null}`))
		case "/repos/acme/widgets/pulls/2":
			_, _ = w.Write([]byte(`{"id":102,"number":2,"title":"Open PR","html_url":"https://github.com/acme/widgets/pull/2","state":"open","draft":false,"merged_at":null}`))
		case "/repos/acme/widgets/pulls/3":
			_, _ = w.Write([]byte(`{"id":103,"number":3,"title":"Closed PR","html_url":"https://github.com/acme/widgets/pull/3","state":"closed","draft":false,"merged_at":null}`))
		case "/repos/acme/widgets/pulls/4":
			_, _ = w.Write([]byte(`{"id":104,"number":4,"title":"Merged PR","html_url":"https://github.com/acme/widgets/pull/4","state":"closed","draft":false,"merged_at":"2026-07-18T12:00:00Z"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.Client(), server.URL)
	repo, err := client.GetRepository(context.Background(), "private-token", "acme", "widgets")
	if err != nil || repo.ID != 99 || !repo.Private || repo.Owner != "acme" {
		t.Fatalf("GetRepository = %+v, %v", repo, err)
	}
	branch, err := client.GetBranch(context.Background(), "private-token", "acme", "widgets", "feature/one")
	if err != nil || branch.State != model.GitHubLinkStateBranch || branch.BranchName == nil || *branch.BranchName != "feature/one" {
		t.Fatalf("GetBranch = %+v, %v", branch, err)
	}
	wantStates := []model.GitHubLinkState{model.GitHubLinkStateDraft, model.GitHubLinkStateOpen, model.GitHubLinkStateClosed, model.GitHubLinkStateMerged}
	for i, want := range wantStates {
		got, err := client.GetPullRequest(context.Background(), "private-token", "acme", "widgets", i+1)
		if err != nil || got.State != want {
			t.Errorf("PR %d = %+v, %v; want %s", i+1, got, err, want)
		}
	}
}

func TestClientMapsProviderErrorsWithoutReadingSecrets(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		status    int
		headers   map[string]string
		want      error
		wantRetry time.Time
	}{
		{"unauthorized", http.StatusUnauthorized, nil, ErrUnauthorized, time.Time{}},
		{"forbidden", http.StatusForbidden, nil, ErrUnauthorized, time.Time{}},
		{"not found", http.StatusNotFound, nil, ErrUnavailable, time.Time{}},
		{"gone", http.StatusGone, nil, ErrUnavailable, time.Time{}},
		{"retry after", http.StatusTooManyRequests, map[string]string{"Retry-After": "30"}, ErrRateLimited, now.Add(30 * time.Second)},
		{"reset", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(now.Add(time.Hour).Unix(), 10)}, ErrRateLimited, now.Add(time.Hour)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for key, value := range test.headers {
					w.Header().Set(key, value)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"message":"private-token must never escape"}`))
			}))
			defer server.Close()
			client := NewClient(server.Client(), server.URL)
			client.now = func() time.Time { return now }
			_, err := client.GetRepository(context.Background(), "private-token", "acme", "widgets")
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("error = %v", err)
			}
			if !test.wantRetry.IsZero() {
				var rate *RateLimitError
				if !errors.As(err, &rate) || !rate.RetryAt.Equal(test.wantRetry) {
					t.Fatalf("rate error = %+v", rate)
				}
			}
		})
	}
}

type errorDoer struct{}

func (errorDoer) Do(*http.Request) (*http.Response, error) { return nil, errors.New("network down") }

func TestClientRejectsNetworkMalformedAndIncompleteResponses(t *testing.T) {
	defaulted := NewClient(nil, "")
	if defaulted.httpClient == nil || defaulted.baseURL != "https://api.github.com" {
		t.Fatalf("default client = %+v", defaulted)
	}
	client := NewClient(errorDoer{}, "https://api.github.test")
	if _, err := client.GetRepository(context.Background(), "token", "acme", "widgets"); err == nil {
		t.Fatal("network error returned nil")
	}
	for _, body := range []string{"not json", `{"id":0}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		client = NewClient(server.Client(), server.URL)
		if _, err := client.GetRepository(context.Background(), "token", "acme", "widgets"); err == nil {
			t.Fatalf("body %q returned nil error", body)
		}
		server.Close()
	}
	client = NewClient(http.DefaultClient, "://bad")
	if _, err := client.GetRepository(context.Background(), "token", "acme", "widgets"); err == nil {
		t.Fatal("invalid base URL returned nil error")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/branches/"):
			_, _ = w.Write([]byte(`{"name":""}`))
		case strings.Contains(r.URL.Path, "/pulls/"):
			_, _ = w.Write([]byte(`{"id":0}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client = NewClient(server.Client(), server.URL)
	if _, err := client.GetBranch(context.Background(), "token", "acme", "widgets", "main"); err == nil {
		t.Fatal("incomplete branch returned nil error")
	}
	if _, err := client.GetPullRequest(context.Background(), "token", "acme", "widgets", 1); err == nil {
		t.Fatal("incomplete pull request returned nil error")
	}
	if _, err := client.GetRepository(context.Background(), "token", "acme", "widgets"); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("server error = %v", err)
	}
}

func TestGitHubRetryAtHeaderVariants(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	date := now.Add(2 * time.Minute)
	header := http.Header{"Retry-After": {date.Format(http.TimeFormat)}}
	if got := githubRetryAt(header, now); !got.Equal(date) {
		t.Fatalf("date retry = %v", got)
	}
	header = http.Header{"Retry-After": {"invalid"}, "X-RateLimit-Reset": {"invalid"}}
	if got := githubRetryAt(header, now); !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("default retry = %v", got)
	}
}

func TestClientReadsAuthenticatedUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer good-token":
			_, _ = w.Write([]byte(`{"login":"octocat","id":1}`))
		case "Bearer incomplete-token":
			_, _ = w.Write([]byte(`{"id":1}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	client := NewClient(server.Client(), server.URL)
	if login, err := client.GetAuthenticatedUser(context.Background(), "good-token"); err != nil || login != "octocat" {
		t.Fatalf("GetAuthenticatedUser = %q, %v", login, err)
	}
	if _, err := client.GetAuthenticatedUser(context.Background(), "incomplete-token"); err == nil {
		t.Fatal("incomplete user returned nil error")
	}
	if _, err := client.GetAuthenticatedUser(context.Background(), "expired-token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired token error = %v", err)
	}
}
