package admin

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/sylvinhio676-ux/tenant-core/eventbus"
)

const (
	defaultMaxRetries  = 5
	defaultMaxSize     = 1000
	defaultBaseBackoff = time.Second
	tickInterval       = 200 * time.Millisecond
)

// pendingEvent tracks one event awaiting (re)publication, along with how
// many attempts have already failed and when the next attempt is due.
type pendingEvent struct {
	event         eventbus.TenantEvent
	attempts      int
	nextAttemptAt time.Time
}

// PublishRetryQueue provides best-effort in-memory retry for event
// publication. It is NOT a durable Outbox — pending events are lost if
// the process terminates. Use this to absorb transient EventBus
// failures, not as a guarantee of eventual delivery.
type PublishRetryQueue struct {
	bus         eventbus.EventBus
	mu          sync.Mutex
	pending     []*pendingEvent
	maxRetries  int           // default 5
	maxSize     int           // default 1000
	baseBackoff time.Duration // default 1 second
	stopCh      chan struct{}
	doneCh      chan struct{}
}

// NewPublishRetryQueue creates a PublishRetryQueue that retries
// publishing to bus with the default policy: 5 retries, a 1000-event
// bound, and a 1-second base exponential backoff.
func NewPublishRetryQueue(bus eventbus.EventBus) *PublishRetryQueue {
	return &PublishRetryQueue{
		bus:         bus,
		maxRetries:  defaultMaxRetries,
		maxSize:     defaultMaxSize,
		baseBackoff: defaultBaseBackoff,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

// Enqueue schedules event for an immediate publish attempt on the next
// worker tick. If the queue is already at maxSize, the oldest pending
// event is dropped (and logged as a critical loss) to make room — this
// queue is a bounded, best-effort buffer, not an unbounded durable log.
func (q *PublishRetryQueue) Enqueue(event eventbus.TenantEvent) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.pending) >= q.maxSize {
		dropped := q.pending[0]
		q.pending = q.pending[1:]
		log.Printf(
			"CRITICAL: PublishRetryQueue full (size=%d), dropping oldest pending event: tenant_id=%s state=%s attempts=%d",
			q.maxSize, dropped.event.TenantID, dropped.event.State, dropped.attempts,
		)
	}

	q.pending = append(q.pending, &pendingEvent{
		event:         event,
		attempts:      0,
		nextAttemptAt: time.Now(),
	})
}

// PendingCount returns the number of events currently awaiting
// (re)publication. Intended for observability and tests, not for
// controlling queue behavior.
func (q *PublishRetryQueue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Start launches a single background worker goroutine that periodically
// retries publishing pending events, until ctx is canceled or Stop is
// called. Start must be called at most once per PublishRetryQueue.
func (q *PublishRetryQueue) Start(ctx context.Context) {
	go q.run(ctx)
}

// Stop signals the worker to exit and blocks until it has actually
// stopped. Only valid to call after Start.
func (q *PublishRetryQueue) Stop() {
	close(q.stopCh)
	<-q.doneCh
}

func (q *PublishRetryQueue) run(ctx context.Context) {
	defer close(q.doneCh)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopCh:
			return
		case <-ticker.C:
			q.retryDue(ctx)
		}
	}
}

// retryDue publishes every pending event whose nextAttemptAt has elapsed,
// strictly sequentially (never in parallel). Following the same pattern
// as eventbus.MemoryEventBus.Publish: the events to handle are copied
// out while holding mu briefly, the lock is released before the
// potentially slow bus.Publish calls, and mu is re-acquired only to
// apply the results — so a slow or stuck EventBus never blocks Enqueue().
func (q *PublishRetryQueue) retryDue(ctx context.Context) {
	now := time.Now()

	q.mu.Lock()
	var due []*pendingEvent
	for _, pe := range q.pending {
		if !pe.nextAttemptAt.After(now) {
			due = append(due, pe)
		}
	}
	q.mu.Unlock()

	if len(due) == 0 {
		return
	}

	// Publish outside the lock, one at a time, recording each outcome.
	errs := make([]error, len(due))
	for i, pe := range due {
		errs[i] = q.bus.Publish(ctx, pe.event)
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	for i, pe := range due {
		// Defensive: re-locate pe by pointer identity rather than
		// assuming it is still at its previous position. With a single
		// worker goroutine it can never have moved, but this avoids any
		// risk of acting on a stale index if that ever changes.
		idx := q.indexOf(pe)
		if idx == -1 {
			continue
		}

		if errs[i] == nil {
			q.removeAt(idx)
			continue
		}

		if pe.attempts < q.maxRetries {
			pe.attempts++
			pe.nextAttemptAt = time.Now().Add(q.backoff(pe.attempts))
			continue
		}

		log.Printf(
			"CRITICAL: publish retry exhausted, event permanently dropped: tenant_id=%s state=%s attempts=%d error=%v",
			pe.event.TenantID, pe.event.State, pe.attempts, errs[i],
		)
		q.removeAt(idx)
	}
}

// indexOf returns the index of pe within q.pending, compared by pointer
// identity, or -1 if it is no longer present. Callers must hold mu.
func (q *PublishRetryQueue) indexOf(pe *pendingEvent) int {
	for i, p := range q.pending {
		if p == pe {
			return i
		}
	}
	return -1
}

// removeAt removes the element at index i from q.pending, preserving
// order — Enqueue relies on q.pending[0] being the oldest entry when
// deciding what to drop once the queue is full. Callers must hold mu.
func (q *PublishRetryQueue) removeAt(i int) {
	q.pending = append(q.pending[:i], q.pending[i+1:]...)
}

// backoff returns the delay before the attempt-th retry: baseBackoff *
// 2^(attempts-1), i.e. 1s, 2s, 4s, 8s, 16s for the default 1-second base.
func (q *PublishRetryQueue) backoff(attempts int) time.Duration {
	return q.baseBackoff * time.Duration(1<<uint(attempts-1))
}
