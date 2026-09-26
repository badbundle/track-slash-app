package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

type ListRecentlyCompletedIssuesParams struct {
	ProjectID uuid.UUID
	// Since is the earliest completion time to include.
	Since time.Time
	Limit int
}

// ListRecentlyCompletedIssues returns a project's top-level Done issues whose
// last move to Done happened at or after Since, most recently completed first.
//
// Issues carry no completion column, so the time comes from the project
// changelog: the newest update that set the status to Done. Moving an issue to
// Done always bumps updated_at, so updated_at >= Since narrows the candidates
// before each one looks up its changelog through the issue index. An issue
// with no recorded move to Done (only possible for rows written outside the
// store) falls back to its creation time.
func (s *Store) ListRecentlyCompletedIssues(ctx context.Context, p ListRecentlyCompletedIssuesParams) ([]model.CompletedIssue, bool, error) {
	const q = `
		SELECT i.id, i.project_id, u.username, pr.key, i.number, i.title, i.description, i.status, i.close_reason, i.priority,
		       i.assignee_id, i.reporter_id, i.sprint_id, i.parent_issue_id, i.due_date, i.created_at, i.updated_at,
		       done.completed_at
		FROM issues i
		JOIN projects pr ON pr.id = i.project_id
		JOIN users u ON u.id = pr.owner_id
		CROSS JOIN LATERAL (
			SELECT COALESCE((
				SELECT e.created_at
				FROM project_changelog_entries e
				WHERE e.issue_id = i.id
				  AND e.entity = 'issue'
				  AND e.entity_id = i.id
				  AND e.op = 'update'
				  AND e.details->'changes' @> '[{"field": "status", "to": "Done"}]'::jsonb
				ORDER BY e.created_at DESC, e.id DESC
				LIMIT 1
			), i.created_at) AS completed_at
		) done
		WHERE i.project_id = $1 AND i.deleted_at IS NULL AND pr.deleted_at IS NULL AND u.deleted_at IS NULL
		  AND i.parent_issue_id IS NULL
		  AND i.status = 'done'
		  AND i.updated_at >= $2
		  AND done.completed_at >= $2
		ORDER BY done.completed_at DESC, i.number DESC
		LIMIT $3
	`
	rows, err := s.db.Query(ctx, q, p.ProjectID, p.Since, p.Limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	issues := make([]model.Issue, 0, p.Limit)
	completedAt := make([]time.Time, 0, p.Limit)
	for rows.Next() {
		var at time.Time
		iss, err := scanIssue(completedIssueScanner{row: rows, completedAt: &at})
		if err != nil {
			return nil, false, err
		}
		issues = append(issues, iss)
		completedAt = append(completedAt, at)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(issues) > p.Limit
	if hasMore {
		issues = issues[:p.Limit]
	}
	issues, err = s.hydrateIssueTags(ctx, issues)
	if err != nil {
		return nil, false, err
	}
	out := make([]model.CompletedIssue, 0, len(issues))
	for i, iss := range issues {
		out = append(out, model.CompletedIssue{Issue: iss, CompletedAt: completedAt[i]})
	}
	return out, hasMore, nil
}

// completedIssueScanner lets scanIssue read a row that carries one extra
// completed_at column after the issue columns.
type completedIssueScanner struct {
	row         issueScanner
	completedAt *time.Time
}

func (c completedIssueScanner) Scan(dest ...any) error {
	return c.row.Scan(append(dest, c.completedAt)...)
}
