package server

import (
	"net/url"
	"reflect"
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
