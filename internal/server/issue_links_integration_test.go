package server_test

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func (e *httpEnv) mustCreateIssue(t *testing.T, title string) model.Issue {
	t.Helper()
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     title,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return iss
}

func TestHTTPCreateIssueLinkHappy(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")

	code, body := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	if code != http.StatusCreated {
		t.Fatalf("code = %d body = %s", code, body)
	}
	link := decode[model.IssueLink](t, body)
	if link.SourceID != a.ID || link.TargetID != b.ID || link.LinkType != model.LinkTypeBlocks {
		t.Fatalf("link = %+v", link)
	}
}

func TestHTTPCreateIssueLinkDuplicatesClosesSource(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "dup")
	b := e.mustCreateIssue(t, "canon")

	code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "duplicates"})
	if code != http.StatusCreated {
		t.Fatalf("code = %d", code)
	}

	code, body := e.do(t, http.MethodGet, e.issuePath(a), nil)
	if code != http.StatusOK {
		t.Fatalf("get code = %d", code)
	}
	issue := decode[model.Issue](t, body)
	if issue.Status != model.StatusClosed {
		t.Fatalf("source not closed")
	}
	if issue.CloseReason == nil || *issue.CloseReason != model.CloseReasonDuplicate {
		t.Fatalf("source close reason = %v, want duplicate", issue.CloseReason)
	}
}

func TestHTTPCreateIssueLinkBadSourceID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPost, "/"+e.ownerUsername+"/issues/not-a-ref/links",
		map[string]any{"target_issue": e.projKey + "-999999", "link_type": "blocks"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateIssueLinkBadJSON(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	req, _ := http.NewRequestWithContext(e.ctx, http.MethodPost,
		e.ts.URL+apiPath(e.issueLinksPath(a)),
		bytes.NewReader([]byte("nope")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.authToken)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("code = %d", res.StatusCode)
	}
}

func TestHTTPCreateIssueLinkMissingTarget(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"link_type": "blocks"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateIssueLinkInvalidType(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")
	code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "explodes"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateIssueLinkSelfRejected(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": a.Identifier, "link_type": "blocks"})
	if code != http.StatusConflict {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateIssueLinkDuplicateRejected(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")
	body := map[string]any{"target_issue": b.Identifier, "link_type": "blocks"}
	if code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a), body); code != http.StatusCreated {
		t.Fatalf("first code = %d", code)
	}
	code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a), body)
	if code != http.StatusConflict {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateIssueLinkCrossProject(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	other, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "other", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a := e.mustCreateIssue(t, "A")
	b, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: other.ID, Title: "B"})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}

	code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	if code != http.StatusConflict {
		t.Fatalf("code = %d", code)
	}

	code, _ = e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": other.Key + "-999999", "link_type": "blocks"})
	if code != http.StatusConflict {
		t.Fatalf("missing cross-project target code = %d", code)
	}
}

