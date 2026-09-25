package realtime

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleymackey/track-slash/internal/testutil"
)

func newRealtimeDB(t *testing.T) (context.Context, *pgxpool.Pool, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	db := testutil.NewMigratedDatabase(t)
	return ctx, db.Pool, db.URL
}

func runRealtimeListener(t *testing.T, ctx context.Context, dbURL string, hub *Hub) {
	t.Helper()
	runListener(t, ctx, NewListener(dbURL, hub))
}

func runListener(t *testing.T, ctx context.Context, listener *Listener) {
	t.Helper()
	listenerCtx, stopListener := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		listener.Run(listenerCtx)
	}()
	t.Cleanup(func() {
		stopListener()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("realtime listener did not stop within 2s")
		}
	})
}

func requireListenerResync(t *testing.T, c *Client) {
	t.Helper()
	select {
	case msg := <-c.control:
		if msg.Type != resyncMessageType || msg.Reason != resyncListener {
			t.Fatalf("listener control = %#v, want reconnect resync", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not request resync within 3s")
	}
}

func TestListenerReconnectSignalsResyncAndResumesEvents(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)
	hub := NewHub()
	client := newTestClient(sendBuffer)
	hub.Subscribe(client, "project:00000000-0000-0000-0000-000000000001")

	listener := NewListener(dbURL, hub)
	runListener(t, ctx, listener)
	requireListenerResync(t, client)

	var terminated bool
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(bool_or(pg_terminate_backend(pid)), false)
		FROM pg_stat_activity
		WHERE application_name = $1
		  AND datname = current_database()
		  AND pid <> pg_backend_pid()
	`, listener.applicationName).Scan(&terminated); err != nil {
		t.Fatalf("terminate listener connection: %v", err)
	}
	if !terminated {
		t.Fatal("listener connection was not found for forced outage")
	}

	requireListenerResync(t, client)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-reconnect")
	hub.Subscribe(client, "project:"+projectID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO issues (project_id, number, title) VALUES ($1, 1, 'after reconnect')
	`, projectID); err != nil {
		t.Fatalf("insert issue after reconnect: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-client.send:
			if ev.Entity != EntityIssue {
				continue
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Fatalf("issue event after reconnect = %#v", ev)
			}
			return
		case <-deadline:
			t.Fatal("listener did not resume events after reconnect")
		}
	}
}

// TestListenerReceivesEventFromIssueInsert exercises the full pipeline:
// trigger fires pg_notify, Listener decodes payload, Hub fans out to a
// client subscribed to the project's topic.
func TestListenerReceivesEventFromIssueInsert(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	// Wait for the listener's LISTEN to register before producing events
	// the test expects to observe.
	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-test")

	// Subscribe after the project insert. The project event may still be
	// in flight inside the listener at this moment, so we loop and discard
	// anything that isn't the issue insert we care about.
	c := newTestClient(8)
	hub.Subscribe(c, "project:"+projectID)

	if _, err := pool.Exec(ctx, `
		INSERT INTO issues (project_id, number, title) VALUES ($1, 1, 'hello')
	`, projectID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityIssue {
				continue
			}
			if ev.Op != OpInsert {
				t.Errorf("op = %s, want insert", ev.Op)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatal("did not receive issue event within 3s")
		}
	}
}

func TestListenerReceivesProjectBlockEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)
	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)
	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-project-block")
	client := newTestClient(8)
	hub.Subscribe(client, "project:"+projectID)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, name)
		VALUES ($1, $2, 'Blocked Realtime User')
		RETURNING id::text
	`, "rtblocked"+suffix, "rt-blocked-"+suffix+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("insert blocked user: %v", err)
	}
	var blockID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO project_user_blocks (project_id, user_id, created_by_id)
		SELECT $1, $2, owner_id FROM projects WHERE id = $1
		RETURNING id::text
	`, projectID, userID).Scan(&blockID); err != nil {
		t.Fatalf("insert project block: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-client.send:
			if ev.Entity != EntityProjectBlock {
				continue
			}
			if ev.Op != OpInsert || ev.ID.String() != blockID {
				t.Fatalf("project block event = %#v", ev)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Fatalf("project block project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatal("did not receive project block event within 3s")
		}
	}
}

// TestListenerReceivesSubIssueEvent verifies child issue events include
// parent_issue_id so the hub can fan them out on the parent issue topic.
func TestListenerReceivesSubIssueEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-sub-issue")
	var parentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title) VALUES ($1, 1, 'parent') RETURNING id::text
	`, projectID).Scan(&parentID); err != nil {
		t.Fatalf("insert parent: %v", err)
	}

	parentSub := newTestClient(16)
	projSub := newTestClient(16)
	hub.Subscribe(parentSub, "issue:"+parentID)
	hub.Subscribe(projSub, "project:"+projectID)

	var childID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title, parent_issue_id)
		VALUES ($1, 2, 'child', $2)
		RETURNING id::text
	`, projectID, parentID).Scan(&childID); err != nil {
		t.Fatalf("insert child: %v", err)
	}

	waitForSubIssueEvent(t, parentSub, childID, parentID, projectID, OpInsert)
	waitForSubIssueEvent(t, projSub, childID, parentID, projectID, OpInsert)
}

