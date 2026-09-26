package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const DateLayout = "2006-01-02"

type Date time.Time

func ParseDate(raw string) (Date, error) {
	t, err := time.Parse(DateLayout, raw)
	if err != nil {
		return Date{}, err
	}
	return DateFromTime(t), nil
}

func DateFromTime(t time.Time) Date {
	return Date(time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC))
}

func (d Date) Time() time.Time {
	return time.Time(d)
}

func (d Date) String() string {
	return d.Time().Format(DateLayout)
}

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	parsed, err := ParseDate(raw)
	if err != nil {
		return fmt.Errorf("date must be YYYY-MM-DD")
	}
	*d = parsed
	return nil
}

type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusClosed     Status = "closed"
)

func (s Status) Valid() bool {
	switch s {
	case StatusTodo, StatusInProgress, StatusDone, StatusClosed:
		return true
	}
	return false
}

func (s Status) CountsAsDone() bool {
	switch s {
	case StatusDone, StatusClosed:
		return true
	}
	return false
}

type IssueCloseReason string

const (
	CloseReasonDuplicate IssueCloseReason = "duplicate"
	CloseReasonWontDo    IssueCloseReason = "wont_do"
	CloseReasonInvalid   IssueCloseReason = "invalid"
)

func (r IssueCloseReason) Valid() bool {
	switch r {
	case CloseReasonDuplicate, CloseReasonWontDo, CloseReasonInvalid:
		return true
	}
	return false
}

type IssuePriority string

const (
	PriorityP0 IssuePriority = "P0"
	PriorityP1 IssuePriority = "P1"
	PriorityP2 IssuePriority = "P2"
	PriorityP3 IssuePriority = "P3"
	PriorityP4 IssuePriority = "P4"
)

func (p IssuePriority) Valid() bool {
	switch p {
	case PriorityP0, PriorityP1, PriorityP2, PriorityP3, PriorityP4:
		return true
	}
	return false
}

const MaxIssueTagNameLength = 80

type IssueTagColor string

const (
	TagColorSlate  IssueTagColor = "slate"
	TagColorRed    IssueTagColor = "red"
	TagColorOrange IssueTagColor = "orange"
	TagColorAmber  IssueTagColor = "amber"
	TagColorYellow IssueTagColor = "yellow"
	TagColorGreen  IssueTagColor = "green"
	TagColorTeal   IssueTagColor = "teal"
	TagColorCyan   IssueTagColor = "cyan"
	TagColorBlue   IssueTagColor = "blue"
	TagColorViolet IssueTagColor = "violet"
	TagColorPink   IssueTagColor = "pink"
)

func (c IssueTagColor) Valid() bool {
	switch c {
	case TagColorSlate, TagColorRed, TagColorOrange, TagColorAmber, TagColorYellow, TagColorGreen, TagColorTeal, TagColorCyan, TagColorBlue, TagColorViolet, TagColorPink:
		return true
	}
	return false
}

func IssueTagColorOrDefault(c IssueTagColor) IssueTagColor {
	if c == "" {
		return TagColorBlue
	}
	return c
}

func NormalizeIssueTagName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	name = strings.TrimPrefix(name, "#")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("tag name required")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("tag name must not contain control characters")
		}
	}
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", fmt.Errorf("tag name required")
	}
	if utf8.RuneCountInString(name) > MaxIssueTagNameLength {
		return "", fmt.Errorf("tag name must be at most %d chars", MaxIssueTagNameLength)
	}
	return name, nil
}

func IssueTagDisplayName(name string) string {
	if strings.HasPrefix(name, "#") {
		return name
	}
	return "#" + name
}

type SprintStatus string

const (
	SprintStatusPlanned   SprintStatus = "planned"
	SprintStatusActive    SprintStatus = "active"
	SprintStatusCompleted SprintStatus = "completed"
)

func (s SprintStatus) Valid() bool {
	switch s {
	case SprintStatusPlanned, SprintStatusActive, SprintStatusCompleted:
		return true
	}
	return false
}

