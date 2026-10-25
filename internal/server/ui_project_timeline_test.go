package server

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func timelineTestDay(t *testing.T, raw string) model.Date {
	t.Helper()
	d, err := model.ParseDate(raw)
	if err != nil {
		t.Fatalf("ParseDate(%q): %v", raw, err)
	}
	return d
}

func TestUIProjectTimelinePath(t *testing.T) {
	t.Parallel()
	project := model.Project{OwnerUsername: "demo", Key: "DEMO"}
	for _, tc := range []struct {
		rng   model.InsightRange
		panel bool
		want  string
	}{
		{rng: model.DefaultInsightRange, want: "/demo/projects/DEMO/timeline"},
		{rng: "", panel: true, want: "/demo/projects/DEMO/timeline/panel"},
		{rng: model.InsightRangeAll, panel: true, want: "/demo/projects/DEMO/timeline/panel?range=all"},
	} {
		if got := uiProjectTimelinePath(project, tc.rng, tc.panel); got != tc.want {
			t.Fatalf("uiProjectTimelinePath(%q, %v) = %q, want %q", tc.rng, tc.panel, got, tc.want)
		}
	}
}

func TestUITimelineFrameBars(t *testing.T) {
	t.Parallel()
	// Ten days, so each day is 10% of the chart.
	frame := newUITimelineFrame(timelineTestDay(t, "2026-10-01"), timelineTestDay(t, "2026-10-10"))
	for _, tc := range []struct {
		name, start, end string
		x, width         string
		clipped          bool
	}{
		{name: "a span covers its days, both included", start: "2026-10-02", end: "2026-10-04", x: "10%", width: "30%"},
		{name: "a bar from before the chart is cut off at its start", start: "2026-09-20", end: "2026-10-02", x: "0%", width: "20%", clipped: true},
		{name: "a bar wholly before the chart keeps a sliver at its start", start: "2026-09-20", end: "2026-09-25", x: "0%", width: "10%", clipped: true},
		{name: "the last day reaches the right edge", start: "2026-10-10", end: "2026-10-10", x: "90%", width: "10%"},
	} {
		bar := frame.bar(timelineTestDay(t, tc.start), timelineTestDay(t, tc.end), "fill-x")
		if bar.X != tc.x || bar.Width != tc.width || bar.Clipped != tc.clipped || bar.Class != "fill-x" {
			t.Fatalf("%s: bar = %+v, want x %s width %s clipped %v", tc.name, bar, tc.x, tc.width, tc.clipped)
		}
	}

	// On a long chart a one-day bar keeps a visible width, and one on the
	// last day is pulled back inside the edge.
	long := newUITimelineFrame(timelineTestDay(t, "2026-01-01"), timelineTestDay(t, "2026-12-31"))
	if bar := long.bar(timelineTestDay(t, "2026-06-01"), timelineTestDay(t, "2026-06-01"), ""); bar.Width != "0.6%" {
		t.Fatalf("one-day bar on a year = %+v, want 0.6%% wide", bar)
	}
	if bar := long.bar(timelineTestDay(t, "2026-12-31"), timelineTestDay(t, "2026-12-31"), ""); bar.X != "99.4%" || bar.Width != "0.6%" {
		t.Fatalf("last-day bar on a year = %+v, want it pulled inside the edge", bar)
	}
}

