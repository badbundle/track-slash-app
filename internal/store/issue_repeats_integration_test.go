package store_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func mustRepeatRule(t *testing.T, raw string) model.RepeatRule {
	t.Helper()
	rule, err := model.ParseRepeatRule(raw)
	if err != nil {
		t.Fatalf("ParseRepeatRule(%q): %v", raw, err)
	}
	return rule
}

func repeatDate(t *testing.T, raw string) model.Date {
	t.Helper()
	d, err := model.ParseDate(raw)
	if err != nil {
		t.Fatalf("ParseDate(%q): %v", raw, err)
	}
	return d
}

func repeatDatePtr(t *testing.T, raw string) *model.Date {
	t.Helper()
	d := repeatDate(t, raw)
	return &d
}

func setRepeatClock(env *sprintsTestEnv, at string) {
	now, err := time.Parse(time.RFC3339, at)
	if err != nil {
		panic(err)
	}
	env.store.SetNow(func() time.Time { return now })
}

func completeIssue(t *testing.T, env *sprintsTestEnv, id uuid.UUID) model.Issue {
	t.Helper()
	done := model.StatusDone
	iss, err := env.store.UpdateIssue(env.ctx, id, store.UpdateIssueParams{Status: &done})
	if err != nil {
		t.Fatalf("complete issue: %v", err)
	}
	return iss
}

func mustGetIssue(t *testing.T, env *sprintsTestEnv, id uuid.UUID) model.Issue {
	t.Helper()
	iss, err := env.store.GetIssue(env.ctx, id)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return iss
}

func repetitionCount(t *testing.T, env *sprintsTestEnv, repeatID uuid.UUID) int {
	t.Helper()
	var n int
	if err := env.pool.QueryRow(env.ctx, `SELECT count(*) FROM issues WHERE repeat_id = $1`, repeatID).Scan(&n); err != nil {
		t.Fatalf("count repetitions: %v", err)
	}
	return n
}

func nextRepetition(t *testing.T, env *sprintsTestEnv, completed model.Issue) model.Issue {
	t.Helper()
	if completed.Repeat == nil || completed.Repeat.Next == nil {
		t.Fatalf("%s has no next repetition: %+v", completed.Identifier, completed.Repeat)
	}
	next, err := env.store.GetIssueByOwnerKeyNumber(env.ctx, completed.OwnerUsername, completed.ProjectKey, completed.Repeat.Next.Number)
	if err != nil {
		t.Fatalf("get next repetition: %v", err)
	}
	return next
}

