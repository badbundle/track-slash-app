package notifications

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pushWorkerTestEnv struct {
	ctx          context.Context
	pool         *pgxpool.Pool
	store        *store.Store
	projectID    uuid.UUID
	owner        model.User
	member       model.User
	issue        model.Issue
	subscription model.PushSubscription
}

func newPushWorkerTestEnv(t *testing.T) *pushWorkerTestEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db := testutil.NewMigratedDatabase(t)
	st := store.New(db.Pool)
	suffix := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	owner, err := st.CreateOrUpdateAdminUser(ctx, "push-worker-"+suffix+"@example.com", "Push Worker")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	project, err := st.CreateProjectForUser(ctx, owner.ID, "N"+suffix[:9], "Push Worker", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	member, err := st.CreateUserProfile(ctx, "member"+strings.ToLower(suffix[:12]), "member-"+suffix+"@example.com", "Member")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	if _, err := st.GrantProjectAccess(ctx, project.ID, member.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	issue, err := st.CreateIssue(ctx, store.CreateIssueParams{ProjectID: project.ID, Title: "Worker delivery"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	subscription, err := st.UpsertPushSubscription(ctx, store.UpsertPushSubscriptionParams{
		UserID: member.ID, Endpoint: "https://push.example.test/" + uuid.NewString(),
		P256DH:     base64.RawURLEncoding.EncodeToString(make([]byte, 65)),
		AuthSecret: base64.RawURLEncoding.EncodeToString(make([]byte, 16)), UserAgent: "worker test",
	})
	if err != nil {
		t.Fatalf("UpsertPushSubscription: %v", err)
	}
	if _, err := st.UpdateIssue(store.WithActor(ctx, owner.ID), issue.ID, store.UpdateIssueParams{AssigneeID: &member.ID}); err != nil {
		t.Fatalf("UpdateIssue assignee: %v", err)
	}
	return &pushWorkerTestEnv{
		ctx: ctx, pool: db.Pool, store: st, projectID: project.ID, owner: owner,
		member: member, issue: issue, subscription: subscription,
	}
}

type recordingPushSender struct {
	status   int
	err      error
	calls    int
	payloads []store.PushNotificationPayload
}

func (s *recordingPushSender) Send(_ context.Context, _ store.PushNotificationDelivery, payload store.PushNotificationPayload) (int, error) {
	s.calls++
	s.payloads = append(s.payloads, payload)
	return s.status, s.err
}

func TestPushWorkerDeliversAssignment(t *testing.T) {
	t.Parallel()
	env := newPushWorkerTestEnv(t)
	sender := &recordingPushSender{status: 201}
	result, err := NewWorker(env.store, sender, WorkerOptions{}).RunOnce(env.ctx)
	if err != nil || result.EventsClaimed != 2 || result.DeliveriesCreated != 1 ||
		result.DeliveriesClaimed != 1 || result.Delivered != 1 || sender.calls != 1 {
		t.Fatalf("RunOnce = %+v, sender calls=%d err=%v", result, sender.calls, err)
	}
	if len(sender.payloads) != 1 || !strings.Contains(sender.payloads[0].Title, env.issue.Identifier) ||
		!strings.Contains(sender.payloads[0].Body, "assigned this issue to you") {
		t.Fatalf("payloads = %+v", sender.payloads)
	}
	if result, err := NewWorker(env.store, sender, WorkerOptions{}).RunOnce(env.ctx); err != nil || result != (RunResult{}) {
		t.Fatalf("empty RunOnce = %+v, %v", result, err)
	}
}

func TestPushWorkerDisablesRejectedSubscription(t *testing.T) {
	t.Parallel()
	env := newPushWorkerTestEnv(t)
	sender := &recordingPushSender{status: 410}
	result, err := NewWorker(env.store, sender, WorkerOptions{}).RunOnce(env.ctx)
	if err != nil || result.Disabled != 1 || result.Suppressed != 1 || sender.calls != 1 {
		t.Fatalf("RunOnce = %+v, sender calls=%d err=%v", result, sender.calls, err)
	}
	if active, err := env.store.PushSubscriptionActive(env.ctx, env.member.ID, env.subscription.Endpoint); err != nil || active {
		t.Fatalf("rejected subscription active = %v, %v", active, err)
	}
}

func TestPushWorkerRetriesThenRecordsTerminalFailure(t *testing.T) {
	t.Parallel()
	env := newPushWorkerTestEnv(t)
	sender := &recordingPushSender{err: errors.New("provider unavailable")}
	worker := NewWorker(env.store, sender, WorkerOptions{
		MaxAttempts: 2, MinBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
	})
	first, err := worker.RunOnce(env.ctx)
	if err != nil || first.Retried != 1 || first.DeliveriesClaimed != 1 {
		t.Fatalf("first RunOnce = %+v, %v", first, err)
	}
	if _, err := env.pool.Exec(env.ctx, `
		UPDATE push_notification_deliveries SET next_attempt_at = now()
		WHERE delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
	`); err != nil {
		t.Fatalf("make retry due: %v", err)
	}
	second, err := worker.RunOnce(env.ctx)
	if err != nil || second.Failed != 1 || second.DeliveriesClaimed != 1 {
		t.Fatalf("second RunOnce = %+v, %v", second, err)
	}
	var attempts int
	var lastError string
	var failedAt *time.Time
	if err := env.pool.QueryRow(env.ctx, `
		SELECT attempt_count, last_error, failed_at
		FROM push_notification_deliveries
		WHERE subscription_id = $1
	`, env.subscription.ID).Scan(&attempts, &lastError, &failedAt); err != nil {
		t.Fatalf("read failed delivery: %v", err)
	}
	if attempts != 2 || lastError != "provider unavailable" || failedAt == nil {
		t.Fatalf("failed delivery attempts=%d error=%q failed_at=%v", attempts, lastError, failedAt)
	}
}

func TestPushWorkerSuppressesDeliveryAfterAccessRevocation(t *testing.T) {
	t.Parallel()
	env := newPushWorkerTestEnv(t)
	events, err := env.store.ClaimPushNotificationEvents(env.ctx, 100, time.Minute)
	if err != nil {
		t.Fatalf("ClaimPushNotificationEvents: %v", err)
	}
	for _, event := range events {
		if _, err := env.store.MaterializePushNotificationEvent(env.ctx, event, 24*time.Hour); err != nil {
			t.Fatalf("MaterializePushNotificationEvent: %v", err)
		}
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, env.member.ID); err != nil {
		t.Fatalf("RevokeProjectAccess: %v", err)
	}
	sender := &recordingPushSender{status: 201}
	result, err := NewWorker(env.store, sender, WorkerOptions{}).RunOnce(env.ctx)
	if err != nil || result.DeliveriesClaimed != 1 || result.Suppressed != 1 || sender.calls != 0 {
		t.Fatalf("RunOnce = %+v, sender calls=%d err=%v", result, sender.calls, err)
	}
}

func TestPushWorkerRecordsNonRetryableProviderFailure(t *testing.T) {
	t.Parallel()
	env := newPushWorkerTestEnv(t)
	sender := &recordingPushSender{status: 401}
	result, err := NewWorker(env.store, sender, WorkerOptions{}).RunOnce(env.ctx)
	if err != nil || result.Failed != 1 || sender.calls != 1 {
		t.Fatalf("RunOnce = %+v, sender calls=%d err=%v", result, sender.calls, err)
	}
}

// Events queued while push was off are expired together before the queue is
// worked, and then pruned, so a backlog never holds up a new notification.
func TestPushWorkerExpiresAndPrunesBacklog(t *testing.T) {
	t.Parallel()
	env := newPushWorkerTestEnv(t)
	if _, err := env.pool.Exec(env.ctx, `
		UPDATE push_notification_events
		SET created_at = now() - interval '30 days', next_attempt_at = now() - interval '30 days'
	`); err != nil {
		t.Fatalf("backdate events: %v", err)
	}
	if _, err := env.store.CreateIssue(store.WithActor(env.ctx, env.owner.ID), store.CreateIssueParams{
		ProjectID: env.projectID, Title: "Fresh assignment", AssigneeID: &env.member.ID,
	}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	sender := &recordingPushSender{status: 201}
	worker := NewWorker(env.store, sender, WorkerOptions{BatchSize: 1})
	result, err := worker.RunOnce(env.ctx)
	want := RunResult{EventsExpired: 2, EventsClaimed: 1, DeliveriesCreated: 1, DeliveriesClaimed: 1, Delivered: 1, Pruned: 2}
	if err != nil || result != want || sender.calls != 1 {
		t.Fatalf("RunOnce = %+v, sender calls=%d err=%v; want %+v", result, sender.calls, err, want)
	}
	if len(sender.payloads) != 1 || !strings.Contains(sender.payloads[0].Body, "Fresh assignment") {
		t.Fatalf("payloads = %+v", sender.payloads)
	}

	if _, err := env.pool.Exec(env.ctx, `UPDATE push_notification_events SET created_at = now() - interval '30 days'`); err != nil {
		t.Fatalf("age events: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx, `UPDATE push_notification_deliveries SET created_at = now() - interval '30 days'`); err != nil {
		t.Fatalf("age deliveries: %v", err)
	}
	// Pruning runs at most hourly in each worker.
	if result, err := worker.RunOnce(env.ctx); err != nil || result != (RunResult{}) {
		t.Fatalf("RunOnce within the prune interval = %+v, %v", result, err)
	}
	if result, err := NewWorker(env.store, sender, WorkerOptions{}).RunOnce(env.ctx); err != nil || result != (RunResult{Pruned: 2}) {
		t.Fatalf("RunOnce after the prune interval = %+v, %v", result, err)
	}
	var rows int
	if err := env.pool.QueryRow(env.ctx, `
		SELECT (SELECT count(*) FROM push_notification_events) + (SELECT count(*) FROM push_notification_deliveries)
	`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("queue rows after pruning = %d, %v", rows, err)
	}
}

type cancelingPushSender struct {
	cancel context.CancelFunc
	calls  int
}

func (s *cancelingPushSender) Send(context.Context, store.PushNotificationDelivery, store.PushNotificationPayload) (int, error) {
	s.calls++
	s.cancel()
	return 201, nil
}

// A full batch means more is waiting, so the worker goes straight on to the
// next rather than waiting out the poll interval.
func TestPushWorkerRunDrainsFullBatchesWithoutWaiting(t *testing.T) {
	t.Parallel()
	env := newPushWorkerTestEnv(t)
	ctx, cancel := context.WithTimeout(env.ctx, 10*time.Second)
	defer cancel()
	sender := &cancelingPushSender{cancel: cancel}
	// The first batch of one holds the issue's creation, which notifies nobody;
	// only a second batch reaches the assignment.
	NewWorker(env.store, sender, WorkerOptions{
		BatchSize: 1, PollInterval: time.Hour, Logger: log.New(io.Discard, "", 0),
	}).Run(ctx)
	if sender.calls != 1 || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("sender calls = %d, ctx err = %v; want one send before the deadline", sender.calls, ctx.Err())
	}
}

func TestPushWorkerReportsExpiryFailure(t *testing.T) {
	t.Parallel()
	db := testutil.NewMigratedDatabase(t)
	st := store.New(db.Pool)
	db.Pool.Close()
	if _, err := NewWorker(st, &recordingPushSender{}, WorkerOptions{}).RunOnce(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "expire events") {
		t.Fatalf("RunOnce on a closed pool = %v", err)
	}
}
