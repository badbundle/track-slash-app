package notifications

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bradleymackey/track-slash/internal/store"
)

const maxPushNotificationErrorLength = 2000

// pushNotificationPruneInterval is how often finished queue rows are pruned.
const pushNotificationPruneInterval = time.Hour

type WorkerOptions struct {
	BatchSize    int
	MaxAttempts  int
	PollInterval time.Duration
	// LeaseTimeout is how long a claim is held. It is never shorter than a
	// whole batch of sends plus a minute, or another worker could reclaim
	// deliveries that are still being sent and send them twice.
	LeaseTimeout time.Duration
	SendTimeout  time.Duration
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	MaxEventAge  time.Duration
	// Retention is how long finished events and deliveries are kept.
	Retention time.Duration
	Logger    *log.Logger
}

type RunResult struct {
	EventsExpired     int
	EventsClaimed     int
	DeliveriesCreated int
	DeliveriesClaimed int
	Delivered         int
	Retried           int
	Suppressed        int
	Disabled          int
	Failed            int
	Pruned            int
}

type Worker struct {
	store        *store.Store
	sender       Sender
	batchSize    int
	maxAttempts  int
	pollInterval time.Duration
	leaseTimeout time.Duration
	sendTimeout  time.Duration
	minBackoff   time.Duration
	maxBackoff   time.Duration
	maxEventAge  time.Duration
	retention    time.Duration
	logger       *log.Logger
	prunedAt     time.Time
}