func TestIssueRepeatLifecycle(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-28T12:00:00Z")

	tag, err := env.store.CreateIssueTag(env.ctx, store.CreateIssueTagParams{ProjectID: env.projectID, Name: "chores"})
	if err != nil {
		t.Fatalf("CreateIssueTag: %v", err)
	}
	sprint, err := env.store.CreateSprint(env.ctx, store.CreateSprintParams{ProjectID: env.projectID, Name: "October"})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	agent := model.WorkerAgent
	first, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{
		ProjectID:   env.projectID,
		Title:       "Send the invoice",
		Description: "Bill for the month.",
		Priority:    model.PriorityP1,
		Worker:      &agent,
		Repeat: &store.IssueRepeatSetting{
			Rule:     mustRepeatRule(t, "monthly"),
			TimeZone: "Europe/London",
			StartsOn: repeatDatePtr(t, "2026-11-01"),
		},
	})
	if err != nil {
		t.Fatalf("CreateIssue with repeat: %v", err)
	}
	r := first.Repeat
	if r == nil || !r.Current || r.Rule != "FREQ=MONTHLY;BYMONTHDAY=1" || r.Summary != "Every month on the 1st" ||
		r.TimeZone != "Europe/London" || r.Occurrence.String() != "2026-11-01" || r.NextOccurrence == nil || r.NextOccurrence.String() != "2026-12-01" {
		t.Fatalf("first repeat = %+v", r)
	}
	if first.DueDate == nil || first.DueDate.String() != "2026-11-01" {
		t.Fatalf("first due date = %v, want the occurrence", first.DueDate)
	}
	if _, err := env.store.CreateIssueTagLink(env.ctx, store.CreateIssueTagLinkParams{IssueID: first.ID, TagID: tag.ID}); err != nil {
		t.Fatalf("CreateIssueTagLink: %v", err)
	}
	if _, err := env.store.UpdateIssue(env.ctx, first.ID, store.UpdateIssueParams{SprintID: &sprint.ID}); err != nil {
		t.Fatalf("put first in sprint: %v", err)
	}

	// The issue list shows that it repeats.
	listed, _, err := env.store.ListIssues(env.ctx, store.ListIssuesParams{ProjectID: env.projectID, IncludePrivate: true, Limit: 50})
	if err != nil || len(listed) != 1 || listed[0].Repeat == nil || !listed[0].Repeat.Current {
		t.Fatalf("ListIssues repeat = %+v, %v", listed, err)
	}

	// Completed early: the very next date, copying the issue but not its
	// sprint, status or due date.
	completedFirst := completeIssue(t, env, first.ID)
	if completedFirst.Repeat == nil || completedFirst.Repeat.Current {
		t.Fatalf("completed repetition should stop being current: %+v", completedFirst.Repeat)
	}
	second := nextRepetition(t, env, completedFirst)
	if second.Number != first.Number+1 || second.Title != first.Title || second.Description != first.Description ||
		second.Priority != model.PriorityP1 || second.Worker == nil || *second.Worker != model.WorkerAgent ||
		second.Status != model.StatusTodo || second.SprintID != nil || second.DueDate == nil || second.DueDate.String() != "2026-12-01" {
		t.Fatalf("second repetition = %+v", second)
	}
	if len(second.Tags) != 1 || second.Tags[0].ID != tag.ID {
		t.Fatalf("second repetition tags = %+v", second.Tags)
	}
	if second.Repeat == nil || !second.Repeat.Current || second.Repeat.Occurrence.String() != "2026-12-01" || second.Repeat.SkippedCount != 0 ||
		second.Repeat.Previous == nil || second.Repeat.Previous.Number != first.Number || second.Repeat.ID != first.Repeat.ID {
		t.Fatalf("second repeat = %+v", second.Repeat)
	}

	// Closed late, with any close reason: 1 January has passed, so it's
	// skipped and recorded on the next repetition and the completed one.
	setRepeatClock(env, "2027-01-03T09:00:00Z")
	closed := model.StatusClosed
	wontDo := model.CloseReasonWontDo
	closedSecond, err := env.store.UpdateIssue(env.ctx, second.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &wontDo})
	if err != nil {
		t.Fatalf("close second: %v", err)
	}
	if n := closedSecond.Repeat.Next; n == nil || n.Occurrence.String() != "2027-02-01" || n.SkippedCount != 1 ||
		len(n.SkippedDates) != 1 || n.SkippedDates[0].String() != "2027-01-01" {
		t.Fatalf("closed second's next = %+v", closedSecond.Repeat.Next)
	}
	third := nextRepetition(t, env, closedSecond)
	if third.Repeat.SkippedCount != 1 || third.Repeat.SkippedDates[0].String() != "2027-01-01" || third.DueDate.String() != "2027-02-01" {
		t.Fatalf("third repeat = %+v due %v", third.Repeat, third.DueDate)
	}

	// Completed on the next date itself: it's created, due that day.
	setRepeatClock(env, "2027-03-01T08:00:00Z")
	fourth := nextRepetition(t, env, completeIssue(t, env, third.ID))
	if fourth.Repeat.Occurrence.String() != "2027-03-01" || fourth.Repeat.SkippedCount != 0 {
		t.Fatalf("fourth repeat = %+v", fourth.Repeat)
	}
	setRepeatClock(env, "2027-04-01T08:00:00Z")
	fifth := nextRepetition(t, env, completeIssue(t, env, fourth.ID))
	if fifth.Repeat.Occurrence.String() != "2027-04-01" || fifth.Repeat.SkippedCount != 0 {
		t.Fatalf("fifth repeat (completed on its date) = %+v", fifth.Repeat)
	}

	// Reopening and completing an earlier repetition, or moving it from done
	// to closed, doesn't create another.
	count := repetitionCount(t, env, first.Repeat.ID)
	todo := model.StatusTodo
	if _, err := env.store.UpdateIssue(env.ctx, fourth.ID, store.UpdateIssueParams{Status: &todo}); err != nil {
		t.Fatalf("reopen fourth: %v", err)
	}
	completeIssue(t, env, fourth.ID)
	if _, err := env.store.UpdateIssue(env.ctx, fourth.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &wontDo}); err != nil {
		t.Fatalf("done to closed: %v", err)
	}
	if got := repetitionCount(t, env, first.Repeat.ID); got != count {
		t.Fatalf("repetitions after reopen and recomplete = %d, want %d", got, count)
	}
	if mustGetIssue(t, env, fourth.ID).Repeat.Current {
		t.Fatal("a reopened earlier repetition must not become current again")
	}
	// Deleting a repetition hides the link to it rather than skipping to
	// the one after.
	if err := env.store.DeleteIssue(env.ctx, fourth.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if got := mustGetIssue(t, env, third.ID); got.Repeat.Next != nil {
		t.Fatalf("next past a deleted repetition = %+v", got.Repeat.Next)
	}
	if _, err := env.store.RestoreIssue(env.ctx, fourth.ID); err != nil {
		t.Fatalf("RestoreIssue: %v", err)
	}
	// An earlier repetition made to repeat starts a series of its own.
	if _, err := env.store.UpdateIssue(env.ctx, fourth.ID, store.UpdateIssueParams{Status: &todo}); err != nil {
		t.Fatalf("reopen fourth again: %v", err)
	}
	own, err := env.store.UpdateIssue(env.ctx, fourth.ID, store.UpdateIssueParams{Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil || own.Repeat.ID == first.Repeat.ID || !own.Repeat.Current {
		t.Fatalf("earlier repetition's own repeat = %+v, %v", own.Repeat, err)
	}

	// The completion date is taken in the repeat's time zone: 23:30 UTC on
	// 1 May is already 2 May in London, so 1 May has passed.
	setRepeatClock(env, "2027-05-01T23:30:00Z")
	sixth := nextRepetition(t, env, completeIssue(t, env, fifth.ID))
	if sixth.Repeat.Occurrence.String() != "2027-06-01" || sixth.Repeat.SkippedCount != 1 || sixth.Repeat.SkippedDates[0].String() != "2027-05-01" {
		t.Fatalf("sixth repeat = %+v", sixth.Repeat)
	}

	// The changelog records the new repetition and what was skipped.
	var summary string
	var skipped string
	if err := env.pool.QueryRow(env.ctx, `
		SELECT summary, coalesce(details->'changes'->1->>'to', '')
		FROM project_changelog_entries
		WHERE issue_id = $1 AND op = 'insert'
	`, sixth.ID).Scan(&summary, &skipped); err != nil {
		t.Fatalf("read changelog: %v", err)
	}
	if summary != "Created "+sixth.Identifier+" as the next repetition of "+fifth.Identifier || skipped != "2027-05-01" {
		t.Fatalf("changelog = %q, skipped %q", summary, skipped)
	}
}

func TestIssueRepeatSkipsManyDates(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-01-01T12:00:00Z")
	daily, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{
		ProjectID: env.projectID,
		Title:     "Water the plants",
		Repeat:    &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if daily.Repeat.TimeZone != "UTC" || daily.Repeat.Occurrence.String() != "2026-01-01" {
		t.Fatalf("default zone and start = %+v", daily.Repeat)
	}
	setRepeatClock(env, "2026-02-15T12:00:00Z")
	next := nextRepetition(t, env, completeIssue(t, env, daily.ID))
	// 2 January to 14 February have passed; only the first ten dates are kept.
	if next.Repeat.Occurrence.String() != "2026-02-15" || next.Repeat.SkippedCount != 44 ||
		len(next.Repeat.SkippedDates) != model.MaxRepeatSkippedDates || next.Repeat.SkippedDates[0].String() != "2026-01-02" {
		t.Fatalf("next repeat = %+v", next.Repeat)
	}
	var skipped string
	if err := env.pool.QueryRow(env.ctx, `
		SELECT details->'changes'->1->>'to' FROM project_changelog_entries WHERE issue_id = $1 AND op = 'insert'
	`, next.ID).Scan(&skipped); err != nil {
		t.Fatalf("read changelog: %v", err)
	}
	if want := "2026-01-02, 2026-01-03, 2026-01-04, 2026-01-05, 2026-01-06, 2026-01-07, 2026-01-08, 2026-01-09, 2026-01-10, 2026-01-11 and 34 more"; skipped != want {
		t.Fatalf("skipped label = %q, want %q", skipped, want)
	}
}

func TestIssueRepeatSetChangeAndStop(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	issue := mustCreateIssue(t, env, "Weekly review")
	if issue.Repeat != nil {
		t.Fatalf("new issue repeat = %+v, want nil", issue.Repeat)
	}

	// A shorthand takes its day from the start date, and the due date
	// follows the schedule.
	weekly, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Repeat: &store.IssueRepeatSetting{
		Rule:     mustRepeatRule(t, "weekly"),
		TimeZone: "America/New_York",
		StartsOn: repeatDatePtr(t, "2026-10-07"),
	}})
	if err != nil {
		t.Fatalf("set repeat: %v", err)
	}
	if weekly.Repeat == nil || weekly.Repeat.Rule != "FREQ=WEEKLY;BYDAY=WE" || weekly.DueDate.String() != "2026-10-07" {
		t.Fatalf("weekly = %+v due %v", weekly.Repeat, weekly.DueDate)
	}

	// Changing the rule keeps the series and starts from the current
	// repetition's date.
	friday, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Repeat: &store.IssueRepeatSetting{
		Rule: mustRepeatRule(t, "FREQ=WEEKLY;BYDAY=FR"),
	}})
	if err != nil {
		t.Fatalf("change repeat: %v", err)
	}
	if friday.Repeat.ID != weekly.Repeat.ID || friday.Repeat.TimeZone != "America/New_York" || friday.Repeat.Occurrence.String() != "2026-10-09" || friday.DueDate.String() != "2026-10-09" {
		t.Fatalf("changed = %+v due %v", friday.Repeat, friday.DueDate)
	}

	// A due date someone chose stays put when the rule changes, and a due
	// date in the same request wins over the schedule's.
	chosen := repeatDate(t, "2026-10-20")
	if _, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{DueDate: &chosen}); err != nil {
		t.Fatalf("choose due date: %v", err)
	}
	daily, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("change to daily: %v", err)
	}
	if daily.DueDate.String() != "2026-10-20" || daily.Repeat.Occurrence.String() != "2026-10-09" {
		t.Fatalf("daily = %+v due %v", daily.Repeat, daily.DueDate)
	}
	other := repeatDate(t, "2026-10-30")
	both, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{
		DueDate: &other,
		Repeat:  &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "yearly"), StartsOn: repeatDatePtr(t, "2026-12-25")},
	})
	if err != nil || both.DueDate.String() != "2026-10-30" || both.Repeat.Rule != "FREQ=YEARLY;BYMONTH=12;BYMONTHDAY=25" {
		t.Fatalf("repeat with due date = %+v due %v, %v", both.Repeat, both.DueDate, err)
	}

	// The changelog records each change.
	var repeatChanges int
	if err := env.pool.QueryRow(env.ctx, `
		SELECT count(*) FROM project_changelog_entries e, jsonb_array_elements(e.details->'changes') c
		WHERE e.issue_id = $1 AND c->>'field' = 'repeat'
	`, issue.ID).Scan(&repeatChanges); err != nil {
		t.Fatalf("count repeat changes: %v", err)
	}
	if repeatChanges != 4 {
		t.Fatalf("repeat changelog changes = %d, want 4", repeatChanges)
	}

	// Stopping leaves the issue alone and creates nothing more; stopping
	// again changes nothing.
	stopped, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{ClearRepeat: true})
	if err != nil || stopped.Repeat == nil || stopped.Repeat.Current || stopped.DueDate.String() != "2026-10-30" {
		t.Fatalf("stopped = %+v, %v", stopped.Repeat, err)
	}
	if _, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{ClearRepeat: true}); err != nil {
		t.Fatalf("stop again: %v", err)
	}
	completed := completeIssue(t, env, issue.ID)
	if completed.Repeat.Next != nil || repetitionCount(t, env, weekly.Repeat.ID) != 1 {
		t.Fatalf("a stopped repeat created a repetition: %+v", completed.Repeat)
	}

	// A completed issue must be reopened first, unless the same request
	// reopens it.
	_, err = env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if !errors.Is(err, store.ErrRepeatOnCompletedIssue) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("repeat on a completed issue err = %v", err)
	}
	todo := model.StatusTodo
	reopened, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Status: &todo, Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil || !reopened.Repeat.Current || reopened.Repeat.ID != weekly.Repeat.ID {
		t.Fatalf("a repeat restarted on its newest repetition keeps its series: %+v, %v", reopened.Repeat, err)
	}

	// Making an issue repeat and completing it in one request creates the
	// next repetition straight away.
	fresh := mustCreateIssue(t, env, "One go")
	done := model.StatusDone
	oneGo, err := env.store.UpdateIssue(env.ctx, fresh.ID, store.UpdateIssueParams{Status: &done, Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil || oneGo.Repeat == nil || oneGo.Repeat.Next == nil || oneGo.Repeat.Next.Occurrence.String() != "2026-10-06" {
		t.Fatalf("repeat and complete = %+v, %v", oneGo.Repeat, err)
	}

	// Bad settings are conflicts.
	if _, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily"), TimeZone: "Mars/Olympus"}}); !errors.Is(err, store.ErrConflict) || !errors.Is(err, model.ErrInvalidRepeatRule) {
		t.Fatalf("unknown zone err = %v", err)
	}
	if _, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "bad", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily"), TimeZone: "Nowhere"}}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("create with unknown zone err = %v", err)
	}
}

