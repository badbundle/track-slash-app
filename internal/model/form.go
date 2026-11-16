package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Limits on a form. The database repeats the ones on plain columns as CHECKs.
const (
	FormMaxTitleLength       = 200
	FormMaxDescriptionLength = 20000
	FormMaxCommentLength     = 10000
	FormMaxOriginLabelLength = 200
	FormMaxOriginURLLength   = 2000
	FormMaxSections          = 12
	FormMaxQuestions         = 40
	FormMaxOptions           = 20
	FormMaxLabelLength       = 300
	FormMaxHelpLength        = 2000
	FormMaxPlaceholderLength = 200
	FormMaxNoteLength        = 2000
	FormMaxOtherLength       = 2000
	FormMaxTextAnswerLength  = 10000
	FormMaxDefinitionBytes   = 64 * 1024
	// FormMaxQuestionMedia is how many files one question can show.
	FormMaxQuestionMedia = 6
	// FormMaxAttachments is how many distinct files one form can carry,
	// counting question media, option media and its explicit attachments.
	FormMaxAttachments = 20
)

// FormOtherOptionID is the id a choice answer uses for the "Other" option that
// allow_other adds. No option of a definition may take it.
const FormOtherOptionID = "other"

var formIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// FormStatus is where a form is in its life: open while it waits on its
// assignee, submitted once answered, acknowledged once the answers were used,
// or cancelled.
type FormStatus string

const (
	FormStatusOpen         FormStatus = "open"
	FormStatusSubmitted    FormStatus = "submitted"
	FormStatusAcknowledged FormStatus = "acknowledged"
	FormStatusCancelled    FormStatus = "cancelled"
)

// formTransitions is the whole lifecycle: the statuses each status can move to.
var formTransitions = map[FormStatus][]FormStatus{
	FormStatusOpen:      {FormStatusSubmitted, FormStatusCancelled},
	FormStatusSubmitted: {FormStatusAcknowledged, FormStatusOpen, FormStatusCancelled},
}

func (s FormStatus) Valid() bool {
	switch s {
	case FormStatusOpen, FormStatusSubmitted, FormStatusAcknowledged, FormStatusCancelled:
		return true
	}
	return false
}

// Label is the status as people read it. Acknowledged reads "Picked up".
func (s FormStatus) Label() string {
	switch s {
	case FormStatusOpen:
		return "Open"
	case FormStatusSubmitted:
		return "Submitted"
	case FormStatusAcknowledged:
		return "Picked up"
	case FormStatusCancelled:
		return "Cancelled"
	}
	return string(s)
}

// Final reports whether the form can no longer change.
func (s FormStatus) Final() bool {
	return s == FormStatusAcknowledged || s == FormStatusCancelled
}

// CanTransitionTo reports whether a form in this status may move to next.
func (s FormStatus) CanTransitionTo(next FormStatus) bool {
	return slices.Contains(formTransitions[s], next)
}

// FormSubmitSurface records how a form was submitted.
type FormSubmitSurface string

const (
	FormSubmitWeb FormSubmitSurface = "web"
	FormSubmitAPI FormSubmitSurface = "api"
)

func (s FormSubmitSurface) Valid() bool {
	return s == FormSubmitWeb || s == FormSubmitAPI
}

// FormQuestionType is the kind of answer a question takes.
type FormQuestionType string

const (
	FormQuestionSingleChoice FormQuestionType = "single_choice"
	FormQuestionMultiChoice  FormQuestionType = "multi_choice"
	FormQuestionConfirm      FormQuestionType = "confirm"
	FormQuestionText         FormQuestionType = "text"
)

// FormDefinition is a form's questions, grouped into sections. It is written
// once, when the form is created.
type FormDefinition struct {
	Sections []FormSection `json:"sections" jsonschema:"one or more groups of questions; each section is one step when there are several"`
}

type FormSection struct {
	Title       string         `json:"title,omitempty" jsonschema:"section title; required when the form has more than one section"`
	Description string         `json:"description,omitempty" jsonschema:"optional Markdown shown above the section's questions"`
	Questions   []FormQuestion `json:"questions" jsonschema:"the section's questions, at least one"`
}

