package server_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
)

// answerFilesSections is a form with a required file question, an optional
// choice and an optional text question.
func answerFilesSections() []map[string]any {
	return []map[string]any{{
		"questions": []map[string]any{
			{"id": "shot", "type": "file", "label": "Attach a screenshot"},
			{
				"id": "channel", "type": "single_choice", "label": "Which channel?", "required": false,
				"options": []map[string]any{{"id": "a", "label": "A"}, {"id": "b", "label": "B"}},
			},
			{"id": "notes", "type": "text", "label": "Notes", "required": false},
			{"id": "extra", "type": "file", "label": "Anything else to show?", "required": false},
		},
	}}
}

func (e *httpEnv) mustCreateAnswerFilesForm(t *testing.T) formJSON {
	t.Helper()
	code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formsPath(), map[string]any{"title": "Needs files", "sections": answerFilesSections()})
	if code != http.StatusCreated {
		t.Fatalf("create form code = %d body = %s", code, raw)
	}
	return decode[formJSON](t, raw)
}

func (e *httpEnv) answerFilesPath(ref, question string) string {
	return e.formPath(ref) + "/answers/" + question + "/files"
}

func (e *httpEnv) mustAttachAnswerFile(t *testing.T, token, ref, question, name, contentType string, content []byte) formAttachmentJSON {
	t.Helper()
	code, raw := e.postFile(t, token, apiPath(e.answerFilesPath(ref, question)), name, contentType, content)
	if code != http.StatusCreated {
		t.Fatalf("attach %s code = %d body = %s", name, code, raw)
	}
	return decode[formAttachmentJSON](t, raw)
}

func (e *httpEnv) mustGetForm(t *testing.T, token, ref string) formJSON {
	t.Helper()
	code, raw := e.doWithToken(t, token, http.MethodGet, e.formPath(ref), nil)
	if code != http.StatusOK {
		t.Fatalf("get form code = %d body = %s", code, raw)
	}
	return decode[formJSON](t, raw)
}

