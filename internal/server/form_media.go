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

// A form can carry files: images, audio, video or anything else. The agent
// that asks uploads them first and names them when it creates the form. An
// agent can't send a file through a tool call, and its shell holds no account
// token, so it uploads through an upload URL that carries its own secret key,
// as a form's wait URL does:
//
//  1. POST .../forms/upload-urls (or track_create_form_upload_url) returns
//     the URL.
//  2. `curl -F file=@a.png "$upload_url"` sends one file per request and
//     gets back its object ref. The file is a pending upload: no generic
//     object route shows it.
//  3. Creating the form claims the pending uploads it names in the questions'
//     `media`, an option's `media` or its `attachments`.
//
// The upload URL buys one thing: putting a file in the creator's pending
// uploads, within the URL's time, file count and size limits. See
// SECURITY_MODEL.md.
const (
	// defaultFormMediaMaxFileBytes is the largest file of form media. It is
	// never more than the storage service allows.
	defaultFormMediaMaxFileBytes = 25 << 20
	// defaultFormUploadGrantTTL is how long an upload URL works.
	defaultFormUploadGrantTTL = 30 * time.Minute
	// defaultFormUploadGrantFiles is how many files an upload URL takes.
	defaultFormUploadGrantFiles = 20

	formUploadRoutePrefix = "/api/v1/form-uploads/"
)

// FormMediaOptions are the limits on form media. Zero values use the
// defaults.
type FormMediaOptions struct {
	// MaxFileBytes is the largest file one upload may carry. It is cut to
	// what the storage service allows.
	MaxFileBytes int64
	// GrantTTL is how long an upload URL works, and GrantMaxFiles how many
	// files it takes.
	GrantTTL      time.Duration
	GrantMaxFiles int
}

func (o FormMediaOptions) withDefaults() FormMediaOptions {
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = defaultFormMediaMaxFileBytes
	}
	if o.GrantTTL <= 0 {
		o.GrantTTL = defaultFormUploadGrantTTL
	}
	if o.GrantMaxFiles <= 0 {
		o.GrantMaxFiles = defaultFormUploadGrantFiles
	}
	return o
}

// formMediaMaxFileBytes is the largest form media file: the setting, and never
// more than the storage service takes.
func (s *Server) formMediaMaxFileBytes() int64 {
	limit := s.formMedia.MaxFileBytes
	if s.objectStorage != nil && s.objectStorage.MaxUploadBytes() < limit {
		limit = s.objectStorage.MaxUploadBytes()
	}
	return limit
}

// newFormUploadKey makes an upload key and the hash the store keeps for it.
func newFormUploadKey() (key string, hash []byte) {
	return newFormWaitKey()
}

func formUploadKeyHash(key string) []byte {
	return formWaitKeyHash(key)
}

// formUploadURL is the link an upload key is handed out as: absolute when the
// server knows its public origin, and otherwise the path alone.
func (s *Server) formUploadURL(key string) string {
	return strings.TrimSuffix(s.publicOrigin, "/") + formUploadRoutePrefix + key
}

// formUploadURLResponse is what creating an upload URL returns. The URL holds
// the key, which the server can't show again.
type formUploadURLResponse struct {
	UploadURL    string    `json:"upload_url"`
	ExpiresAt    time.Time `json:"expires_at"`
	MaxFiles     int       `json:"max_files"`
	MaxFileBytes int64     `json:"max_file_bytes"`
}

// newFormUploadURL makes an upload URL for a member who can write to the
// project. Only the key's hash is stored, so the URL is shown once.
func (s *Server) newFormUploadURL(ctx context.Context, projectID, userID uuid.UUID) (formUploadURLResponse, error) {
	key, hash := newFormUploadKey()
	grant, err := s.store.CreateFormUploadGrant(ctx, store.CreateFormUploadGrantParams{
		ProjectID:   projectID,
		CreatedByID: userID,
		KeyHash:     hash,
		ExpiresAt:   time.Now().Add(s.formMedia.GrantTTL),
		MaxFiles:    s.formMedia.GrantMaxFiles,
	})
	if err != nil {
		return formUploadURLResponse{}, err // defensive: callers resolved the project a moment ago; only a concurrent delete or DB outage fails here
	}
	return formUploadURLResponse{
		UploadURL:    s.formUploadURL(key),
		ExpiresAt:    grant.ExpiresAt,
		MaxFiles:     grant.MaxFiles,
		MaxFileBytes: s.formMediaMaxFileBytes(),
	}, nil
}

// createFormUploadURL is the REST route that makes an upload URL.
func (s *Server) createFormUploadURL(w http.ResponseWriter, r *http.Request) {
	project, permissions, ok := s.formProjectFromRoute(w, r)
	if !ok {
		return
	}
	if !permissions.CanWrite {
		writeForbidden(w)
		return
	}
	created, err := s.newFormUploadURL(r.Context(), project.ID, currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err) // defensive: the project was just resolved; only a concurrent delete or DB outage fails here
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, created)
}