func TestIssueRepeatScheduleRunsOut(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2096-02-01T12:00:00Z")
	// The cron library looks five years ahead, so 29 February 2096 has no
	// date after it: 2100 isn't a leap year and 2104 is too far.
	leap := mustRepeatRule(t, "CRON:0 0 29 2 *")
	_, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "never", Repeat: &store.IssueRepeatSetting{Rule: leap, StartsOn: repeatDatePtr(t, "2097-03-01")}})
	if !errors.Is(err, model.ErrInvalidRepeatRule) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a schedule with no dates err = %v", err)
	}
	last, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "leap day", Repeat: &store.IssueRepeatSetting{Rule: leap}})
	if err != nil || last.Repeat.Occurrence.String() != "2096-02-29" {
		t.Fatalf("leap day = %+v, %v", last.Repeat, err)
	}
	completed := completeIssue(t, env, last.ID)
	if completed.Repeat.Next != nil || completed.Repeat.Current || repetitionCount(t, env, last.Repeat.ID) != 1 {
		t.Fatalf("a schedule that ran out created %+v", completed.Repeat)
	}
}

func TestIssueRepeatSurvivesBadStoredSeries(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Fragile", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily"), TimeZone: "Asia/Tokyo"}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// A zone that stops loading falls back to UTC.
	if _, err := env.pool.Exec(env.ctx, `UPDATE issue_repeats SET time_zone = 'Gone/Away' WHERE id = $1`, issue.Repeat.ID); err != nil {
		t.Fatalf("break zone: %v", err)
	}
	if got := mustGetIssue(t, env, issue.ID); got.Repeat.NextOccurrence != nil {
		t.Fatalf("an unloadable zone still previews %v", got.Repeat.NextOccurrence)
	}
	next := nextRepetition(t, env, completeIssue(t, env, issue.ID))
	// A rule that stops parsing ends the series instead of failing.
	if _, err := env.pool.Exec(env.ctx, `UPDATE issue_repeats SET rule = 'FREQ=FORTNIGHTLY' WHERE id = $1`, issue.Repeat.ID); err != nil {
		t.Fatalf("break rule: %v", err)
	}
	if got := mustGetIssue(t, env, next.ID); got.Repeat.Summary != "FREQ=FORTNIGHTLY" {
		t.Fatalf("unparsable rule summary = %q", got.Repeat.Summary)
	}
	if got := completeIssue(t, env, next.ID); got.Repeat.Next != nil || got.Repeat.Current {
		t.Fatalf("a broken rule created %+v", got.Repeat)
	}
	if n := repetitionCount(t, env, issue.Repeat.ID); n != 2 {
		t.Fatalf("repetitions = %d, want 2", n)
	}
	var summary string
	if err := env.pool.QueryRow(env.ctx, `SELECT summary FROM project_changelog_entries WHERE issue_id = $1 AND op = 'update' ORDER BY created_at DESC LIMIT 1`, next.ID).Scan(&summary); err != nil || !strings.Contains(summary, next.Identifier) {
		t.Fatalf("completion changelog = %q, %v", summary, err)
	}
}

