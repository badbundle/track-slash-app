package realtime

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestClient(buffer int) *Client {
	return &Client{
		send:    make(chan Event, buffer),
		control: make(chan serverControl, 1),
	}
}

func recv(t *testing.T, c *Client, timeout time.Duration) (Event, bool) {
	t.Helper()
	select {
	case ev := <-c.send:
		return ev, true
	case <-time.After(timeout):
		return Event{}, false
	}
}

func TestHubFanoutToSubscribers(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	issueID := uuid.New()

	projSub := newTestClient(4)
	issueSub := newTestClient(4)
	other := newTestClient(4)

	hub.Subscribe(projSub, ProjectTopic(projectID))
	hub.Subscribe(issueSub, IssueTopic(issueID))
	hub.Subscribe(other, ProjectTopic(uuid.New()))

	ev := Event{
		Op:        OpUpdate,
		Entity:    EntityIssue,
		ID:        issueID,
		ProjectID: &projectID,
		Version:   2,
	}
	hub.Publish(ev)

	if _, ok := recv(t, projSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive event")
	}
	if _, ok := recv(t, issueSub, time.Second); !ok {
		t.Fatal("issue subscriber did not receive event")
	}
	if _, ok := recv(t, other, 100*time.Millisecond); ok {
		t.Fatal("unrelated subscriber received event")
	}
}

func TestSubIssueEventFansOutToChildParentAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	parentID := uuid.New()
	childID := uuid.New()

	childSub := newTestClient(4)
	parentSub := newTestClient(4)
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(childSub, IssueTopic(childID))
	hub.Subscribe(parentSub, IssueTopic(parentID))
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, IssueTopic(uuid.New()))

	hub.Publish(Event{
		Op:            OpInsert,
		Entity:        EntityIssue,
		ID:            childID,
		ParentIssueID: &parentID,
		ProjectID:     &projectID,
		Version:       1,
	})

	if _, ok := recv(t, childSub, time.Second); !ok {
		t.Fatal("child issue subscriber did not receive event")
	}
	if _, ok := recv(t, parentSub, time.Second); !ok {
		t.Fatal("parent issue subscriber did not receive child event")
	}
	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive child event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated issue subscriber received event")
	}
}

func TestSprintEventFansOutToProjectAndSprintTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	sprintID := uuid.New()

	projSub := newTestClient(4)
	sprintSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(projSub, ProjectTopic(projectID))
	hub.Subscribe(sprintSub, SprintTopic(sprintID))
	hub.Subscribe(unrelated, SprintTopic(uuid.New()))

	hub.Publish(Event{
		Op:        OpUpdate,
		Entity:    EntitySprint,
		ID:        sprintID,
		ProjectID: &projectID,
		Version:   3,
	})

	if _, ok := recv(t, projSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive sprint event")
	}
	if _, ok := recv(t, sprintSub, time.Second); !ok {
		t.Fatal("sprint subscriber did not receive event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated sprint subscriber received event")
	}
}

func TestSprintAttachmentEventFansOutToProjectAndSprintTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	sprintID := uuid.New()
	attachmentID := uuid.New()

	projectSub := newTestClient(4)
	sprintSub := newTestClient(4)
	unrelated := newTestClient(4)
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(sprintSub, SprintTopic(sprintID))
	hub.Subscribe(unrelated, SprintTopic(uuid.New()))

	hub.Publish(Event{Op: OpInsert, Entity: EntitySprintAttachment, ID: attachmentID, SprintID: &sprintID, ProjectID: &projectID, Version: 1})

	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive sprint attachment event")
	}
	if _, ok := recv(t, sprintSub, time.Second); !ok {
		t.Fatal("sprint subscriber did not receive sprint attachment event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated subscriber received sprint attachment event")
	}
}

func TestProjectAttachmentEventFansOutToProjectTopic(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, ProjectTopic(uuid.New()))

	hub.Publish(Event{Op: OpInsert, Entity: EntityProjectAttachment, ID: uuid.New(), ProjectID: &projectID, Version: 1})

	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive project attachment event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated subscriber received project attachment event")
	}
}

