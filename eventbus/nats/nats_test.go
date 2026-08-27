package nats

import (
	"context"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"

	natsio "github.com/nats-io/nats.go"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEmbeddedNATS starts an in-process NATS server (the NATS equivalent
// of miniredis: a real server implementation, not a mock, but running
// in-memory with no separate process and no real network — see
// nats-io/nats-server/v2/server), and returns a *nats.Conn already
// connected to it via NATS's in-process transport (nats.InProcessServer),
// which never touches a TCP socket at all. t.Cleanup tears down both the
// connection and the server, so callers don't need their own defer.
func startEmbeddedNATS(t *testing.T) *natsio.Conn {
	t.Helper()

	opts := &natsserver.Options{
		Host:       "127.0.0.1",
		DontListen: true, // no TCP socket — pure in-process transport
	}
	srv, err := natsserver.NewServer(opts)
	require.NoError(t, err)

	go srv.Start()
	if !srv.ReadyForConnections(4 * time.Second) {
		t.Fatal("embedded NATS server did not become ready in time")
	}
	t.Cleanup(srv.Shutdown)

	conn, err := natsio.Connect("", natsio.InProcessServer(srv))
	require.NoError(t, err)
	t.Cleanup(conn.Close)

	return conn
}

func TestNATSEventBus_PublishAndSubscribe(t *testing.T) {
	conn := startEmbeddedNATS(t)
	bus := New(conn, "tenant-events")

	events := make(chan eventbus.TenantEvent, 1)
	err := bus.Subscribe(func(event eventbus.TenantEvent) {
		events <- event
	})
	require.NoError(t, err)

	sent := eventbus.TenantEvent{
		TenantID:  "tenant-A",
		State:     tenant.Banned,
		Timestamp: time.Now(),
	}
	err = bus.Publish(context.Background(), sent)
	require.NoError(t, err)

	select {
	case received := <-events:
		assert.Equal(t, sent.TenantID, received.TenantID)
		assert.Equal(t, sent.State, received.State)
		assert.WithinDuration(t, sent.Timestamp, received.Timestamp, time.Second)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: expected event was not received")
	}
}

func TestNATSEventBus_SubscribeFailsWhenNATSUnavailable(t *testing.T) {
	// A connection that was already closed can no longer accept a new
	// subscription — Subscribe() must surface that immediately
	// (fail-fast), not silently.
	conn := startEmbeddedNATS(t)
	conn.Close()

	bus := New(conn, "tenant-events")
	err := bus.Subscribe(func(event eventbus.TenantEvent) {})

	assert.Error(t, err)
}

func TestNATSEventBus_PanickingHandlerDoesNotAffectOthers(t *testing.T) {
	conn := startEmbeddedNATS(t)
	bus := New(conn, "tenant-events")

	gotB := make(chan eventbus.TenantEvent, 1)

	err := bus.Subscribe(func(event eventbus.TenantEvent) {
		panic("handler A deliberately panics")
	})
	require.NoError(t, err)

	err = bus.Subscribe(func(event eventbus.TenantEvent) {
		gotB <- event
	})
	require.NoError(t, err)

	sent := eventbus.TenantEvent{TenantID: "tenant-A", State: tenant.Banned, Timestamp: time.Now()}
	err = bus.Publish(context.Background(), sent)
	require.NoError(t, err)

	select {
	case received := <-gotB:
		assert.Equal(t, sent.TenantID, received.TenantID)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: handler B never received its event — a panic in handler A must not affect it")
	}
}

func TestNATSEventBus_Stop_StopsDeliveringMessages(t *testing.T) {
	conn := startEmbeddedNATS(t)
	bus := New(conn, "tenant-events")

	events := make(chan eventbus.TenantEvent, 2)
	err := bus.Subscribe(func(event eventbus.TenantEvent) {
		events <- event
	})
	require.NoError(t, err)

	// Sanity check: delivery works before Stop.
	err = bus.Publish(context.Background(), eventbus.TenantEvent{
		TenantID: "tenant-A", State: tenant.Banned, Timestamp: time.Now(),
	})
	require.NoError(t, err)

	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: expected event was not received before Stop")
	}

	bus.Stop()

	// After Stop, a new publish must not reach the now-closed subscription.
	err = bus.Publish(context.Background(), eventbus.TenantEvent{
		TenantID: "tenant-B", State: tenant.Banned, Timestamp: time.Now(),
	})
	require.NoError(t, err)

	select {
	case event := <-events:
		t.Fatalf("unexpected event received after Stop: tenant=%s", event.TenantID)
	case <-time.After(200 * time.Millisecond):
		// No event received: expected, the subscription was closed by Stop.
	}
}

func TestNATSEventBus_Stop_IsIdempotent(t *testing.T) {
	conn := startEmbeddedNATS(t)
	bus := New(conn, "tenant-events")

	err := bus.Subscribe(func(event eventbus.TenantEvent) {})
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		bus.Stop()
		bus.Stop()
	})
}

func TestNATSEventBus_Subscribe_FailsAfterStop(t *testing.T) {
	conn := startEmbeddedNATS(t)
	bus := New(conn, "tenant-events")

	bus.Stop()

	err := bus.Subscribe(func(event eventbus.TenantEvent) {})

	assert.ErrorIs(t, err, ErrStopped)
}

func TestNATSEventBus_Stop_SafeWithoutSubscribe(t *testing.T) {
	conn := startEmbeddedNATS(t)
	bus := New(conn, "tenant-events")

	assert.NotPanics(t, func() {
		bus.Stop()
	})
}
