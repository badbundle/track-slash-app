package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateProfileImageThumbnail(t *testing.T) {
	t.Parallel()
	thumbnail, err := generateProfileImageThumbnail(testProfilePNG(t, 4, 2))
	if err != nil {
		t.Fatalf("generateProfileImageThumbnail: %v", err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(thumbnail))
	if err != nil {
		t.Fatalf("DecodeConfig thumbnail: %v", err)
	}
	if format != "png" || cfg.Width != profileThumbnailSize || cfg.Height != profileThumbnailSize {
		t.Fatalf("thumbnail format=%s size=%dx%d, want png %dx%d", format, cfg.Width, cfg.Height, profileThumbnailSize, profileThumbnailSize)
	}
}

func TestGenerateProfileImageThumbnailRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{name: "corrupt", data: []byte("not an image"), want: "could not be decoded"},
		{name: "huge dimensions", data: testProfilePNGConfigOnly(9000, 10, 8), want: "dimensions too large"},
		{name: "too many pixels", data: testProfilePNGConfigOnly(5000, 4000, 8), want: "dimensions too large"},
		// 16 megapixels is allowed, but at 16 bits per channel it decodes
		// to 128 MB.
		{name: "16-bit too large to decode", data: testProfilePNGConfigOnly(4000, 4000, 16), want: "dimensions too large"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := generateProfileImageThumbnail(tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("generateProfileImageThumbnail err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestGenerateProfileImageThumbnailAccepts16BitImages(t *testing.T) {
	t.Parallel()
	img := image.NewNRGBA64(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.NRGBA64{R: 0xffff, A: 0xffff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if _, err := generateProfileImageThumbnail(buf.Bytes()); err != nil {
		t.Fatalf("generateProfileImageThumbnail: %v", err)
	}
}

func TestDecodedBytesPerPixel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		model color.Model
		want  int64
	}{
		{name: "palette", model: color.Palette{color.Black, color.White}, want: 4},
		{name: "gray", model: color.GrayModel, want: 4},
		{name: "YCbCr", model: color.YCbCrModel, want: 4},
		{name: "NRGBA", model: color.NRGBAModel, want: 4},
		{name: "gray16", model: color.Gray16Model, want: 8},
		{name: "RGBA64", model: color.RGBA64Model, want: 8},
	} {
		if got := decodedBytesPerPixel(tc.model); got != tc.want {
			t.Fatalf("decodedBytesPerPixel(%s) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// Decoding waits for one of a few slots. A request that ends while it waits
// is answered as busy rather than decoded.
func TestParseProfileImageUploadWaitsForADecodeSlot(t *testing.T) {
	for range cap(imageDecodeSlots) {
		imageDecodeSlots <- struct{}{}
	}
	defer func() {
		for range cap(imageDecodeSlots) {
			<-imageDecodeSlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := testProfileImageRequest(t, "face.png", testProfilePNG(t, 2, 2)).WithContext(ctx)
	rec := httptest.NewRecorder()
	if _, ok := parseProfileImageUpload(rec, req, 1024); ok {
		t.Fatal("parseProfileImageUpload ok = true with every decode slot taken")
	}
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "busy") {
		t.Fatalf("status=%d body=%s, want 503 busy", rec.Code, rec.Body.String())
	}
}

func TestParseProfileImageUpload(t *testing.T) {
	t.Parallel()
	content := testProfilePNG(t, 2, 2)
	req := testProfileImageRequest(t, "face.png", content)
	rec := httptest.NewRecorder()
	upload, ok := parseProfileImageUpload(rec, req, int64(len(content)+1024))
	if !ok {
		t.Fatalf("parseProfileImageUpload failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if upload.Filename != "face.png" || upload.ContentType != "image/png" || !bytes.Equal(upload.Original, content) {
		t.Fatalf("upload = %+v", upload)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(upload.ThumbnailPNG))
	if err != nil {
		t.Fatalf("DecodeConfig thumbnail: %v", err)
	}
	if format != "png" || cfg.Width != profileThumbnailSize || cfg.Height != profileThumbnailSize {
		t.Fatalf("thumbnail format=%s size=%dx%d, want png %dx%d", format, cfg.Width, cfg.Height, profileThumbnailSize, profileThumbnailSize)
	}
}

func TestParseProfileImageUploadValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		req     *http.Request
		max     int64
		status  int
		message string
	}{
		{
			name:    "multipart required",
			req:     httptest.NewRequest(http.MethodPost, "/api/v1/me/profile-image", strings.NewReader("plain")),
			max:     1024,
			status:  http.StatusBadRequest,
			message: "multipart file required",
		},
		{
			name:    "unsupported type",
			req:     testProfileImageRequest(t, "vector.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)),
			max:     1024,
			status:  http.StatusBadRequest,
			message: "unsupported profile image type",
		},
		{
			name:    "corrupt allowed type",
			req:     testProfileImageRequest(t, "corrupt.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 'b', 'a', 'd'}),
			max:     1024,
			status:  http.StatusBadRequest,
			message: "could not be decoded",
		},
		{
			name:    "file too large",
			req:     testProfileImageRequest(t, "face.png", testProfilePNG(t, 2, 2)),
			max:     8,
			status:  http.StatusRequestEntityTooLarge,
			message: "file too large",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			if _, ok := parseProfileImageUpload(rec, tc.req, tc.max); ok {
				t.Fatalf("parseProfileImageUpload ok = true, want false")
			}
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("status=%d body=%s, want %d containing %q", rec.Code, rec.Body.String(), tc.status, tc.message)
			}
		})
	}
}

func TestProfileImageContentTypeAllowed(t *testing.T) {
	t.Parallel()
	for _, contentType := range []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", " IMAGE/PNG "} {
		if !profileImageContentTypeAllowed(contentType) {
			t.Fatalf("profileImageContentTypeAllowed(%q) = false, want true", contentType)
		}
	}
	for _, contentType := range []string{"image/svg+xml", "image/avif", "text/plain", "application/octet-stream", ""} {
		if profileImageContentTypeAllowed(contentType) {
			t.Fatalf("profileImageContentTypeAllowed(%q) = true, want false", contentType)
		}
	}
}

func testProfilePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(20 + x*30), G: uint8(40 + y*30), B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func testProfileImageRequest(t *testing.T, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/profile-image", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func testProfilePNGConfigOnly(width, height uint32, bitDepth byte) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = bitDepth
	ihdr[9] = 2
	testProfilePNGChunk(&buf, "IHDR", ihdr)
	return buf.Bytes()
}

func testProfilePNGChunk(buf *bytes.Buffer, kind string, data []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	buf.Write(length[:])
	buf.WriteString(kind)
	buf.Write(data)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte(kind))
	_, _ = crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	buf.Write(sum[:])
}
