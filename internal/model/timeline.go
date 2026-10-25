package model

import "time"

// ProjectTimeline lays a project's top-level issues out in time, one bar each,
// as the rows of a Gantt chart. With sprints on, the rows are grouped by
// sprint: completed sprints in range, oldest first, then the active sprint,
// then planned sprints in their planned order, then the work in no sprint.
// With sprints off there is one group, of every issue that has a schedule.
type ProjectTimeline struct {
	Range InsightRange `json:"range"`
	// Since is the first day of the range: completed work and completed
	// sprints count only from it. It is null for all time.
	Since *Date `json:"since"`
	// From and To are the first and last days the chart covers. They always
	// include Today, the day the timeline was built, in UTC.
	From           Date            `json:"from"`
	To             Date            `json:"to"`
	Today          Date            `json:"today"`
	SprintsEnabled bool            `json:"sprints_enabled"`
	Groups         []TimelineGroup `json:"groups"`
	// MoreCompletedSprints and MorePlannedSprints say that the range holds
	// more sprints than the timeline lists.
	MoreCompletedSprints bool `json:"more_completed_sprints"`
	MorePlannedSprints   bool `json:"more_planned_sprints"`
	// Unscheduled counts the open To do issues left out because nothing
	// dates them: no due date and no sprint.
	Unscheduled int `json:"unscheduled"`
}

// TimelineGroup is one sprint's issues, or with a nil Sprint, the issues in
// no sprint (all of them, when sprints are off).
type TimelineGroup struct {
	Sprint  *Sprint        `json:"sprint"`
	Items   []TimelineItem `json:"items"`
	HasMore bool           `json:"has_more"`
}

// TimelineItem is an issue and its bar. StartedAt is its first move to In
// progress and CompletedAt its last move to Done or Closed while it is
// completed. Start and End are the bar's first and last days; both are null
// for an issue nothing dates, which only a planned sprint without dates holds.
type TimelineItem struct {
	Issue
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Start       *Date      `json:"start"`
	End         *Date      `json:"end"`
}

// TimelineRangeDays is how many days back a range reaches; zero for all time
// or an unknown range.
func TimelineRangeDays(r InsightRange) int {
	switch r {
	case InsightRangeTwoWeeks:
		return 14
	case InsightRangeThirtyDays:
		return 30
	case InsightRangeNinetyDays:
		return 90
	default:
		return 0
	}
}

// TimelineSpan works out an issue's bar. It starts when work started, or else
// when its sprint starts, or else the day the issue was filed. A completed
// issue's bar ends when it was completed. An open issue's ends at its due
// date, or else its sprint's end, and an issue in progress runs to today at
// least. An open To do issue with neither has no bar, and ok is false. A bar
// whose end comes before its start, such as a due date set before work began,
// is drawn on its end day alone. sprint is nil for an issue in no sprint, and
// for every issue while sprints are off.
func TimelineSpan(issue Issue, sprint *Sprint, startedAt, completedAt *time.Time, today Date) (start, end Date, ok bool) {
	switch {
	case startedAt != nil:
		start = DateFromTime(*startedAt)
	case sprint != nil && sprint.StartDate != nil:
		start = DateFromTime(*sprint.StartDate)
	default:
		start = DateFromTime(issue.CreatedAt)
	}
	switch {
	case completedAt != nil:
		end, ok = DateFromTime(*completedAt), true
	case issue.DueDate != nil:
		end, ok = *issue.DueDate, true
	case sprint != nil && sprint.EndDate != nil:
		end, ok = DateFromTime(*sprint.EndDate), true
	}
	if completedAt == nil && issue.Status == StatusInProgress && (!ok || end.Before(today)) {
		end, ok = today, true
	}
	if !ok {
		return Date{}, Date{}, false
	}
	if end.Before(start) {
		start = end
	}
	return start, end, true
}

// TimelineFrame picks the days a timeline covers. It starts on since, or for
// all time on the earliest day any bar or sprint reaches, and ends on the
// latest. It always includes today, with a week to spare after it so today's
// line never sits on the edge, and a bar outside a bounded range is cut off
// at its start.
func TimelineFrame(since *Date, today Date, groups []TimelineGroup) (from, to Date) {
	from, to = today, today.AddDays(7)
	if since != nil {
		from = *since
	}
	widen := func(start, end Date) {
		if since == nil && start.Before(from) {
			from = start
		}
		if to.Before(end) {
			to = end
		}
	}
	for _, group := range groups {
		if sprint := group.Sprint; sprint != nil && sprint.StartDate != nil && sprint.EndDate != nil {
			widen(DateFromTime(*sprint.StartDate), DateFromTime(*sprint.EndDate))
		}
		for _, item := range group.Items {
			if item.Start != nil {
				widen(*item.Start, *item.End)
			}
		}
	}
	return from, to
}
