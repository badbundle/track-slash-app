package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

// ListTimelineIssuesParams picks the issues a project timeline draws.
type ListTimelineIssuesParams struct {
	ProjectID uuid.UUID
	// SprintsEnabled groups issues by sprint: every top-level issue in one of
	// SprintIDs is listed under its sprint, whatever its state. Issues in no
	// sprint, or in a deleted one, form the other group; with sprints off
	// that group holds every issue.
	SprintsEnabled bool
	SprintIDs      []uuid.UUID
	// Since leaves out of the other group the issues completed before it;
	// nil keeps them all. That group never holds an open To do issue without
	// a due date, which has nothing to date it.
	Since *time.Time
	// Limit caps each group; a group with more sets its HasMore.
	Limit          int
	IncludePrivate bool
}

// TimelineIssue is an issue with the changelog times its bar is drawn from:
// its first move to In progress, and while it is completed, its last move to
// Done or Closed.
type TimelineIssue struct {
	Issue       model.Issue
	StartedAt   *time.Time
	CompletedAt *time.Time
}

// TimelineIssues are a timeline's issues by group, each group in the order its
// work began.
type TimelineIssues struct {
	BySprint       map[uuid.UUID][]TimelineIssue
	SprintHasMore  map[uuid.UUID]bool
	Outside        []TimelineIssue
	OutsideHasMore bool
	// Unscheduled counts the open To do issues the other group leaves out
	// for having no due date.
	Unscheduled int
}

// timelineOutsideSQL is true for an issue i that belongs to the timeline's
// other group: every issue while sprints are off ($2), and otherwise one in
// no live sprint.
const timelineOutsideSQL = `(NOT $2 OR NOT EXISTS (SELECT 1 FROM sprints s WHERE s.id = i.sprint_id AND s.deleted_at IS NULL))`

