package server

import (
	"bytes"
	"html"
	"html/template"
	"net/url"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"

	"github.com/bradleymackey/track-slash/internal/model"
)

type markdownTarget struct {
	downloadHref string
	inlineHref   string
	inlineImage  bool
}

type markdownRenderer struct {
	targets map[string]markdownTarget
}

func renderIssueDescriptionMarkdown(issue model.Issue, attachments []model.IssueAttachment) template.HTML {
	return renderIssueMarkdown(issue, issue.Description, attachments)
}

// renderIssueCommentMarkdown renders a comment body with the same Markdown
// pipeline as the issue description, so object-N refs resolve against the
// attachments on the comment's issue.
func renderIssueCommentMarkdown(issue model.Issue, comment model.Comment, attachments []model.IssueAttachment) template.HTML {
	return renderIssueMarkdown(issue, comment.Body, attachments)
}

func renderIssueMarkdown(issue model.Issue, source string, attachments []model.IssueAttachment) template.HTML {
	objects := make([]model.StorageObject, 0, len(attachments))
	for _, attachment := range attachments {
		objects = append(objects, attachment.Object)
	}
	return renderDescriptionMarkdown(source, objects, func(object model.StorageObject) string {
		return uiIssueAttachmentContentPath(issue, object)
	})
}

func renderSprintDescriptionMarkdown(project model.Project, sprint model.Sprint, attachments []model.SprintAttachment) template.HTML {
	objects := make([]model.StorageObject, 0, len(attachments))
	for _, attachment := range attachments {
		objects = append(objects, attachment.Object)
	}
	return renderDescriptionMarkdown(sprint.Goal, objects, func(object model.StorageObject) string {
		return uiProjectSprintAttachmentContentPath(project, sprint, object)
	})
}

func renderProjectDescriptionMarkdown(project model.Project, attachments []model.ProjectAttachment) template.HTML {
	objects := make([]model.StorageObject, 0, len(attachments))
	for _, attachment := range attachments {
		objects = append(objects, attachment.Object)
	}
	return renderDescriptionMarkdown(project.Description, objects, func(object model.StorageObject) string {
		return uiProjectAttachmentContentPath(project, object)
	})
}

func renderProjectContextMarkdown(project model.Project, contextItem model.ProjectContext, attachments []model.ContextAttachment) template.HTML {
	objects := make([]model.StorageObject, 0, len(attachments))
	for _, attachment := range attachments {
		objects = append(objects, attachment.Object)
	}
	return renderDescriptionMarkdown(contextItem.Body, objects, func(object model.StorageObject) string {
		return uiProjectContextAttachmentContentPath(project, contextItem, object)
	})
}

// renderWhiteboardMarkdown renders a whiteboard page with the shared safe
// pipeline. Pages have no attachments, so object-N refs stay inert text.
func renderWhiteboardMarkdown(page model.WhiteboardPage) template.HTML {
	return renderMarkdown(page.Body, nil)
}

func renderDescriptionMarkdown(source string, objects []model.StorageObject, contentHref func(model.StorageObject) string) template.HTML {
	targets := make(map[string]markdownTarget, len(objects))
	for _, object := range objects {
		href := contentHref(object)
		target := markdownTarget{downloadHref: href}
		if storageObjectSafeInlineImage(object) {
			target.inlineHref = href + "?inline=1"
			target.inlineImage = true
		}
		targets[object.Ref] = target
	}
	return renderMarkdown(source, targets)
}

func renderMarkdown(source string, targets map[string]markdownTarget) template.HTML {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(markdownRenderer{targets: targets}, 900)),
		),
	)
	var out bytes.Buffer
	if err := md.Convert([]byte(source), &out); err != nil {
		return template.HTML(html.EscapeString(source))
	}
	return template.HTML(out.String())
}

func (r markdownRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindImage, r.renderImage)
}

