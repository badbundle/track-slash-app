package store

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bradleymackey/track-slash/internal/model"
)

// IssueRepeatSetting makes an issue repeat, or changes how it repeats.
type IssueRepeatSetting struct {
	Rule model.RepeatRule
	// TimeZone is an IANA name. Empty keeps the zone when changing a repeat,
	// and means UTC for a new one.
	TimeZone string
	// StartsOn anchors the schedule, and the issue becomes the repetition for
	// the first schedule date on or after it. Nil means today in the time
	// zone for a new repeat, and the current repetition's date when changing
	// one.
	StartsOn *model.Date
	// CreatedBy reports the repetitions that follow.
	CreatedBy *uuid.UUID
}

// ErrRepeatOnCompletedIssue refuses to make a done or closed issue repeat:
// only completing the live repetition creates the next one.
var ErrRepeatOnCompletedIssue = fmt.Errorf("reopen the issue to make it repeat: %w", ErrConflict)

// clock is the time repeats are measured against; tests can replace it.
func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func repeatSettingError(err error) error {
	return fmt.Errorf("%w: %w", err, ErrConflict)
}

type issueRepeatState struct {
	RepeatID   *uuid.UUID
	Current    bool
	Occurrence *time.Time
	Rule       *string
	TimeZone   *string
}

func loadIssueRepeatState(ctx context.Context, tx pgx.Tx, issueID uuid.UUID) (issueRepeatState, error) {
	var state issueRepeatState
	err := tx.QueryRow(ctx, `
		SELECT i.repeat_id, i.repeat_current, i.repeat_occurrence, r.rule, r.time_zone
		FROM issues i
		LEFT JOIN issue_repeats r ON r.id = i.repeat_id
		WHERE i.id = $1
	`, issueID).Scan(&state.RepeatID, &state.Current, &state.Occurrence, &state.Rule, &state.TimeZone)
	return state, err
}

// summary is the changelog's label for the issue's repeat.
func (state issueRepeatState) summary() string {
	if !state.Current || state.Rule == nil {
		return "None"
	}
	return repeatRuleSummary(*state.Rule)
}

func repeatRuleSummary(rule string) string {
	parsed, err := model.ParseRepeatRule(rule)
	if err != nil {
		return rule // defensive: stored rules are canonical
	}
	return parsed.Summary()
}

type issueRepeatChange struct {
	From, To string
	DueDate  *model.Date
}

// The bare `return err`s in this file follow queries on rows the caller has
// already found and locked, so they only fail when the database does.