type FormQuestion struct {
	ID          string           `json:"id" jsonschema:"unique in the form: 1 to 40 lowercase letters, digits, - or _"`
	Type        FormQuestionType `json:"type" jsonschema:"single_choice, multi_choice, confirm or text"`
	Label       string           `json:"label" jsonschema:"the question as the person reads it"`
	Help        string           `json:"help,omitempty" jsonschema:"optional Markdown under the label"`
	Required    *bool            `json:"required,omitempty" jsonschema:"defaults to true; false lets the person skip the question"`
	Options     []FormOption     `json:"options,omitempty" jsonschema:"2 to 20 options, for single_choice and multi_choice only"`
	AllowOther  bool             `json:"allow_other,omitempty" jsonschema:"choice questions only: adds an Other option with a text box"`
	Multiline   bool             `json:"multiline,omitempty" jsonschema:"text questions only: a multi-line box"`
	Placeholder string           `json:"placeholder,omitempty" jsonschema:"text questions only: placeholder text"`
	Default     *FormValue       `json:"default,omitempty" jsonschema:"the answer you suggest; the form opens with it filled in"`
	Media       []string         `json:"media,omitempty" jsonschema:"up to 6 object refs, such as object-12, of files you uploaded with an upload URL; they are shown with the question"`
}

type FormOption struct {
	ID          string `json:"id" jsonschema:"unique in the question: 1 to 40 lowercase letters, digits, - or _; not other"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty" jsonschema:"optional one-line explanation"`
	Media       string `json:"media,omitempty" jsonschema:"optional object ref, such as object-12, of a file you uploaded with an upload URL; it is shown with this option"`
}

// FormValue is the answer to one question, or the answer a question suggests.
// Which fields apply depends on the question's type.
type FormValue struct {
	Selected []string `json:"selected,omitempty" jsonschema:"choice questions: the chosen option ids; other stands for the Other option"`
	Other    string   `json:"other,omitempty" jsonschema:"choice questions: the text for the Other option"`
	Checked  bool     `json:"checked,omitempty" jsonschema:"confirm questions: whether the box is ticked"`
	Text     string   `json:"text,omitempty" jsonschema:"text questions: the answer"`
}

func (v FormValue) isZero() bool {
	return len(v.Selected) == 0 && v.Other == "" && !v.Checked && v.Text == ""
}

func (v FormValue) equal(o FormValue) bool {
	return slices.Equal(v.Selected, o.Selected) && v.Other == o.Other && v.Checked == o.Checked && v.Text == o.Text
}

// FormAnswer is a person's answer to one question, with their optional note.
type FormAnswer struct {
	FormValue
	Note string `json:"note,omitempty"`
}

// FormAnswers holds a form's answers by question id. A question with no entry
// has not been answered.
type FormAnswers map[string]FormAnswer

// FormError says why a form's details, definition or answers were refused.
// Field locates the problem: a path such as sections[0].questions[2].default
// in a definition, or the question's id for an answer.
type FormError struct {
	Field   string
	Message string
}

