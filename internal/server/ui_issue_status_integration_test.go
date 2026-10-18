package server_test

import (
	"errors"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestUIEditStatusUpdatesIssuePanel(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-status")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "status target issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	edit := e.uiGet(t, e.issuePath(issue)+"/status/edit", token)
	for _, want := range []string{
		"status target issue",
		`aria-label="Change status: To do"`,
		`aria-expanded="true"`,
		`data-option-dropdown-toggle`,
		`data-option-dropdown-list`,
		`role="listbox" aria-label="Issue status"`,
		`method="post" action="` + e.issuePath(issue) + `/status"`,
		`hx-post="` + e.issuePath(issue) + `/status"`,
		`hx-push-url="false"`,
		`name="status" value="todo"`,
		`name="status" value="in_progress"`,
		`name="status" value="done"`,
		`name="status" value="closed"`,
		`hx-get="` + e.issuePath(issue) + `/panel"`,
		"To do",
		"In progress",
		"Done",
		"Closed",
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("status edit response missing %q: %s", want, edit)
		}
	}
	if strings.Contains(edit, `aria-label="Cancel status change"`) ||
		strings.Contains(edit, `disabled aria-label="Change status"`) ||
		strings.Contains(edit, `cursor-default`) ||
		strings.Contains(edit, `title="Change status"`) ||
		strings.Contains(edit, `title="Cancel status change"`) {
		t.Fatalf("status edit response has native tooltip state: %s", edit)
	}

	form := url.Values{"status": {string(model.StatusInProgress)}}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/status", token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update status code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "In progress") || strings.Contains(body, `role="option"`) {
		t.Fatalf("update status response did not return read mode with new status: %s", body)
	}
	updated, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after status update: %v", err)
	}
	if updated.Status != model.StatusInProgress {
		t.Fatalf("Status = %q, want %q", updated.Status, model.StatusInProgress)
	}

	badStatus := url.Values{"status": {"blocked"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/status", token, strings.NewReader(badStatus.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad status code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "invalid status") {
		t.Fatalf("bad status response missing validation error: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after bad status: %v", err)
	}
	if updated.Status != model.StatusInProgress {
		t.Fatalf("bad status changed Status = %q, want %q", updated.Status, model.StatusInProgress)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/status", token, strings.NewReader("%zz"))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad form status code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "unable to read form") {
		t.Fatalf("bad form status response missing parse error: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after bad form status: %v", err)
	}
	if updated.Status != model.StatusInProgress {
		t.Fatalf("bad form changed Status = %q, want %q", updated.Status, model.StatusInProgress)
	}

	closedStatus := url.Values{"status": {string(model.StatusClosed)}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/status", token, strings.NewReader(closedStatus.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("closed status code = %d body = %s", res.StatusCode, body)
	}
	for _, want := range []string{
		`role="dialog" aria-modal="true"`,
		"Close issue",
		"Closed",
		"Close reason",
		`data-option-dropdown-toggle`,
		`data-option-dropdown-list`,
		`method="post" action="` + e.issuePath(issue) + `/close-reason"`,
		`name="close_reason"`,
		"Duplicate",
		"Won&#39;t Do",
		"Invalid",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("closed status response missing %q: %s", want, body)
		}
	}
	modalEnd := strings.Index(body, `<section class="grid gap-6`)
	if modalEnd < 0 {
		t.Fatalf("closed status response missing issue detail section: %s", body)
	}
	if strings.Contains(body[:modalEnd], "Missing reason") {
		t.Fatalf("closed status response rendered modal missing reason indicator: %s", body[:modalEnd])
	}
	if !strings.Contains(body[modalEnd:], "Missing reason") {
		t.Fatalf("closed status response missing detail-panel missing reason indicator: %s", body[modalEnd:])
	}
	if strings.Contains(body, ">Reason</option>") || strings.Contains(body, `aria-expanded="false"`) && strings.Contains(body, "To do") {
		t.Fatalf("closed status response kept confusing pending close UI: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after closed status: %v", err)
	}
	if updated.Status != model.StatusInProgress || updated.CloseReason != nil {
		t.Fatalf("closed status picker changed issue = status %q reason %v, want in_progress/no reason", updated.Status, updated.CloseReason)
	}

	closeReason := url.Values{"close_reason": {string(model.CloseReasonInvalid)}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/close-reason", token, strings.NewReader(closeReason.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("close reason code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Closed") || !strings.Contains(body, "Invalid") || strings.Contains(body, `name="close_reason"`) {
		t.Fatalf("close reason response did not return read mode with reason: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after close reason: %v", err)
	}
	if updated.Status != model.StatusClosed || updated.CloseReason == nil || *updated.CloseReason != model.CloseReasonInvalid {
		t.Fatalf("closed issue = status %q reason %v, want closed/invalid", updated.Status, updated.CloseReason)
	}
}

