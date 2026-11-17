package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/server"
)

const expiryMaxAge = 30 * 24 * time.Hour

// removeFormMedia ages the form's cancelled_at and runs the cleanup that the
// worker runs every hour.
func (e *httpEnv) removeFormMedia(t *testing.T, ref string) {
	t.Helper()
	if _, err := e.pool.Exec(e.ctx, `UPDATE forms SET cancelled_at = now() - interval '31 days' WHERE project_id = $1 AND number = $2`, e.projectID, mustFormNumber(t, ref)); err != nil {
		t.Fatalf("age the form: %v", err)
	}
	done, err := e.store.ExpireFormMedia(e.ctx, expiryMaxAge, 10)
	if err != nil || done.Forms != 1 {
		t.Fatalf("ExpireFormMedia = %+v, %v, want the form", done, err)
	}
}

func mustFormNumber(t *testing.T, ref string) int {
	t.Helper()
	number, err := strconv.Atoi(strings.TrimPrefix(ref, "form-"))
	if err != nil {
		t.Fatalf("form ref %q: %v", ref, err)
	}
	return number
}

func TestFormMediaRemovalShowsOnRESTAndMCPAndStopsTheContentRoutes(t *testing.T) {
	t.Parallel()
	e, root := formMediaEnv(t, server.Options{})
	form, refs := uiFormMediaForm(t, e)
	if before := storedFiles(t, root); before == 0 || len(form.Attachments) == 0 {
		t.Fatalf("the form has no files to remove: %d stored, %d attachments", before, len(form.Attachments))
	}
	object := refs["a.png"]

	// While the files are kept, the form says nothing was removed.
	code, raw := e.do(t, http.MethodGet, e.formPath(form.Ref), nil)
	if got := decode[formJSON](t, raw); code != http.StatusOK || got.MediaRemovedAt != nil || len(got.Attachments) == 0 {
		t.Fatalf("before: code = %d removed = %v attachments = %d", code, got.MediaRemovedAt, len(got.Attachments))
	}
	if code, _ := e.do(t, http.MethodPost, e.formPath(form.Ref)+"/cancel", nil); code != http.StatusOK {
		t.Fatalf("cancel code = %d", code)
	}
	e.removeFormMedia(t, form.Ref)

	// REST: the marker is set and attachments is empty, not null.
	code, raw = e.do(t, http.MethodGet, e.formPath(form.Ref), nil)
	got := decode[formJSON](t, raw)
	if code != http.StatusOK || got.MediaRemovedAt == nil || time.Since(*got.MediaRemovedAt) > time.Minute || got.Attachments == nil || len(got.Attachments) != 0 {
		t.Fatalf("REST after: code = %d removed = %v attachments = %#v", code, got.MediaRemovedAt, got.Attachments)
	}
	if !strings.Contains(string(raw), `"attachments":[]`) || !strings.Contains(string(raw), `"media_removed_at":"`) {
		t.Fatalf("REST body = %s", raw)
	}
	// A list row carries it too.
	code, raw = e.do(t, http.MethodGet, e.formsPath(), nil)
	if rows := decode[struct {
		Items []formJSON `json:"items"`
	}](t, raw); code != http.StatusOK || len(rows.Items) != 1 || rows.Items[0].MediaRemovedAt == nil {
		t.Fatalf("REST list = %d %s", code, raw)
	}

	// The content routes answer 404 for a removed file, for the API and the page.
	if res, _ := e.getMedia(t, false, e.authToken, e.formContentPath(form.Ref, object), nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("API content code = %d, want 404", res.StatusCode)
	}
	if res, _ := e.getMedia(t, true, e.authToken, e.projectPath()+"/forms/"+form.Ref+"/attachments/"+object+"/content", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("page content code = %d, want 404", res.StatusCode)
	}

	// MCP: both outputs carry the marker and no attachments.
	session := mcpConnect(t, e, e.authToken)
	for name, extra := range map[string]map[string]any{"brief": {"form": form.Ref}, "full": {"form": form.Ref, "include_definition": true}} {
		out := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_get_form", mcpFormArgs(e, extra)), "form")
		if out.MediaRemovedAt == nil || out.Attachments == nil || len(out.Attachments) != 0 {
			t.Fatalf("MCP %s: removed = %v attachments = %#v", name, out.MediaRemovedAt, out.Attachments)
		}
	}
	listed := decodeMCPField[[]formJSON](t, mcpCall(t, e, session, "track_list_forms", mcpFormArgs(e, nil)), "items")
	if len(listed) != 1 || listed[0].MediaRemovedAt == nil {
		t.Fatalf("MCP list = %+v", listed)
	}
	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	described := false
	for _, tool := range tools.Tools {
		if tool.Name == "track_get_form" {
			described = strings.Contains(tool.Description, "deleted 30 days after it is picked up or cancelled") && strings.Contains(tool.Description, "media_removed_at")
		}
	}
	if !described {
		t.Fatal("track_get_form does not say when a form's files are deleted")
	}
}

func TestFormMediaRemovedPageReadsCorrectly(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	_, readerToken := e.mustUserToken(t, "removed-reader")
	e.makeProjectPublic(t)
	form, refs := uiFormMediaForm(t, e)
	path := e.projectPath() + "/forms/" + form.Ref
	if code, _ := e.do(t, http.MethodPost, e.formPath(form.Ref)+"/cancel", nil); code != http.StatusOK {
		t.Fatalf("cancel code = %d", code)
	}
	_, before := e.uiFormGetDoc(t, path, e.authToken)
	requireContains(t, "before removal", before, "data-form-files", "stray.pdf", "/attachments/"+refs["hero.png"])
	requireOmits(t, "before removal", before, "media was removed")

	e.removeFormMedia(t, form.Ref)
	res, body := e.uiFormGetDoc(t, path, e.authToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("page code = %d", res.StatusCode)
	}
	requireContains(t, "after removal", body, "data-form-media-removed-note", "This form's media was removed on ", "<time datetime=", "Cancelled")
	// No Files card, no picture, player or link to a removed file; the
	// Markdown that named files is inert text.
	requireOmits(t, "after removal", body, "data-form-files", `loading="lazy"`, "<audio", "<video", "data-form-media-image", "data-form-media-file", "/attachments/", "stray.pdf", refs["hero.png"], refs["q.txt"])
	_, panel := e.uiFormGetDoc(t, path+"/panel", e.authToken)
	requireContains(t, "panel", panel, "data-form-media-removed-note")
	requireOmits(t, "panel", panel, "data-form-files", `loading="lazy"`, "/attachments/")

	// Someone who is not a member still gets nothing, and learns nothing.
	for who, token := range map[string]string{"reader who is not a member": readerToken, "visitor": ""} {
		for _, target := range []string{path, path + "/panel", path + "/attachments/" + refs["a.png"] + "/content"} {
			res, body := e.uiFormGetDoc(t, target, token)
			if res.StatusCode == http.StatusOK {
				t.Fatalf("%s got 200 from %s", who, target)
			}
			requireOmits(t, who+" "+target, body, "media was removed", "Media removed", "Pick the thumbnail")
		}
	}
}

func TestFormWithoutMediaNeverShowsARemovalLine(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formsPath(), map[string]any{"title": "Plain", "sections": formTestSections()})
	if code != http.StatusCreated {
		t.Fatalf("create code = %d body = %s", code, raw)
	}
	form := decode[formJSON](t, raw)
	_, body := e.uiFormGetDoc(t, e.projectPath()+"/forms/"+form.Ref+"?step=1", e.authToken)
	requireOmits(t, "plain form", body, "media was removed", "Media removed")
}
