package server_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
)

// uploadAll puts one file of each kind in the creator's pending uploads, with
// no credentials but the upload URL's.
func (e *httpEnv) uploadAll(t *testing.T, token string) (png, mp3, mp4, text uploadedJSON) {
	t.Helper()
	created := e.mustUploadURL(t, token)
	png = e.mustUploadFile(t, created.UploadURL, "screen.png", "", formMediaPNG)
	mp3 = e.mustUploadFile(t, created.UploadURL, "take.mp3", "audio/mpeg", formMediaMP3)
	mp4 = e.mustUploadFile(t, created.UploadURL, "demo.mp4", "video/mp4", formMediaMP4)
	text = e.mustUploadFile(t, created.UploadURL, "notes.txt", "text/plain", formMediaText)
	return png, mp3, mp4, text
}

func TestHTTPFormClaimsTheUploadsItNames(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{PublicOrigin: "https://track.example.com/"})
	if created := e.mustUploadURL(t, e.authToken); !strings.HasPrefix(created.UploadURL, "https://track.example.com/api/v1/form-uploads/") {
		t.Fatalf("upload_url = %q, want it on the public origin", created.UploadURL)
	}
	png, mp3, mp4, text := e.uploadAll(t, e.authToken)

	// The first question shows the picture and the recording, its first
	// option shows the video, and the notes are attached to the form. The
	// picture is also named in attachments and the notes twice: each counts once.
	code, raw := e.createFormWithMedia(t, e.authToken, []string{png.Object, mp3.Object}, mp4.Object, []string{text.Object, png.Object, text.Object})
	if code != http.StatusCreated {
		t.Fatalf("create code = %d body = %s", code, raw)
	}
	form := decode[formJSON](t, raw)
	if len(form.Attachments) != 4 {
		t.Fatalf("attachments = %+v, want 4", form.Attachments)
	}
	wantURL := func(object string) string {
		return "https://track.example.com/" + e.ownerUsername + "/projects/" + e.projKey + "/forms/form-1/attachments/" + object + "/content"
	}
	want := []formAttachmentJSON{
		{Object: png.Object, Filename: "screen.png", ContentType: "image/png", ByteSize: int64(len(formMediaPNG)), Kind: "image", URL: wantURL(png.Object)},
		{Object: mp3.Object, Filename: "take.mp3", ContentType: "audio/mpeg", ByteSize: int64(len(formMediaMP3)), Kind: "audio", URL: wantURL(mp3.Object)},
		{Object: mp4.Object, Filename: "demo.mp4", ContentType: "video/mp4", ByteSize: int64(len(formMediaMP4)), Kind: "video", URL: wantURL(mp4.Object)},
		{Object: text.Object, Filename: "notes.txt", ContentType: "text/plain", ByteSize: int64(len(formMediaText)), Kind: "file", URL: wantURL(text.Object)},
	}
	for i, w := range want {
		if form.Attachments[i] != w {
			t.Fatalf("attachment %d = %+v, want %+v", i, form.Attachments[i], w)
		}
	}
	if form.Definition == nil || len(form.Definition.Sections[0].Questions[0].Media) != 2 || form.Definition.Sections[0].Questions[0].Options[0].Media != mp4.Object {
		t.Fatalf("definition keeps no media: %+v", form.Definition)
	}
	if e.pendingUploads(t) != 0 {
		t.Fatalf("pending uploads = %d, want every upload claimed", e.pendingUploads(t))
	}

	// Reading the form again gives the same files; a list row carries none.
	code, raw = e.do(t, http.MethodGet, e.formPath(form.Ref), nil)
	if got := decode[formJSON](t, raw); code != http.StatusOK || len(got.Attachments) != 4 || got.Attachments[2] != want[2] {
		t.Fatalf("get code = %d attachments = %+v", code, got.Attachments)
	}
	code, raw = e.do(t, http.MethodGet, e.formsPath(), nil)
	if code != http.StatusOK || strings.Contains(string(raw), "attachments") {
		t.Fatalf("list code = %d body = %s, want rows without attachments", code, raw)
	}
	// The other answers to a form say what files it has, too.
	e.do(t, http.MethodPut, e.formPath(form.Ref)+"/answers", map[string]any{"answers": map[string]any{"rotated": map[string]any{"checked": true}}})
	code, raw = e.do(t, http.MethodPost, e.formPath(form.Ref)+"/submit", nil)
	if got := decode[formJSON](t, raw); code != http.StatusOK || len(got.Attachments) != 4 {
		t.Fatalf("submit code = %d attachments = %+v", code, got.Attachments)
	}

	// Each file is served from its form: an image inline, the rest as a download.
	res, body := e.doRaw(t, e.authToken, http.MethodGet, e.formContentPath(form.Ref, png.Object), nil, "")
	if res.StatusCode != http.StatusOK || !bytes.Equal(body, formMediaPNG) || res.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("image: code = %d headers = %v", res.StatusCode, res.Header)
	}
	res, body = e.doRaw(t, e.authToken, http.MethodGet, e.formContentPath(form.Ref, mp3.Object), nil, "")
	if res.StatusCode != http.StatusOK || !bytes.Equal(body, formMediaMP3) || res.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("audio: code = %d headers = %v", res.StatusCode, res.Header)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(strings.Join(res.Header.Values("Content-Security-Policy"), ";"), "sandbox") {
		t.Fatalf("form media is served without the upload protections: %v", res.Header)
	}
}

