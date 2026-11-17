package server

import (
	"html/template"
	"slices"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"

	"github.com/bradleymackey/track-slash/internal/model"
)

// The files a form carries are shown on its page: with the question or option
// that names them, inside Markdown that points at them, or, when nothing
// places them, in a list under the description. Every href is the form's own
// content route, which is for members only, so nothing here is shown to anyone
// the page itself is not.

// uiFormMediaItem is one file as the page shows it. Kind is image, audio, video
// or file, as formAttachmentKind says.
type uiFormMediaItem struct {
	Kind     string
	Href     string
	Filename string
	Size     string
	// Ref is the file's object ref. Remove is the route that removes it from
	// the person's answer, set only for an answer file the signed-in person
	// can still remove.
	Ref    string
	Remove string
}

// uiFormMedia is the files shown together. Images come first, as a grid when
// there are several, then audio, video and downloads in their own order.
type uiFormMedia struct {
	Images []uiFormMediaItem
	Others []uiFormMediaItem
}

// uiFormThumb is a small picture beside a chosen option in the review step and
// the submitted view.
type uiFormThumb struct {
	Href string
}

// uiFormMediaLookup finds a form's file by its object ref.
type uiFormMediaLookup map[string]uiFormMediaItem

func newUIFormMediaLookup(base string, form model.Form) uiFormMediaLookup {
	lookup := make(uiFormMediaLookup, len(form.Attachments))
	for _, attachment := range form.Attachments {
		object := attachment.Object
		lookup[object.Ref] = uiFormMediaItem{
			Kind:     formAttachmentKind(object),
			Href:     base + "/attachments/" + object.Ref + "/content",
			Filename: object.Filename,
			Size:     uiByteSize(object.ByteSize),
			Ref:      object.Ref,
		}
	}
	return lookup
}

// media groups the files the refs name. A ref that is not one of the form's
// files is left out.
func (l uiFormMediaLookup) media(refs []string) uiFormMedia {
	var out uiFormMedia
	for _, ref := range refs {
		item, ok := l[ref]
		if !ok {
			continue
		}
		if item.Kind == "image" {
			out.Images = append(out.Images, item)
		} else {
			out.Others = append(out.Others, item)
		}
	}
	return out
}

// item is the file a ref names, or nil.
func (l uiFormMediaLookup) item(ref string) *uiFormMediaItem {
	item, ok := l[ref]
	if !ok {
		return nil
	}
	return &item
}

// unplaced lists the form's files that no question, option or Markdown shows,
// in the order they were attached.
func (l uiFormMediaLookup) unplaced(form model.Form) uiFormMedia {
	placed := map[string]bool{}
	for _, ref := range form.Definition.MediaRefs() {
		placed[ref] = true
	}
	mark := func(source string) {
		for _, ref := range markdownObjectRefs(source) {
			placed[ref] = true
		}
	}
	mark(form.Description)
	for _, section := range form.Definition.Sections {
		mark(section.Description)
		for _, question := range section.Questions {
			mark(question.Help)
		}
	}
	var refs []string
	for _, attachment := range form.AskerAttachments() {
		if ref := attachment.Object.Ref; !placed[ref] {
			refs = append(refs, ref)
		}
	}
	return l.media(refs)
}

// thumbs are the pictures of the options a person chose, for the review step
// and the submitted view. An option with no image, and one that is Other, has
// none.
func (l uiFormMediaLookup) thumbs(question model.FormQuestion, selected []model.FormSelectedOption) []uiFormThumb {
	var out []uiFormThumb
	for _, chosen := range selected {
		i := slices.IndexFunc(question.Options, func(o model.FormOption) bool { return o.ID == chosen.ID })
		if i < 0 || question.Options[i].Media == "" {
			continue
		}
		if item, ok := l[question.Options[i].Media]; ok && item.Kind == "image" {
			out = append(out, uiFormThumb{Href: item.Href})
		}
	}
	return out
}

// uiFormHasPictures reports whether any option of the question has an image, which
// lays the options out as picture cards in a grid.
func uiFormHasPictures(options []uiFormOptionData) bool {
	return slices.ContainsFunc(options, func(o uiFormOptionData) bool {
		return o.Media != nil && o.Media.Kind == "image"
	})
}

// renderFormMarkdown renders form text so that object-N in an image or link
// resolves against the files the asker put in the form, as an issue's
// description does against its attachments. The files the person attached to
// their answers are not the asker's to place.
func renderFormMarkdown(base string, form model.Form, source string) template.HTML {
	asked := form.AskerAttachments()
	objects := make([]model.StorageObject, 0, len(asked))
	for _, attachment := range asked {
		objects = append(objects, attachment.Object)
	}
	return renderDescriptionMarkdown(source, objects, func(object model.StorageObject) string {
		return base + "/attachments/" + object.Ref + "/content"
	})
}

// markdownObjectRefs lists the object-N refs that Markdown links or shows as an
// image, so a file the text places is not listed again as unplaced.
func markdownObjectRefs(source string) []string {
	if source == "" {
		return nil
	}
	src := []byte(source)
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(src))
	var refs []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var destination []byte
		switch node := n.(type) {
		case *ast.Link:
			destination = node.Destination
		case *ast.Image:
			destination = node.Destination
		default:
			return ast.WalkContinue, nil
		}
		ref := strings.TrimSpace(string(destination))
		if _, err := parseTypedRef(ref, "object"); err == nil {
			refs = append(refs, ref)
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// uiFormAnswerFiles backs the form-answer-files component: the files attached
// to one answer, with the picker and Remove while the person can still change
// them.
type uiFormAnswerFiles struct {
	CSRFToken  string
	QuestionID string
	Label      string
	// Action posts a file; Step is the step the card is on, posted back so the
	// footer that answers is the one for that step.
	Action string
	Step   string
	Media  uiFormMedia
	Count  int
	// Editable shows the picker and Remove. Full hides the picker because the
	// answer has all the files it can take.
	Editable bool
	Full     bool
	// OOB marks the copy an upload or removal sends back, which replaces the
	// component on the page.
	OOB bool
}

// answerFiles prepares the files the refs name, which are one answer's. A ref
// that is not one of the form's files is left out. With editable set, each file
// can be removed.
func (l uiFormMediaLookup) answerFiles(base, token string, question model.FormQuestion, refs []string, editable bool) uiFormAnswerFiles {
	data := uiFormAnswerFiles{
		CSRFToken:  token,
		QuestionID: question.ID,
		Label:      question.Label,
		Action:     base + "/answers/" + question.ID + "/files",
		Editable:   editable,
	}
	for _, ref := range refs {
		item, ok := l[ref]
		if !ok {
			continue
		}
		if editable {
			item.Remove = data.Action + "/" + ref + "/remove"
		}
		if item.Kind == "image" {
			data.Media.Images = append(data.Media.Images, item)
		} else {
			data.Media.Others = append(data.Media.Others, item)
		}
		data.Count++
	}
	data.Full = data.Count >= model.FormMaxAnswerFiles
	return data
}
