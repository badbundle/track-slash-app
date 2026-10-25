package server

import (
	"net/url"
	"strconv"

	"github.com/bradleymackey/track-slash/internal/model"
)

// uiProjectTimelineData backs the Timeline view: a Gantt chart of the
// project's issues, grouped by sprint when the project runs sprints. Every
// position is a percentage of the chart's width, so the SVG keeps its shape at
// any width without an inline style, which the CSP forbids.
type uiProjectTimelineData struct {
	Range          model.InsightRange
	RangeOptions   []uiRangeOption
	FrameLabel     string
	Ticks          []uiTimelineTick
	TodayPos       string
	Groups         []uiTimelineGroup
	Empty          bool
	SprintsEnabled bool
	// Notes say what the chart leaves out.
	Notes []string
}

// uiTimelineTick is one labelled gridline on the chart's date axis.
type uiTimelineTick struct {
	Pos   string
	Label string
}

// uiTimelineGroup is one sprint's rows, or the rows in no sprint. Heading is
// empty for the one group of a project without sprints.
type uiTimelineGroup struct {
	Sprint  *model.Sprint
	Heading string
	Status  string
	Dates   string
	Window  *uiTimelineBar
	Rows    []uiTimelineRow
	HasMore bool
}

// uiTimelineRow is one issue: its link, its bar, and the same dates as text.
type uiTimelineRow struct {
	Issue   model.Issue
	Dates   string
	Summary string
	Bar     *uiTimelineBar
	DuePos  string
	// Window repeats the group's sprint band behind the row.
	Window *uiTimelineBar
}

// uiTimelineBar is a span of days on the chart. Clipped marks a bar that runs
// on before the chart's first day, which a bounded range cuts off.
type uiTimelineBar struct {
	X       string
	Width   string
	Class   string
	Dashed  bool
	Clipped bool
}

// uiTimelineMinWidth keeps a one-day bar visible on a long chart.
const uiTimelineMinWidth = 0.6

func uiProjectTimelinePath(project model.Project, rng model.InsightRange, panel bool) string {
	path := uiProjectPath(project) + "/timeline"
	if panel {
		path += "/panel"
	}
	if rng != "" && rng != model.DefaultInsightRange {
		path += "?" + url.Values{"range": {string(rng)}}.Encode()
	}
	return path
}

// uiTimelineFrame converts days into positions across a chart that runs from
// From to To, both included.
type uiTimelineFrame struct {
	From, To model.Date
	days     int
}

func newUITimelineFrame(from, to model.Date) uiTimelineFrame {
	return uiTimelineFrame{From: from, To: to, days: uiTimelineDaysBetween(from, to) + 1}
}

func uiTimelineDaysBetween(from, to model.Date) int {
	return int(to.Time().Sub(from.Time()).Hours() / 24)
}

// pos is where day begins, as a percentage of the chart.
func (f uiTimelineFrame) pos(day model.Date) float64 {
	return float64(uiTimelineDaysBetween(f.From, day)) / float64(f.days) * 100
}

// bar spans start to end, both days included, cut off at the chart's first
// day and never thinner than uiTimelineMinWidth.
func (f uiTimelineFrame) bar(start, end model.Date, class string) *uiTimelineBar {
	bar := &uiTimelineBar{Class: class}
	if start.Before(f.From) {
		start, bar.Clipped = f.From, true
	}
	if end.Before(start) {
		end = start
	}
	x := f.pos(start)
	width := f.pos(end.AddDays(1)) - x
	if width < uiTimelineMinWidth {
		width = uiTimelineMinWidth
	}
	if x+width > 100 {
		x = 100 - width
	}
	bar.X, bar.Width = uiInsightPercent(x), uiInsightPercent(width)
	return bar
}

// ticks labels the axis at a step that suits the chart's length: weeks (on
// Mondays) up to ten weeks, then months, then quarters. A tick too close
// to the right edge for its label is left off.
func (f uiTimelineFrame) ticks() []uiTimelineTick {
	var step func(model.Date) model.Date
	first := f.From
	label := func(day model.Date) string { return uiTimelineDayLabel(day, false) }
	switch {
	case f.days <= 70:
		for first.Time().Weekday() != 1 {
			first = first.AddDays(1)
		}
		step = func(day model.Date) model.Date { return day.AddDays(7) }
	default:
		months := 1
		if f.days > 400 {
			months = 3
		}
		t := f.From.Time()
		first = model.Date(t.AddDate(0, 0, 1-t.Day()))
		for first.Before(f.From) || (int(first.Time().Month())-1)%months != 0 {
			first = model.Date(first.Time().AddDate(0, 1, 0))
		}
		step = func(day model.Date) model.Date { return model.Date(day.Time().AddDate(0, months, 0)) }
		label = func(day model.Date) string {
			if day.Time().Month() == 1 || months == 3 {
				return day.Time().Format("Jan 2006")
			}
			return day.Time().Format("Jan")
		}
	}
	var ticks []uiTimelineTick
	for day := first; !f.To.Before(day); day = step(day) {
		pos := f.pos(day)
		if pos > 90 {
			break
		}
		ticks = append(ticks, uiTimelineTick{Pos: uiInsightPercent(pos), Label: label(day)})
	}
	return ticks
}

// uiTimelineDayLabel is a short date, with the year when it isn't this one's.
func uiTimelineDayLabel(day model.Date, withYear bool) string {
	if withYear {
		return day.Time().Format("Jan 2, 2006")
	}
	return day.Time().Format("Jan 2")
}

