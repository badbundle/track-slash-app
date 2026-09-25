package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestParseProjectInsightsQuery(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		rng        string
		sprint     string
		wantRange  model.InsightRange
		wantSprint int
		wantErr    bool
	}{
		{name: "defaults", wantRange: model.DefaultInsightRange},
		{name: "range is case insensitive", rng: " 90D ", wantRange: model.InsightRangeNinetyDays},
		{name: "sprint ref", rng: "all", sprint: "Sprint-4", wantRange: model.InsightRangeAll, wantSprint: 4},
		{name: "unknown range", rng: "7d", wantErr: true},
		{name: "bad sprint ref", sprint: "4", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProjectInsightsQuery(tt.rng, tt.sprint)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseProjectInsightsQuery err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && (got.Range != tt.wantRange || got.SprintNumber != tt.wantSprint) {
				t.Fatalf("parseProjectInsightsQuery = %+v", got)
			}
		})
	}
	if _, err := parseProjectInsightsQuery("never", ""); !errors.Is(err, errInsightRange) {
		t.Fatalf("unknown range err = %v, want errInsightRange", err)
	}
}

func TestProjectInsightsSprintsEnabled(t *testing.T) {
	t.Parallel()
	if projectInsightsSprintsEnabled(model.Project{}) {
		t.Fatal("project with sprints off reported sprints enabled")
	}
	if !projectInsightsSprintsEnabled(model.Project{SprintsEnabled: true}) {
		t.Fatal("project in sprint mode reported sprints disabled")
	}
}

func TestUIInsightScale(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		max      float64
		integer  bool
		wantTop  float64
		wantStep float64
	}{
		{max: 0, integer: true, wantTop: 4, wantStep: 1},
		{max: math.NaN(), integer: true, wantTop: 4, wantStep: 1},
		{max: 1, integer: true, wantTop: 2, wantStep: 1},
		{max: 3, integer: true, wantTop: 3, wantStep: 1},
		{max: 7, integer: true, wantTop: 8, wantStep: 2},
		{max: 42, integer: true, wantTop: 50, wantStep: 10},
		{max: 85, integer: true, wantTop: 100, wantStep: 20},
		{max: 0.2, integer: false, wantTop: 0.2, wantStep: 0.05},
		{max: 7.3, integer: false, wantTop: 8, wantStep: 2},
	} {
		top, step := uiInsightScale(tt.max, tt.integer)
		if math.Abs(top-tt.wantTop) > 1e-9 || math.Abs(step-tt.wantStep) > 1e-9 {
			t.Fatalf("uiInsightScale(%v, %v) = %v, %v; want %v, %v", tt.max, tt.integer, top, step, tt.wantTop, tt.wantStep)
		}
	}
	ticks := uiInsightYTicks(8, 2, uiInsightDecimal)
	if len(ticks) != 5 || ticks[0].Label != "0" || ticks[0].Pos != "100%" || ticks[4].Label != "8" || ticks[4].Pos != "0%" || ticks[1].Pos != "75%" {
		t.Fatalf("y ticks = %+v", ticks)
	}
}

func TestUIInsightTickIndexesAndAnchors(t *testing.T) {
	t.Parallel()
	if got := uiInsightTickIndexes(0); got != nil {
		t.Fatalf("no indexes = %v", got)
	}
	if got := uiInsightTickIndexes(3); len(got) != 3 || got[2] != 2 {
		t.Fatalf("short indexes = %v", got)
	}
	if got := uiInsightTickIndexes(14); len(got) != 5 || got[0] != 0 || got[2] != 7 || got[4] != 13 {
		t.Fatalf("long indexes = %v", got)
	}
	if got := uiInsightTickIndexes(6); len(got) != 5 || got[1] != 1 || got[3] != 4 {
		t.Fatalf("six indexes = %v", got)
	}
	for _, tt := range []struct {
		k, n int
		want string
	}{{0, 1, "middle"}, {0, 3, "start"}, {1, 3, "middle"}, {2, 3, "end"}} {
		if got := uiInsightEdgeAnchor(tt.k, tt.n); got != tt.want {
			t.Fatalf("uiInsightEdgeAnchor(%d, %d) = %q", tt.k, tt.n, got)
		}
	}
	ticks := uiInsightTimeTicks(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC))
	if len(ticks) != 5 || ticks[2].Label != "Sep 5" || ticks[2].Pos != "50%" || ticks[4].Anchor != "end" {
		t.Fatalf("time ticks = %+v", ticks)
	}
}

