package server_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
)

// formAttachmentJSON is a file a form carries, as the API returns it.
type formAttachmentJSON struct {
	Object      string `json:"object"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
}

type uploadURLJSON struct {
	UploadURL    string    `json:"upload_url"`
	ExpiresAt    time.Time `json:"expires_at"`
	MaxFiles     int       `json:"max_files"`
	MaxFileBytes int64     `json:"max_file_bytes"`
}

type uploadedJSON struct {
	Object      string `json:"object"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
}

var (
	formMediaPNG  = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("pixels")...)
	formMediaMP3  = []byte("ID3 not really audio")
	formMediaMP4  = []byte("not really a video")
	formMediaText = []byte("plain notes")
)

// formMediaEnv is a server with object storage, small limits and its bytes on
// disk where a test can count them.
func formMediaEnv(t *testing.T, options server.Options) (*httpEnv, string) {
	t.Helper()
	storageSvc, root := newLocalStorageService(t, 4096)
	options.ObjectStorage = storageSvc
	return newHTTPEnvWithOptions(t, options), root
}

// storedFiles counts the files in the storage root.
func storedFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return n
}

func (e *httpEnv) formUploadsPath() string {
	return e.formsPath() + "/upload-urls"
}

func (e *httpEnv) mustUploadURL(t *testing.T, token string) uploadURLJSON {
	t.Helper()
	code, raw := e.doWithToken(t, token, http.MethodPost, e.formUploadsPath(), nil)
	if code != http.StatusCreated {
		t.Fatalf("create upload URL code = %d body = %s", code, raw)
	}
	return decode[uploadURLJSON](t, raw)
}

// multipartBody builds a one-file upload. contentType is the part's own, or
// empty to send none.
func multipartBody(t *testing.T, filename, contentType string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &buf, writer.FormDataContentType()
}

// postFile uploads a file to a path on the server, as given (not under
// /api/v1), with a token when one is given.
func (e *httpEnv) postFile(t *testing.T, token, path, filename, contentType string, content []byte) (int, []byte) {
	t.Helper()
	body, formType := multipartBody(t, filename, contentType, content)
	return e.postRaw(t, token, http.MethodPost, path, body, formType)
}