func TestProjectBlockEventFansOutToProjectTopic(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, ProjectTopic(uuid.New()))

	hub.Publish(Event{Op: OpInsert, Entity: EntityProjectBlock, ID: uuid.New(), ProjectID: &projectID, Version: 1})

	if ev, ok := recv(t, projectSub, time.Second); !ok || ev.Entity != EntityProjectBlock {
		t.Fatalf("project block event = %#v, received = %v", ev, ok)
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated subscriber received project block event")
	}
	if got := (Event{Entity: EntityProjectBlock, ID: uuid.New()}).Topics(); got != nil {
		t.Fatalf("project block topics without project ID = %v, want nil", got)
	}
}

func TestWhiteboardPageEventFansOutToPageAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	pageID := uuid.New()
	pageSub := newTestClient(4)
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)
	hub.Subscribe(pageSub, WhiteboardPageTopic(pageID))
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, WhiteboardPageTopic(uuid.New()))

	hub.Publish(Event{Op: OpDelete, Entity: EntityWhiteboardPage, ID: pageID, ProjectID: &projectID, Version: 2})

	if ev, ok := recv(t, pageSub, time.Second); !ok || ev.Entity != EntityWhiteboardPage || ev.Op != OpDelete {
		t.Fatalf("page subscriber event = %#v, received = %v", ev, ok)
	}
	if ev, ok := recv(t, projectSub, time.Second); !ok || ev.Entity != EntityWhiteboardPage {
		t.Fatalf("project subscriber event = %#v, received = %v", ev, ok)
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated whiteboard subscriber received event")
	}
	got := (Event{Entity: EntityWhiteboardPage, ID: pageID}).Topics()
	if len(got) != 1 || got[0] != WhiteboardPageTopic(pageID) {
		t.Fatalf("whiteboard topics without project ID = %v, want only the page topic", got)
	}
}

func TestContextAttachmentEventFansOutToContextAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	contextID := uuid.New()
	projectSub := newTestClient(4)
	contextSub := newTestClient(4)
	unrelated := newTestClient(4)
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(contextSub, ProjectContextTopic(contextID))
	hub.Subscribe(unrelated, ProjectContextTopic(uuid.New()))

	hub.Publish(Event{Op: OpInsert, Entity: EntityContextAttachment, ID: uuid.New(), ContextID: &contextID, ProjectID: &projectID, Version: 1})

	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive context attachment event")
	}
	if _, ok := recv(t, contextSub, time.Second); !ok {
		t.Fatal("context subscriber did not receive context attachment event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated subscriber received context attachment event")
	}
}

func TestCommentEventFansOutToCommentIssueAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	issueID := uuid.New()
	commentID := uuid.New()

	commentSub := newTestClient(4)
	issueSub := newTestClient(4)
	projSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(commentSub, CommentTopic(commentID))
	hub.Subscribe(issueSub, IssueTopic(issueID))
	hub.Subscribe(projSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, CommentTopic(uuid.New()))

	hub.Publish(Event{
		Op:        OpInsert,
		Entity:    EntityComment,
		ID:        commentID,
		IssueID:   &issueID,
		ProjectID: &projectID,
		Version:   1,
	})

	if _, ok := recv(t, commentSub, time.Second); !ok {
		t.Fatal("comment subscriber did not receive event")
	}
	if _, ok := recv(t, issueSub, time.Second); !ok {
		t.Fatal("issue subscriber did not receive comment event")
	}
	if _, ok := recv(t, projSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive comment event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated comment subscriber received event")
	}
}

func TestContextEventFansOutToContextAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	contextID := uuid.New()

	contextSub := newTestClient(4)
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(contextSub, ProjectContextTopic(contextID))
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, ProjectContextTopic(uuid.New()))

	hub.Publish(Event{
		Op:        OpUpdate,
		Entity:    EntityContext,
		ID:        contextID,
		ProjectID: &projectID,
		Version:   2,
	})

	if _, ok := recv(t, contextSub, time.Second); !ok {
		t.Fatal("context subscriber did not receive event")
	}
	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive context event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated context subscriber received event")
	}
}