func TestUIInsightPaths(t *testing.T) {
	t.Parallel()
	one, two, three := 1.0, 2.0, 3.0
	if got := uiInsightLinePath([]*float64{&one, &two, nil, &three}, 4); got != "M0,750L333.3,500M1000,250" {
		t.Fatalf("line path = %q", got)
	}
	if got := uiInsightLinePath([]*float64{&two}, 4); got != "M500,500" {
		t.Fatalf("single point path = %q", got)
	}
	if got := uiInsightAreaPath([]float64{2, 4}, []float64{0, 1}, 4); got != "M0,500L1000,0L1000,750L0,1000Z" {
		t.Fatalf("area path = %q", got)
	}
	if got := uiInsightAreaPath(nil, nil, 4); got != "" {
		t.Fatalf("empty area path = %q", got)
	}
}

func TestUIInsightFormatting(t *testing.T) {
	t.Parallel()
	for hours, want := range map[float64]string{0: "0 hours", 0.5: "Under an hour", 1: "1 hour", 5.4: "5 hours", 24: "1 day", 36: "1.5 days", 240: "10 days"} {
		if got := uiInsightDuration(hours); got != want {
			t.Fatalf("uiInsightDuration(%v) = %q, want %q", hours, got, want)
		}
	}
	if uiInsightSigned(3) != "+3" || uiInsightSigned(0) != "0" || uiInsightSigned(-2) != "-2" {
		t.Fatal("uiInsightSigned formats signs")
	}
	if uiInsightShare(2, 3) != "67%" || uiInsightShare(0, 0) != "0%" {
		t.Fatal("uiInsightShare rounds and guards an empty total")
	}
	if uiInsightPlural(1, "issue", "issues") != "issue" || uiInsightPlural(2, "issue", "issues") != "issues" {
		t.Fatal("uiInsightPlural picks forms")
	}
	if uiInsightDecimal(5.25) != "5.3" || uiInsightDecimal(4) != "4" || uiInsightPercent(33.333) != "33.33%" {
		t.Fatal("decimal and percent rounding")
	}
	keys := map[string]string{
		"dashed": "border-dashed",
		"line":   "h-0.5 w-3 rounded-full bg-x",
		"dot":    "rounded-full bg-x",
		"bar":    "rounded-sm bg-x",
	}
	for mark, want := range keys {
		series := uiInsightSeries{Mark: mark, Swatch: "bg-x", Dot: "fill-x"}
		if !strings.Contains(series.KeyClass(), want) || series.MarkerClass() != "fill-x" {
			t.Fatalf("%s key class = %q", mark, series.KeyClass())
		}
	}
}