func (e *httpEnv) postRaw(t *testing.T, token, method, path string, body io.Reader, contentType string) (int, []byte) {
	t.Helper()
	// An upload URL is absolute when the server knows its public origin, and
	// that origin is not the test server's.
	if strings.HasPrefix(path, "https://") {
		u, err := url.Parse(path)
		if err != nil {
			t.Fatalf("parse %q: %v", path, err)
		}
		path = u.RequestURI()
	}
	req, err := http.NewRequestWithContext(e.ctx, method, e.ts.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, raw
}

// mustUploadFile sends a file to an upload URL with no credentials at all.
func (e *httpEnv) mustUploadFile(t *testing.T, uploadURL, filename, contentType string, content []byte) uploadedJSON {
	t.Helper()
	code, raw := e.postFile(t, "", uploadURL, filename, contentType, content)
	if code != http.StatusCreated {
		t.Fatalf("upload %s code = %d body = %s", filename, code, raw)
	}
	return decode[uploadedJSON](t, raw)
}

func uploadKey(uploadURL string) string {
	return strings.TrimPrefix(uploadURL, "/api/v1/form-uploads/")
}

// grantWithKey stores an upload grant for a key a test chose, so it can make
// one that has already expired.
func (e *httpEnv) grantWithKey(t *testing.T, key string, creator uuid.UUID, expiresIn time.Duration, maxFiles int) {
	t.Helper()
	sum := sha256.Sum256([]byte(key))
	if _, err := e.store.CreateFormUploadGrant(e.ctx, store.CreateFormUploadGrantParams{
		ProjectID: e.projectID, CreatedByID: creator, KeyHash: sum[:], ExpiresAt: time.Now().Add(expiresIn), MaxFiles: maxFiles,
	}); err != nil {
		t.Fatalf("CreateFormUploadGrant: %v", err)
	}
}

func (e *httpEnv) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (e *httpEnv) pendingUploads(t *testing.T) int {
	return e.countRows(t, `SELECT count(*) FROM form_uploads WHERE project_id = $1`, e.projectID)
}

func (e *httpEnv) formContentPath(ref, object string) string {
	return e.formPath(ref) + "/attachments/" + object + "/content"
}

// mustCreateFormWithMedia creates a form whose first question shows media and
// whose first option shows optionMedia, with the given attachments.
func (e *httpEnv) createFormWithMedia(t *testing.T, token string, media []string, optionMedia string, attachments []string) (int, []byte) {
	t.Helper()
	sections := formTestSections()
	question := sections[0]["questions"].([]map[string]any)[0]
	if media != nil {
		question["media"] = media
	}
	if optionMedia != "" {
		question["options"].([]map[string]any)[0]["media"] = optionMedia
	}
	body := map[string]any{"title": "With files", "sections": sections}
	if attachments != nil {
		body["attachments"] = attachments
	}
	return e.doWithToken(t, token, http.MethodPost, e.formsPath(), body)
}

func TestFormUploadURLTakesFilesWithNoCredentials(t *testing.T) {
	t.Parallel()
	e, root := formMediaEnv(t, server.Options{FormMedia: server.FormMediaOptions{MaxFileBytes: 1024, GrantMaxFiles: 4, GrantTTL: 20 * time.Minute}})

	before := time.Now()
	created := e.mustUploadURL(t, e.authToken)
	if !strings.HasPrefix(created.UploadURL, "/api/v1/form-uploads/") || len(created.UploadURL) < len("/api/v1/form-uploads/")+26 {
		t.Fatalf("upload_url = %q", created.UploadURL)
	}
	if created.MaxFiles != 4 || created.MaxFileBytes != 1024 {
		t.Fatalf("limits = %+v", created)
	}
	if created.ExpiresAt.Before(before.Add(19*time.Minute)) || created.ExpiresAt.After(time.Now().Add(21*time.Minute)) {
		t.Fatalf("expires_at = %v, want about 20 minutes from now", created.ExpiresAt)
	}
	if other := e.mustUploadURL(t, e.authToken); other.UploadURL == created.UploadURL {
		t.Fatal("two upload URLs are the same")
	}

	// The URL is the only credential. One file per request, and the file is
	// the creator's pending upload.
	first := e.mustUploadFile(t, created.UploadURL, `C:\shots\main screen.png`, "", formMediaPNG)
	if first.Object != "object-1" || first.Filename != "main screen.png" || first.ContentType != "image/png" || first.ByteSize != int64(len(formMediaPNG)) {
		t.Fatalf("first upload = %+v", first)
	}
	second := e.mustUploadFile(t, created.UploadURL, "notes.txt", "text/plain; charset=utf-8", formMediaText)
	if second.Object != "object-2" || second.ContentType != "text/plain" {
		t.Fatalf("second upload = %+v", second)
	}
	if got := e.pendingUploads(t); got != 2 {
		t.Fatalf("pending uploads = %d, want 2", got)
	}
	var createdBy uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `SELECT created_by_id FROM form_uploads WHERE storage_object_id = (SELECT id FROM storage_objects WHERE number = 1 AND project_id = $1)`, e.projectID).Scan(&createdBy); err != nil || createdBy != e.adminID {
		t.Fatalf("upload created by %v, %v, want the URL's creator", createdBy, err)
	}
	if got := storedFiles(t, root); got != 2 {
		t.Fatalf("stored files = %d, want 2", got)
	}

	// The bytes of a form's file come back as uploaded once the form has it.
	code, raw := e.createFormWithMedia(t, e.authToken, []string{first.Object}, second.Object, nil)
	if code != http.StatusCreated {
		t.Fatalf("create form code = %d body = %s", code, raw)
	}
	form := decode[formJSON](t, raw)
	res, content := e.doRaw(t, e.authToken, http.MethodGet, e.formContentPath(form.Ref, first.Object), nil, "")
	if res.StatusCode != http.StatusOK || !bytes.Equal(content, formMediaPNG) || res.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("image content: code = %d type = %q disposition = %q", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"))
	}

	// The key never comes back: not in the upload's answer, the form, the
	// list, or any object route.
	key := uploadKey(created.UploadURL)
	for _, shown := range []string{
		string(raw), string(mustJSON(t, first)), string(mustJSON(t, second)),
	} {
		if strings.Contains(shown, key) {
			t.Fatalf("the upload key was shown: %s", shown)
		}
	}
	for _, path := range []string{e.formPath(form.Ref), e.formsPath(), e.projectObjectsPath()} {
		if code, raw := e.do(t, http.MethodGet, path, nil); strings.Contains(string(raw), key) || strings.Contains(string(raw), "form-uploads") {
			t.Fatalf("GET %s (%d) shows the upload key: %s", path, code, raw)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestFormUploadURLRefusesWhatItShould(t *testing.T) {
	t.Parallel()
	e, root := formMediaEnv(t, server.Options{FormMedia: server.FormMediaOptions{MaxFileBytes: 1024, GrantMaxFiles: 2}})
	created := e.mustUploadURL(t, e.authToken)
	key := uploadKey(created.UploadURL)

	// An unknown key, and one that expired, are answered exactly as a route
	// that doesn't exist. The key is checked before the body is read: these
	// requests aren't even uploads.
	// A wait URL with an unknown key is the other secret-bearing route's way
	// of saying not found.
	missingCode, missingBody := e.postRaw(t, "", http.MethodGet, "/api/v1/form-waits/NOBODYWASGIVENTHISKEY00000", nil, "")
	e.grantWithKey(t, "expired-key", e.adminID, -time.Minute, 3)
	for name, path := range map[string]string{
		"unknown": "/api/v1/form-uploads/NOBODYWASGIVENTHISKEY00000", "expired": "/api/v1/form-uploads/expired-key",
	} {
		code, body := e.postRaw(t, "", http.MethodPost, path, strings.NewReader("not multipart"), "text/plain")
		if code != http.StatusNotFound || missingCode != http.StatusNotFound || string(body) != string(missingBody) {
			t.Fatalf("%s: code = %d body = %s, want the body of a missing route (%d %s)", name, code, body, missingCode, missingBody)
		}
	}
	if code, raw := e.postFile(t, "", "/api/v1/form-uploads/expired-key", "a.png", "", formMediaPNG); code != http.StatusNotFound {
		t.Fatalf("upload with an expired key code = %d body = %s", code, raw)
	}

	// Only POST uploads. Another method falls through to the routes that want
	// an account, as it does on a wait URL, and the key is no credential there.
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if code, raw := e.postRaw(t, "", method, created.UploadURL, nil, ""); code != http.StatusUnauthorized {
			t.Fatalf("%s upload URL code = %d body = %s, want 401", method, code, raw)
		}
		if code, raw := e.postRaw(t, e.authToken, method, created.UploadURL, nil, ""); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Fatalf("%s upload URL with a token code = %d body = %s, want 404 or 405", method, code, raw)
		}
	}
	// Not multipart, and multipart with no file.
	if code, raw := e.postRaw(t, "", http.MethodPost, created.UploadURL, strings.NewReader(`{"file":"x"}`), "application/json"); code != http.StatusBadRequest || !strings.Contains(string(raw), "multipart file required") {
		t.Fatalf("json upload code = %d body = %s", code, raw)
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("title", "no file")
	_ = writer.Close()
	if code, raw := e.postRaw(t, "", http.MethodPost, created.UploadURL, &buf, writer.FormDataContentType()); code != http.StatusBadRequest || !strings.Contains(string(raw), "file required") {
		t.Fatalf("upload with no file code = %d body = %s", code, raw)
	}
	if code, raw := e.postRaw(t, "", http.MethodPost, created.UploadURL, strings.NewReader("--x\r\nbroken"), "multipart/form-data; boundary=x"); code != http.StatusBadRequest {
		t.Fatalf("broken multipart code = %d body = %s", code, raw)
	}
	if code, raw := e.postFile(t, "", created.UploadURL, "..", "", []byte("x")); code != http.StatusBadRequest || !strings.Contains(string(raw), "filename required") {
		t.Fatalf("upload with no usable filename code = %d body = %s", code, raw)
	}

	// Too large: over the form media limit, and over the storage limit even
	// when the setting allows more. Nothing is stored or counted.
	if code, raw := e.postFile(t, "", created.UploadURL, "big.bin", "", bytes.Repeat([]byte("a"), 1025)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("1025 byte upload code = %d body = %s, want 413", code, raw)
	}
	if code, raw := e.postFile(t, "", created.UploadURL, "huge.bin", "", bytes.Repeat([]byte("a"), 3*1024*1024)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("3 MiB upload code = %d body = %s, want 413", code, raw)
	}
	if got := storedFiles(t, root); got != 0 || e.pendingUploads(t) != 0 {
		t.Fatalf("refused uploads left %d files and %d pending uploads", got, e.pendingUploads(t))
	}
	// The limit itself is fine, and the refusals above took no place from the
	// URL: both of its places are still there.
	e.mustUploadFile(t, created.UploadURL, "edge.bin", "", bytes.Repeat([]byte("a"), 1024))
	e.mustUploadFile(t, created.UploadURL, "second.bin", "", []byte("b"))
	code, raw := e.postFile(t, "", created.UploadURL, "third.bin", "", []byte("c"))
	if code != http.StatusConflict || !strings.Contains(string(raw), "no files left") {
		t.Fatalf("third upload code = %d body = %s, want 409", code, raw)
	}
	// A URL with no files left is still the same URL: it doesn't read as missing.
	if strings.Contains(string(raw), key) {
		t.Fatalf("409 shows the key: %s", raw)
	}
	if got := storedFiles(t, root); got != 2 {
		t.Fatalf("stored files = %d, want 2", got)
	}
}

func TestFormUploadsRacingForTheLastPlaceLeaveOneFile(t *testing.T) {
	t.Parallel()
	e, root := formMediaEnv(t, server.Options{FormMedia: server.FormMediaOptions{GrantMaxFiles: 1}})
	created := e.mustUploadURL(t, e.authToken)

	const racers = 6
	codes := make([]int, racers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i], _ = e.postFile(t, "", created.UploadURL, fmt.Sprintf("race-%d.png", i), "", formMediaPNG)
		}()
	}
	close(start)
	wg.Wait()

	created201, conflicts := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created201++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("racer code = %d, want 201 or 409 (%v)", code, codes)
		}
	}
	if created201 != 1 || conflicts != racers-1 {
		t.Fatalf("codes = %v, want one 201 and the rest 409", codes)
	}
	// The losers' bytes were cleaned up, and they left no object behind.
	if got := storedFiles(t, root); got != 1 || e.pendingUploads(t) != 1 {
		t.Fatalf("stored files = %d, pending = %d, want 1 and 1", got, e.pendingUploads(t))
	}
	if n := e.countRows(t, `SELECT count(*) FROM storage_objects WHERE project_id = $1`, e.projectID); n != 1 {
		t.Fatalf("storage objects = %d, want 1", n)
	}
}