// applyIssueRepeat makes issue (locked, as it was before this change) the
// live repetition of a series following setting. Changing an existing repeat
// keeps the series and its history.
func applyIssueRepeat(ctx context.Context, tx pgx.Tx, issue model.Issue, setting IssueRepeatSetting, now time.Time) (issueRepeatChange, error) {
	state, err := loadIssueRepeatState(ctx, tx, issue.ID)
	if err != nil {
		return issueRepeatChange{}, err
	}
	timeZone := setting.TimeZone
	if timeZone == "" && state.Current && state.TimeZone != nil {
		timeZone = *state.TimeZone
	}
	loc, err := model.LoadRepeatLocation(timeZone)
	if err != nil {
		return issueRepeatChange{}, repeatSettingError(err)
	}
	var startsOn model.Date
	switch {
	case setting.StartsOn != nil:
		startsOn = *setting.StartsOn
	case state.Current && state.Occurrence != nil:
		startsOn = model.DateFromTime(*state.Occurrence)
	default:
		startsOn = model.TodayIn(loc, now)
	}
	schedule := model.NewRepeatSchedule(setting.Rule, startsOn, loc)
	occurrence, ok := schedule.FirstOnOrAfter(startsOn)
	if !ok {
		return issueRepeatChange{}, repeatSettingError(fmt.Errorf("%w: the schedule never falls on a date", model.ErrInvalidRepeatRule))
	}
	rule := schedule.Rule.String()

	// A repeat stopped on its newest repetition picks the same series up
	// again, keeping the links to earlier repetitions.
	reuse := state.Current
	if !reuse && state.RepeatID != nil {
		if err := tx.QueryRow(ctx, `
			SELECT NOT EXISTS (SELECT 1 FROM issues WHERE repeat_id = $1 AND number > $2)
		`, *state.RepeatID, issue.Number).Scan(&reuse); err != nil {
			return issueRepeatChange{}, err
		}
	}
	repeatID := uuid.Nil
	if reuse && state.RepeatID != nil {
		repeatID = *state.RepeatID
		if _, err := tx.Exec(ctx, `
			UPDATE issue_repeats
			SET rule = $2, time_zone = $3, starts_on = $4, updated_at = now()
			WHERE id = $1
		`, repeatID, rule, loc.String(), startsOn.Time()); err != nil {
			return issueRepeatChange{}, err
		}
	} else if err := tx.QueryRow(ctx, `
		INSERT INTO issue_repeats (project_id, rule, time_zone, starts_on, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, issue.ProjectID, rule, loc.String(), startsOn.Time(), setting.CreatedBy).Scan(&repeatID); err != nil {
		return issueRepeatChange{}, err
	}

	// The due date follows the schedule unless someone chose a different one.
	dueDate := issue.DueDate
	followsSchedule := dueDate == nil ||
		(state.Current && state.Occurrence != nil && dueDate.String() == model.DateFromTime(*state.Occurrence).String())
	if followsSchedule {
		dueDate = &occurrence
	}
	resetSkipped := !reuse
	if _, err := tx.Exec(ctx, `
		UPDATE issues
		SET repeat_id = $2,
		    repeat_current = true,
		    repeat_occurrence = $3,
		    due_date = $4,
		    repeat_skipped_count = CASE WHEN $5 THEN 0 ELSE repeat_skipped_count END,
		    repeat_skipped_dates = CASE WHEN $5 THEN '{}'::date[] ELSE repeat_skipped_dates END
		WHERE id = $1
	`, issue.ID, repeatID, occurrence.Time(), issueDueDateValue(dueDate), resetSkipped); err != nil {
		return issueRepeatChange{}, err
	}
	return issueRepeatChange{From: state.summary(), To: schedule.Rule.Summary(), DueDate: dueDate}, nil
}

// stopIssueRepeat stops the series the issue is the live repetition of. The
// issue stays as it is and nothing more is created.
func stopIssueRepeat(ctx context.Context, tx pgx.Tx, issueID uuid.UUID) (issueRepeatChange, error) {
	state, err := loadIssueRepeatState(ctx, tx, issueID)
	if err != nil {
		return issueRepeatChange{}, err
	}
	if !state.Current {
		return issueRepeatChange{From: "None", To: "None"}, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE issues SET repeat_current = false WHERE id = $1`, issueID); err != nil {
		return issueRepeatChange{}, err
	}
	return issueRepeatChange{From: state.summary(), To: "None"}, nil
}

