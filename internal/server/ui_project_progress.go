package server

import (
	"context"
	"net/url"

	"github.com/bradleymackey/track-slash/internal/model"
)

// uiProjectProgressData backs the In progress view, which takes the place of
// Planned when a project has sprints turned off. The panel's ProgressNotice
// explains an action that landed here instead of doing what was asked, such as
// starting a sprint after sprints were turned off.
type uiProjectProgressData struct {
	InProgress               []uiIssueItem
	InProgressHasMore        bool
	RecentlyCompleted        []uiIssueItem
	RecentlyCompletedHasMore bool
	Window                   model.CompletionWindow
	WindowOptions            []uiRangeOption
}

func uiProjectProgressPath(project model.Project, window model.CompletionWindow, panel bool) string {
	path := uiProjectPath(project) + "/progress"
	if panel {
		path += "/panel"
	}
	if window != "" && window != model.DefaultCompletionWindow {
		path += "?" + url.Values{"completed_within": {string(window)}}.Encode()
	}
	return path
}

func (s *Server) uiBuildProjectProgress(ctx context.Context, project model.Project, progress model.ProjectProgress) (*uiProjectProgressData, error) {
	inProgress, err := s.uiIssueItemsWithSubIssueProgress(ctx, progress.InProgress, project, nil)
	if err != nil {
		return nil, err
	}
	completedIssues := make([]model.Issue, 0, len(progress.RecentlyCompleted))
	for _, completed := range progress.RecentlyCompleted {
		completedIssues = append(completedIssues, completed.Issue)
	}
	completed, err := s.uiIssueItemsWithSubIssueProgress(ctx, completedIssues, project, nil)
	if err != nil {
		return nil, err
	}
	for i := range completed {
		completedAt := progress.RecentlyCompleted[i].CompletedAt
		completed[i].CompletedAt = &completedAt
	}
	data := &uiProjectProgressData{
		InProgress:               inProgress,
		InProgressHasMore:        progress.InProgressHasMore,
		RecentlyCompleted:        completed,
		RecentlyCompletedHasMore: progress.RecentlyCompletedHasMore,
		Window:                   progress.CompletedWithin,
	}
	for _, window := range model.CompletionWindows() {
		data.WindowOptions = append(data.WindowOptions, uiRangeOption{
			Label:  window.Label(),
			Href:   uiProjectProgressPath(project, window, false),
			HXGet:  uiProjectProgressPath(project, window, true),
			Active: window == progress.CompletedWithin,
		})
	}
	return data, nil
}
