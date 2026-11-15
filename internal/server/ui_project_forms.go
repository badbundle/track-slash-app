package server

import (
	"context"
	"net/http"
	"net/url"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

// uiProjectFormsPageSize is how many forms the project's Forms view lists
// before "Load more".
const uiProjectFormsPageSize = 50

// uiFormsFilter is a status choice on the project's Forms view.
type uiFormsFilter struct {
	Key      string
	Label    string
	Statuses []model.FormStatus
}

// uiFormsFilters are the choices in the order they are shown. The first is
// the one a bare URL means.
var uiFormsFilters = []uiFormsFilter{
	{Key: "open", Label: "Open", Statuses: []model.FormStatus{model.FormStatusOpen}},
	{Key: "submitted", Label: "Submitted", Statuses: []model.FormStatus{model.FormStatusSubmitted}},
	{Key: "done", Label: "Done", Statuses: []model.FormStatus{model.FormStatusAcknowledged, model.FormStatusCancelled}},
	{Key: "all", Label: "All"},
}

// uiFormsFilterFor reads ?status=. An unknown value is the same as all.
func uiFormsFilterFor(raw string) uiFormsFilter {
	for _, filter := range uiFormsFilters {
		if filter.Key == raw {
			return filter
		}
	}
	return uiFormsFilters[len(uiFormsFilters)-1]
}

func uiProjectFormsPath(project model.Project, filter uiFormsFilter, suffix string) string {
	path := uiProjectPath(project) + "/forms" + suffix
	if filter.Key != "all" {
		path += "?" + url.Values{"status": {filter.Key}}.Encode()
	}
	return path
}

// uiProjectFormsData backs the project's Forms view: the project's forms,
// newest first, behind a status filter.
type uiProjectFormsData struct {
	Filter      uiFormsFilter
	FilterRange uiRangeControl
	Filtered    bool
	Rows        []uiInboxRow
	HasMore     bool
	MoreHref    string
}

func (s *Server) uiBuildProjectForms(ctx context.Context, r *http.Request, project model.Project) (*uiProjectFormsData, error) {
	filter := uiFormsFilterFor(r.URL.Query().Get("status"))
	params := store.ListProjectFormsParams{ProjectID: project.ID, Statuses: filter.Statuses, Limit: uiProjectFormsPageSize}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.FormsCursor
		if err := decodeCursor(raw, &c); err != nil {
			return nil, errUIBadRequest
		}
		params.Cursor = &c
	}
	forms, hasMore, err := s.store.ListProjectForms(ctx, params)
	if err != nil {
		return nil, err
	}
	data := &uiProjectFormsData{Filter: filter, Filtered: filter.Key != "all", HasMore: hasMore}
	options := make([]uiRangeOption, 0, len(uiFormsFilters))
	for _, choice := range uiFormsFilters {
		options = append(options, uiRangeOption{
			Label:  choice.Label,
			Href:   uiProjectFormsPath(project, choice, ""),
			HXGet:  uiProjectFormsPath(project, choice, "/panel"),
			Active: choice.Key == filter.Key,
		})
	}
	data.FilterRange = uiNewRangeControl("Form status", options)

	assignees := map[uuid.UUID]*model.User{}
	for _, form := range forms {
		assignee, seen := assignees[form.AssigneeID]
		if !seen {
			user, err := s.uiOptionalUser(ctx, &form.AssigneeID)
			if err != nil {
				return nil, err
			}
			if user != nil {
				// Only the name and picture go to the page, never the email.
				user.Email = ""
			}
			assignee = user
			assignees[form.AssigneeID] = assignee
		}
		href := formPath(form.OwnerUsername, form.ProjectKey, form.Ref)
		data.Rows = append(data.Rows, uiInboxRow{
			Form: form, Href: href, PanelHref: href + "/panel", Progress: uiInboxProgress(form),
			Done: true, Assignee: assignee,
		})
	}
	if hasMore {
		next := encodeCursor(store.FormsCursor{Number: forms[len(forms)-1].Number})
		more := uiProjectFormsPath(project, filter, "/page")
		sep := "?"
		if filter.Key != "all" {
			sep = "&"
		}
		data.MoreHref = more + sep + "cursor=" + url.QueryEscape(next)
	}
	return data, nil
}

// uiProjectFormsMember answers whether the viewer may read the project's
// forms. Anyone else, a public reader included, gets the not-found page, as on
// a form's own page.
func (s *Server) uiProjectFormsMember(w http.ResponseWriter, r *http.Request) (model.Project, bool) {
	project, ok := s.uiProjectFromRoute(w, r)
	if !ok {
		return model.Project{}, false
	}
	perms, err := s.store.ProjectPermissionsForUser(r.Context(), currentUser(r), project.ID)
	if err != nil {
		writeUIStoreError(w, err) // defensive: the project was just resolved
		return model.Project{}, false
	}
	if !perms.CanReadMembersOnly {
		s.uiNotFound(w, r)
		return model.Project{}, false
	}
	return project, true
}

func (s *Server) uiProjectFormsView(w http.ResponseWriter, r *http.Request, panel bool) {
	if _, ok := s.uiProjectFormsMember(w, r); !ok {
		return
	}
	if panel {
		s.uiProjectWorkPanel(w, r, "forms")
		return
	}
	s.uiProjectWorkPage(w, r, "forms")
}

// uiProjectFormsMore answers "Load more": the next run of rows.
func (s *Server) uiProjectFormsMore(w http.ResponseWriter, r *http.Request) {
	project, ok := s.uiProjectFormsMember(w, r)
	if !ok {
		return
	}
	data, err := s.uiBuildProjectForms(r.Context(), r, project)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "project-form-rows", data)
}
