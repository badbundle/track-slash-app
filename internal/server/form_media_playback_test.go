package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
)

// playbackBackend is a storage backend that opens whatever the test gives it.
type playbackBackend struct {
	open func() (io.ReadCloser, error)
}

func (b playbackBackend) Put(context.Context, string, io.Reader, int64) (objectstorage.WrittenObject, error) {
	return objectstorage.WrittenObject{}, errors.New("not used")
}
func (b playbackBackend) Open(context.Context, string) (io.ReadCloser, error) { return b.open() }
func (b playbackBackend) Delete(context.Context, string) error                { return nil }

// seekableBody is a body that can seek, as a file can.
type seekableBody struct{ *bytes.Reader }

func (seekableBody) Close() error { return nil }

// erroringBody fails the first read and can't seek.
type erroringBody struct{}

func (erroringBody) Read([]byte) (int, error) { return 0, errors.New("disk gone") }
func (erroringBody) Close() error             { return nil }

func playbackServer(t *testing.T, open func() (io.ReadCloser, error)) *Server {
	t.Helper()
	svc, err := objectstorage.NewService("fake", "bucket", 1<<20, playbackBackend{open: open})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return NewWithOptions(nil, nil, Options{ObjectStorage: svc})
}

func playbackObject(contentType string) model.StorageObject {
	return model.StorageObject{ObjectKey: "k", Filename: "take.m4a", ContentType: contentType, SHA256: "abc123", ByteSize: 26}
}

const playbackClip = "abcdefghijklmnopqrstuvwxyz"

func serveForm(srv *Server, object model.StorageObject, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/content", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.streamFormMediaContent(rec, req, object)
	return rec
}

func TestStreamFormMediaContentWithSeekableAndPlainBodies(t *testing.T) {
	t.Parallel()
	bodies := map[string]func() (io.ReadCloser, error){
		"seekable":     func() (io.ReadCloser, error) { return seekableBody{bytes.NewReader([]byte(playbackClip))}, nil },
		"not seekable": func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(playbackClip)), nil },
	}
	for name, open := range bodies {
		srv := playbackServer(t, open)

		rec := serveForm(srv, playbackObject("audio/mp4"), nil)
		if rec.Code != http.StatusOK || rec.Body.String() != playbackClip {
			t.Fatalf("%s: whole = %d %q", name, rec.Code, rec.Body.String())
		}
		for header, want := range map[string]string{
			"Content-Type":                 "audio/mp4",
			"Content-Disposition":          "inline; filename=take.m4a",
			"Accept-Ranges":                "bytes",
			"Content-Length":               "26",
			"Etag":                         `"abc123"`,
			"X-Content-Type-Options":       "nosniff",
			"Content-Security-Policy":      "default-src 'none'; sandbox",
			"Cross-Origin-Resource-Policy": "same-origin",
		} {
			if got := rec.Header().Get(header); got != want {
				t.Fatalf("%s: %s = %q, want %q", name, header, got, want)
			}
		}

		rec = serveForm(srv, playbackObject("audio/mp4"), map[string]string{"Range": "bytes=2-11"})
		if rec.Code != http.StatusPartialContent || rec.Body.String() != "cdefghijkl" || rec.Header().Get("Content-Range") != "bytes 2-11/26" || rec.Header().Get("Content-Type") != "audio/mp4" {
			t.Fatalf("%s: range = %d %q %q", name, rec.Code, rec.Body.String(), rec.Header())
		}
		rec = serveForm(srv, playbackObject("audio/mp4"), map[string]string{"Range": "bytes=-3"})
		if rec.Code != http.StatusPartialContent || rec.Body.String() != "xyz" {
			t.Fatalf("%s: suffix range = %d %q", name, rec.Code, rec.Body.String())
		}
		rec = serveForm(srv, playbackObject("audio/mp4"), map[string]string{"Range": "bytes=90-99"})
		if rec.Code != http.StatusRequestedRangeNotSatisfiable || rec.Header().Get("Content-Range") != "bytes */26" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: unsatisfiable = %d %q", name, rec.Code, rec.Header())
		}
		rec = serveForm(srv, playbackObject("audio/mp4"), map[string]string{"If-None-Match": `"abc123"`})
		if rec.Code != http.StatusNotModified {
			t.Fatalf("%s: If-None-Match code = %d", name, rec.Code)
		}
		// If-Range with the current validator keeps the range; a stale one
		// sends the whole file.
		rec = serveForm(srv, playbackObject("audio/mp4"), map[string]string{"Range": "bytes=0-1", "If-Range": `"abc123"`})
		if rec.Code != http.StatusPartialContent || rec.Body.String() != "ab" {
			t.Fatalf("%s: If-Range match = %d %q", name, rec.Code, rec.Body.String())
		}
		rec = serveForm(srv, playbackObject("audio/mp4"), map[string]string{"Range": "bytes=0-1", "If-Range": `"stale"`})
		if rec.Code != http.StatusOK || rec.Body.String() != playbackClip {
			t.Fatalf("%s: If-Range stale = %d %q", name, rec.Code, rec.Body.String())
		}
	}
}

