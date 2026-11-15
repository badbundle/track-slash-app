package server

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestUIFormOriginHref(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw, href, host string
	}{
		{"https with path", "https://chat.example.com/t/1?x=2", "https://chat.example.com/t/1?x=2", "chat.example.com"},
		{"port is not part of the host", "https://example.com:8443/a", "https://example.com:8443/a", "example.com"},
		{"surrounding space", "  https://example.com/a ", "https://example.com/a", "example.com"},
		{"http", "http://example.com/a", "", ""},
		{"javascript", "javascript:alert(1)", "", ""},
		{"no host", "https:///path", "", ""},
		{"relative", "/forms/1", "", ""},
		{"empty", "", "", ""},
		{"unparseable", "https://exa mple.com/%zz", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			href, host := uiFormOriginHref(tc.raw)
			if href != tc.href || host != tc.host {
				t.Fatalf("uiFormOriginHref(%q) = %q, %q; want %q, %q", tc.raw, href, host, tc.href, tc.host)
			}
		})
	}
	if got := uiFormOrigin("Chat", "http://insecure.example.com"); got.Label != "Chat" || got.Href != "" {
		t.Fatalf("origin with an unsafe URL = %+v", got)
	}
}

func uiFormCopyTestForm() model.Form {
	no := false
	return model.Form{
		Ref:   "form-7",
		Title: "Release decisions",
		Definition: model.FormDefinition{Sections: []model.FormSection{{Questions: []model.FormQuestion{
			{ID: "env", Type: model.FormQuestionSingleChoice, Label: "Environment?", Options: []model.FormOption{{ID: "a", Label: "Staging"}, {ID: "b", Label: "Prod"}}, AllowOther: true},
			{ID: "regions", Type: model.FormQuestionMultiChoice, Label: "Regions?", Options: []model.FormOption{{ID: "eu", Label: "Europe"}, {ID: "us", Label: "US"}}},
			{ID: "ok", Type: model.FormQuestionConfirm, Label: "Keys rotated?"},
			{ID: "why", Type: model.FormQuestionText, Label: "Why?", Required: &no},
			{ID: "skip", Type: model.FormQuestionText, Label: "Skipped?", Required: &no},
		}}}},
		Answers: model.FormAnswers{
			"env":     {FormValue: model.FormValue{Selected: []string{"a"}}, Note: "Safe"},
			"regions": {FormValue: model.FormValue{Selected: []string{"eu", "other"}, Other: "Mars"}},
			"ok":      {FormValue: model.FormValue{Checked: true}},
			"why":     {FormValue: model.FormValue{Text: "line one\nline two"}},
		},
	}
}

