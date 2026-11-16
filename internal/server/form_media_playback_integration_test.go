package server_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
)

// getMedia reads a content route as the REST API (with a token) or as a
// signed-in browser (with a session cookie), with extra request headers.
func (e *httpEnv) getMedia(t *testing.T, ui bool, token, path string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	if ui {
		res := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, path, token, nil, headers)
		defer res.Body.Close()
		return res, []byte(readBody(t, res))
	}
	req, err := http.NewRequestWithContext(e.ctx, http.MethodGet, e.ts.URL+apiPath(path), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	return res, []byte(readBody(t, res))
}

func requireProtectiveHeaders(t *testing.T, what string, res *http.Response, content []byte) {
	t.Helper()
	sum := sha256.Sum256(content)
	if got := res.Header.Get("Etag"); got != `"`+hex.EncodeToString(sum[:])+`"` {
		t.Fatalf("%s: ETag = %q, want the SHA-256 of the bytes", what, got)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("%s: lost nosniff or CORP: %v", what, res.Header)
	}
	if !strings.Contains(strings.Join(res.Header.Values("Content-Security-Policy"), ";"), "default-src 'none'; sandbox") {
		t.Fatalf("%s: lost the sandbox policy: %v", what, res.Header.Values("Content-Security-Policy"))
	}
}