func TestUIInsightLabelsAndPaths(t *testing.T) {
	t.Parallel()
	project := model.Project{OwnerUsername: "bradley", Key: "TRACK"}
	for _, tt := range []struct {
		rng    model.InsightRange
		sprint string
		panel  bool
		want   string
	}{
		{rng: model.DefaultInsightRange, want: "/bradley/projects/TRACK/insights"},
		{rng: "", panel: true, want: "/bradley/projects/TRACK/insights/panel"},
		{rng: model.InsightRangeAll, sprint: "sprint-2", want: "/bradley/projects/TRACK/insights?range=all&sprint=sprint-2"},
		{rng: model.InsightRangeTwoWeeks, panel: true, want: "/bradley/projects/TRACK/insights/panel?range=2w"},
	} {
		if got := uiProjectInsightsPath(project, tt.rng, tt.sprint, tt.panel); got != tt.want {
			t.Fatalf("uiProjectInsightsPath = %q, want %q", got, tt.want)
		}
	}
	week := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	weekly := uiInsightPeriodLabels(model.InsightBucketWeek, []model.ProjectInsightFlowPoint{
		{PeriodStart: week.AddDate(0, 0, -7), PeriodEnd: week},
		{PeriodStart: week, PeriodEnd: week.AddDate(0, 0, 3)},
	})
	if weekly[0] != "Week of Sep 7" || weekly[1] != "Week of Sep 14 (so far)" {
		t.Fatalf("weekly labels = %v", weekly)
	}
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	monthly := uiInsightPeriodLabels(model.InsightBucketMonth, []model.ProjectInsightFlowPoint{{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}})
	if monthly[0] != "August 2026" || uiInsightAxisLabel(model.InsightBucketMonth, month) != "Aug 2026" {
		t.Fatalf("monthly labels = %v", monthly)
	}
	if uiInsightNextPeriod(model.InsightBucketMonth, month) != month.AddDate(0, 1, 0) {
		t.Fatal("month period length")
	}
	for bucket, want := range map[model.InsightBucket]string{model.InsightBucketDay: "daily", model.InsightBucketWeek: "weekly", model.InsightBucketMonth: "monthly"} {
		label := uiInsightPeriodLabel(model.ProjectInsights{Bucket: bucket, Start: month, End: week})
		if label != "Aug 1, 2026 to Sep 14, 2026 · "+want+" · UTC" {
			t.Fatalf("period label = %q", label)
		}
	}
}

// insightFixture is a fortnight of daily flow with a sprint history, so every
// chart builder has data to draw.
func insightFixture(now time.Time) model.ProjectInsights {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -13)
	insights := model.ProjectInsights{
		Range:  model.InsightRangeTwoWeeks,
		Bucket: model.InsightBucketDay,
		Start:  start,
		End:    now,
	}
	for i := 0; i < 14; i++ {
		periodStart := start.AddDate(0, 0, i)
		periodEnd := periodStart.AddDate(0, 0, 1)
		if periodEnd.After(now) {
			periodEnd = now
		}
		todo, inProgress, done := 10-i/2, 2+i%3, i
		insights.Flow = append(insights.Flow, model.ProjectInsightFlowPoint{
			PeriodStart: periodStart, PeriodEnd: periodEnd,
			Todo: todo, InProgress: inProgress, Done: done, Cancelled: i / 7,
			Scope: todo + inProgress + done, Started: inProgress + done, Completed: done,
		})
		insights.Throughput = append(insights.Throughput, model.ProjectInsightThroughputPoint{
			PeriodStart: periodStart, PeriodEnd: periodEnd, Created: i % 4, Resolved: i % 3, Reopened: i / 13,
		})
	}
	committed := 6
	activeID, doneID := uuid.New(), uuid.New()
	insights.CycleTime = model.ProjectInsightCycleTime{
		Issues: []model.ProjectInsightCycleTimeIssue{
			{Identifier: "TRACK-1", Number: 1, Title: "Quick fix", StartedAt: start.Add(time.Hour), CompletedAt: start.Add(20 * time.Hour), DurationHours: 19},
			{Identifier: "TRACK-2", Number: 2, Title: "Long feature", StartedAt: start.AddDate(0, 0, 2), CompletedAt: start.AddDate(0, 0, 9), DurationHours: 168},
		},
		MedianHours: 93.5, P85Hours: 145.15, NotStarted: 2, Truncated: true,
	}
	insights.Sprints = model.ProjectInsightSprints{
		Enabled: true,
		Count:   3,
		Velocity: []model.ProjectInsightSprintVelocity{
			{SprintID: doneID, Ref: "sprint-1", Number: 1, Name: "Legacy", CompletedAt: start.AddDate(0, 0, 3), Total: 5, Done: 3, Cancelled: 1, CarriedOver: 1},
			{SprintID: uuid.New(), Ref: "sprint-2", Number: 2, CompletedAt: start.AddDate(0, 0, 10), Committed: &committed, Total: 7, Done: 5, CarriedOver: 2},
		},
		Options: []model.ProjectInsightSprintOption{
			{SprintID: activeID, Ref: "sprint-3", Name: "Current", Status: model.SprintStatusActive},
			{SprintID: doneID, Ref: "sprint-1", Name: "Legacy", Status: model.SprintStatusCompleted},
		},
	}
	burnupStart := start.AddDate(0, 0, 11)
	insights.Sprints.Burnup = &model.ProjectInsightSprintBurnup{
		SprintID: activeID, Ref: "sprint-3", Name: "Current", Status: model.SprintStatusActive,
		Start: burnupStart, End: burnupStart.AddDate(0, 0, 7), Days: 7, Estimated: true,
		Points: []model.ProjectInsightFlowPoint{
			{PeriodStart: burnupStart, PeriodEnd: burnupStart.AddDate(0, 0, 1), Scope: 4, Started: 1},
			{PeriodStart: burnupStart.AddDate(0, 0, 1), PeriodEnd: burnupStart.AddDate(0, 0, 2), Scope: 5, Started: 2, Completed: 1},
			{PeriodStart: burnupStart.AddDate(0, 0, 2), PeriodEnd: now, Scope: 6, Started: 3, Completed: 2},
		},
	}
	return insights
}

