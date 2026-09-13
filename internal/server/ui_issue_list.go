package server

import (
	"strings"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func uiProjectTabs(project model.Project, view string, assigneeIDs []uuid.UUID) uiTabBarData {
	const (
		projectPanelTarget    = "#project-panel-tab-content"
		projectPanelSelectOOB = "#project-breadcrumb,#project-tab-bar,#project-actions-menu,#project-favorite-action"
	)
	var sprintAssigneeIDs []uuid.UUID
	if view == "sprint" {
		sprintAssigneeIDs = assigneeIDs
	}
	return uiTabBarData{
		Label: "Project views",
		Items: []uiTabItem{
			{
				Label:       "Sprint",
				Icon:        "person-standing",
				Href:        uiProjectViewPath(project, "sprint", sprintAssigneeIDs),
				HXGet:       uiProjectPanelPath(project, "sprint", sprintAssigneeIDs),
				HXTarget:    projectPanelTarget,
				HXPushURL:   uiProjectViewPath(project, "sprint", sprintAssigneeIDs),
				HXSelect:    projectPanelTarget,
				HXSelectOOB: projectPanelSelectOOB,
				HXSwap:      "outerHTML",
				Active:      view == "sprint",
			},
			{
				Label:       "Planned",
				Icon:        "calendar-range",
				Href:        uiProjectViewPath(project, "planned"),
				HXGet:       uiProjectPanelPath(project, "planned"),
				HXTarget:    projectPanelTarget,
				HXPushURL:   uiProjectViewPath(project, "planned"),
				HXSelect:    projectPanelTarget,
				HXSelectOOB: projectPanelSelectOOB,
				HXSwap:      "outerHTML",
				Active:      view == "planned",
			},
			{
				Label:       "All",
				Icon:        "list-filter",
				Href:        uiProjectViewPath(project, "all"),
				HXGet:       uiProjectPanelPath(project, "all"),
				HXTarget:    projectPanelTarget,
				HXPushURL:   uiProjectViewPath(project, "all"),
				HXSelect:    projectPanelTarget,
				HXSelectOOB: projectPanelSelectOOB,
				HXSwap:      "outerHTML",
				Active:      view == "all",
			},
			{
				Label:          "Context",
				Icon:           "book-open",
				Href:           uiProjectViewPath(project, "context"),
				HXGet:          uiProjectPanelPath(project, "context"),
				HXTarget:       projectPanelTarget,
				HXPushURL:      uiProjectViewPath(project, "context"),
				HXSelect:       projectPanelTarget,
				HXSelectOOB:    projectPanelSelectOOB,
				HXSwap:         "outerHTML",
				Active:         view == "context",
				MobileOverflow: true,
			},
			{
				Label:          "About",
				Icon:           "info",
				Href:           uiProjectViewPath(project, "about"),
				HXGet:          uiProjectPanelPath(project, "about"),
				HXTarget:       projectPanelTarget,
				HXPushURL:      uiProjectViewPath(project, "about"),
				HXSelect:       projectPanelTarget,
				HXSelectOOB:    projectPanelSelectOOB,
				HXSwap:         "outerHTML",
				Active:         view == "about",
				MobileOverflow: true,
			},
		},
	}
}

func uiWorkTabs(view string, query uiIssueListQuery) uiTabBarData {
	return uiTabBarData{
		Label: "Me views",
		Items: []uiTabItem{
			{
				Label:     "Active Sprints",
				Icon:      "person-standing",
				Href:      uiWorkViewPath("active", query),
				HXGet:     uiWorkPanelPath("active", query),
				HXTarget:  "#main",
				HXPushURL: uiWorkViewPath("active", query),
				Active:    view == "active",
			},
			{
				Label:     "All",
				Icon:      "list-filter",
				Href:      uiWorkViewPath("all", query),
				HXGet:     uiWorkPanelPath("all", query),
				HXTarget:  "#main",
				HXPushURL: uiWorkViewPath("all", query),
				Active:    view == "all",
			},
		},
	}
}

func uiWorkIssueControls(view string, query uiIssueListQuery) uiIssueControlsData {
	paths := uiWorkIssueListPaths(view)
	return uiIssueControls(query, paths)
}

func uiProjectAssigneeFilters(project model.Project, view string, assignees []model.ProjectAssignee, selectedIDs []uuid.UUID) []uiAssigneeFilterItem {
	out := make([]uiAssigneeFilterItem, 0, len(assignees))
	for _, assignee := range assignees {
		nextIDs, selected := uiToggleAssigneeIDs(selectedIDs, assignee.ID)
		out = append(out, uiAssigneeFilterItem{
			Assignee: assignee,
			Selected: selected,
			Href:     uiProjectViewPath(project, view, nextIDs),
			HXGet:    uiProjectPanelPath(project, view, nextIDs),
			HXPush:   uiProjectViewPath(project, view, nextIDs),
		})
	}
	return out
}

func uiProjectAllAssigneeFilters(project model.Project, assignees []model.ProjectAssignee, query uiProjectAllQuery) []uiAssigneeFilterItem {
	out := make([]uiAssigneeFilterItem, 0, len(assignees))
	for _, assignee := range assignees {
		nextQuery := query
		nextIDs, selected := uiToggleAssigneeIDs(query.AssigneeIDs, assignee.ID)
		nextQuery.AssigneeIDs = nextIDs
		out = append(out, uiAssigneeFilterItem{
			Assignee: assignee,
			Selected: selected,
			Href:     uiProjectAllViewPath(project, nextQuery),
			HXGet:    uiProjectAllPanelPath(project, nextQuery),
			HXPush:   uiProjectAllViewPath(project, nextQuery),
		})
	}
	return out
}

func uiProjectSprintAssigneeFilters(project model.Project, assignees []model.ProjectAssignee, query uiIssueListQuery) []uiAssigneeFilterItem {
	out := make([]uiAssigneeFilterItem, 0, len(assignees))
	for _, assignee := range assignees {
		nextQuery := query
		nextIDs, selected := uiToggleAssigneeIDs(query.AssigneeIDs, assignee.ID)
		nextQuery.AssigneeIDs = nextIDs
		out = append(out, uiAssigneeFilterItem{
			Assignee: assignee,
			Selected: selected,
			Href:     uiProjectSprintViewPath(project, nextQuery),
			HXGet:    uiProjectSprintPanelPath(project, nextQuery),
			HXPush:   uiProjectSprintViewPath(project, nextQuery),
		})
	}
	return out
}

func uiProjectAssigneeMap(assignees []model.ProjectAssignee) map[uuid.UUID]model.ProjectAssignee {
	out := make(map[uuid.UUID]model.ProjectAssignee, len(assignees))
	for _, assignee := range assignees {
		out[assignee.ID] = assignee
	}
	return out
}

func uiIssueItemAssignee(issue model.Issue, assignees map[uuid.UUID]model.ProjectAssignee) *model.ProjectAssignee {
	if issue.AssigneeID == nil {
		return nil
	}
	assignee, ok := assignees[*issue.AssigneeID]
	if !ok {
		return nil
	}
	return &assignee
}

type uiIssueListPaths func(uiIssueListQuery) (href, hxGet, hxPush string)

func uiProjectAllIssueListPaths(project model.Project) uiIssueListPaths {
	return func(query uiIssueListQuery) (string, string, string) {
		href := uiProjectAllViewPath(project, query)
		return href, uiProjectAllPanelPath(project, query), href
	}
}

func uiProjectSprintIssueListPaths(project model.Project) uiIssueListPaths {
	return func(query uiIssueListQuery) (string, string, string) {
		href := uiProjectSprintViewPath(project, query)
		return href, uiProjectSprintPanelPath(project, query), href
	}
}

func uiWorkIssueListPaths(view string) uiIssueListPaths {
	return func(query uiIssueListQuery) (string, string, string) {
		href := uiWorkViewPath(view, query)
		return href, uiWorkPanelPath(view, query), href
	}
}

func uiIssueControls(query uiIssueListQuery, paths uiIssueListPaths) uiIssueControlsData {
	return uiIssueControlsWithAssignees(query, paths, nil, nil, false, "", "", "")
}

func uiProjectSprintIssueControls(project model.Project, query uiIssueListQuery, tags []model.IssueTag, assignees []uiAssigneeFilterItem, assigneeActive bool, clearHref, clearHXGet, clearHXPush string) uiIssueControlsData {
	return uiIssueControlsWithAssignees(query, uiProjectSprintIssueListPaths(project), tags, assignees, assigneeActive, clearHref, clearHXGet, clearHXPush)
}

func uiProjectAllIssueControls(project model.Project, query uiProjectAllQuery, tags []model.IssueTag, assignees []uiAssigneeFilterItem, assigneeActive bool, clearHref, clearHXGet, clearHXPush string) uiIssueControlsData {
	return uiIssueControlsWithAssignees(query, uiProjectAllIssueListPaths(project), tags, assignees, assigneeActive, clearHref, clearHXGet, clearHXPush)
}

func uiIssueControlsWithAssignees(query uiIssueListQuery, paths uiIssueListPaths, tags []model.IssueTag, assignees []uiAssigneeFilterItem, assigneeActive bool, clearHref, clearHXGet, clearHXPush string) uiIssueControlsData {
	return uiIssueControlsData{
		StatusFilters:        uiIssueStatusFilters(query, paths),
		PriorityFilters:      uiIssuePriorityFilters(query, paths),
		TagFilters:           uiIssueTagFilters(query, paths, tags),
		ActiveFilterCount:    uiActiveIssueFilterCount(query),
		SortOptions:          uiIssueSortOptions(query, paths),
		SortLabel:            uiIssueSortLabel(uiIssueListSort(query)),
		DirectionOptions:     uiIssueSortDirectionOptions(query, paths),
		DirectionLabel:       uiIssueSortDirectionLabel(uiIssueListDirection(query)),
		DirectionIcon:        uiIssueSortDirectionIcon(uiIssueListDirection(query)),
		AssigneeFilters:      assignees,
		AssigneeFilterActive: assigneeActive,
		ClearAssigneeHref:    clearHref,
		ClearAssigneeHXGet:   clearHXGet,
		ClearAssigneeHXPush:  clearHXPush,
	}
}

func uiActiveIssueFilterCount(query uiIssueListQuery) int {
	return len(query.Statuses) + len(query.Priorities) + len(query.TagNames) + len(query.AssigneeIDs)
}

func uiProjectAllStatusFilters(project model.Project, query uiProjectAllQuery) []uiProjectStatusFilterItem {
	return uiIssueStatusFilters(query, uiProjectAllIssueListPaths(project))
}

func uiIssueStatusFilters(query uiIssueListQuery, paths uiIssueListPaths) []uiProjectStatusFilterItem {
	options := []struct {
		Label  string
		Status model.Status
	}{
		{Label: "Any", Status: ""},
		{Label: uiStatusLabel(model.StatusTodo), Status: model.StatusTodo},
		{Label: uiStatusLabel(model.StatusInProgress), Status: model.StatusInProgress},
		{Label: uiStatusLabel(model.StatusDone), Status: model.StatusDone},
		{Label: uiStatusLabel(model.StatusClosed), Status: model.StatusClosed},
	}
	out := make([]uiProjectStatusFilterItem, 0, len(options))
	for _, option := range options {
		nextQuery := query
		active := false
		if option.Status == "" {
			nextQuery.Statuses = nil
			nextQuery.AnyStatus = true
			active = query.AnyStatus
		} else {
			var selected bool
			nextQuery.Statuses, selected = uiToggleStatuses(query.Statuses, option.Status)
			// Unticking the last status reads as "stop filtering by status", not
			// as "fall back to the default I just unticked".
			nextQuery.AnyStatus = len(nextQuery.Statuses) == 0
			active = selected
		}
		nextQuery.Cursor = ""
		href, hxGet, hxPush := paths(nextQuery)
		out = append(out, uiProjectStatusFilterItem{
			Label:  option.Label,
			Href:   href,
			HXGet:  hxGet,
			HXPush: hxPush,
			Active: active,
		})
	}
	return out
}

func uiProjectAllPriorityFilters(project model.Project, query uiProjectAllQuery) []uiProjectPriorityFilterItem {
	return uiIssuePriorityFilters(query, uiProjectAllIssueListPaths(project))
}

func uiIssuePriorityFilters(query uiIssueListQuery, paths uiIssueListPaths) []uiProjectPriorityFilterItem {
	options := []struct {
		Label    string
		Priority model.IssuePriority
	}{
		{Label: "Any", Priority: ""},
		{Label: uiPriorityLabel(model.PriorityP0), Priority: model.PriorityP0},
		{Label: uiPriorityLabel(model.PriorityP1), Priority: model.PriorityP1},
		{Label: uiPriorityLabel(model.PriorityP2), Priority: model.PriorityP2},
		{Label: uiPriorityLabel(model.PriorityP3), Priority: model.PriorityP3},
		{Label: uiPriorityLabel(model.PriorityP4), Priority: model.PriorityP4},
	}
	out := make([]uiProjectPriorityFilterItem, 0, len(options))
	for _, option := range options {
		nextQuery := query
		active := false
		if option.Priority == "" {
			nextQuery.Priorities = nil
			active = len(query.Priorities) == 0
		} else {
			var selected bool
			nextQuery.Priorities, selected = uiTogglePriorities(query.Priorities, option.Priority)
			active = selected
		}
		nextQuery.Cursor = ""
		href, hxGet, hxPush := paths(nextQuery)
		out = append(out, uiProjectPriorityFilterItem{
			Priority: option.Priority,
			Label:    option.Label,
			Href:     href,
			HXGet:    hxGet,
			HXPush:   hxPush,
			Active:   active,
		})
	}
	return out
}

func uiIssueTagFilters(query uiIssueListQuery, paths uiIssueListPaths, tags []model.IssueTag) []uiTagFilterItem {
	if len(tags) == 0 {
		return nil
	}
	out := make([]uiTagFilterItem, 0, len(tags)+1)
	anyQuery := query
	anyQuery.TagNames = nil
	anyQuery.Cursor = ""
	href, hxGet, hxPush := paths(anyQuery)
	out = append(out, uiTagFilterItem{
		Label:    "Any",
		Selected: len(query.TagNames) == 0,
		Href:     href,
		HXGet:    hxGet,
		HXPush:   hxPush,
	})
	for _, tag := range tags {
		if strings.TrimSpace(tag.Name) == "" {
			continue
		}
		nextQuery := query
		nextNames, selected := uiToggleTagNames(query.TagNames, tag.Name)
		nextQuery.TagNames = nextNames
		nextQuery.Cursor = ""
		href, hxGet, hxPush := paths(nextQuery)
		out = append(out, uiTagFilterItem{
			Tag:      tag,
			Label:    tag.DisplayName,
			Selected: selected,
			Href:     href,
			HXGet:    hxGet,
			HXPush:   hxPush,
		})
	}
	return out
}

func uiProjectAllSortOptions(project model.Project, query uiProjectAllQuery) []uiProjectSortOptionItem {
	return uiIssueSortOptions(query, uiProjectAllIssueListPaths(project))
}

func uiIssueSortOptions(query uiIssueListQuery, paths uiIssueListPaths) []uiProjectSortOptionItem {
	currentSort := uiIssueListSort(query)
	options := []struct {
		Label string
		Sort  store.ListIssuesSort
	}{
		{Label: "Updated", Sort: store.ListIssuesSortUpdated},
		{Label: "Created", Sort: store.ListIssuesSortCreated},
		{Label: "Status", Sort: store.ListIssuesSortStatus},
		{Label: "Priority", Sort: store.ListIssuesSortPriority},
		{Label: "Due date", Sort: store.ListIssuesSortDueDate},
	}
	out := make([]uiProjectSortOptionItem, 0, len(options))
	for _, option := range options {
		nextQuery := query
		nextQuery.Sort = option.Sort
		nextQuery.Direction = uiDefaultIssueSortDirection(option.Sort)
		nextQuery.Cursor = ""
		href, hxGet, hxPush := paths(nextQuery)
		out = append(out, uiProjectSortOptionItem{
			Label:  option.Label,
			Href:   href,
			HXGet:  hxGet,
			HXPush: hxPush,
			Active: currentSort == option.Sort,
		})
	}
	return out
}

func uiIssueSortDirectionOptions(query uiIssueListQuery, paths uiIssueListPaths) []uiProjectSortOptionItem {
	currentDirection := uiIssueListDirection(query)
	options := []struct {
		Label     string
		Icon      string
		Direction store.ListIssuesSortDirection
	}{
		{Label: "Asc", Icon: "arrow-up", Direction: store.ListIssuesSortAscending},
		{Label: "Desc", Icon: "arrow-down", Direction: store.ListIssuesSortDescending},
	}
	out := make([]uiProjectSortOptionItem, 0, len(options))
	for _, option := range options {
		nextQuery := query
		nextQuery.Direction = option.Direction
		nextQuery.Cursor = ""
		href, hxGet, hxPush := paths(nextQuery)
		out = append(out, uiProjectSortOptionItem{
			Label:  option.Label,
			Icon:   option.Icon,
			Href:   href,
			HXGet:  hxGet,
			HXPush: hxPush,
			Active: currentDirection == option.Direction,
		})
	}
	return out
}

func uiIssueListSort(query uiIssueListQuery) store.ListIssuesSort {
	if query.Sort == "" {
		return uiIssueListDefaultSort
	}
	return query.Sort
}

func uiIssueListDirection(query uiIssueListQuery) store.ListIssuesSortDirection {
	if query.Direction != "" {
		return query.Direction
	}
	return uiDefaultIssueSortDirection(uiIssueListSort(query))
}

func uiDefaultIssueSortDirection(sort store.ListIssuesSort) store.ListIssuesSortDirection {
	return defaultIssueListSortDirection(sort)
}

func uiIssueSortLabel(sort store.ListIssuesSort) string {
	switch sort {
	case store.ListIssuesSortCreated:
		return "Created"
	case store.ListIssuesSortStatus:
		return "Status"
	case store.ListIssuesSortPriority:
		return "Priority"
	case store.ListIssuesSortDueDate:
		return "Due date"
	default:
		return "Updated"
	}
}

func uiIssueSortDirectionLabel(direction store.ListIssuesSortDirection) string {
	if direction == store.ListIssuesSortAscending {
		return "Asc"
	}
	return "Desc"
}

func uiIssueSortDirectionIcon(direction store.ListIssuesSortDirection) string {
	if direction == store.ListIssuesSortAscending {
		return "arrow-up"
	}
	return "arrow-down"
}
