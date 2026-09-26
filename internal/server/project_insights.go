package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// projectInsightsQuery is the shared insight request shape for the HTTP API,
// MCP, and the Insights page.
type projectInsightsQuery struct {
	Range model.InsightRange
	// SprintNumber selects the sprint burn-up; zero means the default.
	SprintNumber int
}

var errInsightRange = errors.New("range must be one of 2w, 30d, 90d, all")

func parseProjectInsightsQuery(rawRange, rawSprint string) (projectInsightsQuery, error) {
	query := projectInsightsQuery{Range: model.DefaultInsightRange}
	if raw := strings.ToLower(strings.TrimSpace(rawRange)); raw != "" {
		query.Range = model.InsightRange(raw)
		if !query.Range.Valid() {
			return projectInsightsQuery{}, errInsightRange
		}
	}
	if strings.TrimSpace(rawSprint) != "" {
		number, err := parseTypedRef(rawSprint, "sprint")
		if err != nil {
			return projectInsightsQuery{}, err
		}
		query.SprintNumber = number
	}
	return query, nil
}

func parseProjectInsightsValues(values url.Values) (projectInsightsQuery, error) {
	return parseProjectInsightsQuery(values.Get("range"), values.Get("sprint"))
}

// projectInsightsSprintsEnabled decides whether sprint-dependent insights
// (sprint burn-up and velocity) apply to a project: they follow the project's
// sprint mode, so a project working one issue at a time does not see them.
func projectInsightsSprintsEnabled(project model.Project) bool {
	return project.SprintsEnabled
}

// projectInsights loads every insight series for a project the caller has
// already been authorised to read.
func (s *Server) projectInsights(ctx context.Context, project model.Project, query projectInsightsQuery) (model.ProjectInsights, error) {
	var sprintID *uuid.UUID
	if query.SprintNumber > 0 {
		sprint, err := s.store.GetSprintByProjectNumber(ctx, project.ID, query.SprintNumber)
		if err != nil {
			return model.ProjectInsights{}, err
		}
		sprintID = &sprint.ID
	}
	insights, err := s.store.GetProjectInsights(ctx, store.ProjectInsightsParams{
		ProjectID: project.ID,
		Range:     query.Range,
		SprintID:  sprintID,
	})
	if err != nil {
		return model.ProjectInsights{}, err
	}
	insights.Sprints.Enabled = projectInsightsSprintsEnabled(project)
	return insights, nil
}

func (s *Server) getProjectInsights(w http.ResponseWriter, r *http.Request) {
	project, ok := s.projectFromRoute(w, r)
	if !ok {
		return
	}
	if !s.requireProjectAccess(w, r, project.ID) {
		return
	}
	query, err := parseProjectInsightsValues(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	insights, err := s.projectInsights(r.Context(), project, query)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, insights)
}