func (e *FormError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func formError(field, format string, args ...any) error {
	return &FormError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// FormIncompleteError names the questions that stop a form being submitted.
type FormIncompleteError struct {
	QuestionIDs []string
}

func (e *FormIncompleteError) Error() string {
	return "questions still need an answer: " + strings.Join(e.QuestionIDs, ", ")
}

// FormDetails are the parts of a form its creator writes besides the questions.
type FormDetails struct {
	Title       string
	Description string
	OriginLabel string
	OriginURL   string
}

// ValidateFormDetails trims and checks a new form's details. An origin URL
// must be an https link with no credentials in it, because the form page
// links to it.
func ValidateFormDetails(in FormDetails) (FormDetails, error) {
	out := FormDetails{
		Title:       strings.TrimSpace(in.Title),
		Description: strings.TrimSpace(in.Description),
		OriginLabel: strings.TrimSpace(in.OriginLabel),
		OriginURL:   strings.TrimSpace(in.OriginURL),
	}
	if out.Title == "" || utf8.RuneCountInString(out.Title) > FormMaxTitleLength {
		return FormDetails{}, formError("title", "required, at most %d characters", FormMaxTitleLength)
	}
	if utf8.RuneCountInString(out.Description) > FormMaxDescriptionLength {
		return FormDetails{}, formError("description", "at most %d characters", FormMaxDescriptionLength)
	}
	if utf8.RuneCountInString(out.OriginLabel) > FormMaxOriginLabelLength {
		return FormDetails{}, formError("origin_label", "at most %d characters", FormMaxOriginLabelLength)
	}
	if out.OriginURL != "" {
		if utf8.RuneCountInString(out.OriginURL) > FormMaxOriginURLLength {
			return FormDetails{}, formError("origin_url", "at most %d characters", FormMaxOriginURLLength)
		}
		u, err := url.Parse(out.OriginURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return FormDetails{}, formError("origin_url", "must be an https link")
		}
	}
	return out, nil
}

// ValidateFormComment trims and checks the closing "Anything else?" text.
func ValidateFormComment(raw string) (string, error) {
	comment := strings.TrimSpace(raw)
	if utf8.RuneCountInString(comment) > FormMaxCommentLength {
		return "", formError("comment", "at most %d characters", FormMaxCommentLength)
	}
	return comment, nil
}

// ValidateFormDefinition checks a new form's questions and returns them
// trimmed, with each default tidied the way an answer would be.
func ValidateFormDefinition(in FormDefinition) (FormDefinition, error) {
	if len(in.Sections) == 0 {
		return FormDefinition{}, formError("sections", "a form needs at least one section")
	}
	if len(in.Sections) > FormMaxSections {
		return FormDefinition{}, formError("sections", "at most %d sections", FormMaxSections)
	}
	out := FormDefinition{Sections: make([]FormSection, len(in.Sections))}
	seen := map[string]bool{}
	total := 0
	for si, section := range in.Sections {
		path := fmt.Sprintf("sections[%d]", si)
		title := strings.TrimSpace(section.Title)
		if title == "" && len(in.Sections) > 1 {
			return FormDefinition{}, formError(path+".title", "a form with several sections needs a title for each")
		}
		if utf8.RuneCountInString(title) > FormMaxTitleLength {
			return FormDefinition{}, formError(path+".title", "at most %d characters", FormMaxTitleLength)
		}
		description := strings.TrimSpace(section.Description)
		if utf8.RuneCountInString(description) > FormMaxHelpLength {
			return FormDefinition{}, formError(path+".description", "at most %d characters", FormMaxHelpLength)
		}
		if len(section.Questions) == 0 {
			return FormDefinition{}, formError(path+".questions", "a section needs at least one question")
		}
		total += len(section.Questions)
		if total > FormMaxQuestions {
			return FormDefinition{}, formError("sections", "at most %d questions in a form", FormMaxQuestions)
		}
		questions := make([]FormQuestion, len(section.Questions))
		for qi, question := range section.Questions {
			qpath := fmt.Sprintf("%s.questions[%d]", path, qi)
			clean, err := validateFormQuestion(qpath, question)
			if err != nil {
				return FormDefinition{}, err
			}
			if seen[clean.ID] {
				return FormDefinition{}, formError(qpath+".id", "question id %q is used twice", clean.ID)
			}
			seen[clean.ID] = true
			questions[qi] = clean
		}
		out.Sections[si] = FormSection{Title: title, Description: description, Questions: questions}
	}
	if raw, _ := json.Marshal(out); len(raw) > FormMaxDefinitionBytes {
		return FormDefinition{}, formError("sections", "the questions are too large: at most %d KB as JSON", FormMaxDefinitionBytes/1024)
	}
	return out, nil
}

const formIDRule = "must be 1 to 40 lowercase letters, digits, - or _, starting with a letter or digit"

func validateFormQuestion(path string, q FormQuestion) (FormQuestion, error) {
	q.ID = strings.TrimSpace(q.ID)
	if !formIDPattern.MatchString(q.ID) {
		return FormQuestion{}, formError(path+".id", "%s", formIDRule)
	}
	q.Label = strings.TrimSpace(q.Label)
	if q.Label == "" || utf8.RuneCountInString(q.Label) > FormMaxLabelLength {
		return FormQuestion{}, formError(path+".label", "required, at most %d characters", FormMaxLabelLength)
	}
	q.Help = strings.TrimSpace(q.Help)
	if utf8.RuneCountInString(q.Help) > FormMaxHelpLength {
		return FormQuestion{}, formError(path+".help", "at most %d characters", FormMaxHelpLength)
	}
	q.Placeholder = strings.TrimSpace(q.Placeholder)
	if utf8.RuneCountInString(q.Placeholder) > FormMaxPlaceholderLength {
		return FormQuestion{}, formError(path+".placeholder", "at most %d characters", FormMaxPlaceholderLength)
	}
	media, err := validateFormMedia(path+".media", q.Media)
	if err != nil {
		return FormQuestion{}, err
	}
	q.Media = media

	switch q.Type {
	case FormQuestionSingleChoice, FormQuestionMultiChoice:
		if q.Multiline || q.Placeholder != "" {
			return FormQuestion{}, formError(path, "multiline and placeholder apply to text questions only")
		}
		options, err := validateFormOptions(path+".options", q.Options)
		if err != nil {
			return FormQuestion{}, err
		}
		q.Options = options
	case FormQuestionConfirm:
		if len(q.Options) > 0 || q.AllowOther {
			return FormQuestion{}, formError(path, "options and allow_other apply to choice questions only")
		}
		if q.Multiline || q.Placeholder != "" {
			return FormQuestion{}, formError(path, "multiline and placeholder apply to text questions only")
		}
	case FormQuestionText:
		if len(q.Options) > 0 || q.AllowOther {
			return FormQuestion{}, formError(path, "options and allow_other apply to choice questions only")
		}
	default:
		return FormQuestion{}, formError(path+".type", "must be single_choice, multi_choice, confirm or text")
	}

	if q.Default != nil {
		value, problem := q.cleanValue(*q.Default)
		if problem != "" {
			return FormQuestion{}, formError(path+".default", "%s", problem)
		}
		q.Default = nil
		if !value.isZero() {
			q.Default = &value
		}
	}
	return q, nil
}

func validateFormOptions(path string, in []FormOption) ([]FormOption, error) {
	if len(in) < 2 || len(in) > FormMaxOptions {
		return nil, formError(path, "a choice question needs 2 to %d options", FormMaxOptions)
	}
	out := make([]FormOption, len(in))
	seen := map[string]bool{}
	for i, option := range in {
		opath := fmt.Sprintf("%s[%d]", path, i)
		id := strings.TrimSpace(option.ID)
		if !formIDPattern.MatchString(id) {
			return nil, formError(opath+".id", "%s", formIDRule)
		}
		if id == FormOtherOptionID {
			return nil, formError(opath+".id", "%q is reserved: set allow_other to offer an Other option", FormOtherOptionID)
		}
		if seen[id] {
			return nil, formError(opath+".id", "option id %q is used twice", id)
		}
		seen[id] = true
		label := strings.TrimSpace(option.Label)
		if label == "" || utf8.RuneCountInString(label) > FormMaxLabelLength {
			return nil, formError(opath+".label", "required, at most %d characters", FormMaxLabelLength)
		}
		description := strings.TrimSpace(option.Description)
		if utf8.RuneCountInString(description) > FormMaxLabelLength {
			return nil, formError(opath+".description", "at most %d characters", FormMaxLabelLength)
		}
		var media string
		if strings.TrimSpace(option.Media) != "" {
			number, err := parseFormMediaRef(opath+".media", option.Media)
			if err != nil {
				return nil, err
			}
			media = StorageObjectRef(number)
		}
		out[i] = FormOption{ID: id, Label: label, Description: description, Media: media}
	}
	return out, nil
}

// IsRequired reports whether the question must be answered before its form
// can be submitted. Questions are required unless they say otherwise.
func (q FormQuestion) IsRequired() bool {
	return q.Required == nil || *q.Required
}

func (q FormQuestion) hasOption(id string) bool {
	return slices.ContainsFunc(q.Options, func(o FormOption) bool { return o.ID == id })
}

// optionLabel names a chosen option. An id that is no option of the question
// is returned as it is.
func (q FormQuestion) optionLabel(id string) string {
	if id == FormOtherOptionID {
		return "Other"
	}
	for _, option := range q.Options {
		if option.ID == id {
			return option.Label
		}
	}
	return id
}

// cleanValue checks a value against the question's type and options and
// returns it tidied: text trimmed, and chosen options de-duplicated and put in
// the question's own order. A non-empty problem says why it was refused. An
// incomplete value is fine here; Answered says whether it counts as an answer.
func (q FormQuestion) cleanValue(v FormValue) (FormValue, string) {
	switch q.Type {
	case FormQuestionSingleChoice, FormQuestionMultiChoice:
		if v.Checked || v.Text != "" {
			return FormValue{}, "a choice question takes selected option ids"
		}
		chosen := map[string]bool{}
		for _, raw := range v.Selected {
			id := strings.TrimSpace(raw)
			if id == FormOtherOptionID {
				if !q.AllowOther {
					return FormValue{}, "this question has no Other option"
				}
			} else if !q.hasOption(id) {
				return FormValue{}, fmt.Sprintf("unknown option %q", id)
			}
			chosen[id] = true
		}
		var selected []string
		for _, option := range q.Options {
			if chosen[option.ID] {
				selected = append(selected, option.ID)
			}
		}
		if chosen[FormOtherOptionID] {
			selected = append(selected, FormOtherOptionID)
		}
		if q.Type == FormQuestionSingleChoice && len(selected) > 1 {
			return FormValue{}, "pick one option"
		}
		other := strings.TrimSpace(v.Other)
		if other != "" && !chosen[FormOtherOptionID] {
			return FormValue{}, "Other text needs the other option selected"
		}
		if utf8.RuneCountInString(other) > FormMaxOtherLength {
			return FormValue{}, fmt.Sprintf("Other text can be at most %d characters", FormMaxOtherLength)
		}
		return FormValue{Selected: selected, Other: other}, ""
	case FormQuestionConfirm:
		if len(v.Selected) > 0 || v.Other != "" || v.Text != "" {
			return FormValue{}, "a confirm question takes checked"
		}
		return FormValue{Checked: v.Checked}, ""
	default:
		if len(v.Selected) > 0 || v.Other != "" || v.Checked {
			return FormValue{}, "a text question takes text"
		}
		text := strings.TrimSpace(v.Text)
		if utf8.RuneCountInString(text) > FormMaxTextAnswerLength {
			return FormValue{}, fmt.Sprintf("text can be at most %d characters", FormMaxTextAnswerLength)
		}
		return FormValue{Text: text}, ""
	}
}

// Answered reports whether the answer counts as an answer to the question: an
// option chosen (with its text, if that option is Other), the box ticked, or
// some text written.
func (q FormQuestion) Answered(a FormAnswer) bool {
	switch q.Type {
	case FormQuestionConfirm:
		return a.Checked
	case FormQuestionText:
		return a.Text != ""
	default:
		if len(a.Selected) == 0 {
			return false
		}
		return !slices.Contains(a.Selected, FormOtherOptionID) || a.Other != ""
	}
}

// blocksSubmit reports whether the question stops its form being submitted:
// it is required and unanswered, or it is left on Other with nothing written.
func (q FormQuestion) blocksSubmit(a FormAnswer) bool {
	if q.Answered(a) {
		return false
	}
	return q.IsRequired() || len(a.Selected) > 0
}

// Questions returns every question in the order the form asks them.
func (d FormDefinition) Questions() []FormQuestion {
	var out []FormQuestion
	for _, section := range d.Sections {
		out = append(out, section.Questions...)
	}
	return out
}

// Question finds a question by its id.
func (d FormDefinition) Question(id string) (FormQuestion, bool) {
	for _, q := range d.Questions() {
		if q.ID == id {
			return q, true
		}
	}
	return FormQuestion{}, false
}

// StartingAnswers are a new form's answers: each question's suggested answer,
// so an untouched form already holds what its creator proposed.
func (d FormDefinition) StartingAnswers() FormAnswers {
	out := FormAnswers{}
	for _, q := range d.Questions() {
		if q.Default != nil {
			out[q.ID] = FormAnswer{FormValue: *q.Default}
		}
	}
	return out
}

// CleanAnswers checks answers against the questions they are for and returns
// them tidied. It accepts answers to some of the questions, and incomplete
// ones, so it suits saving a form as it is filled in; Unanswered says what a
// submit still needs.
func (d FormDefinition) CleanAnswers(in FormAnswers) (FormAnswers, error) {
	ids := make([]string, 0, len(in))
	for id := range in {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := FormAnswers{}
	for _, id := range ids {
		q, ok := d.Question(id)
		if !ok {
			return nil, formError(id, "the form has no question with this id")
		}
		value, problem := q.cleanValue(in[id].FormValue)
		if problem != "" {
			return nil, formError(id, "%s", problem)
		}
		note := strings.TrimSpace(in[id].Note)
		if utf8.RuneCountInString(note) > FormMaxNoteLength {
			return nil, formError(id, "a note can be at most %d characters", FormMaxNoteLength)
		}
		out[id] = FormAnswer{FormValue: value, Note: note}
	}
	return out, nil
}

// Merged returns the answers with changes laid over them, one whole question
// at a time. A change that empties a question removes its entry.
func (a FormAnswers) Merged(changes FormAnswers) FormAnswers {
	out := FormAnswers{}
	for id, answer := range a {
		out[id] = answer
	}
	for id, answer := range changes {
		if answer.isZero() && answer.Note == "" {
			delete(out, id)
			continue
		}
		out[id] = answer
	}
	return out
}

// Unanswered lists, in form order, the questions that stop the form being
// submitted with these answers.
func (d FormDefinition) Unanswered(a FormAnswers) []string {
	var out []string
	for _, q := range d.Questions() {
		if q.blocksSubmit(a[q.ID]) {
			out = append(out, q.ID)
		}
	}
	return out
}

// SectionUnanswered is Unanswered split by section: the ids that block
// submitting in each section, in section order. A section with none is
// complete.
func (d FormDefinition) SectionUnanswered(a FormAnswers) [][]string {
	out := make([][]string, len(d.Sections))
	for i, section := range d.Sections {
		for _, q := range section.Questions {
			if q.blocksSubmit(a[q.ID]) {
				out[i] = append(out[i], q.ID)
			}
		}
	}
	return out
}

// FirstIncompleteSection is the index of the first section with a question
// that blocks submitting, or -1 when there is none.
func (d FormDefinition) FirstIncompleteSection(a FormAnswers) int {
	for i, ids := range d.SectionUnanswered(a) {
		if len(ids) > 0 {
			return i
		}
	}
	return -1
}

// CheckComplete returns a FormIncompleteError unless the answers are enough
// to submit the form.
func (d FormDefinition) CheckComplete(a FormAnswers) error {
	if ids := d.Unanswered(a); len(ids) > 0 {
		return &FormIncompleteError{QuestionIDs: ids}
	}
	return nil
}

// Progress counts the required questions and how many of them are answered.
func (d FormDefinition) Progress(a FormAnswers) (answered, required int) {
	for _, q := range d.Questions() {
		if !q.IsRequired() {
			continue
		}
		required++
		if q.Answered(a[q.ID]) {
			answered++
		}
	}
	return answered, required
}

// FormSelectedOption is a chosen option with the label the person saw.
type FormSelectedOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// FormAnswerView is one question with its answer, readable without the
// definition: what was asked, what was chosen by label, and whether the answer
// differs from the one the form suggested.
type FormAnswerView struct {
	ID                 string               `json:"id"`
	Section            string               `json:"section,omitempty"`
	Label              string               `json:"label"`
	Type               FormQuestionType     `json:"type"`
	Required           bool                 `json:"required"`
	Answered           bool                 `json:"answered"`
	Selected           []FormSelectedOption `json:"selected,omitempty"`
	Other              string               `json:"other,omitempty"`
	Checked            *bool                `json:"checked,omitempty"`
	Text               string               `json:"text,omitempty"`
	Note               string               `json:"note,omitempty"`
	ChangedFromDefault bool                 `json:"changed_from_default"`
}

// AnswerViews returns every question with its answer, in form order. A
// question with no suggested answer never counts as changed from it.
func (d FormDefinition) AnswerViews(a FormAnswers) []FormAnswerView {
	var out []FormAnswerView
	for _, section := range d.Sections {
		for _, q := range section.Questions {
			answer := a[q.ID]
			view := FormAnswerView{
				ID:       q.ID,
				Section:  section.Title,
				Label:    q.Label,
				Type:     q.Type,
				Required: q.IsRequired(),
				Answered: q.Answered(answer),
				Other:    answer.Other,
				Text:     answer.Text,
				Note:     answer.Note,
			}
			for _, id := range answer.Selected {
				view.Selected = append(view.Selected, FormSelectedOption{ID: id, Label: q.optionLabel(id)})
			}
			if q.Type == FormQuestionConfirm {
				checked := answer.Checked
				view.Checked = &checked
			}
			if q.Default != nil {
				view.ChangedFromDefault = !answer.FormValue.equal(*q.Default)
			}
			out = append(out, view)
		}
	}
	return out
}

// Form is a set of questions an agent asks a person, with the person's
// answers. Forms belong to a project and are for its members only.
type Form struct {
	ID              uuid.UUID          `json:"id"`
	ProjectID       uuid.UUID          `json:"project_id"`
	ProjectKey      string             `json:"project_key"`
	OwnerUsername   string             `json:"owner_username"`
	Number          int                `json:"number"`
	Ref             string             `json:"ref"`
	IssueID         *uuid.UUID         `json:"issue_id"`
	IssueIdentifier *string            `json:"issue"`
	Title           string             `json:"title"`
	Description     string             `json:"description"`
	Definition      FormDefinition     `json:"definition"`
	Answers         FormAnswers        `json:"answers"`
	Comment         string             `json:"comment"`
	Status          FormStatus         `json:"status"`
	AssigneeID      uuid.UUID          `json:"assignee_id"`
	CreatedByID     uuid.UUID          `json:"created_by_id"`
	OriginLabel     string             `json:"origin_label"`
	OriginURL       string             `json:"origin_url"`
	SubmittedVia    *FormSubmitSurface `json:"submitted_via"`
	SubmittedAt     *time.Time         `json:"submitted_at"`
	AcknowledgedAt  *time.Time         `json:"acknowledged_at"`
	CancelledAt     *time.Time         `json:"cancelled_at"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	// Attachments are the files the form carries, by object number. The
	// store fills them in when it reads one form; a list row has none. They
	// are left out of the JSON because the API describes them its own way
	// (a name, a kind and a link), and the storage object holds locations.
	Attachments []FormAttachment `json:"-"`
}

// FormAttachment is a file a form carries: an upload a form's creator named
// when they created it.
type FormAttachment struct {
	ID              uuid.UUID     `json:"id"`
	FormID          uuid.UUID     `json:"form_id"`
	StorageObjectID uuid.UUID     `json:"storage_object_id"`
	Object          StorageObject `json:"object"`
	CreatedByID     uuid.UUID     `json:"created_by_id"`
	CreatedAt       time.Time     `json:"created_at"`
}

// FormSummary is a list row: a form without its questions and answers, with
// the counts a list shows in their place.
type FormSummary struct {
	ID               uuid.UUID  `json:"id"`
	ProjectID        uuid.UUID  `json:"project_id"`
	ProjectKey       string     `json:"project_key"`
	OwnerUsername    string     `json:"owner_username"`
	Number           int        `json:"number"`
	Ref              string     `json:"ref"`
	IssueID          *uuid.UUID `json:"issue_id"`
	IssueIdentifier  *string    `json:"issue"`
	Title            string     `json:"title"`
	Status           FormStatus `json:"status"`
	AssigneeID       uuid.UUID  `json:"assignee_id"`
	CreatedByID      uuid.UUID  `json:"created_by_id"`
	OriginLabel      string     `json:"origin_label"`
	OriginURL        string     `json:"origin_url"`
	QuestionCount    int        `json:"question_count"`
	RequiredCount    int        `json:"required_count"`
	RequiredAnswered int        `json:"required_answered"`
	SubmittedAt      *time.Time `json:"submitted_at"`
	AcknowledgedAt   *time.Time `json:"acknowledged_at"`
	CancelledAt      *time.Time `json:"cancelled_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// Summary reduces a form to its list row.
func (f Form) Summary() FormSummary {
	answered, required := f.Definition.Progress(f.Answers)
	return FormSummary{
		ID:               f.ID,
		ProjectID:        f.ProjectID,
		ProjectKey:       f.ProjectKey,
		OwnerUsername:    f.OwnerUsername,
		Number:           f.Number,
		Ref:              f.Ref,
		IssueID:          f.IssueID,
		IssueIdentifier:  f.IssueIdentifier,
		Title:            f.Title,
		Status:           f.Status,
		AssigneeID:       f.AssigneeID,
		CreatedByID:      f.CreatedByID,
		OriginLabel:      f.OriginLabel,
		OriginURL:        f.OriginURL,
		QuestionCount:    len(f.Definition.Questions()),
		RequiredCount:    required,
		RequiredAnswered: answered,
		SubmittedAt:      f.SubmittedAt,
		AcknowledgedAt:   f.AcknowledgedAt,
		CancelledAt:      f.CancelledAt,
		CreatedAt:        f.CreatedAt,
		UpdatedAt:        f.UpdatedAt,
	}
}

func FormRef(number int) string {
	return fmt.Sprintf("form-%d", number)
}

// parseFormMediaRef reads an object ref a form names, trimmed. Field is the
// path to report if it is not one.
func parseFormMediaRef(field, raw string) (int, error) {
	number, ok := ParseStorageObjectRef(strings.TrimSpace(raw))
	if !ok {
		return 0, formError(field, "%q is not an object ref: it must look like object-12", raw)
	}
	return number, nil
}

// validateFormMedia checks the files one question shows and returns their
// refs in canonical form. A file can be shown once per question.
func validateFormMedia(path string, in []string) ([]string, error) {
	if len(in) > FormMaxQuestionMedia {
		return nil, formError(path, "at most %d files per question", FormMaxQuestionMedia)
	}
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, len(in))
	seen := map[int]bool{}
	for i, raw := range in {
		field := fmt.Sprintf("%s[%d]", path, i)
		number, err := parseFormMediaRef(field, raw)
		if err != nil {
			return nil, err
		}
		if seen[number] {
			return nil, formError(field, "%s is listed twice", StorageObjectRef(number))
		}
		seen[number] = true
		out[i] = StorageObjectRef(number)
	}
	return out, nil
}

// MediaRefs lists the distinct files the questions and options show, in the
// order the form asks them: a question's own files, then each option's.
func (d FormDefinition) MediaRefs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(ref string) {
		if ref != "" && !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	for _, q := range d.Questions() {
		for _, ref := range q.Media {
			add(ref)
		}
		for _, option := range q.Options {
			add(option.Media)
		}
	}
	return out
}

// ParseFormAttachments reads the refs a form names in its attachments list
// into object numbers, in the order given with repeats left out. Field names
// the list in an error.
func ParseFormAttachments(refs []string) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	for i, raw := range refs {
		number, err := parseFormMediaRef(fmt.Sprintf("attachments[%d]", i), raw)
		if err != nil {
			return nil, err
		}
		if !seen[number] {
			seen[number] = true
			out = append(out, number)
		}
	}
	return out, nil
}

// FormMediaNumbers is every file a form carries as object numbers: the files
// its questions and options show, in form order, then the explicit
// attachments that are not among them. Each appears once, and a form takes at
// most FormMaxAttachments. The definition must be one ValidateFormDefinition
// returned.
func FormMediaNumbers(d FormDefinition, attachments []int) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	add := func(number int) {
		if !seen[number] {
			seen[number] = true
			out = append(out, number)
		}
	}
	for _, ref := range d.MediaRefs() {
		number, _ := ParseStorageObjectRef(ref) // validated with the definition
		add(number)
	}
	for _, number := range attachments {
		add(number)
	}
	if len(out) > FormMaxAttachments {
		return nil, formError("attachments", "a form can carry at most %d files, counting those its questions show", FormMaxAttachments)
	}
	return out, nil
}
