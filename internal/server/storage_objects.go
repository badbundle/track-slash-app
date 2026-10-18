package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
	"github.com/bradleymackey/track-slash/internal/store"
)

const (
	maxStorageObjectFilenameLength    = 255
	maxStorageObjectContentTypeLength = 255
	storageMultipartOverheadBytes     = 1024 * 1024
	storageCleanupTimeout             = 5 * time.Second
)

func (s *Server) createStorageObject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	if !s.requireObjectStorage(w) {
		return
	}
	created, ok := s.createStorageObjectFromRequest(w, r, project.ID, currentUser(r).ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) createStorageObjectFromRequest(w http.ResponseWriter, r *http.Request, projectID, userID uuid.UUID) (model.StorageObject, bool) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		writeError(w, http.StatusBadRequest, "multipart file required")
		return model.StorageObject{}, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.objectStorage.MaxUploadBytes()+storageMultipartOverheadBytes)
	if err := r.ParseMultipartForm(s.objectStorage.MaxUploadBytes() + storageMultipartOverheadBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return model.StorageObject{}, false
		}
		writeError(w, http.StatusBadRequest, "unable to read upload")
		return model.StorageObject{}, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file required")
		return model.StorageObject{}, false
	}
	defer file.Close()

	filename, err := normalizeStorageObjectFilename(header.Filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return model.StorageObject{}, false
	}
	contentType, body, err := storageUploadContentTypeAndBody(file, header.Header.Get("Content-Type"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return model.StorageObject{}, false
	}

	objectID := uuid.New()
	stored, err := s.objectStorage.Put(r.Context(), projectID, objectID, body)
	if err != nil {
		writeStorageError(w, err)
		return model.StorageObject{}, false
	}
	created, err := s.store.CreateStorageObject(r.Context(), store.CreateStorageObjectParams{
		ID:          objectID,
		ProjectID:   projectID,
		Backend:     stored.Backend,
		Bucket:      stored.Bucket,
		ObjectKey:   stored.ObjectKey,
		Filename:    filename,
		ContentType: contentType,
		ByteSize:    stored.ByteSize,
		SHA256:      stored.SHA256,
		CreatedByID: userID,
	})
	if err != nil {
		_ = s.deleteStorageBackendObject(r.Context(), stored.ObjectKey)
		writeStoreError(w, err)
		return model.StorageObject{}, false
	}
	return created, true
}

func (s *Server) listStorageObjects(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	permissions, ok := s.requireProjectReadPermissions(w, r, project.ID)
	if !ok {
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var cursor *store.StorageObjectsCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.StorageObjectsCursor
		if err := decodeCursor(raw, &c); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cursor = &c
	}
	out, hasMore, err := s.store.ListStorageObjects(r.Context(), store.ListStorageObjectsParams{
		ProjectID:                   project.ID,
		Cursor:                      cursor,
		Limit:                       limit,
		HideMembersOnlyIssueObjects: !permissions.CanReadMembersOnly,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var next *string
	if hasMore {
		last := out[len(out)-1]
		enc := encodeCursor(store.StorageObjectsCursor{Number: last.Number})
		next = &enc
	}
	writePage(w, out, next)
}

func (s *Server) getStorageObject(w http.ResponseWriter, r *http.Request) {
	_, object, ok := s.storageObjectFromRoute(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) getStorageObjectContent(w http.ResponseWriter, r *http.Request) {
	_, object, ok := s.storageObjectFromRoute(w, r)
	if !ok {
		return
	}
	s.streamStorageObjectContent(w, r, object, false)
}

func (s *Server) streamStorageObjectContent(w http.ResponseWriter, r *http.Request, object model.StorageObject, inline bool) {
	if !s.requireObjectStorage(w) {
		return
	}
	// Downloads have no request deadline (see isDownloadRequest). Instead,
	// opening the object and each read from it must finish within the idle
	// timeout, or idle cancels the backend's context.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	idle := time.AfterFunc(s.downloadIdleTimeout, cancel)
	defer idle.Stop()
	body, err := s.objectStorage.Open(ctx, object.ObjectKey)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	defer body.Close()

	// Uploads are served from the app's own origin, so only the safe inline
	// image types keep their declared type. Anything else goes out as opaque
	// bytes in a sandbox with no script or style, so a file declared as script
	// or a page cannot be loaded as one from here.
	contentType := "application/octet-stream"
	disposition := "attachment"
	if storageObjectSafeInlineImage(object) {
		contentType = object.ContentType
		if inline {
			disposition = "inline"
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.ByteSize, 10))
	w.Header().Set("ETag", `"`+object.SHA256+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Added beside the site-wide policy, not in place of it: a browser
	// enforces every policy it is given, so this only narrows it.
	w.Header().Add("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": object.Filename}))
	w.WriteHeader(http.StatusOK)
	copyWithIdleTimeout(w, body, idle, s.downloadIdleTimeout)
}

// copyWithIdleTimeout copies body to w. Each read from body must return
// within timeout, or idle fires and cancels it; each write must finish within
// timeout, or its write deadline fails it.
func copyWithIdleTimeout(w http.ResponseWriter, body io.Reader, idle *time.Timer, timeout time.Duration) {
	controller := http.NewResponseController(w)
	// The server sets no write timeout of its own, so a deadline left here
	// would carry over to the next request on the connection.
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	buf := make([]byte, 32*1024)
	for {
		idle.Reset(timeout)
		n, err := body.Read(buf)
		idle.Stop()
		if n > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(timeout))
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) cleanupStorageObject(ctx context.Context, object model.StorageObject) {
	cleanupCtx, cancel := storageCleanupContext(ctx)
	defer cancel()

	deleted, err := s.store.DeleteStorageObject(cleanupCtx, object.ID)
	if err == nil {
		_ = s.objectStorage.Delete(cleanupCtx, deleted.ObjectKey)
		return
	}
	_ = s.objectStorage.Delete(cleanupCtx, object.ObjectKey)
}

func storageCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), storageCleanupTimeout)
}

