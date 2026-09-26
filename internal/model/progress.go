package model

import "time"

// CompletionWindow is how far back a project's progress view looks for
// recently completed issues.
type CompletionWindow string

const (
	CompletionWindowDay      CompletionWindow = "1d"
	CompletionWindowWeek     CompletionWindow = "7d"
	CompletionWindowTwoWeeks CompletionWindow = "14d"
	CompletionWindowMonth    CompletionWindow = "30d"

	// DefaultCompletionWindow is used when a caller does not name a window.
	DefaultCompletionWindow = CompletionWindowWeek
)

// CompletionWindows lists the windows in the order controls should offer them.
func CompletionWindows() []CompletionWindow {
	return []CompletionWindow{CompletionWindowDay, CompletionWindowWeek, CompletionWindowTwoWeeks, CompletionWindowMonth}
}

func (w CompletionWindow) Valid() bool {
	switch w {
	case CompletionWindowDay, CompletionWindowWeek, CompletionWindowTwoWeeks, CompletionWindowMonth:
		return true
	}
	return false
}

func (w CompletionWindow) Label() string {
	switch w {
	case CompletionWindowDay:
		return "24 hours"
	case CompletionWindowWeek:
		return "7 days"
	case CompletionWindowTwoWeeks:
		return "14 days"
	case CompletionWindowMonth:
		return "30 days"
	default:
		return string(w)
	}
}

// Duration is how far back the window reaches; zero for an unknown window.
func (w CompletionWindow) Duration() time.Duration {
	switch w {
	case CompletionWindowDay:
		return 24 * time.Hour
	case CompletionWindowWeek:
		return 7 * 24 * time.Hour
	case CompletionWindowTwoWeeks:
		return 14 * 24 * time.Hour
	case CompletionWindowMonth:
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}

// CompletedIssue is a Done issue with the time it last moved to Done.
type CompletedIssue struct {
	Issue
	CompletedAt time.Time `json:"completed_at"`
}

// ProjectProgress is what a project is working on now: its top-level issues in
// progress, and those completed within the window, most recent first.
type ProjectProgress struct {
	CompletedWithin          CompletionWindow `json:"completed_within"`
	CompletedSince           time.Time        `json:"completed_since"`
	InProgress               []Issue          `json:"in_progress"`
	InProgressHasMore        bool             `json:"in_progress_has_more"`
	RecentlyCompleted        []CompletedIssue `json:"recently_completed"`
	RecentlyCompletedHasMore bool             `json:"recently_completed_has_more"`
}