type User struct {
	ID                            uuid.UUID  `json:"id"`
	Username                      string     `json:"username"`
	Email                         string     `json:"email,omitempty"`
	Name                          string     `json:"name"`
	IsAdmin                       bool       `json:"is_admin"`
	ProfileImageObjectID          *uuid.UUID `json:"profile_image_object_id,omitempty"`
	ProfileImageThumbnailObjectID *uuid.UUID `json:"profile_image_thumbnail_object_id,omitempty"`
	CreatedAt                     time.Time  `json:"created_at"`
}

type PushNotificationPreferences struct {
	Mentions       bool `json:"mentions"`
	Assignments    bool `json:"assignments"`
	Comments       bool `json:"comments"`
	StatusChanges  bool `json:"status_changes"`
	DueDateChanges bool `json:"due_date_changes"`
}

type PushSubscription struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	Endpoint      string     `json:"-"`
	P256DH        string     `json:"-"`
	AuthSecret    string     `json:"-"`
	UserAgent     string     `json:"user_agent"`
	FailureCount  int        `json:"failure_count"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	DisabledAt    *time.Time `json:"disabled_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type AuthCredentialKind string

const (
	AuthCredentialKindPassword AuthCredentialKind = "password"
	AuthCredentialKindPasskey  AuthCredentialKind = "passkey"
)

func (k AuthCredentialKind) Valid() bool {
	switch k {
	case AuthCredentialKindPassword, AuthCredentialKindPasskey:
		return true
	}
	return false
}

type AuthTokenKind string

const (
	AuthTokenKindAPI     AuthTokenKind = "api"
	AuthTokenKindSession AuthTokenKind = "session"
	// AuthTokenKindOAuth is an access token issued to a connected OAuth client
	// through the authorization-code flow. It carries the permissions of the
	// user who approved it, exactly as an API token they created themselves
	// would, and expires on its own without needing to be revoked.
	AuthTokenKindOAuth AuthTokenKind = "oauth"
)

func (k AuthTokenKind) Valid() bool {
	switch k {
	case AuthTokenKindAPI, AuthTokenKindSession, AuthTokenKindOAuth:
		return true
	}
	return false
}

type AuthToken struct {
	ID         uuid.UUID     `json:"id"`
	UserID     uuid.UUID     `json:"user_id"`
	Kind       AuthTokenKind `json:"kind"`
	Name       string        `json:"name"`
	CreatedAt  time.Time     `json:"created_at"`
	LastUsedAt *time.Time    `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time    `json:"expires_at,omitempty"`
	RevokedAt  *time.Time    `json:"revoked_at,omitempty"`
}

// Live reports whether a token can still authenticate. It mirrors the predicate
// the store applies when resolving a raw token, so anything counted from a
// listing agrees with what the token would actually do.
func (t AuthToken) Live(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	return t.ExpiresAt == nil || t.ExpiresAt.After(now)
}

type PasskeyCredential struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	Name           string     `json:"name"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	BackupEligible bool       `json:"backup_eligible"`
	BackupState    bool       `json:"backup_state"`
	CloneWarning   bool       `json:"clone_warning"`
}

type PasswordLoginState struct {
	HasPassword    bool `json:"has_password"`
	Enabled        bool `json:"enabled"`
	CanDisable     bool `json:"can_disable"`
	ActivePasskeys int  `json:"active_passkeys"`
}

type ProjectMemberRole string

const (
	ProjectMemberRoleMember   ProjectMemberRole = "member"
	ProjectMemberRoleReadonly ProjectMemberRole = "readonly"
)

func (r ProjectMemberRole) Valid() bool {
	switch r {
	case ProjectMemberRoleMember, ProjectMemberRoleReadonly:
		return true
	}
	return false
}