func TestHTTPFormAnswerFilesLifecycle(t *testing.T) {
	t.Parallel()
	e, root := formMediaEnv(t, server.Options{FormMedia: server.FormMediaOptions{MaxFileBytes: 1024}})
	form := e.mustCreateAnswerFilesForm(t)

	// A required file question blocks Done until a file is attached.
	if code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formPath(form.Ref)+"/submit", nil); code != http.StatusBadRequest || !strings.Contains(string(raw), "shot") {
		t.Fatalf("submit with no file code = %d body = %s", code, raw)
	}
	if got := e.mustGetForm(t, e.authToken, form.Ref); got.Responses[0].Answered || len(got.Responses[0].Files) != 0 || got.Responses[0].Files == nil {
		t.Fatalf("response before a file = %+v, want an empty files list", got.Responses[0])
	}

	// One file per request; the answer's file comes back described.
	shot := e.mustAttachAnswerFile(t, e.authToken, form.Ref, "shot", "screen.png", "", formMediaPNG)
	if shot.Object != "object-1" || shot.Filename != "screen.png" || shot.ContentType != "image/png" || shot.ByteSize != int64(len(formMediaPNG)) || shot.Kind != "image" {
		t.Fatalf("attached = %+v", shot)
	}
	clip := e.mustAttachAnswerFile(t, e.authToken, form.Ref, "channel", "voice.m4a", "", []byte("0123456789"))
	if clip.Kind != "audio" || clip.ContentType != "audio/mp4" {
		t.Fatalf("clip = %+v: an upload with no type takes it from the extension", clip)
	}
	note := e.mustAttachAnswerFile(t, e.authToken, form.Ref, "channel", "notes.txt", "text/plain", formMediaText)
	if note.Kind != "file" {
		t.Fatalf("note kind = %q", note.Kind)
	}
	if got := storedFiles(t, root); got != 3 {
		t.Fatalf("stored files = %d, want 3", got)
	}

	got := e.mustGetForm(t, e.authToken, form.Ref)
	if !got.Responses[0].Answered || len(got.Responses[0].Files) != 1 || got.Responses[0].Files[0].Object != shot.Object ||
		got.Responses[0].Files[0].URL != e.formContentPath(form.Ref, shot.Object) || got.Responses[0].Files[0].Kind != "image" {
		t.Fatalf("shot response = %+v", got.Responses[0])
	}
	// Files on a choice question are extras: shown, and not an answer.
	if got.Responses[1].Answered || len(got.Responses[1].Files) != 2 {
		t.Fatalf("channel response = %+v", got.Responses[1])
	}
	// The top-level attachments are the asker's files only.
	if len(got.Attachments) != 0 {
		t.Fatalf("attachments = %+v, want none: the asker put nothing in the form", got.Attachments)
	}
	if got.RequiredAnswered != 0 && got.RequiredCount != 0 {
		t.Fatalf("unexpected counts on the full form: %+v", got)
	}
	// The derived files are in the form's answers too, and a save leaves them.
	if len(got.Answers["shot"].Files) != 1 {
		t.Fatalf("answers = %+v", got.Answers)
	}
	code, raw := e.doWithToken(t, e.authToken, http.MethodPut, e.formPath(form.Ref)+"/answers", map[string]any{
		"answers": map[string]any{"shot": map[string]any{"files": []string{"object-77"}, "note": "see it"}, "channel": map[string]any{"selected": []string{"a"}}},
	})
	saved := decode[formJSON](t, raw)
	if code != http.StatusOK || len(saved.Responses[0].Files) != 1 || saved.Responses[0].Files[0].Object != shot.Object || saved.Responses[0].Note != "see it" || len(saved.Responses[1].Files) != 2 {
		t.Fatalf("save with files code = %d body = %s", code, raw)
	}

	// Members read the bytes through the form's content route.
	_, memberToken := e.mustProjectMemberToken(t, "answer-files-reader")
	for _, token := range []string{e.authToken, memberToken} {
		res, body := e.doRaw(t, token, http.MethodGet, e.formContentPath(form.Ref, shot.Object), nil, "")
		if res.StatusCode != http.StatusOK || !bytes.Equal(body, formMediaPNG) || res.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("content code = %d type = %q", res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	res, _ := e.doRaw(t, e.authToken, http.MethodGet, e.formContentPath(form.Ref, clip.Object), nil, "")
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "audio/mp4" {
		t.Fatalf("clip content code = %d type = %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	// No generic object route shows an answer file.
	for _, path := range []string{e.projectObjectsPath(), e.projectObjectsPath() + "/" + shot.Object, e.projectObjectsPath() + "/" + shot.Object + "/content"} {
		code, body := e.do(t, http.MethodGet, path, nil)
		if strings.Contains(string(body), "screen.png") || (code == http.StatusOK && !strings.Contains(path, "/object-") && strings.Contains(string(body), "object-1")) {
			t.Fatalf("GET %s (%d) shows an answer file: %s", path, code, body)
		}
		if strings.HasSuffix(path, shot.Object) || strings.HasSuffix(path, "content") {
			if code != http.StatusNotFound {
				t.Fatalf("GET %s code = %d, want 404", path, code)
			}
		}
	}

	// Refusals: not a multipart request, no file, a file that is too large,
	// a question that isn't there, a ref that isn't one.
	if code, raw := e.postRaw(t, e.authToken, http.MethodPost, apiPath(e.answerFilesPath(form.Ref, "shot")), strings.NewReader("plain"), "text/plain"); code != http.StatusBadRequest {
		t.Fatalf("not multipart code = %d body = %s", code, raw)
	}
	if code, raw := e.postFile(t, e.authToken, apiPath(e.answerFilesPath(form.Ref, "shot")), "big.png", "", bytes.Repeat([]byte("x"), 2048)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large code = %d body = %s", code, raw)
	}
	if code, raw := e.postFile(t, e.authToken, apiPath(e.answerFilesPath(form.Ref, "nope")), "a.png", "", formMediaPNG); code != http.StatusBadRequest || !strings.Contains(string(raw), "no question") {
		t.Fatalf("unknown question code = %d body = %s", code, raw)
	}
	if code, raw := e.do(t, http.MethodDelete, e.answerFilesPath(form.Ref, "shot")+"/banana", nil); code != http.StatusBadRequest {
		t.Fatalf("bad ref code = %d body = %s", code, raw)
	}
	if got := storedFiles(t, root); got != 3 {
		t.Fatalf("stored files = %d: a refused upload left bytes behind", got)
	}

	// Submitting works with a file; the submitted form refuses more.
	if code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formPath(form.Ref)+"/submit", nil); code != http.StatusOK {
		t.Fatalf("submit code = %d body = %s", code, raw)
	}
	if code, raw := e.postFile(t, e.authToken, apiPath(e.answerFilesPath(form.Ref, "shot")), "late.png", "", formMediaPNG); code != http.StatusConflict {
		t.Fatalf("attach to a submitted form code = %d body = %s", code, raw)
	}
	if code, raw := e.do(t, http.MethodDelete, e.answerFilesPath(form.Ref, "shot")+"/"+shot.Object, nil); code != http.StatusConflict {
		t.Fatalf("remove from a submitted form code = %d body = %s", code, raw)
	}
	// Reopening keeps the files.
	if code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formPath(form.Ref)+"/reopen", nil); code != http.StatusOK || len(decode[formJSON](t, raw).Responses[0].Files) != 1 {
		t.Fatalf("reopen code = %d body = %s", code, raw)
	}

	// Remove: the file is gone from the answer and the content route.
	if code, raw := e.do(t, http.MethodDelete, e.answerFilesPath(form.Ref, "shot")+"/"+shot.Object, nil); code != http.StatusNoContent {
		t.Fatalf("remove code = %d body = %s", code, raw)
	}
	if code, _ := e.do(t, http.MethodDelete, e.answerFilesPath(form.Ref, "shot")+"/"+shot.Object, nil); code != http.StatusNotFound {
		t.Fatalf("remove twice code = %d, want 404", code)
	}
	if code, _ := e.do(t, http.MethodGet, e.formContentPath(form.Ref, shot.Object), nil); code != http.StatusNotFound {
		t.Fatalf("content of a removed file code = %d, want 404", code)
	}
	if got := e.mustGetForm(t, e.authToken, form.Ref); got.Responses[0].Answered {
		t.Fatalf("the question is still answered with no file: %+v", got.Responses[0])
	}
	// The asker's media cannot be removed through an answer.
	if n := e.countRows(t, `SELECT count(*) FROM form_attachments WHERE answer_question_id IS NOT NULL`); n != 2 {
		t.Fatalf("answer files left = %d, want 2", n)
	}
}

func TestHTTPFormAnswerFilesLimits(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{FormMedia: server.FormMediaOptions{MaxFileBytes: 1024}})
	form := e.mustCreateAnswerFilesForm(t)
	for i := 0; i < model.FormMaxAnswerFiles; i++ {
		e.mustAttachAnswerFile(t, e.authToken, form.Ref, "shot", fmt.Sprintf("s%d.png", i), "", formMediaPNG)
	}
	code, raw := e.postFile(t, e.authToken, apiPath(e.answerFilesPath(form.Ref, "shot")), "s7.png", "", formMediaPNG)
	if code != http.StatusConflict || !strings.Contains(string(raw), "6 files") {
		t.Fatalf("seventh file code = %d body = %s", code, raw)
	}
	for i := 0; i < 14; i++ {
		question := "channel"
		switch {
		case i >= 2*model.FormMaxAnswerFiles:
			question = "extra"
		case i >= model.FormMaxAnswerFiles:
			question = "notes"
		}
		e.mustAttachAnswerFile(t, e.authToken, form.Ref, question, fmt.Sprintf("c%d.png", i), "", formMediaPNG)
	}
	code, raw = e.postFile(t, e.authToken, apiPath(e.answerFilesPath(form.Ref, "extra")), "n.png", "", formMediaPNG)
	if code != http.StatusConflict || !strings.Contains(string(raw), "20 attached files") {
		t.Fatalf("twenty-first file code = %d body = %s", code, raw)
	}
}

// answerFilesRoutes are the routes that attach and remove an answer file.
func (e *httpEnv) answerFilesRoutes(ref string) []struct {
	method string
	path   string
} {
	return []struct {
		method string
		path   string
	}{
		{http.MethodPost, e.answerFilesPath(ref, "shot")},
		{http.MethodDelete, e.answerFilesPath(ref, "shot") + "/object-1"},
	}
}

