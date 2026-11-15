package server_test

import (
	"net/http"
	"reflect"
	"testing"
)

// inboxFixture has the admin's forms (one open, one submitted, one cancelled,
// one more open) and a member with one open form of their own.
func inboxFixture(t *testing.T, e *httpEnv) (memberToken string) {
	t.Helper()
	member, memberToken := e.mustProjectMemberToken(t, "inbox-rest-member")
	e.mustCreateForm(t, map[string]any{"title": "A"})
	e.mustSubmitForm(t, e.authToken, e.mustCreateForm(t, map[string]any{"title": "B"}).Ref)
	cancelled := e.mustCreateForm(t, map[string]any{"title": "C"})
	if code, body := e.do(t, http.MethodPost, e.formPath(cancelled.Ref)+"/cancel", nil); code != http.StatusOK {
		t.Fatalf("cancel code = %d body = %s", code, body)
	}
	e.mustCreateForm(t, map[string]any{"title": "D"})
	e.mustCreateForm(t, map[string]any{"title": "Theirs", "assignee_id": member.ID})
	return memberToken
}

func TestHTTPInbox(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	memberToken := inboxFixture(t, e)

	list := func(token, query string) pageDecoded[formJSON] {
		t.Helper()
		code, body := e.doWithToken(t, token, http.MethodGet, "/me/inbox"+query, nil)
		if code != http.StatusOK {
			t.Fatalf("inbox %q code = %d body = %s", query, code, body)
		}
		return decodePage[formJSON](t, body)
	}
	titles := func(page pageDecoded[formJSON]) []string {
		out := make([]string, len(page.Items))
		for i, item := range page.Items {
			out[i] = item.Title
		}
		return out
	}

	// The default is todo: open forms, longest-waiting first, with their links.
	todo := list(e.authToken, "")
	if got := titles(todo); !reflect.DeepEqual(got, []string{"A", "D"}) || todo.NextCursor != nil {
		t.Fatalf("todo = %v next = %v", got, todo.NextCursor)
	}
	if todo.Items[0].URL == "" || todo.Items[0].QuestionCount != 3 || todo.Items[0].Definition != nil {
		t.Fatalf("todo row = %+v, want a link and counts without questions", todo.Items[0])
	}
	if got := titles(list(e.authToken, "?state=todo")); !reflect.DeepEqual(got, []string{"A", "D"}) {
		t.Fatalf("explicit todo = %v", got)
	}
	// Done: the rest, most recently changed first.
	if got := titles(list(e.authToken, "?state=done")); !reflect.DeepEqual(got, []string{"C", "B"}) {
		t.Fatalf("done = %v", got)
	}

	// Paging.
	first := list(e.authToken, "?state=done&limit=1")
	if got := titles(first); !reflect.DeepEqual(got, []string{"C"}) || first.NextCursor == nil {
		t.Fatalf("page 1 = %v next = %v", got, first.NextCursor)
	}
	second := list(e.authToken, "?state=done&limit=1&cursor="+*first.NextCursor)
	if got := titles(second); !reflect.DeepEqual(got, []string{"B"}) || second.NextCursor != nil {
		t.Fatalf("page 2 = %v next = %v", got, second.NextCursor)
	}

	// Each person sees only the forms addressed to them.
	if got := titles(list(memberToken, "")); !reflect.DeepEqual(got, []string{"Theirs"}) {
		t.Fatalf("member todo = %v", got)
	}
	if got := titles(list(memberToken, "?state=done")); len(got) != 0 {
		t.Fatalf("member done = %v", got)
	}

	for _, query := range []string{"?state=all", "?state=Todo", "?limit=0", "?limit=abc", "?cursor=not-a-cursor!"} {
		if code, body := e.do(t, http.MethodGet, "/me/inbox"+query, nil); code != http.StatusBadRequest {
			t.Fatalf("inbox %q code = %d body = %s, want 400", query, code, body)
		}
	}
	if code, _ := e.doWithToken(t, "", http.MethodGet, "/me/inbox", nil); code != http.StatusUnauthorized {
		t.Fatalf("signed-out inbox code = %d, want 401", code)
	}
}

func TestMCPListInbox(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	memberToken := inboxFixture(t, e)
	session := mcpConnect(t, e, e.authToken)

	call := func(args map[string]any) ([]string, *string) {
		t.Helper()
		out := mcpCall(t, e, session, "track_list_inbox", args)
		items := decodeMCPField[[]formJSON](t, out, "items")
		got := make([]string, len(items))
		for i, item := range items {
			got[i] = item.Title
		}
		return got, decodeMCPField[*string](t, out, "next_cursor")
	}
	if got, next := call(nil); !reflect.DeepEqual(got, []string{"A", "D"}) || next != nil {
		t.Fatalf("default = %v next = %v", got, next)
	}
	if got, _ := call(map[string]any{"state": "done"}); !reflect.DeepEqual(got, []string{"C", "B"}) {
		t.Fatalf("done = %v", got)
	}
	got, next := call(map[string]any{"state": "done", "limit": 1})
	if !reflect.DeepEqual(got, []string{"C"}) || next == nil {
		t.Fatalf("page 1 = %v next = %v", got, next)
	}
	if got, next := call(map[string]any{"state": "done", "limit": 1, "cursor": *next}); !reflect.DeepEqual(got, []string{"B"}) || next != nil {
		t.Fatalf("page 2 = %v next = %v", got, next)
	}
	for name, args := range map[string]map[string]any{
		"bad state":  {"state": "all"},
		"bad limit":  {"limit": -1},
		"bad cursor": {"cursor": "not-a-cursor!"},
	} {
		t.Run(name, func(t *testing.T) {
			requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_list_inbox", args), "validation_error")
		})
	}

	// A second user sees only their own forms.
	other := mcpConnect(t, e, memberToken)
	out := mcpCall(t, e, other, "track_list_inbox", nil)
	items := decodeMCPField[[]formJSON](t, out, "items")
	if len(items) != 1 || items[0].Title != "Theirs" {
		t.Fatalf("member's inbox = %+v", items)
	}
}