func TestIssueRepeatClosedAsDuplicate(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	target := mustCreateIssue(t, env, "Original")
	for _, viaUpdate := range []bool{false, true} {
		issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Repeating duplicate", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if viaUpdate {
			link, err := env.store.CreateIssueLink(env.ctx, store.CreateIssueLinkParams{SourceID: issue.ID, TargetID: target.ID, LinkType: model.LinkTypeRelatesTo})
			if err != nil {
				t.Fatalf("CreateIssueLink: %v", err)
			}
			if _, err := env.store.UpdateIssueLink(env.ctx, link.ID, store.UpdateIssueLinkParams{SourceID: issue.ID, TargetID: target.ID, LinkType: model.LinkTypeDuplicates}); err != nil {
				t.Fatalf("UpdateIssueLink: %v", err)
			}
		} else if _, err := env.store.CreateIssueLink(env.ctx, store.CreateIssueLinkParams{SourceID: issue.ID, TargetID: target.ID, LinkType: model.LinkTypeDuplicates}); err != nil {
			t.Fatalf("CreateIssueLink: %v", err)
		}
		got := mustGetIssue(t, env, issue.ID)
		if got.Status != model.StatusClosed || got.Repeat.Current || got.Repeat.Next == nil || got.Repeat.Next.Occurrence.String() != "2026-10-06" {
			t.Fatalf("closed as duplicate (update %v) = %s %+v", viaUpdate, got.Status, got.Repeat)
		}
	}
}