func TestUIEditCloseReasonUpdatesAndReopenHidesReason(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-close-reason")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "close reason target",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	closed := model.StatusClosed
	initialReason := model.CloseReasonWontDo
	issue, err = e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{
		Status:      &closed,
		CloseReason: &initialReason,
	})
	if err != nil {
		t.Fatalf("close issue: %v", err)
	}

	body := e.uiGet(t, e.issuePath(issue), token)
	for _, want := range []string{
		"close reason target",
		"Close reason",
		"W",
		`hx-get="` + e.issuePath(issue) + `/close-reason/edit"`,
		`aria-label="Edit close reason"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("closed issue detail missing %q: %s", want, body)
		}
	}
	if !strings.Contains(body, "Won&#39;t Do") && !strings.Contains(body, "Won't Do") {
		t.Fatalf("closed issue detail missing Won't Do label: %s", body)
	}

	edit := e.uiGet(t, e.issuePath(issue)+"/close-reason/edit", token)
	for _, want := range []string{
		`method="post" action="` + e.issuePath(issue) + `/close-reason"`,
		`hx-post="` + e.issuePath(issue) + `/close-reason"`,
		`data-option-dropdown-toggle`,
		`data-option-dropdown-list`,
		`name="close_reason"`,
		`value="duplicate"`,
		`value="wont_do" role="option" aria-selected="true"`,
		`value="invalid"`,
		`aria-label="Choose close reason: Won&#39;t Do"`,
		`data-lucide="check"`,
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("close reason edit missing %q: %s", want, edit)
		}
	}

	res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/close-reason", token, strings.NewReader(url.Values{"close_reason": {"bogus"}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bad close reason code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Choose a close reason.") {
		t.Fatalf("bad close reason response missing validation error: %s", body)
	}
	updated, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after bad reason: %v", err)
	}
	if updated.CloseReason == nil || *updated.CloseReason != model.CloseReasonWontDo {
		t.Fatalf("bad reason changed close reason = %v, want wont_do", updated.CloseReason)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/close-reason", token, strings.NewReader(url.Values{"close_reason": {string(model.CloseReasonDuplicate)}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update close reason code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Duplicate") || strings.Contains(body, `name="close_reason"`) {
		t.Fatalf("close reason update response did not return read mode: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after reason update: %v", err)
	}
	if updated.CloseReason == nil || *updated.CloseReason != model.CloseReasonDuplicate {
		t.Fatalf("updated close reason = %v, want duplicate", updated.CloseReason)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/status", token, strings.NewReader(url.Values{"status": {string(model.StatusInProgress)}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reopen code = %d body = %s", res.StatusCode, body)
	}
	if strings.Contains(body, "Close reason") || strings.Contains(body, "Duplicate") {
		t.Fatalf("reopened response still rendered close reason: %s", body)
	}
	updated, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after reopen: %v", err)
	}
	if updated.Status != model.StatusInProgress || updated.CloseReason != nil {
		t.Fatalf("reopened issue = status %q reason %v, want in_progress/nil", updated.Status, updated.CloseReason)
	}
}

func TestUIDeleteIssueReturnsBackTarget(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-delete")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "delete target issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/delete", token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	allNotice := e.projectPath() + "/all?deleted_issue=" + url.QueryEscape(issue.Identifier)
	if loc := res.Header.Get("Location"); loc != allNotice {
		t.Fatalf("delete Location = %q", loc)
	}
	if _, err := e.store.GetIssue(e.ctx, issue.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetIssue deleted err = %v, want ErrNotFound", err)
	}
	body := e.uiGet(t, allNotice, token)
	for _, want := range []string{
		"Issue deleted",
		"delete target issue",
		"Undo delete",
		`hx-post="` + e.issuePath(issue) + `/restore"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("delete notice missing %q: %s", want, body)
		}
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/restore", token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("restore code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if loc := res.Header.Get("Location"); loc != e.issuePath(issue) {
		t.Fatalf("restore Location = %q", loc)
	}
	if _, err := e.store.GetIssue(e.ctx, issue.ID); err != nil {
		t.Fatalf("GetIssue restored: %v", err)
	}

	parent, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "delete child parent",
	})
	if err != nil {
		t.Fatalf("CreateIssue parent: %v", err)
	}
	child, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{
		ParentIssueID: parent.ID,
		Title:         "delete child target",
	})
	if err != nil {
		t.Fatalf("CreateSubIssue child: %v", err)
	}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, e.issuePath(child)+"/delete", token, nil, map[string]string{
		"HX-Request": "true",
	})
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("htmx delete code = %d body = %s", res.StatusCode, body)
	}
	parentNotice := e.issuePath(parent) + "?deleted_issue=" + url.QueryEscape(child.Identifier)
	if push := res.Header.Get("HX-Push-Url"); push != parentNotice {
		t.Fatalf("HX-Push-Url = %q", push)
	}
	for _, want := range []string{
		"delete child parent",
		"Issue deleted",
		"delete child target",
		"Undo delete",
		`hx-post="` + e.issuePath(child) + `/restore"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("htmx delete response missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "<!doctype html>") {
		t.Fatalf("htmx delete response should render parent issue panel: %s", body)
	}
	if _, err := e.store.GetIssue(e.ctx, child.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetIssue child err = %v, want ErrNotFound", err)
	}
	if _, err := e.store.GetIssue(e.ctx, parent.ID); err != nil {
		t.Fatalf("GetIssue parent after child delete: %v", err)
	}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, e.issuePath(child)+"/restore", token, nil, map[string]string{
		"HX-Request": "true",
	})
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("htmx restore code = %d body = %s", res.StatusCode, body)
	}
	if push := res.Header.Get("HX-Push-Url"); push != e.issuePath(child) {
		t.Fatalf("restore HX-Push-Url = %q", push)
	}
	if strings.Contains(body, "<!doctype html>") || strings.Contains(body, "Issue deleted") || !strings.Contains(body, "delete child target") {
		t.Fatalf("htmx restore response should render child issue panel: %s", body)
	}
	if _, err := e.store.GetIssue(e.ctx, child.ID); err != nil {
		t.Fatalf("GetIssue child restored: %v", err)
	}
}