type ProjectMember struct {
	ProjectID                     uuid.UUID         `json:"project_id"`
	UserID                        uuid.UUID         `json:"user_id"`
	Username                      string            `json:"username"`
	Name                          string            `json:"name"`
	ProfileImageThumbnailObjectID *uuid.UUID        `json:"profile_image_thumbnail_object_id,omitempty"`
	Role                          ProjectMemberRole `json:"role"`
	CreatedAt                     time.Time         `json:"created_at"`
}

type ProjectMemberCandidate struct {
	ID                            uuid.UUID  `json:"id"`
	Username                      string     `json:"username"`
	Name                          string     `json:"name"`
	ProfileImageThumbnailObjectID *uuid.UUID `json:"profile_image_thumbnail_object_id,omitempty"`
}

type ProjectAccessSettings struct {
	IsPublic            bool `json:"is_public"`
	PublicIssueCreation bool `json:"public_issue_creation"`
}

type ProjectUserBlock struct {
	ID                            uuid.UUID  `json:"id"`
	ProjectID                     uuid.UUID  `json:"project_id"`
	UserID                        uuid.UUID  `json:"user_id"`
	Username                      string     `json:"username"`
	Name                          string     `json:"name"`
	ProfileImageThumbnailObjectID *uuid.UUID `json:"profile_image_thumbnail_object_id,omitempty"`
	CreatedByID                   uuid.UUID  `json:"created_by_id"`
	CreatedAt                     time.Time  `json:"created_at"`
}

type ProjectAssignee struct {
	ID                            uuid.UUID  `json:"id"`
	Username                      string     `json:"username"`
	Name                          string     `json:"name"`
	ProfileImageThumbnailObjectID *uuid.UUID `json:"profile_image_thumbnail_object_id,omitempty"`
}

type ProjectIssueStatusCounts struct {
	Total      int `json:"total"`
	Todo       int `json:"todo"`
	InProgress int `json:"in_progress"`
	Done       int `json:"done"`
	Closed     int `json:"closed"`
}

type ProjectAssigneeIssueStats struct {
	UserID                        uuid.UUID                `json:"user_id"`
	Username                      string                   `json:"username"`
	Name                          string                   `json:"name"`
	ProfileImageThumbnailObjectID *uuid.UUID               `json:"profile_image_thumbnail_object_id,omitempty"`
	Counts                        ProjectIssueStatusCounts `json:"counts"`
}

type ProjectStats struct {
	ProjectID    uuid.UUID                   `json:"project_id"`
	AllTime      ProjectIssueStatusCounts    `json:"all_time"`
	Last7Days    ProjectIssueStatusCounts    `json:"last_7_days"`
	TopAssignees []ProjectAssigneeIssueStats `json:"top_assignees"`
}

// InsightRange names the window every project insight series covers.
type InsightRange string

const (
	InsightRangeTwoWeeks   InsightRange = "2w"
	InsightRangeThirtyDays InsightRange = "30d"
	InsightRangeNinetyDays InsightRange = "90d"
	InsightRangeAll        InsightRange = "all"

	// DefaultInsightRange is used when a caller does not name a range.
	DefaultInsightRange = InsightRangeThirtyDays
)

// InsightRanges lists the ranges in the order controls should offer them.
func InsightRanges() []InsightRange {
	return []InsightRange{InsightRangeTwoWeeks, InsightRangeThirtyDays, InsightRangeNinetyDays, InsightRangeAll}
}

func (r InsightRange) Valid() bool {
	switch r {
	case InsightRangeTwoWeeks, InsightRangeThirtyDays, InsightRangeNinetyDays, InsightRangeAll:
		return true
	}
	return false
}

func (r InsightRange) Label() string {
	switch r {
	case InsightRangeTwoWeeks:
		return "2 weeks"
	case InsightRangeThirtyDays:
		return "30 days"
	case InsightRangeNinetyDays:
		return "90 days"
	case InsightRangeAll:
		return "All time"
	default:
		return string(r)
	}
}

// InsightBucket is the period each point or bar in an insight series covers.
type InsightBucket string

const (
	InsightBucketDay   InsightBucket = "day"
	InsightBucketWeek  InsightBucket = "week"
	InsightBucketMonth InsightBucket = "month"
)