func TestUIFormCopyText(t *testing.T) {
	t.Parallel()
	form := uiFormCopyTestForm()
	for _, tc := range []struct {
		name    string
		comment string
		want    string
	}{
		{
			name: "without a comment",
			want: "form-7 · Release decisions\nhttps://t.example/forms/form-7\n\n" +
				"1. Environment?: Staging\n   Note: Safe\n" +
				"2. Regions?: Europe, Other: Mars\n" +
				"3. Keys rotated?: Yes\n" +
				"4. Why?: line one\nline two\n" +
				"5. Skipped?: No answer",
		},
		{
			name:    "with a comment",
			comment: "Ship Monday.",
			want: "form-7 · Release decisions\nhttps://t.example/forms/form-7\n\n" +
				"1. Environment?: Staging\n   Note: Safe\n" +
				"2. Regions?: Europe, Other: Mars\n" +
				"3. Keys rotated?: Yes\n" +
				"4. Why?: line one\nline two\n" +
				"5. Skipped?: No answer\n\nAnything else: Ship Monday.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := form
			f.Comment = tc.comment
			if got := uiFormCopyText(f, "https://t.example/forms/form-7"); got != tc.want {
				t.Fatalf("copy text =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestUIFormAnswerFromValues(t *testing.T) {
	t.Parallel()
	choice := model.FormQuestion{Type: model.FormQuestionSingleChoice}
	for _, tc := range []struct {
		name   string
		q      model.FormQuestion
		values url.Values
		want   model.FormAnswer
	}{
		{"choice", choice, url.Values{"selected": {"a"}}, model.FormAnswer{FormValue: model.FormValue{Selected: []string{"a"}}}},
		{"other kept while chosen", choice, url.Values{"selected": {"other"}, "other": {"Blue"}}, model.FormAnswer{FormValue: model.FormValue{Selected: []string{"other"}, Other: "Blue"}}},
		{"other dropped when not chosen", choice, url.Values{"selected": {"a"}, "other": {"stale"}}, model.FormAnswer{FormValue: model.FormValue{Selected: []string{"a"}}}},
		{"multi", model.FormQuestion{Type: model.FormQuestionMultiChoice}, url.Values{"selected": {"a", "b", "other"}, "other": {"x"}, "note": {"n"}}, model.FormAnswer{FormValue: model.FormValue{Selected: []string{"a", "b", "other"}, Other: "x"}, Note: "n"}},
		{"nothing chosen", choice, url.Values{"other": {"stale"}}, model.FormAnswer{}},
		{"confirm ticked", model.FormQuestion{Type: model.FormQuestionConfirm}, url.Values{"checked": {"true"}}, model.FormAnswer{FormValue: model.FormValue{Checked: true}}},
		{"confirm unticked", model.FormQuestion{Type: model.FormQuestionConfirm}, url.Values{"checked": {"false"}}, model.FormAnswer{}},
		{"text", model.FormQuestion{Type: model.FormQuestionText}, url.Values{"text": {"hi"}, "selected": {"a"}}, model.FormAnswer{FormValue: model.FormValue{Text: "hi"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := uiFormAnswerFromValues(tc.q, tc.values)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("answer = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUIFormProgressAndNotice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		p    uiFormProgressData
		want string
	}{
		{uiFormProgressData{Answered: 2, Required: 3}, "2 of 3 answered"},
		{uiFormProgressData{Answered: 0, Required: 0}, "No required questions"},
	} {
		if got := tc.p.Text(); got != tc.want {
			t.Fatalf("Text() = %q, want %q", got, tc.want)
		}
	}
	if got := uiFormIncompleteNotice(1); got != "1 question still needs an answer." {
		t.Fatalf("notice(1) = %q", got)
	}
	if got := uiFormIncompleteNotice(3); got != "3 questions still need an answer." {
		t.Fatalf("notice(3) = %q", got)
	}
	unticked := false
	if got := uiFormAnswerText(model.FormAnswerView{Type: model.FormQuestionConfirm, Answered: true, Checked: &unticked}); got != "No" {
		t.Fatalf("confirm text = %q", got)
	}
}

func uiFormStepTestDefinition() model.FormDefinition {
	return model.FormDefinition{Sections: []model.FormSection{
		{Title: "One", Questions: []model.FormQuestion{{ID: "a", Type: model.FormQuestionText, Label: "A"}}},
		{Questions: []model.FormQuestion{{ID: "b", Type: model.FormQuestionText, Label: "B"}}},
		{Title: "Three", Questions: []model.FormQuestion{{ID: "c", Type: model.FormQuestionText, Label: "C"}}},
	}}
}

func TestUIFormResolveStep(t *testing.T) {
	t.Parallel()
	def := uiFormStepTestDefinition()
	text := func(v string) model.FormAnswer { return model.FormAnswer{FormValue: model.FormValue{Text: v}} }
	for _, tc := range []struct {
		name    string
		raw     string
		answers model.FormAnswers
		want    int
	}{
		{"landing on the first incomplete section", "", model.FormAnswers{"a": text("x")}, 2},
		{"landing on review when all answered", "", model.FormAnswers{"a": text("x"), "b": text("y"), "c": text("z")}, 4},
		{"landing on step 1 when nothing answered", "", nil, 1},
		{"a section number", "3", nil, 3},
		{"review", "review", nil, 4},
		{"zero", "0", nil, 1},
		{"past review", "99", nil, 1},
		{"review as a number", "4", nil, 1},
		{"negative", "-1", nil, 1},
		{"garbage", "abc", nil, 1},
	} {
		if got := uiFormResolveStep(def, tc.answers, tc.raw); got != tc.want {
			t.Errorf("%s: uiFormResolveStep(%q) = %d, want %d", tc.name, tc.raw, got, tc.want)
		}
	}
}

func TestUIFormBuildStepper(t *testing.T) {
	t.Parallel()
	def := uiFormStepTestDefinition()
	answers := model.FormAnswers{"a": {FormValue: model.FormValue{Text: "x"}}}
	states := func(s *uiFormStepper) string {
		var out []string
		for _, step := range s.Steps {
			out = append(out, step.Key+"="+step.State)
		}
		return strings.Join(out, " ")
	}
	got := uiFormBuildStepper("/f", def, answers, 2)
	if want := "1=complete 2=current 3=incomplete review=incomplete"; states(got) != want {
		t.Fatalf("states on step 2 = %q, want %q", states(got), want)
	}
	if got.Text != "Step 2 of 4" || got.Heading != "Section 2" || got.Steps[1].Title != "Section 2" {
		t.Fatalf("step 2 text = %q heading = %q", got.Text, got.Heading)
	}
	if got.Steps[2].Href != "/f?step=3" || got.Steps[3].PanelHref != "/f/panel?step=review" || !got.Steps[3].Review {
		t.Fatalf("links = %+v", got.Steps)
	}
	all := model.FormAnswers{"a": answers["a"], "b": answers["a"], "c": answers["a"]}
	got = uiFormBuildStepper("/f", def, all, 4)
	if want := "1=complete 2=complete 3=complete review=current"; states(got) != want {
		t.Fatalf("states on review = %q, want %q", states(got), want)
	}
	if got.Text != "Step 4 of 4" || got.Heading != "Review" {
		t.Fatalf("review text = %q heading = %q", got.Text, got.Heading)
	}
	got = uiFormBuildStepper("/f", def, all, 1)
	if want := "1=current 2=complete 3=complete review=complete"; states(got) != want {
		t.Fatalf("states with everything answered = %q, want %q", states(got), want)
	}
}
