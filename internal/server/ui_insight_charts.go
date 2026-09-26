package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
)

// Insight charts are server-rendered SVG. Lines and areas are drawn in a
// stretched 1000x1000 viewBox with non-scaling strokes so they fill any width;
// everything that must keep its shape (text, dots, bars, gridlines) is placed
// with percentage coordinates in unscaled SVG or HTML. app.js adds the hover
// crosshair, tooltips, keyboard stepping, and legend toggles from the JSON in
// data-insight-chart.

const uiInsightViewBox = 1000.0

// uiInsightColor is one series' colour in every mark. Hues follow issue status
// colours: neutral for scope and to-do work, blue for started work, emerald
// for completed work, and the indigo accent for arrivals and single-series
// marks. Adjacent pairs were checked for colour-vision separation on both
// surfaces.
type uiInsightColor struct {
	Stroke string
	Fill   string
	Area   string
	Swatch string
}

var (
	uiInsightNeutral = uiInsightColor{
		Stroke: "stroke-slate-400 dark:stroke-slate-500",
		Fill:   "fill-slate-400 dark:fill-slate-500",
		Area:   "fill-slate-400/25 dark:fill-slate-500/30",
		Swatch: "bg-slate-400 dark:bg-slate-500",
	}
	uiInsightBlue = uiInsightColor{
		Stroke: "stroke-blue-500",
		Fill:   "fill-blue-500",
		Area:   "fill-blue-500/25 dark:fill-blue-500/30",
		Swatch: "bg-blue-500",
	}
	uiInsightGreen = uiInsightColor{
		Stroke: "stroke-emerald-600",
		Fill:   "fill-emerald-600",
		Area:   "fill-emerald-600/25 dark:fill-emerald-600/35",
		Swatch: "bg-emerald-600",
	}
	uiInsightIndigo = uiInsightColor{
		Stroke: "stroke-indigo-500",
		Fill:   "fill-indigo-500",
		Area:   "fill-indigo-500/25",
		Swatch: "bg-indigo-500",
	}
	uiInsightReference = uiInsightColor{
		Stroke: "stroke-slate-500 dark:stroke-slate-400",
		Fill:   "fill-slate-500 dark:fill-slate-400",
		Swatch: "bg-slate-500 dark:bg-slate-400",
	}
)

type uiInsightRangeOption struct {
	Label  string
	Href   string
	HXGet  string
	Active bool
}

type uiInsightSprintOption struct {
	Ref    string
	Name   string
	Status model.SprintStatus
	Href   string
	HXGet  string
	Active bool
}

type uiInsightStat struct {
	Label string
	Value string
}

// uiInsightSeries is one legend entry. Mark picks the legend key shape: line,
// dashed, area, bar, or dot.
type uiInsightSeries struct {
	Key    string
	Label  string
	Value  string
	Mark   string
	Swatch string
	Dot    string
}

// KeyClass is the legend key, shaped like the series' mark.
func (s uiInsightSeries) KeyClass() string {
	switch s.Mark {
	case "dashed":
		return "w-3 border-t-2 border-dashed border-slate-500 dark:border-slate-400"
	case "line":
		return "h-0.5 w-3 rounded-full " + s.Swatch
	case "dot":
		return "h-2.5 w-2.5 rounded-full " + s.Swatch
	default:
		return "h-2.5 w-2.5 rounded-sm " + s.Swatch
	}
}

// MarkerClass colours the hover marker on a line or area edge.
func (s uiInsightSeries) MarkerClass() string {
	return s.Dot
}

type uiInsightTick struct {
	Label  string
	Pos    string
	Anchor string
}

type uiInsightPath struct {
	Series string
	D      string
	Class  string
	Dashed bool
}

type uiInsightBar struct {
	Series string
	X      string
	Width  string
	Y      string
	Height string
	Class  string
}

type uiInsightColumn struct {
	Index int
	Bars  []uiInsightBar
}

type uiInsightDot struct {
	Series string
	Href   string
	CX     string
	CY     string
	Class  string
	Label  string
	Title  string
	Value  string
	Detail string
}

type uiInsightRefLine struct {
	Series string
	Y      string
	Class  string
	Dashed bool
}

type uiInsightTableRow struct {
	Cells []string
	Href  string
}

type uiInsightTable struct {
	Caption string
	Headers []string
	Rows    []uiInsightTableRow
	// LeftColumns counts the leading text columns; the rest are numbers.
	LeftColumns int
}

// uiInsightChart is everything the insight-chart template draws. Kind is
// line, area, bars, or scatter.
type uiInsightChart struct {
	ID          string
	Title       string
	Description string
	Kind        string
	PlotLabel   string
	Stats       []uiInsightStat
	Series      []uiInsightSeries
	YTicks      []uiInsightTick
	XTicks      []uiInsightTick
	Areas       []uiInsightPath
	Lines       []uiInsightPath
	Columns     []uiInsightColumn
	Dots        []uiInsightDot
	RefLines    []uiInsightRefLine
	Data        string
	Table       uiInsightTable
	Empty       string
	Notes       []string
	// Sprints is the burn-up sprint picker; only the sprint burn-up sets it.
	Sprints      []uiInsightSprintOption
	SprintPicker string
}