func TestHTTPCreateFormRefusesUploadsItCannotClaim(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	member, memberToken := e.mustProjectMemberToken(t, "claim-member")
	mine := e.mustUploadFile(t, e.mustUploadURL(t, e.authToken).UploadURL, "mine.png", "", formMediaPNG)
	theirs := e.mustUploadFile(t, e.mustUploadURL(t, memberToken).UploadURL, "theirs.png", "", formMediaPNG)
	_ = member

	// Already on another form.
	taken := e.mustUploadFile(t, e.mustUploadURL(t, e.authToken).UploadURL, "taken.png", "", formMediaPNG)
	if code, raw := e.createFormWithMedia(t, e.authToken, nil, "", []string{taken.Object}); code != http.StatusCreated {
		t.Fatalf("first claim code = %d body = %s", code, raw)
	}
	// A deleted upload, a generic project object, an issue attachment and a
	// number that was never issued.
	deleted := e.mustUploadFile(t, e.mustUploadURL(t, e.authToken).UploadURL, "deleted.png", "", formMediaPNG)
	var deletedID string
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM storage_objects WHERE project_id = $1 AND number = $2`, e.projectID, mustNumber(t, deleted.Object)).Scan(&deletedID); err != nil {
		t.Fatalf("find deleted: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE storage_objects SET deleted_at = now() WHERE id = $1`, deletedID); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	code, raw := e.doMultipartObject(t, e.authToken, "generic.png", formMediaPNG)
	if code != http.StatusCreated {
		t.Fatalf("generic object code = %d body = %s", code, raw)
	}
	generic := decode[model.StorageObject](t, raw)
	issue := e.mustCreateIssue(t, "Has a file")
	code, raw = e.doMultipartPath(t, e.authToken, e.issueAttachmentsPath(issue), "issue.png", formMediaPNG)
	if code != http.StatusCreated {
		t.Fatalf("issue attachment code = %d body = %s", code, raw)
	}
	onIssue := decode[model.IssueAttachment](t, raw).Object

	pending := e.pendingUploads(t)
	nextForm := e.countRows(t, `SELECT next_form_number FROM projects WHERE id = $1`, e.projectID)
	for name, ref := range map[string]string{
		"someone else's pending upload": theirs.Object,
		"attached to another form":      taken.Object,
		"deleted":                       deleted.Object,
		"a generic project object":      generic.Ref,
		"an issue attachment":           onIssue.Ref,
		"missing":                       "object-999",
	} {
		for where, post := range map[string]func() (int, []byte){
			"attachments": func() (int, []byte) {
				return e.createFormWithMedia(t, e.authToken, nil, "", []string{mine.Object, ref})
			},
			"question": func() (int, []byte) { return e.createFormWithMedia(t, e.authToken, []string{ref}, "", nil) },
			"option":   func() (int, []byte) { return e.createFormWithMedia(t, e.authToken, nil, ref, nil) },
		} {
			code, raw := post()
			wantMessage := "media: " + ref + " is not one of your uploads waiting for a form"
			if code != http.StatusBadRequest || !strings.Contains(string(raw), wantMessage) {
				t.Fatalf("%s in %s: code = %d body = %s, want 400 %q", name, where, code, raw, wantMessage)
			}
		}
		if got := e.pendingUploads(t); got != pending {
			t.Fatalf("%s: pending uploads = %d, want %d: a refused form claimed some", name, got, pending)
		}
	}
	if got := e.countRows(t, `SELECT next_form_number FROM projects WHERE id = $1`, e.projectID); got != nextForm {
		t.Fatalf("next form number = %d, want %d: a refused form took one", got, nextForm)
	}

	// A ref that is not an object ref, wherever it is written, is a plain 400
	// that says where.
	for _, tc := range []struct {
		name  string
		media []string
		opt   string
		att   []string
		field string
	}{
		{"attachments", nil, "", []string{"nope"}, "attachments[0]"},
		{"question media", []string{"object-1", "file-2"}, "", nil, "media[1]"},
		{"option media", nil, "https://example.com/a.png", nil, "options[0].media"},
	} {
		code, raw := e.createFormWithMedia(t, e.authToken, tc.media, tc.opt, tc.att)
		if code != http.StatusBadRequest || !strings.Contains(string(raw), tc.field) || !strings.Contains(string(raw), "not an object ref") {
			t.Fatalf("%s: code = %d body = %s", tc.name, code, raw)
		}
	}

	// Twenty files are the most a form takes; the 21st is refused with every
	// upload still pending.
	numbers := make([]string, 0, 21)
	url := e.mustUploadURL(t, e.authToken)
	for i := range 21 {
		if i == 20 {
			url = e.mustUploadURL(t, e.authToken)
		}
		numbers = append(numbers, e.mustUploadFile(t, url.UploadURL, fmt.Sprintf("f%d.png", i), "", formMediaPNG).Object)
	}
	pending = e.pendingUploads(t)
	if code, raw := e.createFormWithMedia(t, e.authToken, nil, "", numbers); code != http.StatusBadRequest || !strings.Contains(string(raw), "at most 20 files") {
		t.Fatalf("21 files code = %d body = %s", code, raw)
	}
	if got := e.pendingUploads(t); got != pending {
		t.Fatalf("pending uploads after the refused 21 = %d, want %d", got, pending)
	}
	if code, raw := e.createFormWithMedia(t, e.authToken, nil, "", numbers[:20]); code != http.StatusCreated {
		t.Fatalf("20 files code = %d body = %s", code, raw)
	}
	// The upload that was fine all along (mine) is still there to use.
	if code, raw := e.createFormWithMedia(t, e.authToken, []string{mine.Object}, "", nil); code != http.StatusCreated {
		t.Fatalf("claiming my upload code = %d body = %s", code, raw)
	}
}