// ProjectInsightFlowPoint is issue status as of the end of one period. Scope,
// Started, and Completed are the burn-up lines: cancelled issues leave scope,
// and a reopened issue leaves Completed on the day it reopens.
type ProjectInsightFlowPoint struct {
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Todo        int       `json:"todo"`
	InProgress  int       `json:"in_progress"`
	Done        int       `json:"done"`
	Cancelled   int       `json:"cancelled"`
	Scope       int       `json:"scope"`
	Started     int       `json:"started"`
	Completed   int       `json:"completed"`
}

// ProjectInsightThroughputPoint counts status transitions inside one period.
// Resolved counts every move into Done or Closed from an open status, and
// Reopened every move back out, so an issue resolved twice counts twice.
type ProjectInsightThroughputPoint struct {
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Created     int       `json:"created"`
	Resolved    int       `json:"resolved"`
	Reopened    int       `json:"reopened"`
}

// ProjectInsightCycleTimeIssue is one Done issue: first move to In progress
// through its last move to Done.
type ProjectInsightCycleTimeIssue struct {
	IssueID       uuid.UUID `json:"issue_id"`
	Identifier    string    `json:"identifier"`
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at"`
	DurationHours float64   `json:"duration_hours"`
}

type ProjectInsightCycleTime struct {
	Issues []ProjectInsightCycleTimeIssue `json:"issues"`
	// MedianHours and P85Hours are continuous percentiles over Issues; zero
	// when Issues is empty.
	MedianHours float64 `json:"median_hours"`
	P85Hours    float64 `json:"p85_hours"`
	// NotStarted counts issues completed in the range that never moved to In
	// progress, so they have no cycle time.
	NotStarted int `json:"not_started"`
	// Truncated reports that only the most recent completions are included.
	Truncated bool `json:"truncated"`
}

// ProjectInsightSprintVelocity summarises one completed sprint. Committed is
// the issue count when the sprint started; it is nil when that moment was not
// recorded. Done, Cancelled, and CarriedOver come from the membership captured
// when the sprint completed.
type ProjectInsightSprintVelocity struct {
	SprintID    uuid.UUID  `json:"sprint_id"`
	Ref         string     `json:"ref"`
	Number      int        `json:"number"`
	Name        string     `json:"name"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time  `json:"completed_at"`
	Committed   *int       `json:"committed"`
	Total       int        `json:"total"`
	Done        int        `json:"done"`
	Cancelled   int        `json:"cancelled"`
	CarriedOver int        `json:"carried_over"`
}

// ProjectInsightSprintBurnup is daily scope/started/completed for one sprint.
// Points stop at the present for an active sprint; End is the planned end
// when that is later, so the remaining days are visible.
type ProjectInsightSprintBurnup struct {
	SprintID  uuid.UUID                 `json:"sprint_id"`
	Ref       string                    `json:"ref"`
	Name      string                    `json:"name"`
	Status    SprintStatus              `json:"status"`
	Start     time.Time                 `json:"start"`
	End       time.Time                 `json:"end"`
	Days      int                       `json:"days"`
	Points    []ProjectInsightFlowPoint `json:"points"`
	Estimated bool                      `json:"estimated"`
}

// ProjectInsightSprintOption is a sprint whose burn-up can be requested.
type ProjectInsightSprintOption struct {
	SprintID uuid.UUID    `json:"sprint_id"`
	Ref      string       `json:"ref"`
	Name     string       `json:"name"`
	Status   SprintStatus `json:"status"`
}

type ProjectInsightSprints struct {
	// Enabled reports whether sprint charts apply: the project's sprint mode.
	// The store leaves it false and the server fills it in. The sprint series
	// are returned either way, since completed sprints stay history.
	Enabled bool `json:"enabled"`
	// Count is every sprint the project has ever had, in any status.
	Count    int                            `json:"count"`
	Velocity []ProjectInsightSprintVelocity `json:"velocity"`
	Options  []ProjectInsightSprintOption   `json:"options"`
	Burnup   *ProjectInsightSprintBurnup    `json:"burnup,omitempty"`
}

// ProjectInsights is every insight series for one range, all derived from
// persisted history. Deleted issues are excluded throughout.
type ProjectInsights struct {
	ProjectID  uuid.UUID                       `json:"project_id"`
	Range      InsightRange                    `json:"range"`
	Bucket     InsightBucket                   `json:"bucket"`
	Start      time.Time                       `json:"start"`
	End        time.Time                       `json:"end"`
	Flow       []ProjectInsightFlowPoint       `json:"flow"`
	Throughput []ProjectInsightThroughputPoint `json:"throughput"`
	CycleTime  ProjectInsightCycleTime         `json:"cycle_time"`
	Sprints    ProjectInsightSprints           `json:"sprints"`
}

type ProjectChangelogChange struct {
	Field string `json:"field"`
	Label string `json:"label"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
}

