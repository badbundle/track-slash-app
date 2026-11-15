package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// testFormDefinition is a valid two-section form with one question of each
// type, used as the starting point for the tests below.
func testFormDefinition() FormDefinition {
	return FormDefinition{Sections: []FormSection{
		{
			Title:       " Release ",
			Description: " Pick how to ship. ",
			Questions: []FormQuestion{
				{
					ID:    "channel",
					Type:  FormQuestionSingleChoice,
					Label: " Which channel? ",
					Help:  " TestFlight is the safe one. ",
					Options: []FormOption{
						{ID: "testflight", Label: " TestFlight ", Description: " Internal testers first. "},
						{ID: "store", Label: "App Store"},
					},
					AllowOther: true,
					Default:    &FormValue{Selected: []string{"testflight"}},
				},
				{
					ID:    "regions",
					Type:  FormQuestionMultiChoice,
					Label: "Which regions?",
					Options: []FormOption{
						{ID: "uk", Label: "UK"},
						{ID: "us", Label: "US"},
						{ID: "eu", Label: "EU"},
					},
					Required: boolPtr(false),
					Default:  &FormValue{Selected: []string{"us", "uk", "us"}},
				},
			},
		},
		{
			Title: "Checks",
			Questions: []FormQuestion{
				{ID: "rotated", Type: FormQuestionConfirm, Label: "I rotated the key"},
				{ID: "notes", Type: FormQuestionText, Label: "Release notes", Multiline: true, Placeholder: " What changed? ", Default: &FormValue{Text: "  Bug fixes.  "}},
			},
		},
	}}
}

func mustValidFormDefinition(t *testing.T) FormDefinition {
	t.Helper()
	def, err := ValidateFormDefinition(testFormDefinition())
	if err != nil {
		t.Fatalf("ValidateFormDefinition: %v", err)
	}
	return def
}

func wantFormError(t *testing.T, err error, field, messagePart string) {
	t.Helper()
	var formErr *FormError
	if !errors.As(err, &formErr) {
		t.Fatalf("err = %v, want a *FormError for %s", err, field)
	}
	if formErr.Field != field || !strings.Contains(formErr.Message, messagePart) {
		t.Fatalf("err = %q (field %q), want field %q with %q", formErr.Message, formErr.Field, field, messagePart)
	}
}

func TestFormStatus(t *testing.T) {
	labels := map[FormStatus]string{
		FormStatusOpen:         "Open",
		FormStatusSubmitted:    "Submitted",
		FormStatusAcknowledged: "Picked up",
		FormStatusCancelled:    "Cancelled",
	}
	for status, label := range labels {
		if !status.Valid() || status.Label() != label {
			t.Errorf("%s: Valid = %v, Label = %q, want valid and %q", status, status.Valid(), status.Label(), label)
		}
	}
	if FormStatus("draft").Valid() || FormStatus("draft").Label() != "draft" {
		t.Errorf("unknown status must be invalid and labelled by its own name")
	}

	allowed := map[FormStatus][]FormStatus{
		FormStatusOpen:         {FormStatusSubmitted, FormStatusCancelled},
		FormStatusSubmitted:    {FormStatusAcknowledged, FormStatusOpen, FormStatusCancelled},
		FormStatusAcknowledged: nil,
		FormStatusCancelled:    nil,
	}
	all := []FormStatus{FormStatusOpen, FormStatusSubmitted, FormStatusAcknowledged, FormStatusCancelled}
	for from, targets := range allowed {
		for _, to := range all {
			want := false
			for _, target := range targets {
				want = want || target == to
			}
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("%s -> %s: CanTransitionTo = %v, want %v", from, to, got, want)
			}
		}
		if wantFinal := len(targets) == 0; from.Final() != wantFinal {
			t.Errorf("%s: Final = %v, want %v", from, from.Final(), wantFinal)
		}
	}
}

func TestFormSubmitSurfaceValid(t *testing.T) {
	if !FormSubmitWeb.Valid() || !FormSubmitAPI.Valid() || FormSubmitSurface("mcp").Valid() {
		t.Fatalf("only web and api are submit surfaces")
	}
}

func TestFormRef(t *testing.T) {
	if got := FormRef(12); got != "form-12" {
		t.Fatalf("FormRef(12) = %q", got)
	}
}