func TestStreamFormMediaContentTypes(t *testing.T) {
	t.Parallel()
	srv := playbackServer(t, func() (io.ReadCloser, error) { return seekableBody{bytes.NewReader([]byte(playbackClip))}, nil })
	for contentType, want := range map[string]string{
		"audio/mpeg": "audio/mpeg", " Video/MP4 ": "video/mp4", "video/webm": "video/webm", "image/png": "image/png",
		// Not playable, so a download of opaque bytes in a sandbox.
		"text/html": "application/octet-stream", "image/svg+xml": "application/octet-stream",
		"application/javascript": "application/octet-stream", "audio/flac": "application/octet-stream",
		"application/pdf": "application/octet-stream", "": "application/octet-stream",
	} {
		rec := serveForm(srv, playbackObject(contentType), nil)
		disposition := "attachment"
		if want != "application/octet-stream" {
			disposition = "inline"
		}
		if rec.Header().Get("Content-Type") != want || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), disposition) {
			t.Fatalf("%q served as %q, %q; want %q, %s", contentType, rec.Header().Get("Content-Type"), rec.Header().Get("Content-Disposition"), want, disposition)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
			t.Fatalf("%q lost its protective headers: %v", contentType, rec.Header())
		}
	}
}

func TestStreamFormMediaContentFailures(t *testing.T) {
	t.Parallel()
	// No object storage.
	rec := serveForm(NewWithOptions(nil, nil, Options{}), playbackObject("audio/mpeg"), nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no storage code = %d", rec.Code)
	}
	// The file is gone from storage.
	rec = serveForm(playbackServer(t, func() (io.ReadCloser, error) { return nil, objectstorage.ErrNotFound }), playbackObject("audio/mpeg"), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing bytes code = %d", rec.Code)
	}
	// A body that can't seek and can't be read.
	rec = serveForm(playbackServer(t, func() (io.ReadCloser, error) { return erroringBody{}, nil }), playbackObject("audio/mpeg"), nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unreadable body code = %d", rec.Code)
	}
}

