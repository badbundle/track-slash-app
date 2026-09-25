package server

import (
	"html/template"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

type uiLoginData struct {
	Error     string
	Next      string
	CSRFToken string
}

type uiSignupData struct {
	Error                string
	Next                 string
	CSRFToken            string
	PreviewTermsRequired bool
}

type uiShellData struct {
	CSRFToken         string
	Authenticated     bool
	Anonymous         bool
	User              model.User
	Projects          []model.Project
	SidebarFavorites  uiSidebarFavoritesData
	SidebarActive     uiSidebarState
	WorkPanel         *uiWorkPanelData
	ProjectsPanel     *uiProjectsPanelData
	NewProjectPanel   *uiNewProjectPanelData
	NewIssuePanel     *uiNewIssuePanelData
	ProjectPanel      *uiProjectPanelData
	DeletedPanel      *uiDeletedIssuesPanelData
	DeletedIssuePanel *uiDeletedIssuePanelData
	IssuePanel        *uiIssuePanelData
	ContextManager    *uiContextManagerData
	TagManager        *uiTagManagerData
	TokenPanel        *uiTokenPanelData
	SettingsPanel     *uiSettingsPanelData
	ErrorPanel        *uiErrorPanelData
}

type uiSidebarState struct {
	View      string
	ProjectID uuid.UUID
}

type uiSidebarFavoritesData struct {
	Projects        []model.Project
	ActiveProjectID uuid.UUID
	OOB             bool
}

type uiUserAvatarData struct {
	ID           uuid.UUID
	Label        string
	Initials     string
	ThumbnailURL string
	Class        string
}

type uiProjectIconData struct {
	Label        string
	Initial      string
	ThumbnailURL string
	Class        string
}

type uiImagePickerData struct {
	CSRFToken    string
	Modal        uiModalData
	Label        string
	ThumbnailURL string
	Fallback     string
	Circular     bool
	TriggerLabel string
	UploadAction string
	DeleteAction string
	HasImage     bool
	HXTarget     string
}

type uiProjectFavoriteData struct {
	CSRFToken string
	Project   model.Project
	View      string
	Favorite  bool
	Sidebar   uiSidebarFavoritesData
}

type uiBreadcrumbData struct {
	Items []uiBreadcrumbItem
}

type uiBreadcrumbItem struct {
	Label    string
	Href     string
	HXGet    string
	IssueKey bool
	Current  bool
}

type uiIssueItem struct {
	Issue            model.Issue
	Project          model.Project
	Sprint           *model.Sprint
	Assignee         *model.ProjectAssignee
	SubIssueProgress store.SubIssueProgress
}

type uiIssueColumn struct {
	Status model.Status
	Label  string
	Issues []uiIssueItem
}

type uiSprintDescriptionData struct {
	Project             model.Project
	Sprint              model.Sprint
	AttachmentCount     int
	DescriptionExpanded bool
	DescriptionHTML     template.HTML
	Attachments         []model.SprintAttachment
	AttachmentsHasMore  bool
}

type uiPlannedSprint struct {
	Project             model.Project
	Sprint              model.Sprint
	Issues              []uiIssueItem
	HasMore             bool
	AttachmentCount     int
	DescriptionExpanded bool
	DescriptionHTML     template.HTML
	Attachments         []model.SprintAttachment
	AttachmentsHasMore  bool
}

type uiSprintFormData struct {
	NameInput      string
	GoalInput      string
	StartDateInput string
	EndDateInput   string
	Error          string
}

type uiDescriptionAttachment struct {
	Object         model.StorageObject
	ContentHref    string
	InlineHref     string
	DeleteHref     string
	DeleteJSONHref string
	Markdown       string
	InlineImage    bool
}

type uiAttachmentListData struct {
	ID        string
	Items     []uiDescriptionAttachment
	HasMore   bool
	Editing   bool
	CanDelete bool
	UploadURL string
}

type uiDescriptionEditorData struct {
	Name        string
	Source      string
	Rows        int
	Autofocus   bool
	UploadURL   string
	ListTarget  string
	Placeholder string
}

type uiDescriptionBodyData struct {
	Source     string
	HTML       template.HTML
	EmptyLabel string
}

type uiSprintIssueFormData struct {
	IssueInput string
	Error      string
}

type uiAssigneeFilterItem struct {
	Assignee model.ProjectAssignee
	Selected bool
	Href     string
	HXGet    string
	HXPush   string
}

type uiTagFilterItem struct {
	Tag      model.IssueTag
	Label    string
	Selected bool
	Href     string
	HXGet    string
	HXPush   string
}

type uiProjectStatusFilterItem struct {
	Label  string
	Href   string
	HXGet  string
	HXPush string
	Active bool
}

type uiProjectPriorityFilterItem struct {
	Priority model.IssuePriority
	Label    string
	Href     string
	HXGet    string
	HXPush   string
	Active   bool
}

type uiProjectSortOptionItem struct {
	Label  string
	Icon   string
	Href   string
	HXGet  string
	HXPush string
	Active bool
}

type uiIssueControlsData struct {
	StatusFilters        []uiProjectStatusFilterItem
	PriorityFilters      []uiProjectPriorityFilterItem
	TagFilters           []uiTagFilterItem
	ActiveFilterCount    int
	SortOptions          []uiProjectSortOptionItem
	SortLabel            string
	DirectionOptions     []uiProjectSortOptionItem
	DirectionLabel       string
	DirectionIcon        string
	AssigneeFilters      []uiAssigneeFilterItem
	AssigneeFilterActive bool
	ClearAssigneeHref    string
	ClearAssigneeHXGet   string
	ClearAssigneeHXPush  string
}

type uiProjectAllIssuePageData struct {
	Issues    []uiIssueItem
	NextHXGet string
}

type uiIssueCommentItem struct {
	Comment                             model.Comment
	BodyHTML                            template.HTML
	AuthorID                            uuid.UUID
	AuthorUsername                      string
	AuthorName                          string
	AuthorEmail                         string
	AuthorProfileImageThumbnailObjectID *uuid.UUID
	CanEdit                             bool
}

type uiIssueLinkItem struct {
	Link        model.IssueLink
	LinkedIssue model.Issue
	HasIssue    bool
}

type uiProjectContextItem struct {
	Context             model.ProjectContextSummary
	LinkedIssues        []model.Issue
	LinkedIssuesHasMore bool
	LinkIssueInput      string
	LinkIssueError      string
}

type uiProjectContextOption struct {
	Value string
	Label string
}

type uiContextManagerItem struct {
	ID             uuid.UUID
	Ref            string
	Number         int
	Scope          model.ProjectContextScope
	Position       *int64
	Title          string
	ContentType    string
	SourceFilename *string
	UpdatedAt      time.Time
}

type uiContextManagerData struct {
	// permissions is the viewer's access to Project, kept so the project
	// header rendered around the manager matches every other project view.
	permissions store.ProjectPermissions

	CSRFToken          string
	Mode               string
	Action             string
	Project            model.Project
	Issue              model.Issue
	ParentIssue        *model.Issue
	HasIssue           bool
	CanWrite           bool
	OwnerCrumb         bool
	Items              []uiContextManagerItem
	HasMore            bool
	ContextOptions     []uiProjectContextOption
	ActiveContextID    uuid.UUID
	HasActiveContext   bool
	ActiveContext      model.ProjectContext
	ActiveHTML         template.HTML
	Attachments        []model.ContextAttachment
	AttachmentsHasMore bool
	LinkedIssues       []model.Issue
	LinkedIssueCount   int
	ContextInput       string
	ContextTitle       string
	ContextBody        string
	ContextError       string
	ContextCreateError string
	ContextUploadError string
	ContextEditTitle   string
	ContextEditBody    string
	ContextEditError   string
	LinkIssueInput     string
	LinkIssueError     string
}

type uiIssueSprintOption struct {
	Value string
	Label string
}

type uiAutocompleteOption struct {
	Value       string
	Label       string
	Badge       string
	SearchText  string
	TargetValue string
}

type uiOptionDropdownData struct {
	CSRFToken    string
	Action       string
	HXTarget     string
	HXPushURL    string
	CancelHXGet  string
	ToggleLabel  string
	ListLabel    string
	Name         string
	CurrentValue string
	CurrentLabel string
	CurrentClass string
	Error        string
	Options      []uiOptionDropdownOption
}

type uiOptionDropdownOption struct {
	Value string
	Label string
	Class string
}

type uiAutocompleteEditData struct {
	CSRFToken         string
	ID                string
	Label             string
	Action            string
	PanelPath         string
	IssueHref         string
	Name              string
	Value             string
	HiddenName        string
	HiddenValue       string
	TargetName        string
	Placeholder       string
	SaveLabel         string
	CancelLabel       string
	Error             string
	Autofocus         bool
	Collapsible       bool
	OptionsOpen       bool
	InputHXGet        string
	InputHXTrigger    string
	InputHXTarget     string
	InputHXSwap       string
	InputHXInclude    string
	InputHXPushURL    string
	SearchClearTarget string
	OptionsID         string
	EmptyLabel        string
	Options           []uiAutocompleteOption
}

type uiModalData struct {
	ID               string
	Title            string
	Description      string
	WidthClass       string
	CancelLabel      string
	CancelHXGet      string
	CancelHXPushURL  string
	Badges           []uiModalBadge
	ClientControlled bool
	Open             bool
}

type uiModalBadge struct {
	Label string
	Class string
}

type uiIssueDeleteNotice struct {
	CSRFToken string
	Issue     model.Issue
	CanWrite  bool
}

type uiTabBarData struct {
	Label string
	Items []uiTabItem
}

type uiTabItem struct {
	Label          string
	Icon           string
	Href           string
	HXGet          string
	HXTarget       string
	HXPushURL      string
	HXSelect       string
	HXSelectOOB    string
	HXSwap         string
	Active         bool
	MobileOverflow bool
}

type uiWorkPanelData struct {
	View           string
	Title          string
	Subtitle       string
	IssueListLabel string
	Issues         []uiIssueItem
	Columns        []uiIssueColumn
	HasMore        bool
	ProjectCount   int
	WorkTabs       uiTabBarData
	IssueControls  uiIssueControlsData
}

type uiProjectPanelData struct {
	CSRFToken                       string
	Project                         model.Project
	View                            string
	Anonymous                       bool
	CanWrite                        bool
	CanCreateIssues                 bool
	PublicIssueCreationEnabled      bool
	CanManageMembers                bool
	CanDeleteProject                bool
	DeleteProject                   bool
	DeleteProjectInput              string
	DeleteProjectError              string
	GitHubConfigured                bool
	GitHubConnections               []model.GitHubConnection
	GitHubRepositoryInput           string
	GitHubConnectionError           string
	OwnerCrumb                      bool
	MembersPage                     bool
	Members                         []model.ProjectMember
	AccessSettings                  model.ProjectAccessSettings
	AccessError                     string
	BlockedUsers                    []model.ProjectUserBlock
	BlockInput                      string
	BlockError                      string
	MemberCandidates                []model.ProjectMemberCandidate
	MemberInput                     string
	MemberRoleInput                 model.ProjectMemberRole
	MemberError                     string
	Favorite                        bool
	ProjectTabs                     uiTabBarData
	EditProjectName                 bool
	ProjectNameInput                string
	ProjectNameError                string
	EditProjectDescription          bool
	ProjectDescriptionInput         string
	ProjectDescriptionError         string
	ProjectDescriptionHTML          template.HTML
	ProjectAttachments              []model.ProjectAttachment
	ProjectAttachmentsHasMore       bool
	AssigneeFilters                 []uiAssigneeFilterItem
	AssigneeFilterActive            bool
	ClearAssigneeHref               string
	ClearAssigneeHXGet              string
	ClearAssigneeHXPush             string
	ActiveSprint                    *model.Sprint
	ActiveSprintDescription         uiSprintDescriptionData
	ActiveSprintAction              string
	ActiveSprintForm                uiSprintFormData
	ActiveSprintIssueForm           uiSprintIssueFormData
	SprintColumns                   []uiIssueColumn
	SprintControls                  uiIssueControlsData
	PlannedSprints                  []uiPlannedSprint
	NewSprint                       bool
	NewSprintForm                   uiSprintFormData
	PlannedSprintActionID           uuid.UUID
	PlannedSprintAction             string
	PlannedSprintForm               uiSprintFormData
	PlannedSprintIssueForm          uiSprintIssueFormData
	PlannedSprintAttachments        []model.SprintAttachment
	PlannedSprintAttachmentsHasMore bool
	AllIssues                       []uiIssueItem
	AllIssuePage                    uiProjectAllIssuePageData
	AllControls                     uiIssueControlsData
	SprintHistoryPage               uiProjectSprintHistoryPageData
	ChangelogPage                   uiProjectChangelogPageData
	ProjectStats                    model.ProjectStats
	CompletionChart                 uiProjectCompletionChartData
	Tags                            []model.IssueTag
	ContextItems                    []uiProjectContextItem
	ContextHasMore                  bool
	ContextManager                  *uiContextManagerData
	DeleteNotice                    *uiIssueDeleteNotice
	SprintIssuesHasMore             bool
	PlannedHasMore                  bool
}

type uiProjectCompletionChartPoint struct {
	X          int
	Y          int
	WeekLabel  string
	AsOfLabel  string
	RateLabel  string
	Total      int
	Completed  int
	HasTickets bool
}

type uiProjectCompletionChartSegment struct {
	Points string
}

type uiProjectCompletionChartData struct {
	Points     []uiProjectCompletionChartPoint
	Segments   []uiProjectCompletionChartSegment
	HasData    bool
	HasTrend   bool
	RangeLabel string
	FirstLabel string
	LastLabel  string
}

const uiIssueListDefaultSort = store.ListIssuesSortUpdated
const uiProjectAllDefaultSort = uiIssueListDefaultSort

// uiOpenIssueStatuses is what a flat issue list shows when the URL asks for
// nothing: work that is still open. A list of everything ever filed is a
// worse answer to "what is on this project" than a list of what is left.
// Done and Cancelled stay one click away behind the Any filter.
func uiOpenIssueStatuses() []model.Status {
	return []model.Status{model.StatusTodo, model.StatusInProgress}
}

type uiIssueListQuery struct {
	Statuses []model.Status
	// AnyStatus is the URL explicitly asking for every status, which is not the
	// same as asking for nothing: nothing means the open-work default.
	AnyStatus   bool
	Priorities  []model.IssuePriority
	TagNames    []string
	Sort        store.ListIssuesSort
	Direction   store.ListIssuesSortDirection
	AssigneeIDs []uuid.UUID
	Cursor      string
}

type uiProjectAllQuery = uiIssueListQuery

type uiProjectSprintHistoryPageData struct {
	Project      model.Project
	Sprints      []model.Sprint
	Descriptions map[uuid.UUID]uiSprintDescriptionData
	StatusCounts map[uuid.UUID]model.ProjectIssueStatusCounts
	HasMore      bool
	NextCursor   string
}

type uiProjectSprintHistoryIssuePageData struct {
	Project   model.Project
	Sprint    model.Sprint
	Issues    []uiIssueItem
	NextHXGet string
}

type uiProjectChangelogPageData struct {
	Project    model.Project
	Entries    []model.ProjectChangelogEntry
	HasMore    bool
	NextCursor string
}

type uiDeletedIssuesPanelData struct {
	CSRFToken string
	Project   model.Project
	Issues    []model.Issue
	HasMore   bool
	CanWrite  bool
}

type uiDeletedIssuePanelData struct {
	CSRFToken string
	Issue     model.Issue
	Project   model.Project
	CanWrite  bool
	BackHref  string
	BackHXGet string
}

type uiIssuePanelData struct {
	CSRFToken          string
	Issue              model.Issue
	Project            model.Project
	CanWrite           bool
	GitHubConfigured   bool
	GitHubConnections  []model.GitHubConnection
	GitHubLinks        []uiGitHubIssueLink
	GitHubConnectionID string
	GitHubReference    string
	GitHubError        string
	OwnerCrumb         bool
	ParentIssue        *model.Issue
	Sprint             *model.Sprint
	Assignee           *model.User
	Reporter           *model.User
	EditTitle          bool
	EditDescription    bool
	EditStatus         bool
	PendingCloseReason bool
	EditCloseReason    bool
	EditPriority       bool
	EditDueDate        bool
	EditAssignee       bool
	EditReporter       bool
	EditSprint         bool
	CanEditSprint      bool
	DescriptionHTML    template.HTML
	Attachments        []model.IssueAttachment
	AttachmentsHasMore bool
	TitleInput         string
	AssigneeInput      string
	ReporterInput      string
	SprintInput        string
	DueDateInput       string
	CloseReasonInput   string
	AssigneeError      string
	ReporterError      string
	SprintError        string
	TitleError         string
	DueDateError       string
	CloseReasonError   string
	MemberOptions      []model.User
	SprintOptions      []uiIssueSprintOption
	SubIssues          []model.Issue
	SubIssuesHasMore   bool
	AddSubIssue        bool
	SubIssueTitle      string
	SubIssueError      string
	Comments           []uiIssueCommentItem
	CommentsHasMore    bool
	CommentBody        string
	CommentError       string
	EditCommentID      uuid.UUID
	CommentEditBody    string
	CommentEditError   string
	Links              []uiIssueLinkItem
	LinksHasMore       bool
	AddLink            bool
	EditLinkID         uuid.UUID
	LinkTarget         string
	LinkRelation       string
	LinkError          string
	Contexts           []model.ProjectContext
	ContextsHasMore    bool
	EditTags           bool
	TagModalAttached   []model.IssueTag
	TagModalAvailable  []model.IssueTag
	TagInput           string
	TagError           string
	BackHref           string
	BackHXGet          string
	BackLabel          string
	DeleteNotice       *uiIssueDeleteNotice
}

type uiGitHubIssueLink struct {
	Link          model.GitHubIssueLink
	Stale         bool
	LastRefreshed uiLocalTimeData
}

type uiTagManagerData struct {
	CSRFToken   string
	Mode        string
	Project     model.Project
	Issue       model.Issue
	HasIssue    bool
	CanWrite    bool
	Tags        []model.IssueTag
	Available   []model.IssueTag
	BackHref    string
	BackHXGet   string
	BackLabel   string
	NameInput   string
	ColorInput  model.IssueTagColor
	TagError    string
	EditTagID   uuid.UUID
	EditName    string
	EditColor   model.IssueTagColor
	EditError   string
	AttachInput string
	AttachError string
}

type uiProjectsPanelData struct {
	Projects []model.Project
	HasMore  bool
	Owner    *model.User
	// Owners holds the public profile of every listed project's owner, keyed
	// by user ID, so each row can say whose project it is.
	Owners map[uuid.UUID]model.User
}

const uiProjectOwnerAvatarClass = "grid h-6 w-6 shrink-0 place-items-center border border-slate-300 bg-white text-[10px] font-semibold text-slate-700 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200"

// OwnerAvatar is the avatar on the trailing edge of a project row. An owner
// missing from Owners falls back to the username the project already carries.
func (p *uiProjectsPanelData) OwnerAvatar(project model.Project) uiUserAvatarData {
	owner, ok := p.Owners[project.OwnerID]
	if !ok {
		return uiUserAvatarFields(project.OwnerID, "", project.OwnerUsername, "", nil, uiProjectOwnerAvatarClass)
	}
	return uiUserAvatarFields(owner.ID, owner.Name, owner.Username, "", owner.ProfileImageThumbnailObjectID, uiProjectOwnerAvatarClass)
}

type uiNewProjectPanelData struct {
	CSRFToken   string
	Error       string
	Key         string
	Name        string
	Description string
}

type uiNewIssuePanelData struct {
	CSRFToken         string
	Project           model.Project
	HasProject        bool
	ProjectScoped     bool
	PublicSubmission  bool
	ProjectID         string
	ProjectInput      string
	ProjectSearchOpen bool
	ProjectOptions    []model.Project
	Error             string
	Title             string
	Description       string
	Priority          string
	DueDate           string
	AssigneeInput     string
	ReporterInput     string
	MemberOptions     []model.User
	BackHref          string
	BackHXGet         string
}

type uiTokenPanelData struct {
	CSRFToken string
	// Tokens holds API tokens only. Web sessions are numerous, short-lived, and
	// their names carry no information, so listing them one by one buried the
	// tokens people actually manage.
	Tokens         []model.AuthToken
	ActiveSessions int
	Error          string
	Created        string
	// ConnectedApps counts live access tokens issued to OAuth clients. They are
	// minted and retired on their own schedule as connectors refresh, so a row
	// each would churn beneath the tokens people actually manage.
	ConnectedApps       int
	OAuthClients        []model.OAuthClient
	OAuthError          string
	CreatedClientID     string
	CreatedClientSecret string
}

// uiOAuthConsentData backs the screen that asks a user to approve a connector.
// It is rendered standalone rather than inside the app shell: this is an
// interstitial shown on behalf of a third party, not a page of the product.
type uiOAuthConsentData struct {
	CSRFToken     string
	User          model.User
	ClientName    string
	ClientID      string
	RedirectURI   string
	RedirectHost  string
	State         string
	Scope         string
	Resource      string
	CodeChallenge string
}

type uiOAuthErrorData struct {
	Title   string
	Message string
}

type uiSettingsPanelData struct {
	CSRFToken       string
	User            model.User
	ProfileError    string
	ProfileSaved    bool
	PasswordError   string
	PasswordChanged bool
	PasswordLogin   model.PasswordLoginState
	Passkeys        []model.PasskeyCredential
	PushEnabled     bool
	PushPublicKey   string
	PushPreferences model.PushNotificationPreferences
	PushDeviceCount int
}