func TestFormErrorMessages(t *testing.T) {
	if got := (&FormError{Field: "title", Message: "required"}).Error(); got != "title: required" {
		t.Errorf("FormError with field = %q", got)
	}
	if got := (&FormError{Message: "bad"}).Error(); got != "bad" {
		t.Errorf("FormError without field = %q", got)
	}
	if got := (&FormIncompleteError{QuestionIDs: []string{"a", "b"}}).Error(); got != "questions still need an answer: a, b" {
		t.Errorf("FormIncompleteError = %q", got)
	}
}

func TestValidateFormDetails(t *testing.T) {
	got, err := ValidateFormDetails(FormDetails{
		Title:       "  Release decisions ",
		Description: " Blocked on **you**. ",
		OriginLabel: " Claude Code · release ",
		OriginURL:   " https://example.com/session/1?x=y ",
	})
	if err != nil {
		t.Fatalf("ValidateFormDetails: %v", err)
	}
	want := FormDetails{
		Title:       "Release decisions",
		Description: "Blocked on **you**.",
		OriginLabel: "Claude Code · release",
		OriginURL:   "https://example.com/session/1?x=y",
	}
	if got != want {
		t.Fatalf("details = %+v, want %+v", got, want)
	}
	if _, err := ValidateFormDetails(FormDetails{Title: strings.Repeat("é", FormMaxTitleLength)}); err != nil {
		t.Fatalf("a title of exactly the limit in characters, with no origin: %v", err)
	}

	cases := []struct {
		name    string
		in      FormDetails
		field   string
		message string
	}{
		{"empty title", FormDetails{Title: "   "}, "title", "required"},
		{"long title", FormDetails{Title: strings.Repeat("a", FormMaxTitleLength+1)}, "title", "at most 200"},
		{"long description", FormDetails{Title: "t", Description: strings.Repeat("a", FormMaxDescriptionLength+1)}, "description", "at most 20000"},
		{"long origin label", FormDetails{Title: "t", OriginLabel: strings.Repeat("a", FormMaxOriginLabelLength+1)}, "origin_label", "at most 200"},
		{"long origin url", FormDetails{Title: "t", OriginURL: "https://example.com/" + strings.Repeat("a", FormMaxOriginURLLength)}, "origin_url", "at most 2000"},
		{"unparseable origin url", FormDetails{Title: "t", OriginURL: "https://exa mple.com/%zz"}, "origin_url", "https link"},
		{"http origin url", FormDetails{Title: "t", OriginURL: "http://example.com/x"}, "origin_url", "https link"},
		{"script origin url", FormDetails{Title: "t", OriginURL: "javascript:alert(1)"}, "origin_url", "https link"},
		{"origin url without a host", FormDetails{Title: "t", OriginURL: "https:///path"}, "origin_url", "https link"},
		{"origin url with credentials", FormDetails{Title: "t", OriginURL: "https://trusted.example@evil.example/x"}, "origin_url", "https link"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateFormDetails(tc.in)
			wantFormError(t, err, tc.field, tc.message)
		})
	}
}

func TestValidateFormComment(t *testing.T) {
	if got, err := ValidateFormComment("  Ship it.  "); err != nil || got != "Ship it." {
		t.Fatalf("ValidateFormComment = %q, %v", got, err)
	}
	_, err := ValidateFormComment(strings.Repeat("a", FormMaxCommentLength+1))
	wantFormError(t, err, "comment", "at most 10000")
}

func TestValidateFormDefinitionTidiesAValidForm(t *testing.T) {
	def := mustValidFormDefinition(t)

	first := def.Sections[0]
	if first.Title != "Release" || first.Description != "Pick how to ship." {
		t.Fatalf("section = %q / %q, want trimmed", first.Title, first.Description)
	}
	channel := first.Questions[0]
	if channel.Label != "Which channel?" || channel.Help != "TestFlight is the safe one." {
		t.Fatalf("question = %q / %q, want trimmed", channel.Label, channel.Help)
	}
	if channel.Options[0].Label != "TestFlight" || channel.Options[0].Description != "Internal testers first." {
		t.Fatalf("option = %+v, want trimmed", channel.Options[0])
	}
	if !channel.IsRequired() || first.Questions[1].IsRequired() {
		t.Fatalf("a question is required unless it says required: false")
	}
	// A default is tidied like an answer: de-duplicated, in option order.
	if got := first.Questions[1].Default.Selected; !reflect.DeepEqual(got, []string{"uk", "us"}) {
		t.Fatalf("multi-choice default = %v, want [uk us]", got)
	}
	notes := def.Sections[1].Questions[1]
	if notes.Default.Text != "Bug fixes." || notes.Placeholder != "What changed?" {
		t.Fatalf("text question = %+v, want trimmed default and placeholder", notes)
	}
	if def.Sections[1].Questions[0].Default != nil {
		t.Fatalf("a question without a default keeps none")
	}

	if got := len(def.Questions()); got != 4 {
		t.Fatalf("Questions() = %d, want 4", got)
	}
	if q, ok := def.Question("rotated"); !ok || q.Type != FormQuestionConfirm {
		t.Fatalf("Question(rotated) = %+v, %v", q, ok)
	}
	if _, ok := def.Question("missing"); ok {
		t.Fatalf("Question(missing) must not be found")
	}
}