func TestUITimelineFrameTicks(t *testing.T) {
	t.Parallel()
	labels := func(ticks []uiTimelineTick) string {
		out := make([]string, 0, len(ticks))
		for _, tick := range ticks {
			out = append(out, tick.Label+"@"+tick.Pos)
		}
		return strings.Join(out, " ")
	}
	for _, tc := range []struct {
		name, from, to, want string
	}{
		// Weekly ticks fall on Mondays; a label too near the right edge is
		// left off.
		{name: "weeks", from: "2026-10-01", to: "2026-10-27", want: "Oct 5@14.81% Oct 12@40.74% Oct 19@66.67%"},
		// Months, with the year on January.
		{name: "months", from: "2026-11-15", to: "2027-03-31", want: "Dec@11.68% Jan 2027@34.31% Feb@56.93% Mar@77.37%"},
		// Quarters always carry the year.
		{name: "quarters", from: "2026-02-10", to: "2027-06-30", want: "Apr 2026@9.88% Jul 2026@27.87% Oct 2026@46.05% Jan 2027@64.23% Apr 2027@82.02%"},
	} {
		frame := newUITimelineFrame(timelineTestDay(t, tc.from), timelineTestDay(t, tc.to))
		if got := labels(frame.ticks()); got != tc.want {
			t.Fatalf("%s ticks = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUITimelineLabels(t *testing.T) {
	t.Parallel()
	today := timelineTestDay(t, "2026-10-20")
	day := func(raw string) model.Date { return timelineTestDay(t, raw) }
	for _, tc := range []struct {
		start, end, want string
	}{
		{start: "2026-10-05", end: "2026-10-05", want: "Oct 5"},
		{start: "2026-10-05", end: "2026-10-09", want: "Oct 5 – Oct 9"},
		{start: "2026-12-28", end: "2027-01-04", want: "Dec 28, 2026 – Jan 4, 2027"},
	} {
		if got := uiTimelineSpanLabel(day(tc.start), day(tc.end), today); got != tc.want {
			t.Fatalf("uiTimelineSpanLabel(%s, %s) = %q, want %q", tc.start, tc.end, got, tc.want)
		}
	}

	for _, tc := range []struct {
		status model.Status
		class  string
		dashed bool
	}{
		{status: model.StatusTodo, class: uiInsightNeutral.Fill},
		{status: model.StatusInProgress, class: uiInsightBlue.Fill},
		{status: model.StatusDone, class: uiInsightGreen.Fill},
		{status: model.StatusClosed, class: "fill-white stroke-slate-400 dark:fill-slate-900 dark:stroke-slate-500", dashed: true},
	} {
		if class, dashed := uiTimelineBarClass(tc.status); class != tc.class || dashed != tc.dashed {
			t.Fatalf("uiTimelineBarClass(%s) = %q dashed %v", tc.status, class, dashed)
		}
	}

	for status, want := range map[model.SprintStatus]string{
		model.SprintStatusActive:    "Active",
		model.SprintStatusCompleted: "Completed",
		model.SprintStatusPlanned:   "Planned",
	} {
		if got := uiTimelineSprintStatus(status); got != want {
			t.Fatalf("uiTimelineSprintStatus(%s) = %q, want %q", status, got, want)
		}
	}

	start, end, nextYear := day("2026-10-05"), day("2026-10-09"), day("2027-01-15")
	thisYear := day("2026-10-25")
	for _, tc := range []struct {
		item model.TimelineItem
		want string
	}{
		{item: model.TimelineItem{Issue: model.Issue{Status: model.StatusTodo}}, want: "To do. No dates."},
		{item: model.TimelineItem{Issue: model.Issue{Status: model.StatusDone}, Start: &start, End: &end}, want: "Done. Oct 5 – Oct 9."},
		{item: model.TimelineItem{Issue: model.Issue{Status: model.StatusInProgress, DueDate: &thisYear}, Start: &start, End: &thisYear}, want: "In progress. Oct 5 – Oct 25. Due Oct 25."},
		{item: model.TimelineItem{Issue: model.Issue{Status: model.StatusTodo, DueDate: &nextYear}, Start: &start, End: &nextYear}, want: "To do. Oct 5, 2026 – Jan 15, 2027. Due Jan 15, 2027."},
	} {
		if got := uiTimelineSummary(tc.item, today); got != tc.want {
			t.Fatalf("uiTimelineSummary = %q, want %q", got, tc.want)
		}
	}

	for _, tc := range []struct {
		timeline model.ProjectTimeline
		want     []string
	}{
		{timeline: model.ProjectTimeline{}},
		{timeline: model.ProjectTimeline{Unscheduled: 1}, want: []string{"1 open issue has no due date or sprint, so it isn't drawn."}},
		{timeline: model.ProjectTimeline{Unscheduled: 4, MoreCompletedSprints: true, MorePlannedSprints: true}, want: []string{
			"4 open issues have no due date or sprint, so they aren't drawn.",
			"Showing the 12 most recent completed sprints in this range.",
			"Showing the next 12 planned sprints.",
		}},
	} {
		if got := uiTimelineNotes(tc.timeline); strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("uiTimelineNotes = %q, want %q", got, tc.want)
		}
	}
}

func TestUIBuildProjectTimeline(t *testing.T) {
	t.Parallel()
	project := model.Project{OwnerUsername: "demo", Key: "DEMO"}
	day := func(raw string) *model.Date { d := timelineTestDay(t, raw); return &d }
	at := func(raw string) *time.Time { d := timelineTestDay(t, raw).Time(); return &d }
	today := *day("2026-10-20")
	base := model.ProjectTimeline{Range: model.InsightRangeThirtyDays, Since: day("2026-09-20"), From: *day("2026-09-20"), To: *day("2026-10-29"), Today: today}

	// No groups at all is the empty state.
	empty := uiBuildProjectTimeline(project, base)
	if !empty.Empty || len(empty.Groups) != 0 || len(empty.RangeOptions) != 4 || !empty.RangeOptions[1].Active || empty.FrameLabel != "Sep 20 – Oct 29" {
		t.Fatalf("empty timeline = %+v", empty)
	}

	// A sprint with nothing in it still draws its window, so the chart is not
	// empty.
	withSprint := base
	withSprint.SprintsEnabled = true
	withSprint.Groups = []model.TimelineGroup{
		{Sprint: &model.Sprint{Ref: "sprint-2", Name: "Now", Status: model.SprintStatusActive, StartDate: at("2026-10-15"), EndDate: at("2026-10-28")}},
		{Sprint: &model.Sprint{Ref: "sprint-3", Name: "Later", Status: model.SprintStatusPlanned}, Items: []model.TimelineItem{{Issue: model.Issue{Identifier: "DEMO-9", Status: model.StatusTodo}}}},
		{HasMore: true, Items: []model.TimelineItem{
			{Issue: model.Issue{Identifier: "DEMO-1", Status: model.StatusInProgress, DueDate: day("2026-09-01")}, Start: day("2026-09-01"), End: &today},
			{Issue: model.Issue{Identifier: "DEMO-2", Status: model.StatusClosed, DueDate: day("2026-10-25")}, Start: day("2026-10-01"), End: day("2026-10-25")},
		}},
	}
	data := uiBuildProjectTimeline(project, withSprint)
	if data.Empty || len(data.Groups) != 3 || data.TodayPos != "76.25%" {
		t.Fatalf("timeline = %+v", data)
	}
	now, later, outside := data.Groups[0], data.Groups[1], data.Groups[2]
	if now.Heading != "Now" || now.Status != "Active" || now.Dates != "Oct 15 – Oct 28" || now.Window == nil || len(now.Rows) != 0 {
		t.Fatalf("active sprint group = %+v", now)
	}
	if later.Status != "Planned" || later.Dates != "No dates" || later.Window != nil || later.Rows[0].Bar != nil || later.Rows[0].Dates != "No dates" {
		t.Fatalf("undated sprint group = %+v", later)
	}
	if outside.Heading != "Not in a sprint" || !outside.HasMore || outside.Window != nil {
		t.Fatalf("outside group = %+v", outside)
	}
	overdue, closed := outside.Rows[0], outside.Rows[1]
	// A due date before the chart begins gets no marker; the bar is cut off.
	if overdue.DuePos != "" || !overdue.Bar.Clipped || overdue.Bar.Class != uiInsightBlue.Fill || overdue.Dates != "Sep 1 – Oct 20" {
		t.Fatalf("overdue row = %+v bar %+v", overdue, overdue.Bar)
	}
	if closed.DuePos != "88.75%" || !closed.Bar.Dashed || closed.Summary != "Closed. Oct 1 – Oct 25. Due Oct 25." {
		t.Fatalf("closed row = %+v bar %+v", closed, closed.Bar)
	}

	// With sprints off the one group has no heading.
	off := base
	off.Groups = []model.TimelineGroup{{Items: []model.TimelineItem{{Issue: model.Issue{Status: model.StatusDone}, Start: day("2026-10-01"), End: day("2026-10-02")}}}}
	if data := uiBuildProjectTimeline(project, off); data.Empty || data.Groups[0].Heading != "" {
		t.Fatalf("sprints-off timeline = %+v", data)
	}
	// Nor does an empty one make a chart.
	off.Groups = []model.TimelineGroup{{}}
	if data := uiBuildProjectTimeline(project, off); !data.Empty {
		t.Fatalf("empty sprints-off timeline = %+v", data)
	}
}

func TestTimelineItemsOrderByBarStart(t *testing.T) {
	t.Parallel()
	today := timelineTestDay(t, "2026-10-20")
	at := func(raw string) *time.Time { d := timelineTestDay(t, raw).Time(); return &d }
	issue := func(status model.Status, created string) model.Issue {
		return model.Issue{ID: uuid.New(), Status: status, CreatedAt: *at(created)}
	}
	late := store.TimelineIssue{Issue: issue(model.StatusInProgress, "2026-10-01"), StartedAt: at("2026-10-15")}
	undated := store.TimelineIssue{Issue: issue(model.StatusTodo, "2026-09-01")}
	early := store.TimelineIssue{Issue: issue(model.StatusInProgress, "2026-10-01"), StartedAt: at("2026-10-02")}
	alsoUndated := store.TimelineIssue{Issue: issue(model.StatusTodo, "2026-09-02")}
	sameDay := store.TimelineIssue{Issue: issue(model.StatusInProgress, "2026-10-01"), StartedAt: at("2026-10-15")}

	items := timelineItems([]store.TimelineIssue{late, undated, early, alsoUndated, sameDay}, nil, today)
	want := []uuid.UUID{early.Issue.ID, late.Issue.ID, sameDay.Issue.ID, undated.Issue.ID, alsoUndated.Issue.ID}
	for i, id := range want {
		if items[i].ID != id {
			t.Fatalf("item %d = %+v, want %s", i, items[i], id)
		}
	}
	if items[3].Start != nil || items[0].Start == nil || *items[0].Start != timelineTestDay(t, "2026-10-02") {
		t.Fatalf("bars = %v, %v", items[0].Start, items[3].Start)
	}
}