func insightChartByID(t *testing.T, data *uiProjectInsightsData, id string) uiInsightChart {
	t.Helper()
	for _, chart := range data.Charts {
		if chart.ID == id {
			return chart
		}
	}
	t.Fatalf("chart %s missing from %d charts", id, len(data.Charts))
	return uiInsightChart{}
}

func TestUIBuildProjectInsightsCharts(t *testing.T) {
	t.Parallel()
	project := model.Project{OwnerUsername: "bradley", Key: "TRACK"}
	now := time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)
	data := uiBuildProjectInsights(project, insightFixture(now), projectInsightsQuery{Range: model.InsightRangeTwoWeeks, SprintNumber: 3})
	if len(data.Charts) != 6 || !data.SprintsEnabled || data.Range != model.InsightRangeTwoWeeks {
		t.Fatalf("insights data = %d charts, sprints %v", len(data.Charts), data.SprintsEnabled)
	}
	if len(data.RangeOptions) != 4 || !data.RangeOptions[0].Active || data.RangeOptions[0].Href != "/bradley/projects/TRACK/insights?range=2w&sprint=sprint-3" {
		t.Fatalf("range options = %+v", data.RangeOptions)
	}

	burnup := insightChartByID(t, data, "insight-burnup")
	if len(burnup.Lines) != 3 || burnup.Stats[0].Value != "65%" || burnup.Series[0].Value != "20" || burnup.Empty != "" {
		t.Fatalf("burn-up = %+v", burnup)
	}
	var payload uiInsightChartJSON
	if err := json.Unmarshal([]byte(burnup.Data), &payload); err != nil || payload.Kind != "line" || len(payload.Labels) != 14 || !strings.HasSuffix(payload.Labels[13], "(so far)") {
		t.Fatalf("burn-up payload = %+v err %v", payload, err)
	}

	flow := insightChartByID(t, data, "insight-flow")
	if len(flow.Areas) != 3 || len(flow.Lines) != 3 || flow.Series[0].Key != "done" || flow.Series[0].Mark != "area" || flow.Stats[1].Value != "1" {
		t.Fatalf("cumulative flow = %+v", flow)
	}

	throughput := insightChartByID(t, data, "insight-throughput")
	if len(throughput.Columns) != 14 || throughput.Series[0].Value != "19" || throughput.Stats[1].Value != "1" || len(throughput.Table.Rows) != 14 {
		t.Fatalf("throughput = %+v", throughput)
	}
	if first := throughput.Columns[0]; len(first.Bars) != 0 {
		t.Fatalf("zero bars should be omitted, got %+v", first)
	}

	cycle := insightChartByID(t, data, "insight-cycle-time")
	if len(cycle.Dots) != 2 || cycle.Dots[1].Href != "/bradley/issues/TRACK-2" || cycle.Dots[1].Value != "7 days" || len(cycle.RefLines) != 2 || !cycle.RefLines[1].Dashed {
		t.Fatalf("cycle time = %+v", cycle)
	}
	if len(cycle.Notes) != 2 || !strings.Contains(cycle.Notes[0], "2 issues went straight to Done") || cycle.Table.LeftColumns != 2 || cycle.Table.Rows[0].Href == "" {
		t.Fatalf("cycle notes/table = %+v %+v", cycle.Notes, cycle.Table)
	}

	sprint := insightChartByID(t, data, "insight-sprint-burnup")
	if sprint.SprintPicker != "sprint-3 · Current" || len(sprint.Sprints) != 2 || !sprint.Sprints[0].Active || sprint.Sprints[1].Active {
		t.Fatalf("sprint burn-up picker = %+v", sprint)
	}
	if sprint.Sprints[1].Href != "/bradley/projects/TRACK/insights?range=2w&sprint=sprint-1" || len(sprint.Notes) != 1 || sprint.Stats[0].Value != "2 of 6" || sprint.Stats[1].Value != "33%" || len(sprint.Stats) != 3 || sprint.Stats[2].Value != "4" {
		t.Fatalf("sprint burn-up = %+v", sprint)
	}
	if err := json.Unmarshal([]byte(sprint.Data), &payload); err != nil || len(payload.Labels) != 7 || payload.Series[0].Values[6] != nil || !strings.HasSuffix(payload.Labels[2], "(so far)") {
		t.Fatalf("sprint burn-up payload = %+v err %v", payload, err)
	}

	velocity := insightChartByID(t, data, "insight-velocity")
	if len(velocity.Columns) != 2 || len(velocity.Columns[0].Bars) != 1 || len(velocity.Columns[1].Bars) != 2 || velocity.Stats[0].Value != "4" || len(velocity.Notes) != 1 {
		t.Fatalf("velocity = %+v", velocity)
	}
	if err := json.Unmarshal([]byte(velocity.Data), &payload); err != nil || payload.Series[0].Text[0] != "Not recorded" || payload.Series[0].Text[1] != "6" || len(payload.Details) != 3 {
		t.Fatalf("velocity payload = %+v err %v", payload, err)
	}
}