func TestIssueContextLinkEventFansOutToLinkIssueContextAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	issueID := uuid.New()
	contextID := uuid.New()
	linkID := uuid.New()

	linkSub := newTestClient(4)
	issueSub := newTestClient(4)
	contextSub := newTestClient(4)
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(linkSub, IssueContextLinkTopic(linkID))
	hub.Subscribe(issueSub, IssueTopic(issueID))
	hub.Subscribe(contextSub, ProjectContextTopic(contextID))
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, IssueContextLinkTopic(uuid.New()))

	hub.Publish(Event{
		Op:        OpInsert,
		Entity:    EntityContextLink,
		ID:        linkID,
		IssueID:   &issueID,
		ContextID: &contextID,
		ProjectID: &projectID,
		Version:   1,
	})

	if _, ok := recv(t, linkSub, time.Second); !ok {
		t.Fatal("link subscriber did not receive event")
	}
	if _, ok := recv(t, issueSub, time.Second); !ok {
		t.Fatal("issue subscriber did not receive context link event")
	}
	if _, ok := recv(t, contextSub, time.Second); !ok {
		t.Fatal("context subscriber did not receive context link event")
	}
	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive context link event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated issue_context_link subscriber received event")
	}
}

func TestIssueTagEventFansOutToTagAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	tagID := uuid.New()

	tagSub := newTestClient(4)
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(tagSub, IssueTagTopic(tagID))
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, IssueTagTopic(uuid.New()))

	hub.Publish(Event{
		Op:        OpInsert,
		Entity:    EntityIssueTag,
		ID:        tagID,
		ProjectID: &projectID,
		Version:   1,
	})

	if _, ok := recv(t, tagSub, time.Second); !ok {
		t.Fatal("tag subscriber did not receive event")
	}
	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive tag event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated issue_tag subscriber received event")
	}
}

func TestIssueTagLinkEventFansOutToLinkIssueTagIssueAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	issueID := uuid.New()
	tagID := uuid.New()
	linkID := uuid.New()

	linkSub := newTestClient(4)
	issueSub := newTestClient(4)
	tagSub := newTestClient(4)
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(linkSub, IssueTagLinkTopic(linkID))
	hub.Subscribe(issueSub, IssueTopic(issueID))
	hub.Subscribe(tagSub, IssueTagTopic(tagID))
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, IssueTagLinkTopic(uuid.New()))

	hub.Publish(Event{
		Op:        OpInsert,
		Entity:    EntityIssueTagLink,
		ID:        linkID,
		IssueID:   &issueID,
		TagID:     &tagID,
		ProjectID: &projectID,
		Version:   1,
	})

	if _, ok := recv(t, linkSub, time.Second); !ok {
		t.Fatal("link subscriber did not receive tag link event")
	}
	if _, ok := recv(t, issueSub, time.Second); !ok {
		t.Fatal("issue subscriber did not receive tag link event")
	}
	if _, ok := recv(t, tagSub, time.Second); !ok {
		t.Fatal("tag subscriber did not receive tag link event")
	}
	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive tag link event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated issue_tag_link subscriber received event")
	}
}

func TestIssueAttachmentEventFansOutToIssueAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	issueID := uuid.New()
	attachmentID := uuid.New()

	issueSub := newTestClient(4)
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(issueSub, IssueTopic(issueID))
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, IssueTopic(uuid.New()))

	hub.Publish(Event{
		Op:        OpInsert,
		Entity:    EntityAttachment,
		ID:        attachmentID,
		IssueID:   &issueID,
		ProjectID: &projectID,
		Version:   1,
	})

	if _, ok := recv(t, issueSub, time.Second); !ok {
		t.Fatal("issue subscriber did not receive attachment event")
	}
	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive attachment event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated issue subscriber received attachment event")
	}
}

