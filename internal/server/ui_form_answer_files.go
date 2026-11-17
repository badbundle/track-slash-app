package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// The form page lets the person attach files to an answer and remove them,
// without a reload. It is the same act as the REST routes in
// form_answer_files.go, and as answering: only the assignee, and only while
// the form is open. Both routes answer an htmx request with out-of-band
// fragments only: the footer, the question's error slot and its file list.

// captureWriter takes what a REST-style upload helper would write, so the page
// can show its message in the question's error slot instead.
type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCaptureWriter() *captureWriter {
	return &captureWriter{header: http.Header{}, status: http.StatusOK}
}

func (c *captureWriter) Header() http.Header         { return c.header }
func (c *captureWriter) WriteHeader(status int)      { c.status = status }
func (c *captureWriter) Write(p []byte) (int, error) { return c.body.Write(p) }

// message says in words for the person why the helper refused.
func (c *captureWriter) message(maxFileBytes int64) string {
	var body errorBody
	_ = json.Unmarshal(c.body.Bytes(), &body)
	switch {
	case c.status == http.StatusRequestEntityTooLarge:
		return fmt.Sprintf("That file is too large: the most is %s.", uiByteSize(maxFileBytes))
	case c.status >= 500 || body.Error == "":
		return "The file could not be saved. Try again."
	}
	return uiSentence(body.Error)
}

// uiSentence makes a message start with a capital and end with a full stop.
func uiSentence(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return message
	}
	message = strings.ToUpper(message[:1]) + message[1:]
	if !strings.HasSuffix(message, ".") {
		message += "."
	}
	return message
}

// uiFormFileMessage says in words why a file could not be attached or removed.
func uiFormFileMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrFormAnswerFilesFull):
		return fmt.Sprintf("You can attach %d files to an answer.", model.FormMaxAnswerFiles)
	case errors.Is(err, store.ErrFormFilesFull):
		return fmt.Sprintf("You can attach %d files to a form.", model.FormMaxAnswerFilesPerForm)
	case errors.Is(err, store.ErrConflict):
		return "This form is no longer open, so its answers can't change."
	}
	return "The file could not be saved. Try again."
}

// uiFormFilesSaveData backs the fragment an upload or removal answers with:
// the footer, the question's error slot and its list of files.
type uiFormFilesSaveData struct {
	Footer  uiFormFooterData
	SlotID  string
	Message string
	Files   uiFormAnswerFiles
}

func (s *Server) uiAttachFormAnswerFile(w http.ResponseWriter, r *http.Request) {
	project, _, form, ok := s.uiAssigneeFormFromRoute(w, r)
	if !ok {
		return
	}
	question, ok := form.Definition.Question(chi.URLParam(r, "questionID"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	message, step := s.uiStoreAnswerFiles(r, form, question)
	s.finishFormFiles(w, r, project, form, question, message, step)
}

// uiStoreAnswerFiles reads the files of the request into the person's answer,
// one at a time, so a file that is refused does not stop the ones before it. It
// returns the message to show, if any, and the step the card was on.
func (s *Server) uiStoreAnswerFiles(r *http.Request, form model.Form, question model.FormQuestion) (message, step string) {
	if s.objectStorage == nil {
		return "File uploads are not available.", ""
	}
	maxFileBytes := s.formMediaMaxFileBytes()
	parsed := newCaptureWriter()
	if !parseUploadRequest(parsed, r, maxFileBytes, model.FormMaxAnswerFiles) {
		return parsed.message(maxFileBytes), ""
	}
	step = r.FormValue("step")
	if len(r.MultipartForm.File["file"]) == 0 {
		return "Choose a file to attach.", step
	}
	user := currentUser(r)
	for _, header := range r.MultipartForm.File["file"] {
		if err := store.CheckFormAnswerFileAttach(form, question.ID); err != nil {
			return uiFormFileMessage(err), step
		}
		file, err := header.Open()
		if err != nil {
			return "The file could not be read. Try again.", step // defensive: the parsed upload is held in memory or in a temporary file this request made
		}
		rec := newCaptureWriter()
		_, stored := s.storeUploadedFile(rec, r, file, header, form.ProjectID, user.ID, maxFileBytes, formMediaDeclaredType,
			func(ctx context.Context, p store.CreateStorageObjectParams) (model.StorageObject, error) {
				updated, attached, err := s.store.AttachFormAnswerFile(ctx, store.AttachFormAnswerFileParams{
					FormID: form.ID, AssigneeID: user.ID, QuestionID: question.ID, Object: p,
				})
				if err == nil {
					form = updated
				}
				return attached.Object, err
			})
		_ = file.Close()
		if !stored {
			return rec.message(maxFileBytes), step
		}
	}
	return "", step
}

func (s *Server) uiRemoveFormAnswerFile(w http.ResponseWriter, r *http.Request) {
	project, _, form, ok := s.uiAssigneeFormFromRoute(w, r)
	if !ok {
		return
	}
	question, ok := form.Definition.Question(chi.URLParam(r, "questionID"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	number, err := parseTypedRef(chi.URLParam(r, "objectRef"), "object")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	message := ""
	_, err = s.store.RemoveFormAnswerFile(r.Context(), form.ID, currentUser(r).ID, question.ID, number)
	switch {
	case err == nil, errors.Is(err, store.ErrNotFound):
		// A file that is already gone, from a page that was out of date, is
		// removed all the same: the list that comes back shows it.
	case errors.Is(err, store.ErrConflict):
		message = uiFormFileMessage(err)
	default:
		writeUIStoreError(w, err) // defensive: the form was just read; only an outage fails here
		return
	}
	s.finishFormFiles(w, r, project, form, question, message, r.FormValue("step"))
}

// finishFormFiles answers an upload or removal with the form as it is now. A
// plain post goes back to the form's page.
func (s *Server) finishFormFiles(w http.ResponseWriter, r *http.Request, project model.Project, before model.Form, question model.FormQuestion, message, step string) {
	if !isHTMXRequest(r) {
		http.Redirect(w, r, formPath(project.OwnerUsername, project.Key, before.Ref), http.StatusSeeOther)
		return
	}
	latest, err := s.store.GetForm(r.Context(), before.ID)
	if err != nil {
		writeUIStoreError(w, err) // defensive: the form was read a moment ago
		return
	}
	base := formPath(project.OwnerUsername, project.Key, latest.Ref)
	files := newUIFormMediaLookup(base, latest).answerFiles(base, uiSessionCSRFToken(r), question, latest.Answers[question.ID].Files, latest.Status == model.FormStatusOpen)
	files.Step = step
	files.OOB = true
	renderUITemplate(w, http.StatusOK, "form-files-response", uiFormFilesSaveData{
		Footer:  s.uiFormFooter(r, latest, true, step),
		SlotID:  "form-question-error-" + question.ID,
		Message: message,
		Files:   files,
	})
}