func TestHTTPFormAnswerFilesAreTheAssigneesAlone(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	form := e.mustCreateAnswerFilesForm(t)
	shot := e.mustAttachAnswerFile(t, e.authToken, form.Ref, "shot", "mine.png", "", formMediaPNG)
	_, memberToken := e.mustProjectMemberToken(t, "af-writer")
	readonly, readonlyToken := e.mustUserToken(t, "af-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "af-outsider")
	blocked, blockedToken := e.mustUserToken(t, "af-blocked")

	send := func(token, method, path string) (int, string) {
		t.Helper()
		if method == http.MethodPost {
			code, raw := e.postFile(t, token, apiPath(path), "x.png", "", formMediaPNG)
			return code, string(raw)
		}
		code, raw := e.doWithToken(t, token, method, path, nil)
		return code, string(raw)
	}
	removePath := e.answerFilesPath(form.Ref, "shot") + "/" + shot.Object

	// Another member, even one who can write, and a read-only member are
	// refused, and may read the file.
	for name, token := range map[string]string{"writer": memberToken, "readonly": readonlyToken} {
		for _, route := range []struct{ method, path string }{{http.MethodPost, e.answerFilesPath(form.Ref, "shot")}, {http.MethodDelete, removePath}} {
			if code, body := send(token, route.method, route.path); code != http.StatusForbidden || !strings.Contains(body, "only the form's assignee can answer it") {
				t.Fatalf("%s %s %s code = %d body = %s, want 403 (shot %+v)", name, route.method, route.path, code, body, shot)
			}
		}
		if res, _ := e.doRaw(t, token, http.MethodGet, e.formContentPath(form.Ref, shot.Object), nil, ""); res.StatusCode != http.StatusOK {
			t.Fatalf("%s content code = %d, want a member to read it", name, res.StatusCode)
		}
	}
	// Someone outside a private project, a blocked user and a signed-out caller.
	for name, token := range map[string]string{"outsider": outsiderToken} {
		for _, route := range e.answerFilesRoutes(form.Ref) {
			if code, body := send(token, route.method, route.path); code != http.StatusForbidden {
				t.Fatalf("%s %s %s code = %d body = %s, want 403", name, route.method, route.path, code, body)
			}
		}
	}
	for _, route := range e.answerFilesRoutes(form.Ref) {
		if code, body := send("", route.method, route.path); code != http.StatusUnauthorized {
			t.Fatalf("signed-out %s %s code = %d body = %s, want 401", route.method, route.path, code, body)
		}
	}
	// A public viewer is told nothing is there, on the content route as well.
	e.makeProjectPublic(t)
	for _, route := range e.answerFilesRoutes(form.Ref) {
		code, body := send(outsiderToken, route.method, route.path)
		if code != http.StatusNotFound || !strings.Contains(body, "not found") {
			t.Fatalf("public viewer %s %s code = %d body = %s, want 404", route.method, route.path, code, body)
		}
	}
	for _, path := range []string{e.formContentPath(form.Ref, shot.Object), e.formPath(form.Ref) + "/attachments/" + shot.Object + "/url"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/url") {
			method = http.MethodPost
		}
		if code, body := e.doWithToken(t, outsiderToken, method, path, nil); code != http.StatusNotFound {
			t.Fatalf("public viewer %s %s code = %d body = %s, want 404", method, path, code, body)
		}
	}
	if _, err := e.store.BlockProjectUser(e.ctx, e.projectID, blocked.ID, e.adminID); err != nil {
		t.Fatalf("BlockProjectUser: %v", err)
	}
	for _, route := range e.answerFilesRoutes(form.Ref) {
		if code, body := send(blockedToken, route.method, route.path); code != http.StatusForbidden {
			t.Fatalf("blocked %s %s code = %d body = %s, want 403", route.method, route.path, code, body)
		}
	}
	// A help-desk reporter has no project read at all.
	f := newHelpDeskFixture(t, e)
	for _, route := range e.answerFilesRoutes(form.Ref) {
		if code, body := send(f.aliceToken, route.method, route.path); code != http.StatusForbidden {
			t.Fatalf("reporter %s %s code = %d body = %s, want 403", route.method, route.path, code, body)
		}
	}
	if got := e.mustGetForm(t, e.authToken, form.Ref); len(got.Responses[0].Files) != 1 {
		t.Fatalf("the refused calls changed the answer: %+v", got.Responses[0])
	}
}

