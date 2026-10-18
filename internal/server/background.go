package server

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// backgroundTaskTimeout bounds work a handler leaves running after its
// response. A delivery gives up after 10 seconds, which leaves room for the
// lookups around it.
const backgroundTaskTimeout = 15 * time.Second

// backgroundTasks tracks work running after its response was written, so
// shutdown, and tests, can wait for it.
type backgroundTasks struct {
	// mu orders each Add before any Wait that starts after it.
	mu sync.Mutex
	wg sync.WaitGroup
}

// goBackground runs fn in its own goroutine once the handler has returned,
// with ctx's values but not its cancellation or deadline, bounded instead by
// backgroundTaskTimeout. The handler must hand fn everything it needs: fn
// runs after the response and must not touch the request. A panic in fn is
// logged under source rather than taking the process down.
func (s *Server) goBackground(ctx context.Context, source string, fn func(context.Context)) {
	ctx = context.WithoutCancel(ctx)
	s.background.mu.Lock()
	s.background.wg.Add(1)
	s.background.mu.Unlock()
	go func() {
		defer s.background.wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				logInternalError(source, fmt.Errorf("panic: %v", recovered))
			}
		}()
		ctx, cancel := context.WithTimeout(ctx, backgroundTaskTimeout)
		defer cancel()
		fn(ctx)
	}()
}

// WaitBackground waits until the work handlers left running after their
// responses has finished, or until ctx ends. Call it once the server has
// stopped taking requests.
func (s *Server) WaitBackground(ctx context.Context) error {
	// Taking the lock orders this wait after every task started so far.
	s.background.mu.Lock()
	s.background.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.background.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
