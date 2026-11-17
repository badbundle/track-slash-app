package storage

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bradleymackey/track-slash/internal/store"
)

const maxDeletionErrorLength = 2000

type DeletionWorkerOptions struct {
	BatchSize     int
	MaxAttempts   int
	PollInterval  time.Duration
	LeaseTimeout  time.Duration
	DeleteTimeout time.Duration
	MinBackoff    time.Duration
	MaxBackoff    time.Duration
	Logger        *log.Logger
	// FormUploadSweepEvery is how often Run sweeps away the files uploaded
	// for forms that no form claimed (default one hour), and FormUploadMaxAge
	// how old an unclaimed upload must be before it goes (default 24 hours).
	FormUploadSweepEvery time.Duration
	FormUploadMaxAge     time.Duration
}

type DeletionRunResult struct {
	Claimed int
	Deleted int
	// Kept counts jobs completed without deleting, because another live
	// object, such as a repeating issue's attachment copy, uses the bytes.
	Kept    int
	Retried int
	Failed  int
}

type DeletionWorker struct {
	store         *store.Store
	service       *Service
	batchSize     int
	maxAttempts   int
	pollInterval  time.Duration
	leaseTimeout  time.Duration
	deleteTimeout time.Duration
	minBackoff    time.Duration
	maxBackoff    time.Duration
	logger        *log.Logger

	formUploadSweepEvery time.Duration
	formUploadMaxAge     time.Duration
	lastFormUploadSweep  time.Time
}

const (
	defaultFormUploadSweepEvery = time.Hour
	defaultFormUploadMaxAge     = 24 * time.Hour
)

