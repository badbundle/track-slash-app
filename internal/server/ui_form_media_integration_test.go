package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/server"
)

// uiFormMediaForm makes, through the agent flow, a two-section form that
// carries files in every place: Markdown, a question, picture options, clip
// options, and one file nothing places.
func uiFormMediaForm(t *testing.T, e *httpEnv) (formJSON, map[string]string) {
	t.Helper()
	upload := e.mustUploadURL(t, e.authToken).UploadURL
	clip := []byte("0123456789")
	refs := map[string]string{}
	for name, f := range map[string]struct {
		declared string
		content  []byte
	}{
		"hero.png": {"", formMediaPNG}, "a.png": {"", formMediaPNG}, "b.png": {"", formMediaPNG},
		"q1.png": {"", formMediaPNG}, "q2.png": {"", formMediaPNG},
		"q.m4a": {"", clip}, "q.mp4": {"", clip}, "q.txt": {"text/plain", formMediaText},
		"x.m4a": {"", clip}, "y.mp4": {"", clip}, "z.txt": {"text/plain", formMediaText},
		"stray.pdf": {"application/pdf", []byte("%PDF-1.4 stray")},
	} {
		refs[name] = e.mustUploadFile(t, upload, name, f.declared, f.content).Object
	}
	sections := []map[string]any{
		{
			"title": "Pictures",
			"questions": []map[string]any{
				{
					"id": "thumb", "type": "single_choice", "label": "Which thumbnail?", "help": "Compare ![](" + refs["hero.png"] + ") first.",
					"media": []string{refs["q1.png"]},
					"options": []map[string]any{
						{"id": "a", "label": "Thumbnail A", "media": refs["a.png"]},
						{"id": "b", "label": "Thumbnail B", "media": refs["b.png"]},
						{"id": "c", "label": "Neither"},
					},
					"allow_other": true,
				},
				{
					"id": "gallery", "type": "text", "label": "Notes on the gallery", "required": false,
					"media": []string{refs["q1.png"], refs["q2.png"], refs["q.m4a"], refs["q.mp4"], refs["q.txt"]},
				},
			},
		},
		{
			"title": "Clips",
			"questions": []map[string]any{{
				"id": "clip", "type": "single_choice", "label": "Which clip?", "required": false,
				"options": []map[string]any{
					{"id": "x", "label": "Voice", "media": refs["x.m4a"]},
					{"id": "y", "label": "Video", "media": refs["y.mp4"]},
					{"id": "z", "label": "Notes", "media": refs["z.txt"]},
				},
			}},
		},
	}
	code, raw := e.doWithToken(t, e.authToken, http.MethodPost, e.formsPath(), map[string]any{
		"title": "Pick the thumbnail", "description": "Look at ![the hero](" + refs["hero.png"] + ") and [the notes](" + refs["q.txt"] + ").",
		"sections": sections, "attachments": []string{refs["hero.png"], refs["stray.pdf"]},
	})
	if code != http.StatusCreated {
		t.Fatalf("create form code = %d body = %s", code, raw)
	}
	return decode[formJSON](t, raw), refs
}