type uiProjectInsightsData struct {
	Project        model.Project
	Range          model.InsightRange
	RangeOptions   []uiInsightRangeOption
	PeriodLabel    string
	SprintsEnabled bool
	Charts         []uiInsightChart
}

// uiInsightChartJSON is what app.js reads to draw the crosshair and tooltip.
type uiInsightChartJSON struct {
	Kind    string                `json:"kind"`
	Top     float64               `json:"top"`
	Labels  []string              `json:"labels"`
	Series  []uiInsightSeriesJSON `json:"series"`
	Details []uiInsightDetailJSON `json:"details,omitempty"`
}

type uiInsightSeriesJSON struct {
	Key    string     `json:"key"`
	Label  string     `json:"label"`
	Swatch string     `json:"swatch"`
	Dot    string     `json:"dot"`
	Values []*float64 `json:"values"`
	Text   []string   `json:"text,omitempty"`
}

// uiInsightDetailJSON is an extra tooltip row with no mark of its own.
type uiInsightDetailJSON struct {
	Label  string   `json:"label"`
	Values []string `json:"values"`
}

// uiInsightLineSeries is one plotted series before geometry is computed.
type uiInsightLineSeries struct {
	Key    string
	Label  string
	Color  uiInsightColor
	Values []*float64
	// Text overrides the tooltip value per point, for gaps such as an
	// unrecorded commitment.
	Text []string
}