func mustNumber(t *testing.T, ref string) int {
	t.Helper()
	n, ok := model.ParseStorageObjectRef(ref)
	if !ok {
		t.Fatalf("%q is not an object ref", ref)
	}
	return n
}

func TestFormMediaIsHiddenFromEveryGenericObjectRoute(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	_, memberToken := e.mustProjectMemberToken(t, "vis-member")
	_, outsiderToken := e.mustUserToken(t, "vis-outsider")
	e.makeProjectPublic(t)
	issue := e.mustCreateIssue(t, "Ordinary issue")

	// One plain object people may see, one file on a form, one still pending.
	code, raw := e.doMultipartObject(t, e.authToken, "plain.png", formMediaPNG)
	if code != http.StatusCreated {
		t.Fatalf("plain object code = %d body = %s", code, raw)
	}
	plain := decode[model.StorageObject](t, raw)
	url := e.mustUploadURL(t, e.authToken)
	attached := e.mustUploadFile(t, url.UploadURL, "on-form.png", "", formMediaPNG)
	pending := e.mustUploadFile(t, url.UploadURL, "pending.png", "", formMediaPNG)
	code, raw = e.createFormWithMedia(t, e.authToken, nil, "", []string{attached.Object})
	if code != http.StatusCreated {
		t.Fatalf("create form code = %d body = %s", code, raw)
	}
	form := decode[formJSON](t, raw)

	people := map[string]string{"owner": e.authToken, "member": memberToken, "public viewer": outsiderToken}
	for who, token := range people {
		// The list shows the plain object and neither form file.
		code, raw := e.doWithToken(t, token, http.MethodGet, e.projectObjectsPath(), nil)
		if code != http.StatusOK || !strings.Contains(string(raw), plain.Ref) || strings.Contains(string(raw), attached.Object) || strings.Contains(string(raw), pending.Object) || strings.Contains(string(raw), "on-form.png") {
			t.Fatalf("%s: list code = %d body = %s", who, code, raw)
		}
		for _, object := range []string{attached.Object, pending.Object} {
			for _, route := range []struct{ method, path string }{
				{http.MethodGet, e.projectObjectsPath() + "/" + object},
				{http.MethodGet, e.projectObjectsPath() + "/" + object + "/content"},
				{http.MethodDelete, e.projectObjectsPath() + "/" + object},
				{http.MethodGet, e.projectPath() + "/attachments/" + object + "/content"},
				{http.MethodGet, e.issueAttachmentsPath(issue) + "/" + object + "/content"},
			} {
				if code, raw := e.doWithToken(t, token, route.method, route.path, nil); code != http.StatusNotFound {
					t.Fatalf("%s: %s %s code = %d body = %s, want 404", who, route.method, route.path, code, raw)
				}
			}
			for _, path := range []string{
				e.projectPath() + "/attachments/" + object + "/content",
				"/" + e.ownerUsername + "/issues/" + issue.Identifier + "/attachments/" + object + "/content",
			} {
				res := e.uiDoNoRedirect(t, http.MethodGet, path, token, nil)
				res.Body.Close()
				if res.StatusCode != http.StatusNotFound {
					t.Fatalf("%s: UI %s code = %d, want 404", who, path, res.StatusCode)
				}
			}
		}
		// The plain object is still reachable.
		if code, _ := e.doWithToken(t, token, http.MethodGet, e.projectObjectsPath()+"/"+plain.Ref, nil); code != http.StatusOK {
			t.Fatalf("%s: plain object code = %d", who, code)
		}
	}
	// A member can still delete a plain object but a form's file is not one
	// they can delete from here.
	if code, _ := e.doWithToken(t, memberToken, http.MethodDelete, e.projectObjectsPath()+"/"+attached.Object, nil); code != http.StatusNotFound {
		t.Fatalf("member delete of form media code = %d", code)
	}

	// The same through MCP.
	for who, token := range people {
		session := mcpConnect(t, e, token)
		listed := mcpCall(t, e, session, "track_list_objects", mcpFormArgs(e, nil))
		items := decodeMCPField[[]model.StorageObject](t, listed, "items")
		if len(items) != 1 || items[0].Ref != plain.Ref {
			t.Fatalf("%s: track_list_objects = %+v, want only the plain object", who, items)
		}
		for _, tool := range []string{"track_get_object", "track_read_object_content"} {
			for _, object := range []string{attached.Object, pending.Object} {
				out := mcpCallExpectError(t, e, session, tool, mcpFormArgs(e, map[string]any{"object": object}))
				requireMCPErrorCode(t, out, "not_found")
			}
			mcpCall(t, e, session, tool, mcpFormArgs(e, map[string]any{"object": plain.Ref}))
		}
		for _, object := range []string{attached.Object, pending.Object} {
			_, err := session.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: fmt.Sprintf("track://object-content/%s/%s/%s", e.ownerUsername, e.projKey, object)})
			if err == nil {
				t.Fatalf("%s: track://object-content read form media %s", who, object)
			}
		}
	}
	_ = form
}