func TestConnectorTokenCannotAttachButCanMakeADownloadURL(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)
	tokens := e.exchange(t, e.approve(t, nil))
	connector, _ := tokens["access_token"].(string)

	// A file the asker put in a form, and one the person attached, made
	// through the store because this server has no object storage.
	upload, err := e.store.CreateFormUpload(e.ctx, store.CreateFormUploadParams{Object: store.CreateStorageObjectParams{
		ID: uuid.New(), ProjectID: e.projectID, Backend: "local", Bucket: "local", ObjectKey: "k/asked", Filename: "asked.png",
		ContentType: "image/png", ByteSize: 4, SHA256: strings.Repeat("a", 64), CreatedByID: e.user.ID,
	}})
	if err != nil {
		t.Fatalf("CreateFormUpload: %v", err)
	}
	created, err := e.store.CreateForm(e.ctx, store.CreateFormParams{
		ProjectID: e.projectID, Title: "Needs files", Definition: model.FormDefinition{Sections: []model.FormSection{{Questions: answerFilesQuestions()}}},
		AssigneeID: e.user.ID, CreatedByID: e.user.ID, AttachmentObjectNumbers: []int{upload.Number},
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	_, answer, err := e.store.AttachFormAnswerFile(e.ctx, store.AttachFormAnswerFileParams{
		FormID: created.ID, AssigneeID: e.user.ID, QuestionID: "shot", Object: store.CreateStorageObjectParams{
			ID: uuid.New(), ProjectID: e.projectID, Backend: "local", Bucket: "local", ObjectKey: "k/answer", Filename: "answer.png",
			ContentType: "image/png", ByteSize: 4, SHA256: strings.Repeat("b", 64), CreatedByID: e.user.ID,
		},
	})
	if err != nil {
		t.Fatalf("AttachFormAnswerFile: %v", err)
	}

	// Attaching or removing is answering: the connector is refused, before
	// anything is read.
	for _, route := range e.answerFilesRoutes(created.Ref) {
		code, body := e.postRaw(t, connector, route.method, apiPath(route.path), strings.NewReader("x"), "text/plain")
		if code != http.StatusForbidden || !strings.Contains(string(body), "connected applications cannot answer forms") {
			t.Fatalf("connector %s %s code = %d body = %s, want 403", route.method, route.path, code, body)
		}
	}
	if n := e.countRows(t, `SELECT count(*) FROM form_attachments WHERE form_id = $1`, created.ID); n != 2 {
		t.Fatalf("attachments = %d, want the two there before", n)
	}
	// Reading is allowed: it can make a download URL for either file.
	for _, ref := range []string{upload.Ref, answer.Object.Ref} {
		code, body := e.doWithToken(t, connector, http.MethodPost, e.formPath(created.Ref)+"/attachments/"+ref+"/url", nil)
		got := decode[map[string]any](t, body)
		if code != http.StatusCreated || !strings.HasPrefix(got["download_url"].(string), "/api/v1/form-files/") {
			t.Fatalf("connector download URL for %s code = %d body = %s", ref, code, body)
		}
	}
}

func answerFilesQuestions() []model.FormQuestion {
	return []model.FormQuestion{{ID: "shot", Type: model.FormQuestionFile, Label: "Attach a screenshot"}}
}

type fileURLJSON struct {
	DownloadURL string    `json:"download_url"`
	ExpiresAt   time.Time `json:"expires_at"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	ByteSize    int64     `json:"byte_size"`
}

func (e *httpEnv) mustFileURL(t *testing.T, token, ref, object string) fileURLJSON {
	t.Helper()
	code, raw := e.doWithToken(t, token, http.MethodPost, e.formPath(ref)+"/attachments/"+object+"/url", nil)
	if code != http.StatusCreated {
		t.Fatalf("download URL for %s code = %d body = %s", object, code, raw)
	}
	return decode[fileURLJSON](t, raw)
}

func TestFormFileDownloadURL(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{FormMedia: server.FormMediaOptions{FileURLTTL: 7 * time.Minute}})
	// A form with a file of the asker's and one the person attached.
	upload := e.mustUploadURL(t, e.authToken).UploadURL
	asked := e.mustUploadFile(t, upload, "asked.png", "", formMediaPNG)
	code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formsPath(), map[string]any{"title": "Needs files", "sections": answerFilesSections(), "attachments": []string{asked.Object}})
	if code != http.StatusCreated {
		t.Fatalf("create form code = %d body = %s", code, raw)
	}
	form := decode[formJSON](t, raw)
	shot := e.mustAttachAnswerFile(t, e.authToken, form.Ref, "shot", "answer.png", "", formMediaPNG)
	_, memberToken := e.mustProjectMemberToken(t, "dl-member")

	before := time.Now()
	for name, object := range map[string]string{"answer file": shot.Object, "asker's file": asked.Object} {
		created := e.mustFileURL(t, memberToken, form.Ref, object)
		if !strings.HasPrefix(created.DownloadURL, "/api/v1/form-files/") || len(created.DownloadURL) < len("/api/v1/form-files/")+26 {
			t.Fatalf("%s: download_url = %q", name, created.DownloadURL)
		}
		if created.ExpiresAt.Before(before.Add(6*time.Minute)) || created.ExpiresAt.After(time.Now().Add(8*time.Minute)) {
			t.Fatalf("%s: expires_at = %v, want about 7 minutes from now", name, created.ExpiresAt)
		}
		if created.Filename == "" || created.ContentType != "image/png" || created.ByteSize != int64(len(formMediaPNG)) {
			t.Fatalf("%s: %+v", name, created)
		}
		// No credentials at all: the bytes come back, to a GET only.
		res, body := e.doRaw(t, "", http.MethodGet, strings.TrimPrefix(created.DownloadURL, "/api/v1"), nil, "")
		sum := sha256.Sum256(formMediaPNG)
		if res.StatusCode != http.StatusOK || !bytes.Equal(body, formMediaPNG) || res.Header.Get("Content-Type") != "image/png" ||
			res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("ETag") != `"`+hex.EncodeToString(sum[:])+`"` {
			// the ETag is the stored hash, which the storage service computed
			if res.StatusCode != http.StatusOK || !bytes.Equal(body, formMediaPNG) || res.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("%s: GET code = %d type = %q cache = %q", name, res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Cache-Control"))
			}
		}
		if !strings.Contains(strings.Join(res.Header.Values("Content-Security-Policy"), ";"), "sandbox") {
			t.Fatalf("%s: the file was served without its protective headers", name)
		}
		// A range works, as on the content route.
		req, _ := http.NewRequestWithContext(e.ctx, http.MethodGet, e.ts.URL+created.DownloadURL, nil)
		req.Header.Set("Range", "bytes=0-3")
		ranged, err := e.ts.Client().Do(req)
		if err != nil || ranged.StatusCode != http.StatusPartialContent {
			t.Fatalf("%s: range request = %v, %v", name, ranged, err)
		}
		ranged.Body.Close()
		// The key is no credential anywhere else.
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			if code, raw := e.postRaw(t, "", method, created.DownloadURL, nil, ""); code == http.StatusOK || code == http.StatusCreated || code == http.StatusNoContent {
				t.Fatalf("%s: %s on a download URL code = %d body = %s", name, method, code, raw)
			}
		}
		if code, raw := e.postRaw(t, "", http.MethodGet, apiPath(e.formPath(form.Ref))+"?key="+strings.TrimPrefix(created.DownloadURL, "/api/v1/form-files/"), nil, ""); code != http.StatusUnauthorized {
			t.Fatalf("%s: the key worked as a credential on the form route: code = %d body = %s", name, code, raw)
		}
		// Whoever asks gets a URL of their own.
		if other := e.mustFileURL(t, memberToken, form.Ref, object); other.DownloadURL == created.DownloadURL {
			t.Fatalf("%s: two URLs are the same", name)
		}
	}

	// The key is shown once: not in the form, the list, or any later response.
	created := e.mustFileURL(t, e.authToken, form.Ref, shot.Object)
	key := strings.TrimPrefix(created.DownloadURL, "/api/v1/form-files/")
	for _, path := range []string{e.formPath(form.Ref), e.formsPath(), e.projectObjectsPath()} {
		if code, raw := e.do(t, http.MethodGet, path, nil); strings.Contains(string(raw), key) || strings.Contains(string(raw), "form-files") {
			t.Fatalf("GET %s (%d) shows the download key: %s", path, code, raw)
		}
	}
	if n := e.countRows(t, `SELECT count(*) FROM form_file_grants WHERE key_hash = $1`, sha256Of(key)); n != 1 {
		t.Fatalf("the grant is stored by the hash of its key: %d rows", n)
	}
	if n := e.countRows(t, `SELECT count(*) FROM form_file_grants WHERE encode(key_hash, 'escape') = $1`, key); n != 0 {
		t.Fatal("the key itself is stored")
	}

	// Unknown, expired and unusable keys are answered as any missing route.
	missingCode, missingBody := e.postRaw(t, "", http.MethodGet, "/api/v1/form-waits/NOBODYWASGIVENTHISKEY00000", nil, "")
	if _, err := e.pool.Exec(e.ctx, `UPDATE form_file_grants SET expires_at = now() - interval '1 second' WHERE key_hash = $1`, sha256Of(key)); err != nil {
		t.Fatalf("expire the grant: %v", err)
	}
	for name, path := range map[string]string{"unknown": "/api/v1/form-files/NOBODYWASGIVENTHISKEY00000", "expired": created.DownloadURL} {
		code, body := e.postRaw(t, "", http.MethodGet, path, nil, "")
		if code != http.StatusNotFound || missingCode != http.StatusNotFound || string(body) != string(missingBody) {
			t.Fatalf("%s: code = %d body = %s, want the body of a missing route (%d %s)", name, code, body, missingCode, missingBody)
		}
	}

	// A URL stops working when its file is removed, and when its creator can
	// no longer read the form.
	fresh := e.mustFileURL(t, memberToken, form.Ref, shot.Object)
	if code, _ := e.postRaw(t, "", http.MethodGet, fresh.DownloadURL, nil, ""); code != http.StatusOK {
		t.Fatalf("fresh URL code = %d", code)
	}
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM project_members WHERE project_id = $1 AND user_id <> $2`, e.projectID, e.adminID); err != nil {
		t.Fatalf("remove the members: %v", err)
	}
	if code, body := e.postRaw(t, "", http.MethodGet, fresh.DownloadURL, nil, ""); code != http.StatusNotFound || string(body) != string(missingBody) {
		t.Fatalf("URL of a removed member code = %d body = %s", code, body)
	}
	owned := e.mustFileURL(t, e.authToken, form.Ref, shot.Object)
	if code, raw := e.do(t, http.MethodDelete, e.answerFilesPath(form.Ref, "shot")+"/"+shot.Object, nil); code != http.StatusNoContent {
		t.Fatalf("remove code = %d body = %s", code, raw)
	}
	if code, _ := e.postRaw(t, "", http.MethodGet, owned.DownloadURL, nil, ""); code != http.StatusNotFound {
		t.Fatalf("URL of a removed file code = %d, want 404", code)
	}
	if code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formPath(form.Ref)+"/attachments/"+shot.Object+"/url", nil); code != http.StatusNotFound {
		t.Fatalf("download URL for a removed file code = %d body = %s", code, raw)
	}
	if code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formPath(form.Ref)+"/attachments/banana/url", nil); code != http.StatusBadRequest {
		t.Fatalf("download URL for a bad ref code = %d body = %s", code, raw)
	}
	// And when the project is deleted.
	last := e.mustFileURL(t, e.authToken, form.Ref, asked.Object)
	if err := e.store.DeleteProject(e.ctx, e.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if code, _ := e.postRaw(t, "", http.MethodGet, last.DownloadURL, nil, ""); code != http.StatusNotFound {
		t.Fatalf("URL in a deleted project code = %d, want 404", code)
	}
}