func TestUIFormPageShowsItsMedia(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	form, refs := uiFormMediaForm(t, e)
	path := e.projectPath() + "/forms/" + form.Ref
	src := func(name string) string { return path + "/attachments/" + refs[name] + "/content" }

	// Step 1: question images, picture cards, a grid of several images, the
	// audio, video and a download row, Markdown that resolves, the unplaced file.
	res, body := e.uiFormGetDoc(t, path+"?step=1", e.authToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("page code = %d", res.StatusCode)
	}
	requireContains(t, "step 1", body,
		// Markdown in the description and in a question's help.
		`<img src="`+src("hero.png")+`?inline=1" alt="the hero">`, `<a href="`+src("q.txt")+`">the notes</a>`,
		`<img src="`+src("hero.png")+`?inline=1" alt="">`,
		// The first question has one image: shown once, opening in a new tab.
		`<a href="`+src("q1.png")+`" target="_blank" rel="noopener noreferrer" data-form-media-image`,
		`<img src="`+src("q1.png")+`" alt="q1.png" loading="lazy"`,
		// Picture options: a card with its image and an Open link, in a grid.
		`data-form-option-image`, `<img src="`+src("a.png")+`" alt="a.png" loading="lazy"`, `<img src="`+src("b.png")+`" alt="b.png" loading="lazy"`,
		`href="`+src("a.png")+`" target="_blank" rel="noopener noreferrer" data-form-option-open`,
		`grid grid-cols-2 gap-2 sm:grid-cols-3`, `name="selected" value="a"`,
		// The second question: two images in a 2-column grid, then the clips.
		`<div class="grid gap-2 grid-cols-2">`, `<img src="`+src("q2.png")+`" alt="q2.png" loading="lazy"`,
		`<audio controls preload="metadata" src="`+src("q.m4a")+`"`, `<video controls playsinline preload="metadata" src="`+src("q.mp4")+`"`,
		`data-form-media-file`, `href="`+src("q.txt")+`"`, ">q.txt<", ">11 B<",
		// The file nothing places is listed under the description.
		`data-form-files`, ">stray.pdf<", `href="`+src("stray.pdf")+`"`,
	)
	requireOmits(t, "step 1", body, "autoplay", ` style=`, "<script>", src("x.m4a"), src("z.txt"))
	if strings.Count(body, "data-form-files") != 1 || strings.Count(body, ">stray.pdf<") != 1 {
		t.Fatalf("the unplaced file is listed more than once: %s", body)
	}
	if strings.Count(body, `href="`+src("hero.png")) != 0 || strings.Contains(body[strings.Index(body, "data-form-files"):], ">hero.png<") {
		t.Fatalf("a file Markdown places is listed as unplaced")
	}

	// Step 2: clips inside their options, under the label, and a download row
	// for a file option; no picture grid, since no option has an image.
	_, body = e.uiFormGetDoc(t, path+"?step=2", e.authToken)
	requireContains(t, "step 2", body,
		`<audio controls preload="metadata" src="`+src("x.m4a")+`"`, `<video controls playsinline preload="metadata" src="`+src("y.mp4")+`"`,
		`data-form-media-file`, `href="`+src("z.txt")+`"`, ">z.txt<", `data-form-files`,
	)
	requireOmits(t, "step 2", body, "data-form-option-image", "grid-cols-2 gap-2", "autoplay", src("a.png"))

	// The review step and the submitted view carry a thumbnail beside a
	// chosen picture option, and nothing for a clip.
	e.uiFormPost(t, path+"/answers/thumb", e.authToken, url.Values{"selected": {"a"}}, false)
	e.uiFormPost(t, path+"/answers/clip", e.authToken, url.Values{"selected": {"x"}}, false)
	_, body = e.uiFormGetDoc(t, path+"?step=review", e.authToken)
	requireContains(t, "review", body, `data-form-thumbs`, `<img src="`+src("a.png")+`" alt="" loading="lazy"`)
	if strings.Count(body, "data-form-thumbs") != 1 || strings.Contains(body, `<img src="`+src("x.m4a")) {
		t.Fatalf("review shows a thumbnail for something that is not a chosen picture: %s", body)
	}
	res, body = e.uiFormPost(t, path+"/submit", e.authToken, url.Values{}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("submit code = %d body = %s", res.StatusCode, body)
	}
	_, body = e.uiFormGetDoc(t, path, e.authToken)
	requireContains(t, "submitted view", body, `data-form-status="submitted"`, `data-form-thumbs`, `<img src="`+src("a.png")+`" alt="" loading="lazy"`,
		`data-form-files`, ">stray.pdf<", `<img src="`+src("hero.png")+`?inline=1" alt="the hero">`)
	requireOmits(t, "submitted view", body, "<audio", "<video", "data-form-option-image")
	if strings.Count(body, "data-form-thumbs") != 1 {
		t.Fatalf("submitted view thumbnails = %d, want 1", strings.Count(body, "data-form-thumbs"))
	}
}

func TestUIFormMediaIsMembersOnly(t *testing.T) {
	t.Parallel()
	e, _ := formMediaEnv(t, server.Options{})
	_, readerToken := e.mustUserToken(t, "media-reader")
	e.makeProjectPublic(t)
	form, refs := uiFormMediaForm(t, e)
	path := e.projectPath() + "/forms/" + form.Ref

	for who, token := range map[string]string{"reader who is not a member": readerToken, "visitor": ""} {
		for _, target := range []string{path, path + "?step=1", path + "/panel", path + "/attachments/" + refs["a.png"] + "/content"} {
			res, body := e.uiFormGetDoc(t, target, token)
			if res.StatusCode == http.StatusOK {
				t.Fatalf("%s got 200 from %s", who, target)
			}
			requireOmits(t, who+" "+target, body, "a.png", "stray.pdf", "attachments/object", "Pick the thumbnail", "<img src=\"/"+e.ownerUsername)
		}
	}
}