func TestFormMediaIsServedForPlaybackOnTheFormRoutes(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	_, outsiderToken := e.mustUserToken(t, "play-outsider")
	e.makeProjectPublic(t)

	clip := bytes.Repeat([]byte("0123456789"), 10)
	html := []byte("<html><script>alert(1)</script></html>")
	upload := e.mustUploadURL(t, e.authToken).UploadURL
	type file struct {
		name, declared string
		content        []byte
		wantType       string // as stored
		served         string // as served
		kind           string
	}
	files := []file{
		{"take.mp3", "audio/mpeg", clip, "audio/mpeg", "audio/mpeg", "audio"},
		// curl declares application/octet-stream: the extension decides.
		{"voice.m4a", "", clip, "audio/mp4", "audio/mp4", "audio"},
		{"clip.mp4", "application/octet-stream", clip, "video/mp4", "video/mp4", "video"},
		{"clip.mov", "", clip, "video/quicktime", "video/quicktime", "video"},
		{"clip.webm", "video/webm", clip, "video/webm", "video/webm", "video"},
		{"shot.png", "", formMediaPNG, "image/png", "image/png", "image"},
		// Everything else stays an opaque download, whoever declared what.
		{"page.html", "text/html", html, "text/html", "application/octet-stream", "file"},
		{"page2.html", "", html, "text/html", "application/octet-stream", "file"},
		{"fake.mp3", "text/html", html, "text/html", "application/octet-stream", "file"},
		{"logo.svg", "image/svg+xml", []byte("<svg onload=alert(1)/>"), "image/svg+xml", "application/octet-stream", "file"},
		{"app.js", "application/javascript", []byte("alert(1)"), "application/javascript", "application/octet-stream", "file"},
		{"song.flac", "audio/flac", clip, "audio/flac", "application/octet-stream", "file"},
		{"notes.txt", "text/plain", formMediaText, "text/plain", "application/octet-stream", "file"},
	}
	refs := make([]string, len(files))
	for i, f := range files {
		uploaded := e.mustUploadFile(t, upload, f.name, f.declared, f.content)
		if uploaded.ContentType != f.wantType {
			t.Fatalf("%s stored as %q, want %q", f.name, uploaded.ContentType, f.wantType)
		}
		refs[i] = uploaded.Object
	}
	code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formsPath(), map[string]any{"title": "Media", "sections": formTestSections(), "attachments": refs})
	if code != http.StatusCreated {
		t.Fatalf("create form code = %d body = %s", code, raw)
	}
	form := decode[formJSON](t, raw)
	for i, f := range files {
		if form.Attachments[i].Kind != f.kind {
			t.Fatalf("%s kind = %q, want %q", f.name, form.Attachments[i].Kind, f.kind)
		}
	}

	uiPath := func(object string) string {
		return e.projectPath() + "/forms/" + form.Ref + "/attachments/" + object + "/content"
	}
	for _, ui := range []bool{false, true} {
		for i, f := range files {
			path := e.formContentPath(form.Ref, refs[i])
			if ui {
				path = uiPath(refs[i])
			}
			what := f.name
			if ui {
				what += " (UI)"
			}
			res, body := e.getMedia(t, ui, e.authToken, path, nil)
			if res.StatusCode != http.StatusOK || !bytes.Equal(body, f.content) {
				t.Fatalf("%s: code = %d body = %q", what, res.StatusCode, body)
			}
			disposition := "attachment"
			if f.served != "application/octet-stream" {
				disposition = "inline"
			}
			if res.Header.Get("Content-Type") != f.served || !strings.HasPrefix(res.Header.Get("Content-Disposition"), disposition) || res.Header.Get("Accept-Ranges") != "bytes" {
				t.Fatalf("%s: type = %q disposition = %q ranges = %q, want %q %s bytes", what, res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"), res.Header.Get("Accept-Ranges"), f.served, disposition)
			}
			requireProtectiveHeaders(t, what, res, f.content)
		}
	}

	// Byte ranges, on audio, video and a download alike.
	for _, ui := range []bool{false, true} {
		for _, i := range []int{0, 2, 12} {
			path := e.formContentPath(form.Ref, refs[i])
			if ui {
				path = uiPath(refs[i])
			}
			content := files[i].content
			res, body := e.getMedia(t, ui, e.authToken, path, map[string]string{"Range": "bytes=0-9"})
			if res.StatusCode != http.StatusPartialContent || len(body) != 10 || !bytes.Equal(body, content[:10]) || res.Header.Get("Content-Range") != "bytes 0-9/"+strconv.Itoa(len(content)) {
				t.Fatalf("%s range: code = %d range = %q body = %q", files[i].name, res.StatusCode, res.Header.Get("Content-Range"), body)
			}
			if res.Header.Get("Content-Type") != files[i].served {
				t.Fatalf("%s range type = %q", files[i].name, res.Header.Get("Content-Type"))
			}
			requireProtectiveHeaders(t, files[i].name+" range", res, content)
			res, body = e.getMedia(t, ui, e.authToken, path, map[string]string{"Range": "bytes=5000-6000"})
			if res.StatusCode != http.StatusRequestedRangeNotSatisfiable || res.Header.Get("Content-Range") != "bytes */"+strconv.Itoa(len(content)) || bytes.Contains(body, content[:4]) {
				t.Fatalf("%s unsatisfiable: code = %d range = %q", files[i].name, res.StatusCode, res.Header.Get("Content-Range"))
			}
			sum := sha256.Sum256(content)
			quoted := `"` + hex.EncodeToString(sum[:]) + `"`
			if res, _ = e.getMedia(t, ui, e.authToken, path, map[string]string{"If-None-Match": quoted}); res.StatusCode != http.StatusNotModified {
				t.Fatalf("%s If-None-Match code = %d", files[i].name, res.StatusCode)
			}
			if res, body = e.getMedia(t, ui, e.authToken, path, map[string]string{"Range": "bytes=0-1", "If-Range": quoted}); res.StatusCode != http.StatusPartialContent || len(body) != 2 {
				t.Fatalf("%s If-Range code = %d", files[i].name, res.StatusCode)
			}
			if res, body = e.getMedia(t, ui, e.authToken, path, map[string]string{"Range": "bytes=0-1", "If-Range": `"other"`}); res.StatusCode != http.StatusOK || !bytes.Equal(body, content) {
				t.Fatalf("%s stale If-Range code = %d", files[i].name, res.StatusCode)
			}
		}
	}

	// Members only: a signed-in outsider of a public project, and a visitor
	// with no sign-in, are given nothing, and no byte of it.
	for _, ui := range []bool{false, true} {
		for _, token := range []string{outsiderToken, ""} {
			path := e.formContentPath(form.Ref, refs[0])
			if ui {
				path = uiPath(refs[0])
			}
			res, body := e.getMedia(t, ui, token, path, map[string]string{"Range": "bytes=0-9"})
			if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusPartialContent || bytes.Contains(body, clip[:10]) || res.Header.Get("Accept-Ranges") != "" || res.Header.Get("Content-Range") != "" {
				t.Fatalf("outsider (ui=%v token=%q) got code = %d headers = %v", ui, token, res.StatusCode, res.Header)
			}
		}
	}
}

