// Package nats provides a NATS-backed implementation of eventbus.EventBus.
package nats

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"

	"github.com/sylvinhio676-ux/tenant-core/eventbus"

	natsio "github.com/nats-io/nats.go"
)

var _ eventbus.EventBus = (*NATSEventBus)(nil)

// ErrStopped is returned by Subscribe once Stop has been called. A
// stopped NATSEventBus cannot be reused.
var ErrStopped = errors.New("eventbus/nats: bus is stopped")

// NATSEventBus is an implementation of eventbus.EventBus that propagates
// events via NATS core publish/subscribe, allowing propagation across
// multiple server instances — unlike eventbus.MemoryEventBus, which only
// works in a single instance.
//
// Network resilience: reconnection and resubscription on transient network
// failures are handled natively by nats.go's *nats.Conn — not by this
// type. Verified directly against the client's source (nats.go v1.53.1):
// GetDefaultOptions() sets AllowReconnect: true, DefaultMaxReconnect (60
// attempts), DefaultReconnectWait (2s between attempts, plus jitter to
// avoid a thundering herd), and DefaultPingInterval (2 minutes) for a
// periodic health-check ping that detects silent disconnects — the same
// role go-redis's own health-check ping plays for eventbus/redis. On
// reconnect, doReconnect calls resendSubscriptions, which re-issues the
// NATS SUB protocol line for every subscription this connection created,
// so a *nats.Subscription's callback keeps receiving messages after a
// reconnect with no action required from this package. In other words:
// don't reimplement backoff/reconnect/resubscribe logic on top of this
// type, it would duplicate what nats.go already does — the caller's
// *nats.Conn should simply be created with nats.Connect (which applies
// these defaults unless overridden).
type NATSEventBus struct {
	conn    *natsio.Conn
	subject string

	mu   sync.Mutex
	subs []*natsio.Subscription
	// stopped is set once Stop() has run; guarded by mu, same purpose and
	// same race being closed as eventbus/redis's stopped flag — see
	// Subscribe.
	stopped bool
}

// New creates a NATSEventBus using the given connection and NATS subject.
// The connection's lifecycle (including reconnection policy) is the
// caller's responsibility; see the type-level doc comment.
func New(conn *natsio.Conn, subject string) *NATSEventBus {
	return &NATSEventBus{conn: conn, subject: subject}
}

func (b *NATSEventBus) Publish(_ context.Context, event eventbus.TenantEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return b.conn.Publish(b.subject, data)
}

// Subscribe registers handler to be called for every event published on
// this bus's subject. It fails fast: nats.Conn.Subscribe returns an error
// immediately if the connection is closed or otherwise unable to accept a
// new subscription, rather than failing silently later — the same
// fail-fast guarantee eventbus/redis's Subscribe provides via
// pubsub.Receive.
func (b *NATSEventBus) Subscribe(handler func(eventbus.TenantEvent)) error {
	sub, err := b.conn.Subscribe(b.subject, func(msg *natsio.Msg) {
		var event eventbus.TenantEvent
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			log.Printf("eventbus/nats: failed to unmarshal event: %v", err)
			return
		}

		go safeCall(handler, event)
	})
	if err != nil {
		return err
	}

	// Register the subscription only if Stop hasn't already run — same
	// race as eventbus/redis's Subscribe: Stop() could run — closing every
	// subscription it currently knows about — while this call is still in
	// flight against a slow/degraded NATS server. Without this check, a
	// subscription registered after Stop() already ran would never be
	// closed and would keep delivering messages forever.
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		_ = sub.Unsubscribe()
		return ErrStopped
	}
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	return nil
}

// Stop unsubscribes every subscription created via Subscribe, and marks
// the bus as stopped: any Subscribe call still in flight will notice this
// once it completes and unsubscribe immediately instead of registering —
// see the check in Subscribe. Once stopped, a NATSEventBus cannot be
// reused; Subscribe will return ErrStopped.
//
// Stop is safe to call multiple times, and safe to call even if Subscribe
// was never called. Stop does not close the underlying *nats.Conn — the
// connection was supplied by the caller via New and remains theirs to
// manage (e.g. it may be shared with other consumers).
//
// This is unrelated to network resilience — see the type-level doc
// comment: nats.go already reconnects and resubscribes on its own after a
// transient failure. Stop is purely for an intentional, clean shutdown of
// this NATSEventBus (e.g. when the application terminates).
func (b *NATSEventBus) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopped = true
	for _, sub := range b.subs {
		if err := sub.Unsubscribe(); err != nil {
			log.Printf("eventbus/nats: error unsubscribing: %v", err)
		}
	}
	b.subs = nil
}

// safeCall runs a handler while recovering from any panic, so that a
// failing handler never affects other subscribers nor the process as a
// whole — same principle as MemoryEventBus and eventbus/redis.
func safeCall(handler func(eventbus.TenantEvent), event eventbus.TenantEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("eventbus/nats: handler panicked: %v", r)
		}
	}()
	handler(event)
}