// deleteStorageBackendObject deletes bytes no row was ever written for, such
// as an upload whose metadata insert failed. Nothing can share them. For an
// object that had a row, use deleteRemovedStorageBytes.
func (s *Server) deleteStorageBackendObject(parent context.Context, objectKey string) error {
	cleanupCtx, cancel := storageCleanupContext(parent)
	defer cancel()
	return s.objectStorage.Delete(cleanupCtx, objectKey)
}

// deleteRemovedStorageBytes is the immediate, best-effort backend delete after
// an object's row is soft-deleted. It leaves bytes another live object still
// uses, such as a repeating issue's attachment copy. When it can't tell, it
// leaves them to the deletion worker the soft-delete queued, which checks
// again.
func (s *Server) deleteRemovedStorageBytes(parent context.Context, objectKey string) error {
	cleanupCtx, cancel := storageCleanupContext(parent)
	defer cancel()
	inUse, err := s.store.StorageObjectBytesInUse(cleanupCtx, s.objectStorage.BackendName(), s.objectStorage.Bucket(), objectKey)
	if err != nil || inUse {
		return nil
	}
	return s.objectStorage.Delete(cleanupCtx, objectKey)
}

func (s *Server) deleteStorageObject(w http.ResponseWriter, r *http.Request) {
	project, object, ok := s.storageObjectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	deleted, err := s.store.DeleteStorageObject(r.Context(), object.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if s.objectStorage != nil {
		_ = s.deleteRemovedStorageBytes(r.Context(), deleted.ObjectKey)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) storageObjectFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, model.StorageObject, bool) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return model.Project{}, model.StorageObject{}, false
	}
	permissions, ok := s.requireProjectReadPermissions(w, r, project.ID)
	if !ok {
		return model.Project{}, model.StorageObject{}, false
	}
	number, ok := parseTypedRefParam(w, r, "objectRef", "object")
	if !ok {
		return model.Project{}, model.StorageObject{}, false
	}
	// Deleted issues, and the files only they hold, are for members.
	object, err := s.store.GetVisibleStorageObjectByProjectNumber(r.Context(), project.ID, number, !permissions.CanReadMembersOnly)
	if err != nil {
		writeStoreError(w, err)
		return model.Project{}, model.StorageObject{}, false
	}
	return project, object, true
}

func (s *Server) requireObjectStorage(w http.ResponseWriter) bool {
	if s.objectStorage == nil {
		writeError(w, http.StatusServiceUnavailable, "object storage unavailable")
		return false
	}
	return true
}

func writeStorageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, objectstorage.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, objectstorage.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
	case errors.Is(err, objectstorage.ErrExists), errors.Is(err, objectstorage.ErrInvalidKey):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeInternalError(w, "object storage", err)
	}
}

func normalizeStorageObjectFilename(raw string) (string, error) {
	filename := strings.ReplaceAll(raw, "\\", "/")
	filename = path.Base(filename)
	filename = strings.TrimSpace(filename)
	filename = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, filename)
	filename = strings.TrimSpace(filename)
	if filename == "" || filename == "." || filename == ".." {
		return "", errors.New("filename required")
	}
	if utf8.RuneCountInString(filename) > maxStorageObjectFilenameLength {
		return "", errors.New("filename max 255 chars")
	}
	return filename, nil
}

func normalizeStorageObjectContentType(raw string, sample []byte) string {
	mediaType := strings.TrimSpace(raw)
	if mediaType == "" || strings.EqualFold(mediaType, "application/octet-stream") {
		mediaType = http.DetectContentType(sample)
	}
	parsed, _, err := mime.ParseMediaType(mediaType)
	if err != nil || parsed == "" {
		return "application/octet-stream"
	}
	mediaType = strings.ToLower(parsed)
	if len(mediaType) > maxStorageObjectContentTypeLength {
		return "application/octet-stream"
	}
	return mediaType
}

func storageUploadContentTypeAndBody(file io.Reader, rawContentType string) (string, io.Reader, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", nil, errors.New("unable to read upload")
	}
	head = head[:n]
	return normalizeStorageObjectContentType(rawContentType, head), io.MultiReader(bytes.NewReader(head), file), nil
}

func storageObjectSafeInlineImage(object model.StorageObject) bool {
	switch strings.ToLower(strings.TrimSpace(object.ContentType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp":
		return true
	default:
		return false
	}
}