func TestFormUploadURLStopsWhenItsCreatorCannotWrite(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	member, memberToken := e.mustProjectMemberToken(t, "up-member")
	readOnly, readOnlyToken := e.mustProjectMemberToken(t, "up-readonly")
	blocked, blockedToken := e.mustProjectMemberToken(t, "up-blocked")
	removed, removedToken := e.mustProjectMemberToken(t, "up-removed")
	deleted, deletedToken := e.mustProjectMemberToken(t, "up-deleted")
	urls := map[string]uploadURLJSON{}
	for name, token := range map[string]string{"member": memberToken, "readonly": readOnlyToken, "blocked": blockedToken, "removed": removedToken, "deleted": deletedToken, "owner": e.authToken} {
		urls[name] = e.mustUploadURL(t, token)
	}
	try := func(name string) int {
		t.Helper()
		code, _ := e.postFile(t, "", urls[name].UploadURL, "a.png", "", formMediaPNG)
		return code
	}
	for name := range urls {
		if code := try(name); code != http.StatusCreated {
			t.Fatalf("%s: upload before anything changed code = %d", name, code)
		}
	}

	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readOnly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	if _, err := e.store.BlockProjectUser(e.ctx, e.projectID, blocked.ID, e.adminID); err != nil {
		t.Fatalf("BlockProjectUser: %v", err)
	}
	if err := e.store.RevokeProjectAccess(e.ctx, e.projectID, removed.ID); err != nil {
		t.Fatalf("RevokeProjectAccess: %v", err)
	}
	if err := e.store.DeleteUser(e.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	pending := e.pendingUploads(t)
	for _, name := range []string{"readonly", "blocked", "removed", "deleted"} {
		if code := try(name); code != http.StatusNotFound {
			t.Fatalf("%s: upload after losing write access code = %d, want 404", name, code)
		}
	}
	for _, name := range []string{"member", "owner"} {
		if code := try(name); code != http.StatusCreated {
			t.Fatalf("%s: upload stopped working for someone who can still write: %d", name, code)
		}
	}
	if got := e.pendingUploads(t); got != pending+2 {
		t.Fatalf("pending uploads = %d, want %d: a refused upload was stored", got, pending+2)
	}
	_ = member

	if err := e.store.DeleteProject(e.ctx, e.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	for _, name := range []string{"member", "owner"} {
		if code := try(name); code != http.StatusNotFound {
			t.Fatalf("%s: upload to a deleted project code = %d, want 404", name, code)
		}
	}
}

func TestFormUploadRoutesNeedWriteAccess(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	_, outsiderToken := e.mustUserToken(t, "up-outsider")
	_, memberToken := e.mustProjectMemberToken(t, "up-writer")
	readOnly, readOnlyToken := e.mustProjectMemberToken(t, "up-ro")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readOnly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	routes := []struct {
		name string
		do   func(token string) int
	}{
		{"create upload URL", func(token string) int {
			code, _ := e.doWithToken(t, token, http.MethodPost, e.formUploadsPath(), nil)
			return code
		}},
		{"upload with a token", func(token string) int {
			code, _ := e.postFile(t, token, apiPath(e.formsPath()+"/uploads"), "a.png", "", formMediaPNG)
			return code
		}},
	}

	for _, route := range routes {
		if code := route.do(memberToken); code != http.StatusCreated {
			t.Fatalf("%s as a member code = %d, want 201", route.name, code)
		}
		if code := route.do(e.authToken); code != http.StatusCreated {
			t.Fatalf("%s as the owner code = %d, want 201", route.name, code)
		}
		if code := route.do(readOnlyToken); code != http.StatusForbidden {
			t.Fatalf("%s as a read-only member code = %d, want 403", route.name, code)
		}
		if code := route.do(outsiderToken); code != http.StatusForbidden {
			t.Fatalf("%s as an outsider code = %d, want 403", route.name, code)
		}
		if code := route.do(""); code != http.StatusUnauthorized {
			t.Fatalf("%s signed out code = %d, want 401", route.name, code)
		}
	}

	// On a public project a reader who is not a member is told nothing is there.
	e.makeProjectPublic(t)
	for _, route := range routes {
		if code := route.do(outsiderToken); code != http.StatusNotFound {
			t.Fatalf("%s as a public viewer code = %d, want 404", route.name, code)
		}
	}
	// The token upload is the same pending upload an upload URL makes, with no
	// grant behind it. Only the two that worked (member, owner) were stored.
	if n := e.countRows(t, `SELECT count(*) FROM form_uploads WHERE grant_id IS NULL`); n != 2 {
		t.Fatalf("pending uploads without a grant = %d, want 2", n)
	}
}