func TestHTTPCreateIssueLinkSourceNotFound(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	b := e.mustCreateIssue(t, "B")
	code, _ := e.do(t, http.MethodPost, "/"+e.ownerUsername+"/issues/"+e.projKey+"-999999/links",
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	if code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateIssueLinkTargetNotFound(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": e.projKey + "-999999", "link_type": "blocks"})
	if code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

type linkView struct {
	model.IssueLink
	Direction    string    `json:"direction"`
	DisplayType  string    `json:"display_type"`
	OtherIssueID uuid.UUID `json:"other_issue_id"`
}

func TestHTTPListIssueLinksDirectionalView(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")
	c := e.mustCreateIssue(t, "C")

	cases := []struct {
		linkType     model.LinkType
		incomingName string
		outgoingName string
	}{
		{model.LinkTypeBlocks, "is_blocked_by", "blocks"},
		{model.LinkTypeRelatesTo, "relates_to", "relates_to"},
		{model.LinkTypeClones, "is_cloned_by", "clones"},
	}

	// Each case: A -> B (outgoing on A, incoming on B). Re-use same A and
	// fresh targets so we exercise all four display names on a single read.
	for i, c := range cases {
		// fresh target so unique constraint isn't tripped
		target, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
			ProjectID: e.projectID, Title: fmt.Sprintf("t-%d", i),
		})
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		code, _ := e.do(t, http.MethodPost, e.issueLinksPath(a),
			map[string]any{"target_issue": target.Identifier, "link_type": string(c.linkType)})
		if code != http.StatusCreated {
			t.Fatalf("create %s: code = %d", c.linkType, code)
		}
		_ = b
	}

	// Incoming on A: C -> A (relates_to so the inverse-display path is exercised)
	if code, _ := e.do(t, http.MethodPost, e.issueLinksPath(c),
		map[string]any{"target_issue": a.Identifier, "link_type": "blocks"}); code != http.StatusCreated {
		t.Fatalf("inbound: code = %d", code)
	}

	code, body := e.do(t, http.MethodGet, e.issueLinksPath(a), nil)
	if code != http.StatusOK {
		t.Fatalf("list code = %d", code)
	}
	got := decodePage[linkView](t, body).Items
	if len(got) != len(cases)+1 {
		t.Fatalf("len = %d, want %d", len(got), len(cases)+1)
	}
	outCount, inCount := 0, 0
	for _, v := range got {
		switch v.Direction {
		case "outgoing":
			outCount++
			if v.SourceID != a.ID {
				t.Fatalf("outgoing source = %s, want %s", v.SourceID, a.ID)
			}
			if v.DisplayType != string(v.LinkType) {
				t.Fatalf("outgoing display = %s, want %s", v.DisplayType, v.LinkType)
			}
			if v.OtherIssueID != v.TargetID {
				t.Fatalf("outgoing other = %s, want target %s", v.OtherIssueID, v.TargetID)
			}
		case "incoming":
			inCount++
			if v.TargetID != a.ID {
				t.Fatalf("incoming target = %s, want %s", v.TargetID, a.ID)
			}
			if v.LinkType == model.LinkTypeBlocks && v.DisplayType != "is_blocked_by" {
				t.Fatalf("incoming blocks display = %s", v.DisplayType)
			}
			if v.OtherIssueID != v.SourceID {
				t.Fatalf("incoming other = %s, want source %s", v.OtherIssueID, v.SourceID)
			}
		default:
			t.Fatalf("unknown direction: %s", v.Direction)
		}
	}
	if outCount != len(cases) || inCount != 1 {
		t.Fatalf("outCount = %d, inCount = %d", outCount, inCount)
	}
}

// TestHTTPListIssueLinksIncomingDisplayNames pins down the inverse-display
// table for every link type via incoming-direction lookups.
func TestHTTPListIssueLinksIncomingDisplayNames(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	target := e.mustCreateIssue(t, "target")

	cases := []struct {
		linkType model.LinkType
		display  string
	}{
		{model.LinkTypeBlocks, "is_blocked_by"},
		{model.LinkTypeDuplicates, "is_duplicated_by"},
		{model.LinkTypeClones, "is_cloned_by"},
		{model.LinkTypeRelatesTo, "relates_to"},
	}

	for i, c := range cases {
		src, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
			ProjectID: e.projectID, Title: fmt.Sprintf("src-%d", i),
		})
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		code, _ := e.do(t, http.MethodPost, e.issueLinksPath(src),
			map[string]any{"target_issue": target.Identifier, "link_type": string(c.linkType)})
		if code != http.StatusCreated {
			t.Fatalf("create %s: code = %d", c.linkType, code)
		}
	}

	code, body := e.do(t, http.MethodGet, e.issueLinksPath(target), nil)
	if code != http.StatusOK {
		t.Fatalf("list code = %d", code)
	}
	got := decodePage[linkView](t, body).Items
	displayByType := map[model.LinkType]string{}
	for _, v := range got {
		if v.Direction != "incoming" {
			t.Fatalf("direction = %s, want incoming", v.Direction)
		}
		displayByType[v.LinkType] = v.DisplayType
	}
	for _, c := range cases {
		if got := displayByType[c.linkType]; got != c.display {
			t.Fatalf("incoming %s display = %s, want %s", c.linkType, got, c.display)
		}
	}
}