func TestChangelogEventFansOutToChangelogIssueParentAndProjectTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	parentID := uuid.New()
	issueID := uuid.New()
	entryID := uuid.New()

	changelogSub := newTestClient(4)
	issueSub := newTestClient(4)
	parentSub := newTestClient(4)
	projectSub := newTestClient(4)
	unrelated := newTestClient(4)

	hub.Subscribe(changelogSub, ProjectChangelogTopic(entryID))
	hub.Subscribe(issueSub, IssueTopic(issueID))
	hub.Subscribe(parentSub, IssueTopic(parentID))
	hub.Subscribe(projectSub, ProjectTopic(projectID))
	hub.Subscribe(unrelated, ProjectChangelogTopic(uuid.New()))

	hub.Publish(Event{
		Op:            OpInsert,
		Entity:        EntityChangelog,
		ID:            entryID,
		IssueID:       &issueID,
		ParentIssueID: &parentID,
		ProjectID:     &projectID,
		Version:       1,
	})

	if _, ok := recv(t, changelogSub, time.Second); !ok {
		t.Fatal("changelog subscriber did not receive event")
	}
	if _, ok := recv(t, issueSub, time.Second); !ok {
		t.Fatal("issue subscriber did not receive changelog event")
	}
	if _, ok := recv(t, parentSub, time.Second); !ok {
		t.Fatal("parent issue subscriber did not receive changelog event")
	}
	if _, ok := recv(t, projectSub, time.Second); !ok {
		t.Fatal("project subscriber did not receive changelog event")
	}
	if _, ok := recv(t, unrelated, 100*time.Millisecond); ok {
		t.Fatal("unrelated changelog subscriber received event")
	}
}

func TestHubDeduplicatesAcrossTopics(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	issueID := uuid.New()

	c := newTestClient(4)
	hub.Subscribe(c, ProjectTopic(projectID))
	hub.Subscribe(c, IssueTopic(issueID))

	hub.Publish(Event{
		Op:        OpUpdate,
		Entity:    EntityIssue,
		ID:        issueID,
		ProjectID: &projectID,
		Version:   1,
	})

	if _, ok := recv(t, c, time.Second); !ok {
		t.Fatal("expected one event")
	}
	if _, ok := recv(t, c, 100*time.Millisecond); ok {
		t.Fatal("expected no second (deduplicated) event")
	}
}

func TestHubSignalsResyncWhenSlowConsumerOverflows(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()

	slow := newTestClient(sendBuffer)
	hub.Subscribe(slow, ProjectTopic(projectID))

	for i := 0; i < sendBuffer+5; i++ {
		hub.Publish(Event{
			Op:        OpInsert,
			Entity:    EntityIssue,
			ID:        uuid.New(),
			ProjectID: &projectID,
			Version:   1,
		})
	}

	if got := hub.Dropped(); got != 5 {
		t.Fatalf("dropped = %d, want 5", got)
	}
	if got := len(slow.send); got != sendBuffer {
		t.Fatalf("queued events = %d, want %d", got, sendBuffer)
	}
	if got := len(slow.control); got != 1 {
		t.Fatalf("queued controls = %d, want one coalesced resync", got)
	}
	msg := <-slow.control
	if msg.Type != resyncMessageType || msg.Reason != resyncOverflow {
		t.Fatalf("control = %#v, want overflow resync", msg)
	}
}

func TestHubResyncAllDeduplicatesClientsAndCoalescesSignals(t *testing.T) {
	hub := NewHub()
	client := newTestClient(1)
	hub.Subscribe(client, ProjectTopic(uuid.New()))
	hub.Subscribe(client, IssueTopic(uuid.New()))

	hub.ResyncAll(resyncListener)
	hub.ResyncAll(resyncOverflow)
	if got := len(client.control); got != 1 {
		t.Fatalf("queued controls = %d, want one coalesced resync", got)
	}
	if msg := <-client.control; msg.Type != resyncMessageType || msg.Reason != resyncListener {
		t.Fatalf("first control = %#v", msg)
	}

	hub.ResyncAll(resyncOverflow)
	if msg := <-client.control; msg.Type != resyncMessageType || msg.Reason != resyncOverflow {
		t.Fatalf("second control = %#v", msg)
	}
}