func (r markdownRenderer) renderLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	href, ok := r.linkHref(string(n.Destination))
	if !ok {
		return ast.WalkContinue, nil
	}
	if !entering {
		_, _ = w.WriteString("</a>")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<a href="`)
	_, _ = w.WriteString(html.EscapeString(href))
	_ = w.WriteByte('"')
	if len(n.Title) > 0 {
		_, _ = w.WriteString(` title="`)
		_, _ = w.WriteString(html.EscapeString(string(n.Title)))
		_ = w.WriteByte('"')
	}
	_ = w.WriteByte('>')
	return ast.WalkContinue, nil
}

func (r markdownRenderer) renderImage(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	alt := markdownPlainText(n, source)
	target, ok := r.objectTarget(string(n.Destination))
	if !ok {
		if src, safe := safeMarkdownImageURL(string(n.Destination)); safe {
			r.renderImageTag(w, src, alt, n.Title)
		} else if href, safe := safeMarkdownURL(string(n.Destination)); safe {
			r.renderFallbackImageLink(w, href, alt, n.Title)
		} else {
			_, _ = w.WriteString(html.EscapeString(alt))
		}
		return ast.WalkSkipChildren, nil
	}
	if !target.inlineImage {
		r.renderFallbackImageLink(w, target.downloadHref, alt, n.Title)
		return ast.WalkSkipChildren, nil
	}
	r.renderImageTag(w, target.inlineHref, alt, n.Title)
	return ast.WalkSkipChildren, nil
}

func (r markdownRenderer) renderImageTag(w util.BufWriter, src, alt string, title []byte) {
	_, _ = w.WriteString(`<img src="`)
	_, _ = w.WriteString(html.EscapeString(src))
	_, _ = w.WriteString(`" alt="`)
	_, _ = w.WriteString(html.EscapeString(alt))
	_ = w.WriteByte('"')
	if len(title) > 0 {
		_, _ = w.WriteString(` title="`)
		_, _ = w.WriteString(html.EscapeString(string(title)))
		_ = w.WriteByte('"')
	}
	_ = w.WriteByte('>')
}

func (r markdownRenderer) renderFallbackImageLink(w util.BufWriter, href, alt string, title []byte) {
	label := alt
	if strings.TrimSpace(label) == "" {
		label = href
	}
	_, _ = w.WriteString(`<a href="`)
	_, _ = w.WriteString(html.EscapeString(href))
	_, _ = w.WriteString(`" rel="noreferrer" referrerpolicy="no-referrer"`)
	if len(title) > 0 {
		_, _ = w.WriteString(` title="`)
		_, _ = w.WriteString(html.EscapeString(string(title)))
		_ = w.WriteByte('"')
	}
	_ = w.WriteByte('>')
	_, _ = w.WriteString(html.EscapeString(label))
	_, _ = w.WriteString("</a>")
}

func (r markdownRenderer) linkHref(raw string) (string, bool) {
	if target, ok := r.objectTarget(raw); ok {
		return target.downloadHref, true
	}
	return safeMarkdownURL(raw)
}

func (r markdownRenderer) objectTarget(raw string) (markdownTarget, bool) {
	ref := strings.TrimSpace(raw)
	if _, err := parseTypedRef(ref, "object"); err != nil {
		return markdownTarget{}, false
	}
	target, ok := r.targets[ref]
	return target, ok
}

func safeMarkdownURL(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	if u.Scheme == "" {
		return trimmed, strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "#")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return trimmed, true
	default:
		return "", false
	}
}

func safeMarkdownImageURL(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	if u.Scheme != "" || u.Host != "" {
		return "", false
	}
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, `/\`) {
		return "", false
	}
	return trimmed, true
}

func markdownPlainText(node ast.Node, source []byte) string {
	var out strings.Builder
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch current := n.(type) {
		case *ast.Text:
			out.Write(current.Segment.Value(source))
			if current.SoftLineBreak() || current.HardLineBreak() {
				out.WriteByte(' ')
			}
		case *ast.String:
			out.Write(current.Value)
		case *ast.CodeSpan:
			for child := current.FirstChild(); child != nil; child = child.NextSibling() {
				if textNode, ok := child.(*ast.Text); ok {
					out.Write(textNode.Segment.Value(source))
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(out.String())
}