func uiProjectInsightsPath(project model.Project, rng model.InsightRange, sprintRef string, panel bool) string {
	path := uiProjectPath(project) + "/insights"
	if panel {
		path += "/panel"
	}
	query := url.Values{}
	if rng != "" && rng != model.DefaultInsightRange {
		query.Set("range", string(rng))
	}
	if sprintRef != "" {
		query.Set("sprint", sprintRef)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}

func uiBuildProjectInsights(project model.Project, insights model.ProjectInsights, query projectInsightsQuery) *uiProjectInsightsData {
	selectedSprint := ""
	if query.SprintNumber > 0 {
		selectedSprint = model.SprintRef(query.SprintNumber)
	}
	data := &uiProjectInsightsData{
		Project:        project,
		Range:          insights.Range,
		PeriodLabel:    uiInsightPeriodLabel(insights),
		SprintsEnabled: insights.Sprints.Enabled,
	}
	for _, rng := range model.InsightRanges() {
		data.RangeOptions = append(data.RangeOptions, uiInsightRangeOption{
			Label:  rng.Label(),
			Href:   uiProjectInsightsPath(project, rng, selectedSprint, false),
			HXGet:  uiProjectInsightsPath(project, rng, selectedSprint, true),
			Active: rng == insights.Range,
		})
	}
	labels := uiInsightPeriodLabels(insights.Bucket, insights.Flow)
	data.Charts = append(data.Charts,
		uiInsightBurnupChart(insights, labels),
		uiInsightFlowChart(insights, labels),
		uiInsightThroughputChart(insights, labels),
		uiInsightCycleTimeChart(project, insights),
	)
	if insights.Sprints.Enabled {
		data.Charts = append(data.Charts,
			uiInsightSprintBurnupChart(project, insights),
			uiInsightVelocityChart(insights),
		)
	}
	return data
}

func uiInsightPeriodLabel(insights model.ProjectInsights) string {
	bucket := map[model.InsightBucket]string{
		model.InsightBucketDay:   "daily",
		model.InsightBucketWeek:  "weekly",
		model.InsightBucketMonth: "monthly",
	}[insights.Bucket]
	return fmt.Sprintf("%s to %s · %s · UTC", insights.Start.Format("Jan 2, 2006"), insights.End.Format("Jan 2, 2006"), bucket)
}

// uiInsightPeriodLabels names each flow period for tooltips and tables. The
// current period is marked as partial.
func uiInsightPeriodLabels(bucket model.InsightBucket, points []model.ProjectInsightFlowPoint) []string {
	labels := make([]string, len(points))
	for i, point := range points {
		var label string
		switch bucket {
		case model.InsightBucketWeek:
			label = "Week of " + point.PeriodStart.Format("Jan 2")
		case model.InsightBucketMonth:
			label = point.PeriodStart.Format("January 2006")
		default:
			label = point.PeriodStart.Format("Mon, Jan 2")
		}
		if i == len(points)-1 && point.PeriodEnd.Before(uiInsightNextPeriod(bucket, point.PeriodStart)) {
			label += " (so far)"
		}
		labels[i] = label
	}
	return labels
}

func uiInsightNextPeriod(bucket model.InsightBucket, start time.Time) time.Time {
	switch bucket {
	case model.InsightBucketWeek:
		return start.AddDate(0, 0, 7)
	case model.InsightBucketMonth:
		return start.AddDate(0, 1, 0)
	default:
		return start.AddDate(0, 0, 1)
	}
}

// uiInsightAxisLabel is the short x-axis label for a period start.
func uiInsightAxisLabel(bucket model.InsightBucket, start time.Time) string {
	if bucket == model.InsightBucketMonth {
		return start.Format("Jan 2006")
	}
	return start.Format("Jan 2")
}

func uiInsightFlowValues(points []model.ProjectInsightFlowPoint, pick func(model.ProjectInsightFlowPoint) int) []*float64 {
	values := make([]*float64, len(points))
	for i, point := range points {
		v := float64(pick(point))
		values[i] = &v
	}
	return values
}

func uiInsightBurnupSeries(points []model.ProjectInsightFlowPoint, total int) []uiInsightLineSeries {
	pad := func(values []*float64) []*float64 {
		for len(values) < total {
			values = append(values, nil)
		}
		return values
	}
	return []uiInsightLineSeries{
		{Key: "scope", Label: "Scope", Color: uiInsightNeutral, Values: pad(uiInsightFlowValues(points, func(p model.ProjectInsightFlowPoint) int { return p.Scope }))},
		{Key: "started", Label: "Started", Color: uiInsightBlue, Values: pad(uiInsightFlowValues(points, func(p model.ProjectInsightFlowPoint) int { return p.Started }))},
		{Key: "completed", Label: "Completed", Color: uiInsightGreen, Values: pad(uiInsightFlowValues(points, func(p model.ProjectInsightFlowPoint) int { return p.Completed }))},
	}
}

// uiInsightFlowPeriodsWithData counts periods that had any live issue.
func uiInsightFlowPeriodsWithData(points []model.ProjectInsightFlowPoint) int {
	count := 0
	for _, point := range points {
		if point.Scope > 0 || point.Cancelled > 0 {
			count++
		}
	}
	return count
}

// uiInsightFlowEmpty sets the empty state, or a note when a single period
// holds all the history so far and there is no trend to read yet.
func uiInsightFlowEmpty(chart *uiInsightChart, points []model.ProjectInsightFlowPoint) bool {
	switch uiInsightFlowPeriodsWithData(points) {
	case 0:
		chart.Empty = "No issues existed in this range."
		return true
	case 1:
		chart.Notes = append(chart.Notes, "Only the latest period has issues so far; the trend appears as history builds up.")
	}
	return false
}

func uiInsightBurnupChart(insights model.ProjectInsights, labels []string) uiInsightChart {
	points := insights.Flow
	chart := uiInsightChart{
		ID:          "insight-burnup",
		Title:       "Burn-up",
		Description: "Scope against started and completed issues at the end of each period. Cancelled issues leave scope; a reopened issue leaves Completed on the day it reopens.",
		Kind:        "line",
		PlotLabel:   "Burn-up chart. Use the arrow keys to read each period.",
	}
	series := uiInsightBurnupSeries(points, len(points))
	uiInsightLineChart(&chart, series, labels, uiInsightXTicksForPeriods(insights.Bucket, points), false)
	chart.Table = uiInsightFlowTable("Burn-up by period", labels, []string{"Scope", "Started", "Completed"}, points, func(p model.ProjectInsightFlowPoint) []int {
		return []int{p.Scope, p.Started, p.Completed}
	})
	if uiInsightFlowEmpty(&chart, points) {
		return chart
	}
	last := points[len(points)-1]
	chart.Stats = []uiInsightStat{
		{Label: "Complete", Value: uiInsightShare(last.Completed, last.Scope)},
		{Label: "Remaining", Value: strconv.Itoa(last.Scope - last.Completed)},
	}
	return chart
}

func uiInsightFlowChart(insights model.ProjectInsights, labels []string) uiInsightChart {
	points := insights.Flow
	chart := uiInsightChart{
		ID:          "insight-flow",
		Title:       "Cumulative flow",
		Description: "Issues in each status at the end of each period. A widening In progress band means work is starting faster than it finishes.",
		Kind:        "area",
		PlotLabel:   "Cumulative flow chart. Use the arrow keys to read each period.",
	}
	// Stacked bottom to top in workflow order, so finished work sits on the
	// baseline and the top edge is total open plus done scope.
	series := []uiInsightLineSeries{
		{Key: "done", Label: "Done", Color: uiInsightGreen, Values: uiInsightFlowValues(points, func(p model.ProjectInsightFlowPoint) int { return p.Done })},
		{Key: "in_progress", Label: "In progress", Color: uiInsightBlue, Values: uiInsightFlowValues(points, func(p model.ProjectInsightFlowPoint) int { return p.InProgress })},
		{Key: "todo", Label: "To do", Color: uiInsightNeutral, Values: uiInsightFlowValues(points, func(p model.ProjectInsightFlowPoint) int { return p.Todo })},
	}
	uiInsightLineChart(&chart, series, labels, uiInsightXTicksForPeriods(insights.Bucket, points), true)
	chart.Table = uiInsightFlowTable("Issues by status and period", labels, []string{"To do", "In progress", "Done", "Cancelled"}, points, func(p model.ProjectInsightFlowPoint) []int {
		return []int{p.Todo, p.InProgress, p.Done, p.Cancelled}
	})
	if uiInsightFlowEmpty(&chart, points) {
		return chart
	}
	total := 0
	for _, point := range points {
		total += point.InProgress
	}
	chart.Stats = []uiInsightStat{
		{Label: "Average in progress", Value: uiInsightDecimal(float64(total) / float64(len(points)))},
		{Label: "Cancelled", Value: strconv.Itoa(points[len(points)-1].Cancelled)},
	}
	chart.Notes = append(chart.Notes, "Cancelled issues are not stacked; the table lists them.")
	return chart
}

func uiInsightThroughputChart(insights model.ProjectInsights, labels []string) uiInsightChart {
	points := insights.Throughput
	chart := uiInsightChart{
		ID:          "insight-throughput",
		Title:       "Created vs resolved",
		Description: "Issues created and resolved in each period. Resolved counts every move to Done or Closed; reopening is shown alongside.",
		Kind:        "bars",
		PlotLabel:   "Created versus resolved chart. Use the arrow keys to read each period.",
	}
	created := make([]*float64, len(points))
	resolved := make([]*float64, len(points))
	reopened := make([]string, len(points))
	var totalCreated, totalResolved, totalReopened int
	maxValue := 0
	rows := make([]uiInsightTableRow, len(points))
	for i, point := range points {
		c, r := float64(point.Created), float64(point.Resolved)
		created[i], resolved[i] = &c, &r
		reopened[i] = strconv.Itoa(point.Reopened)
		totalCreated += point.Created
		totalResolved += point.Resolved
		totalReopened += point.Reopened
		maxValue = max(maxValue, point.Created, point.Resolved)
		rows[i] = uiInsightTableRow{Cells: []string{labels[i], strconv.Itoa(point.Created), strconv.Itoa(point.Resolved), strconv.Itoa(point.Reopened)}}
	}
	series := []uiInsightLineSeries{
		{Key: "created", Label: "Created", Color: uiInsightIndigo, Values: created},
		{Key: "resolved", Label: "Resolved", Color: uiInsightGreen, Values: resolved},
	}
	details := []uiInsightDetailJSON{{Label: "Reopened", Values: reopened}}
	uiInsightBarChart(&chart, series, labels, uiInsightXTicksForBars(insights.Bucket, insights.Flow), float64(maxValue), details)
	chart.Series[0].Value = strconv.Itoa(totalCreated)
	chart.Series[1].Value = strconv.Itoa(totalResolved)
	chart.Table = uiInsightTable{Caption: "Created and resolved issues by period", Headers: []string{"Period", "Created", "Resolved", "Reopened"}, Rows: rows, LeftColumns: 1}
	if totalCreated == 0 && totalResolved == 0 && totalReopened == 0 {
		chart.Empty = "No issues were created or resolved in this range."
		return chart
	}
	chart.Stats = []uiInsightStat{
		{Label: "Net open", Value: uiInsightSigned(totalCreated - totalResolved + totalReopened)},
		{Label: "Reopened", Value: strconv.Itoa(totalReopened)},
	}
	return chart
}

func uiInsightCycleTimeChart(project model.Project, insights model.ProjectInsights) uiInsightChart {
	cycle := insights.CycleTime
	chart := uiInsightChart{
		ID:          "insight-cycle-time",
		Title:       "Cycle time",
		Description: "Each Done issue, from its first move to In progress to its final move to Done, by completion date.",
		Kind:        "scatter",
		PlotLabel:   "Cycle time scatter plot. Each point links to its issue.",
		Series: []uiInsightSeries{
			{Key: "issues", Label: "Issues", Mark: "dot", Swatch: uiInsightIndigo.Swatch},
			{Key: "median", Label: "Median", Value: uiInsightDuration(cycle.MedianHours), Mark: "line", Swatch: uiInsightReference.Swatch},
			{Key: "p85", Label: "85th percentile", Value: uiInsightDuration(cycle.P85Hours), Mark: "dashed", Swatch: uiInsightReference.Swatch},
		},
	}
	rows := make([]uiInsightTableRow, 0, len(cycle.Issues))
	maxDays := cycle.P85Hours / 24
	for _, issue := range cycle.Issues {
		maxDays = math.Max(maxDays, issue.DurationHours/24)
	}
	top, step := uiInsightScale(maxDays, false)
	chart.YTicks = uiInsightYTicks(top, step, func(v float64) string { return uiInsightDecimal(v) + "d" })
	span := insights.End.Sub(insights.Start).Seconds()
	for _, issue := range cycle.Issues {
		href := uiIssuePath(model.Issue{OwnerUsername: project.OwnerUsername, Identifier: issue.Identifier})
		x := 0.5
		if span > 0 {
			x = math.Min(1, math.Max(0, issue.CompletedAt.Sub(insights.Start).Seconds()/span))
		}
		duration := uiInsightDuration(issue.DurationHours)
		detail := "Started " + issue.StartedAt.Format("Jan 2") + " · Done " + issue.CompletedAt.Format("Jan 2")
		chart.Dots = append(chart.Dots, uiInsightDot{
			Series: "issues",
			Href:   href,
			CX:     uiInsightPercent(x * 100),
			CY:     uiInsightPercent((1 - issue.DurationHours/24/top) * 100),
			Class:  uiInsightIndigo.Fill,
			Label:  fmt.Sprintf("%s %s: %s. %s", issue.Identifier, issue.Title, duration, detail),
			Title:  issue.Identifier,
			Value:  duration,
			Detail: issue.Title + "\n" + detail,
		})
		rows = append(rows, uiInsightTableRow{
			Href:  href,
			Cells: []string{issue.Identifier, issue.Title, issue.StartedAt.Format("Jan 2, 2006"), issue.CompletedAt.Format("Jan 2, 2006"), duration},
		})
	}
	chart.XTicks = uiInsightTimeTicks(insights.Start, insights.End)
	chart.Table = uiInsightTable{Caption: "Cycle time of completed issues", Headers: []string{"Issue", "Title", "Started", "Done", "Cycle time"}, Rows: rows, LeftColumns: 2}
	if cycle.NotStarted > 0 {
		chart.Notes = append(chart.Notes, fmt.Sprintf("%d %s went straight to Done without In progress, so %s no cycle time.", cycle.NotStarted, uiInsightPlural(cycle.NotStarted, "issue", "issues"), uiInsightPlural(cycle.NotStarted, "has", "have")))
	}
	if cycle.Truncated {
		chart.Notes = append(chart.Notes, fmt.Sprintf("Showing the %d most recent completions; the percentiles cover all of them.", len(cycle.Issues)))
	}
	if len(cycle.Issues) == 0 {
		chart.Empty = "No issues moved from In progress to Done in this range."
		chart.Series[1].Value, chart.Series[2].Value = "", ""
		return chart
	}
	chart.Series[0].Value = strconv.Itoa(len(cycle.Issues))
	chart.RefLines = []uiInsightRefLine{
		{Series: "median", Y: uiInsightPercent((1 - cycle.MedianHours/24/top) * 100), Class: uiInsightReference.Stroke},
		{Series: "p85", Y: uiInsightPercent((1 - cycle.P85Hours/24/top) * 100), Class: uiInsightReference.Stroke, Dashed: true},
	}
	return chart
}

func uiInsightSprintBurnupChart(project model.Project, insights model.ProjectInsights) uiInsightChart {
	chart := uiInsightChart{
		ID:          "insight-sprint-burnup",
		Title:       "Sprint burn-up",
		Description: "Scope against started and completed issues for one sprint, day by day. Scope moves when issues join or leave the sprint.",
		Kind:        "line",
		PlotLabel:   "Sprint burn-up chart. Use the arrow keys to read each day.",
	}
	burnup := insights.Sprints.Burnup
	for _, option := range insights.Sprints.Options {
		active := burnup != nil && burnup.SprintID == option.SprintID
		chart.Sprints = append(chart.Sprints, uiInsightSprintOption{
			Ref:    option.Ref,
			Name:   option.Name,
			Status: option.Status,
			Href:   uiProjectInsightsPath(project, insights.Range, option.Ref, false),
			HXGet:  uiProjectInsightsPath(project, insights.Range, option.Ref, true),
			Active: active,
		})
	}
	if burnup == nil {
		chart.Empty = "No active sprint, and no sprint was completed in this range."
		return chart
	}
	chart.SprintPicker = burnup.Ref
	if burnup.Name != "" {
		chart.SprintPicker += " · " + burnup.Name
	}
	days := make([]time.Time, burnup.Days)
	labels := make([]string, burnup.Days)
	for i := range days {
		days[i] = burnup.Start.AddDate(0, 0, i)
		labels[i] = days[i].Format("Mon, Jan 2")
		if i < len(burnup.Points) && burnup.Points[i].PeriodEnd.Before(days[i].AddDate(0, 0, 1)) {
			if burnup.Status == model.SprintStatusCompleted {
				labels[i] += " (at completion)"
			} else {
				labels[i] += " (so far)"
			}
		}
	}
	series := uiInsightBurnupSeries(burnup.Points, burnup.Days)
	uiInsightLineChart(&chart, series, labels, uiInsightDayTicks(days), false)
	rows := make([]uiInsightTableRow, len(burnup.Points))
	for i, point := range burnup.Points {
		rows[i] = uiInsightTableRow{Cells: []string{labels[i], strconv.Itoa(point.Scope), strconv.Itoa(point.Started), strconv.Itoa(point.Completed)}}
	}
	chart.Table = uiInsightTable{Caption: "Sprint burn-up by day", Headers: []string{"Day", "Scope", "Started", "Completed"}, Rows: rows, LeftColumns: 1}
	if burnup.Estimated {
		chart.Notes = append(chart.Notes, "This sprint predates sprint membership history, so its scope is estimated from the issues it held when history began.")
	}
	if len(burnup.Points) == 0 {
		chart.Empty = "This sprint has not started yet."
		return chart
	}
	if len(burnup.Points) < 2 {
		chart.Notes = append(chart.Notes, "The sprint started today; the lines appear after its first full day.")
	}
	last := burnup.Points[len(burnup.Points)-1]
	chart.Stats = []uiInsightStat{
		{Label: "Completed", Value: fmt.Sprintf("%d of %d", last.Completed, last.Scope)},
		{Label: "Complete", Value: uiInsightShare(last.Completed, last.Scope)},
	}
	if burnup.Status == model.SprintStatusActive {
		if remaining := int(burnup.End.Sub(last.PeriodEnd).Hours() / 24); remaining > 0 {
			chart.Stats = append(chart.Stats, uiInsightStat{Label: "Days left", Value: strconv.Itoa(remaining)})
		}
	}
	return chart
}

func uiInsightVelocityChart(insights model.ProjectInsights) uiInsightChart {
	velocity := insights.Sprints.Velocity
	chart := uiInsightChart{
		ID:          "insight-velocity",
		Title:       "Velocity",
		Description: "Issues committed when each sprint started against issues Done when it completed.",
		Kind:        "bars",
		PlotLabel:   "Velocity chart. Use the arrow keys to read each sprint.",
	}
	committed := make([]*float64, len(velocity))
	committedText := make([]string, len(velocity))
	completed := make([]*float64, len(velocity))
	labels := make([]string, len(velocity))
	scope := make([]string, len(velocity))
	cancelled := make([]string, len(velocity))
	carried := make([]string, len(velocity))
	rows := make([]uiInsightTableRow, len(velocity))
	maxValue, totalDone, unrecorded := 0, 0, 0
	var ticks []uiInsightTick
	for i, sprint := range velocity {
		done := float64(sprint.Done)
		completed[i] = &done
		committedText[i] = "Not recorded"
		if sprint.Committed != nil {
			c := float64(*sprint.Committed)
			committed[i] = &c
			committedText[i] = strconv.Itoa(*sprint.Committed)
			maxValue = max(maxValue, *sprint.Committed)
		} else {
			unrecorded++
		}
		maxValue = max(maxValue, sprint.Done)
		totalDone += sprint.Done
		labels[i] = sprint.Ref
		if sprint.Name != "" {
			labels[i] += " · " + sprint.Name
		}
		scope[i] = strconv.Itoa(sprint.Total)
		cancelled[i] = strconv.Itoa(sprint.Cancelled)
		carried[i] = strconv.Itoa(sprint.CarriedOver)
		rows[i] = uiInsightTableRow{Cells: []string{labels[i], committedText[i], strconv.Itoa(sprint.Done), cancelled[i], carried[i], scope[i]}}
	}
	for _, i := range uiInsightTickIndexes(len(velocity)) {
		ticks = append(ticks, uiInsightTick{Label: velocity[i].Ref, Pos: uiInsightPercent((float64(i) + 0.5) / float64(len(velocity)) * 100), Anchor: "middle"})
	}
	series := []uiInsightLineSeries{
		{Key: "committed", Label: "Committed", Color: uiInsightNeutral, Values: committed, Text: committedText},
		{Key: "completed", Label: "Completed", Color: uiInsightGreen, Values: completed},
	}
	details := []uiInsightDetailJSON{
		{Label: "Cancelled", Values: cancelled},
		{Label: "Carried over", Values: carried},
		{Label: "In sprint at completion", Values: scope},
	}
	uiInsightBarChart(&chart, series, labels, ticks, float64(maxValue), details)
	chart.Table = uiInsightTable{Caption: "Velocity by sprint", Headers: []string{"Sprint", "Committed", "Completed", "Cancelled", "Carried over", "In sprint at completion"}, Rows: rows, LeftColumns: 1}
	if len(velocity) == 0 {
		chart.Empty = "No sprints were completed in this range."
		return chart
	}
	chart.Stats = []uiInsightStat{
		{Label: "Average completed", Value: uiInsightDecimal(float64(totalDone) / float64(len(velocity)))},
		{Label: "Sprints", Value: strconv.Itoa(len(velocity))},
	}
	if unrecorded > 0 {
		chart.Notes = append(chart.Notes, fmt.Sprintf("Commitment was not recorded for %d %s that started before sprint history was kept.", unrecorded, uiInsightPlural(unrecorded, "sprint", "sprints")))
	}
	return chart
}

// uiInsightLineChart fills line or stacked-area geometry, ticks, legend, and
// the tooltip JSON. Values may be nil past the present; lines stop there.
func uiInsightLineChart(chart *uiInsightChart, series []uiInsightLineSeries, labels []string, xTicks []uiInsightTick, stacked bool) {
	n := len(labels)
	maxValue := 0.0
	stack := make([]float64, n)
	for _, s := range series {
		for i, v := range s.Values {
			if v == nil {
				continue
			}
			if stacked {
				stack[i] += *v
				maxValue = math.Max(maxValue, stack[i])
			} else {
				maxValue = math.Max(maxValue, *v)
			}
		}
	}
	top, step := uiInsightScale(maxValue, true)
	chart.YTicks = uiInsightYTicks(top, step, uiInsightDecimal)
	chart.XTicks = xTicks
	payload := uiInsightChartJSON{Kind: chart.Kind, Top: top, Labels: labels}
	base := make([]float64, n)
	mark := "line"
	if stacked {
		mark = "area"
	}
	for _, s := range series {
		latest := ""
		for i := len(s.Values) - 1; i >= 0; i-- {
			if s.Values[i] != nil {
				latest = uiInsightDecimal(*s.Values[i])
				break
			}
		}
		chart.Series = append(chart.Series, uiInsightSeries{Key: s.Key, Label: s.Label, Value: latest, Mark: mark, Swatch: s.Color.Swatch, Dot: s.Color.Fill})
		payload.Series = append(payload.Series, uiInsightSeriesJSON{Key: s.Key, Label: s.Label, Swatch: s.Color.Swatch, Dot: s.Color.Fill, Values: s.Values})
		if stacked {
			upper := make([]float64, n)
			for i := range upper {
				upper[i] = base[i]
				if i < len(s.Values) && s.Values[i] != nil {
					upper[i] += *s.Values[i]
				}
			}
			chart.Areas = append(chart.Areas, uiInsightPath{Series: s.Key, D: uiInsightAreaPath(upper, base, top), Class: s.Color.Area})
			chart.Lines = append(chart.Lines, uiInsightPath{Series: s.Key, D: uiInsightLinePath(uiInsightPointers(upper), top), Class: s.Color.Stroke})
			base = upper
			continue
		}
		chart.Lines = append(chart.Lines, uiInsightPath{Series: s.Key, D: uiInsightLinePath(s.Values, top), Class: s.Color.Stroke})
	}
	chart.Data = uiInsightJSON(payload)
}

// uiInsightBarChart fills grouped bars, one column per period.
func uiInsightBarChart(chart *uiInsightChart, series []uiInsightLineSeries, labels []string, xTicks []uiInsightTick, maxValue float64, details []uiInsightDetailJSON) {
	top, step := uiInsightScale(maxValue, true)
	chart.YTicks = uiInsightYTicks(top, step, uiInsightDecimal)
	chart.XTicks = xTicks
	payload := uiInsightChartJSON{Kind: chart.Kind, Top: top, Labels: labels, Details: details}
	width := 100.0 / float64(len(series))
	for i := range labels {
		column := uiInsightColumn{Index: i}
		for j, s := range series {
			if s.Values[i] == nil || *s.Values[i] <= 0 {
				continue
			}
			height := *s.Values[i] / top * 100
			column.Bars = append(column.Bars, uiInsightBar{
				Series: s.Key,
				X:      uiInsightPercent(float64(j)*width + width*0.1),
				Width:  uiInsightPercent(width * 0.8),
				Y:      uiInsightPercent(100 - height),
				Height: uiInsightPercent(height),
				Class:  s.Color.Fill,
			})
		}
		chart.Columns = append(chart.Columns, column)
	}
	for _, s := range series {
		chart.Series = append(chart.Series, uiInsightSeries{Key: s.Key, Label: s.Label, Mark: "bar", Swatch: s.Color.Swatch})
		payload.Series = append(payload.Series, uiInsightSeriesJSON{Key: s.Key, Label: s.Label, Swatch: s.Color.Swatch, Dot: s.Color.Fill, Values: s.Values, Text: s.Text})
	}
	chart.Data = uiInsightJSON(payload)
}

func uiInsightJSON(payload uiInsightChartJSON) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		// Defensive: the payload is strings and finite floats only.
		return "{}"
	}
	return string(raw)
}

