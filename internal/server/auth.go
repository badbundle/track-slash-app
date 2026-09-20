package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type authContextKey struct{}

type authContext struct {
	User  model.User
	Token model.AuthToken
}

func (s *Server) authenticateRequest(r *http.Request) (authContext, error) {
	raw, ok := bearerToken(r)
	if !ok {
		return authContext{}, store.ErrUnauthorized
	}
	auth, err := s.store.AuthenticateToken(r.Context(), raw)
	if err != nil {
		return authContext{}, err
	}
	return authContext{User: auth.User, Token: auth.Token}, nil
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, err := s.authenticateRequest(r)
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				if s.anonymousProjectReadAllowed(r, true) {
					next.ServeHTTP(w, r)
					return
				}
				writeUnauthorized(w)
				return
			}
			writeStoreError(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, auth)
		ctx = store.WithActor(ctx, auth.User.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", false
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func currentAuth(r *http.Request) authContext {
	auth, _ := r.Context().Value(authContextKey{}).(authContext)
	return auth
}

func currentUser(r *http.Request) model.User {
	return currentAuth(r).User
}

// requireFirstPartyToken refuses an action to a token issued to an OAuth
// connector.
//
// A connector acts for the user, but only for as long as the user lets it.
// Minting an API token would escape that: the new token records no client, so
// revoking the connector cannot reach it and it outlives the consent it was
// created under. Listing and revoking tokens are refused for the same reason —
// they are the account's credential management, not the connector's work.
//
// This is about the credential, not the person, so it is deliberately separate
// from the admin and project permission checks.
func requireFirstPartyToken(w http.ResponseWriter, r *http.Request) bool {
	if currentAuth(r).Token.Kind == model.AuthTokenKindOAuth {
		writeError(w, http.StatusForbidden, "connected applications cannot manage account credentials")
		return false
	}
	return true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !currentUser(r).IsAdmin {
		writeForbidden(w)
		return false
	}
	return true
}

func (s *Server) requireProjectAccess(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) bool {
	user := currentUser(r)
	ok, err := s.store.UserCanAccessProject(r.Context(), user, projectID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !ok {
		writeForbidden(w)
		return false
	}
	return true
}

func (s *Server) requireProjectWriteAccess(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) bool {
	ok, err := s.store.UserCanWriteProject(r.Context(), currentUser(r), projectID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !ok {
		writeForbidden(w)
		return false
	}
	return true
}

func (s *Server) requireProjectIssueCreation(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) bool {
	ok, err := s.store.UserCanCreateProjectIssue(r.Context(), currentUser(r), projectID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !ok {
		writeForbidden(w)
		return false
	}
	return true
}

func (s *Server) requireProjectDeletion(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) bool {
	ok, err := s.store.UserCanDeleteProject(r.Context(), currentUser(r), projectID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !ok {
		writeForbidden(w)
		return false
	}
	return true
}

func (s *Server) requireProjectMemberManagement(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) bool {
	ok, err := s.store.UserCanManageProjectMembers(r.Context(), currentUser(r), projectID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !ok {
		writeForbidden(w)
		return false
	}
	return true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

func writeForbidden(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "forbidden")
}

func (s *Server) anonymousProjectReadAllowed(r *http.Request, api bool) bool {
	if s.store == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if api {
		if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" {
			return false
		}
		parts = parts[2:]
		if len(parts) == 1 && parts[0] == "projects" {
			return true
		}
	}
	if (len(parts) == 2 || len(parts) == 3 && parts[2] == "panel") && parts[1] == "projects" {
		_, err := store.NormalizeUsername(parts[0])
		return err == nil
	}
	if len(parts) < 3 {
		return false
	}
	owner, err := store.NormalizeUsername(parts[0])
	if err != nil {
		return false
	}
	var projectID uuid.UUID
	switch parts[1] {
	case "projects":
		key := strings.ToUpper(strings.TrimSpace(parts[2]))
		if !projectKeyRe.MatchString(key) {
			return false
		}
		project, err := s.store.GetProjectByOwnerKey(r.Context(), owner, key)
		if err != nil {
			return false
		}
		projectID = project.ID
	case "issues":
		ref, err := parseIssueRef(parts[2])
		if err != nil {
			return false
		}
		issue, err := s.store.GetIssueByOwnerKeyNumber(r.Context(), owner, ref.ProjectKey, ref.Number)
		if err != nil {
			return false
		}
		projectID = issue.ProjectID
	default:
		return false
	}
	permissions, err := s.store.ProjectPermissionsForUser(r.Context(), model.User{}, projectID)
	return err == nil && permissions.CanRead
}
