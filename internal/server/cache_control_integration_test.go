package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Pages for a signed-in viewer and stored files are for one viewer only, so
// no cache may keep them. Shared static assets keep their own caching.
func TestPrivateResponsesAreNotCached(t *testing.T) {
	t.Parallel()
	e, _ := newStorageHTTPEnv(t, 1024*1024)
	_, token := e.mustProjectMemberToken(t, "cache-viewer")
	object := e.mustUploadObject(t, "notes.txt", []byte("private notes"))

	requirePrivateNoStore := func(t *testing.T, name string, res *http.Response) {
		t.Helper()
		defer res.Body.Close()
		if got := res.Header.Get("Cache-Control"); got != "private, no-store" {
			t.Fatalf("%s code = %d Cache-Control = %q, want private, no-store", name, res.StatusCode, got)
		}
	}

	page := e.uiDoNoRedirect(t, http.MethodGet, "/me", token, nil)
	if page.StatusCode != http.StatusOK {
		t.Fatalf("GET /me code = %d", page.StatusCode)
	}
	requirePrivateNoStore(t, "signed-in page", page)

	missing := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, "/no/such/page/here", token, nil, map[string]string{"Accept": "text/html"})
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing page code = %d", missing.StatusCode)
	}
	requirePrivateNoStore(t, "signed-in error page", missing)

	download := e.getObjectContent(t, object)
	if download.StatusCode != http.StatusOK {
		t.Fatalf("download code = %d", download.StatusCode)
	}
	requirePrivateNoStore(t, "download", download)

	// A public project's files are still served through access checks, such
	// as a project block, so they are not cached either.
	e.makeProjectPublic(t)
	anonymous, _ := e.doRaw(t, "", http.MethodGet, e.storageObjectPath(object)+"/content", nil, "")
	if anonymous.StatusCode != http.StatusOK {
		t.Fatalf("anonymous download code = %d", anonymous.StatusCode)
	}
	requirePrivateNoStore(t, "anonymous download", anonymous)

	asset := e.uiDoNoRedirect(t, http.MethodGet, "/static/app.css", token, nil)
	defer asset.Body.Close()
	if asset.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/app.css code = %d", asset.StatusCode)
	}
	if got := asset.Header.Get("Cache-Control"); strings.Contains(got, "no-store") {
		t.Fatalf("static asset Cache-Control = %q, want caching left as it was", got)
	}
}