func uiInsightPointers(values []float64) []*float64 {
	out := make([]*float64, len(values))
	for i := range values {
		out[i] = &values[i]
	}
	return out
}

func uiInsightX(i, n int) float64 {
	if n <= 1 {
		return uiInsightViewBox / 2
	}
	return float64(i) / float64(n-1) * uiInsightViewBox
}

func uiInsightY(v, top float64) float64 {
	return uiInsightViewBox - v/top*uiInsightViewBox
}

// uiInsightLinePath draws a polyline in viewBox units, breaking at nil values.
func uiInsightLinePath(values []*float64, top float64) string {
	var b strings.Builder
	pen := false
	for i, v := range values {
		if v == nil {
			pen = false
			continue
		}
		command := "L"
		if !pen {
			command = "M"
		}
		fmt.Fprintf(&b, "%s%s,%s", command, uiInsightCoord(uiInsightX(i, len(values))), uiInsightCoord(uiInsightY(*v, top)))
		pen = true
	}
	return b.String()
}

// uiInsightAreaPath fills between an upper and lower edge.
func uiInsightAreaPath(upper, lower []float64, top float64) string {
	if len(upper) == 0 {
		return ""
	}
	var b strings.Builder
	for i, v := range upper {
		command := "L"
		if i == 0 {
			command = "M"
		}
		fmt.Fprintf(&b, "%s%s,%s", command, uiInsightCoord(uiInsightX(i, len(upper))), uiInsightCoord(uiInsightY(v, top)))
	}
	for i := len(lower) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "L%s,%s", uiInsightCoord(uiInsightX(i, len(lower))), uiInsightCoord(uiInsightY(lower[i], top)))
	}
	b.WriteString("Z")
	return b.String()
}