// formUploadResponse is what an upload returns: the ref to name in a form.
type formUploadResponse struct {
	Object      string `json:"object"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
}

func newFormUploadResponse(object model.StorageObject) formUploadResponse {
	return formUploadResponse{
		Object:      object.Ref,
		Filename:    object.Filename,
		ContentType: object.ContentType,
		ByteSize:    object.ByteSize,
	}
}

// uploadFormMedia reads one file from the request into the creator's pending
// uploads, counting it against the grant when there is one.
func (s *Server) uploadFormMedia(w http.ResponseWriter, r *http.Request, projectID, creatorID uuid.UUID, grantID *uuid.UUID) {
	if !s.requireObjectStorage(w) {
		return
	}
	object, ok := s.storeUploadedObject(w, r, projectID, creatorID, s.formMediaMaxFileBytes(),
		func(ctx context.Context, p store.CreateStorageObjectParams) (model.StorageObject, error) {
			return s.store.CreateFormUpload(ctx, store.CreateFormUploadParams{Object: p, GrantID: grantID})
		})
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, newFormUploadResponse(object))
}

// uploadFormMediaWithKey takes a file for an upload URL. The key is the
// credential. The grant is checked before the body is read, so a key that is
// unknown, expired or used up costs the server nothing, and the file counts
// against the grant in the same transaction that records it.
func (s *Server) uploadFormMediaWithKey(w http.ResponseWriter, r *http.Request) {
	grant, err := s.store.GetFormUploadGrant(r.Context(), formUploadKeyHash(chi.URLParam(r, "uploadKey")))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.uploadFormMedia(w, r, grant.ProjectID, grant.CreatedByID, &grant.ID)
}

// uploadFormMediaWithToken takes a file from a member who can write to the
// project, with no upload URL.
func (s *Server) uploadFormMediaWithToken(w http.ResponseWriter, r *http.Request) {
	project, permissions, ok := s.formProjectFromRoute(w, r)
	if !ok {
		return
	}
	if !permissions.CanWrite {
		writeForbidden(w)
		return
	}
	s.uploadFormMedia(w, r, project.ID, currentUser(r).ID, nil)
}

// formAttachmentKind says how a client should show a file: an image the
// browser may show inline, audio, video, or a download.
func formAttachmentKind(object model.StorageObject) string {
	contentType := strings.ToLower(strings.TrimSpace(object.ContentType))
	switch {
	case storageObjectSafeInlineImage(object):
		return "image"
	case strings.HasPrefix(contentType, "audio/"):
		return "audio"
	case strings.HasPrefix(contentType, "video/"):
		return "video"
	default:
		return "file"
	}
}

// formAttachmentPath is a file's content on its form's page, relative to the
// site.
func formAttachmentPath(ownerUsername, projectKey, formRef, objectRef string) string {
	return formPath(ownerUsername, projectKey, formRef) + "/attachments/" + objectRef + "/content"
}

// formAttachmentResponse is a file a form carries, as the API and MCP return
// it. URL is where a member opens it.
type formAttachmentResponse struct {
	Object      string `json:"object"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ByteSize    int64  `json:"byte_size"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
}

func (s *Server) formAttachmentResponses(form model.Form) []formAttachmentResponse {
	out := make([]formAttachmentResponse, len(form.Attachments))
	for i, attachment := range form.Attachments {
		object := attachment.Object
		out[i] = formAttachmentResponse{
			Object:      object.Ref,
			Filename:    object.Filename,
			ContentType: object.ContentType,
			ByteSize:    object.ByteSize,
			Kind:        formAttachmentKind(object),
			URL:         strings.TrimSuffix(s.publicOrigin, "/") + formAttachmentPath(form.OwnerUsername, form.ProjectKey, form.Ref, object.Ref),
		}
	}
	return out
}

// getFormAttachmentContent serves a file of a form to a member of its
// project. The file is looked up on that form only: one that is pending, on
// another form or not attached is not found.
func (s *Server) getFormAttachmentContent(w http.ResponseWriter, r *http.Request) {
	form, _, ok := s.formFromRoute(w, r)
	if !ok {
		return
	}
	number, ok := parseTypedRefParam(w, r, "objectRef", "object")
	if !ok {
		return
	}
	s.serveFormAttachment(w, r, form, number, writeStoreError)
}

// uiGetFormAttachmentContent is getFormAttachmentContent for a signed-in
// browser.
func (s *Server) uiGetFormAttachmentContent(w http.ResponseWriter, r *http.Request) {
	_, _, form, ok := s.uiFormFromRoute(w, r)
	if !ok {
		return
	}
	number, err := parseTypedRef(chi.URLParam(r, "objectRef"), "object")
	if err != nil {
		s.uiNotFound(w, r)
		return
	}
	s.serveFormAttachment(w, r, form, number, writeUIStoreError)
}

func (s *Server) serveFormAttachment(w http.ResponseWriter, r *http.Request, form model.Form, number int, fail func(http.ResponseWriter, error)) {
	attachment, err := s.store.GetFormAttachmentByObjectNumber(r.Context(), form.ID, number)
	if err != nil {
		fail(w, err)
		return
	}
	s.streamStorageObjectContent(w, r, attachment.Object, true)
}