// TestListenerReceivesSprintEvent verifies sprint INSERT fires the
// sprints_events trigger, the listener decodes the payload, and the hub fans
// it out on both the project topic and the sprint topic.
func TestListenerReceivesSprintEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-sprint")

	projSub := newTestClient(8)
	hub.Subscribe(projSub, "project:"+projectID)

	var sprintID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO sprints (project_id, number, name, start_date, end_date)
		VALUES ($1, 1, 'S1', DATE '2026-06-01', DATE '2026-06-14')
		RETURNING id::text
	`, projectID).Scan(&sprintID); err != nil {
		t.Fatalf("insert sprint: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-projSub.send:
			if ev.Entity != EntitySprint {
				continue
			}
			if ev.Op != OpInsert {
				t.Errorf("op = %s, want insert", ev.Op)
			}
			if ev.ID.String() != sprintID {
				t.Errorf("id = %s, want %s", ev.ID, sprintID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatal("did not receive sprint event within 3s")
		}
	}
}

// TestListenerReceivesIssueLinkEvent verifies issue_links INSERT fires the
// issue_links_events trigger, the listener decodes the payload, and the hub
// fans it out on both the project topic and the issue_link topic. The
// duplicates link path also closes the source issue, producing a follow-up
// issue UPDATE event.
func TestListenerReceivesIssueLinkEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-link")

	var srcID, tgtID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title) VALUES ($1, 1, 'A') RETURNING id::text
	`, projectID).Scan(&srcID); err != nil {
		t.Fatalf("insert src: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title) VALUES ($1, 2, 'B') RETURNING id::text
	`, projectID).Scan(&tgtID); err != nil {
		t.Fatalf("insert tgt: %v", err)
	}

	projSub := newTestClient(16)
	hub.Subscribe(projSub, "project:"+projectID)

	var linkID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue_links (project_id, number, source_id, target_id, link_type)
		VALUES ($1, 1, $2, $3, 'blocks')
		RETURNING id::text
	`, projectID, srcID, tgtID).Scan(&linkID); err != nil {
		t.Fatalf("insert link: %v", err)
	}

	linkSub := newTestClient(8)
	hub.Subscribe(linkSub, "issue_link:"+linkID)

	gotProjLink := false
	deadline := time.After(3 * time.Second)
	for !gotProjLink {
		select {
		case ev := <-projSub.send:
			if ev.Entity != EntityIssueLink {
				continue
			}
			if ev.Op != OpInsert {
				t.Errorf("op = %s, want insert", ev.Op)
			}
			if ev.ID.String() != linkID {
				t.Errorf("id = %s, want %s", ev.ID, linkID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			gotProjLink = true
		case <-deadline:
			t.Fatal("did not receive link event on project topic within 3s")
		}
	}

	// Delete the link and verify the OpDelete event arrives on the link topic.
	if _, err := pool.Exec(ctx, `DELETE FROM issue_links WHERE id = $1`, linkID); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	deadline = time.After(3 * time.Second)
	for {
		select {
		case ev := <-linkSub.send:
			if ev.Entity != EntityIssueLink {
				continue
			}
			if ev.Op == OpDelete {
				return
			}
		case <-deadline:
			t.Fatal("did not receive link delete event within 3s")
		}
	}
}

// TestListenerReceivesCommentEvent verifies comment events include both issue_id
// and project_id so the hub can fan them out on comment, issue, and project topics.
func TestListenerReceivesCommentEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	var projectID, issueID, authorID string
	projectID = insertRealtimeProject(ctx, t, pool, "rt-comment")
	userKey := uniqueKey(t)
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, name) VALUES ($1, $2, 'Commenter') RETURNING id::text
	`, "rtcomment"+userKey, "rt-comment-"+userKey+"@example.com").Scan(&authorID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title) VALUES ($1, 1, 'A') RETURNING id::text
	`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}

	issueSub := newTestClient(16)
	projSub := newTestClient(16)
	hub.Subscribe(issueSub, "issue:"+issueID)
	hub.Subscribe(projSub, "project:"+projectID)

	var commentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO comments (issue_id, number, author_id, body)
		VALUES ($1, 1, $2, 'hello')
		RETURNING id::text
	`, issueID, authorID).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	waitForCommentEvent(t, issueSub, commentID, issueID, projectID, OpInsert)
	waitForCommentEvent(t, projSub, commentID, issueID, projectID, OpInsert)

	commentSub := newTestClient(16)
	hub.Subscribe(commentSub, "comment:"+commentID)
	if _, err := pool.Exec(ctx, `
		UPDATE comments SET body = 'edited' WHERE id = $1
	`, commentID); err != nil {
		t.Fatalf("update comment: %v", err)
	}
	waitForCommentEvent(t, commentSub, commentID, issueID, projectID, OpUpdate)

	if _, err := pool.Exec(ctx, `DELETE FROM comments WHERE id = $1`, commentID); err != nil {
		t.Fatalf("delete comment: %v", err)
	}
	waitForCommentEvent(t, commentSub, commentID, issueID, projectID, OpDelete)
}

// TestListenerReceivesProjectContextEvents verifies context and issue-context
// link triggers include enough ids for project, issue, and context fanout.
func TestListenerReceivesProjectContextEvents(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-context")
	var ownerID string
	if err := pool.QueryRow(ctx, `SELECT owner_id::text FROM projects WHERE id = $1`, projectID).Scan(&ownerID); err != nil {
		t.Fatalf("select owner: %v", err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title) VALUES ($1, 1, 'context issue') RETURNING id::text
	`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}

	projSub := newTestClient(32)
	hub.Subscribe(projSub, "project:"+projectID)

	var contextID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO project_context (project_id, number, position, title, body, created_by_id, updated_by_id)
		VALUES ($1, 1, 1, 'Architecture', 'Use the existing store.', $2, $2)
		RETURNING id::text
	`, projectID, ownerID).Scan(&contextID); err != nil {
		t.Fatalf("insert project_context: %v", err)
	}
	waitForProjectContextEvent(t, projSub, contextID, projectID, OpInsert)

	contextSub := newTestClient(32)
	issueSub := newTestClient(32)
	hub.Subscribe(contextSub, "project_context:"+contextID)
	hub.Subscribe(issueSub, "issue:"+issueID)

	if _, err := pool.Exec(ctx, `UPDATE project_context SET body = 'Updated' WHERE id = $1`, contextID); err != nil {
		t.Fatalf("update project_context: %v", err)
	}
	waitForProjectContextEvent(t, contextSub, contextID, projectID, OpUpdate)

	var linkID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue_context_links (project_id, issue_id, context_id)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, projectID, issueID, contextID).Scan(&linkID); err != nil {
		t.Fatalf("insert issue_context_links: %v", err)
	}
	waitForIssueContextLinkEvent(t, issueSub, linkID, issueID, contextID, projectID, OpInsert)
	waitForIssueContextLinkEvent(t, contextSub, linkID, issueID, contextID, projectID, OpInsert)

	linkSub := newTestClient(32)
	hub.Subscribe(linkSub, "issue_context_link:"+linkID)
	if _, err := pool.Exec(ctx, `DELETE FROM issue_context_links WHERE id = $1`, linkID); err != nil {
		t.Fatalf("delete issue_context_links: %v", err)
	}
	waitForIssueContextLinkEvent(t, linkSub, linkID, issueID, contextID, projectID, OpDelete)
}

// TestListenerReceivesIssueTagEvents verifies tag and issue-tag-link triggers
// include enough ids for project, issue, and tag fanout.
func TestListenerReceivesIssueTagEvents(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-tags")
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title)
		VALUES ($1, 1, 'tagged issue')
		RETURNING id::text
	`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}

	projectSub := newTestClient(32)
	hub.Subscribe(projectSub, "project:"+projectID)

	var tagID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue_tags (project_id, number, name, color)
		VALUES ($1, 1, 'Customer Beta', 'green')
		RETURNING id::text
	`, projectID).Scan(&tagID); err != nil {
		t.Fatalf("insert issue_tags: %v", err)
	}
	waitForIssueTagEvent(t, projectSub, tagID, projectID, OpInsert)

	issueSub := newTestClient(32)
	tagSub := newTestClient(32)
	projectLinkSub := newTestClient(32)
	hub.Subscribe(issueSub, "issue:"+issueID)
	hub.Subscribe(tagSub, "issue_tag:"+tagID)
	hub.Subscribe(projectLinkSub, "project:"+projectID)

	var linkID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue_tag_links (project_id, issue_id, tag_id)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, projectID, issueID, tagID).Scan(&linkID); err != nil {
		t.Fatalf("insert issue_tag_links: %v", err)
	}

	waitForIssueTagLinkEvent(t, issueSub, linkID, issueID, tagID, projectID, OpInsert)
	waitForIssueTagLinkEvent(t, tagSub, linkID, issueID, tagID, projectID, OpInsert)
	waitForIssueTagLinkEvent(t, projectLinkSub, linkID, issueID, tagID, projectID, OpInsert)
}

// TestListenerReceivesIssueAttachmentEvent verifies issue attachment trigger
// payloads include issue_id and project_id for issue and project topic fanout.
func TestListenerReceivesIssueAttachmentEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-attachment")
	var ownerID string
	if err := pool.QueryRow(ctx, `SELECT owner_id::text FROM projects WHERE id = $1`, projectID).Scan(&ownerID); err != nil {
		t.Fatalf("select owner: %v", err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title)
		VALUES ($1, 1, 'attachment issue')
		RETURNING id::text
	`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	var objectID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO storage_objects (
			project_id, number, backend, bucket, object_key, filename, content_type,
			byte_size, sha256, created_by_id
		)
		VALUES ($1::uuid, 1, 'local', 'local', 'projects/' || $1::text || '/objects/1',
			'screenshot.png', 'image/png', 4, repeat('a', 64), $2::uuid)
		RETURNING id::text
	`, projectID, ownerID).Scan(&objectID); err != nil {
		t.Fatalf("insert storage_objects: %v", err)
	}

	issueSub := newTestClient(32)
	projectSub := newTestClient(32)
	hub.Subscribe(issueSub, "issue:"+issueID)
	hub.Subscribe(projectSub, "project:"+projectID)

	var attachmentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue_attachments (project_id, issue_id, storage_object_id, created_by_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, projectID, issueID, objectID, ownerID).Scan(&attachmentID); err != nil {
		t.Fatalf("insert issue_attachments: %v", err)
	}

	waitForIssueAttachmentEvent(t, issueSub, attachmentID, issueID, projectID, OpInsert)
	waitForIssueAttachmentEvent(t, projectSub, attachmentID, issueID, projectID, OpInsert)
}

func TestListenerReceivesSprintAttachmentEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)
	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)
	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-sprint-attachment")
	var ownerID string
	if err := pool.QueryRow(ctx, `SELECT owner_id::text FROM projects WHERE id = $1`, projectID).Scan(&ownerID); err != nil {
		t.Fatalf("select owner: %v", err)
	}
	var sprintID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO sprints (project_id, number, name)
		VALUES ($1, 1, 'attachment sprint')
		RETURNING id::text
	`, projectID).Scan(&sprintID); err != nil {
		t.Fatalf("insert sprint: %v", err)
	}
	var objectID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO storage_objects (
			project_id, number, backend, bucket, object_key, filename, content_type,
			byte_size, sha256, created_by_id
		)
		VALUES ($1::uuid, 1, 'local', 'local', 'projects/' || $1::text || '/objects/sprint-1',
			'plan.png', 'image/png', 4, repeat('a', 64), $2::uuid)
		RETURNING id::text
	`, projectID, ownerID).Scan(&objectID); err != nil {
		t.Fatalf("insert storage object: %v", err)
	}

	sprintSub := newTestClient(32)
	projectSub := newTestClient(32)
	hub.Subscribe(sprintSub, "sprint:"+sprintID)
	hub.Subscribe(projectSub, "project:"+projectID)

	var attachmentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO sprint_attachments (project_id, sprint_id, storage_object_id, created_by_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, projectID, sprintID, objectID, ownerID).Scan(&attachmentID); err != nil {
		t.Fatalf("insert sprint attachment: %v", err)
	}

	waitForSprintAttachmentEvent(t, sprintSub, attachmentID, sprintID, projectID, OpInsert)
	waitForSprintAttachmentEvent(t, projectSub, attachmentID, sprintID, projectID, OpInsert)
	if _, err := pool.Exec(ctx, `DELETE FROM sprint_attachments WHERE id = $1`, attachmentID); err != nil {
		t.Fatalf("delete sprint attachment: %v", err)
	}
	waitForSprintAttachmentEvent(t, sprintSub, attachmentID, sprintID, projectID, OpDelete)
	waitForSprintAttachmentEvent(t, projectSub, attachmentID, sprintID, projectID, OpDelete)
}

func TestListenerReceivesProjectAttachmentEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)
	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)
	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-project-attachment")
	var ownerID string
	if err := pool.QueryRow(ctx, `SELECT owner_id::text FROM projects WHERE id = $1`, projectID).Scan(&ownerID); err != nil {
		t.Fatalf("select owner: %v", err)
	}
	var objectID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO storage_objects (
			project_id, number, backend, bucket, object_key, filename, content_type,
			byte_size, sha256, created_by_id
		)
		VALUES ($1::uuid, 1, 'local', 'local', 'projects/' || $1::text || '/objects/project-1',
			'project.png', 'image/png', 4, repeat('a', 64), $2::uuid)
		RETURNING id::text
	`, projectID, ownerID).Scan(&objectID); err != nil {
		t.Fatalf("insert storage object: %v", err)
	}

	projectSub := newTestClient(32)
	hub.Subscribe(projectSub, "project:"+projectID)
	var attachmentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO project_attachments (project_id, storage_object_id, created_by_id)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, projectID, objectID, ownerID).Scan(&attachmentID); err != nil {
		t.Fatalf("insert project attachment: %v", err)
	}
	waitForProjectAttachmentEvent(t, projectSub, attachmentID, projectID, OpInsert)
	if _, err := pool.Exec(ctx, `DELETE FROM project_attachments WHERE id = $1`, attachmentID); err != nil {
		t.Fatalf("delete project attachment: %v", err)
	}
	waitForProjectAttachmentEvent(t, projectSub, attachmentID, projectID, OpDelete)
}