// lockIssueProject locks the issue's project before the caller locks the
// issue. Whatever may complete an issue takes it first, since completing a
// repeating issue numbers its next repetition in the project, and linking to
// an issue or adding context to it locks the project and then the issue.
// FOR NO KEY UPDATE still serializes numbering, while other writes to the
// project's issues can check their changelog entries' project meanwhile.
// A missing issue locks nothing, and the caller's own lock reports it.
func lockIssueProject(ctx context.Context, tx pgx.Tx, issueID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		SELECT 1 FROM projects
		WHERE id = (SELECT project_id FROM issues WHERE id = $1)
		FOR NO KEY UPDATE
	`, issueID)
	return err
}

// advanceIssueRepeat runs when an issue becomes done or closed. If it was
// its series' live repetition, it creates the next repetition: the very next
// schedule date, or, when that date has already passed, the first one that
// hasn't, recording the dates skipped. It returns nil when the issue doesn't
// repeat, or the schedule has no more dates.
func advanceIssueRepeat(ctx context.Context, tx pgx.Tx, completed model.Issue, now time.Time) (*model.Issue, error) {
	var (
		repeatID             uuid.UUID
		occurrence, startsOn time.Time
		rule, timeZone       string
		createdBy            *uuid.UUID
	)
	// Locking the series makes concurrent completions create one repetition;
	// the caller has already locked the project and then the issue.
	err := tx.QueryRow(ctx, `
		SELECT r.id, i.repeat_occurrence, r.rule, r.time_zone, r.starts_on, r.created_by
		FROM issues i
		JOIN issue_repeats r ON r.id = i.repeat_id
		WHERE i.id = $1 AND i.repeat_current
		FOR UPDATE OF r
	`, completed.ID).Scan(&repeatID, &occurrence, &rule, &timeZone, &startsOn, &createdBy)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE issues SET repeat_current = false WHERE id = $1`, completed.ID); err != nil {
		return nil, err
	}
	// Completing an issue must never fail over its stored series. A rule
	// that no longer parses ends the series; a zone that no longer loads
	// (the host's zoneinfo changed) falls back to UTC.
	parsed, err := model.ParseRepeatRule(rule)
	if err != nil {
		return nil, nil
	}
	loc, err := model.LoadRepeatLocation(timeZone)
	if err != nil {
		loc = time.UTC
	}
	schedule := model.NewRepeatSchedule(parsed, model.DateFromTime(startsOn), loc)
	plan := schedule.PlanAfterCompletion(model.DateFromTime(occurrence), model.TodayIn(loc, now))
	if !plan.OK {
		return nil, nil
	}

	var number int
	if err := tx.QueryRow(ctx, `
		SELECT next_issue_number FROM projects WHERE id = $1 FOR NO KEY UPDATE
	`, completed.ProjectID).Scan(&number); err != nil {
		return nil, err
	}
	// People who have left the project don't come back with the next
	// repetition: completing an issue must never fail over who it copies.
	assigneeID, err := keepIssuePerson(ctx, tx, completed.ProjectID, completed.AssigneeID, issueProjectMemberExists)
	if err != nil {
		return nil, err
	}
	// Whoever set the repeat up reports it only while they're a member: a
	// reporter check would let someone removed from a public or help-desk
	// project keep following every repetition.
	reporterID, err := keepIssuePerson(ctx, tx, completed.ProjectID, createdBy, issueProjectMemberExists)
	if err != nil {
		return nil, err
	}
	// Otherwise the last repetition's reporter carries on, such as the person
	// a help-desk ticket is for, unless that's the person who has just been
	// turned down.
	if reporterID == nil && !sameUUIDPtr(completed.ReporterID, createdBy) {
		if reporterID, err = keepIssuePerson(ctx, tx, completed.ProjectID, completed.ReporterID, issueProjectReporterExists); err != nil {
			return nil, err
		}
	}
	skipped := make([]time.Time, 0, len(plan.Skipped))
	for _, d := range plan.Skipped {
		skipped = append(skipped, d.Time())
	}
	var nextID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO issues (
			project_id, number, title, description, priority, worker, private, assignee_id, reporter_id,
			parent_issue_id, due_date, repeat_id, repeat_current, repeat_occurrence, repeat_skipped_count, repeat_skipped_dates
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, true, $11, $13, $14)
		RETURNING id
	`, completed.ProjectID, number, completed.Title, completed.Description, string(completed.Priority),
		issueWorkerValue(completed.Worker), completed.Private, assigneeID, reporterID,
		completed.ParentIssueID, plan.Next.Time(), repeatID, plan.SkippedCount, skipped).Scan(&nextID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE projects SET next_issue_number = next_issue_number + 1, updated_at = now() WHERE id = $1
	`, completed.ProjectID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO issue_tag_links (project_id, issue_id, tag_id)
		SELECT project_id, $2, tag_id FROM issue_tag_links WHERE issue_id = $1
	`, completed.ID, nextID); err != nil {
		return nil, err
	}
	if err := copyRepeatAttachments(ctx, tx, completed, nextID); err != nil {
		return nil, err
	}
	next, err := getIssueForChangelog(ctx, tx, nextID, false)
	if err != nil {
		return nil, err
	}

	changes := changelogAppendChange(nil, "repeat", "Repeat", "", parsed.Summary())
	if plan.SkippedCount > 0 {
		changes = changelogAppendChange(changes, "repeat_skipped", "Skipped", "", repeatSkippedLabel(plan.Skipped, plan.SkippedCount))
	}
	details := model.ProjectChangelogDetails{Changes: changes}
	if preview := changelogPreview(next.Description); preview != "" {
		details.Preview = preview
	}
	details.PushNotification = pushNotificationChangelogData(next.AssigneeID, nil)
	if err := appendProjectChangelog(ctx, tx, appendProjectChangelogParams{
		ProjectID:       next.ProjectID,
		Entity:          "issue",
		Op:              "insert",
		EntityID:        next.ID,
		IssueID:         &next.ID,
		ParentIssueID:   next.ParentIssueID,
		RelatedIssueIDs: []uuid.UUID{completed.ID},
		TargetRef:       next.Identifier,
		TargetTitle:     next.Title,
		Summary:         fmt.Sprintf("Created %s as the next repetition of %s", next.Identifier, completed.Identifier),
		Details:         details,
	}); err != nil {
		return nil, err
	}
	return &next, nil
}

// repeatObjectDestinations finds attachment refs where Markdown rendering
// resolves them: a whole link or image destination, optionally with a title
// (`![Chart](object-3)`, `[log](object-4 "Log")`), or a reference definition
// (`[log]: object-4`). Only canonical refs (no leading zeros) resolve. A ref
// elsewhere, such as in prose or inside a pasted URL, is left alone.
var repeatObjectDestinations = []*regexp.Regexp{
	regexp.MustCompile(`(\]\(\s*)object-([1-9][0-9]*)(\s*(?:"[^"]*"|'[^']*'|\([^()]*\))?\s*\))`),
	regexp.MustCompile(`(?m)(^ {0,3}\[[^\]]+\]:[ \t]*)object-([1-9][0-9]*)([ \t]|$)`),
}

