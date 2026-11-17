package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInsightRange(t *testing.T) {
	t.Parallel()
	want := map[InsightRange]string{
		InsightRangeTwoWeeks:   "2 weeks",
		InsightRangeThirtyDays: "30 days",
		InsightRangeNinetyDays: "90 days",
		InsightRangeAll:        "All time",
	}
	ranges := InsightRanges()
	if len(ranges) != len(want) || ranges[0] != InsightRangeTwoWeeks || ranges[len(ranges)-1] != InsightRangeAll {
		t.Fatalf("InsightRanges() = %v", ranges)
	}
	for _, r := range ranges {
		if !r.Valid() || r.Label() != want[r] {
			t.Fatalf("%q valid=%v label=%q", r, r.Valid(), r.Label())
		}
	}
	if !DefaultInsightRange.Valid() {
		t.Fatal("default insight range is invalid")
	}
	if bogus := InsightRange("7d"); bogus.Valid() || bogus.Label() != "7d" {
		t.Fatalf("unknown range valid=%v label=%q", bogus.Valid(), bogus.Label())
	}
}

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

func TestProjectAccessMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mode       ProjectAccessMode
		valid      bool
		publicRead bool
		issues     bool
		label      string
	}{
		{ProjectAccessPrivate, true, false, false, "Private"},
		{ProjectAccessPublic, true, true, false, "Public"},
		{ProjectAccessPublicIssues, true, true, true, "Public, open to issues"},
		{ProjectAccessHelpDesk, true, false, true, "Help desk"},
		{"everyone", false, false, false, "Private"},
		{"", false, false, false, "Private"},
	}
	for _, tc := range cases {
		if got := tc.mode.Valid(); got != tc.valid {
			t.Fatalf("%q.Valid() = %v, want %v", tc.mode, got, tc.valid)
		}
		if got := tc.mode.PublicRead(); got != tc.publicRead {
			t.Fatalf("%q.PublicRead() = %v, want %v", tc.mode, got, tc.publicRead)
		}
		if got := tc.mode.OutsideIssueCreation(); got != tc.issues {
			t.Fatalf("%q.OutsideIssueCreation() = %v, want %v", tc.mode, got, tc.issues)
		}
		if got := tc.mode.Label(); got != tc.label {
			t.Fatalf("%q.Label() = %q, want %q", tc.mode, got, tc.label)
		}
		settings := NewProjectAccessSettings(tc.mode)
		if settings.AccessMode != tc.mode || settings.IsPublic != tc.publicRead || settings.PublicIssueCreation != tc.issues {
			t.Fatalf("NewProjectAccessSettings(%q) = %+v", tc.mode, settings)
		}
	}
	for _, tc := range []struct {
		isPublic, issues bool
		want             ProjectAccessMode
	}{
		{false, false, ProjectAccessPrivate},
		{false, true, ProjectAccessPrivate},
		{true, false, ProjectAccessPublic},
		{true, true, ProjectAccessPublicIssues},
	} {
		if got := ProjectAccessModeFromFlags(tc.isPublic, tc.issues); got != tc.want {
			t.Fatalf("ProjectAccessModeFromFlags(%v, %v) = %q, want %q", tc.isPublic, tc.issues, got, tc.want)
		}
	}
}

func TestCommentVisibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		visibility CommentVisibility
		valid      bool
		label      string
	}{
		{CommentVisibilityShared, true, "Shared"},
		{CommentVisibilityMembers, true, "Members only"},
		{"public", false, "Shared"},
		{"", false, "Shared"},
	} {
		if got := tc.visibility.Valid(); got != tc.valid {
			t.Fatalf("%q.Valid() = %v, want %v", tc.visibility, got, tc.valid)
		}
		if got := tc.visibility.Label(); got != tc.label {
			t.Fatalf("%q.Label() = %q, want %q", tc.visibility, got, tc.label)
		}
	}
	for mode, want := range map[ProjectAccessMode]CommentVisibility{
		ProjectAccessPrivate:      CommentVisibilityShared,
		ProjectAccessPublic:       CommentVisibilityShared,
		ProjectAccessPublicIssues: CommentVisibilityShared,
		ProjectAccessHelpDesk:     CommentVisibilityMembers,
	} {
		if got := DefaultCommentVisibility(mode); got != want {
			t.Fatalf("DefaultCommentVisibility(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestReporterIssue(t *testing.T) {
	t.Parallel()
	for status, want := range map[Status]ReporterStatus{
		StatusTodo:       ReporterStatusOpen,
		StatusInProgress: ReporterStatusInProgress,
		StatusDone:       ReporterStatusClosed,
		StatusClosed:     ReporterStatusClosed,
		"":               ReporterStatusOpen,
	} {
		if got := NewReporterStatus(status); got != want {
			t.Fatalf("NewReporterStatus(%q) = %q, want %q", status, got, want)
		}
	}
	for status, want := range map[ReporterStatus]string{
		ReporterStatusOpen:       "Open",
		ReporterStatusInProgress: "In progress",
		ReporterStatusClosed:     "Closed",
	} {
		if got := status.Label(); got != want {
			t.Fatalf("%q.Label() = %q, want %q", status, got, want)
		}
	}

	reporterID := uuid.New()
	reason := CloseReasonWontDo
	due := DateFromTime(time.Now())
	issue := Issue{
		ID: uuid.New(), ProjectID: uuid.New(), OwnerUsername: "owner", ProjectKey: "HELP", Number: 3, Identifier: "HELP-3",
		Title: "Broken", Description: "Details", Status: StatusClosed, CloseReason: &reason, Priority: PriorityP0,
		AssigneeID: &reporterID, ReporterID: &reporterID, DueDate: &due, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	worker := WorkerHuman
	issue.Worker = &worker
	got := NewReporterIssue(issue)
	if got.ID != issue.ID || got.Identifier != "HELP-3" || got.Title != "Broken" || got.Description != "Details" ||
		got.Status != ReporterStatusClosed || got.ReporterID != &reporterID || !got.CreatedAt.Equal(issue.CreatedAt) {
		t.Fatalf("NewReporterIssue = %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, hidden := range []string{"priority", "worker", "assignee_id", "close_reason", "due_date", "sprint_id", "parent_issue_id", "tags"} {
		if strings.Contains(string(raw), `"`+hidden+`"`) {
			t.Fatalf("reporter issue JSON carries %s: %s", hidden, raw)
		}
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

func TestIssueWorkerValid(t *testing.T) {
	cases := []struct {
		in   IssueWorker
		want bool
	}{
		{WorkerAgent, true},
		{WorkerHuman, true},
		{"", false},
		{"Agent", false},
		{"robot", false},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := c.in.Valid(); got != c.want {
				t.Fatalf("IssueWorker(%q).Valid() = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIssueWorkerLabel(t *testing.T) {
	for in, want := range map[IssueWorker]string{WorkerAgent: "Agent", WorkerHuman: "Human", "robot": "robot"} {
		if got := in.Label(); got != want {
			t.Fatalf("IssueWorker(%q).Label() = %q, want %q", in, got, want)
		}
	}
}

func TestBackgroundPreset(t *testing.T) {
	labels := map[BackgroundPreset]string{
		BackgroundIndigo:  "Indigo",
		BackgroundSlate:   "Slate",
		BackgroundEmerald: "Emerald",
		BackgroundSky:     "Sky",
		BackgroundAmber:   "Amber",
	}
	if len(BackgroundPresets) != len(labels) || BackgroundPresets[0] != BackgroundIndigo {
		t.Fatalf("BackgroundPresets = %v, want every preset with Indigo first", BackgroundPresets)
	}
	for _, preset := range BackgroundPresets {
		if !preset.Valid() || preset.OrDefault() != preset || preset.Label() != labels[preset] {
			t.Fatalf("preset %q: valid %v, default %q, label %q", preset, preset.Valid(), preset.OrDefault(), preset.Label())
		}
	}
	for _, unknown := range []BackgroundPreset{"", "Indigo", "rose", "violet"} {
		if unknown.Valid() || unknown.OrDefault() != BackgroundIndigo || unknown.Label() != string(unknown) {
			t.Fatalf("unknown %q: valid %v, default %q, label %q", unknown, unknown.Valid(), unknown.OrDefault(), unknown.Label())
		}
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

func TestParseStorageObjectRef(t *testing.T) {
	for _, number := range []int{1, 9, 12, 100000} {
		got, ok := ParseStorageObjectRef(StorageObjectRef(number))
		if !ok || got != number {
			t.Fatalf("ParseStorageObjectRef(%q) = %d, %v", StorageObjectRef(number), got, ok)
		}
	}
	for _, raw := range []string{
		"", "object", "object-", "object-0", "object--1", "object-+1", "object-01", "object-1 ", " object-1",
		"Object-1", "issue-1", "object-1.5", "object-x", "object-99999999999999999999", "1",
	} {
		if got, ok := ParseStorageObjectRef(raw); ok {
			t.Fatalf("ParseStorageObjectRef(%q) = %d, true; want false", raw, got)
		}
	}
}