func NewWorker(st *store.Store, sender Sender, opts WorkerOptions) *Worker {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 20
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 8
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.SendTimeout <= 0 {
		opts.SendTimeout = 10 * time.Second
	}
	if minimum := time.Duration(opts.BatchSize)*opts.SendTimeout + time.Minute; opts.LeaseTimeout < minimum {
		opts.LeaseTimeout = minimum
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = 5 * time.Second
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 15 * time.Minute
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = opts.MinBackoff
	}
	if opts.MaxEventAge <= 0 {
		opts.MaxEventAge = 24 * time.Hour
	}
	if opts.Retention <= 0 {
		opts.Retention = 7 * 24 * time.Hour
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	return &Worker{
		store: st, sender: sender, batchSize: opts.BatchSize, maxAttempts: opts.MaxAttempts,
		pollInterval: opts.PollInterval, leaseTimeout: opts.LeaseTimeout, sendTimeout: opts.SendTimeout,
		minBackoff: opts.MinBackoff, maxBackoff: opts.MaxBackoff, maxEventAge: opts.MaxEventAge,
		retention: opts.Retention, logger: opts.Logger,
	}
}

func (w *Worker) Run(ctx context.Context) {
	for {
		result, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.Printf("push notification worker: %v", err)
		}
		// A full batch means more is waiting, so the next starts straight away.
		if err == nil && (result.EventsClaimed >= w.batchSize || result.DeliveriesClaimed >= w.batchSize) {
			continue
		}
		timer := time.NewTimer(w.pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (RunResult, error) {
	if w.store == nil || w.sender == nil {
		return RunResult{}, errors.New("push notification worker requires store and sender")
	}
	result := RunResult{}
	expired, err := w.store.ExpirePushNotificationEvents(ctx, w.maxEventAge)
	if err != nil {
		return result, fmt.Errorf("expire events: %w", err)
	}
	result.EventsExpired = int(expired)
	events, err := w.store.ClaimPushNotificationEvents(ctx, w.batchSize, w.leaseTimeout)
	if err != nil {
		return result, fmt.Errorf("claim events: %w", err)
	}
	result.EventsClaimed = len(events)
	for _, event := range events {
		created, err := w.store.MaterializePushNotificationEvent(ctx, event, w.maxEventAge)
		if err == nil {
			result.DeliveriesCreated += created
			continue
		}
		if retryErr := w.failEvent(ctx, event, err); retryErr != nil {
			return result, retryErr
		}
		if event.AttemptCount >= w.maxAttempts {
			result.Failed++
		} else {
			result.Retried++
		}
	}

	deliveries, err := w.store.ClaimPushNotificationDeliveries(ctx, w.batchSize, w.leaseTimeout)
	if err != nil {
		return result, fmt.Errorf("claim deliveries: %w", err)
	}
	result.DeliveriesClaimed = len(deliveries)
	for _, delivery := range deliveries {
		payload, authorized, err := w.store.PreparePushNotificationDelivery(ctx, delivery)
		if err != nil {
			if retryErr := w.failDelivery(ctx, delivery, err); retryErr != nil {
				return result, retryErr
			}
			if delivery.AttemptCount >= w.maxAttempts {
				result.Failed++
			} else {
				result.Retried++
			}
			continue
		}
		if !authorized {
			if err := w.store.SuppressPushNotificationDelivery(ctx, delivery, "recipient no longer has issue access"); err != nil {
				return result, fmt.Errorf("suppress delivery %s: %w", delivery.ID, err)
			}
			result.Suppressed++
			continue
		}

		sendCtx, cancel := context.WithTimeout(ctx, w.sendTimeout)
		status, sendErr := w.sender.Send(sendCtx, delivery, payload)
		cancel()
		if sendErr == nil && status >= http.StatusOK && status < http.StatusMultipleChoices {
			if err := w.store.CompletePushNotificationDelivery(ctx, delivery); err != nil {
				return result, fmt.Errorf("complete delivery %s: %w", delivery.ID, err)
			}
			result.Delivered++
			continue
		}
		if sendErr == nil && rejectedPushSubscriptionStatus(status) {
			message := fmt.Sprintf("push provider rejected subscription with status %d", status)
			if err := w.store.DisableRejectedPushSubscription(ctx, delivery, message); err != nil {
				return result, fmt.Errorf("disable subscription %s: %w", delivery.SubscriptionID, err)
			}
			w.logger.Printf("push notification disabled subscription_id=%s status=%d", delivery.SubscriptionID, status)
			result.Disabled++
			result.Suppressed++
			continue
		}

		deliveryErr := sendErr
		if deliveryErr == nil {
			deliveryErr = fmt.Errorf("push provider returned status %d", status)
		}
		if sendErr == nil && !retryablePushStatus(status) {
			if err := w.store.FailPushNotificationDelivery(ctx, delivery, boundedPushNotificationError(deliveryErr)); err != nil {
				return result, fmt.Errorf("fail delivery %s: %w", delivery.ID, err)
			}
			w.logger.Printf("push notification terminal failure delivery_id=%s status=%d", delivery.ID, status)
			result.Failed++
			continue
		}
		if retryErr := w.failDelivery(ctx, delivery, deliveryErr); retryErr != nil {
			return result, retryErr
		}
		if delivery.AttemptCount >= w.maxAttempts {
			result.Failed++
		} else {
			result.Retried++
		}
	}

	if time.Since(w.prunedAt) >= pushNotificationPruneInterval {
		pruned, err := w.store.PrunePushNotifications(ctx, w.retention)
		if err != nil {
			// Defensive: the retention is always positive, so only a DB outage
			// reaches this.
			return result, fmt.Errorf("prune: %w", err)
		}
		result.Pruned = int(pruned)
		w.prunedAt = time.Now()
	}
	return result, nil
}

func (w *Worker) failEvent(ctx context.Context, event store.PushNotificationEvent, eventErr error) error {
	message := boundedPushNotificationError(eventErr)
	if event.AttemptCount >= w.maxAttempts {
		if err := w.store.FailPushNotificationEvent(ctx, event, message); err != nil {
			return fmt.Errorf("fail event %s: %w", event.ChangelogID, err)
		}
		w.logger.Printf("push notification event terminal failure changelog_id=%s attempts=%d error=%s", event.ChangelogID, event.AttemptCount, message)
		return nil
	}
	nextAttemptAt := time.Now().Add(pushNotificationBackoff(event.AttemptCount, w.minBackoff, w.maxBackoff))
	if err := w.store.RetryPushNotificationEvent(ctx, event, nextAttemptAt, message); err != nil {
		return fmt.Errorf("retry event %s: %w", event.ChangelogID, err)
	}
	return nil
}

func (w *Worker) failDelivery(ctx context.Context, delivery store.PushNotificationDelivery, deliveryErr error) error {
	message := boundedPushNotificationError(deliveryErr)
	if delivery.AttemptCount >= w.maxAttempts {
		if err := w.store.FailPushNotificationDelivery(ctx, delivery, message); err != nil {
			return fmt.Errorf("fail delivery %s: %w", delivery.ID, err)
		}
		w.logger.Printf("push notification terminal failure delivery_id=%s attempts=%d error=%s", delivery.ID, delivery.AttemptCount, message)
		return nil
	}
	nextAttemptAt := time.Now().Add(pushNotificationBackoff(delivery.AttemptCount, w.minBackoff, w.maxBackoff))
	if err := w.store.RetryPushNotificationDelivery(ctx, delivery, nextAttemptAt, message); err != nil {
		return fmt.Errorf("retry delivery %s: %w", delivery.ID, err)
	}
	return nil
}

func rejectedPushSubscriptionStatus(status int) bool {
	return status == http.StatusBadRequest || status == http.StatusNotFound || status == http.StatusGone
}

func retryablePushStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

func pushNotificationBackoff(attempt int, minimum, maximum time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := minimum
	for i := 1; i < attempt; i++ {
		if delay >= maximum || delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func boundedPushNotificationError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) <= maxPushNotificationErrorLength {
		return message
	}
	return message[:maxPushNotificationErrorLength]
}
