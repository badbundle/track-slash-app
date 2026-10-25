package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func timelineDay(t *testing.T, raw string) Date {
	t.Helper()
	d, err := ParseDate(raw)
	if err != nil {
		t.Fatalf("ParseDate(%q): %v", raw, err)
	}
	return d
}

func timelineTime(t *testing.T, raw string) *time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("time.Parse(%q): %v", raw, err)
	}
	return &at
}

func TestTimelineRangeDays(t *testing.T) {
	t.Parallel()
	want := map[InsightRange]int{
		InsightRangeTwoWeeks:   14,
		InsightRangeThirtyDays: 30,
		InsightRangeNinetyDays: 90,
		InsightRangeAll:        0,
		InsightRange("1y"):     0,
	}
	for rng, days := range want {
		if got := TimelineRangeDays(rng); got != days {
			t.Fatalf("TimelineRangeDays(%q) = %d, want %d", rng, got, days)
		}
	}
}

func TestTimelineSpan(t *testing.T) {
	t.Parallel()
	today := timelineDay(t, "2026-10-20")
	created := *timelineTime(t, "2026-09-01T15:00:00Z")
	due := timelineDay(t, "2026-10-25")
	early := timelineDay(t, "2026-08-15")
	sprintStart, sprintEnd := timelineTime(t, "2026-10-12T00:00:00Z"), timelineTime(t, "2026-10-26T00:00:00Z")
	sprint := &Sprint{StartDate: sprintStart, EndDate: sprintEnd}
	undated := &Sprint{}
	started := timelineTime(t, "2026-10-05T09:30:00Z")
	completed := timelineTime(t, "2026-10-09T23:59:00Z")

	issue := func(status Status, dueDate *Date) Issue {
		return Issue{Status: status, DueDate: dueDate, CreatedAt: created}
	}
	cases := []struct {
		name                 string
		issue                Issue
		sprint               *Sprint
		startedAt, completed *time.Time
		start, end           string
		ok                   bool
	}{
		{name: "completed work runs from its start to its completion", issue: issue(StatusDone, &due), sprint: sprint, startedAt: started, completed: completed, start: "2026-10-05", end: "2026-10-09", ok: true},
		{name: "work completed without starting starts with its sprint", issue: issue(StatusClosed, nil), sprint: sprint, completed: timelineTime(t, "2026-10-14T10:00:00Z"), start: "2026-10-12", end: "2026-10-14", ok: true},
		{name: "work completed before its sprint began sits on its completion day", issue: issue(StatusDone, nil), sprint: sprint, completed: completed, start: "2026-10-09", end: "2026-10-09", ok: true},
		{name: "open work in a sprint runs to its due date", issue: issue(StatusTodo, &due), sprint: sprint, start: "2026-10-12", end: "2026-10-25", ok: true},
		{name: "open work without a due date runs to its sprint's end", issue: issue(StatusTodo, nil), sprint: sprint, start: "2026-10-12", end: "2026-10-26", ok: true},
		{name: "open work in no sprint runs from its filing to its due date", issue: issue(StatusTodo, &due), start: "2026-09-01", end: "2026-10-25", ok: true},
		{name: "work in progress with nothing ahead runs to today", issue: issue(StatusInProgress, nil), startedAt: started, start: "2026-10-05", end: "2026-10-20", ok: true},
		{name: "overdue work in progress runs on to today", issue: issue(StatusInProgress, &early), startedAt: started, start: "2026-10-05", end: "2026-10-20", ok: true},
		{name: "work in progress keeps a due date still ahead", issue: issue(StatusInProgress, &due), startedAt: started, start: "2026-10-05", end: "2026-10-25", ok: true},
		{name: "overdue To do work sits on its due day", issue: issue(StatusTodo, &early), start: "2026-08-15", end: "2026-08-15", ok: true},
		{name: "To do work in an undated sprint has no bar", issue: issue(StatusTodo, nil), sprint: undated, ok: false},
		{name: "To do work in no sprint without a due date has no bar", issue: issue(StatusTodo, nil), ok: false},
		{name: "a reopened issue keeps when work first started", issue: issue(StatusTodo, &due), sprint: sprint, startedAt: started, start: "2026-10-05", end: "2026-10-25", ok: true},
	}
	for _, tc := range cases {
		start, end, ok := TimelineSpan(tc.issue, tc.sprint, tc.startedAt, tc.completed, today)
		if ok != tc.ok {
			t.Fatalf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
		}
		if !ok {
			if start != (Date{}) || end != (Date{}) {
				t.Fatalf("%s: no bar but got %s..%s", tc.name, start, end)
			}
			continue
		}
		if start.String() != tc.start || end.String() != tc.end {
			t.Fatalf("%s: span = %s..%s, want %s..%s", tc.name, start, end, tc.start, tc.end)
		}
	}
}

