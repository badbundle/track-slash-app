package server

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// uiInboxPageSize is how many forms the Inbox lists before "Load more".
const uiInboxPageSize = 50

// uiInboxPanelData backs the Inbox page: the forms addressed to the signed-in
// user, as the To do and Done tabs.
type uiInboxPanelData struct {
	// TodoCount is the number of open forms waiting on the user, shown on the
	// To do tab whichever tab is open.
	TodoCount int
	Tabs      uiTabBarData
	uiInboxRowsData
}

// uiInboxRowsData is a run of rows and the link to the next run.
type uiInboxRowsData struct {
	Done     bool
	Rows     []uiInboxRow
	HasMore  bool
	MoreHref string
}

type uiInboxRow struct {
	Form      model.FormSummary
	Href      string
	PanelHref string
	Progress  string
	// Done shows the form's status badge.
	Done bool
	// Assignee, when set, shows the person's avatar. Only the project's Forms
	// list sets it, since the Inbox is all the signed-in user's.
	Assignee *model.User
}

// uiInboxProgress is the progress text of a row: how many questions the form
// asks and, when some are required, how many of those have an answer.
func uiInboxProgress(form model.FormSummary) string {
	noun := "questions"
	if form.QuestionCount == 1 {
		noun = "question"
	}
	text := fmt.Sprintf("%d %s", form.QuestionCount, noun)
	if form.RequiredCount > 0 {
		text += fmt.Sprintf(" · %d of %d answered", form.RequiredAnswered, form.RequiredCount)
	}
	return text
}

func uiInboxPath(done bool, suffix string) string {
	if done {
		return "/inbox/done" + suffix
	}
	return "/inbox" + suffix
}

func uiInboxTabs(done bool, todoCount int) uiTabBarData {
	tab := func(label, icon string, tabDone bool, count int) uiTabItem {
		return uiTabItem{
			Count:     count,
			Label:     label,
			Icon:      icon,
			Href:      uiInboxPath(tabDone, ""),
			HXGet:     uiInboxPath(tabDone, "/panel"),
			HXTarget:  "#main",
			HXPushURL: uiInboxPath(tabDone, ""),
			Active:    done == tabDone,
		}
	}
	return uiTabBarData{
		Label: "Inbox views",
		Items: []uiTabItem{tab("To do", "circle-dot", false, todoCount), tab("Done", "check", true, 0)},
	}
}

func (s *Server) uiBuildInboxRows(r *http.Request, done bool) (uiInboxRowsData, error) {
	state := store.InboxTodo
	if done {
		state = store.InboxDone
	}
	params := store.ListInboxFormsParams{UserID: currentUser(r).ID, State: state, Limit: uiInboxPageSize}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c store.InboxCursor
		if err := decodeCursor(raw, &c); err != nil {
			return uiInboxRowsData{}, errUIBadRequest
		}
		params.Cursor = &c
	}
	forms, hasMore, err := s.store.ListInboxForms(r.Context(), params)
	if err != nil {
		return uiInboxRowsData{}, err
	}
	data := uiInboxRowsData{Done: done, HasMore: hasMore}
	for _, form := range forms {
		href := formPath(form.OwnerUsername, form.ProjectKey, form.Ref)
		data.Rows = append(data.Rows, uiInboxRow{Form: form, Href: href, PanelHref: href + "/panel", Progress: uiInboxProgress(form), Done: done})
	}
	if hasMore {
		next := encodeCursor(store.NextInboxCursor(state, forms[len(forms)-1]))
		data.MoreHref = uiInboxPath(done, "/page") + "?cursor=" + url.QueryEscape(next)
	}
	return data, nil
}

func (s *Server) uiBuildInboxPanel(r *http.Request, done bool) (*uiInboxPanelData, error) {
	rows, err := s.uiBuildInboxRows(r, done)
	if err != nil {
		return nil, err
	}
	count, err := s.store.CountInboxForms(r.Context(), currentUser(r).ID)
	if err != nil {
		return nil, err // defensive: the list above reached the DB
	}
	return &uiInboxPanelData{TodoCount: count, Tabs: uiInboxTabs(done, count), uiInboxRowsData: rows}, nil
}

func (s *Server) uiInboxPage(w http.ResponseWriter, r *http.Request, done bool) {
	panel, err := s.uiBuildInboxPanel(r, done)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	s.renderUIShell(w, r, http.StatusOK, uiShellData{
		User:          currentUser(r),
		SidebarActive: uiSidebarState{View: "inbox"},
		InboxPanel:    panel,
	})
}

func (s *Server) uiInboxPanel(w http.ResponseWriter, r *http.Request, done bool) {
	panel, err := s.uiBuildInboxPanel(r, done)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "inbox-panel", panel)
}

// uiInboxCount answers the sidebar's periodic refresh with the Inbox link and
// the number of forms waiting on the signed-in user. The page that asked
// restores the link's current-page state after swapping it in.
func (s *Server) uiInboxCount(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.CountInboxForms(r.Context(), currentUser(r).ID)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	renderUITemplate(w, http.StatusOK, "sidebar-inbox", uiSidebarInboxData{Count: count})
}

// uiInboxMore answers "Load more": the next run of rows.
func (s *Server) uiInboxMore(w http.ResponseWriter, r *http.Request, done bool) {
	rows, err := s.uiBuildInboxRows(r, done)
	if err != nil {
		writeUIStoreError(w, err)
		return
	}
	renderUITemplate(w, http.StatusOK, "inbox-rows", rows)
}
