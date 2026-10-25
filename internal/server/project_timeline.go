package server

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// A timeline lists at most this many completed sprints, the most recent in
// range, and this many planned sprints, the next in planned order.
const (
	timelineCompletedSprintLimit = 12
	timelinePlannedSprintLimit   = 12
)

// parseTimelineRange reads the range shared by the HTTP API, MCP, and the
// Timeline view, the same values Insights takes. Empty means the default.
func parseTimelineRange(raw string) (model.InsightRange, error) {
	rng := model.InsightRange(strings.ToLower(strings.TrimSpace(raw)))
	if rng == "" {
		return model.DefaultInsightRange, nil
	}
	if !rng.Valid() {
		return "", errInsightRange
	}
	return rng, nil
}

// projectTimeline lays out a project the caller may read as a Gantt chart:
// each top-level issue as a bar (model.TimelineSpan), grouped by sprint when
// the project runs sprints. includePrivate lists private issues, for a reader
// who may see them (readsPrivateIssues).
func (s *Server) projectTimeline(ctx context.Context, project model.Project, rng model.InsightRange, now time.Time, includePrivate bool) (model.ProjectTimeline, error) {
	today := model.DateFromTime(now.UTC())
	timeline := model.ProjectTimeline{
		Range:          rng,
		Today:          today,
		SprintsEnabled: project.SprintsEnabled,
		Groups:         []model.TimelineGroup{},
	}
	var since *time.Time
	if days := model.TimelineRangeDays(rng); days > 0 {
		day := today.AddDays(-days)
		timeline.Since = &day
		at := day.Time()
		since = &at
	}
	var sprints []model.Sprint
	if project.SprintsEnabled {
		var err error
		sprints, timeline.MoreCompletedSprints, timeline.MorePlannedSprints, err = s.timelineSprints(ctx, project.ID, since)
		if err != nil {
			// Defensive: only a DB outage reaches this.
			return model.ProjectTimeline{}, err
		}
	}
	sprintIDs := make([]uuid.UUID, 0, len(sprints))
	for _, sprint := range sprints {
		sprintIDs = append(sprintIDs, sprint.ID)
	}
	issues, err := s.store.ListTimelineIssues(ctx, store.ListTimelineIssuesParams{
		ProjectID:      project.ID,
		SprintsEnabled: project.SprintsEnabled,
		SprintIDs:      sprintIDs,
		Since:          since,
		Limit:          MaxLimit,
		IncludePrivate: includePrivate,
	})
	if err != nil {
		// Defensive: only a DB outage reaches this.
		return model.ProjectTimeline{}, err
	}
	for i := range sprints {
		sprint := &sprints[i]
		timeline.Groups = append(timeline.Groups, model.TimelineGroup{
			Sprint:  sprint,
			Items:   timelineItems(issues.BySprint[sprint.ID], sprint, today),
			HasMore: issues.SprintHasMore[sprint.ID],
		})
	}
	// With sprints on, work in no sprint gets a group only when there is
	// some; with them off it is the one group.
	if outside := timelineItems(issues.Outside, nil, today); len(outside) > 0 || !project.SprintsEnabled {
		timeline.Groups = append(timeline.Groups, model.TimelineGroup{Items: outside, HasMore: issues.OutsideHasMore})
	}
	timeline.Unscheduled = issues.Unscheduled
	timeline.From, timeline.To = model.TimelineFrame(timeline.Since, today, timeline.Groups)
	return timeline, nil
}

// timelineSprints picks the sprints a timeline groups by, in the order they
// run: the completed sprints that ended in range, oldest first, then the
// active sprint, then the planned sprints in their planned order. The flags
// say that the range holds more completed or planned sprints than listed.
func (s *Server) timelineSprints(ctx context.Context, projectID uuid.UUID, since *time.Time) ([]model.Sprint, bool, bool, error) {
	// One past the cap, newest first: those in range come first, so a full
	// page past the cap means there are more than the timeline lists.
	completed, _, err := s.store.ListSprints(ctx, store.ListSprintsParams{
		ProjectID: projectID,
		Status:    model.SprintStatusCompleted,
		Sort:      store.ListSprintsSortCompleted,
		Limit:     timelineCompletedSprintLimit + 1,
	})
	if err != nil {
		// Defensive: only a DB outage reaches this, and the two below.
		return nil, false, false, err
	}
	inRange := completed[:0]
	for _, sprint := range completed {
		if since != nil && (sprint.CompletedAt == nil || sprint.CompletedAt.Before(*since)) {
			break
		}
		inRange = append(inRange, sprint)
	}
	moreCompleted := len(inRange) > timelineCompletedSprintLimit
	if moreCompleted {
		inRange = inRange[:timelineCompletedSprintLimit]
	}
	slices.Reverse(inRange)
	active, _, err := s.store.ListSprints(ctx, store.ListSprintsParams{
		ProjectID: projectID,
		Status:    model.SprintStatusActive,
		Limit:     1,
	})
	if err != nil {
		return nil, false, false, err
	}
	planned, morePlanned, err := s.store.ListSprints(ctx, store.ListSprintsParams{
		ProjectID: projectID,
		Status:    model.SprintStatusPlanned,
		Limit:     timelinePlannedSprintLimit,
	})
	if err != nil {
		return nil, false, false, err
	}
	sprints := append(append(inRange, active...), planned...)
	return sprints, moreCompleted, morePlanned, nil
}

// timelineItems draws each issue's bar and orders the group by where its bar
// begins, issues without one last, keeping the store's order between ties.
func timelineItems(issues []store.TimelineIssue, sprint *model.Sprint, today model.Date) []model.TimelineItem {
	items := make([]model.TimelineItem, 0, len(issues))
	for _, issue := range issues {
		item := model.TimelineItem{Issue: issue.Issue, StartedAt: issue.StartedAt, CompletedAt: issue.CompletedAt}
		if start, end, ok := model.TimelineSpan(issue.Issue, sprint, issue.StartedAt, issue.CompletedAt, today); ok {
			item.Start, item.End = &start, &end
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Start, items[j].Start
		if a == nil || b == nil {
			return a != nil
		}
		return a.Before(*b)
	})
	return items
}

func (s *Server) getProjectTimeline(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	permissions, ok := s.requireProjectReadPermissions(w, r, project.ID)
	if !ok {
		return
	}
	rng, err := parseTimelineRange(r.URL.Query().Get("range"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	timeline, err := s.projectTimeline(r.Context(), project, rng, time.Now(), permissions.CanReadMembersOnly)
	if err != nil {
		// Defensive: the project was just read, so only a DB outage reaches this.
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, timeline)
}
