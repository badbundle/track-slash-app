package server

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestRenderIssueDescriptionMarkdownResolvesIssueAttachmentsSafely(t *testing.T) {
	t.Parallel()
	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	issue := model.Issue{
		ID:            issueID,
		ProjectID:     projectID,
		OwnerUsername: "bradley",
		ProjectKey:    "TRACK",
		Number:        7,
		Identifier:    "TRACK-7",
		Description: strings.Join([]string{
			"**Bold**",
			"![Screenshot](object-1)",
			"[Download log](object-2)",
			"![Vector](object-3)",
			"![Missing](object-99)",
			"[Unsafe](javascript:alert(1))",
			"<script>alert('x')</script>",
			"![External](https://example.com/image.png)",
			"![External SVG](https://news.ycombinator.com/y18.svg)",
			"![Protocol relative](//tracker.example/pixel.png)",
			"![Local](/static/logo.png)",
			"[External docs](https://example.com/docs)",
			"![Data URI](data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=)",
		}, "\n\n"),
	}
	attachments := []model.IssueAttachment{
		testMarkdownAttachment(projectID, issueID, 1, "screenshot.png", "image/png"),
		testMarkdownAttachment(projectID, issueID, 2, "log.txt", "text/plain"),
		testMarkdownAttachment(projectID, issueID, 3, "vector.svg", "image/svg+xml"),
	}

	out := string(renderIssueDescriptionMarkdown(issue, attachments))
	for _, want := range []string{
		"<strong>Bold</strong>",
		`<img src="/bradley/issues/TRACK-7/attachments/object-1/content?inline=1" alt="Screenshot">`,
		`<a href="/bradley/issues/TRACK-7/attachments/object-2/content">Download log</a>`,
		`<a href="/bradley/issues/TRACK-7/attachments/object-3/content" rel="noreferrer" referrerpolicy="no-referrer">Vector</a>`,
		"Missing",
		"Unsafe",
		`<a href="https://example.com/image.png" rel="noreferrer" referrerpolicy="no-referrer">External</a>`,
		`<a href="https://news.ycombinator.com/y18.svg" rel="noreferrer" referrerpolicy="no-referrer">External SVG</a>`,
		`<a href="//tracker.example/pixel.png" rel="noreferrer" referrerpolicy="no-referrer">Protocol relative</a>`,
		`<img src="/static/logo.png" alt="Local">`,
		`<a href="https://example.com/docs">External docs</a>`,
		"Data URI",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown output missing %q: %s", want, out)
		}
	}
	for _, notWant := range []string{
		"<script",
		"javascript:",
		`<img src="/bradley/issues/TRACK-7/attachments/object-3/content`,
		`<img src="https://example.com/image.png"`,
		`<img src="https://news.ycombinator.com/y18.svg"`,
		`<img src="//tracker.example/pixel.png"`,
		"data:image",
		`object-99/content`,
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("markdown output included %q: %s", notWant, out)
		}
	}
	if strings.Count(out, "<img ") != 2 {
		t.Fatalf("inline image count = %d output=%s", strings.Count(out, "<img "), out)
	}
}

func TestRenderIssueDescriptionMarkdownEmptySource(t *testing.T) {
	t.Parallel()
	if got := renderIssueDescriptionMarkdown(model.Issue{Description: " \n\t "}, nil); got != "" {
		t.Fatalf("empty markdown = %q, want empty", got)
	}
}

