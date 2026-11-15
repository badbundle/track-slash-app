package server

import (
	"context"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

// uiIssueFormsShown is how many of an issue's forms the Details row lists
// before "and N more".
const uiIssueFormsShown = 10

// uiIssueFormLine is one of an issue's forms: a link to its page and, for a
// form waiting on someone else, whom it is waiting on.
type uiIssueFormLine struct {
	Form      model.FormSummary
	Href      string
	PanelHref string
	WaitingOn string
}

// uiIssueFormsData backs the issue page's form notice and Details row. It is
// only built for members, and only when the issue has forms.
type uiIssueFormsData struct {
	// Open are the forms still waiting on a person, newest first.
	Open []uiIssueFormLine
	// Lines are the newest forms of any status, at most uiIssueFormsShown.
	Lines []uiIssueFormLine
	// Total is the number of forms the issue has.
	Total int
	// More is how many forms Lines leaves out.
	More         int
	AllHref      string
	AllPanelHref string
}

// uiBuildIssueForms loads an issue's forms for a member. It answers nil when
// the issue has none.
func (s *Server) uiBuildIssueForms(ctx context.Context, project model.Project, issue model.Issue, viewerID uuid.UUID) (*uiIssueFormsData, error) {
	forms, _, err := s.store.ListProjectForms(ctx, store.ListProjectFormsParams{ProjectID: project.ID, IssueID: &issue.ID, Limit: MaxLimit})
	if err != nil {
		return nil, err
	}
	if len(forms) == 0 {
		return nil, nil
	}
	names := map[uuid.UUID]string{}
	waitingOn := func(form model.FormSummary) (string, error) {
		if form.Status != model.FormStatusOpen || form.AssigneeID == viewerID {
			return "", nil
		}
		if name, ok := names[form.AssigneeID]; ok {
			return name, nil
		}
		user, err := s.uiOptionalUser(ctx, &form.AssigneeID)
		if err != nil {
			return "", err
		}
		name := ""
		if user != nil {
			name = uiUserLabel(user.Name, user.Username, "")
		}
		names[form.AssigneeID] = name
		return name, nil
	}
	data := &uiIssueFormsData{
		Total:        len(forms),
		AllHref:      uiProjectViewPath(project, "forms"),
		AllPanelHref: uiProjectPanelPath(project, "forms"),
	}
	for i, form := range forms {
		href := formPath(form.OwnerUsername, form.ProjectKey, form.Ref)
		line := uiIssueFormLine{Form: form, Href: href, PanelHref: href + "/panel"}
		if form.Status == model.FormStatusOpen {
			if line.WaitingOn, err = waitingOn(form); err != nil {
				return nil, err
			}
			data.Open = append(data.Open, line)
		}
		if i < uiIssueFormsShown {
			data.Lines = append(data.Lines, line)
		}
	}
	data.More = len(forms) - len(data.Lines)
	return data, nil
}

// openFormRefs lists the refs of an issue's open forms, newest first. It is
// never nil, so a member's response carries an empty list rather than none.
func (s *Server) openFormRefs(ctx context.Context, issue model.Issue) ([]string, error) {
	forms, _, err := s.store.ListProjectForms(ctx, store.ListProjectFormsParams{
		ProjectID: issue.ProjectID,
		IssueID:   &issue.ID,
		Statuses:  []model.FormStatus{model.FormStatusOpen},
		Limit:     MaxLimit,
	})
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(forms))
	for _, form := range forms {
		refs = append(refs, form.Ref)
	}
	return refs, nil
}

// issueWithOpenForms is the issue a member reads, with the forms still
// waiting on a person for it.
type issueWithOpenForms struct {
	model.Issue
	OpenForms []string `json:"open_forms"`
}

// memberIssue wraps an issue with its open forms when the reader is a member,
// and leaves it as it is for anyone else.
func (s *Server) memberIssue(ctx context.Context, permissions store.ProjectPermissions, issue model.Issue) (any, error) {
	if !permissions.CanReadMembersOnly {
		return issue, nil
	}
	refs, err := s.openFormRefs(ctx, issue)
	if err != nil {
		return nil, err
	}
	return issueWithOpenForms{Issue: issue, OpenForms: refs}, nil
}
