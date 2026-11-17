package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// The person answering a form can attach files to an answer, and an agent can
// fetch them.
//
// Attaching or removing a file is answering, so it follows "A person answers a
// form" in SECURITY_MODEL.md: only the form's assignee, only while the form is
// open, and never a connected application. There is no MCP tool for it.
// Answer files are form files: members read them through the form's content
// routes and nowhere else.
//
// An agent's shell holds no account token, so it fetches a file through a
// download URL, as it uploads through an upload URL:
//
//  1. POST .../forms/{formRef}/attachments/{objectRef}/url (or
//     track_create_form_file_url), by any member who can read the form,
//     returns the URL.
//  2. `curl -sS -o FILE "$download_url"` saves the file.
//
// The URL carries its own secret key, serves that one file, only to a GET, for
// ten minutes, and stands for the member who made it: it stops working when
// they can no longer read the form. Only the key's hash is stored, and the
// request log leaves the key out. See SECURITY_MODEL.md.

// formFileURL is the link a download key is handed out as: absolute when the
// server knows its public origin, and otherwise the path alone.
func (s *Server) formFileURL(key string) string {
	return strings.TrimSuffix(s.publicOrigin, "/") + formFileRoutePrefix + key
}

// formFileURLResponse is what creating a download URL returns. The URL holds
// the key, which the server can't show again.
type formFileURLResponse struct {
	DownloadURL string    `json:"download_url"`
	ExpiresAt   time.Time `json:"expires_at"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	ByteSize    int64     `json:"byte_size"`
}

// newFormFileURL makes a download URL for a file of a form, on behalf of a
// member who can read the form.
func (s *Server) newFormFileURL(ctx context.Context, form model.Form, number int, userID uuid.UUID) (formFileURLResponse, error) {
	attachment, err := s.store.GetFormAttachmentByObjectNumber(ctx, form.ID, number)
	if err != nil {
		return formFileURLResponse{}, err
	}
	key, hash := newFormWaitKey()
	grant, err := s.store.CreateFormFileGrant(ctx, store.CreateFormFileGrantParams{
		FormID:          form.ID,
		StorageObjectID: attachment.StorageObjectID,
		CreatedByID:     userID,
		KeyHash:         hash,
		ExpiresAt:       time.Now().Add(s.formMedia.FileURLTTL),
	})
	if err != nil {
		return formFileURLResponse{}, err // the file was just read: only a concurrent removal or a DB outage fails here
	}
	object := attachment.Object
	return formFileURLResponse{
		DownloadURL: s.formFileURL(key),
		ExpiresAt:   grant.ExpiresAt,
		Filename:    object.Filename,
		ContentType: object.ContentType,
		ByteSize:    object.ByteSize,
	}, nil
}

// createFormFileURL is the REST route that makes a download URL. Reading is
// all it takes, so a connected application may call it.
func (s *Server) createFormFileURL(w http.ResponseWriter, r *http.Request) {
	form, _, ok := s.formFromRoute(w, r)
	if !ok {
		return
	}
	number, ok := parseTypedRefParam(w, r, "objectRef", "object")
	if !ok {
		return
	}
	created, err := s.newFormFileURL(r.Context(), form, number, currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, created)
}

// getFormFileWithKey serves the one file a download URL stands for. It takes
// no account credentials: the key in the path is the credential. A key that is
// unknown, expired, or whose creator can no longer read the form is not found,
// with the body of any missing route.
func (s *Server) getFormFileWithKey(w http.ResponseWriter, r *http.Request) {
	grant, err := s.store.GetFormFileGrant(r.Context(), formWaitKeyHash(chi.URLParam(r, "fileKey")))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.streamFormMediaContent(w, r, grant.Object)
}

// formAnswerFileResponse is what attaching a file returns.
type formAnswerFileResponse struct {
	Object      string `json:"object"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
	Kind        string `json:"kind"`
}

func newFormAnswerFileResponse(object model.StorageObject) formAnswerFileResponse {
	return formAnswerFileResponse{
		Object:      object.Ref,
		Filename:    object.Filename,
		ContentType: object.ContentType,
		ByteSize:    object.ByteSize,
		Kind:        formAttachmentKind(object),
	}
}

// attachFormAnswerFile is the REST route that attaches one file to the
// person's answer to a question.
func (s *Server) attachFormAnswerFile(w http.ResponseWriter, r *http.Request) {
	form, ok := s.answerableFormFromRoute(w, r)
	if !ok {
		return
	}
	questionID := chi.URLParam(r, "questionID")
	// Refuse before the body is read when the file could not be taken anyway.
	if err := store.CheckFormAnswerFileAttach(form, questionID); err != nil {
		writeFormError(w, err)
		return
	}
	if !s.requireObjectStorage(w) {
		return
	}
	var attached model.FormAttachment
	_, ok = s.storeUploadedObject(w, r, form.ProjectID, currentUser(r).ID, s.formMediaMaxFileBytes(), formMediaDeclaredType,
		func(ctx context.Context, p store.CreateStorageObjectParams) (model.StorageObject, error) {
			var err error
			_, attached, err = s.store.AttachFormAnswerFile(ctx, store.AttachFormAnswerFileParams{
				FormID: form.ID, AssigneeID: currentUser(r).ID, QuestionID: questionID, Object: p,
			})
			return attached.Object, err
		})
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, newFormAnswerFileResponse(attached.Object))
}

// removeFormAnswerFile is the REST route that removes a file from the person's
// answer.
func (s *Server) removeFormAnswerFile(w http.ResponseWriter, r *http.Request) {
	form, ok := s.answerableFormFromRoute(w, r)
	if !ok {
		return
	}
	number, ok := parseTypedRefParam(w, r, "objectRef", "object")
	if !ok {
		return
	}
	if _, err := s.store.RemoveFormAnswerFile(r.Context(), form.ID, currentUser(r).ID, chi.URLParam(r, "questionID"), number); err != nil {
		writeFormError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
