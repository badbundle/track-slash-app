package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bradleymackey/track-slash/internal/model"
)

const (
	// insightCycleTimeLimit caps the scatter so an all-time range on a large
	// project stays a light payload. Percentiles still cover every issue.
	insightCycleTimeLimit = 500
	// insightVelocityLimit keeps the most recent completed sprints.
	insightVelocityLimit = 26
	// insightSprintBurnupMaxDays bounds a sprint burn-up to a readable window.
	insightSprintBurnupMaxDays = 92
	// insightMinimumAllDays keeps an all-time range on a new project from
	// collapsing to a single day.
	insightMinimumAllDays = 14
)

type ProjectInsightsParams struct {
	ProjectID uuid.UUID
	Range     model.InsightRange
	// SprintID selects the sprint burn-up. Nil picks the active sprint, then
	// the most recently completed sprint in range.
	SprintID *uuid.UUID
	Now      time.Time
}

type insightPeriod struct {
	Start time.Time
	End   time.Time
}

// GetProjectInsights derives every insight series from persisted history:
// issue creation times, status changes in the project changelog, sprint
// membership history, and sprint completion snapshots. Current issue state is
// only used for an issue's status before its first recorded change.
//
// Every query here is a read inside one read-only transaction, so the bare
// `return err` paths below are defensive: only a database failure reaches
// them, and they pass through unmapped.
func (s *Store) GetProjectInsights(ctx context.Context, p ProjectInsightsParams) (model.ProjectInsights, error) {
	rng := p.Range
	if rng == "" {
		rng = model.DefaultInsightRange
	}
	if !rng.Valid() {
		return model.ProjectInsights{}, fmt.Errorf("invalid insight range %q: %w", rng, ErrConflict)
	}
	project, err := s.GetProject(ctx, p.ProjectID)
	if err != nil {
		return model.ProjectInsights{}, err
	}
	now := p.Now.UTC()
	if p.Now.IsZero() {
		now = time.Now().UTC()
	}

	var out model.ProjectInsights
	err = pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		earliest := project.CreatedAt.UTC()
		if rng == model.InsightRangeAll {
			var firstIssue *time.Time
			if err := tx.QueryRow(ctx, `
				SELECT min(created_at) FROM issues WHERE project_id = $1 AND deleted_at IS NULL
			`, p.ProjectID).Scan(&firstIssue); err != nil {
				return err
			}
			if firstIssue != nil && firstIssue.Before(earliest) {
				earliest = firstIssue.UTC()
			}
		}
		bucket, periods := insightWindow(rng, now, earliest)
		out = model.ProjectInsights{
			ProjectID: p.ProjectID,
			Range:     rng,
			Bucket:    bucket,
			Start:     periods[0].Start,
			End:       now,
		}
		out.Flow, out.Throughput, err = insightFlowAndThroughput(ctx, tx, p.ProjectID, periods)
		if err != nil {
			return err
		}
		out.CycleTime, err = insightCycleTime(ctx, tx, project, out.Start, now)
		if err != nil {
			return err
		}
		out.Sprints, err = insightSprints(ctx, tx, p.ProjectID, p.SprintID, out.Start, now)
		return err
	})
	if err != nil {
		return model.ProjectInsights{}, err
	}
	return out, nil
}

// insightWindow splits a range into consecutive periods ending at now. Day and
// week periods start at 00:00 UTC (weeks on Monday); the last period is cut
// off at now.
func insightWindow(rng model.InsightRange, now, earliest time.Time) (model.InsightBucket, []insightPeriod) {
	today := insightDayStart(now)
	switch rng {
	case model.InsightRangeTwoWeeks:
		return model.InsightBucketDay, insightPeriods(model.InsightBucketDay, today.AddDate(0, 0, -13), now)
	case model.InsightRangeNinetyDays:
		return model.InsightBucketWeek, insightPeriods(model.InsightBucketWeek, insightWeekStart(now).AddDate(0, 0, -7*12), now)
	case model.InsightRangeAll:
		start := insightDayStart(earliest)
		if minimum := today.AddDate(0, 0, -(insightMinimumAllDays - 1)); minimum.Before(start) {
			start = minimum
		}
		days := int(today.Sub(start).Hours()/24) + 1
		switch {
		case days <= 31:
			return model.InsightBucketDay, insightPeriods(model.InsightBucketDay, start, now)
		case days <= 26*7:
			return model.InsightBucketWeek, insightPeriods(model.InsightBucketWeek, insightWeekStart(start), now)
		default:
			return model.InsightBucketMonth, insightPeriods(model.InsightBucketMonth, insightMonthStart(start), now)
		}
	default:
		return model.InsightBucketDay, insightPeriods(model.InsightBucketDay, today.AddDate(0, 0, -29), now)
	}
}

