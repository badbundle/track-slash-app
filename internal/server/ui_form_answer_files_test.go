package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestCaptureWriterMessages(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"too large":      {http.StatusRequestEntityTooLarge, `{"error":"file too large"}`, "That file is too large: the most is 1.0 KB."},
		"server error":   {http.StatusInternalServerError, `{"error":"internal error"}`, "The file could not be saved. Try again."},
		"no body":        {http.StatusBadRequest, ``, "The file could not be saved. Try again."},
		"its own words":  {http.StatusBadRequest, `{"error":"filename required"}`, "Filename required."},
		"already a stop": {http.StatusBadRequest, `{"error":"Nope."}`, "Nope."},
	} {
		w := newCaptureWriter()
		w.Header().Set("X", "y")
		w.WriteHeader(tc.status)
		_, _ = w.Write([]byte(tc.body))
		if got := w.message(1024); got != tc.want {
			t.Fatalf("%s: message = %q, want %q", name, got, tc.want)
		}
	}
	if uiSentence("  ") != "" {
		t.Fatal("a blank message is not a sentence")
	}
}

func TestUIFormFileMessage(t *testing.T) {
	t.Parallel()
	for err, want := range map[error]string{
		store.ErrFormAnswerFilesFull: "You can attach 6 files to an answer.",
		store.ErrFormFilesFull:       "You can attach 20 files to a form.",
		store.ErrConflict:            "This form is no longer open, so its answers can't change.",
	} {
		if got := uiFormFileMessage(err); got != want {
			t.Fatalf("message for %v = %q, want %q", err, got, want)
		}
	}
	if got := uiFormFileMessage(http.ErrAbortHandler); !strings.Contains(got, "could not be saved") {
		t.Fatalf("message for another error = %q", got)
	}
}

func TestUIFormAnswerFilesLeavesOutUnknownRefs(t *testing.T) {
	t.Parallel()
	lookup := uiFormMediaLookup{"object-1": {Kind: "image", Ref: "object-1", Href: "/a"}}
	data := lookup.answerFiles("/base", "tok", model.FormQuestion{ID: "shot", Label: "Shot"}, []string{"object-1", "object-404"}, true)
	if data.Count != 1 || len(data.Media.Images) != 1 || data.Media.Images[0].Remove != "/base/answers/shot/files/object-1/remove" || data.Full {
		t.Fatalf("answerFiles = %+v", data)
	}
}