func TestValidateFormDefinitionKeepsOnlyMeaningfulDefaults(t *testing.T) {
	// A default that says nothing (an unticked confirm, blank text) is dropped,
	// so it never shows as a suggestion.
	def, err := ValidateFormDefinition(FormDefinition{Sections: []FormSection{{Questions: []FormQuestion{
		{ID: "go", Type: FormQuestionConfirm, Label: "Go ahead", Default: &FormValue{}},
		{ID: "why", Type: FormQuestionText, Label: "Why", Default: &FormValue{Text: "   "}},
		{ID: "ticked", Type: FormQuestionConfirm, Label: "Ticked", Default: &FormValue{Checked: true}},
	}}}})
	if err != nil {
		t.Fatalf("ValidateFormDefinition: %v", err)
	}
	questions := def.Questions()
	if questions[0].Default != nil || questions[1].Default != nil {
		t.Fatalf("empty defaults must be dropped: %+v %+v", questions[0].Default, questions[1].Default)
	}
	if questions[2].Default == nil || !questions[2].Default.Checked {
		t.Fatalf("a ticked confirm default is kept")
	}
	// One section needs no title.
	if def.Sections[0].Title != "" {
		t.Fatalf("single section title = %q", def.Sections[0].Title)
	}
}

func TestValidateFormDefinitionRefusals(t *testing.T) {
	choice := func(mutate func(q *FormQuestion)) FormDefinition {
		q := FormQuestion{ID: "q", Type: FormQuestionSingleChoice, Label: "Pick", Options: []FormOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}}
		mutate(&q)
		return FormDefinition{Sections: []FormSection{{Questions: []FormQuestion{q}}}}
	}
	text := func(mutate func(q *FormQuestion)) FormDefinition {
		q := FormQuestion{ID: "q", Type: FormQuestionText, Label: "Say"}
		mutate(&q)
		return FormDefinition{Sections: []FormSection{{Questions: []FormQuestion{q}}}}
	}
	confirm := func(mutate func(q *FormQuestion)) FormDefinition {
		q := FormQuestion{ID: "q", Type: FormQuestionConfirm, Label: "Done"}
		mutate(&q)
		return FormDefinition{Sections: []FormSection{{Questions: []FormQuestion{q}}}}
	}
	manyOptions := make([]FormOption, FormMaxOptions+1)
	for i := range manyOptions {
		manyOptions[i] = FormOption{ID: "o" + strings.Repeat("x", i), Label: "Option"}
	}
	manySections := make([]FormSection, FormMaxSections+1)
	for i := range manySections {
		manySections[i] = FormSection{Title: "S", Questions: []FormQuestion{{ID: "q" + strings.Repeat("x", i), Type: FormQuestionConfirm, Label: "Done"}}}
	}
	manyQuestions := make([]FormQuestion, FormMaxQuestions+1)
	for i := range manyQuestions {
		manyQuestions[i] = FormQuestion{ID: "q" + strings.Repeat("x", i%39) + string(rune('a'+i/39)), Type: FormQuestionConfirm, Label: "Done"}
	}
	// 40 questions of 20 options with full-length labels and descriptions pass
	// every per-field limit but not the size limit for the whole definition.
	huge := FormDefinition{Sections: []FormSection{{}}}
	for i := 0; i < FormMaxQuestions; i++ {
		q := FormQuestion{ID: "q" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Type: FormQuestionMultiChoice, Label: "Pick"}
		for j := 0; j < FormMaxOptions; j++ {
			q.Options = append(q.Options, FormOption{ID: "o" + string(rune('a'+j)), Label: strings.Repeat("l", FormMaxLabelLength), Description: strings.Repeat("d", FormMaxLabelLength)})
		}
		huge.Sections[0].Questions = append(huge.Sections[0].Questions, q)
	}

	cases := []struct {
		name    string
		def     FormDefinition
		field   string
		message string
	}{
		{"no sections", FormDefinition{}, "sections", "at least one section"},
		{"too many sections", FormDefinition{Sections: manySections}, "sections", "at most 12 sections"},
		{"untitled section among several", FormDefinition{Sections: []FormSection{
			{Title: "One", Questions: []FormQuestion{{ID: "a", Type: FormQuestionConfirm, Label: "A"}}},
			{Title: "  ", Questions: []FormQuestion{{ID: "b", Type: FormQuestionConfirm, Label: "B"}}},
		}}, "sections[1].title", "needs a title for each"},
		{"long section title", FormDefinition{Sections: []FormSection{{Title: strings.Repeat("a", FormMaxTitleLength+1), Questions: []FormQuestion{{ID: "a", Type: FormQuestionConfirm, Label: "A"}}}}}, "sections[0].title", "at most 200"},
		{"long section description", FormDefinition{Sections: []FormSection{{Description: strings.Repeat("a", FormMaxHelpLength+1), Questions: []FormQuestion{{ID: "a", Type: FormQuestionConfirm, Label: "A"}}}}}, "sections[0].description", "at most 2000"},
		{"empty section", FormDefinition{Sections: []FormSection{{Title: "One"}}}, "sections[0].questions", "at least one question"},
		{"too many questions", FormDefinition{Sections: []FormSection{{Questions: manyQuestions}}}, "sections", "at most 40 questions"},
		{"duplicate question id across sections", FormDefinition{Sections: []FormSection{
			{Title: "One", Questions: []FormQuestion{{ID: "same", Type: FormQuestionConfirm, Label: "A"}}},
			{Title: "Two", Questions: []FormQuestion{{ID: "same", Type: FormQuestionConfirm, Label: "B"}}},
		}}, "sections[1].questions[0].id", `"same" is used twice`},
		{"definition too large", huge, "sections", "too large"},

		{"bad question id", text(func(q *FormQuestion) { q.ID = "Not Valid" }), "sections[0].questions[0].id", "lowercase letters"},
		{"empty question id", text(func(q *FormQuestion) { q.ID = " " }), "sections[0].questions[0].id", "lowercase letters"},
		{"empty label", text(func(q *FormQuestion) { q.Label = " " }), "sections[0].questions[0].label", "required"},
		{"long label", text(func(q *FormQuestion) { q.Label = strings.Repeat("a", FormMaxLabelLength+1) }), "sections[0].questions[0].label", "at most 300"},
		{"long help", text(func(q *FormQuestion) { q.Help = strings.Repeat("a", FormMaxHelpLength+1) }), "sections[0].questions[0].help", "at most 2000"},
		{"long placeholder", text(func(q *FormQuestion) { q.Placeholder = strings.Repeat("a", FormMaxPlaceholderLength+1) }), "sections[0].questions[0].placeholder", "at most 200"},
		{"unknown type", text(func(q *FormQuestion) { q.Type = "date" }), "sections[0].questions[0].type", "single_choice, multi_choice, confirm or text"},

		{"choice with multiline", choice(func(q *FormQuestion) { q.Multiline = true }), "sections[0].questions[0]", "text questions only"},
		{"choice with placeholder", choice(func(q *FormQuestion) { q.Placeholder = "x" }), "sections[0].questions[0]", "text questions only"},
		{"choice with one option", choice(func(q *FormQuestion) { q.Options = q.Options[:1] }), "sections[0].questions[0].options", "2 to 20 options"},
		{"choice with too many options", choice(func(q *FormQuestion) { q.Options = manyOptions }), "sections[0].questions[0].options", "2 to 20 options"},
		{"bad option id", choice(func(q *FormQuestion) { q.Options[1].ID = "B!" }), "sections[0].questions[0].options[1].id", "lowercase letters"},
		{"reserved option id", choice(func(q *FormQuestion) { q.Options[1].ID = "other" }), "sections[0].questions[0].options[1].id", "reserved"},
		{"duplicate option id", choice(func(q *FormQuestion) { q.Options[1].ID = "a" }), "sections[0].questions[0].options[1].id", `"a" is used twice`},
		{"empty option label", choice(func(q *FormQuestion) { q.Options[0].Label = " " }), "sections[0].questions[0].options[0].label", "required"},
		{"long option label", choice(func(q *FormQuestion) { q.Options[0].Label = strings.Repeat("a", FormMaxLabelLength+1) }), "sections[0].questions[0].options[0].label", "at most 300"},
		{"long option description", choice(func(q *FormQuestion) { q.Options[0].Description = strings.Repeat("a", FormMaxLabelLength+1) }), "sections[0].questions[0].options[0].description", "at most 300"},
		{"choice default names a missing option", choice(func(q *FormQuestion) { q.Default = &FormValue{Selected: []string{"z"}} }), "sections[0].questions[0].default", `unknown option "z"`},
		{"choice default of the wrong shape", choice(func(q *FormQuestion) { q.Default = &FormValue{Checked: true} }), "sections[0].questions[0].default", "selected option ids"},

		{"confirm with options", confirm(func(q *FormQuestion) { q.Options = []FormOption{{ID: "a", Label: "A"}} }), "sections[0].questions[0]", "choice questions only"},
		{"confirm with allow_other", confirm(func(q *FormQuestion) { q.AllowOther = true }), "sections[0].questions[0]", "choice questions only"},
		{"confirm with multiline", confirm(func(q *FormQuestion) { q.Multiline = true }), "sections[0].questions[0]", "text questions only"},
		{"confirm with placeholder", confirm(func(q *FormQuestion) { q.Placeholder = "x" }), "sections[0].questions[0]", "text questions only"},
		{"confirm default of the wrong shape", confirm(func(q *FormQuestion) { q.Default = &FormValue{Text: "yes"} }), "sections[0].questions[0].default", "takes checked"},

		{"text with options", text(func(q *FormQuestion) { q.Options = []FormOption{{ID: "a", Label: "A"}} }), "sections[0].questions[0]", "choice questions only"},
		{"text with allow_other", text(func(q *FormQuestion) { q.AllowOther = true }), "sections[0].questions[0]", "choice questions only"},
		{"text default of the wrong shape", text(func(q *FormQuestion) { q.Default = &FormValue{Selected: []string{"a"}} }), "sections[0].questions[0].default", "takes text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateFormDefinition(tc.def)
			wantFormError(t, err, tc.field, tc.message)
		})
	}
}