func TestHubDisconnectAllDeduplicatesClients(t *testing.T) {
	hub := NewHub()
	client := newTestClient(1)
	client.disconnect = make(chan struct{})
	hub.Subscribe(client, ProjectTopic(uuid.New()))
	hub.Subscribe(client, IssueTopic(uuid.New()))

	hub.DisconnectAll()
	hub.DisconnectAll()
	select {
	case <-client.disconnect:
	default:
		t.Fatal("client was not disconnected")
	}
}

func TestHubUnsubscribe(t *testing.T) {
	hub := NewHub()
	projectID := uuid.New()
	c := newTestClient(4)

	topic := ProjectTopic(projectID)
	hub.Subscribe(c, topic)
	hub.Unsubscribe(c, topic)

	hub.Publish(Event{
		Op:      OpUpdate,
		Entity:  EntityProject,
		ID:      projectID,
		Version: 1,
	})

	if _, ok := recv(t, c, 100*time.Millisecond); ok {
		t.Fatal("client received event after unsubscribe")
	}
	if hub.TopicCount() != 0 {
		t.Fatalf("topic count = %d, want 0 (empty topic should be reaped)", hub.TopicCount())
	}
}

func TestHubRemoveDropsAllSubscriptions(t *testing.T) {
	hub := NewHub()
	c := newTestClient(4)
	hub.Subscribe(c, ProjectTopic(uuid.New()))
	hub.Subscribe(c, IssueTopic(uuid.New()))

	if hub.TopicCount() != 2 {
		t.Fatalf("setup: topic count = %d, want 2", hub.TopicCount())
	}

	hub.Remove(c)
	if hub.TopicCount() != 0 {
		t.Fatalf("after Remove: topic count = %d, want 0", hub.TopicCount())
	}
}

func TestTopicsUnknownEntity(t *testing.T) {
	got := Event{Entity: Entity("user"), ID: uuid.New()}.Topics()
	if got != nil {
		t.Fatalf("Topics for unknown entity = %v, want nil", got)
	}
}

func TestParseTopic(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		topic    string
		wantKind string
		wantErr  bool
	}{
		{"issue:" + id.String(), "issue", false},
		{"project:" + id.String(), "project", false},
		{"sprint:" + id.String(), "sprint", false},
		{"comment:" + id.String(), "comment", false},
		{"project_context:" + id.String(), "project_context", false},
		{"issue_context_link:" + id.String(), "issue_context_link", false},
		{"issue_tag:" + id.String(), "issue_tag", false},
		{"issue_tag_link:" + id.String(), "issue_tag_link", false},
		{"whiteboard_page:" + id.String(), "whiteboard_page", false},
		{"user:" + id.String(), "", true},
		{"issue:not-a-uuid", "", true},
		{"sprint:not-a-uuid", "", true},
		{"comment:not-a-uuid", "", true},
		{"project_context:not-a-uuid", "", true},
		{"issue_context_link:not-a-uuid", "", true},
		{"issue_tag:not-a-uuid", "", true},
		{"issue_tag_link:not-a-uuid", "", true},
		{"whiteboard_page:not-a-uuid", "", true},
		{"", "", true},
	}
	for _, tc := range cases {
		kind, _, err := ParseTopic(tc.topic)
		gotErr := err != nil
		if gotErr != tc.wantErr {
			t.Errorf("ParseTopic(%q) err=%v wantErr=%v", tc.topic, err, tc.wantErr)
		}
		if !tc.wantErr && kind != tc.wantKind {
			t.Errorf("ParseTopic(%q) kind=%q want %q", tc.topic, kind, tc.wantKind)
		}
	}
}