// ListTimelineIssues loads the top-level issues a project timeline draws, with
// when work on each began and ended. Both times come from the project
// changelog through its issue index, looked up only for the issues the cheap
// filters keep. A completed issue with no recorded completion (only possible
// for rows written outside the store) counts as completed when it was
// created, as in ListRecentlyCompletedIssues.
func (s *Store) ListTimelineIssues(ctx context.Context, p ListTimelineIssuesParams) (TimelineIssues, error) {
	sprintIDs := p.SprintIDs
	if sprintIDs == nil {
		sprintIDs = []uuid.UUID{}
	}
	const q = `
		WITH candidates AS (
			SELECT i.id, i.number, i.created_at, i.status,
			       CASE WHEN $2 AND i.sprint_id = ANY($3::uuid[]) THEN i.sprint_id END AS group_id
			FROM issues i
			WHERE i.project_id = $1 AND i.deleted_at IS NULL AND i.parent_issue_id IS NULL
			  AND ($6 OR NOT i.private)
			  AND (
			    ($2 AND i.sprint_id = ANY($3::uuid[]))
			    OR (` + timelineOutsideSQL + `
			        AND (i.status = 'in_progress'
			          OR (i.status = 'todo' AND i.due_date IS NOT NULL)
			          OR (i.status IN ('done', 'closed') AND ($4::timestamptz IS NULL OR i.updated_at >= $4))))
			  )
		),
		timed AS (
			SELECT c.*, started.at AS started_at, done.at AS completed_at
			FROM candidates c
			CROSS JOIN LATERAL (
				SELECT min(e.created_at) AS at
				FROM project_changelog_entries e
				WHERE e.issue_id = c.id AND e.entity = 'issue' AND e.entity_id = c.id AND e.op = 'update'
				  AND e.details->'changes' @> '[{"field": "status", "to": "In progress"}]'::jsonb
			) started
			CROSS JOIN LATERAL (
				SELECT CASE WHEN c.status IN ('done', 'closed') THEN COALESCE((
					SELECT e.created_at
					FROM project_changelog_entries e
					WHERE e.issue_id = c.id AND e.entity = 'issue' AND e.entity_id = c.id AND e.op = 'update'
					  AND (e.details->'changes' @> '[{"field": "status", "to": "Done"}]'::jsonb
					    OR e.details->'changes' @> '[{"field": "status", "to": "Closed"}]'::jsonb)
					ORDER BY e.created_at DESC, e.id DESC
					LIMIT 1
				), c.created_at) END AS at
			) done
		),
		ranked AS (
			SELECT t.*, row_number() OVER (
				PARTITION BY t.group_id
				ORDER BY COALESCE(t.started_at, t.created_at), t.number
			) AS rn
			FROM timed t
			WHERE t.group_id IS NOT NULL OR t.completed_at IS NULL OR $4::timestamptz IS NULL OR t.completed_at >= $4
		)
		SELECT i.id, i.project_id, u.username, pr.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority, i.worker, i.private,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at,
		       r.group_id, r.started_at, r.completed_at
		FROM ranked r
		JOIN issues i ON i.id = r.id
		JOIN projects pr ON pr.id = i.project_id
		JOIN users u ON u.id = pr.owner_id
		WHERE r.rn <= $5 + 1
		ORDER BY r.group_id NULLS LAST, r.rn
	`
	rows, err := s.db.Query(ctx, q, p.ProjectID, p.SprintsEnabled, sprintIDs, p.Since, p.Limit, p.IncludePrivate)
	if err != nil {
		// Defensive: a fixed query; only a DB outage reaches this.
		return TimelineIssues{}, err
	}
	defer rows.Close()

	type row struct {
		group   *uuid.UUID
		started *time.Time
		done    *time.Time
	}
	issues := []model.Issue{}
	extras := []row{}
	for rows.Next() {
		var extra row
		iss, err := scanIssue(timelineIssueScanner{row: rows, extra: []any{&extra.group, &extra.started, &extra.done}})
		if err != nil {
			// Defensive: the selected columns match scanIssue's fields.
			return TimelineIssues{}, err
		}
		issues = append(issues, iss)
		extras = append(extras, extra)
	}
	if err := rows.Err(); err != nil {
		// Defensive: post-scan failures require a database/runtime fault.
		return TimelineIssues{}, err
	}
	issues, err = s.hydrateIssues(ctx, issues)
	if err != nil {
		// Defensive: tag and repeat lookups by ID fail only on a DB outage.
		return TimelineIssues{}, err
	}

	out := TimelineIssues{BySprint: map[uuid.UUID][]TimelineIssue{}, SprintHasMore: map[uuid.UUID]bool{}, Outside: []TimelineIssue{}}
	for i, iss := range issues {
		item := TimelineIssue{Issue: iss, StartedAt: extras[i].started, CompletedAt: extras[i].done}
		if group := extras[i].group; group != nil {
			if len(out.BySprint[*group]) == p.Limit {
				out.SprintHasMore[*group] = true
				continue
			}
			out.BySprint[*group] = append(out.BySprint[*group], item)
			continue
		}
		if len(out.Outside) == p.Limit {
			out.OutsideHasMore = true
			continue
		}
		out.Outside = append(out.Outside, item)
	}

	err = s.db.QueryRow(ctx, `
		SELECT count(*)
		FROM issues i
		WHERE i.project_id = $1 AND i.deleted_at IS NULL AND i.parent_issue_id IS NULL
		  AND ($3 OR NOT i.private)
		  AND i.status = 'todo' AND i.due_date IS NULL
		  AND `+timelineOutsideSQL,
		p.ProjectID, p.SprintsEnabled, p.IncludePrivate).Scan(&out.Unscheduled)
	if err != nil {
		// Defensive: a fixed count over the same table fails only on a DB outage.
		return TimelineIssues{}, err
	}
	return out, nil
}

// timelineIssueScanner lets scanIssue read a row that carries extra columns
// after the issue columns.
type timelineIssueScanner struct {
	row   issueScanner
	extra []any
}

func (t timelineIssueScanner) Scan(dest ...any) error {
	return t.row.Scan(append(dest, t.extra...)...)
}
