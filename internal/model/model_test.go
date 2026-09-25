package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStatusValid(t *testing.T) {
	cases := []struct {
		in   Status
		want bool
	}{
		{StatusTodo, true},
		{StatusInProgress, true},
		{StatusDone, true},
		{StatusClosed, true},
		{"", false},
		{"open", false},
		{"DONE", false},
		{"in progress", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := c.in.Valid(); got != c.want {
				t.Fatalf("Status(%q).Valid() = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestProjectMemberRoleValid(t *testing.T) {
	t.Parallel()
	for _, role := range []ProjectMemberRole{ProjectMemberRoleMember, ProjectMemberRoleReadonly} {
		if !role.Valid() {
			t.Fatalf("role %q should be valid", role)
		}
	}
	if ProjectMemberRole("owner").Valid() || ProjectMemberRole("").Valid() {
		t.Fatal("unexpected valid project member role")
	}
}

func TestStatusCountsAsDone(t *testing.T) {
	cases := []struct {
		in   Status
		want bool
	}{
		{StatusTodo, false},
		{StatusInProgress, false},
		{StatusDone, true},
		{StatusClosed, true},
		{"custom", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := c.in.CountsAsDone(); got != c.want {
				t.Fatalf("Status(%q).CountsAsDone() = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIssueCloseReasonValid(t *testing.T) {
	cases := []struct {
		in   IssueCloseReason
		want bool
	}{
		{CloseReasonDuplicate, true},
		{CloseReasonWontDo, true},
		{CloseReasonInvalid, true},
		{"", false},
		{"wontdo", false},
		{"won't_do", false},
		{"DUPLICATE", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := c.in.Valid(); got != c.want {
				t.Fatalf("IssueCloseReason(%q).Valid() = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestDateJSONRoundTrip(t *testing.T) {
	d, err := ParseDate("2026-06-24")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != `"2026-06-24"` {
		t.Fatalf("json = %s", b)
	}
	var got Date
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.String() != "2026-06-24" {
		t.Fatalf("got = %s", got.String())
	}
	if err := json.Unmarshal([]byte(`"2026/06/24"`), &got); err == nil {
		t.Fatal("Unmarshal invalid date succeeded")
	}
	if err := json.Unmarshal([]byte(`123`), &got); err == nil {
		t.Fatal("Unmarshal non-string date succeeded")
	}
}

func TestIssuePriorityValid(t *testing.T) {
	cases := []struct {
		in   IssuePriority
		want bool
	}{
		{PriorityP0, true},
		{PriorityP1, true},
		{PriorityP2, true},
		{PriorityP3, true},
		{PriorityP4, true},
		{"", false},
		{"p0", false},
		{"P5", false},
		{"urgent", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := c.in.Valid(); got != c.want {
				t.Fatalf("IssuePriority(%q).Valid() = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestProjectContextKindValid(t *testing.T) {
	cases := []struct {
		in   ProjectContextKind
		want bool
	}{
		{ProjectContextKindText, true},
		{"image", false},
		{"", false},
	}
	for _, c := range cases {
		if got := c.in.Valid(); got != c.want {
			t.Fatalf("ProjectContextKind(%q).Valid() = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestProjectContextScopeValid(t *testing.T) {
	cases := []struct {
		in   ProjectContextScope
		want bool
	}{
		{ProjectContextScopeProject, true},
		{ProjectContextScopeIssue, true},
		{"workspace", false},
		{"", false},
	}
	for _, c := range cases {
		if got := c.in.Valid(); got != c.want {
			t.Fatalf("ProjectContextScope(%q).Valid() = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestProjectContextRef(t *testing.T) {
	if got := ProjectContextRef(12); got != "context-12" {
		t.Fatalf("ProjectContextRef(12) = %q, want context-12", got)
	}
}

func TestStorageObjectRef(t *testing.T) {
	if got := StorageObjectRef(12); got != "object-12" {
		t.Fatalf("StorageObjectRef(12) = %q, want object-12", got)
	}
}

func TestWhiteboardPageRef(t *testing.T) {
	if got := WhiteboardPageRef(12); got != "whiteboard-12" {
		t.Fatalf("WhiteboardPageRef(12) = %q, want whiteboard-12", got)
	}
}

func TestSprintStatusValid(t *testing.T) {
	cases := []struct {
		in   SprintStatus
		want bool
	}{
		{SprintStatusPlanned, true},
		{SprintStatusActive, true},
		{SprintStatusCompleted, true},
		{"", false},
		{"open", false},
		{"ACTIVE", false},
		{"in progress", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := c.in.Valid(); got != c.want {
				t.Fatalf("SprintStatus(%q).Valid() = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestAuthTokenKindValid(t *testing.T) {
	cases := []struct {
		in   AuthTokenKind
		want bool
	}{
		{AuthTokenKindAPI, true},
		{AuthTokenKindSession, true},
		{"", false},
		{"jwt", false},
		{"API", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := c.in.Valid(); got != c.want {
				t.Fatalf("AuthTokenKind(%q).Valid() = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestAuthTokenLive(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	cases := []struct {
		name  string
		token AuthToken
		want  bool
	}{
		{"no expiry", AuthToken{}, true},
		{"expires later", AuthToken{ExpiresAt: &future}, true},
		{"expired", AuthToken{ExpiresAt: &past}, false},
		{"expires exactly now", AuthToken{ExpiresAt: &now}, false},
		{"revoked", AuthToken{RevokedAt: &past}, false},
		{"revoked and unexpired", AuthToken{ExpiresAt: &future, RevokedAt: &past}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.token.Live(now); got != c.want {
				t.Fatalf("Live() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAuthCredentialKindValid(t *testing.T) {
	cases := []struct {
		in   AuthCredentialKind
		want bool
	}{
		{AuthCredentialKindPassword, true},
		{AuthCredentialKindPasskey, true},
		{"", false},
		{"totp", false},
		{"PASSWORD", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := c.in.Valid(); got != c.want {
				t.Fatalf("AuthCredentialKind(%q).Valid() = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
