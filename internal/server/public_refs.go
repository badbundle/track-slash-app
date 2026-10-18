package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type issueRef struct {
	ProjectKey string
	Number     int
}

func normalizeOwnerParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner, err := store.NormalizeUsername(chi.URLParam(r, "owner"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return owner, true
}

func normalizeProjectKeyParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "key")))
	if !projectKeyRe.MatchString(key) {
		writeError(w, http.StatusBadRequest, "invalid project key")
		return "", false
	}
	return key, true
}

func parseIssueRef(raw string) (issueRef, error) {
	raw = strings.TrimSpace(raw)
	key, numberRaw, ok := strings.Cut(raw, "-")
	if !ok {
		return issueRef{}, fmt.Errorf("issue ref must look like KEY-123")
	}
	key = strings.ToUpper(strings.TrimSpace(key))
	if !projectKeyRe.MatchString(key) {
		return issueRef{}, fmt.Errorf("invalid issue project key")
	}
	// Ref numbers are INT columns, so one past int32 can't name anything.
	n, err := strconv.ParseInt(strings.TrimSpace(numberRaw), 10, 32)
	if err != nil || n < 1 {
		return issueRef{}, fmt.Errorf("invalid issue number")
	}
	return issueRef{ProjectKey: key, Number: int(n)}, nil
}

func requireIssueRefProject(ref issueRef, projectKey string) error {
	if ref.ProjectKey != strings.ToUpper(strings.TrimSpace(projectKey)) {
		return fmt.Errorf("issues belong to different projects: %w", store.ErrConflict)
	}
	return nil
}

func parseIssueRefParam(w http.ResponseWriter, r *http.Request) (issueRef, bool) {
	ref, err := parseIssueRef(chi.URLParam(r, "issueRef"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return issueRef{}, false
	}
	return ref, true
}

func parseTypedRef(raw, prefix string) (int, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	want := prefix + "-"
	if !strings.HasPrefix(raw, want) {
		return 0, fmt.Errorf("%s ref must look like %s1", prefix, want)
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(raw, want), 10, 32)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid %s number", prefix)
	}
	return int(n), nil
}

func parseTypedRefParam(w http.ResponseWriter, r *http.Request, param, prefix string) (int, bool) {
	n, err := parseTypedRef(chi.URLParam(r, param), prefix)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return 0, false
	}
	return n, true
}

func (s *Server) projectFromRoute(w http.ResponseWriter, r *http.Request) (model.Project, bool) {
	owner, ok := normalizeOwnerParam(w, r)
	if !ok {
		return model.Project{}, false
	}
	key, ok := normalizeProjectKeyParam(w, r)
	if !ok {
		return model.Project{}, false
	}
	project, err := s.store.GetProjectByOwnerKey(r.Context(), owner, key)
	if err != nil {
		writeStoreError(w, err)
		return model.Project{}, false
	}
	return project, true
}

// issueFromRoute resolves the route's issue for handlers that serve only
// people who can read it. A reporter who only follows the issue is refused
// here, before a project-level check could let them through to a private
// issue's details.
func (s *Server) issueFromRoute(w http.ResponseWriter, r *http.Request) (model.Issue, bool) {
	issue, permissions, ok := s.issueWithAccessFromRoute(w, r)
	if ok && !permissions.CanRead {
		writeForbidden(w)
		return model.Issue{}, false
	}
	return issue, ok
}

// issueWithAccessFromRoute resolves the route's issue after issueRouteAccess,
// returning the permissions that check read so handlers need not repeat it.
func (s *Server) issueWithAccessFromRoute(w http.ResponseWriter, r *http.Request) (model.Issue, store.ProjectPermissions, bool) {
	owner, ok := normalizeOwnerParam(w, r)
	if !ok {
		return model.Issue{}, store.ProjectPermissions{}, false
	}
	ref, ok := parseIssueRefParam(w, r)
	if !ok {
		return model.Issue{}, store.ProjectPermissions{}, false
	}
	issue, err := s.store.GetIssueByOwnerKeyNumber(r.Context(), owner, ref.ProjectKey, ref.Number)
	if err != nil {
		writeStoreError(w, err)
		return model.Issue{}, store.ProjectPermissions{}, false
	}
	permissions, err := s.issueRouteAccess(r.Context(), currentUser(r), issue)
	if err != nil {
		writeIssueRouteError(w, err)
		return model.Issue{}, store.ProjectPermissions{}, false
	}
	return issue, permissions, true
}

func writeIssueRouteError(w http.ResponseWriter, err error) {
	if errors.Is(err, errIssueRouteForbidden) {
		writeForbidden(w)
		return
	}
	writeStoreError(w, err)
}

func (s *Server) deletedIssueFromRoute(w http.ResponseWriter, r *http.Request) (model.Issue, bool) {
	owner, ok := normalizeOwnerParam(w, r)
	if !ok {
		return model.Issue{}, false
	}
	ref, ok := parseIssueRefParam(w, r)
	if !ok {
		return model.Issue{}, false
	}
	issue, err := s.store.GetDeletedIssueByOwnerKeyNumber(r.Context(), owner, ref.ProjectKey, ref.Number)
	if err != nil {
		writeStoreError(w, err)
		return model.Issue{}, false
	}
	if _, err := s.deletedIssueRouteAccess(r.Context(), currentUser(r), issue); err != nil {
		writeIssueRouteError(w, err)
		return model.Issue{}, false
	}
	return issue, true
}
