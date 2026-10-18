package server

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestStorageUploadContentTypeRewindsTheFile(t *testing.T) {
	t.Parallel()
	content := "%PDF-1.7 " + strings.Repeat("x", 600)
	file := strings.NewReader(content)
	got, err := storageUploadContentType(file, "")
	if err != nil || got != "application/pdf" {
		t.Fatalf("storageUploadContentType = %q, %v; want application/pdf", got, err)
	}
	rest, err := io.ReadAll(file)
	if err != nil || string(rest) != content {
		t.Fatalf("file after sniffing = %d bytes, %v; want all %d", len(rest), err, len(content))
	}
}

func TestStorageUploadContentTypeReadFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		file io.ReadSeeker
	}{
		{name: "read", file: brokenUpload{readErr: errors.New("read failed")}},
		{name: "rewind", file: brokenUpload{seekErr: errors.New("seek failed")}},
	} {
		if _, err := storageUploadContentType(tc.file, "text/plain"); err == nil || err.Error() != "unable to read upload" {
			t.Fatalf("%s: err = %v, want unable to read upload", tc.name, err)
		}
	}
}

type brokenUpload struct {
	readErr error
	seekErr error
}

func (b brokenUpload) Read([]byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return 0, io.EOF
}

func (b brokenUpload) Seek(int64, int) (int64, error) {
	return 0, b.seekErr
}