func TestListenerReceivesWhiteboardPageEvents(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)
	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)
	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-whiteboard")
	var ownerID string
	if err := pool.QueryRow(ctx, `SELECT owner_id::text FROM projects WHERE id = $1`, projectID).Scan(&ownerID); err != nil {
		t.Fatalf("select owner: %v", err)
	}

	projectSub := newTestClient(32)
	hub.Subscribe(projectSub, "project:"+projectID)
	var pageID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO whiteboard_pages (project_id, number, title, body, created_by_id, updated_by_id)
		VALUES ($1, 1, 'Ideas', 'First thought.', $2, $2)
		RETURNING id::text
	`, projectID, ownerID).Scan(&pageID); err != nil {
		t.Fatalf("insert whiteboard page: %v", err)
	}
	waitForWhiteboardPageEvent(t, projectSub, pageID, projectID, OpInsert)

	pageSub := newTestClient(32)
	hub.Subscribe(pageSub, "whiteboard_page:"+pageID)
	if _, err := pool.Exec(ctx, `UPDATE whiteboard_pages SET body = 'Second thought.' WHERE id = $1`, pageID); err != nil {
		t.Fatalf("update whiteboard page: %v", err)
	}
	waitForWhiteboardPageEvent(t, pageSub, pageID, projectID, OpUpdate)
	waitForWhiteboardPageEvent(t, projectSub, pageID, projectID, OpUpdate)

	if _, err := pool.Exec(ctx, `UPDATE whiteboard_pages SET deleted_at = now() WHERE id = $1`, pageID); err != nil {
		t.Fatalf("soft-delete whiteboard page: %v", err)
	}
	waitForWhiteboardPageEvent(t, pageSub, pageID, projectID, OpDelete)
	waitForWhiteboardPageEvent(t, projectSub, pageID, projectID, OpDelete)

	if _, err := pool.Exec(ctx, `DELETE FROM whiteboard_pages WHERE id = $1`, pageID); err != nil {
		t.Fatalf("hard-delete whiteboard page: %v", err)
	}
	waitForWhiteboardPageEvent(t, pageSub, pageID, projectID, OpDelete)
}

func TestListenerReceivesContextAttachmentEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)
	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)
	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-context-attachment")
	var ownerID, contextID, objectID string
	if err := pool.QueryRow(ctx, `SELECT owner_id::text FROM projects WHERE id = $1`, projectID).Scan(&ownerID); err != nil {
		t.Fatalf("select owner: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO project_context (project_id, number, scope, position, title, content_type, body, created_by_id, updated_by_id)
		VALUES ($1, 1, 'project', 1, 'Runbook', 'text/markdown; charset=utf-8', '# Runbook', $2, $2)
		RETURNING id::text
	`, projectID, ownerID).Scan(&contextID); err != nil {
		t.Fatalf("insert project context: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO storage_objects (
			project_id, number, backend, bucket, object_key, filename, content_type,
			byte_size, sha256, created_by_id
		)
		VALUES ($1::uuid, 1, 'local', 'local', 'projects/' || $1::text || '/objects/context-1',
			'runbook.png', 'image/png', 4, repeat('a', 64), $2::uuid)
		RETURNING id::text
	`, projectID, ownerID).Scan(&objectID); err != nil {
		t.Fatalf("insert storage object: %v", err)
	}

	projectSub := newTestClient(32)
	contextSub := newTestClient(32)
	hub.Subscribe(projectSub, "project:"+projectID)
	hub.Subscribe(contextSub, "project_context:"+contextID)
	var attachmentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO context_attachments (project_id, context_id, storage_object_id, created_by_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, projectID, contextID, objectID, ownerID).Scan(&attachmentID); err != nil {
		t.Fatalf("insert context attachment: %v", err)
	}
	waitForContextAttachmentEvent(t, projectSub, attachmentID, contextID, projectID, OpInsert)
	waitForContextAttachmentEvent(t, contextSub, attachmentID, contextID, projectID, OpInsert)
	if _, err := pool.Exec(ctx, `DELETE FROM context_attachments WHERE id = $1`, attachmentID); err != nil {
		t.Fatalf("delete context attachment: %v", err)
	}
	waitForContextAttachmentEvent(t, projectSub, attachmentID, contextID, projectID, OpDelete)
	waitForContextAttachmentEvent(t, contextSub, attachmentID, contextID, projectID, OpDelete)
}

// TestListenerReceivesProjectChangelogEvent verifies changelog inserts refresh
// listeners on the project topic.
func TestListenerReceivesProjectChangelogEvent(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-changelog")
	projectSub := newTestClient(16)
	hub.Subscribe(projectSub, "project:"+projectID)

	var entryID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO project_changelog_entries (project_id, entity, op, entity_id, target_ref, target_title, summary)
		VALUES ($1, 'project', 'insert', $1, 'TRACK', 'Track Slash', 'Created project TRACK')
		RETURNING id::text
	`, projectID).Scan(&entryID); err != nil {
		t.Fatalf("insert project_changelog_entries: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-projectSub.send:
			if ev.Entity != EntityChangelog {
				continue
			}
			if ev.Op != OpInsert {
				t.Errorf("op = %s, want insert", ev.Op)
			}
			if ev.ID.String() != entryID {
				t.Errorf("id = %s, want %s", ev.ID, entryID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatal("did not receive project changelog event within 3s")
		}
	}
}

// TestListenerReceivesProjectSprintModeUpdate verifies toggling sprint mode
// reaches project subscribers through the existing projects trigger, so open
// clients learn that the Sprint board appeared or disappeared.
func TestListenerReceivesProjectSprintModeUpdate(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	projectID := insertRealtimeProject(ctx, t, pool, "rt-sprint-mode")
	projectSub := newTestClient(16)
	hub.Subscribe(projectSub, "project:"+projectID)

	if _, err := pool.Exec(ctx, `UPDATE projects SET sprints_enabled = true WHERE id = $1`, projectID); err != nil {
		t.Fatalf("enable sprint mode: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-projectSub.send:
			// The project's own insert can still be in flight when the
			// subscription starts; only the update is under test.
			if ev.Entity != EntityProject || ev.Op == OpInsert {
				continue
			}
			if ev.Op != OpUpdate || ev.ID.String() != projectID {
				t.Fatalf("project event = %#v, want update of %s", ev, projectID)
			}
			if ev.Version < 2 {
				t.Fatalf("project event version = %d, want the bumped version", ev.Version)
			}
			return
		case <-deadline:
			t.Fatal("did not receive project sprint mode event within 3s")
		}
	}
}

// TestListenerReceivesSoftDeleteAsDelete verifies updating deleted_at emits a
// realtime delete op so subscribers see the same event kind as hard deletes.
func TestListenerReceivesSoftDeleteAsDelete(t *testing.T) {
	t.Parallel()
	ctx, pool, dbURL := newRealtimeDB(t)

	hub := NewHub()
	runRealtimeListener(t, ctx, dbURL, hub)

	time.Sleep(500 * time.Millisecond)

	var projectID, issueID string
	projectID = insertRealtimeProject(ctx, t, pool, "rt-soft-delete")
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, number, title) VALUES ($1, 1, 'A') RETURNING id::text
	`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}

	issueSub := newTestClient(16)
	hub.Subscribe(issueSub, "issue:"+issueID)

	if _, err := pool.Exec(ctx, `UPDATE issues SET deleted_at = now() WHERE id = $1`, issueID); err != nil {
		t.Fatalf("soft-delete issue: %v", err)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-issueSub.send:
			if ev.Entity != EntityIssue {
				continue
			}
			if ev.Op != OpDelete && ev.ID.String() == issueID {
				continue
			}
			if ev.Op != OpDelete {
				t.Fatalf("op = %s, want delete", ev.Op)
			}
			if ev.ID.String() != issueID {
				t.Fatalf("id = %s, want %s", ev.ID, issueID)
			}
			return
		case <-deadline:
			t.Fatal("did not receive soft-delete event within 3s")
		}
	}
}

func waitForCommentEvent(t *testing.T, c *Client, commentID, issueID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityComment || ev.Op != op {
				continue
			}
			if ev.ID.String() != commentID {
				t.Errorf("id = %s, want %s", ev.ID, commentID)
			}
			if ev.IssueID == nil || ev.IssueID.String() != issueID {
				t.Errorf("issue_id = %v, want %s", ev.IssueID, issueID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive comment %s event within 3s", op)
		}
	}
}

func waitForSubIssueEvent(t *testing.T, c *Client, childID, parentID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityIssue || ev.Op != op || ev.ID.String() != childID {
				continue
			}
			if ev.ParentIssueID == nil || ev.ParentIssueID.String() != parentID {
				t.Errorf("parent_issue_id = %v, want %s", ev.ParentIssueID, parentID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive sub-issue %s event within 3s", op)
		}
	}
}

func waitForProjectContextEvent(t *testing.T, c *Client, contextID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityContext || ev.Op != op || ev.ID.String() != contextID {
				continue
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive project context %s event within 3s", op)
		}
	}
}

func waitForIssueContextLinkEvent(t *testing.T, c *Client, linkID, issueID, contextID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityContextLink || ev.Op != op || ev.ID.String() != linkID {
				continue
			}
			if ev.IssueID == nil || ev.IssueID.String() != issueID {
				t.Errorf("issue_id = %v, want %s", ev.IssueID, issueID)
			}
			if ev.ContextID == nil || ev.ContextID.String() != contextID {
				t.Errorf("context_id = %v, want %s", ev.ContextID, contextID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive issue context link %s event within 3s", op)
		}
	}
}

func waitForIssueTagEvent(t *testing.T, c *Client, tagID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityIssueTag || ev.Op != op || ev.ID.String() != tagID {
				continue
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive issue tag %s event within 3s", op)
		}
	}
}

func waitForIssueTagLinkEvent(t *testing.T, c *Client, linkID, issueID, tagID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityIssueTagLink || ev.Op != op || ev.ID.String() != linkID {
				continue
			}
			if ev.IssueID == nil || ev.IssueID.String() != issueID {
				t.Errorf("issue_id = %v, want %s", ev.IssueID, issueID)
			}
			if ev.TagID == nil || ev.TagID.String() != tagID {
				t.Errorf("tag_id = %v, want %s", ev.TagID, tagID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive issue tag link %s event within 3s", op)
		}
	}
}

func waitForIssueAttachmentEvent(t *testing.T, c *Client, attachmentID, issueID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityAttachment || ev.Op != op || ev.ID.String() != attachmentID {
				continue
			}
			if ev.IssueID == nil || ev.IssueID.String() != issueID {
				t.Errorf("issue_id = %v, want %s", ev.IssueID, issueID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive issue attachment %s event within 3s", op)
		}
	}
}

func waitForSprintAttachmentEvent(t *testing.T, c *Client, attachmentID, sprintID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntitySprintAttachment || ev.Op != op || ev.ID.String() != attachmentID {
				continue
			}
			if ev.SprintID == nil || ev.SprintID.String() != sprintID {
				t.Errorf("sprint_id = %v, want %s", ev.SprintID, sprintID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive sprint attachment %s event within 3s", op)
		}
	}
}

func waitForProjectAttachmentEvent(t *testing.T, c *Client, attachmentID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityProjectAttachment || ev.Op != op || ev.ID.String() != attachmentID {
				continue
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive project attachment %s event within 3s", op)
		}
	}
}

func waitForWhiteboardPageEvent(t *testing.T, c *Client, pageID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityWhiteboardPage || ev.Op != op || ev.ID.String() != pageID {
				continue
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive whiteboard page %s event within 3s", op)
		}
	}
}

func waitForContextAttachmentEvent(t *testing.T, c *Client, attachmentID, contextID, projectID string, op Op) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-c.send:
			if ev.Entity != EntityContextAttachment || ev.Op != op || ev.ID.String() != attachmentID {
				continue
			}
			if ev.ContextID == nil || ev.ContextID.String() != contextID {
				t.Errorf("context_id = %v, want %s", ev.ContextID, contextID)
			}
			if ev.ProjectID == nil || ev.ProjectID.String() != projectID {
				t.Errorf("project_id = %v, want %s", ev.ProjectID, projectID)
			}
			return
		case <-deadline:
			t.Fatalf("did not receive context attachment %s event within 3s", op)
		}
	}
}

func uniqueKey(t *testing.T) string {
	t.Helper()
	return "rt_" + time.Now().Format("150405.000000")
}

func insertRealtimeProject(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var ownerID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, name)
		VALUES ($1, $2, 'Realtime Owner')
		RETURNING id::text
	`, "rtowner"+suffix, "rt-owner-"+suffix+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	var projectID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (owner_id, key, name)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, ownerID, uniqueKey(t), name).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	return projectID
}
