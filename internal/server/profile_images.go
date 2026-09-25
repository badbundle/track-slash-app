package server

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/bradleymackey/track-slash/internal/model"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
	"github.com/bradleymackey/track-slash/internal/store"
)

const (
	profileThumbnailSize         = 128
	maxProfileImageDimension     = 8192
	maxProfileImagePixelCount    = 25_000_000
	profileImageOriginalVariant  = "original"
	profileImageThumbnailVariant = "thumbnail"
)

type profileImageUpload struct {
	Filename     string
	ContentType  string
	Original     []byte
	ThumbnailPNG []byte
}

func (s *Server) createMyProfileImage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.replaceProfileImageForCurrentUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) deleteMyProfileImage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.removeProfileImageForCurrentUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) getUserProfileImageContent(w http.ResponseWriter, r *http.Request) {
	s.getUserProfileImageContentVariant(w, r, false)
}

func (s *Server) getUserProfileImageThumbnailContent(w http.ResponseWriter, r *http.Request) {
	s.getUserProfileImageContentVariant(w, r, true)
}

func (s *Server) uiUpdateProfileImage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.replaceProfileImageForCurrentUser(w, r)
	if !ok {
		return
	}
	s.renderUIProfile(w, r, user, "", true)
}

func (s *Server) uiDeleteProfileImage(w http.ResponseWriter, r *http.Request) {
	user, ok := s.removeProfileImageForCurrentUser(w, r)
	if !ok {
		return
	}
	s.renderUIProfile(w, r, user, "", true)
}

func (s *Server) uiGetUserProfileImageContent(w http.ResponseWriter, r *http.Request) {
	s.getUserProfileImageContentVariant(w, r, false)
}

func (s *Server) uiGetUserProfileImageThumbnailContent(w http.ResponseWriter, r *http.Request) {
	s.getUserProfileImageContentVariant(w, r, true)
}

func (s *Server) replaceProfileImageForCurrentUser(w http.ResponseWriter, r *http.Request) (model.User, bool) {
	if !s.requireObjectStorage(w) {
		return model.User{}, false
	}
	upload, ok := parseProfileImageUpload(w, r, s.objectStorage.MaxUploadBytes())
	if !ok {
		return model.User{}, false
	}
	original, thumbnail, ok := s.createProfileImageStorageObjects(w, r, currentUser(r).ID, upload)
	if !ok {
		return model.User{}, false
	}
	replaced, err := s.store.ReplaceUserProfileImage(r.Context(), currentUser(r).ID, original.ID, thumbnail.ID)
	if err != nil {
		s.cleanupStorageObject(r.Context(), original)
		s.cleanupStorageObject(r.Context(), thumbnail)
		writeStoreError(w, err)
		return model.User{}, false
	}
	s.deleteBackendObjectsBestEffort(r, replaced.DeletedObjects)
	return replaced.User, true
}

func (s *Server) removeProfileImageForCurrentUser(w http.ResponseWriter, r *http.Request) (model.User, bool) {
	removed, err := s.store.RemoveUserProfileImage(r.Context(), currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return model.User{}, false
	}
	s.deleteBackendObjectsBestEffort(r, removed.DeletedObjects)
	return removed.User, true
}

func (s *Server) getUserProfileImageContentVariant(w http.ResponseWriter, r *http.Request, thumbnail bool) {
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	object, err := s.store.GetUserProfileImageObject(r.Context(), userID, thumbnail)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.streamStorageObjectContent(w, r, object, true)
}

func parseProfileImageUpload(w http.ResponseWriter, r *http.Request, maxUploadBytes int64) (profileImageUpload, bool) {
	return parseSquareImageUpload(w, r, maxUploadBytes, "profile")
}

func parseProjectImageUpload(w http.ResponseWriter, r *http.Request, maxUploadBytes int64) (profileImageUpload, bool) {
	return parseSquareImageUpload(w, r, maxUploadBytes, "project")
}

func parseSquareImageUpload(w http.ResponseWriter, r *http.Request, maxUploadBytes int64, imageKind string) (profileImageUpload, bool) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		writeError(w, http.StatusBadRequest, "multipart file required")
		return profileImageUpload{}, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+storageMultipartOverheadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes + storageMultipartOverheadBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return profileImageUpload{}, false
		}
		writeError(w, http.StatusBadRequest, "unable to read upload")
		return profileImageUpload{}, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file required")
		return profileImageUpload{}, false
	}
	defer file.Close()

	filename, err := normalizeStorageObjectFilename(header.Filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return profileImageUpload{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "unable to read upload")
		return profileImageUpload{}, false
	}
	if int64(len(data)) > maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return profileImageUpload{}, false
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "file required")
		return profileImageUpload{}, false
	}

	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	mediaType := normalizeStorageObjectContentType(header.Header.Get("Content-Type"), head)
	if !profileImageContentTypeAllowed(mediaType) {
		writeError(w, http.StatusBadRequest, "unsupported "+imageKind+" image type")
		return profileImageUpload{}, false
	}

	thumbnail, err := generateSquareImageThumbnail(data, imageKind)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return profileImageUpload{}, false
	}
	return profileImageUpload{
		Filename:     filename,
		ContentType:  mediaType,
		Original:     data,
		ThumbnailPNG: thumbnail,
	}, true
}

