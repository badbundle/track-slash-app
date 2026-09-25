package server

import (
	"context"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/store"
)

func (s *Server) authorizeTopic(ctx context.Context, kind string, id uuid.UUID) error {
	auth, ok := ctx.Value(authContextKey{}).(authContext)
	if !ok {
		return store.ErrUnauthorized
	}
	var (
		projectID uuid.UUID
		err       error
	)
	switch kind {
	case "project":
		projectID = id
	case "issue":
		projectID, err = s.store.ProjectIDForIssue(ctx, id)
	case "comment":
		projectID, err = s.store.ProjectIDForComment(ctx, id)
	case "sprint":
		projectID, err = s.store.ProjectIDForSprint(ctx, id)
	case "issue_link":
		projectID, err = s.store.ProjectIDForIssueLink(ctx, id)
	case "project_context":
		projectID, err = s.store.ProjectIDForProjectContext(ctx, id)
	case "issue_context_link":
		projectID, err = s.store.ProjectIDForIssueContextLink(ctx, id)
	case "issue_tag":
		projectID, err = s.store.ProjectIDForIssueTag(ctx, id)
	case "issue_tag_link":
		projectID, err = s.store.ProjectIDForIssueTagLink(ctx, id)
	case "project_changelog":
		projectID, err = s.store.ProjectIDForProjectChangelog(ctx, id)
	case "whiteboard_page":
		projectID, err = s.store.ProjectIDForWhiteboardPage(ctx, id)
	default:
		return store.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	ok, err = s.store.UserCanAccessProject(ctx, auth.User, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return store.ErrUnauthorized
	}
	return nil
}

func (s *Server) disconnectRealtimeClients() {
	if s.hub != nil {
		s.hub.DisconnectAll()
	}
}
