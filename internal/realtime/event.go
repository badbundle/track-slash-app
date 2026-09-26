package realtime

import (
	"fmt"

	"github.com/google/uuid"
)

// Op is the kind of mutation that produced the event.
type Op string

const (
	OpInsert Op = "insert"
	OpUpdate Op = "update"
	OpDelete Op = "delete"
)

// Entity is the table the event came from.
type Entity string

const (
	EntityIssue             Entity = "issue"
	EntityProject           Entity = "project"
	EntitySprint            Entity = "sprint"
	EntityIssueLink         Entity = "issue_link"
	EntityComment           Entity = "comment"
	EntityContext           Entity = "project_context"
	EntityContextLink       Entity = "issue_context_link"
	EntityIssueTag          Entity = "issue_tag"
	EntityIssueTagLink      Entity = "issue_tag_link"
	EntityAttachment        Entity = "issue_attachment"
	EntitySprintAttachment  Entity = "sprint_attachment"
	EntityProjectAttachment Entity = "project_attachment"
	EntityContextAttachment Entity = "context_attachment"
	EntityChangelog         Entity = "project_changelog"
	EntityProjectBlock      Entity = "project_block"
	EntityWhiteboardPage    Entity = "whiteboard_page"
)

// Event is the wire envelope sent over both pg_notify and the WebSocket.
// Kept minimal so it always fits inside Postgres' 8000-byte NOTIFY limit
// and so clients are responsible for refetching the full row via REST.
type Event struct {
	Op            Op         `json:"op"`
	Entity        Entity     `json:"entity"`
	ID            uuid.UUID  `json:"id"`
	IssueID       *uuid.UUID `json:"issue_id,omitempty"`
	SprintID      *uuid.UUID `json:"sprint_id,omitempty"`
	ContextID     *uuid.UUID `json:"context_id,omitempty"`
	TagID         *uuid.UUID `json:"tag_id,omitempty"`
	ParentIssueID *uuid.UUID `json:"parent_issue_id,omitempty"`
	ProjectID     *uuid.UUID `json:"project_id,omitempty"`
	Version       int64      `json:"version"`
	Ts            string     `json:"ts"`
}

// Topics returns the topic names this event should be fanned out on.
// An issue event publishes to both its own issue topic and its project topic.
func (e Event) Topics() []string {
	switch e.Entity {
	case EntityIssue:
		topics := []string{IssueTopic(e.ID)}
		if e.ParentIssueID != nil {
			topics = append(topics, IssueTopic(*e.ParentIssueID))
		}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityProject:
		return []string{ProjectTopic(e.ID)}
	case EntitySprint:
		topics := []string{SprintTopic(e.ID)}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityIssueLink:
		topics := []string{IssueLinkTopic(e.ID)}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityComment:
		topics := []string{CommentTopic(e.ID)}
		if e.IssueID != nil {
			topics = append(topics, IssueTopic(*e.IssueID))
		}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityContext:
		topics := []string{ProjectContextTopic(e.ID)}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityContextLink:
		topics := []string{IssueContextLinkTopic(e.ID)}
		if e.IssueID != nil {
			topics = append(topics, IssueTopic(*e.IssueID))
		}
		if e.ContextID != nil {
			topics = append(topics, ProjectContextTopic(*e.ContextID))
		}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityIssueTag:
		topics := []string{IssueTagTopic(e.ID)}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityIssueTagLink:
		topics := []string{IssueTagLinkTopic(e.ID)}
		if e.IssueID != nil {
			topics = append(topics, IssueTopic(*e.IssueID))
		}
		if e.TagID != nil {
			topics = append(topics, IssueTagTopic(*e.TagID))
		}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityAttachment:
		topics := []string{}
		if e.IssueID != nil {
			topics = append(topics, IssueTopic(*e.IssueID))
		}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntitySprintAttachment:
		topics := []string{}
		if e.SprintID != nil {
			topics = append(topics, SprintTopic(*e.SprintID))
		}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityProjectAttachment:
		if e.ProjectID != nil {
			return []string{ProjectTopic(*e.ProjectID)}
		}
		return nil
	case EntityContextAttachment:
		topics := []string{}
		if e.ContextID != nil {
			topics = append(topics, ProjectContextTopic(*e.ContextID))
		}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	case EntityChangelog:
		topics := []string{ProjectChangelogTopic(e.ID)}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		if e.IssueID != nil {
			topics = append(topics, IssueTopic(*e.IssueID))
		}
		if e.ParentIssueID != nil {
			topics = append(topics, IssueTopic(*e.ParentIssueID))
		}
		return topics
	case EntityProjectBlock:
		if e.ProjectID != nil {
			return []string{ProjectTopic(*e.ProjectID)}
		}
		return nil
	case EntityWhiteboardPage:
		topics := []string{WhiteboardPageTopic(e.ID)}
		if e.ProjectID != nil {
			topics = append(topics, ProjectTopic(*e.ProjectID))
		}
		return topics
	}
	return nil
}

func IssueTopic(id uuid.UUID) string            { return "issue:" + id.String() }
func ProjectTopic(id uuid.UUID) string          { return "project:" + id.String() }
func SprintTopic(id uuid.UUID) string           { return "sprint:" + id.String() }
func IssueLinkTopic(id uuid.UUID) string        { return "issue_link:" + id.String() }
func CommentTopic(id uuid.UUID) string          { return "comment:" + id.String() }
func ProjectContextTopic(id uuid.UUID) string   { return "project_context:" + id.String() }
func IssueContextLinkTopic(id uuid.UUID) string { return "issue_context_link:" + id.String() }
func IssueTagTopic(id uuid.UUID) string         { return "issue_tag:" + id.String() }
func IssueTagLinkTopic(id uuid.UUID) string     { return "issue_tag_link:" + id.String() }
func ProjectChangelogTopic(id uuid.UUID) string { return "project_changelog:" + id.String() }
func WhiteboardPageTopic(id uuid.UUID) string   { return "whiteboard_page:" + id.String() }

// ParseTopic validates a client-supplied topic string and returns its
// prefix and uuid component.
func ParseTopic(t string) (kind string, id uuid.UUID, err error) {
	for _, prefix := range []string{"issue_context_link:", "project_context:", "project_changelog:", "whiteboard_page:", "issue_tag_link:", "issue_link:", "issue_tag:", "comment:", "issue:", "project:", "sprint:"} {
		if len(t) > len(prefix) && t[:len(prefix)] == prefix {
			id, err = uuid.Parse(t[len(prefix):])
			if err != nil {
				return "", uuid.Nil, fmt.Errorf("invalid uuid in topic %q: %w", t, err)
			}
			return prefix[:len(prefix)-1], id, nil
		}
	}
	return "", uuid.Nil, fmt.Errorf("unknown topic format %q (want issue:<uuid>, project:<uuid>, sprint:<uuid>, issue_link:<uuid>, comment:<uuid>, project_context:<uuid>, issue_context_link:<uuid>, issue_tag:<uuid>, issue_tag_link:<uuid>, project_changelog:<uuid>, or whiteboard_page:<uuid>)", t)
}
