package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

// readsPrivateIssues reports whether the user may see the project's private
// issues: a member of any role, its owner or a site admin. Every surface that
// lists, counts or names issues passes this on as IncludePrivate, and sprint
// history as IncludeDeleted too, since the same readers see deleted issues.
func (s *Server) readsPrivateIssues(ctx context.Context, user model.User, projectID uuid.UUID) (bool, error) {
	permissions, err := s.store.ProjectPermissionsForUser(ctx, user, projectID)
	if err != nil {
		return false, err
	}
	return permissions.CanReadMembersOnly, nil
}

// parsePrivateFilter reads the optional private=true|false issue list filter.
func parsePrivateFilter(raw string) (*bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("private must be true or false")
	}
	return &value, nil
}

// uiIssuePrivateFieldData renders the "issue-private-field" template.
type uiIssuePrivateFieldData struct {
	ID       string
	Checked  bool
	HelpDesk bool
}

func uiIssuePrivateField(id string, checked, helpDesk bool) uiIssuePrivateFieldData {
	return uiIssuePrivateFieldData{ID: id, Checked: checked, HelpDesk: helpDesk}
}