func sha256Of(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// multipartFiles builds an upload of several files in the one `file` field,
// as a browser sends them from a picker that takes many.
func multipartFiles(t *testing.T, step string, files ...struct {
	name    string
	content []byte
}) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if step != "" {
		if err := writer.WriteField("step", step); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	for _, f := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, f.name))
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatalf("CreatePart: %v", err)
		}
		_, _ = part.Write(f.content)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &buf, writer.FormDataContentType()
}

type uploadFile = struct {
	name    string
	content []byte
}

func (e *httpEnv) uiAttach(t *testing.T, path, token string, plain bool, files ...uploadFile) (*http.Response, string) {
	t.Helper()
	body, contentType := multipartFiles(t, "", files...)
	headers := map[string]string{"Content-Type": contentType}
	if !plain {
		headers["HX-Request"] = "true"
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, path, token, body, headers)
	defer res.Body.Close()
	return res, readBody(t, res)
}

func TestUIFormAnswerFiles(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{FormMedia: server.FormMediaOptions{MaxFileBytes: 1024}})
	form := e.mustCreateAnswerFilesForm(t)
	path := e.projectPath() + "/forms/" + form.Ref
	files := path + "/answers/shot/files"
	choiceFiles := path + "/answers/channel/files"

	// The page: the disclosure says note or file, the picker is its own form,
	// and the file question shows its picker in the question.
	_, body := e.uiFormGetDoc(t, path, e.authToken)
	requireContains(t, "page", body,
		"Add a note or file", ">Add a note<", `data-form-answer-files="shot"`, `data-form-answer-files="channel"`,
		`id="form-files-shot"`, `<input type="file" name="file" multiple class="sr-only">`, `hx-encoding="multipart/form-data"`,
		`hx-post="`+files+`"`, `hx-post="`+choiceFiles+`"`, "Attach a file or photo", `id="qf-shot"`, `form="qf-channel"`,
		`hx-include="#qf-channel"`, "0 of 1 answered",
	)
	requireOmits(t, "page", body, "accept=", "<script>", ` style=`, "Remove")
	// The picker's form is not inside the question's autosave form.
	card := body[strings.Index(body, `id="q-channel"`):]
	card = card[:strings.Index(card, `id="q-notes"`)]
	autosave := card[strings.Index(card, `<form id="qf-channel"`):]
	autosave = autosave[:strings.Index(autosave, "</form>")]
	if strings.Contains(autosave, "data-form-file-upload") || strings.Contains(autosave, `type="file"`) {
		t.Fatalf("the picker is inside the autosave form: %s", autosave)
	}
	if got := strings.Count(body, `type="file"`); got != 4 {
		t.Fatalf("pickers = %d, want one per question", got)
	}

	// Pick two files at once on the file question, as htmx posts them.
	res, out := e.uiAttach(t, files, e.authToken, false, uploadFile{"one.png", formMediaPNG}, uploadFile{"two.m4a", []byte("0123456789")})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("attach code = %d body = %s", res.StatusCode, out)
	}
	requireContains(t, "attach response", out,
		`id="form-footer"`, "1 of 1 answered", `id="form-files-shot" data-form-answer-files="shot" hx-swap-oob="true"`,
		`id="form-question-error-shot"`, `alt="one.png"`, "one.png", `<audio controls`, `data-form-file-remove`, "Attach another file",
		files+"/object-1/remove", files+"/object-2/remove",
	)
	requireOmits(t, "attach response", out, "<html", "Choose a file")
	got := e.mustGetForm(t, e.authToken, form.Ref)
	if !got.Responses[0].Answered || len(got.Responses[0].Files) != 2 {
		t.Fatalf("answer after the upload = %+v", got.Responses[0])
	}

	// A file on the choice question does not change the choice and does not
	// answer it, and saving the choice leaves the files.
	if code, raw := e.doWithToken(t, e.authToken, http.MethodPut, e.formPath(form.Ref)+"/answers", map[string]any{"answers": map[string]any{"channel": map[string]any{"selected": []string{"b"}}}}); code != http.StatusOK {
		t.Fatalf("select b code = %d body = %s", code, raw)
	}
	_, out = e.uiAttach(t, choiceFiles, e.authToken, false, uploadFile{"extra.png", formMediaPNG})
	requireContains(t, "choice attach", out, `data-form-answer-files="channel"`, `alt="extra.png"`)
	got = e.mustGetForm(t, e.authToken, form.Ref)
	if len(got.Answers["channel"].Selected) != 1 || got.Answers["channel"].Selected[0] != "b" || len(got.Responses[1].Files) != 1 {
		t.Fatalf("answers after the choice file = %+v", got.Answers)
	}
	res, out = e.uiFormPost(t, path+"/answers/channel", e.authToken, url.Values{"selected": {"a"}, "note": {"hi"}}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("save choice code = %d", res.StatusCode)
	}
	got = e.mustGetForm(t, e.authToken, form.Ref)
	if got.Answers["channel"].Selected[0] != "a" || len(got.Responses[1].Files) != 1 {
		t.Fatalf("saving the choice lost the file: %+v", got.Answers)
	}
	// The page lists the files, open when the choice has some.
	_, body = e.uiFormGetDoc(t, path, e.authToken)
	requireContains(t, "page with files", body, `<details class="mt-1" data-form-extras open>`, `alt="extra.png"`, `alt="one.png"`, "1 of 1 answered", "Remove")

	// Errors go in the question's slot, and the list still comes back.
	_, out = e.uiAttach(t, files, e.authToken, false, uploadFile{"big.png", bytes.Repeat([]byte("x"), 2048)})
	requireContains(t, "too large", out, "That file is too large: the most is 1.0 KB.", `data-form-answer-files="shot"`, `alt="one.png"`)
	_, out = e.uiAttach(t, files, e.authToken, false)
	requireContains(t, "no file", out, "Choose a file to attach.")
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, files, e.authToken, strings.NewReader("a=b"), map[string]string{"HX-Request": "true"})
	out = readBody(t, res)
	res.Body.Close()
	requireContains(t, "not multipart", out, "Multipart file required.")
	for i := 0; i < 4; i++ {
		e.uiAttach(t, files, e.authToken, false, uploadFile{fmt.Sprintf("more%d.png", i), formMediaPNG})
	}
	_, out = e.uiAttach(t, files, e.authToken, false, uploadFile{"seventh.png", formMediaPNG})
	requireContains(t, "full answer", out, "You can attach 6 files to an answer.", "This answer has all the files it can take.")
	requireOmits(t, "full answer", out, `type="file"`)
	// Several files at once stop at the answer's limit and say so.
	_, out = e.uiAttach(t, choiceFiles, e.authToken, false, uploadFile{"c2.png", formMediaPNG}, uploadFile{"c3.png", formMediaPNG}, uploadFile{"c4.png", formMediaPNG}, uploadFile{"c5.png", formMediaPNG}, uploadFile{"c6.png", formMediaPNG}, uploadFile{"c7.png", formMediaPNG})
	requireContains(t, "stops at the limit", out, "You can attach 6 files to an answer.", `alt="c5.png"`)
	if got := e.mustGetForm(t, e.authToken, form.Ref); len(got.Responses[1].Files) != 6 {
		t.Fatalf("choice files = %d, want 6", len(got.Responses[1].Files))
	}
	_, out = e.uiAttach(t, e.projectPath()+"/forms/"+form.Ref+"/answers/notes/files", e.authToken, false, uploadFile{"n.png", formMediaPNG})
	requireContains(t, "notes", out, `data-form-answer-files="notes"`)

	// Remove: the list and the count come back.
	res, out = e.uiFormPost(t, files+"/object-2/remove", e.authToken, nil, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("remove code = %d body = %s", res.StatusCode, out)
	}
	requireContains(t, "remove response", out, "1 of 1 answered", `data-form-answer-files="shot"`)
	requireOmits(t, "remove response", out, ">two.m4a<")
	res, out = e.uiFormPost(t, files+"/object-1/remove", e.authToken, nil, false)
	requireContains(t, "remove the last", out, "1 of 1 answered")
	// Removing again is harmless, and a file of another question is not removable here.
	res, out = e.uiFormPost(t, files+"/object-1/remove", e.authToken, nil, false)
	if res.StatusCode != http.StatusOK || strings.Contains(out, "could not") {
		t.Fatalf("remove twice code = %d body = %s", res.StatusCode, out)
	}
	if res, _ = e.uiFormPost(t, files+"/object-3/remove", e.authToken, nil, false); res.StatusCode != http.StatusOK {
		t.Fatalf("remove of another question's file code = %d", res.StatusCode)
	}
	if got := e.mustGetForm(t, e.authToken, form.Ref); len(got.Responses[1].Files) != 6 {
		t.Fatalf("a file of another question was removed: %d left", len(got.Responses[1].Files))
	}
	for _, bad := range []string{path + "/answers/nope/files", path + "/answers/shot/files/banana/remove", path + "/answers/nope/files/object-1/remove"} {
		if res, _ := e.uiFormPost(t, bad, e.authToken, nil, false); res.StatusCode != http.StatusNotFound {
			t.Fatalf("POST %s code = %d, want 404", bad, res.StatusCode)
		}
	}
	// A plain post goes back to the page.
	res, _ = e.uiAttach(t, files, e.authToken, true, uploadFile{"plain.png", formMediaPNG})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != path {
		t.Fatalf("plain attach code = %d location = %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ = e.uiFormPost(t, files+"/object-"+fmt.Sprint(e.countRows(t, `SELECT max(number) FROM storage_objects WHERE project_id = $1`, e.projectID))+"/remove", e.authToken, nil, true); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("plain remove code = %d", res.StatusCode)
	}

	// Review and submitted views show the files.
	_, body = e.uiFormGetDoc(t, path, e.authToken)
	requireContains(t, "page", body, "Remove")
	if res, _ := e.uiFormPost(t, path+"/submit", e.authToken, nil, false); res.StatusCode != http.StatusOK {
		t.Fatalf("submit code = %d", res.StatusCode)
	}
	// A submitted form takes no more files, and says so in the slot.
	_, out = e.uiAttach(t, files, e.authToken, false, uploadFile{"late.png", formMediaPNG})
	requireContains(t, "submitted", out, "This form is no longer open, so its answers can")
	_, out = e.uiFormPost(t, files+"/object-4/remove", e.authToken, nil, false)
	requireContains(t, "submitted remove", out, "This form is no longer open")
}