func NewDeletionWorker(st *store.Store, service *Service, opts DeletionWorkerOptions) *DeletionWorker {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 8
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.LeaseTimeout <= 0 {
		opts.LeaseTimeout = time.Minute
	}
	if opts.DeleteTimeout <= 0 {
		opts.DeleteTimeout = 10 * time.Second
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = 5 * time.Second
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 5 * time.Minute
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = opts.MinBackoff
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.FormUploadSweepEvery <= 0 {
		opts.FormUploadSweepEvery = defaultFormUploadSweepEvery
	}
	if opts.FormUploadMaxAge <= 0 {
		opts.FormUploadMaxAge = defaultFormUploadMaxAge
	}
	return &DeletionWorker{
		store:         st,
		service:       service,
		batchSize:     opts.BatchSize,
		maxAttempts:   opts.MaxAttempts,
		pollInterval:  opts.PollInterval,
		leaseTimeout:  opts.LeaseTimeout,
		deleteTimeout: opts.DeleteTimeout,
		minBackoff:    opts.MinBackoff,
		maxBackoff:    opts.MaxBackoff,
		logger:        opts.Logger,

		formUploadSweepEvery: opts.FormUploadSweepEvery,
		formUploadMaxAge:     opts.FormUploadMaxAge,
	}
}

func (w *DeletionWorker) Run(ctx context.Context) {
	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.logger.Printf("storage deletion worker: %v", err)
		}
		w.sweepFormUploadsIfDue(ctx)
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

// sweepFormUploadsIfDue sweeps the unclaimed form uploads when one sweep
// interval has passed since the last. The files it removes are soft-deleted,
// so the next RunOnce deletes their bytes like any other removed object.
func (w *DeletionWorker) sweepFormUploadsIfDue(ctx context.Context) {
	if w.store == nil || time.Since(w.lastFormUploadSweep) < w.formUploadSweepEvery {
		return
	}
	w.lastFormUploadSweep = time.Now()
	if _, err := w.SweepFormUploads(ctx); err != nil && ctx.Err() == nil {
		w.logger.Printf("storage deletion worker: sweep form uploads: %v", err)
	}
}

// SweepFormUploads removes the form uploads no form claimed within the
// maximum age, and the upload grants that expired that long ago.
func (w *DeletionWorker) SweepFormUploads(ctx context.Context) (store.FormUploadSweep, error) {
	return w.store.SweepFormUploads(ctx, w.formUploadMaxAge)
}

func (w *DeletionWorker) RunOnce(ctx context.Context) (DeletionRunResult, error) {
	if w.store == nil || w.service == nil {
		return DeletionRunResult{}, errors.New("storage deletion worker requires store and service")
	}
	jobs, err := w.store.ClaimStorageObjectDeletions(ctx, w.batchSize, w.leaseTimeout)
	if err != nil {
		return DeletionRunResult{}, fmt.Errorf("claim jobs: %w", err)
	}
	result := DeletionRunResult{Claimed: len(jobs)}
	for _, job := range jobs {
		if job.Backend != w.service.BackendName() || job.Bucket != w.service.Bucket() {
			err := fmt.Errorf("configured storage is %s/%s, job requires %s/%s", w.service.BackendName(), w.service.Bucket(), job.Backend, job.Bucket)
			if err := w.failJob(ctx, job, err, true); err != nil {
				return result, err
			}
			result.Failed++
			continue
		}

		// No new object can start using these bytes once this check finds
		// none: a copy is only made from a live object, under a lock its
		// deletion has to wait for.
		inUse, deleteErr := w.store.StorageObjectBytesInUse(ctx, job.Backend, job.Bucket, job.ObjectKey)
		if deleteErr == nil && inUse {
			if err := w.store.CompleteStorageObjectDeletion(ctx, job.StorageObjectID, job.AttemptCount); err != nil {
				return result, fmt.Errorf("complete job %s: %w", job.StorageObjectID, err)
			}
			result.Kept++
			continue
		}
		if deleteErr == nil {
			deleteCtx, cancel := context.WithTimeout(ctx, w.deleteTimeout)
			deleteErr = w.service.Delete(deleteCtx, job.ObjectKey)
			cancel()
		}
		if deleteErr == nil || errors.Is(deleteErr, ErrNotFound) {
			if err := w.store.CompleteStorageObjectDeletion(ctx, job.StorageObjectID, job.AttemptCount); err != nil {
				return result, fmt.Errorf("complete job %s: %w", job.StorageObjectID, err)
			}
			result.Deleted++
			continue
		}

		terminal := job.AttemptCount >= w.maxAttempts
		if err := w.failJob(ctx, job, deleteErr, terminal); err != nil {
			return result, err
		}
		if terminal {
			result.Failed++
		} else {
			result.Retried++
		}
	}
	return result, nil
}

func (w *DeletionWorker) failJob(ctx context.Context, job store.StorageObjectDeletion, deletionErr error, terminal bool) error {
	message := boundedDeletionError(deletionErr)
	if terminal {
		if err := w.store.FailStorageObjectDeletion(ctx, job.StorageObjectID, job.AttemptCount, message); err != nil {
			return fmt.Errorf("fail job %s: %w", job.StorageObjectID, err)
		}
		w.logger.Printf(
			"storage deletion terminal failure object_id=%s backend=%s bucket=%s key=%s attempts=%d error=%s",
			job.StorageObjectID, job.Backend, job.Bucket, job.ObjectKey, job.AttemptCount, message,
		)
		return nil
	}

	nextAttemptAt := time.Now().Add(deletionBackoff(job.AttemptCount, w.minBackoff, w.maxBackoff))
	if err := w.store.RetryStorageObjectDeletion(ctx, job.StorageObjectID, job.AttemptCount, nextAttemptAt, message); err != nil {
		return fmt.Errorf("retry job %s: %w", job.StorageObjectID, err)
	}
	return nil
}

func deletionBackoff(attempt int, minimum, maximum time.Duration) time.Duration {
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

func boundedDeletionError(err error) string {
	message := strings.TrimSpace(err.Error())
	if len(message) <= maxDeletionErrorLength {
		return message
	}
	return message[:maxDeletionErrorLength]
}
