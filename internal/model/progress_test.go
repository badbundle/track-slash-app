package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCompletionWindow(t *testing.T) {
	t.Parallel()
	want := map[CompletionWindow]struct {
		label    string
		duration time.Duration
	}{
		CompletionWindowDay:      {label: "24 hours", duration: 24 * time.Hour},
		CompletionWindowWeek:     {label: "7 days", duration: 7 * 24 * time.Hour},
		CompletionWindowTwoWeeks: {label: "14 days", duration: 14 * 24 * time.Hour},
		CompletionWindowMonth:    {label: "30 days", duration: 30 * 24 * time.Hour},
	}
	windows := CompletionWindows()
	if len(windows) != len(want) || windows[0] != CompletionWindowDay || windows[len(windows)-1] != CompletionWindowMonth {
		t.Fatalf("CompletionWindows() = %v", windows)
	}
	for _, w := range windows {
		if !w.Valid() || w.Label() != want[w].label || w.Duration() != want[w].duration {
			t.Fatalf("%q valid=%v label=%q duration=%s", w, w.Valid(), w.Label(), w.Duration())
		}
	}
	if !DefaultCompletionWindow.Valid() || DefaultCompletionWindow != CompletionWindowWeek {
		t.Fatalf("default completion window = %q", DefaultCompletionWindow)
	}
	if bogus := CompletionWindow("2w"); bogus.Valid() || bogus.Label() != "2w" || bogus.Duration() != 0 {
		t.Fatalf("unknown window valid=%v label=%q duration=%s", bogus.Valid(), bogus.Label(), bogus.Duration())
	}
}

// A completed issue serialises as the issue itself plus completed_at, so API
// clients read it with the same shape as any other issue.
func TestCompletedIssueJSONFlattensTheIssue(t *testing.T) {
	t.Parallel()
	completedAt := time.Date(2026, 9, 25, 10, 30, 0, 0, time.UTC)
	raw, err := json.Marshal(CompletedIssue{Issue: Issue{Identifier: "TRACK-7", Status: StatusDone}, CompletedAt: completedAt})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"identifier":"TRACK-7"`, `"status":"done"`, `"completed_at":"2026-09-25T10:30:00Z"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("completed issue JSON missing %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), `"Issue"`) {
		t.Fatalf("completed issue JSON nests the issue: %s", raw)
	}
}