func TestFormMediaDeclaredType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ filename, declared, want string }{
		// No declared type: the extension decides.
		{"a.mp3", "", "audio/mpeg"}, {"a.m4a", "application/octet-stream", "audio/mp4"},
		{"a.aac", "", "audio/aac"}, {"a.wav", "", "audio/wav"},
		{"a.ogg", "", "audio/ogg"}, {"a.oga", "", "audio/ogg"}, {"a.opus", "", "audio/ogg"},
		{"a.mp4", "", "video/mp4"}, {"a.m4v", "", "video/mp4"},
		{"a.mov", "", "video/quicktime"}, {"a.webm", "", "video/webm"},
		{"CLIP.M4A", " Application/Octet-Stream ", "audio/mp4"},
		{"a.m4a", "application/octet-stream; foo=bar", "audio/mp4"},
		// Not in the map: left for the sniffer.
		{"a.html", "", ""}, {"a.png", "application/octet-stream", "application/octet-stream"}, {"noext", "", ""},
		// A declared type is kept, whatever the extension says.
		{"a.mp3", "text/html", "text/html"}, {"a.png", "audio/mpeg", "audio/mpeg"}, {"a.m4a", "video/mp4", "video/mp4"},
		// A declared type that can't be read is not a declared type.
		{"a.mp3", "///", "audio/mpeg"},
	} {
		if got := formMediaDeclaredType(tc.filename, tc.declared); got != tc.want {
			t.Fatalf("formMediaDeclaredType(%q, %q) = %q, want %q", tc.filename, tc.declared, got, tc.want)
		}
	}
	// Every type the extensions give is one the content route plays.
	for ext, contentType := range formUploadTypesByExtension {
		if formPlayableKind(contentType) == "" {
			t.Fatalf("%s maps to %s, which is not playable", ext, contentType)
		}
	}
}

func TestFormPlayableTypesAreAudioAndVideoOnly(t *testing.T) {
	t.Parallel()
	if len(formPlayableTypes) != 11 {
		t.Fatalf("playable types = %d, want the 11 the security model lists", len(formPlayableTypes))
	}
	for contentType, kind := range formPlayableTypes {
		if !strings.HasPrefix(contentType, kind+"/") || (kind != "audio" && kind != "video") {
			t.Fatalf("%s is listed as %s", contentType, kind)
		}
	}
	for _, kind := range []string{"audio/flac", "video/x-matroska", "text/html", "image/svg+xml", "application/javascript", "application/x-shockwave-flash"} {
		if formPlayableKind(kind) != "" {
			t.Fatalf("%s must not be playable", kind)
		}
	}
}

func uiFormMediaTestForm() model.Form {
	obj := func(n int, name, contentType string, size int64) model.FormAttachment {
		return model.FormAttachment{Object: model.StorageObject{Ref: "object-" + string(rune('0'+n)), Filename: name, ContentType: contentType, ByteSize: size}}
	}
	return model.Form{
		Ref:         "form-1",
		Description: "See ![shot](object-5) and [the notes](object-6).",
		Definition: model.FormDefinition{Sections: []model.FormSection{{
			Description: "Section [clip](object-7)",
			Questions: []model.FormQuestion{{
				ID: "q", Type: model.FormQuestionSingleChoice, Label: "Pick", Help: "A `object-9` in code is not a link",
				Media: []string{"object-1", "object-2", "object-404"},
				Options: []model.FormOption{
					{ID: "a", Label: "A", Media: "object-3"},
					{ID: "b", Label: "B", Media: "object-4"},
					{ID: "c", Label: "C"},
					{ID: "d", Label: "D", Media: "object-404"},
				},
			}},
		}}},
		Attachments: []model.FormAttachment{
			obj(1, "one.png", "image/png", 10), obj(2, "two.m4a", "audio/mp4", 2048),
			obj(3, "a.png", "image/png", 10), obj(4, "b.mp4", "video/mp4", 10),
			obj(5, "shot.png", "image/png", 10), obj(6, "notes.txt", "text/plain", 1<<20),
			obj(7, "clip.mp3", "audio/mpeg", 10), obj(8, "stray.pdf", "application/pdf", 10),
			obj(9, "code.jpg", "image/jpeg", 10),
		},
	}
}