func uiInsightCoord(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

func uiInsightPercent(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64) + "%"
}

// uiInsightScale picks a round axis top and tick step: steps of 1, 2, or 5
// times a power of ten, two to five intervals, and whole numbers for counts.
func uiInsightScale(maxValue float64, integer bool) (float64, float64) {
	if maxValue <= 0 || math.IsNaN(maxValue) || math.IsInf(maxValue, 0) {
		return 4, 1
	}
	magnitude := math.Pow(10, math.Floor(math.Log10(maxValue/5)))
	step := magnitude
	for _, candidate := range []float64{1, 2, 5, 10} {
		step = candidate * magnitude
		if math.Ceil(maxValue/step-1e-9) <= 5 {
			break
		}
	}
	if integer && step < 1 {
		step = 1
	}
	intervals := math.Max(2, math.Ceil(maxValue/step-1e-9))
	return intervals * step, step
}

func uiInsightYTicks(top, step float64, format func(float64) string) []uiInsightTick {
	var ticks []uiInsightTick
	for v := 0.0; v <= top+step/2; v += step {
		ticks = append(ticks, uiInsightTick{Label: format(v), Pos: uiInsightPercent((1 - v/top) * 100)})
	}
	return ticks
}

// uiInsightTickIndexes picks up to five evenly spread indexes, always
// including the first and last.
func uiInsightTickIndexes(n int) []int {
	if n <= 0 {
		return nil
	}
	if n <= 5 {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	seen := map[int]bool{}
	var out []int
	for k := 0; k <= 4; k++ {
		i := int(math.Round(float64(k) * float64(n-1) / 4))
		if !seen[i] {
			seen[i] = true
			out = append(out, i)
		}
	}
	return out
}

func uiInsightXTicksForPeriods(bucket model.InsightBucket, points []model.ProjectInsightFlowPoint) []uiInsightTick {
	var ticks []uiInsightTick
	indexes := uiInsightTickIndexes(len(points))
	for k, i := range indexes {
		ticks = append(ticks, uiInsightTick{
			Label:  uiInsightAxisLabel(bucket, points[i].PeriodStart),
			Pos:    uiInsightPercent(uiInsightX(i, len(points)) / uiInsightViewBox * 100),
			Anchor: uiInsightEdgeAnchor(k, len(indexes)),
		})
	}
	return ticks
}

func uiInsightXTicksForBars(bucket model.InsightBucket, points []model.ProjectInsightFlowPoint) []uiInsightTick {
	var ticks []uiInsightTick
	for _, i := range uiInsightTickIndexes(len(points)) {
		ticks = append(ticks, uiInsightTick{
			Label:  uiInsightAxisLabel(bucket, points[i].PeriodStart),
			Pos:    uiInsightPercent((float64(i) + 0.5) / float64(len(points)) * 100),
			Anchor: "middle",
		})
	}
	return ticks
}

func uiInsightDayTicks(days []time.Time) []uiInsightTick {
	var ticks []uiInsightTick
	indexes := uiInsightTickIndexes(len(days))
	for k, i := range indexes {
		ticks = append(ticks, uiInsightTick{
			Label:  days[i].Format("Jan 2"),
			Pos:    uiInsightPercent(uiInsightX(i, len(days)) / uiInsightViewBox * 100),
			Anchor: uiInsightEdgeAnchor(k, len(indexes)),
		})
	}
	return ticks
}

// uiInsightTimeTicks labels a continuous time axis at five even points.
func uiInsightTimeTicks(start, end time.Time) []uiInsightTick {
	span := end.Sub(start)
	var ticks []uiInsightTick
	for k := 0; k <= 4; k++ {
		at := start.Add(time.Duration(float64(span) * float64(k) / 4))
		ticks = append(ticks, uiInsightTick{Label: at.Format("Jan 2"), Pos: uiInsightPercent(float64(k) * 25), Anchor: uiInsightEdgeAnchor(k, 5)})
	}
	return ticks
}

func uiInsightEdgeAnchor(k, n int) string {
	switch {
	case n > 1 && k == 0:
		return "start"
	case n > 1 && k == n-1:
		return "end"
	default:
		return "middle"
	}
}

func uiInsightFlowTable(caption string, labels, headers []string, points []model.ProjectInsightFlowPoint, pick func(model.ProjectInsightFlowPoint) []int) uiInsightTable {
	table := uiInsightTable{Caption: caption, Headers: append([]string{"Period"}, headers...), LeftColumns: 1}
	for i, point := range points {
		cells := []string{labels[i]}
		for _, v := range pick(point) {
			cells = append(cells, strconv.Itoa(v))
		}
		table.Rows = append(table.Rows, uiInsightTableRow{Cells: cells})
	}
	return table
}

func uiInsightDecimal(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

// uiInsightShare is part as a whole-number percentage of total.
func uiInsightShare(part, total int) string {
	if total <= 0 {
		return "0%"
	}
	return strconv.Itoa(int(math.Round(float64(part)/float64(total)*100))) + "%"
}

func uiInsightSigned(v int) string {
	if v > 0 {
		return "+" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

func uiInsightDuration(hours float64) string {
	switch {
	case hours <= 0:
		return "0 hours"
	case hours < 1:
		return "Under an hour"
	case hours < 24:
		h := int(math.Round(hours))
		return fmt.Sprintf("%d %s", h, uiInsightPlural(h, "hour", "hours"))
	default:
		days := uiInsightDecimal(hours / 24)
		if days == "1" {
			return "1 day"
		}
		return days + " days"
	}
}

func uiInsightPlural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
