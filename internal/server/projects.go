package server

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

var projectKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

type createProjectReq struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type updateProjectReq struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type projectResponse struct {
	model.Project
	Favorite bool `json:"favorite"`
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	req.Name = strings.TrimSpace(req.Name)
	if !projectKeyRe.MatchString(req.Key) {
		writeError(w, http.StatusBadRequest, "key must match ^[A-Z][A-Z0-9]{1,9}$")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}

	p, err := s.store.CreateProjectForUser(r.Context(), currentUser(r).ID, req.Key, req.Name, req.Description)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectResponse{Project: p})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var cursor *store.ProjectsCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.ProjectsCursor
		if err := decodeCursor(raw, &c); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cursor = &c
	}

	projects, hasMore, err := s.store.ListProjects(r.Context(), store.ListProjectsParams{
		Cursor:        cursor,
		Limit:         limit,
		VisibleToUser: visibleProjectUser(currentUser(r)),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out, err := s.projectResponses(r.Context(), currentUser(r), projects)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var next *string
	if hasMore {
		last := projects[len(projects)-1]
		enc := encodeCursor(store.ProjectsCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		next = &enc
	}
	writePage(w, out, next)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	out, err := s.projectResponse(r.Context(), currentUser(r), project)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectWriteAccess(w, r, project.ID) {
		return
	}
	var req updateProjectReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params := store.UpdateProjectParams{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 200 {
			writeError(w, http.StatusBadRequest, "name must be 1..200 chars")
			return
		}
		params.Name = &name
	}
	if req.Description != nil {
		description := *req.Description
		if strings.TrimSpace(description) == "" {
			description = ""
		}
		params.Description = &description
	}
	updated, err := s.store.UpdateProject(r.Context(), project.ID, params)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out, err := s.projectResponse(r.Context(), currentUser(r), updated)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectDeletion(w, r, project.ID) {
		return
	}
	if err := s.store.DeleteProject(r.Context(), project.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) favoriteProject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	if err := s.store.FavoriteProject(r.Context(), currentUser(r).ID, project.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectResponse{Project: project, Favorite: true})
}

func (s *Server) unfavoriteProject(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	if err := s.store.UnfavoriteProject(r.Context(), currentUser(r).ID, project.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectResponse{Project: project})
}

func visibleProjectUser(u model.User) *uuid.UUID {
	if u.IsAdmin {
		return nil
	}
	return &u.ID
}

func (s *Server) projectResponse(ctx context.Context, user model.User, project model.Project) (projectResponse, error) {
	favorite, err := s.store.IsProjectFavorite(ctx, user.ID, project.ID)
	if err != nil {
		return projectResponse{}, err
	}
	return projectResponse{Project: project, Favorite: favorite}, nil
}

func (s *Server) projectResponses(ctx context.Context, user model.User, projects []model.Project) ([]projectResponse, error) {
	ids := make([]uuid.UUID, 0, len(projects))
	for _, project := range projects {
		ids = append(ids, project.ID)
	}
	favorites, err := s.store.FavoriteProjectIDs(ctx, user.ID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]projectResponse, 0, len(projects))
	for _, project := range projects {
		out = append(out, projectResponse{
			Project:  project,
			Favorite: favorites[project.ID],
		})
	}
	return out, nil
}