func (s *Server) createProfileImageStorageObjects(w http.ResponseWriter, r *http.Request, userID uuid.UUID, upload profileImageUpload) (model.StorageObject, model.StorageObject, bool) {
	originalID := uuid.New()
	storedOriginal, err := s.objectStorage.PutUserProfileImage(r.Context(), userID, originalID, profileImageOriginalVariant, bytes.NewReader(upload.Original))
	if err != nil {
		writeStorageError(w, err)
		return model.StorageObject{}, model.StorageObject{}, false
	}
	original, err := s.store.CreateUserStorageObject(r.Context(), store.CreateUserStorageObjectParams{
		ID:          originalID,
		OwnerUserID: userID,
		Backend:     storedOriginal.Backend,
		Bucket:      storedOriginal.Bucket,
		ObjectKey:   storedOriginal.ObjectKey,
		Filename:    upload.Filename,
		ContentType: upload.ContentType,
		ByteSize:    storedOriginal.ByteSize,
		SHA256:      storedOriginal.SHA256,
		CreatedByID: userID,
	})
	if err != nil {
		_ = s.deleteStorageBackendObject(r.Context(), storedOriginal.ObjectKey)
		writeStoreError(w, err)
		return model.StorageObject{}, model.StorageObject{}, false
	}

	thumbnailID := uuid.New()
	storedThumbnail, err := s.objectStorage.PutUserProfileImage(r.Context(), userID, thumbnailID, profileImageThumbnailVariant, bytes.NewReader(upload.ThumbnailPNG))
	if err != nil {
		s.cleanupStorageObject(r.Context(), original)
		writeStorageError(w, err)
		return model.StorageObject{}, model.StorageObject{}, false
	}
	thumbnail, err := s.store.CreateUserStorageObject(r.Context(), store.CreateUserStorageObjectParams{
		ID:          thumbnailID,
		OwnerUserID: userID,
		Backend:     storedThumbnail.Backend,
		Bucket:      storedThumbnail.Bucket,
		ObjectKey:   storedThumbnail.ObjectKey,
		Filename:    "profile-thumbnail.png",
		ContentType: "image/png",
		ByteSize:    storedThumbnail.ByteSize,
		SHA256:      storedThumbnail.SHA256,
		CreatedByID: userID,
	})
	if err != nil {
		s.cleanupStorageObject(r.Context(), original)
		_ = s.deleteStorageBackendObject(r.Context(), storedThumbnail.ObjectKey)
		writeStoreError(w, err)
		return model.StorageObject{}, model.StorageObject{}, false
	}
	return original, thumbnail, true
}

func (s *Server) deleteBackendObjectsBestEffort(r *http.Request, objects []model.StorageObject) {
	if s.objectStorage == nil {
		return
	}
	for _, object := range objects {
		if object.ObjectKey == "" {
			continue
		}
		if err := s.deleteStorageBackendObject(r.Context(), object.ObjectKey); err != nil && !errors.Is(err, objectstorage.ErrNotFound) {
			continue
		}
	}
}

func profileImageContentTypeAllowed(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
		return true
	default:
		return false
	}
}

func generateProfileImageThumbnail(data []byte) ([]byte, error) {
	return generateSquareImageThumbnail(data, "profile")
}

func generateSquareImageThumbnail(data []byte, imageKind string) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New(imageKind + " image could not be decoded")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, errors.New(imageKind + " image dimensions required")
	}
	if cfg.Width > maxProfileImageDimension || cfg.Height > maxProfileImageDimension || int64(cfg.Width)*int64(cfg.Height) > maxProfileImagePixelCount {
		return nil, errors.New(imageKind + " image dimensions too large")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New(imageKind + " image could not be decoded")
	}
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	side := width
	if height < side {
		side = height
	}
	if side <= 0 {
		return nil, errors.New(imageKind + " image dimensions required")
	}
	srcX := bounds.Min.X + (width-side)/2
	srcY := bounds.Min.Y + (height-side)/2
	srcRect := image.Rect(srcX, srcY, srcX+side, srcY+side)
	dst := image.NewNRGBA(image.Rect(0, 0, profileThumbnailSize, profileThumbnailSize))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, srcRect, draw.Over, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, errors.New(imageKind + " image thumbnail failed")
	}
	return out.Bytes(), nil
}