func TestIssueRepeatConcurrentCompletion(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Race", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := model.StatusDone
			_, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Status: &done})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent complete: %v", err)
		}
	}
	if got := repetitionCount(t, env, issue.Repeat.ID); got != 2 {
		t.Fatalf("repetitions after concurrent completes = %d, want 2", got)
	}
}

// raceCompletion completes a repeating issue while other runs. A test
// transaction holds the issue's series, so the completion stops there with
// the locks it took before; other starts once the completion waits and runs
// until it waits too or finishes. Then the series is let go.
func raceCompletion(t *testing.T, env *sprintsTestEnv, issue model.Issue, other func() error) (completeErr, otherErr error) {
	t.Helper()
	blocker, err := env.pool.Begin(env.ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(env.ctx) }()
	if _, err := blocker.Exec(env.ctx, `SELECT 1 FROM issue_repeats WHERE id = $1 FOR UPDATE`, issue.Repeat.ID); err != nil {
		t.Fatalf("lock series: %v", err)
	}
	completed := make(chan error, 1)
	go func() {
		done := model.StatusDone
		_, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Status: &done})
		completed <- err
	}()
	finished := make(chan error, 1)
	waitForLockWaits(t, env, 1, nil)
	go func() { finished <- other() }()
	waitForLockWaits(t, env, 2, finished)
	if err := blocker.Rollback(env.ctx); err != nil {
		t.Fatalf("release series: %v", err)
	}
	return <-completed, <-finished
}