type ProjectChangelogDetails struct {
	Changes          []ProjectChangelogChange              `json:"changes,omitempty"`
	Preview          string                                `json:"preview,omitempty"`
	PushNotification *ProjectChangelogPushNotificationData `json:"push_notification,omitempty"`
}

type ProjectChangelogPushNotificationData struct {
	AssigneeID       *uuid.UUID `json:"assignee_id,omitempty"`
	MentionUsernames []string   `json:"mention_usernames,omitempty"`
}

type ProjectChangelogActor struct {
	ID                            uuid.UUID  `json:"id"`
	Username                      string     `json:"username"`
	Name                          string     `json:"name"`
	ProfileImageThumbnailObjectID *uuid.UUID `json:"profile_image_thumbnail_object_id,omitempty"`
}

type ProjectChangelogEntry struct {
	ID            uuid.UUID               `json:"id"`
	ProjectID     uuid.UUID               `json:"project_id"`
	ActorID       *uuid.UUID              `json:"actor_id,omitempty"`
	Actor         *ProjectChangelogActor  `json:"actor,omitempty"`
	Entity        string                  `json:"entity"`
	Op            string                  `json:"op"`
	EntityID      uuid.UUID               `json:"entity_id"`
	IssueID       *uuid.UUID              `json:"issue_id,omitempty"`
	ParentIssueID *uuid.UUID              `json:"parent_issue_id,omitempty"`
	TargetRef     string                  `json:"target_ref,omitempty"`
	TargetTitle   string                  `json:"target_title,omitempty"`
	Summary       string                  `json:"summary"`
	Details       ProjectChangelogDetails `json:"details,omitempty"`
	Version       int64                   `json:"version"`
	CreatedAt     time.Time               `json:"created_at"`
}

