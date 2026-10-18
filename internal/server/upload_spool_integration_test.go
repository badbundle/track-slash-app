package server_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// An upload larger than the multipart memory limit is spooled to a temp file
// rather than held in memory, and the temp file is removed with the request.
// Not parallel: it points TMPDIR at a directory of its own.
func TestHTTPLargeUploadsSpoolToTempFilesAndCleanUp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	e, _ := newStorageHTTPEnv(t, 8<<20)

	var face bytes.Buffer
	gray := image.NewGray(image.Rect(0, 0, 4, 4))
	gray.SetGray(1, 1, color.Gray{Y: 128})
	if err := png.Encode(&face, gray); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	// A PNG decoder stops at the image end, so padding keeps it valid.
	paddedFace := append(face.Bytes(), make([]byte, 3<<20)...)

	for _, tc := range []struct {
		name     string
		path     string
		filename string
		content  []byte
		want     int
	}{
		{name: "object", path: e.projectObjectsPath(), filename: "large.bin", content: bytes.Repeat([]byte("x"), 3<<20), want: http.StatusCreated},
		{name: "profile image", path: "/me/profile-image", filename: "face.png", content: paddedFace, want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body, spooled := e.postMultipartWatchingTempDir(t, tc.path, tc.filename, tc.content, tmp)
			if code != tc.want {
				t.Fatalf("upload code = %d body = %s", code, body)
			}
			if !spooled {
				t.Fatal("upload was never spooled to a temp file")
			}
			if left := multipartTempFiles(t, tmp); len(left) != 0 {
				t.Fatalf("temp files left after the request: %v", left)
			}
		})
	}
}

// postMultipartWatchingTempDir uploads content through a pipe and, once the
// first 2 MiB of it are sent, reports whether a multipart temp file appears
// in dir before the rest follows.
func (e *httpEnv) postMultipartWatchingTempDir(t *testing.T, path, filename string, content []byte, dir string) (int, []byte, bool) {
	t.Helper()
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+apiPath(path), reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.authToken)

	type result struct {
		res *http.Response
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := e.ts.Client().Do(req)
		done <- result{res: res, err: err}
	}()

	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	head, rest := content[:2<<20], content[2<<20:]
	if _, err := part.Write(head); err != nil {
		t.Fatalf("write head: %v", err)
	}
	spooled := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if len(multipartTempFiles(t, dir)) > 0 {
			spooled = true
			break
		}
	}
	if _, err := part.Write(rest); err != nil {
		t.Fatalf("write rest: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("form.Close: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	got := <-done
	if got.err != nil {
		t.Fatalf("upload: %v", got.err)
	}
	defer got.res.Body.Close()
	body, err := io.ReadAll(got.res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return got.res.StatusCode, body, spooled
}

func multipartTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "multipart-") {
			names = append(names, entry.Name())
		}
	}
	return names
}