func TestUIFormMediaLookup(t *testing.T) {
	t.Parallel()
	form := uiFormMediaTestForm()
	base := "/o/projects/P/forms/form-1"
	lookup := newUIFormMediaLookup(base, form)

	if item := lookup.item("object-2"); item == nil || *item != (uiFormMediaItem{Kind: "audio", Href: base + "/attachments/object-2/content", Filename: "two.m4a", Size: "2.0 KB", Ref: "object-2"}) {
		t.Fatalf("item = %+v", item)
	}
	if lookup.item("object-404") != nil || lookup.item("") != nil {
		t.Fatal("an unknown ref has no item")
	}

	media := lookup.media(form.Definition.Sections[0].Questions[0].Media)
	if len(media.Images) != 1 || media.Images[0].Filename != "one.png" || len(media.Others) != 1 || media.Others[0].Kind != "audio" {
		t.Fatalf("question media = %+v, want one image then the audio, and the unknown ref left out", media)
	}
	if empty := lookup.media(nil); len(empty.Images)+len(empty.Others) != 0 {
		t.Fatalf("no refs = %+v", empty)
	}

	// Only the stray PDF and the file named in code are unplaced: Markdown
	// places object-5, -6 and -7, and the questions place 1 to 4.
	var got []string
	unplaced := lookup.unplaced(form)
	for _, item := range append(append([]uiFormMediaItem{}, unplaced.Images...), unplaced.Others...) {
		got = append(got, item.Filename)
	}
	if !reflect.DeepEqual(got, []string{"code.jpg", "stray.pdf"}) {
		t.Fatalf("unplaced = %v", got)
	}

	// Thumbnails: the chosen options that have an image.
	question := form.Definition.Sections[0].Questions[0]
	selected := func(ids ...string) []model.FormSelectedOption {
		var out []model.FormSelectedOption
		for _, id := range ids {
			out = append(out, model.FormSelectedOption{ID: id})
		}
		return out
	}
	thumbs := lookup.thumbs(question, selected("a", "b", "c", "d", "other", model.FormOtherOptionID))
	if len(thumbs) != 1 || thumbs[0].Href != base+"/attachments/object-3/content" {
		t.Fatalf("thumbs = %+v, want only the image option", thumbs)
	}
	if lookup.thumbs(question, nil) != nil {
		t.Fatal("nothing chosen, no thumbnails")
	}
}

func TestUIFormHasPictures(t *testing.T) {
	t.Parallel()
	image := &uiFormMediaItem{Kind: "image"}
	audio := &uiFormMediaItem{Kind: "audio"}
	for want, options := range map[bool][]uiFormOptionData{
		true:  {{}, {Media: audio}, {Media: image}},
		false: {{}, {Media: audio}},
	} {
		if got := uiFormHasPictures(options); got != want {
			t.Fatalf("uiFormHasPictures(%+v) = %v", options, got)
		}
	}
	if uiFormHasPictures(nil) {
		t.Fatal("no options have no pictures")
	}
}

func TestMarkdownObjectRefs(t *testing.T) {
	t.Parallel()
	for source, want := range map[string][]string{
		"":                                   nil,
		"plain text, object-1":               nil,
		"![a](object-1) and [b]( object-2 )": {"object-1", "object-2"},
		"[x](https://example.com) ![y](/p.png) [z](object-x)": nil,
		"`[c](object-3)`": nil,
	} {
		if got := markdownObjectRefs(source); !reflect.DeepEqual(got, want) {
			t.Fatalf("markdownObjectRefs(%q) = %v, want %v", source, got, want)
		}
	}
}

func TestRenderFormMarkdownResolvesFormFiles(t *testing.T) {
	t.Parallel()
	form := uiFormMediaTestForm()
	html := string(renderFormMarkdown("/base", form, "![shot](object-5) [notes](object-6) ![gone](object-99)"))
	for _, want := range []string{`<img src="/base/attachments/object-5/content?inline=1" alt="shot">`, `<a href="/base/attachments/object-6/content">notes</a>`} {
		if !strings.Contains(html, want) {
			t.Fatalf("html = %s, missing %s", html, want)
		}
	}
	if strings.Contains(html, "object-99/content") {
		t.Fatalf("a ref that is not one of the form's files was linked: %s", html)
	}
}