func TestTimelineFrame(t *testing.T) {
	t.Parallel()
	today := timelineDay(t, "2026-10-20")
	since := timelineDay(t, "2026-09-20")
	day := func(raw string) *Date { d := timelineDay(t, raw); return &d }
	sprint := &Sprint{StartDate: timelineTime(t, "2026-06-01T00:00:00Z"), EndDate: timelineTime(t, "2026-12-15T00:00:00Z")}
	halfDated := &Sprint{StartDate: timelineTime(t, "2025-01-01T00:00:00Z")}
	groups := []TimelineGroup{
		{Sprint: sprint, Items: []TimelineItem{{Start: day("2026-05-01"), End: day("2026-11-01")}, {}}},
		{Sprint: halfDated},
		{Items: []TimelineItem{{Start: day("2026-10-01"), End: day("2027-01-10")}}},
	}

	if from, to := TimelineFrame(&since, today, nil); from != since || to.String() != "2026-10-27" {
		t.Fatalf("empty bounded frame = %s..%s, want 2026-09-20..2026-10-27", from, to)
	}
	if from, to := TimelineFrame(nil, today, nil); from != today || to.String() != "2026-10-27" {
		t.Fatalf("empty all-time frame = %s..%s, want today..a week on", from, to)
	}
	// A bounded range keeps its start and cuts earlier bars off; all time
	// reaches back to the earliest bar or sprint.
	if from, to := TimelineFrame(&since, today, groups); from != since || to.String() != "2027-01-10" {
		t.Fatalf("bounded frame = %s..%s, want 2026-09-20..2027-01-10", from, to)
	}
	if from, to := TimelineFrame(nil, today, groups); from.String() != "2026-05-01" || to.String() != "2027-01-10" {
		t.Fatalf("all-time frame = %s..%s, want 2026-05-01..2027-01-10", from, to)
	}
}

// A timeline item serialises as the issue itself plus its bar, the shape
// CompletedIssue uses, so API clients read the issue fields as anywhere else.
func TestTimelineItemJSONFlattensTheIssue(t *testing.T) {
	t.Parallel()
	start, end := timelineDay(t, "2026-10-05"), timelineDay(t, "2026-10-09")
	item := TimelineItem{
		Issue:       Issue{Identifier: "DEMO-4", Title: "Ship it", Status: StatusDone},
		StartedAt:   timelineTime(t, "2026-10-05T09:30:00Z"),
		CompletedAt: timelineTime(t, "2026-10-09T23:59:00Z"),
		Start:       &start,
		End:         &end,
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"identifier":"DEMO-4"`, `"started_at":"2026-10-05T09:30:00Z"`, `"completed_at":"2026-10-09T23:59:00Z"`, `"start":"2026-10-05"`, `"end":"2026-10-09"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("timeline item JSON %s lacks %s", raw, want)
		}
	}
	raw, err = json.Marshal(TimelineItem{})
	if err != nil {
		t.Fatalf("Marshal undated: %v", err)
	}
	if !strings.Contains(string(raw), `"start":null,"end":null`) {
		t.Fatalf("undated item JSON %s should carry null start and end", raw)
	}
}