func TestFormContentRoutesAreForMembersOnly(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	_, memberToken := e.mustProjectMemberToken(t, "content-member")
	readOnly, readOnlyToken := e.mustProjectMemberToken(t, "content-ro")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readOnly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "content-outsider")
	blocked, blockedToken := e.mustUserToken(t, "content-blocked")

	first := e.mustUploadFile(t, e.mustUploadURL(t, e.authToken).UploadURL, "one.png", "", formMediaPNG)
	second := e.mustUploadFile(t, e.mustUploadURL(t, e.authToken).UploadURL, "two.png", "", formMediaPNG)
	pending := e.mustUploadFile(t, e.mustUploadURL(t, e.authToken).UploadURL, "pending.png", "", formMediaPNG)
	one := e.mustCreateFormWithAttachments(t, []string{first.Object})
	two := e.mustCreateFormWithAttachments(t, []string{second.Object})

	apiPath := func(form formJSON, object string) string { return e.formContentPath(form.Ref, object) }
	uiPath := func(form formJSON, object string) string {
		return "/" + e.ownerUsername + "/projects/" + e.projKey + "/forms/" + form.Ref + "/attachments/" + object + "/content"
	}
	get := func(surface, token string, form formJSON, object string) int {
		t.Helper()
		if surface == "api" {
			code, _ := e.doWithToken(t, token, http.MethodGet, apiPath(form, object), nil)
			return code
		}
		res := e.uiDoNoRedirect(t, http.MethodGet, uiPath(form, object), token, nil)
		res.Body.Close()
		return res.StatusCode
	}

	for _, surface := range []string{"api", "ui"} {
		// Members, read-only ones included, and the owner.
		for who, token := range map[string]string{"owner": e.authToken, "member": memberToken, "read-only member": readOnlyToken} {
			if code := get(surface, token, one, first.Object); code != http.StatusOK {
				t.Fatalf("%s %s: code = %d, want 200", surface, who, code)
			}
			// Another form's file, a pending upload, a missing one: not found.
			for name, object := range map[string]string{"another form's file": second.Object, "a pending upload": pending.Object, "a missing file": "object-99"} {
				if code := get(surface, token, one, object); code != http.StatusNotFound {
					t.Fatalf("%s %s asking for %s: code = %d, want 404", surface, who, name, code)
				}
			}
			if code := get(surface, token, two, second.Object); code != http.StatusOK {
				t.Fatalf("%s %s: second form code = %d", surface, who, code)
			}
			if code := get(surface, token, formJSON{Ref: "form-99"}, first.Object); code != http.StatusNotFound {
				t.Fatalf("%s %s: missing form code = %d, want 404", surface, who, code)
			}
		}
		// A malformed object ref.
		wantBad := http.StatusBadRequest
		if surface == "ui" {
			wantBad = http.StatusNotFound
		}
		if code := get(surface, e.authToken, one, "photo-1"); code != wantBad {
			t.Fatalf("%s malformed object ref code = %d, want %d", surface, code, wantBad)
		}
		// On a private project an outsider is refused, signed out is too.
		if code := get(surface, outsiderToken, one, first.Object); code != http.StatusForbidden && code != http.StatusNotFound {
			t.Fatalf("%s outsider on a private project code = %d", surface, code)
		}
		if code := get(surface, "", one, first.Object); code == http.StatusOK {
			t.Fatalf("%s signed out reads form media", surface)
		}
	}

	// A public viewer is told nothing is there, and a blocked user gets nothing.
	e.makeProjectPublic(t)
	for _, surface := range []string{"api", "ui"} {
		if code := get(surface, outsiderToken, one, first.Object); code != http.StatusNotFound {
			t.Fatalf("%s public viewer code = %d, want 404", surface, code)
		}
		if code := get(surface, "", one, first.Object); code != http.StatusNotFound && code != http.StatusUnauthorized && code != http.StatusSeeOther {
			t.Fatalf("%s signed out on a public project code = %d", surface, code)
		}
	}
	if _, err := e.store.BlockProjectUser(e.ctx, e.projectID, blocked.ID, e.adminID); err != nil {
		t.Fatalf("BlockProjectUser: %v", err)
	}
	for _, surface := range []string{"api", "ui"} {
		if code := get(surface, blockedToken, one, first.Object); code != http.StatusForbidden && code != http.StatusNotFound {
			t.Fatalf("%s blocked user code = %d", surface, code)
		}
	}

	// A help-desk reporter has no project-level read at all.
	f := newHelpDeskFixture(t, e)
	for _, surface := range []string{"api", "ui"} {
		if code := get(surface, f.aliceToken, one, first.Object); code != http.StatusForbidden && code != http.StatusNotFound {
			t.Fatalf("%s help-desk reporter code = %d", surface, code)
		}
	}
}