// repeatDescriptionRefs lists the object numbers the description's refs
// name. A number past the INT range names no object, so it is left out.
func repeatDescriptionRefs(description string) []int {
	numbers := []int{}
	for _, pattern := range repeatObjectDestinations {
		for _, match := range pattern.FindAllStringSubmatch(description, -1) {
			if n, err := strconv.ParseInt(match[2], 10, 32); err == nil && !slices.Contains(numbers, int(n)) {
				numbers = append(numbers, int(n))
			}
		}
	}
	return numbers
}

func rewriteRepeatDescriptionRefs(description string, renumbered map[int]int) string {
	for _, pattern := range repeatObjectDestinations {
		description = pattern.ReplaceAllStringFunc(description, func(match string) string {
			parts := pattern.FindStringSubmatch(match)
			n, _ := strconv.Atoi(parts[2])
			to, ok := renumbered[n]
			if !ok {
				return match
			}
			return parts[1] + model.StorageObjectRef(to) + parts[3]
		})
	}
	return description
}

// copyRepeatAttachments gives the next repetition its own copy of each
// attachment the completed repetition's description shows, and points the
// copied description at the copies, so its images and files keep working.
// A copy is its own storage object, with its own object-N and attachment
// link, sharing the original's backend bytes (see STORAGE.md), so this does no
// storage I/O. Attachments the description doesn't show aren't copied, and
// neither is one that's being deleted right now.
func copyRepeatAttachments(ctx context.Context, tx pgx.Tx, completed model.Issue, nextID uuid.UUID) error {
	numbers := repeatDescriptionRefs(completed.Description)
	if len(numbers) == 0 {
		return nil
	}
	type source struct {
		id, createdBy, linkedBy uuid.UUID
		number                  int
		backend, bucket, key    string
		filename, contentType   string
		byteSize                int64
		sha256                  string
	}
	// FOR SHARE holds the originals live until this commits, so their bytes
	// can't be deleted before the copies count as using them. SKIP LOCKED
	// passes over one being deleted rather than waiting: its deletion holds
	// it and then waits for this issue, which this transaction has locked.
	rows, err := tx.Query(ctx, `
		SELECT so.id, so.number, so.backend, so.bucket, so.object_key, so.filename, so.content_type,
		       so.byte_size, so.sha256, so.created_by_id, ia.created_by_id
		FROM issue_attachments ia
		JOIN storage_objects so ON so.id = ia.storage_object_id
		WHERE ia.issue_id = $1 AND so.number = ANY($2) AND so.deleted_at IS NULL
		ORDER BY so.number
		FOR SHARE OF so SKIP LOCKED
	`, completed.ID, numbers)
	if err != nil {
		return err
	}
	sources := []source{}
	for rows.Next() {
		var src source
		if err := rows.Scan(&src.id, &src.number, &src.backend, &src.bucket, &src.key, &src.filename, &src.contentType,
			&src.byteSize, &src.sha256, &src.createdBy, &src.linkedBy); err != nil {
			rows.Close()
			return err
		}
		sources = append(sources, src)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}
	// The project row is already locked by the caller, so the numbers can be
	// worked out first and the rewritten description checked before
	// anything is written.
	var firstNumber int
	if err := tx.QueryRow(ctx, `SELECT next_object_number FROM projects WHERE id = $1`, completed.ProjectID).Scan(&firstNumber); err != nil {
		return err
	}
	renumbered := make(map[int]int, len(sources))
	for i, src := range sources {
		renumbered[src.number] = firstNumber + i
	}
	description := rewriteRepeatDescriptionRefs(completed.Description, renumbered)
	if utf8.RuneCountInString(description) > MaxIssueDescriptionRunes {
		// Longer refs would take the description over its limit; leave the
		// copy as it was, without attachments, rather than fail.
		return nil
	}
	for _, src := range sources {
		var copyID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO storage_objects (
				project_id, number, backend, bucket, object_key, filename, content_type, byte_size, sha256,
				created_by_id, copied_from_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id
		`, completed.ProjectID, renumbered[src.number], src.backend, src.bucket, src.key, src.filename, src.contentType,
			src.byteSize, src.sha256, src.createdBy, src.id).Scan(&copyID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO issue_attachments (project_id, issue_id, storage_object_id, created_by_id)
			VALUES ($1, $2, $3, $4)
		`, completed.ProjectID, nextID, copyID, src.linkedBy); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE projects SET next_object_number = next_object_number + $2, updated_at = now() WHERE id = $1
	`, completed.ProjectID, len(sources)); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE issues SET description = $2 WHERE id = $1`, nextID, description)
	return err
}

