package server

import (
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
	"strconv"
	"strings"
	"testing"
	"time"
)

const uiCountBadgeClass = "inline-flex shrink-0 items-center rounded-md border border-slate-200 px-2 py-0.5 text-xs font-medium leading-4 text-slate-500 dark:border-slate-700 dark:text-slate-400"

func requireInlineCount(t *testing.T, body, heading string, count int) {
	t.Helper()
	requireInlineCountText(t, body, heading, strconv.Itoa(count))
}

func requireInlineCountText(t *testing.T, body, heading, count string) {
	t.Helper()
	headingIndex := strings.Index(body, ">"+heading+"</h")
	if headingIndex < 0 {
		t.Fatalf("missing heading %q: %s", heading, body)
	}
	segmentEnd := headingIndex + 700
	if segmentEnd > len(body) {
		segmentEnd = len(body)
	}
	segment := body[headingIndex:segmentEnd]
	want := `class="` + uiCountBadgeClass + `">` + count + `</span>`
	if !strings.Contains(segment, want) {
		t.Fatalf("heading %q missing inline count %q: %s", heading, count, body)
	}
}

func sectionClassForHeading(t *testing.T, body, heading string) string {
	t.Helper()
	headingIndex := strings.Index(body, ">"+heading+"</h")
	if headingIndex < 0 {
		t.Fatalf("missing heading %q: %s", heading, body)
	}
	sectionStart := strings.LastIndex(body[:headingIndex], "<section")
	if sectionStart < 0 {
		t.Fatalf("missing section before heading %q: %s", heading, body)
	}
	classStart := strings.Index(body[sectionStart:headingIndex], `class="`)
	if classStart < 0 {
		t.Fatalf("missing section class before heading %q: %s", heading, body)
	}
	classStart += sectionStart + len(`class="`)
	classEnd := strings.Index(body[classStart:headingIndex], `"`)
	if classEnd < 0 {
		t.Fatalf("unterminated section class before heading %q: %s", heading, body)
	}
	return body[classStart : classStart+classEnd]
}

func uiTestIssueTag(projectID uuid.UUID, number int, name string, color model.IssueTagColor) model.IssueTag {
	return model.IssueTag{
		ID:          uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)),
		ProjectID:   projectID,
		Number:      number,
		Ref:         model.IssueTagRef(number),
		Name:        name,
		DisplayName: model.IssueTagDisplayName(name),
		Color:       color,
	}
}

func sortTestIssue(title, projectKey string, number int, status model.Status, priority model.IssuePriority, createdAt, updatedAt time.Time) uiIssueItem {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(title))
	return uiIssueItem{
		Project: model.Project{OwnerUsername: "bradley", Key: projectKey},
		Issue: model.Issue{
			ID:            id,
			OwnerUsername: "bradley",
			ProjectKey:    projectKey,
			Number:        number,
			Identifier:    projectKey + "-" + strconv.Itoa(number),
			Title:         title,
			Status:        status,
			Priority:      priority,
			CreatedAt:     createdAt,
			UpdatedAt:     updatedAt,
		},
	}
}

func issueItemTitles(items []uiIssueItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Issue.Title)
	}
	return out
}

func requireMarkupOrder(t *testing.T, body, first, second string) {
	t.Helper()
	firstIndex := strings.Index(body, first)
	secondIndex := strings.Index(body, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex > secondIndex {
		t.Fatalf("%q should render before %q: %s", first, second, body)
	}
}

// markupFromForTest returns body from the first occurrence of start through the
// next occurrence of end.
func markupFromForTest(t *testing.T, body, start, end string) string {
	t.Helper()
	from := strings.Index(body, start)
	if from < 0 {
		t.Fatalf("missing %q: %s", start, body)
	}
	to := strings.Index(body[from:], end)
	if to < 0 {
		t.Fatalf("unterminated %q: %s", start, body)
	}
	return body[from : from+to]
}