func (e *httpEnv) mustCreateFormWithAttachments(t *testing.T, attachments []string) formJSON {
	t.Helper()
	code, raw := e.createFormWithMedia(t, e.authToken, nil, "", attachments)
	if code != http.StatusCreated {
		t.Fatalf("create code = %d body = %s", code, raw)
	}
	return decode[formJSON](t, raw)
}

func TestFormUploadWithoutObjectStorageIsUnavailable(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	created := e.mustUploadURL(t, e.authToken)
	if created.MaxFileBytes != 25<<20 || created.MaxFiles != 20 {
		t.Fatalf("default limits = %+v, want 25 MiB and 20 files", created)
	}
	if code, raw := e.postFile(t, "", created.UploadURL, "a.png", "", formMediaPNG); code != http.StatusServiceUnavailable {
		t.Fatalf("upload with no storage code = %d body = %s, want 503", code, raw)
	}
}

func TestFormUploadLimitsNeverExceedTheStorageLimit(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{FormMedia: server.FormMediaOptions{MaxFileBytes: 1 << 30}})
	if created := e.mustUploadURL(t, e.authToken); created.MaxFileBytes != 4096 {
		t.Fatalf("max_file_bytes = %d, want the storage limit 4096", created.MaxFileBytes)
	}
}

func TestMCPFormUploadsAndAttachments(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 4096)
	e := newMCPHTTPEnv(t, storageSvc)
	session := mcpConnect(t, e, e.authToken)

	out := mcpCall(t, e, session, "track_create_form_upload_url", mcpFormArgs(e, nil))
	created := uploadURLJSON{
		UploadURL:    decodeMCPField[string](t, out, "upload_url"),
		ExpiresAt:    decodeMCPField[time.Time](t, out, "expires_at"),
		MaxFiles:     decodeMCPField[int](t, out, "max_files"),
		MaxFileBytes: decodeMCPField[int64](t, out, "max_file_bytes"),
	}
	if !strings.HasPrefix(created.UploadURL, "/api/v1/form-uploads/") || created.MaxFiles != 20 || created.MaxFileBytes != 4096 || !created.ExpiresAt.After(time.Now().Add(25*time.Minute)) {
		t.Fatalf("upload URL = %+v", created)
	}
	png := e.mustUploadFile(t, created.UploadURL, "screen.png", "", formMediaPNG)
	mp3 := e.mustUploadFile(t, created.UploadURL, "take.mp3", "audio/mpeg", formMediaMP3)
	text := e.mustUploadFile(t, created.UploadURL, "notes.txt", "text/plain", formMediaText)

	sections := mcpFormSections()
	first := sections[0].(map[string]any)["questions"].([]map[string]any)[0]
	first["media"] = []string{png.Object}
	first["options"].([]map[string]any)[1]["media"] = mp3.Object
	form := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_create_form", mcpFormArgs(e, map[string]any{
		"title": "With files", "sections": sections, "attachments": []string{text.Object},
	})), "form")
	if len(form.Attachments) != 3 || form.Attachments[0].Object != png.Object || form.Attachments[0].Kind != "image" ||
		form.Attachments[1].Kind != "audio" || form.Attachments[2].Kind != "file" ||
		form.Attachments[2].URL != "/"+e.ownerUsername+"/projects/"+e.projKey+"/forms/form-1/attachments/"+text.Object+"/content" {
		t.Fatalf("created attachments = %+v", form.Attachments)
	}

	// Both the brief and the full form name the files; a list does not.
	brief := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_get_form", mcpFormArgs(e, map[string]any{"form": form.Ref})), "form")
	full := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_get_form", mcpFormArgs(e, map[string]any{"form": form.Ref, "include_definition": true})), "form")
	if len(brief.Attachments) != 3 || len(full.Attachments) != 3 || brief.Definition != nil || full.Definition == nil {
		t.Fatalf("brief = %+v, full = %+v", brief.Attachments, full.Attachments)
	}
	listed := mcpCall(t, e, session, "track_list_forms", mcpFormArgs(e, nil))
	if raw := string(listed["items"]); strings.Contains(raw, "attachments") {
		t.Fatalf("track_list_forms rows carry attachments: %s", raw)
	}

	// A file that isn't the caller's pending upload is a validation error that
	// names it, and one that is not a ref says where.
	mine := e.mustUploadFile(t, created.UploadURL+"", "mine.png", "", formMediaPNG)
	_ = mine
	for _, args := range []map[string]any{
		{"attachments": []string{"object-999"}}, // not claimable
		{"attachments": []string{"nope"}},       // not a ref
	} {
		args["title"], args["sections"] = "Bad", mcpFormSections()
		out := mcpCallExpectError(t, e, session, "track_create_form", mcpFormArgs(e, args))
		requireMCPErrorCode(t, out, "validation_error")
	}
	if got := e.pendingUploads(t); got != 1 {
		t.Fatalf("pending uploads = %d, want the one nobody named", got)
	}

	// The tool descriptions teach the flow, and an upload URL can be made only
	// by someone who can write.
	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "track_create_form_upload_url":
			for _, want := range []string{"curl -sS -F file=@PATH", "object-N", "media", "attachments", "30 minutes", "20 files", "25 MB", "within a day"} {
				if !strings.Contains(tool.Description, want) {
					t.Fatalf("upload URL tool doesn't say %q: %s", want, tool.Description)
				}
			}
		case "track_create_form":
			if !strings.Contains(tool.Description, "track_create_form_upload_url") || !strings.Contains(tool.Description, "media") {
				t.Fatalf("track_create_form doesn't point at uploads: %s", tool.Description)
			}
		}
	}
	readOnly, readOnlyToken := e.mustProjectMemberToken(t, "mcp-ro")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readOnly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	roSession := mcpConnect(t, e, readOnlyToken)
	requireMCPErrorCode(t, mcpCallExpectError(t, e, roSession, "track_create_form_upload_url", mcpFormArgs(e, nil)), "forbidden")
	_, outsiderToken := e.mustUserToken(t, "mcp-outsider")
	e.makeProjectPublic(t)
	outsider := mcpConnect(t, e, outsiderToken)
	requireMCPErrorCode(t, mcpCallExpectError(t, e, outsider, "track_create_form_upload_url", mcpFormArgs(e, nil)), "not_found")
}