func keepIssuePerson(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, userID *uuid.UUID, allowed func(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (bool, error)) (*uuid.UUID, error) {
	if userID == nil {
		return nil, nil
	}
	ok, err := allowed(ctx, tx, projectID, *userID)
	if err != nil || !ok {
		return nil, err
	}
	return userID, nil
}

// repeatSkippedLabel lists skipped dates, saying how many more there were
// past the ones kept.
func repeatSkippedLabel(dates []model.Date, count int) string {
	labels := make([]string, 0, len(dates))
	for _, d := range dates {
		labels = append(labels, d.String())
	}
	label := strings.Join(labels, ", ")
	if extra := count - len(dates); extra > 0 {
		label += fmt.Sprintf(" and %d more", extra)
	}
	return label
}

// hydrateIssueRepeats sets Repeat on issues that belong to a series.
func (s *Store) hydrateIssueRepeats(ctx context.Context, issues []model.Issue) error {
	ids := make([]uuid.UUID, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	rows, err := s.db.Query(ctx, `
		SELECT i.id, r.id, r.rule, r.time_zone, r.starts_on, i.repeat_current, i.repeat_occurrence,
		       i.repeat_skipped_count, i.repeat_skipped_dates
		FROM issues i
		JOIN issue_repeats r ON r.id = i.repeat_id
		WHERE i.id = ANY($1)
	`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	byIssue := map[uuid.UUID]*model.IssueRepeat{}
	for rows.Next() {
		var (
			issueID, repeatID    uuid.UUID
			rule, timeZone       string
			startsOn, occurrence time.Time
			current              bool
			skippedCount         int
			skipped              []time.Time
		)
		if err := rows.Scan(&issueID, &repeatID, &rule, &timeZone, &startsOn, &current, &occurrence, &skippedCount, &skipped); err != nil {
			return err
		}
		byIssue[issueID] = newIssueRepeat(repeatID, rule, timeZone, startsOn, current, occurrence, skippedCount, skipped)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range issues {
		issues[i].Repeat = byIssue[issues[i].ID]
	}
	return nil
}

func newIssueRepeat(id uuid.UUID, rule, timeZone string, startsOn time.Time, current bool, occurrence time.Time, skippedCount int, skipped []time.Time) *model.IssueRepeat {
	out := &model.IssueRepeat{
		ID:           id,
		Rule:         rule,
		Summary:      rule,
		TimeZone:     timeZone,
		StartsOn:     model.DateFromTime(startsOn),
		Current:      current,
		Occurrence:   model.DateFromTime(occurrence),
		SkippedDates: repeatDates(skipped),
		SkippedCount: skippedCount,
	}
	parsed, err := model.ParseRepeatRule(rule)
	if err != nil {
		return out // defensive: stored rules are canonical
	}
	out.Summary = parsed.Summary()
	if !current {
		return out
	}
	loc, err := model.LoadRepeatLocation(timeZone)
	if err != nil {
		return out // defensive: stored zones were loaded when saved
	}
	schedule := model.NewRepeatSchedule(parsed, out.StartsOn, loc)
	if next, ok := schedule.NextAfter(out.Occurrence); ok {
		out.NextOccurrence = &next
	}
	return out
}

func repeatDates(times []time.Time) []model.Date {
	out := make([]model.Date, 0, len(times))
	for _, t := range times {
		out = append(out, model.DateFromTime(t))
	}
	return out
}

// hydrateIssueRepetitions names the repetitions either side of issue in its
// series. A private repetition is named only on a private issue: a public
// issue is readable by people who may not see private ones, while only
// members read a private issue in full.
func (s *Store) hydrateIssueRepetitions(ctx context.Context, issue *model.Issue) error {
	if issue.Repeat == nil {
		return nil
	}
	neighbour := func(comparison, order string) (*model.IssueRepetition, error) {
		var (
			rep          model.IssueRepetition
			occurrence   time.Time
			skipped      []time.Time
			skippedCount int
			hidden       bool
		)
		// The immediate neighbour, even when deleted or private, so a
		// hidden one hides the link rather than letting a further one
		// stand in for it.
		err := s.db.QueryRow(ctx, fmt.Sprintf(`
			SELECT number, title, status, repeat_occurrence, repeat_skipped_count, repeat_skipped_dates, created_at,
			       deleted_at IS NOT NULL OR (private AND NOT $3)
			FROM issues
			WHERE repeat_id = $1 AND number %s $2
			ORDER BY number %s
			LIMIT 1
		`, comparison, order), issue.Repeat.ID, issue.Number, issue.Private).
			Scan(&rep.Number, &rep.Title, &rep.Status, &occurrence, &skippedCount, &skipped, &rep.CreatedAt, &hidden)
		if err != nil {
			if isNoRows(err) {
				return nil, nil
			}
			return nil, err
		}
		if hidden {
			return nil, nil
		}
		rep.Identifier = fmt.Sprintf("%s-%d", issue.ProjectKey, rep.Number)
		rep.Occurrence = model.DateFromTime(occurrence)
		rep.SkippedDates = repeatDates(skipped)
		rep.SkippedCount = skippedCount
		return &rep, nil
	}
	var err error
	if issue.Repeat.Previous, err = neighbour("<", "DESC"); err != nil {
		return err
	}
	issue.Repeat.Next, err = neighbour(">", "ASC")
	return err
}