func TestUIFormAnswerFilesInReviewAndSubmittedViews(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formsPath(), map[string]any{
		"title": "Stepped",
		"sections": []map[string]any{
			{"title": "Proof", "questions": []map[string]any{
				{"id": "shot", "type": "file", "label": "Attach a screenshot"},
				{"id": "voice", "type": "file", "label": "A voice note", "required": false},
			}},
			{"title": "Wrap up", "questions": []map[string]any{{"id": "notes", "type": "text", "label": "Notes", "required": false}}},
		},
	})
	if code != http.StatusCreated {
		t.Fatalf("create form code = %d body = %s", code, raw)
	}
	form := decode[formJSON](t, raw)
	path := e.projectPath() + "/forms/" + form.Ref
	_, memberToken := e.mustProjectMemberToken(t, "ui-af-member")
	outsider, outsiderToken := e.mustUserToken(t, "ui-af-outsider")
	_ = outsider

	// Done with no file is refused, and the question is marked.
	res, body := e.uiFormPost(t, path+"/submit", e.authToken, nil, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("submit code = %d", res.StatusCode)
	}
	requireContains(t, "refused submit", body, "1 question still needs an answer.", "data-form-missing", "Needs an answer")

	// Another member can't attach or remove, and an outsider is told nothing.
	for _, route := range []string{path + "/answers/shot/files", path + "/answers/shot/files/object-1/remove"} {
		res, _ := e.uiAttach(t, route, memberToken, false, uploadFile{"x.png", formMediaPNG})
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("member POST %s code = %d, want 403", route, res.StatusCode)
		}
		res, _ = e.uiAttach(t, route, outsiderToken, false, uploadFile{"x.png", formMediaPNG})
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("outsider POST %s code = %d, want 404", route, res.StatusCode)
		}
	}
	if n := e.countRows(t, `SELECT count(*) FROM form_attachments`); n != 0 {
		t.Fatalf("refused attaches left %d attachments", n)
	}

	_, out := e.uiAttach(t, path+"/answers/shot/files", e.authToken, false, uploadFile{"proof.png", formMediaPNG})
	requireContains(t, "attach", out, "1 of 1 answered")
	e.uiAttach(t, path+"/answers/voice/files", e.authToken, false, uploadFile{"note.m4a", []byte("0123456789")})
	e.uiAttach(t, path+"/answers/voice/files", e.authToken, false, uploadFile{"agenda.txt", formMediaText})

	// The review step shows each answer's files, read-only.
	_, body = e.uiFormGetDoc(t, path+"?step=review", e.authToken)
	requireContains(t, "review", body, `data-form-review-section`, "1 file attached", "2 files attached", `data-form-answer-files="shot"`, `alt="proof.png"`,
		`<audio controls`, ">agenda.txt<", `data-form-media-file`)
	requireOmits(t, "review", body, "data-form-file-remove", `type="file"`, "Needs an answer")

	// Another member sees the open form's answers so far, with the files.
	_, body = e.uiFormGetDoc(t, path, memberToken)
	requireContains(t, "member view", body, "data-form-waiting", `data-form-answer-files="shot"`, `alt="proof.png"`, "Attach a screenshot")
	requireOmits(t, "member view", body, "data-form-file-remove", `type="file"`)
	// An outsider sees nothing of it.
	if res, body := e.uiFormGetDoc(t, path, outsiderToken); res.StatusCode != http.StatusNotFound || strings.Contains(body, "proof.png") {
		t.Fatalf("outsider code = %d", res.StatusCode)
	}
	// The files are served through the form's page route to members only.
	content := path + "/attachments/object-1/content"
	if res, body := e.uiFormGetDoc(t, content, memberToken); res.StatusCode != http.StatusOK || body != string(formMediaPNG) {
		t.Fatalf("member content code = %d", res.StatusCode)
	}
	if res, _ := e.uiFormGetDoc(t, content, outsiderToken); res.StatusCode != http.StatusNotFound {
		t.Fatalf("outsider content code = %d, want 404", res.StatusCode)
	}
	e.makeProjectPublic(t)
	if res, _ := e.uiFormGetDoc(t, content, outsiderToken); res.StatusCode != http.StatusNotFound {
		t.Fatalf("public viewer content code = %d, want 404", res.StatusCode)
	}
	// The project's object page never lists them.
	if code, body := e.do(t, http.MethodGet, e.projectObjectsPath(), nil); code != http.StatusOK || strings.Contains(string(body), "proof.png") {
		t.Fatalf("objects list code = %d shows an answer file: %s", code, body)
	}

	// Submitted: the files show in the summary, and the copy text names them.
	if res, _ := e.uiFormPost(t, path+"/submit", e.authToken, nil, false); res.StatusCode != http.StatusOK {
		t.Fatalf("submit code = %d", res.StatusCode)
	}
	_, body = e.uiFormGetDoc(t, path, e.authToken)
	requireContains(t, "submitted view", body, "Submitted", "1 file attached", `data-form-answer-files="shot"`, `alt="proof.png"`, `<audio controls`,
		"Files: proof.png", "Files: note.m4a, agenda.txt")
	requireOmits(t, "submitted view", body, "data-form-file-remove", `type="file"`)
	// Editing again brings the picker and Remove back, with the files kept.
	if res, _ := e.uiFormPost(t, path+"/reopen", e.authToken, nil, false); res.StatusCode != http.StatusOK {
		t.Fatalf("reopen code = %d", res.StatusCode)
	}
	_, body = e.uiFormGetDoc(t, path+"?step=1", e.authToken)
	requireContains(t, "reopened", body, "data-form-file-remove", `alt="proof.png"`)
}