// Only a form's own content routes answer byte ranges and play audio and
// video inline. Every other route that serves an upload goes on sending
// audio and video as opaque downloads, with no Accept-Ranges and no ranges.
func TestOtherContentRoutesAreUnchanged(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	issue := e.mustCreateIssue(t, "Ordinary issue")
	clip := append([]byte("ID3"), bytes.Repeat([]byte("0123456789"), 10)...) // sniffed as audio/mpeg
	sum := sha256.Sum256(clip)
	wantETag := `"` + hex.EncodeToString(sum[:]) + `"`

	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Audio sprint"})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	type route struct{ name, uploadPath, contentPrefix string }
	routes := []route{
		{"generic object", e.projectObjectsPath(), e.projectObjectsPath()},
		{"issue attachment", e.issueAttachmentsPath(issue), e.issueAttachmentsPath(issue)},
		{"project attachment", e.projectPath() + "/attachments", e.projectPath() + "/attachments"},
		{"sprint attachment", e.projectPath() + "/sprints/" + sprint.Ref + "/attachments", e.projectPath() + "/sprints/" + sprint.Ref + "/attachments"},
	}
	for _, r := range routes {
		code, raw := e.doMultipartPath(t, e.authToken, r.uploadPath, "take.mp3", clip)
		if code != http.StatusCreated {
			t.Fatalf("%s upload code = %d body = %s", r.name, code, raw)
		}
		var ref string
		if r.name == "generic object" {
			ref = decode[model.StorageObject](t, raw).Ref
		} else {
			ref = decode[struct {
				Object model.StorageObject `json:"object"`
			}](t, raw).Object.Ref
		}
		for _, ui := range []bool{false, true} {
			if ui && r.name == "generic object" {
				continue // there is no page route for a bare object
			}
			for _, query := range []string{"", "?inline=1"} {
				what := r.name + query
				res, body := e.getMedia(t, ui, e.authToken, r.contentPrefix+"/"+ref+"/content"+query, map[string]string{"Range": "bytes=0-9"})
				if res.StatusCode != http.StatusOK || !bytes.Equal(body, clip) {
					t.Fatalf("%s (ui=%v): code = %d, want the whole file with 200 and no range handling", what, ui, res.StatusCode)
				}
				if res.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
					t.Fatalf("%s (ui=%v): type = %q disposition = %q, want an octet-stream download", what, ui, res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"))
				}
				if res.Header.Get("Accept-Ranges") != "" || res.Header.Get("Content-Range") != "" || res.Header.Get("Content-Length") != strconv.Itoa(len(clip)) {
					t.Fatalf("%s (ui=%v): ranges = %q content-range = %q length = %q", what, ui, res.Header.Get("Accept-Ranges"), res.Header.Get("Content-Range"), res.Header.Get("Content-Length"))
				}
				if res.Header.Get("Etag") != wantETag || res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" {
					t.Fatalf("%s (ui=%v): headers = %v", what, ui, res.Header)
				}
			}
		}
	}

	// A safe image is still inline where it always was, and still no ranges.
	code, raw := e.doMultipartObject(t, e.authToken, "shot.png", formMediaPNG)
	if code != http.StatusCreated {
		t.Fatalf("png upload code = %d", code)
	}
	ref := decode[model.StorageObject](t, raw).Ref
	res, body := e.getMedia(t, false, e.authToken, e.projectObjectsPath()+"/"+ref+"/content", map[string]string{"Range": "bytes=0-3"})
	if res.StatusCode != http.StatusOK || !bytes.Equal(body, formMediaPNG) || res.Header.Get("Accept-Ranges") != "" || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("generic image: code = %d headers = %v", res.StatusCode, res.Header)
	}
}