func TestHTTPListIssueLinksBadID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet, "/"+e.ownerUsername+"/issues/not-a-ref/links", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPListIssueLinksEmpty(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "lone")
	code, body := e.do(t, http.MethodGet, e.issueLinksPath(a), nil)
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	p := decodePage[linkView](t, body)
	if len(p.Items) != 0 || p.NextCursor != nil {
		t.Fatalf("envelope = %+v, want empty items + nil cursor", p)
	}
}

func TestHTTPGetIssueLinkHappy(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")
	_, createBody := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	link := decode[model.IssueLink](t, createBody)

	code, body := e.do(t, http.MethodGet, e.projectLinkPath(link), nil)
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	got := decode[model.IssueLink](t, body)
	if got.ID != link.ID {
		t.Fatalf("id mismatch")
	}
}

func TestHTTPGetIssueLinkBadID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet, e.projectPath()+"/links/zzz", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPGetIssueLinkNotFound(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet, e.projectPath()+"/links/link-999999", nil)
	if code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPUpdateIssueLinkHappy(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")
	c := e.mustCreateIssue(t, "C")
	_, createBody := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	link := decode[model.IssueLink](t, createBody)

	code, body := e.do(t, http.MethodPatch, e.projectLinkPath(link),
		map[string]any{"source_issue": a.Identifier, "target_issue": c.Identifier, "link_type": "clones"})
	if code != http.StatusOK {
		t.Fatalf("code = %d body = %s", code, body)
	}
	got := decode[model.IssueLink](t, body)
	if got.ID != link.ID || got.Ref != link.Ref || got.SourceID != a.ID || got.TargetID != c.ID || got.LinkType != model.LinkTypeClones {
		t.Fatalf("link = %+v, want same ref %s with A clones C", got, link.Ref)
	}
}

func TestHTTPUpdateIssueLinkDuplicatesClosesSource(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "dup")
	b := e.mustCreateIssue(t, "canon")
	_, createBody := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	link := decode[model.IssueLink](t, createBody)

	code, body := e.do(t, http.MethodPatch, e.projectLinkPath(link),
		map[string]any{"source_issue": a.Identifier, "target_issue": b.Identifier, "link_type": "duplicates"})
	if code != http.StatusOK {
		t.Fatalf("code = %d body = %s", code, body)
	}
	updated, err := e.store.GetIssue(e.ctx, a.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if updated.Status != model.StatusClosed {
		t.Fatalf("source status = %s, want closed", updated.Status)
	}
	if updated.CloseReason == nil || *updated.CloseReason != model.CloseReasonDuplicate {
		t.Fatalf("source close reason = %v, want duplicate", updated.CloseReason)
	}
}

