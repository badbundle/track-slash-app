package server_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type projectResponseDecoded struct {
	model.Project
	Favorite bool `json:"favorite"`
}

func TestHTTPUpdateProject(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	code, body := e.do(t, http.MethodPatch, e.projectPath(), map[string]any{
		"name":        "Renamed API Project",
		"description": "updated through API",
	})
	if code != http.StatusOK {
		t.Fatalf("update code = %d body = %s", code, body)
	}
	project := decode[model.Project](t, body)
	if project.Name != "Renamed API Project" || project.Description != "updated through API" {
		t.Fatalf("project = %+v", project)
	}
	entries, _, err := e.store.ListProjectChangelog(e.ctx, store.ListProjectChangelogParams{
		ProjectID: e.projectID,
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("ListProjectChangelog: %v", err)
	}
	if len(entries) == 0 || entries[0].Entity != "project" || entries[0].Op != "update" || entries[0].TargetRef != e.projKey {
		t.Fatalf("missing update changelog: %+v", entries)
	}

	code, body = e.do(t, http.MethodPatch, e.projectPath(), map[string]any{"name": " "})
	if code != http.StatusBadRequest || !strings.Contains(string(body), "name must be 1..200 chars") {
		t.Fatalf("bad name code = %d body = %s", code, body)
	}
	got, err := e.store.GetProject(e.ctx, e.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Name != "Renamed API Project" {
		t.Fatalf("bad name changed project: %+v", got)
	}

	code, body = e.do(t, http.MethodPatch, e.projectPath(), map[string]any{"description": "   "})
	if code != http.StatusOK {
		t.Fatalf("clear description code = %d body = %s", code, body)
	}
	project = decode[model.Project](t, body)
	if project.Description != "" {
		t.Fatalf("cleared description = %q, want empty", project.Description)
	}

	outsider, token := e.mustUserToken(t, "project-update-denied")
	_ = outsider
	code, body = e.doWithToken(t, token, http.MethodPatch, e.projectPath(), map[string]any{"description": "denied"})
	if code != http.StatusForbidden {
		t.Fatalf("denied code = %d body = %s", code, body)
	}
}

func TestHTTPDeleteProject(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	owner, ownerToken := e.mustUserToken(t, "project-delete-owner")
	project, err := e.store.CreateProjectForUser(e.ctx, owner.ID, uniqueProjectKey(t), "API deletable", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	path := "/" + owner.Username + "/projects/" + project.Key

	member, memberToken := e.mustUserToken(t, "project-delete-member")
	if _, err := e.store.GrantProjectAccess(e.ctx, project.ID, member.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "project-delete-outsider")
	for name, token := range map[string]string{"write member": memberToken, "outsider": outsiderToken} {
		code, body := e.doWithToken(t, token, http.MethodDelete, path, nil)
		if code != http.StatusForbidden {
			t.Fatalf("%s delete code = %d body = %s", name, code, body)
		}
	}
	if _, err := e.store.GetProject(e.ctx, project.ID); err != nil {
		t.Fatalf("project deleted by a user without permission: %v", err)
	}

	code, body := e.doWithToken(t, ownerToken, http.MethodDelete, path, nil)
	if code != http.StatusNoContent {
		t.Fatalf("owner delete code = %d body = %s", code, body)
	}
	if _, err := e.store.GetProject(e.ctx, project.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject after delete = %v, want ErrNotFound", err)
	}
	code, body = e.doWithToken(t, ownerToken, http.MethodDelete, path, nil)
	if code != http.StatusNotFound {
		t.Fatalf("repeat delete code = %d body = %s", code, body)
	}

	adminRemovable, err := e.store.CreateProjectForUser(e.ctx, owner.ID, uniqueProjectKey(t), "Admin deletable", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser admin removable: %v", err)
	}
	adminPath := "/" + owner.Username + "/projects/" + adminRemovable.Key
	code, body = e.do(t, http.MethodDelete, adminPath, nil)
	if code != http.StatusNoContent {
		t.Fatalf("admin delete code = %d body = %s", code, body)
	}
	if _, err := e.store.GetProject(e.ctx, adminRemovable.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject after admin delete = %v, want ErrNotFound", err)
	}
}

func TestHTTPProjectFavorites(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	code, body := e.do(t, http.MethodGet, e.projectPath(), nil)
	if code != http.StatusOK {
		t.Fatalf("get project code = %d body = %s", code, body)
	}
	got := decode[projectResponseDecoded](t, body)
	if got.ID != e.projectID || got.Favorite {
		t.Fatalf("initial project = %+v", got)
	}

	key := uniqueProjectKey(t)
	code, body = e.do(t, http.MethodPost, "/projects/", map[string]any{
		"key":  key,
		"name": "Created Favorite API Project",
	})
	if code != http.StatusCreated {
		t.Fatalf("create project code = %d body = %s", code, body)
	}
	created := decode[projectResponseDecoded](t, body)
	if created.ID == uuid.Nil || created.Favorite {
		t.Fatalf("created project = %+v", created)
	}

	code, body = e.do(t, http.MethodPut, e.projectPath()+"/favorite", nil)
	if code != http.StatusOK {
		t.Fatalf("favorite code = %d body = %s", code, body)
	}
	favorited := decode[projectResponseDecoded](t, body)
	if !favorited.Favorite || favorited.ID != e.projectID {
		t.Fatalf("favorited project = %+v", favorited)
	}
	code, body = e.do(t, http.MethodPut, e.projectPath()+"/favorite", nil)
	if code != http.StatusOK || !decode[projectResponseDecoded](t, body).Favorite {
		t.Fatalf("repeat favorite code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodGet, e.projectPath(), nil)
	if code != http.StatusOK || !decode[projectResponseDecoded](t, body).Favorite {
		t.Fatalf("get favorited code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPatch, e.projectPath(), map[string]any{"description": "still favorite"})
	if code != http.StatusOK || !decode[projectResponseDecoded](t, body).Favorite {
		t.Fatalf("update favorited code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, "/projects", nil)
	if code != http.StatusOK {
		t.Fatalf("list projects code = %d body = %s", code, body)
	}
	page := decodePage[projectResponseDecoded](t, body)
	seenFavorite := false
	seenCreated := false
	for _, project := range page.Items {
		if project.ID == e.projectID {
			seenFavorite = project.Favorite
		}
		if project.ID == created.ID {
			seenCreated = true
			if project.Favorite {
				t.Fatalf("created project unexpectedly favorite: %+v", project)
			}
		}
	}
	if !seenFavorite || !seenCreated {
		t.Fatalf("project list did not include expected favorite states: %+v", page.Items)
	}

	_, outsiderToken := e.mustUserToken(t, "project-favorite-denied")
	code, body = e.doWithToken(t, outsiderToken, http.MethodPut, e.projectPath()+"/favorite", nil)
	if code != http.StatusForbidden {
		t.Fatalf("outsider favorite code = %d body = %s", code, body)
	}
	code, body = e.doWithToken(t, outsiderToken, http.MethodDelete, e.projectPath()+"/favorite", nil)
	if code != http.StatusForbidden {
		t.Fatalf("outsider unfavorite code = %d body = %s", code, body)
	}
	missingFavoritePath := "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/favorite"
	code, body = e.do(t, http.MethodPut, missingFavoritePath, nil)
	if code != http.StatusNotFound {
		t.Fatalf("favorite missing project code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodDelete, missingFavoritePath, nil)
	if code != http.StatusNotFound {
		t.Fatalf("unfavorite missing project code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodDelete, e.projectPath()+"/favorite", nil)
	if code != http.StatusOK {
		t.Fatalf("unfavorite code = %d body = %s", code, body)
	}
	if unfavorited := decode[projectResponseDecoded](t, body); unfavorited.Favorite {
		t.Fatalf("unfavorited project = %+v", unfavorited)
	}
	code, body = e.do(t, http.MethodDelete, e.projectPath()+"/favorite", nil)
	if code != http.StatusOK || decode[projectResponseDecoded](t, body).Favorite {
		t.Fatalf("repeat unfavorite code = %d body = %s", code, body)
	}
}
