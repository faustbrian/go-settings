package valkey_test

import (
	"context"
	"os"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
	cache "github.com/faustbrian/golib/pkg/settings/valkey"
	valkeygo "github.com/valkey-io/valkey-go"
)

func TestNativeTransportIntegration(t *testing.T) {
	address := os.Getenv("VALKEY_ADDR")
	if address == "" {
		t.Skip("VALKEY_ADDR is not set")
	}
	client, err := valkeygo.NewClient(valkeygo.ClientOption{InitAddress: []string{address}})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(client.Close)
	provider := cache.New(memory.New(), cache.NewNativeTransport(client), cache.Config{
		Prefix: "settings:integration", TTL: time.Minute,
	})
	key := settings.NewKey("integration", "value", settings.StringCodec{})
	scope := settings.Tenant(t.Name())
	record, err := settings.Set(context.Background(), provider, scope, key, "value", settings.Change{
		Actor: "integration", Reason: "test",
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	cached, ok, err := provider.Get(context.Background(), scope, key.StableID())
	if err != nil || !ok || cached.Version != record.Version {
		t.Fatalf("get = %#v, %v, %v", cached, ok, err)
	}
	transport := cache.NewNativeTransport(client)
	subscriptionContext, cancelSubscription := context.WithCancel(context.Background())
	defer cancelSubscription()
	messages, subscriptionErrors := transport.Subscribe(subscriptionContext, "settings:integration:test")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(time.Second)
	for {
		select {
		case message := <-messages:
			if string(message) != "message" {
				t.Fatalf("message = %q", message)
			}
			goto received
		case err := <-subscriptionErrors:
			t.Fatalf("subscription: %v", err)
		case <-ticker.C:
			if err := transport.Publish(context.Background(), "settings:integration:test", []byte("message")); err != nil {
				t.Fatalf("publish: %v", err)
			}
		case <-deadline:
			t.Fatal("subscription did not receive")
		}
	}

received:
	if err := transport.Delete(context.Background(), "missing"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, err := transport.Get(context.Background(), "missing"); err != nil || ok {
		t.Fatalf("missing get = %v, %v", ok, err)
	}
	cancelSubscription()
	errorContext, cancelErrorSubscription := context.WithCancel(context.Background())
	defer cancelErrorSubscription()
	_, nativeErrors := transport.Subscribe(errorContext, "settings:integration:error")
	client.Close()
	select {
	case err := <-nativeErrors:
		if err == nil {
			t.Fatal("native subscription returned nil error after close")
		}
	case <-time.After(time.Second):
		t.Fatal("native subscription did not report close")
	}
	if _, _, err := transport.Get(context.Background(), "closed"); err == nil {
		t.Fatal("closed native client get succeeded")
	}
}
