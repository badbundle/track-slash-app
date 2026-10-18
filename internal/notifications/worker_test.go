package notifications

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewWorkerOptions(t *testing.T) {
	t.Parallel()
	defaults := NewWorker(nil, nil, WorkerOptions{})
	// The default lease covers a full batch of 20 sends of up to 10s each.
	if defaults.batchSize != 20 || defaults.maxAttempts != 8 || defaults.pollInterval != 2*time.Second ||
		defaults.leaseTimeout != 260*time.Second || defaults.sendTimeout != 10*time.Second ||
		defaults.minBackoff != 5*time.Second || defaults.maxBackoff != 15*time.Minute ||
		defaults.maxEventAge != 24*time.Hour || defaults.retention != 7*24*time.Hour || defaults.logger == nil {
		t.Fatalf("default worker = %+v", defaults)
	}
	logger := log.New(io.Discard, "", 0)
	configured := NewWorker(nil, nil, WorkerOptions{
		BatchSize: 2, MaxAttempts: 3, PollInterval: 4 * time.Second, LeaseTimeout: 10 * time.Minute,
		SendTimeout: 6 * time.Second, MinBackoff: 7 * time.Second, MaxBackoff: time.Second,
		MaxEventAge: 8 * time.Second, Retention: 9 * time.Hour, Logger: logger,
	})
	if configured.batchSize != 2 || configured.maxAttempts != 3 || configured.pollInterval != 4*time.Second ||
		configured.leaseTimeout != 10*time.Minute || configured.sendTimeout != 6*time.Second ||
		configured.minBackoff != 7*time.Second || configured.maxBackoff != 7*time.Second ||
		configured.maxEventAge != 8*time.Second || configured.retention != 9*time.Hour || configured.logger != logger {
		t.Fatalf("configured worker = %+v", configured)
	}
	// A lease shorter than a batch would let another worker reclaim deliveries
	// still being sent, so it is raised to cover one.
	short := NewWorker(nil, nil, WorkerOptions{BatchSize: 2, SendTimeout: 6 * time.Second, LeaseTimeout: 5 * time.Second})
	if short.leaseTimeout != 72*time.Second {
		t.Fatalf("short lease = %v, want 72s", short.leaseTimeout)
	}
}

func TestWorkerRequiresDependenciesAndStops(t *testing.T) {
	t.Parallel()
	worker := NewWorker(nil, nil, WorkerOptions{})
	if _, err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce returned nil error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	NewWorker(nil, nil, WorkerOptions{PollInterval: time.Hour}).Run(ctx)

	ctx, cancel = context.WithCancel(context.Background())
	var logs bytes.Buffer
	NewWorker(nil, nil, WorkerOptions{
		PollInterval: time.Millisecond,
		Logger:       log.New(notificationCancelWriter{Writer: &logs, cancel: cancel}, "", 0),
	}).Run(ctx)
	if !strings.Contains(logs.String(), "push notification worker") {
		t.Fatalf("worker log = %q", logs.String())
	}
}

type notificationCancelWriter struct {
	io.Writer
	cancel context.CancelFunc
}

func (w notificationCancelWriter) Write(p []byte) (int, error) {
	w.cancel()
	return w.Writer.Write(p)
}

func TestPushProviderStatusClassification(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusGone} {
		if !rejectedPushSubscriptionStatus(status) {
			t.Fatalf("status %d not rejected", status)
		}
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if rejectedPushSubscriptionStatus(status) {
			t.Fatalf("status %d unexpectedly rejected", status)
		}
	}
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if !retryablePushStatus(status) {
			t.Fatalf("status %d not retryable", status)
		}
	}
	for _, status := range []int{http.StatusCreated, http.StatusBadRequest, http.StatusUnauthorized} {
		if retryablePushStatus(status) {
			t.Fatalf("status %d unexpectedly retryable", status)
		}
	}
}

func TestPushNotificationBackoffAndBoundedError(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second}, {1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second},
		{4, 5 * time.Second}, {100, 5 * time.Second},
	} {
		if got := pushNotificationBackoff(test.attempt, time.Second, 5*time.Second); got != test.want {
			t.Fatalf("backoff %d = %v, want %v", test.attempt, got, test.want)
		}
	}
	if got := pushNotificationBackoff(1, 10*time.Second, 5*time.Second); got != 5*time.Second {
		t.Fatalf("bounded minimum = %v", got)
	}
	if got := boundedPushNotificationError(errors.New("  retry me  ")); got != "retry me" {
		t.Fatalf("bounded short error = %q", got)
	}
	long := strings.Repeat("x", maxPushNotificationErrorLength+1)
	if got := boundedPushNotificationError(errors.New(long)); len(got) != maxPushNotificationErrorLength {
		t.Fatalf("bounded error length = %d", len(got))
	}
}

func TestNewWebPushSenderRequiresConfiguration(t *testing.T) {
	t.Parallel()
	if _, err := NewWebPushSender("", "private", "mailto:ops@example.com", nil); err == nil {
		t.Fatal("NewWebPushSender returned nil error")
	}
	if sender, err := NewWebPushSender("public", "private", "mailto:ops@example.com", nil); err != nil || sender == nil {
		t.Fatalf("NewWebPushSender = %+v, %v", sender, err)
	}
}