// waitForLockWaits waits until n sessions in the test's database are waiting
// on a lock, or until done has a result, which it puts back.
func waitForLockWaits(t *testing.T, env *sprintsTestEnv, n int, done chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := env.pool.QueryRow(env.ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
		`).Scan(&waiting); err != nil {
			t.Fatalf("count lock waits: %v", err)
		}
		if waiting >= n {
			return
		}
		select {
		case err := <-done:
			done <- err
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d sessions wait on a lock, want %d", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TRACK-111: completing a repeating issue takes its project's lock before the
// issue's, the order linking it and adding context to it use, so none of
// them deadlock.
func TestIssueRepeatCompletionLockOrder(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	other := mustCreateIssue(t, env, "Neighbour")
	for name, run := range map[string]func(issue model.Issue) error{
		"link to it": func(issue model.Issue) error {
			_, err := env.store.CreateIssueLink(env.ctx, store.CreateIssueLinkParams{SourceID: other.ID, TargetID: issue.ID, LinkType: model.LinkTypeRelatesTo})
			return err
		},
		"link from it": func(issue model.Issue) error {
			_, err := env.store.CreateIssueLink(env.ctx, store.CreateIssueLinkParams{SourceID: issue.ID, TargetID: other.ID, LinkType: model.LinkTypeBlocks})
			return err
		},
		"add context to it": func(issue model.Issue) error {
			_, err := env.store.CreateIssueContext(env.ctx, store.CreateIssueContextParams{IssueID: issue.ID, Title: "Notes", Body: "Context", CreatedByID: project.OwnerID})
			return err
		},
		"add a sub-issue to it": func(issue model.Issue) error {
			_, err := env.store.CreateSubIssue(env.ctx, store.CreateSubIssueParams{ParentIssueID: issue.ID, Title: "Step"})
			return err
		},
	} {
		issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Repeat while " + name, Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		completeErr, otherErr := raceCompletion(t, env, issue, func() error { return run(issue) })
		if completeErr != nil || otherErr != nil {
			t.Fatalf("complete while %s: complete err = %v, other err = %v", name, completeErr, otherErr)
		}
		if got := repetitionCount(t, env, issue.Repeat.ID); got != 2 {
			t.Fatalf("repetitions after completing while %s = %d, want 2", name, got)
		}
	}
}

// An edit that holds the issue when completion starts still logs its change:
// the project lock completion takes first doesn't hold up the changelog
// entry's check of its project.
func TestIssueRepeatCompletionWaitsForAnEdit(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Edited", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	edit, err := env.pool.Begin(env.ctx)
	if err != nil {
		t.Fatalf("begin edit: %v", err)
	}
	defer func() { _ = edit.Rollback(env.ctx) }()
	if _, err := edit.Exec(env.ctx, `SELECT 1 FROM issues WHERE id = $1 FOR UPDATE`, issue.ID); err != nil {
		t.Fatalf("lock issue: %v", err)
	}
	completed := make(chan error, 1)
	go func() {
		done := model.StatusDone
		_, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Status: &done})
		completed <- err
	}()
	waitForLockWaits(t, env, 1, completed)
	if _, err := edit.Exec(env.ctx, `
		INSERT INTO project_changelog_entries (project_id, entity, op, entity_id, issue_id, summary)
		VALUES ($1, 'issue', 'update', $2, $2, 'Edited')
	`, env.projectID, issue.ID); err != nil {
		t.Fatalf("log edit: %v", err)
	}
	if err := edit.Commit(env.ctx); err != nil {
		t.Fatalf("commit edit: %v", err)
	}
	if err := <-completed; err != nil {
		t.Fatalf("complete after edit: %v", err)
	}
	if got := repetitionCount(t, env, issue.Repeat.ID); got != 2 {
		t.Fatalf("repetitions = %d, want 2", got)
	}
}

func TestIssueRepeatDeleteRestoreSubIssueAndPeople(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")

	// Deleting the live repetition stops the series; restoring it resumes.
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Pause me", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := env.store.DeleteIssue(env.ctx, issue.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	restored, err := env.store.RestoreIssue(env.ctx, issue.ID)
	if err != nil || restored.Repeat == nil || !restored.Repeat.Current {
		t.Fatalf("restored = %+v, %v", restored.Repeat, err)
	}
	if next := nextRepetition(t, env, completeIssue(t, env, issue.ID)); next.Repeat.Occurrence.String() != "2026-10-06" {
		t.Fatalf("after restore next = %+v", next.Repeat)
	}

	// A repeating sub-issue's repetitions stay under its parent.
	parent := mustCreateIssue(t, env, "Parent")
	sub, err := env.store.CreateSubIssue(env.ctx, store.CreateSubIssueParams{
		ParentIssueID: parent.ID,
		Title:         "Repeating sub-issue",
		DueDate:       repeatDatePtr(t, "2026-10-31"),
		Repeat:        &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "FREQ=WEEKLY;BYDAY=MO"), StartsOn: repeatDatePtr(t, "2026-10-05")},
	})
	if err != nil {
		t.Fatalf("CreateSubIssue: %v", err)
	}
	if sub.DueDate.String() != "2026-10-31" || sub.Repeat.Occurrence.String() != "2026-10-05" {
		t.Fatalf("sub-issue keeps its chosen due date: due %v repeat %+v", sub.DueDate, sub.Repeat)
	}
	nextSub := nextRepetition(t, env, completeIssue(t, env, sub.ID))
	if nextSub.ParentIssueID == nil || *nextSub.ParentIssueID != parent.ID || nextSub.DueDate.String() != "2026-10-12" {
		t.Fatalf("next sub-issue = parent %v due %v", nextSub.ParentIssueID, nextSub.DueDate)
	}

	// The reporter is whoever set the repeat up. People who have left the
	// project aren't copied onto the next repetition.
	memberName := "member" + uniqueProjectKey(t)
	member, err := env.store.CreateUserProfile(env.ctx, memberName, memberName+"@example.com", "Member")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	setterName := "setter" + uniqueProjectKey(t)
	setter, err := env.store.CreateUserProfile(env.ctx, setterName, setterName+"@example.com", "Setter")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	for _, id := range []uuid.UUID{member.ID, setter.ID} {
		if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, id); err != nil {
			t.Fatalf("GrantProjectAccess: %v", err)
		}
	}
	assigned, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{
		ProjectID:  env.projectID,
		Title:      "Assigned chore",
		AssigneeID: &member.ID,
		ReporterID: &member.ID,
		Repeat:     &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily"), CreatedBy: &setter.ID},
	})
	if err != nil {
		t.Fatalf("CreateIssue assigned: %v", err)
	}
	stillHere := nextRepetition(t, env, completeIssue(t, env, assigned.ID))
	if stillHere.AssigneeID == nil || *stillHere.AssigneeID != member.ID || stillHere.ReporterID == nil || *stillHere.ReporterID != setter.ID {
		t.Fatalf("copied people = assignee %v reporter %v", stillHere.AssigneeID, stillHere.ReporterID)
	}
	// Once they've left, they don't report later repetitions, even where
	// anyone signed in may file issues.
	if _, err := env.store.UpdateProjectAccessMode(env.ctx, env.projectID, model.ProjectAccessPublicIssues); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, setter.ID); err != nil {
		t.Fatalf("RevokeProjectAccess setter: %v", err)
	}
	afterSetter := nextRepetition(t, env, completeIssue(t, env, stillHere.ID))
	if afterSetter.ReporterID != nil {
		t.Fatalf("a removed setter still reports: %v", afterSetter.ReporterID)
	}
	// Without them, the reporter is the last repetition's.
	if _, err := env.store.UpdateIssue(env.ctx, afterSetter.ID, store.UpdateIssueParams{ReporterID: &member.ID}); err != nil {
		t.Fatalf("change reporter: %v", err)
	}
	fallback := nextRepetition(t, env, completeIssue(t, env, afterSetter.ID))
	if fallback.ReporterID == nil || *fallback.ReporterID != member.ID || fallback.AssigneeID == nil {
		t.Fatalf("reporter falls back to the last repetition's: reporter %v assignee %v", fallback.ReporterID, fallback.AssigneeID)
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, member.ID); err != nil {
		t.Fatalf("RevokeProjectAccess member: %v", err)
	}
	// An assignee who left isn't copied; on a project anyone may file into,
	// the reporter still may report.
	gone := nextRepetition(t, env, completeIssue(t, env, fallback.ID))
	if gone.AssigneeID != nil || gone.ReporterID == nil || *gone.ReporterID != member.ID {
		t.Fatalf("after leaving: assignee %v reporter %v", gone.AssigneeID, gone.ReporterID)
	}
}

func TestIssueRepeatPrivateNeighbours(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	first, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Private chore", Private: true, Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	second := nextRepetition(t, env, completeIssue(t, env, first.ID))
	if !second.Private {
		t.Fatal("the next repetition keeps the private flag")
	}
	public := false
	if _, err := env.store.UpdateIssue(env.ctx, second.ID, store.UpdateIssueParams{Private: &public}); err != nil {
		t.Fatalf("make second public: %v", err)
	}
	// A public repetition doesn't name a private one; a private one names
	// its neighbours.
	if got := mustGetIssue(t, env, second.ID); got.Repeat.Previous != nil {
		t.Fatalf("public repetition names private previous %+v", got.Repeat.Previous)
	}
	if got := mustGetIssue(t, env, first.ID); got.Repeat.Next == nil || got.Repeat.Next.Number != second.Number {
		t.Fatalf("private repetition next = %+v", got.Repeat.Next)
	}
}

// A ref past the INT range can't name an object, so completing an issue
// whose description holds one still creates the next repetition, with the ref
// left as it was.
func TestIssueRepeatIgnoresOutOfRangeDescriptionRefs(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	description := "Huge ![h](object-3000000000) and [h]: object-99999999999"
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Daily check", Description: description, Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	second := nextRepetition(t, env, completeIssue(t, env, issue.ID))
	if second.Description != description {
		t.Fatalf("next description = %q, want %q", second.Description, description)
	}
}

func TestIssueRepeatCopiesDescriptionAttachments(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Monthly report", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	attach := func(issueID uuid.UUID, key string) model.StorageObject {
		t.Helper()
		object, err := env.store.CreateStorageObject(env.ctx, storageObjectParams(env.projectID, project.OwnerID, key))
		if err != nil {
			t.Fatalf("CreateStorageObject: %v", err)
		}
		if _, err := env.store.CreateIssueAttachment(env.ctx, store.CreateIssueAttachmentParams{IssueID: issueID, StorageObjectID: object.ID, CreatedByID: project.OwnerID}); err != nil {
			t.Fatalf("CreateIssueAttachment: %v", err)
		}
		return object
	}
	chart := attach(issue.ID, "projects/p/objects/chart")
	log := attach(issue.ID, "projects/p/objects/log")
	attach(issue.ID, "projects/p/objects/unmentioned")
	other := mustCreateIssue(t, env, "Somewhere else")
	elsewhere := attach(other.ID, "projects/p/objects/elsewhere")
	// Only refs Markdown resolves are copied and rewritten: whole link and
	// image destinations, with or without a title, and reference
	// definitions. Prose, URLs and zero-padded refs stay as they are.
	description := "![Chart](" + chart.Ref + ") and [log](" + log.Ref + ` "Log").` + "\n\n[again]: " + chart.Ref +
		"\n\nProse " + chart.Ref + ", a URL /x/attachments/" + chart.Ref + "/content, padded ![p](object-0" + strings.TrimPrefix(chart.Ref, "object-") +
		"), not mine ![e](" + elsewhere.Ref + "), missing ![m](object-999)."
	if _, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Description: &description}); err != nil {
		t.Fatalf("set description: %v", err)
	}

	second := nextRepetition(t, env, completeIssue(t, env, issue.ID))
	copies, _, err := env.store.ListIssueAttachments(env.ctx, store.ListIssueAttachmentsParams{IssueID: second.ID, Limit: 10})
	if err != nil || len(copies) != 2 {
		t.Fatalf("copied attachments = %+v, %v", copies, err)
	}
	chartCopy, logCopy := copies[0].Object, copies[1].Object
	if chartCopy.ObjectKey != chart.ObjectKey || logCopy.ObjectKey != log.ObjectKey || chartCopy.ID == chart.ID || chartCopy.Number == chart.Number ||
		chartCopy.Filename != chart.Filename || chartCopy.SHA256 != chart.SHA256 || copies[0].CreatedByID != project.OwnerID {
		t.Fatalf("copies = %+v, %+v", chartCopy, logCopy)
	}
	want := "![Chart](" + chartCopy.Ref + ") and [log](" + logCopy.Ref + ` "Log").` + "\n\n[again]: " + chartCopy.Ref +
		"\n\nProse " + chart.Ref + ", a URL /x/attachments/" + chart.Ref + "/content, padded ![p](object-0" + strings.TrimPrefix(chart.Ref, "object-") +
		"), not mine ![e](" + elsewhere.Ref + "), missing ![m](object-999)."
	if second.Description != want {
		t.Fatalf("copied description = %q, want %q", second.Description, want)
	}
	var copiedFrom uuid.UUID
	if err := env.pool.QueryRow(env.ctx, `SELECT copied_from_id FROM storage_objects WHERE id = $1`, chartCopy.ID).Scan(&copiedFrom); err != nil || copiedFrom != chart.ID {
		t.Fatalf("copied_from_id = %v, %v", copiedFrom, err)
	}

	// Each copy is its own object: deleting the original leaves the copy's
	// bytes in use, and the bytes are free once no live object has them.
	if _, err := env.store.DeleteStorageObject(env.ctx, chart.ID); err != nil {
		t.Fatalf("delete original: %v", err)
	}
	if inUse, err := env.store.StorageObjectBytesInUse(env.ctx, chart.Backend, chart.Bucket, chart.ObjectKey); err != nil || !inUse {
		t.Fatalf("bytes in use after deleting the original = %v, %v", inUse, err)
	}

	// The next repetition copies the copies, and a deleted attachment isn't
	// copied, leaving its ref as it was.
	if _, err := env.store.DeleteStorageObject(env.ctx, logCopy.ID); err != nil {
		t.Fatalf("delete log copy: %v", err)
	}
	third := nextRepetition(t, env, completeIssue(t, env, second.ID))
	thirdCopies, _, err := env.store.ListIssueAttachments(env.ctx, store.ListIssueAttachmentsParams{IssueID: third.ID, Limit: 10})
	if err != nil || len(thirdCopies) != 1 || thirdCopies[0].Object.ObjectKey != chart.ObjectKey {
		t.Fatalf("third copies = %+v, %v", thirdCopies, err)
	}
	if !strings.Contains(third.Description, "![Chart]("+thirdCopies[0].Object.Ref+")") || !strings.Contains(third.Description, "[log]("+logCopy.Ref+` "Log")`) {
		t.Fatalf("third description = %q", third.Description)
	}
	for _, id := range []uuid.UUID{chartCopy.ID, thirdCopies[0].Object.ID} {
		if _, err := env.store.DeleteStorageObject(env.ctx, id); err != nil {
			t.Fatalf("delete copy: %v", err)
		}
	}
	if inUse, err := env.store.StorageObjectBytesInUse(env.ctx, chart.Backend, chart.Bucket, chart.ObjectKey); err != nil || inUse {
		t.Fatalf("bytes in use after deleting every copy = %v, %v", inUse, err)
	}

	// A description without refs is left alone.
	plain := nextRepetition(t, env, completeIssue(t, env, third.ID))
	if plain.Description != third.Description {
		t.Fatalf("description changed without refs to copy: %q", plain.Description)
	}

	// An attachment being deleted right now is passed over, not waited on.
	busyIssue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Busy", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("CreateIssue busy: %v", err)
	}
	busy := attach(busyIssue.ID, "projects/p/objects/busy")
	free := attach(busyIssue.ID, "projects/p/objects/free")
	busyDescription := "![b](" + busy.Ref + ") ![f](" + free.Ref + ")"
	if _, err := env.store.UpdateIssue(env.ctx, busyIssue.ID, store.UpdateIssueParams{Description: &busyDescription}); err != nil {
		t.Fatalf("set busy description: %v", err)
	}
	lock, err := env.pool.Begin(env.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer lock.Rollback(env.ctx)
	if _, err := lock.Exec(env.ctx, `SELECT 1 FROM storage_objects WHERE id = $1 FOR UPDATE`, busy.ID); err != nil {
		t.Fatalf("lock busy object: %v", err)
	}
	afterBusy := nextRepetition(t, env, completeIssue(t, env, busyIssue.ID))
	busyCopies, _, err := env.store.ListIssueAttachments(env.ctx, store.ListIssueAttachmentsParams{IssueID: afterBusy.ID, Limit: 10})
	if err != nil || len(busyCopies) != 1 || busyCopies[0].Object.ObjectKey != free.ObjectKey || !strings.Contains(afterBusy.Description, "![b]("+busy.Ref+")") {
		t.Fatalf("copies while one is locked = %+v, %q, %v", busyCopies, afterBusy.Description, err)
	}
}

func TestIssueRepeatSkipsCopiesThatOverflowTheDescription(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	setRepeatClock(env, "2026-10-05T12:00:00Z")
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Full", Repeat: &store.IssueRepeatSetting{Rule: mustRepeatRule(t, "daily")}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	object, err := env.store.CreateStorageObject(env.ctx, storageObjectParams(env.projectID, project.OwnerID, "projects/p/objects/full"))
	if err != nil {
		t.Fatalf("CreateStorageObject: %v", err)
	}
	if _, err := env.store.CreateIssueAttachment(env.ctx, store.CreateIssueAttachmentParams{IssueID: issue.ID, StorageObjectID: object.ID, CreatedByID: project.OwnerID}); err != nil {
		t.Fatalf("CreateIssueAttachment: %v", err)
	}
	// Push the project's object numbers to two digits, so the copy's ref is
	// one character longer than object-1.
	for i := 0; i < 9; i++ {
		if _, err := env.store.CreateStorageObject(env.ctx, storageObjectParams(env.projectID, project.OwnerID, fmt.Sprintf("projects/p/objects/filler-%d", i))); err != nil {
			t.Fatalf("CreateStorageObject filler: %v", err)
		}
	}
	ref := "![a](" + object.Ref + ")"
	full := ref + strings.Repeat("x", store.MaxIssueDescriptionRunes-len(ref))
	if _, err := env.store.UpdateIssue(env.ctx, issue.ID, store.UpdateIssueParams{Description: &full}); err != nil {
		t.Fatalf("set full description: %v", err)
	}
	next := nextRepetition(t, env, completeIssue(t, env, issue.ID))
	copies, _, err := env.store.ListIssueAttachments(env.ctx, store.ListIssueAttachmentsParams{IssueID: next.ID, Limit: 10})
	if err != nil || len(copies) != 0 || next.Description != full {
		t.Fatalf("overflowing copy = %d attachments, description changed %v, %v", len(copies), next.Description != full, err)
	}
}