func TestUIBuildProjectInsightsEmptyStates(t *testing.T) {
	t.Parallel()
	project := model.Project{OwnerUsername: "bradley", Key: "TRACK"}
	now := time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)
	insights := insightFixture(now)
	for i := range insights.Flow {
		insights.Flow[i] = model.ProjectInsightFlowPoint{PeriodStart: insights.Flow[i].PeriodStart, PeriodEnd: insights.Flow[i].PeriodEnd}
		insights.Throughput[i] = model.ProjectInsightThroughputPoint{PeriodStart: insights.Flow[i].PeriodStart, PeriodEnd: insights.Flow[i].PeriodEnd}
	}
	insights.CycleTime = model.ProjectInsightCycleTime{Issues: []model.ProjectInsightCycleTimeIssue{}}
	insights.Sprints.Velocity = nil
	insights.Sprints.Options = nil
	insights.Sprints.Burnup = nil
	data := uiBuildProjectInsights(project, insights, projectInsightsQuery{Range: model.InsightRangeTwoWeeks})
	for id, want := range map[string]string{
		"insight-burnup":        "No issues existed in this range.",
		"insight-flow":          "No issues existed in this range.",
		"insight-throughput":    "No issues were created or resolved in this range.",
		"insight-cycle-time":    "No issues moved from In progress to Done in this range.",
		"insight-sprint-burnup": "No active sprint, and no sprint was completed in this range.",
		"insight-velocity":      "No sprints were completed in this range.",
	} {
		if chart := insightChartByID(t, data, id); chart.Empty != want || len(chart.Stats) != 0 {
			t.Fatalf("%s empty = %q stats %+v", id, chart.Empty, chart.Stats)
		}
	}

	insights.Sprints.Enabled = false
	if data := uiBuildProjectInsights(project, insights, projectInsightsQuery{}); len(data.Charts) != 4 || data.SprintsEnabled {
		t.Fatalf("sprints disabled = %d charts", len(data.Charts))
	}

	// One period of history draws, with a note that there is no trend yet.
	last := len(insights.Flow) - 1
	insights.Flow[last].Todo, insights.Flow[last].Scope = 1, 1
	data = uiBuildProjectInsights(project, insights, projectInsightsQuery{})
	for _, id := range []string{"insight-burnup", "insight-flow"} {
		if chart := insightChartByID(t, data, id); chart.Empty != "" || len(chart.Notes) == 0 || !strings.Contains(chart.Notes[0], "Only the latest period has issues") {
			t.Fatalf("%s with one period = empty %q notes %v", id, chart.Empty, chart.Notes)
		}
	}

	// A sprint that has not started has no points; one that started today has
	// a single point and says why there is no line yet; a completed one marks
	// its last day as measured at completion.
	insights.Sprints.Enabled = true
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	insights.Sprints.Burnup = &model.ProjectInsightSprintBurnup{Ref: "sprint-9", Status: model.SprintStatusActive, Start: start, End: start.AddDate(0, 0, 3), Days: 3, Points: []model.ProjectInsightFlowPoint{}}
	sprint := insightChartByID(t, uiBuildProjectInsights(project, insights, projectInsightsQuery{}), "insight-sprint-burnup")
	if sprint.Empty != "This sprint has not started yet." || sprint.SprintPicker != "sprint-9" {
		t.Fatalf("unstarted sprint = %+v", sprint)
	}
	insights.Sprints.Burnup.Points = []model.ProjectInsightFlowPoint{{PeriodStart: start, PeriodEnd: start.Add(5 * time.Hour), Scope: 2}}
	sprint = insightChartByID(t, uiBuildProjectInsights(project, insights, projectInsightsQuery{}), "insight-sprint-burnup")
	if sprint.Empty != "" || len(sprint.Notes) != 1 || !strings.Contains(sprint.Notes[0], "started today") {
		t.Fatalf("new sprint = %+v", sprint)
	}
	insights.Sprints.Burnup.Status = model.SprintStatusCompleted
	insights.Sprints.Burnup.End = start.AddDate(0, 0, 1)
	insights.Sprints.Burnup.Days = 1
	sprint = insightChartByID(t, uiBuildProjectInsights(project, insights, projectInsightsQuery{}), "insight-sprint-burnup")
	var payload uiInsightChartJSON
	if err := json.Unmarshal([]byte(sprint.Data), &payload); err != nil || !strings.HasSuffix(payload.Labels[0], "(at completion)") || len(sprint.Stats) != 2 {
		t.Fatalf("completed sprint = %+v payload %+v err %v", sprint, payload, err)
	}
}