func TestRenderIssueCommentMarkdownResolvesIssueAttachmentsSafely(t *testing.T) {
	t.Parallel()
	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	issue := model.Issue{
		ID:            issueID,
		ProjectID:     projectID,
		OwnerUsername: "bradley",
		ProjectKey:    "TRACK",
		Number:        7,
		Identifier:    "TRACK-7",
		Description:   "Description is not what renders here",
	}
	comment := model.Comment{
		IssueID: issueID,
		Body: strings.Join([]string{
			"Looks **ready**.",
			"- one\n- two",
			"![Screenshot](object-1)",
			"[Log](object-2)",
			"![Missing](object-99)",
			"[Unsafe](javascript:alert(1))",
			"<script>alert('x')</script>",
			"![External](https://example.com/image.png)",
		}, "\n\n"),
	}
	attachments := []model.IssueAttachment{
		testMarkdownAttachment(projectID, issueID, 1, "screenshot.png", "image/png"),
		testMarkdownAttachment(projectID, issueID, 2, "log.txt", "text/plain"),
	}

	out := string(renderIssueCommentMarkdown(issue, comment, attachments))
	for _, want := range []string{
		"<p>Looks <strong>ready</strong>.</p>",
		"<li>one</li>",
		"<li>two</li>",
		`<img src="/bradley/issues/TRACK-7/attachments/object-1/content?inline=1" alt="Screenshot">`,
		`<a href="/bradley/issues/TRACK-7/attachments/object-2/content">Log</a>`,
		"Missing",
		"Unsafe",
		`<a href="https://example.com/image.png" rel="noreferrer" referrerpolicy="no-referrer">External</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("comment markdown output missing %q: %s", want, out)
		}
	}
	for _, notWant := range []string{
		"Description is not what renders here",
		"<script",
		"javascript:",
		`<img src="https://example.com/image.png"`,
		`object-99/content`,
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("comment markdown output included %q: %s", notWant, out)
		}
	}
}

func TestRenderIssueCommentMarkdownEmptyBody(t *testing.T) {
	t.Parallel()
	if got := renderIssueCommentMarkdown(model.Issue{}, model.Comment{Body: " \n"}, nil); got != "" {
		t.Fatalf("empty comment markdown = %q, want empty", got)
	}
}

func TestSafeMarkdownImageURLAllowsOnlySameOriginAbsolutePaths(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "same-origin path", raw: "/attachments/object-1/content", want: "/attachments/object-1/content", ok: true},
		{name: "same-origin path with query", raw: " /image?id=1 ", want: "/image?id=1", ok: true},
		{name: "empty", raw: ""},
		{name: "malformed", raw: "%"},
		{name: "external HTTPS", raw: "https://tracker.example/pixel.png"},
		{name: "protocol relative", raw: "//tracker.example/pixel.png"},
		{name: "backslash network path", raw: `/\tracker.example/pixel.png`},
		{name: "relative path", raw: "images/pixel.png"},
		{name: "fragment", raw: "#pixel"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := safeMarkdownImageURL(tt.raw)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("safeMarkdownImageURL(%q) = %q, %v; want %q, %v", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRenderIssueDescriptionMarkdownHeadingsAndTables(t *testing.T) {
	t.Parallel()
	issue := model.Issue{
		Description: strings.Join([]string{
			"# Heading 1",
			"## Heading 2",
			"| Name | Value |",
			"| --- | --- |",
			"| Alpha | One |",
		}, "\n"),
	}
	out := string(renderIssueDescriptionMarkdown(issue, nil))
	for _, want := range []string{
		"<h1>Heading 1</h1>",
		"<h2>Heading 2</h2>",
		"<table>",
		"<th>Name</th>",
		"<th>Value</th>",
		"<td>Alpha</td>",
		"<td>One</td>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown output missing %q: %s", want, out)
		}
	}
}

func TestRenderProjectDescriptionMarkdownSafely(t *testing.T) {
	t.Parallel()
	project := model.Project{
		OwnerUsername: "bradley",
		Key:           "TRACK",
		Description: strings.Join([]string{
			"# Overview",
			"**Bold**",
			"| Name | Value |\n| --- | --- |\n| Alpha | One |",
			"[Docs](/docs)",
			"![External](https://example.com/image.png)",
			"[Unsafe](javascript:alert(1))",
			"<script>alert('x')</script>",
			"[Object](object-1)",
			"![Object image](object-2)",
		}, "\n\n"),
	}
	attachments := []model.ProjectAttachment{
		{Object: model.StorageObject{Number: 1, Ref: "object-1", ContentType: "text/plain"}},
		{Object: model.StorageObject{Number: 2, Ref: "object-2", ContentType: "image/png"}},
	}

	out := string(renderProjectDescriptionMarkdown(project, attachments))
	for _, want := range []string{
		"<h1>Overview</h1>",
		"<strong>Bold</strong>",
		"<table>",
		"<th>Name</th>",
		"<td>One</td>",
		`<a href="/docs">Docs</a>`,
		`<a href="https://example.com/image.png" rel="noreferrer" referrerpolicy="no-referrer">External</a>`,
		"Unsafe",
		"Object",
		"Object image",
		`<a href="/bradley/projects/TRACK/attachments/object-1/content">Object</a>`,
		`<img src="/bradley/projects/TRACK/attachments/object-2/content?inline=1" alt="Object image">`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("project markdown output missing %q: %s", want, out)
		}
	}
	for _, notWant := range []string{
		"<script",
		"javascript:",
		`<img src="https://example.com/image.png"`,
		`<a href="object-1"`,
		`<img src="object-2"`,
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("project markdown output included %q: %s", notWant, out)
		}
	}
}

func TestRenderProjectContextMarkdownScopesAttachments(t *testing.T) {
	t.Parallel()
	project := model.Project{OwnerUsername: "bradley", Key: "TRACK"}
	contextItem := model.ProjectContext{Number: 2, Ref: "context-2", Body: "![diagram](object-4) [missing](object-5)"}
	attachment := model.ContextAttachment{Object: model.StorageObject{Number: 4, Ref: "object-4", Filename: "diagram.png", ContentType: "image/png"}}
	out := string(renderProjectContextMarkdown(project, contextItem, []model.ContextAttachment{attachment}))
	for _, want := range []string{`src="/bradley/projects/TRACK/context/context-2/attachments/object-4/content?inline=1"`, `alt="diagram"`, `missing`} {
		if !strings.Contains(out, want) {
			t.Fatalf("context markdown missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, `href="object-5"`) {
		t.Fatalf("unattached context object should remain inert: %s", out)
	}
}

func TestRenderSprintDescriptionMarkdownResolvesOnlySprintAttachments(t *testing.T) {
	t.Parallel()
	project := model.Project{OwnerUsername: "bradley", Key: "TRACK"}
	sprintID := uuid.New()
	sprint := model.Sprint{
		ID:   sprintID,
		Ref:  "sprint-4",
		Goal: "![Sprint image](object-4)\n\n![Missing](object-5)",
	}
	objectID := uuid.New()
	attachments := []model.SprintAttachment{{
		ID:              uuid.New(),
		SprintID:        sprintID,
		StorageObjectID: objectID,
		Object: model.StorageObject{
			ID:          objectID,
			Number:      4,
			Ref:         "object-4",
			Filename:    "sprint.png",
			ContentType: "image/png",
		},
	}}

	out := string(renderSprintDescriptionMarkdown(project, sprint, attachments))
	if !strings.Contains(out, `<img src="/bradley/projects/TRACK/sprints/sprint-4/attachments/object-4/content?inline=1" alt="Sprint image">`) {
		t.Fatalf("sprint markdown missing attached image: %s", out)
	}
	if strings.Contains(out, "object-5/content") || !strings.Contains(out, "Missing") {
		t.Fatalf("sprint markdown resolved missing attachment: %s", out)
	}
}

func testMarkdownAttachment(projectID, issueID uuid.UUID, number int, filename, contentType string) model.IssueAttachment {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	objectID := uuid.New()
	return model.IssueAttachment{
		ID:              uuid.New(),
		ProjectID:       projectID,
		IssueID:         issueID,
		StorageObjectID: objectID,
		Object: model.StorageObject{
			ID:          objectID,
			ProjectID:   projectID,
			Number:      number,
			Ref:         model.StorageObjectRef(number),
			Backend:     "local",
			Bucket:      "local",
			ObjectKey:   "projects/test/objects/" + objectID.String(),
			Filename:    filename,
			ContentType: contentType,
			ByteSize:    12,
			SHA256:      strings.Repeat("a", 64),
			CreatedByID: uuid.New(),
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		CreatedByID: uuid.New(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}