func TestMCPFormFilesAndDownloadURL(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	session := mcpConnect(t, e, e.authToken)
	created := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_create_form", mcpFormArgs(e, map[string]any{
		"title": "Needs files", "sections": []any{answerFilesSections()[0]},
	})), "form")
	shot := e.mustAttachAnswerFile(t, e.authToken, created.Ref, "shot", "proof.png", "", formMediaPNG)

	got := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_get_form", mcpFormArgs(e, map[string]any{"form": created.Ref})), "form")
	if !got.Responses[0].Answered || len(got.Responses[0].Files) != 1 || got.Responses[0].Files[0].Object != shot.Object || got.Responses[0].Files[0].Kind != "image" || got.Responses[0].Files[0].URL == "" {
		t.Fatalf("responses[0] = %+v", got.Responses[0])
	}
	if len(got.Attachments) != 0 || len(got.Responses[1].Files) != 0 || got.Responses[1].Files == nil {
		t.Fatalf("attachments = %+v, second response files = %+v", got.Attachments, got.Responses[1].Files)
	}

	out := mcpCall(t, e, session, "track_create_form_file_url", mcpFormArgs(e, map[string]any{"form": created.Ref, "object": shot.Object}))
	var url string
	if err := json.Unmarshal(out["download_url"], &url); err != nil || !strings.HasPrefix(url, "/api/v1/form-files/") {
		t.Fatalf("download_url = %s, %v", out["download_url"], err)
	}
	for _, field := range []string{"expires_at", "filename", "content_type", "byte_size"} {
		if _, ok := out[field]; !ok {
			t.Fatalf("result has no %s: %v", field, out)
		}
	}
	if res, body := e.doRaw(t, "", http.MethodGet, strings.TrimPrefix(url, "/api/v1"), nil, ""); res.StatusCode != http.StatusOK || !bytes.Equal(body, formMediaPNG) {
		t.Fatalf("fetching the URL code = %d", res.StatusCode)
	}
	// Bad input, an unknown form, a file the form doesn't carry.
	mcpCallExpectError(t, e, session, "track_create_form_file_url", mcpFormArgs(e, map[string]any{"form": created.Ref, "object": "banana"}))
	mcpCallExpectError(t, e, session, "track_create_form_file_url", mcpFormArgs(e, map[string]any{"form": created.Ref, "object": "object-99"}))
	mcpCallExpectError(t, e, session, "track_create_form_file_url", mcpFormArgs(e, map[string]any{"form": "form-99", "object": shot.Object}))

	// A reader who is not a member is told nothing is there.
	_, outsiderToken := e.mustUserToken(t, "mcp-af-outsider")
	e.makeProjectPublic(t)
	outsider := mcpConnect(t, e, outsiderToken)
	mcpCallExpectError(t, e, outsider, "track_create_form_file_url", mcpFormArgs(e, map[string]any{"form": created.Ref, "object": shot.Object}))

	// The tool descriptions teach the new convention.
	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "track_create_form":
			if !strings.Contains(tool.Description, "file (answered by attaching") {
				t.Fatalf("track_create_form does not name the file question type: %s", tool.Description)
			}
		case "track_create_form_file_url":
			for _, want := range []string{"curl -sS -o FILE", "10 minutes", "needs no credentials"} {
				if !strings.Contains(tool.Description, want) {
					t.Fatalf("track_create_form_file_url description misses %q: %s", want, tool.Description)
				}
			}
		case "track_get_form":
			if !strings.Contains(tool.Description, "track_create_form_file_url") {
				t.Fatalf("track_get_form does not mention the files: %s", tool.Description)
			}
		}
	}
}

func TestFormAnswerFilesNeedObjectStorage(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t) // no object storage
	form := e.mustCreateAnswerFilesForm(t)
	if code, raw := e.postFile(t, e.authToken, apiPath(e.answerFilesPath(form.Ref, "shot")), "a.png", "", formMediaPNG); code != http.StatusServiceUnavailable {
		t.Fatalf("REST attach code = %d body = %s, want 503", code, raw)
	}
	path := e.projectPath() + "/forms/" + form.Ref + "/answers/shot/files"
	_, out := e.uiAttach(t, path, e.authToken, false, uploadFile{"a.png", formMediaPNG})
	requireContains(t, "UI attach", out, "File uploads are not available.")
}