func TestFormStartingAnswersAreTheDefaults(t *testing.T) {
	def := mustValidFormDefinition(t)
	got := def.StartingAnswers()
	want := FormAnswers{
		"channel": {FormValue: FormValue{Selected: []string{"testflight"}}},
		"regions": {FormValue: FormValue{Selected: []string{"uk", "us"}}},
		"notes":   {FormValue: FormValue{Text: "Bug fixes."}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StartingAnswers = %+v, want %+v", got, want)
	}
	// The suggested answers leave only the confirm box to tick.
	if ids := def.Unanswered(got); !reflect.DeepEqual(ids, []string{"rotated"}) {
		t.Fatalf("Unanswered(starting answers) = %v, want [rotated]", ids)
	}
}

func TestFormCleanAnswersTidiesEachType(t *testing.T) {
	def := mustValidFormDefinition(t)
	got, err := def.CleanAnswers(FormAnswers{
		"channel": {FormValue: FormValue{Selected: []string{" other "}, Other: "  A private beta  "}, Note: "  Ask Sam first.  "},
		"regions": {FormValue: FormValue{Selected: []string{"eu", "uk", "eu"}}},
		"rotated": {FormValue: FormValue{Checked: true}},
		"notes":   {FormValue: FormValue{Text: "  Faster sync.\nFewer crashes.  "}},
	})
	if err != nil {
		t.Fatalf("CleanAnswers: %v", err)
	}
	want := FormAnswers{
		"channel": {FormValue: FormValue{Selected: []string{"other"}, Other: "A private beta"}, Note: "Ask Sam first."},
		"regions": {FormValue: FormValue{Selected: []string{"uk", "eu"}}},
		"rotated": {FormValue: FormValue{Checked: true}},
		"notes":   {FormValue: FormValue{Text: "Faster sync.\nFewer crashes."}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CleanAnswers = %+v, want %+v", got, want)
	}
	if ids := def.Unanswered(got); len(ids) != 0 {
		t.Fatalf("Unanswered = %v, want none", ids)
	}
	if err := def.CheckComplete(got); err != nil {
		t.Fatalf("CheckComplete: %v", err)
	}

	// A draft may leave questions out, or leave them empty.
	draft, err := def.CleanAnswers(FormAnswers{
		"channel": {},
		"regions": {FormValue: FormValue{Selected: []string{"us", "other"}}},
	})
	if err == nil {
		t.Fatalf("regions has no Other option, got %+v", draft)
	}
	draft, err = def.CleanAnswers(FormAnswers{"channel": {}, "notes": {FormValue: FormValue{Text: "   "}}})
	if err != nil {
		t.Fatalf("CleanAnswers(draft): %v", err)
	}
	if !reflect.DeepEqual(draft, FormAnswers{"channel": {}, "notes": {}}) {
		t.Fatalf("draft = %+v, want two empty answers", draft)
	}
	empty, err := def.CleanAnswers(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("CleanAnswers(nil) = %+v, %v", empty, err)
	}
}

func TestFormCleanAnswersRefusals(t *testing.T) {
	def := mustValidFormDefinition(t)
	cases := []struct {
		name    string
		answers FormAnswers
		field   string
		message string
	}{
		{"unknown question", FormAnswers{"nope": {FormValue: FormValue{Checked: true}}}, "nope", "no question with this id"},
		{"unknown option", FormAnswers{"channel": {FormValue: FormValue{Selected: []string{"beta"}}}}, "channel", `unknown option "beta"`},
		{"other where it is not offered", FormAnswers{"regions": {FormValue: FormValue{Selected: []string{"other"}, Other: "Mars"}}}, "regions", "no Other option"},
		{"two options for a single choice", FormAnswers{"channel": {FormValue: FormValue{Selected: []string{"store", "testflight"}}}}, "channel", "pick one option"},
		{"other text without the other option", FormAnswers{"channel": {FormValue: FormValue{Selected: []string{"store"}, Other: "A beta"}}}, "channel", "needs the other option selected"},
		{"long other text", FormAnswers{"channel": {FormValue: FormValue{Selected: []string{"other"}, Other: strings.Repeat("a", FormMaxOtherLength+1)}}}, "channel", "at most 2000"},
		{"checked on a choice", FormAnswers{"channel": {FormValue: FormValue{Checked: true}}}, "channel", "selected option ids"},
		{"text on a choice", FormAnswers{"regions": {FormValue: FormValue{Text: "UK"}}}, "regions", "selected option ids"},
		{"selected on a confirm", FormAnswers{"rotated": {FormValue: FormValue{Selected: []string{"yes"}}}}, "rotated", "takes checked"},
		{"other on a confirm", FormAnswers{"rotated": {FormValue: FormValue{Other: "yes"}}}, "rotated", "takes checked"},
		{"text on a confirm", FormAnswers{"rotated": {FormValue: FormValue{Text: "yes"}}}, "rotated", "takes checked"},
		{"selected on a text", FormAnswers{"notes": {FormValue: FormValue{Selected: []string{"a"}}}}, "notes", "takes text"},
		{"other on a text", FormAnswers{"notes": {FormValue: FormValue{Other: "a"}}}, "notes", "takes text"},
		{"checked on a text", FormAnswers{"notes": {FormValue: FormValue{Checked: true}}}, "notes", "takes text"},
		{"long text", FormAnswers{"notes": {FormValue: FormValue{Text: strings.Repeat("a", FormMaxTextAnswerLength+1)}}}, "notes", "at most 10000"},
		{"long note", FormAnswers{"rotated": {Note: strings.Repeat("a", FormMaxNoteLength+1)}}, "rotated", "note can be at most 2000"},
		// Problems are reported in question-id order, so the same input always names the same one.
		{"first problem by id", FormAnswers{"zzz": {}, "aaa": {}}, "aaa", "no question with this id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := def.CleanAnswers(tc.answers)
			wantFormError(t, err, tc.field, tc.message)
		})
	}
}

func TestFormAnswersMerged(t *testing.T) {
	current := FormAnswers{
		"channel": {FormValue: FormValue{Selected: []string{"testflight"}}},
		"rotated": {FormValue: FormValue{Checked: true}},
		"notes":   {FormValue: FormValue{Text: "Bug fixes."}},
	}
	got := current.Merged(FormAnswers{
		"channel": {FormValue: FormValue{Selected: []string{"store"}}, Note: "Ship it"},
		"rotated": {},              // unticked: the entry goes
		"regions": {Note: "Later"}, // a note alone is kept
	})
	want := FormAnswers{
		"channel": {FormValue: FormValue{Selected: []string{"store"}}, Note: "Ship it"},
		"notes":   {FormValue: FormValue{Text: "Bug fixes."}},
		"regions": {Note: "Later"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Merged = %+v, want %+v", got, want)
	}
	if len(current) != 3 || !current["rotated"].Checked {
		t.Fatalf("Merged must not change its receiver: %+v", current)
	}
	if got := FormAnswers(nil).Merged(nil); len(got) != 0 {
		t.Fatalf("Merged of nothing = %+v", got)
	}
}

func TestFormUnansweredAndProgress(t *testing.T) {
	def := mustValidFormDefinition(t)

	// Nothing answered: the three required questions block, the optional one does not.
	if ids := def.Unanswered(nil); !reflect.DeepEqual(ids, []string{"channel", "rotated", "notes"}) {
		t.Fatalf("Unanswered(nil) = %v", ids)
	}
	if answered, required := def.Progress(nil); answered != 0 || required != 3 {
		t.Fatalf("Progress(nil) = %d of %d, want 0 of 3", answered, required)
	}

	answers := FormAnswers{
		// Other chosen with nothing written is not an answer yet.
		"channel": {FormValue: FormValue{Selected: []string{"other"}}},
		"rotated": {FormValue: FormValue{Checked: true}},
		"notes":   {FormValue: FormValue{Text: "Done."}},
	}
	if ids := def.Unanswered(answers); !reflect.DeepEqual(ids, []string{"channel"}) {
		t.Fatalf("Unanswered = %v, want [channel]", ids)
	}
	if answered, required := def.Progress(answers); answered != 2 || required != 3 {
		t.Fatalf("Progress = %d of %d, want 2 of 3", answered, required)
	}
	var incomplete *FormIncompleteError
	if err := def.CheckComplete(answers); !errors.As(err, &incomplete) || !reflect.DeepEqual(incomplete.QuestionIDs, []string{"channel"}) {
		t.Fatalf("CheckComplete = %v, want FormIncompleteError for channel", err)
	}

	answers["channel"] = FormAnswer{FormValue: FormValue{Selected: []string{"other"}, Other: "A beta"}}
	if ids := def.Unanswered(answers); len(ids) != 0 {
		t.Fatalf("Unanswered = %v, want none", ids)
	}
	if answered, required := def.Progress(answers); answered != 3 || required != 3 {
		t.Fatalf("Progress = %d of %d, want 3 of 3", answered, required)
	}
}

func TestOptionalChoiceLeftOnOtherBlocksSubmit(t *testing.T) {
	def, err := ValidateFormDefinition(FormDefinition{Sections: []FormSection{{Questions: []FormQuestion{{
		ID:         "extra",
		Type:       FormQuestionMultiChoice,
		Label:      "Anything extra?",
		Required:   boolPtr(false),
		AllowOther: true,
		Options:    []FormOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
	}}}}})
	if err != nil {
		t.Fatalf("ValidateFormDefinition: %v", err)
	}
	if ids := def.Unanswered(nil); len(ids) != 0 {
		t.Fatalf("an optional question left alone must not block: %v", ids)
	}
	onOther := FormAnswers{"extra": {FormValue: FormValue{Selected: []string{"a", "other"}}}}
	if ids := def.Unanswered(onOther); !reflect.DeepEqual(ids, []string{"extra"}) {
		t.Fatalf("Other chosen with no text must block even when optional: %v", ids)
	}
	if answered, required := def.Progress(onOther); answered != 0 || required != 0 {
		t.Fatalf("Progress counts required questions only: %d of %d", answered, required)
	}
}

func TestFormAnswerViews(t *testing.T) {
	def := mustValidFormDefinition(t)
	views := def.AnswerViews(FormAnswers{
		"channel": {FormValue: FormValue{Selected: []string{"other"}, Other: "A private beta"}, Note: "Ask Sam first."},
		"regions": {FormValue: FormValue{Selected: []string{"uk", "us"}}},
		"notes":   {FormValue: FormValue{Text: "Bug fixes."}},
	})
	if len(views) != 4 {
		t.Fatalf("views = %d, want one per question", len(views))
	}

	channel := views[0]
	if channel.ID != "channel" || channel.Section != "Release" || channel.Label != "Which channel?" || channel.Type != FormQuestionSingleChoice {
		t.Fatalf("channel view = %+v", channel)
	}
	if !channel.Required || !channel.Answered || !channel.ChangedFromDefault {
		t.Fatalf("channel: required %v answered %v changed %v, want all true", channel.Required, channel.Answered, channel.ChangedFromDefault)
	}
	if !reflect.DeepEqual(channel.Selected, []FormSelectedOption{{ID: "other", Label: "Other"}}) || channel.Other != "A private beta" || channel.Note != "Ask Sam first." {
		t.Fatalf("channel answer = %+v", channel)
	}
	if channel.Checked != nil {
		t.Fatalf("only a confirm question reports checked")
	}

	regions := views[1]
	if regions.Required || !regions.Answered || regions.ChangedFromDefault {
		t.Fatalf("regions kept its suggestion: %+v", regions)
	}
	if !reflect.DeepEqual(regions.Selected, []FormSelectedOption{{ID: "uk", Label: "UK"}, {ID: "us", Label: "US"}}) {
		t.Fatalf("regions selected = %+v", regions.Selected)
	}

	rotated := views[2]
	if rotated.Section != "Checks" || rotated.Checked == nil || *rotated.Checked || rotated.Answered || rotated.ChangedFromDefault {
		t.Fatalf("rotated view = %+v, want an unticked box that suggested nothing", rotated)
	}

	notes := views[3]
	if notes.Text != "Bug fixes." || !notes.Answered || notes.ChangedFromDefault {
		t.Fatalf("notes view = %+v", notes)
	}

	// An id that is no option of the question is shown as it is.
	stray := def.AnswerViews(FormAnswers{"channel": {FormValue: FormValue{Selected: []string{"gone"}}}})
	if !reflect.DeepEqual(stray[0].Selected, []FormSelectedOption{{ID: "gone", Label: "gone"}}) {
		t.Fatalf("stray option = %+v", stray[0].Selected)
	}

	if got := (FormDefinition{}).AnswerViews(nil); got != nil {
		t.Fatalf("a form with no questions has no views: %+v", got)
	}
}

func TestFormAnswerJSONShape(t *testing.T) {
	// The note sits beside the value's fields, not nested under them.
	raw, err := json.Marshal(FormAnswers{"channel": {FormValue: FormValue{Selected: []string{"other"}, Other: "Beta"}, Note: "Why"}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, want := string(raw), `{"channel":{"selected":["other"],"other":"Beta","note":"Why"}}`; got != want {
		t.Fatalf("answers JSON = %s, want %s", got, want)
	}
	var back FormAnswers
	if err := json.Unmarshal(raw, &back); err != nil || back["channel"].Other != "Beta" || back["channel"].Note != "Why" {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
}

func TestFormSummary(t *testing.T) {
	def := mustValidFormDefinition(t)
	issue := "TRACK-12"
	form := Form{
		Number:          3,
		Ref:             FormRef(3),
		ProjectKey:      "TRACK",
		OwnerUsername:   "badbundle",
		IssueIdentifier: &issue,
		Title:           "Release decisions",
		Status:          FormStatusOpen,
		OriginLabel:     "Claude Code",
		OriginURL:       "https://example.com/s/1",
		Definition:      def,
		Answers:         def.StartingAnswers(),
	}
	got := form.Summary()
	if got.Ref != "form-3" || got.ProjectKey != "TRACK" || got.OwnerUsername != "badbundle" || *got.IssueIdentifier != "TRACK-12" {
		t.Fatalf("summary identity = %+v", got)
	}
	if got.Title != form.Title || got.Status != FormStatusOpen || got.OriginLabel != "Claude Code" || got.OriginURL != form.OriginURL {
		t.Fatalf("summary fields = %+v", got)
	}
	if got.QuestionCount != 4 || got.RequiredCount != 3 || got.RequiredAnswered != 2 {
		t.Fatalf("summary counts = %d questions, %d of %d required answered; want 4, 2 of 3", got.QuestionCount, got.RequiredAnswered, got.RequiredCount)
	}
}