type Project struct {
	ID                     uuid.UUID  `json:"id"`
	OwnerID                uuid.UUID  `json:"owner_id"`
	OwnerUsername          string     `json:"owner_username"`
	Key                    string     `json:"key"`
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	ImageObjectID          *uuid.UUID `json:"image_object_id,omitempty"`
	ImageThumbnailObjectID *uuid.UUID `json:"image_thumbnail_object_id,omitempty"`
	SprintsEnabled         bool       `json:"sprints_enabled"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type StorageObject struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"project_id"`
	Number      int        `json:"number"`
	Ref         string     `json:"ref"`
	OwnerUserID *uuid.UUID `json:"owner_user_id,omitempty"`
	Backend     string     `json:"backend"`
	Bucket      string     `json:"bucket"`
	ObjectKey   string     `json:"object_key"`
	Filename    string     `json:"filename"`
	ContentType string     `json:"content_type"`
	ByteSize    int64      `json:"byte_size"`
	SHA256      string     `json:"sha256"`
	CreatedByID uuid.UUID  `json:"created_by_id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

type IssueAttachment struct {
	ID              uuid.UUID     `json:"id"`
	ProjectID       uuid.UUID     `json:"project_id"`
	IssueID         uuid.UUID     `json:"issue_id"`
	StorageObjectID uuid.UUID     `json:"storage_object_id"`
	Object          StorageObject `json:"object"`
	CreatedByID     uuid.UUID     `json:"created_by_id"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type SprintAttachment struct {
	ID              uuid.UUID     `json:"id"`
	ProjectID       uuid.UUID     `json:"project_id"`
	SprintID        uuid.UUID     `json:"sprint_id"`
	StorageObjectID uuid.UUID     `json:"storage_object_id"`
	Object          StorageObject `json:"object"`
	CreatedByID     uuid.UUID     `json:"created_by_id"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type ProjectAttachment struct {
	ID              uuid.UUID     `json:"id"`
	ProjectID       uuid.UUID     `json:"project_id"`
	StorageObjectID uuid.UUID     `json:"storage_object_id"`
	Object          StorageObject `json:"object"`
	CreatedByID     uuid.UUID     `json:"created_by_id"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type ContextAttachment struct {
	ID              uuid.UUID     `json:"id"`
	ProjectID       uuid.UUID     `json:"project_id"`
	ContextID       uuid.UUID     `json:"context_id"`
	StorageObjectID uuid.UUID     `json:"storage_object_id"`
	Object          StorageObject `json:"object"`
	CreatedByID     uuid.UUID     `json:"created_by_id"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type Issue struct {
	ID            uuid.UUID         `json:"id"`
	ProjectID     uuid.UUID         `json:"project_id"`
	OwnerUsername string            `json:"owner_username"`
	ProjectKey    string            `json:"project_key"`
	Number        int               `json:"number"`
	Identifier    string            `json:"identifier"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Status        Status            `json:"status"`
	CloseReason   *IssueCloseReason `json:"close_reason"`
	Priority      IssuePriority     `json:"priority"`
	AssigneeID    *uuid.UUID        `json:"assignee_id,omitempty"`
	ReporterID    *uuid.UUID        `json:"reporter_id,omitempty"`
	SprintID      *uuid.UUID        `json:"sprint_id,omitempty"`
	ParentIssueID *uuid.UUID        `json:"parent_issue_id,omitempty"`
	DueDate       *Date             `json:"due_date"`
	Tags          []IssueTag        `json:"tags,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type IssueTag struct {
	ID          uuid.UUID     `json:"id"`
	ProjectID   uuid.UUID     `json:"project_id"`
	Number      int           `json:"number"`
	Ref         string        `json:"ref"`
	Name        string        `json:"name"`
	DisplayName string        `json:"display_name"`
	Color       IssueTagColor `json:"color"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type IssueTagLink struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	IssueID   uuid.UUID `json:"issue_id"`
	TagID     uuid.UUID `json:"tag_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func IssueTagRef(number int) string {
	return fmt.Sprintf("tag-%d", number)
}

type LinkType string

const (
	LinkTypeBlocks     LinkType = "blocks"
	LinkTypeDuplicates LinkType = "duplicates"
	LinkTypeRelatesTo  LinkType = "relates_to"
	LinkTypeClones     LinkType = "clones"
)

func (t LinkType) Valid() bool {
	switch t {
	case LinkTypeBlocks, LinkTypeDuplicates, LinkTypeRelatesTo, LinkTypeClones:
		return true
	}
	return false
}

type IssueLink struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Number    int       `json:"number"`
	Ref       string    `json:"ref"`
	SourceID  uuid.UUID `json:"source_id"`
	TargetID  uuid.UUID `json:"target_id"`
	LinkType  LinkType  `json:"link_type"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ProjectContextKind string

const (
	ProjectContextKindText ProjectContextKind = "text"
)

func (k ProjectContextKind) Valid() bool {
	switch k {
	case ProjectContextKindText:
		return true
	}
	return false
}

type ProjectContextScope string

const (
	ProjectContextScopeProject ProjectContextScope = "project"
	ProjectContextScopeIssue   ProjectContextScope = "issue"
)

func (s ProjectContextScope) Valid() bool {
	switch s {
	case ProjectContextScopeProject, ProjectContextScopeIssue:
		return true
	}
	return false
}

type ProjectContext struct {
	ID             uuid.UUID           `json:"id"`
	ProjectID      uuid.UUID           `json:"project_id"`
	Number         int                 `json:"number"`
	Ref            string              `json:"ref"`
	Scope          ProjectContextScope `json:"scope"`
	Position       *int64              `json:"position,omitempty"`
	Title          string              `json:"title"`
	Kind           ProjectContextKind  `json:"kind"`
	ContentType    string              `json:"content_type"`
	Body           string              `json:"body"`
	SourceFilename *string             `json:"source_filename,omitempty"`
	CreatedByID    uuid.UUID           `json:"created_by_id"`
	UpdatedByID    uuid.UUID           `json:"updated_by_id"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

type ProjectContextSummary struct {
	ID               uuid.UUID           `json:"id"`
	ProjectID        uuid.UUID           `json:"project_id"`
	Number           int                 `json:"number"`
	Ref              string              `json:"ref"`
	Scope            ProjectContextScope `json:"scope"`
	Position         *int64              `json:"position,omitempty"`
	Title            string              `json:"title"`
	Kind             ProjectContextKind  `json:"kind"`
	ContentType      string              `json:"content_type"`
	SourceFilename   *string             `json:"source_filename,omitempty"`
	CreatedByID      uuid.UUID           `json:"created_by_id"`
	UpdatedByID      uuid.UUID           `json:"updated_by_id"`
	LinkedIssueCount int                 `json:"linked_issue_count"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

type IssueContextLink struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	IssueID   uuid.UUID `json:"issue_id"`
	ContextID uuid.UUID `json:"context_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func SprintRef(number int) string {
	return fmt.Sprintf("sprint-%d", number)
}

func IssueLinkRef(number int) string {
	return fmt.Sprintf("link-%d", number)
}

func ProjectContextRef(number int) string {
	return fmt.Sprintf("context-%d", number)
}

func StorageObjectRef(number int) string {
	return fmt.Sprintf("object-%d", number)
}

// WhiteboardPage is a free-form Markdown note on a project's Whiteboard. Pages
// are never linked to issues and are soft-deleted, so a ref is never reused.
type WhiteboardPage struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Number      int       `json:"number"`
	Ref         string    `json:"ref"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	CreatedByID uuid.UUID `json:"created_by_id"`
	UpdatedByID uuid.UUID `json:"updated_by_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// WhiteboardPageSummary is a list row: everything but the Markdown body, which
// is only loaded for the page being read.
type WhiteboardPageSummary struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Number      int       `json:"number"`
	Ref         string    `json:"ref"`
	Title       string    `json:"title"`
	CreatedByID uuid.UUID `json:"created_by_id"`
	UpdatedByID uuid.UUID `json:"updated_by_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func WhiteboardPageRef(number int) string {
	return fmt.Sprintf("whiteboard-%d", number)
}

func CommentRef(number int) string {
	return fmt.Sprintf("comment-%d", number)
}

type Comment struct {
	ID        uuid.UUID  `json:"id"`
	IssueID   uuid.UUID  `json:"issue_id"`
	Number    int        `json:"number"`
	Ref       string     `json:"ref"`
	AuthorID  uuid.UUID  `json:"author_id"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	EditedAt  *time.Time `json:"edited_at"`
}

type Sprint struct {
	ID           uuid.UUID    `json:"id"`
	ProjectID    uuid.UUID    `json:"project_id"`
	Number       int          `json:"number"`
	Ref          string       `json:"ref"`
	Name         string       `json:"name"`
	Goal         string       `json:"goal"`
	Status       SprintStatus `json:"status"`
	PlannedOrder *int64       `json:"planned_order,omitempty"`
	StartDate    *time.Time   `json:"start_date"`
	EndDate      *time.Time   `json:"end_date"`
	CompletedAt  *time.Time   `json:"completed_at,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}