func insightPeriods(bucket model.InsightBucket, start, now time.Time) []insightPeriod {
	var periods []insightPeriod
	for periodStart := start; periodStart.Before(now); {
		next := insightNextPeriod(bucket, periodStart)
		end := next
		if end.After(now) {
			end = now
		}
		periods = append(periods, insightPeriod{Start: periodStart, End: end})
		periodStart = next
	}
	return periods
}

func insightNextPeriod(bucket model.InsightBucket, start time.Time) time.Time {
	switch bucket {
	case model.InsightBucketWeek:
		return start.AddDate(0, 0, 7)
	case model.InsightBucketMonth:
		return start.AddDate(0, 1, 0)
	default:
		return start.AddDate(0, 0, 1)
	}
}

func insightDayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func insightWeekStart(t time.Time) time.Time {
	day := insightDayStart(t)
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

func insightMonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// insightStatusSQL maps a changelog status label back to the issue_status
// value. Labels come from changelogStatusLabel; an unknown label maps to NULL
// and the change is ignored.
func insightStatusSQL(expr string) string {
	return `CASE ` + expr + `
		WHEN 'To do' THEN 'todo'
		WHEN 'In progress' THEN 'in_progress'
		WHEN 'Done' THEN 'done'
		WHEN 'Closed' THEN 'closed'
	END`
}

// insightIssueHistoryCTE rebuilds each live issue's status timeline from the
// project changelog. $1 is the project ID. A change stamped before its issue
// was created (only possible with imported or hand-edited rows) is moved to
// the creation time so intervals never run backwards.
var insightIssueHistoryCTE = `
live_issues AS (
	SELECT i.id, i.created_at, i.status::text AS status
	FROM issues i
	WHERE i.project_id = $1 AND i.deleted_at IS NULL
),
status_changes AS (
	SELECT e.entity_id AS issue_id,
	       GREATEST(e.created_at, li.created_at) AS at,
	       e.id AS entry_id,
	       ` + insightStatusSQL(`c.value->>'from'`) + ` AS from_status,
	       ` + insightStatusSQL(`c.value->>'to'`) + ` AS to_status
	FROM project_changelog_entries e
	JOIN live_issues li ON li.id = e.entity_id
	CROSS JOIN LATERAL jsonb_array_elements(
		CASE WHEN jsonb_typeof(e.details->'changes') = 'array' THEN e.details->'changes' ELSE '[]'::jsonb END
	) AS c(value)
	WHERE e.project_id = $1
	  AND e.entity = 'issue'
	  AND e.op = 'update'
	  AND c.value->>'field' = 'status'
),
status_points AS (
	SELECT li.id AS issue_id, li.created_at AS at, 0 AS seq, NULL::uuid AS entry_id,
	       COALESCE((
	           SELECT sc.from_status
	           FROM status_changes sc
	           WHERE sc.issue_id = li.id
	           ORDER BY sc.at, sc.entry_id
	           LIMIT 1
	       ), li.status) AS status
	FROM live_issues li
	UNION ALL
	SELECT sc.issue_id, sc.at, 1, sc.entry_id, sc.to_status
	FROM status_changes sc
	WHERE sc.to_status IS NOT NULL
),
status_intervals AS (
	SELECT issue_id, status, at AS valid_from,
	       lead(at) OVER (PARTITION BY issue_id ORDER BY at, seq, entry_id) AS valid_to
	FROM status_points
)`

// insightPeriodsCTE exposes periods passed as $2 (starts) and $3 (ends).
const insightPeriodsCTE = `
periods AS (
	SELECT p.idx, p.period_start, p.period_end
	FROM unnest($2::timestamptz[], $3::timestamptz[]) WITH ORDINALITY AS p(period_start, period_end, idx)
)`

// insightStatusCountsSQL counts status intervals open at each period end: the
// state after every change made before the period ended.
const insightStatusCountsSQL = `
	count(si.issue_id) FILTER (WHERE si.status = 'todo')::INT,
	count(si.issue_id) FILTER (WHERE si.status = 'in_progress')::INT,
	count(si.issue_id) FILTER (WHERE si.status = 'done')::INT,
	count(si.issue_id) FILTER (WHERE si.status = 'closed')::INT`

func insightPeriodArgs(periods []insightPeriod) ([]time.Time, []time.Time) {
	starts := make([]time.Time, len(periods))
	ends := make([]time.Time, len(periods))
	for i, period := range periods {
		starts[i] = period.Start
		ends[i] = period.End
	}
	return starts, ends
}

func insightFlowPoint(period insightPeriod, todo, inProgress, done, cancelled int) model.ProjectInsightFlowPoint {
	return model.ProjectInsightFlowPoint{
		PeriodStart: period.Start,
		PeriodEnd:   period.End,
		Todo:        todo,
		InProgress:  inProgress,
		Done:        done,
		Cancelled:   cancelled,
		Scope:       todo + inProgress + done,
		Started:     inProgress + done,
		Completed:   done,
	}
}

func insightFlowAndThroughput(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, periods []insightPeriod) ([]model.ProjectInsightFlowPoint, []model.ProjectInsightThroughputPoint, error) {
	starts, ends := insightPeriodArgs(periods)
	rows, err := tx.Query(ctx, `
		WITH `+insightPeriodsCTE+`, `+insightIssueHistoryCTE+`
		SELECT p.idx,
		       `+insightStatusCountsSQL+`,
		       (SELECT count(*) FROM live_issues li
		        WHERE li.created_at >= p.period_start AND li.created_at < p.period_end)::INT,
		       (SELECT count(*) FROM status_changes sc
		        WHERE sc.at >= p.period_start AND sc.at < p.period_end
		          AND sc.to_status IN ('done', 'closed') AND sc.from_status IN ('todo', 'in_progress'))::INT,
		       (SELECT count(*) FROM status_changes sc
		        WHERE sc.at >= p.period_start AND sc.at < p.period_end
		          AND sc.from_status IN ('done', 'closed') AND sc.to_status IN ('todo', 'in_progress'))::INT
		FROM periods p
		LEFT JOIN status_intervals si
		       ON si.valid_from < p.period_end
		      AND (si.valid_to IS NULL OR si.valid_to >= p.period_end)
		GROUP BY p.idx, p.period_start, p.period_end
		ORDER BY p.idx
	`, projectID, starts, ends)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	flow := make([]model.ProjectInsightFlowPoint, 0, len(periods))
	throughput := make([]model.ProjectInsightThroughputPoint, 0, len(periods))
	for rows.Next() {
		var idx int64
		var todo, inProgress, done, cancelled, created, resolved, reopened int
		if err := rows.Scan(&idx, &todo, &inProgress, &done, &cancelled, &created, &resolved, &reopened); err != nil {
			return nil, nil, err
		}
		period := periods[idx-1]
		flow = append(flow, insightFlowPoint(period, todo, inProgress, done, cancelled))
		throughput = append(throughput, model.ProjectInsightThroughputPoint{
			PeriodStart: period.Start,
			PeriodEnd:   period.End,
			Created:     created,
			Resolved:    resolved,
			Reopened:    reopened,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return flow, throughput, nil
}

// insightCycleTimeCTE lists live Done issues whose last move to Done falls in
// [$2, $3), with the first move to In progress before it. Issues reopened and
// finished again are measured to their final completion.
var insightCycleTimeCTE = insightIssueHistoryCTE + `,
completions AS (
	SELECT sc.issue_id, max(sc.at) AS completed_at
	FROM status_changes sc
	JOIN live_issues li ON li.id = sc.issue_id
	WHERE li.status = 'done' AND sc.to_status = 'done'
	GROUP BY sc.issue_id
),
cycle AS (
	SELECT c.issue_id, c.completed_at,
	       (SELECT min(sc.at) FROM status_changes sc
	        WHERE sc.issue_id = c.issue_id AND sc.to_status = 'in_progress' AND sc.at <= c.completed_at) AS started_at
	FROM completions c
	WHERE c.completed_at >= $2 AND c.completed_at < $3
)`

func insightCycleTime(ctx context.Context, tx pgx.Tx, project model.Project, start, now time.Time) (model.ProjectInsightCycleTime, error) {
	out := model.ProjectInsightCycleTime{Issues: []model.ProjectInsightCycleTimeIssue{}}
	var started int
	if err := tx.QueryRow(ctx, `
		WITH `+insightCycleTimeCTE+`
		SELECT count(*) FILTER (WHERE started_at IS NULL)::INT,
		       count(*) FILTER (WHERE started_at IS NOT NULL)::INT,
		       COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY hours), 0)::FLOAT8,
		       COALESCE(percentile_cont(0.85) WITHIN GROUP (ORDER BY hours), 0)::FLOAT8
		FROM (
			SELECT started_at, EXTRACT(EPOCH FROM (completed_at - started_at)) / 3600.0 AS hours
			FROM cycle
		) measured
	`, project.ID, start, now).Scan(&out.NotStarted, &started, &out.MedianHours, &out.P85Hours); err != nil {
		return model.ProjectInsightCycleTime{}, err
	}
	out.Truncated = started > insightCycleTimeLimit
	if started == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		WITH `+insightCycleTimeCTE+`
		SELECT i.id, i.number, i.title, cy.started_at, cy.completed_at
		FROM cycle cy
		JOIN issues i ON i.id = cy.issue_id
		WHERE cy.started_at IS NOT NULL
		ORDER BY cy.completed_at DESC, i.number DESC
		LIMIT $4
	`, project.ID, start, now, insightCycleTimeLimit)
	if err != nil {
		return model.ProjectInsightCycleTime{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var issue model.ProjectInsightCycleTimeIssue
		if err := rows.Scan(&issue.IssueID, &issue.Number, &issue.Title, &issue.StartedAt, &issue.CompletedAt); err != nil {
			return model.ProjectInsightCycleTime{}, err
		}
		issue.StartedAt = issue.StartedAt.UTC()
		issue.CompletedAt = issue.CompletedAt.UTC()
		issue.Identifier = fmt.Sprintf("%s-%d", project.Key, issue.Number)
		issue.DurationHours = issue.CompletedAt.Sub(issue.StartedAt).Hours()
		out.Issues = append(out.Issues, issue)
	}
	if err := rows.Err(); err != nil {
		return model.ProjectInsightCycleTime{}, err
	}
	// Oldest first reads left to right on the chart.
	for i, j := 0, len(out.Issues)-1; i < j; i, j = i+1, j-1 {
		out.Issues[i], out.Issues[j] = out.Issues[j], out.Issues[i]
	}
	return out, nil
}

// insightSprintActivationsCTE is when each sprint of $1 first became active,
// from the changelog row UpdateSprint writes for that transition.
const insightSprintActivationsCTE = `
activations AS (
	SELECT e.entity_id AS sprint_id, min(e.created_at) AS started_at
	FROM project_changelog_entries e
	WHERE e.project_id = $1
	  AND e.entity = 'sprint'
	  AND e.op = 'update'
	  AND e.details->'changes' @> '[{"field": "status", "to": "Active"}]'::jsonb
	GROUP BY e.entity_id
)`

func insightSprints(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, sprintID *uuid.UUID, start, now time.Time) (model.ProjectInsightSprints, error) {
	out := model.ProjectInsightSprints{
		Velocity: []model.ProjectInsightSprintVelocity{},
		Options:  []model.ProjectInsightSprintOption{},
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::INT FROM sprints WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID).Scan(&out.Count); err != nil {
		return model.ProjectInsightSprints{}, err
	}
	if out.Count == 0 {
		if sprintID != nil {
			return model.ProjectInsightSprints{}, ErrNotFound
		}
		return out, nil
	}
	velocity, err := insightVelocity(ctx, tx, projectID, start)
	if err != nil {
		return model.ProjectInsightSprints{}, err
	}
	out.Velocity = velocity

	var active *model.ProjectInsightSprintOption
	var option model.ProjectInsightSprintOption
	var number int
	err = tx.QueryRow(ctx, `
		SELECT id, number, name, status
		FROM sprints
		WHERE project_id = $1 AND status = 'active' AND deleted_at IS NULL
	`, projectID).Scan(&option.SprintID, &number, &option.Name, &option.Status)
	switch {
	case err == nil:
		option.Ref = model.SprintRef(number)
		active = &option
		out.Options = append(out.Options, option)
	case !isNoRows(err):
		return model.ProjectInsightSprints{}, err
	}
	for i := len(velocity) - 1; i >= 0; i-- {
		out.Options = append(out.Options, model.ProjectInsightSprintOption{
			SprintID: velocity[i].SprintID,
			Ref:      velocity[i].Ref,
			Name:     velocity[i].Name,
			Status:   model.SprintStatusCompleted,
		})
	}

	target := sprintID
	switch {
	case target != nil:
	case active != nil:
		target = &active.SprintID
	case len(velocity) > 0:
		target = &velocity[len(velocity)-1].SprintID
	default:
		return out, nil
	}
	out.Burnup, err = insightSprintBurnup(ctx, tx, projectID, *target, now)
	if err != nil {
		return model.ProjectInsightSprints{}, err
	}
	return out, nil
}

func insightVelocity(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, start time.Time) ([]model.ProjectInsightSprintVelocity, error) {
	rows, err := tx.Query(ctx, `
		WITH `+insightSprintActivationsCTE+`
		SELECT * FROM (
			SELECT s.id, s.number, s.name, s.completed_at, a.started_at,
			       count(i.id)::INT,
			       count(i.id) FILTER (WHERE sis.status = 'done')::INT,
			       count(i.id) FILTER (WHERE sis.status = 'closed')::INT,
			       CASE
			           WHEN a.started_at IS NULL
			             OR EXISTS (SELECT 1 FROM sprint_issue_memberships m WHERE m.sprint_id = s.id AND m.backfilled)
			           THEN NULL
			           ELSE (
			               SELECT count(*)::INT
			               FROM sprint_issue_memberships m
			               JOIN issues mi ON mi.id = m.issue_id AND mi.deleted_at IS NULL
			               WHERE m.sprint_id = s.id
			                 AND m.added_at <= a.started_at
			                 AND (m.removed_at IS NULL OR m.removed_at > a.started_at)
			           )
			       END
			FROM sprints s
			LEFT JOIN activations a ON a.sprint_id = s.id
			LEFT JOIN sprint_issue_snapshots sis ON sis.sprint_id = s.id
			LEFT JOIN issues i ON i.id = sis.issue_id AND i.deleted_at IS NULL
			WHERE s.project_id = $1
			  AND s.deleted_at IS NULL
			  AND s.status = 'completed'
			  AND s.completed_at >= $2
			GROUP BY s.id, s.number, s.name, s.completed_at, a.started_at
			ORDER BY s.completed_at DESC, s.number DESC
			LIMIT $3
		) recent
		ORDER BY completed_at ASC, number ASC
	`, projectID, start, insightVelocityLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ProjectInsightSprintVelocity{}
	for rows.Next() {
		var v model.ProjectInsightSprintVelocity
		if err := rows.Scan(&v.SprintID, &v.Number, &v.Name, &v.CompletedAt, &v.StartedAt, &v.Total, &v.Done, &v.Cancelled, &v.Committed); err != nil {
			return nil, err
		}
		v.Ref = model.SprintRef(v.Number)
		v.CompletedAt = v.CompletedAt.UTC()
		if v.StartedAt != nil {
			startedAt := v.StartedAt.UTC()
			v.StartedAt = &startedAt
		}
		v.CarriedOver = v.Total - v.Done - v.Cancelled
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func insightSprintBurnup(ctx context.Context, tx pgx.Tx, projectID, sprintID uuid.UUID, now time.Time) (*model.ProjectInsightSprintBurnup, error) {
	var sprint model.Sprint
	var startedAt *time.Time
	var estimated bool
	err := tx.QueryRow(ctx, `
		WITH `+insightSprintActivationsCTE+`
		SELECT s.id, s.number, s.name, s.status, s.start_date, s.end_date, s.completed_at, s.created_at,
		       a.started_at,
		       EXISTS (SELECT 1 FROM sprint_issue_memberships m WHERE m.sprint_id = s.id AND m.backfilled)
		FROM sprints s
		LEFT JOIN activations a ON a.sprint_id = s.id
		WHERE s.id = $2 AND s.project_id = $1 AND s.deleted_at IS NULL
		  AND s.status IN ('active', 'completed')
	`, projectID, sprintID).Scan(
		&sprint.ID, &sprint.Number, &sprint.Name, &sprint.Status, &sprint.StartDate, &sprint.EndDate,
		&sprint.CompletedAt, &sprint.CreatedAt, &startedAt, &estimated,
	)
	if err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	burnup := &model.ProjectInsightSprintBurnup{
		SprintID:  sprint.ID,
		Ref:       model.SprintRef(sprint.Number),
		Name:      sprint.Name,
		Status:    sprint.Status,
		Estimated: estimated,
		Points:    []model.ProjectInsightFlowPoint{},
	}
	periods, startDay, endDay := insightSprintPeriods(sprint, startedAt, now)
	burnup.Start = startDay
	burnup.End = endDay
	burnup.Days = int(endDay.Sub(startDay).Hours() / 24)
	if len(periods) == 0 {
		return burnup, nil
	}
	starts, ends := insightPeriodArgs(periods)
	rows, err := tx.Query(ctx, `
		WITH `+insightPeriodsCTE+`, `+insightIssueHistoryCTE+`,
		members AS (
			SELECT m.issue_id, m.added_at, m.removed_at
			FROM sprint_issue_memberships m
			JOIN live_issues li ON li.id = m.issue_id
			WHERE m.sprint_id = $4
		)
		SELECT p.idx, `+insightStatusCountsSQL+`
		FROM periods p
		LEFT JOIN members m
		       ON m.added_at < p.period_end
		      AND (m.removed_at IS NULL OR m.removed_at >= p.period_end)
		LEFT JOIN status_intervals si
		       ON si.issue_id = m.issue_id
		      AND si.valid_from < p.period_end
		      AND (si.valid_to IS NULL OR si.valid_to >= p.period_end)
		GROUP BY p.idx
		ORDER BY p.idx
	`, projectID, starts, ends, sprintID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var idx int64
		var todo, inProgress, done, cancelled int
		if err := rows.Scan(&idx, &todo, &inProgress, &done, &cancelled); err != nil {
			return nil, err
		}
		burnup.Points = append(burnup.Points, insightFlowPoint(periods[idx-1], todo, inProgress, done, cancelled))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return burnup, nil
}

// insightSprintPeriods lays a sprint out in whole UTC days, from the day it
// started to the day it completed or, while active, the later of today and its
// planned end. Only days that have begun get a period; each one is measured at
// the end of its day, cut off at completion or now.
func insightSprintPeriods(sprint model.Sprint, startedAt *time.Time, now time.Time) ([]insightPeriod, time.Time, time.Time) {
	var start time.Time
	switch {
	case startedAt != nil:
		start = startedAt.UTC()
	case sprint.StartDate != nil:
		start = sprint.StartDate.UTC()
	default:
		start = sprint.CreatedAt.UTC()
	}
	last := now
	if sprint.CompletedAt != nil {
		last = sprint.CompletedAt.UTC()
	}
	startDay := insightDayStart(start)
	// The day slot containing the last instant; an instant at midnight
	// closes the previous day rather than opening an empty one.
	endDay := insightDayStart(last.Add(-time.Nanosecond)).AddDate(0, 0, 1)
	if sprint.CompletedAt == nil && sprint.EndDate != nil {
		if planned := insightDayStart(*sprint.EndDate).AddDate(0, 0, 1); planned.After(endDay) {
			endDay = planned
		}
	}
	if endDay.Before(startDay.AddDate(0, 0, 1)) {
		endDay = startDay.AddDate(0, 0, 1)
	}
	if limit := endDay.AddDate(0, 0, -insightSprintBurnupMaxDays); startDay.Before(limit) {
		startDay = limit
	}
	var periods []insightPeriod
	for day := startDay; day.Before(endDay) && day.Before(last); day = day.AddDate(0, 0, 1) {
		end := day.AddDate(0, 0, 1)
		if end.After(last) {
			// Measured at the instant of completion, so issues carried over
			// by the completion itself still count toward final scope.
			end = last
		}
		periods = append(periods, insightPeriod{Start: day, End: end})
	}
	return periods, startDay, endDay
}