func uiTimelineSpanLabel(start, end, today model.Date) string {
	withYear := start.Time().Year() != today.Time().Year() || end.Time().Year() != today.Time().Year()
	if start == end {
		return uiTimelineDayLabel(start, withYear)
	}
	return uiTimelineDayLabel(start, withYear) + " – " + uiTimelineDayLabel(end, withYear)
}

// uiTimelineBarClass colours a bar by its issue's status, as Insights colours
// work: neutral for To do, blue once started, emerald when done. A closed
// (cancelled) issue is an outline, so it reads apart from To do without
// colour.
func uiTimelineBarClass(status model.Status) (string, bool) {
	switch status {
	case model.StatusInProgress:
		return uiInsightBlue.Fill, false
	case model.StatusDone:
		return uiInsightGreen.Fill, false
	case model.StatusClosed:
		return "fill-white stroke-slate-400 dark:fill-slate-900 dark:stroke-slate-500", true
	default:
		return uiInsightNeutral.Fill, false
	}
}

// uiTimelineSummary says in words what a row's bar shows, for screen readers.
func uiTimelineSummary(item model.TimelineItem, today model.Date) string {
	summary := uiStatusLabel(item.Status) + "."
	if item.Start == nil {
		summary += " No dates."
	} else {
		summary += " " + uiTimelineSpanLabel(*item.Start, *item.End, today) + "."
	}
	if item.DueDate != nil {
		summary += " Due " + uiTimelineDayLabel(*item.DueDate, item.DueDate.Time().Year() != today.Time().Year()) + "."
	}
	return summary
}

func uiBuildProjectTimeline(project model.Project, timeline model.ProjectTimeline) *uiProjectTimelineData {
	frame := newUITimelineFrame(timeline.From, timeline.To)
	data := &uiProjectTimelineData{
		Range:          timeline.Range,
		FrameLabel:     uiTimelineSpanLabel(timeline.From, timeline.To, timeline.Today),
		Ticks:          frame.ticks(),
		TodayPos:       uiInsightPercent(frame.pos(timeline.Today) + 50/float64(frame.days)),
		SprintsEnabled: timeline.SprintsEnabled,
		Empty:          true,
	}
	for _, rng := range model.InsightRanges() {
		data.RangeOptions = append(data.RangeOptions, uiRangeOption{
			Label:  rng.Label(),
			Href:   uiProjectTimelinePath(project, rng, false),
			HXGet:  uiProjectTimelinePath(project, rng, true),
			Active: rng == timeline.Range,
		})
	}
	for _, group := range timeline.Groups {
		out := uiTimelineGroup{Sprint: group.Sprint, HasMore: group.HasMore}
		switch {
		case group.Sprint != nil:
			// A sprint with nothing in it still marks its window on the chart.
			data.Empty = false
			out.Heading = group.Sprint.Name
			out.Status = uiTimelineSprintStatus(group.Sprint.Status)
			out.Dates = "No dates"
			if start, end := group.Sprint.StartDate, group.Sprint.EndDate; start != nil && end != nil {
				out.Dates = uiTimelineSpanLabel(model.DateFromTime(*start), model.DateFromTime(*end), timeline.Today)
				out.Window = frame.bar(model.DateFromTime(*start), model.DateFromTime(*end), "")
			}
		case timeline.SprintsEnabled:
			out.Heading = "Not in a sprint"
		}
		for _, item := range group.Items {
			row := uiTimelineRow{Issue: item.Issue, Dates: "No dates", Summary: uiTimelineSummary(item, timeline.Today), Window: out.Window}
			if item.Start != nil {
				class, dashed := uiTimelineBarClass(item.Status)
				row.Bar = frame.bar(*item.Start, *item.End, class)
				row.Bar.Dashed = dashed
				row.Dates = uiTimelineSpanLabel(*item.Start, *item.End, timeline.Today)
			}
			if item.DueDate != nil && !item.DueDate.Before(frame.From) {
				row.DuePos = uiInsightPercent(frame.pos(*item.DueDate) + 50/float64(frame.days))
			}
			out.Rows = append(out.Rows, row)
			data.Empty = false
		}
		data.Groups = append(data.Groups, out)
	}
	data.Notes = uiTimelineNotes(timeline)
	return data
}

// uiTimelineSprintStatus names where a sprint stands, beside its name.
func uiTimelineSprintStatus(status model.SprintStatus) string {
	switch status {
	case model.SprintStatusActive:
		return "Active"
	case model.SprintStatusCompleted:
		return "Completed"
	default:
		return "Planned"
	}
}

// uiTimelineNotes says what the chart leaves out, one line each.
func uiTimelineNotes(timeline model.ProjectTimeline) []string {
	var notes []string
	switch n := timeline.Unscheduled; {
	case n == 1:
		notes = append(notes, "1 open issue has no due date or sprint, so it isn't drawn.")
	case n > 1:
		notes = append(notes, strconv.Itoa(n)+" open issues have no due date or sprint, so they aren't drawn.")
	}
	if timeline.MoreCompletedSprints {
		notes = append(notes, "Showing the "+strconv.Itoa(timelineCompletedSprintLimit)+" most recent completed sprints in this range.")
	}
	if timeline.MorePlannedSprints {
		notes = append(notes, "Showing the next "+strconv.Itoa(timelinePlannedSprintLimit)+" planned sprints.")
	}
	return notes
}