func TestHTTPUpdateIssueLinkValidation(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")
	c := e.mustCreateIssue(t, "C")
	other, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "other", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	otherIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: other.ID, Title: "other issue"})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	_, createBody := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	link := decode[model.IssueLink](t, createBody)
	_, createBody = e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": c.Identifier, "link_type": "blocks"})
	duplicateTarget := decode[model.IssueLink](t, createBody)

	req, _ := http.NewRequestWithContext(e.ctx, http.MethodPatch,
		e.ts.URL+apiPath(e.projectLinkPath(link)),
		bytes.NewReader([]byte("nope")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.authToken)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad json code = %d", res.StatusCode)
	}

	for _, tc := range []struct {
		name string
		path string
		body map[string]any
		want int
	}{
		{
			name: "bad link ref",
			path: e.projectPath() + "/links/nope",
			body: map[string]any{"source_issue": a.Identifier, "target_issue": b.Identifier, "link_type": "blocks"},
			want: http.StatusBadRequest,
		},
		{
			name: "bad source ref",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": "not-a-ref", "target_issue": b.Identifier, "link_type": "blocks"},
			want: http.StatusBadRequest,
		},
		{
			name: "bad target ref",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": a.Identifier, "target_issue": "not-a-ref", "link_type": "blocks"},
			want: http.StatusBadRequest,
		},
		{
			name: "invalid type",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": a.Identifier, "target_issue": b.Identifier, "link_type": "explodes"},
			want: http.StatusBadRequest,
		},
		{
			name: "missing source",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": e.projKey + "-999999", "target_issue": b.Identifier, "link_type": "blocks"},
			want: http.StatusNotFound,
		},
		{
			name: "missing target",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": a.Identifier, "target_issue": e.projKey + "-999999", "link_type": "blocks"},
			want: http.StatusNotFound,
		},
		{
			name: "cross project missing source",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": other.Key + "-999999", "target_issue": b.Identifier, "link_type": "blocks"},
			want: http.StatusConflict,
		},
		{
			name: "cross project missing target",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": a.Identifier, "target_issue": other.Key + "-999999", "link_type": "blocks"},
			want: http.StatusConflict,
		},
		{
			name: "cross project",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": a.Identifier, "target_issue": otherIssue.Identifier, "link_type": "blocks"},
			want: http.StatusConflict,
		},
		{
			name: "self",
			path: e.projectLinkPath(link),
			body: map[string]any{"source_issue": a.Identifier, "target_issue": a.Identifier, "link_type": "blocks"},
			want: http.StatusConflict,
		},
		{
			name: "duplicate",
			path: e.projectLinkPath(duplicateTarget),
			body: map[string]any{"source_issue": a.Identifier, "target_issue": b.Identifier, "link_type": "blocks"},
			want: http.StatusConflict,
		},
	} {
		code, _ := e.do(t, http.MethodPatch, tc.path, tc.body)
		if code != tc.want {
			t.Fatalf("%s: code = %d, want %d", tc.name, code, tc.want)
		}
	}
}

func TestHTTPDeleteIssueLink(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")
	_, createBody := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	link := decode[model.IssueLink](t, createBody)

	code, _ := e.do(t, http.MethodDelete, e.projectLinkPath(link), nil)
	if code != http.StatusNoContent {
		t.Fatalf("code = %d", code)
	}
	code, _ = e.do(t, http.MethodDelete, e.projectLinkPath(link), nil)
	if code != http.StatusNotFound {
		t.Fatalf("second delete code = %d", code)
	}
}

func TestHTTPIssueLinkToDeletedIssueCanBeEditedAndRemoved(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := e.mustCreateIssue(t, "A")
	b := e.mustCreateIssue(t, "B")
	c := e.mustCreateIssue(t, "C")
	_, createBody := e.do(t, http.MethodPost, e.issueLinksPath(a),
		map[string]any{"target_issue": b.Identifier, "link_type": "blocks"})
	edited := decode[model.IssueLink](t, createBody)
	_, createBody = e.do(t, http.MethodPost, e.issueLinksPath(c),
		map[string]any{"target_issue": a.Identifier, "link_type": "relates_to"})
	removed := decode[model.IssueLink](t, createBody)
	if err := e.store.DeleteIssue(e.ctx, b.ID); err != nil {
		t.Fatalf("DeleteIssue target: %v", err)
	}
	if err := e.store.DeleteIssue(e.ctx, c.ID); err != nil {
		t.Fatalf("DeleteIssue source: %v", err)
	}
	d := e.mustCreateIssue(t, "D")

	code, body := e.do(t, http.MethodPatch, e.projectLinkPath(edited),
		map[string]any{"source_issue": a.Identifier, "target_issue": d.Identifier, "link_type": "blocks"})
	if code != http.StatusOK {
		t.Fatalf("update code = %d body = %s", code, body)
	}
	got := decode[model.IssueLink](t, body)
	if got.SourceID != a.ID || got.TargetID != d.ID {
		t.Fatalf("updated link = %+v, want A blocks D", got)
	}

	code, body = e.do(t, http.MethodDelete, e.projectLinkPath(removed), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete code = %d body = %s", code, body)
	}
	if _, err := e.store.GetIssueLink(e.ctx, removed.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetIssueLink after delete err = %v, want ErrNotFound", err)
	}
}

func TestHTTPDeleteIssueLinkBadID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodDelete, e.projectPath()+"/links/nope", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}