func TestUIProjectPanelRendersInsights(t *testing.T) {
	t.Parallel()
	project := model.Project{ID: uuid.New(), OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	now := time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "project-panel", uiProjectPanelData{
		Project:     project,
		View:        "insights",
		ProjectTabs: uiProjectTabs(project, "insights", nil),
		Insights:    uiBuildProjectInsights(project, insightFixture(now), projectInsightsQuery{Range: model.InsightRangeTwoWeeks}),
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`data-project-insights`,
		`href="/bradley/projects/TRACK/insights" hx-get="/bradley/projects/TRACK/insights/panel"`,
		`aria-current="page"`,
		`data-lucide="chart-line"`,
		`id="insight-burnup" aria-labelledby="insight-burnup-title" data-insight-chart="{&#34;kind&#34;:&#34;line&#34;`,
		`viewBox="0 0 1000 1000" preserveAspectRatio="none"`,
		`vector-effect="non-scaling-stroke"`,
		`data-insight-crosshair`,
		`data-insight-marker="completed"`,
		`data-insight-column="13"`,
		`<a href="/bradley/issues/TRACK-2" data-series="issues" data-insight-point data-tooltip-disabled`,
		`stroke-dasharray="6 4"`,
		`data-insight-toggle="p85"`,
		`w-3 border-t-2 border-dashed`,
		`<caption class="sr-only">Cycle time of completed issues</caption>`,
		`sprint-3 · Current`,
		`Commitment was not recorded for 1 sprint`,
		`<template data-insight-tooltip-row>`,
		`data-insight-live aria-live="polite"`,
		`<foreignObject data-insight-tooltip-box`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("insights view missing %q", want)
		}
	}
	if strings.Contains(body, "Sprints are off for this project") {
		t.Fatal("insights view with sprints rendered the sprints note")
	}
	// Only the scatter plot skips the plot-level tab stop: its points are links.
	if got := strings.Count(body, `data-insight-plot tabindex="0"`); got != 5 {
		t.Fatalf("focusable plots = %d, want 5", got)
	}
}
