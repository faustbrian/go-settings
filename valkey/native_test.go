package valkey_test

import (
	"context"
	"errors"
	"testing"
	"time"

	cache "github.com/faustbrian/golib/pkg/settings/valkey"
	valkeygo "github.com/valkey-io/valkey-go"
	valkeymock "github.com/valkey-io/valkey-go/mock"
	"go.uber.org/mock/gomock"
)

func TestNativeTransportExecutesVersionedCacheCommands(t *testing.T) {
	t.Parallel()

	t.Run("get hit", func(t *testing.T) {
		t.Parallel()
		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Do(gomock.Any(), valkeymock.Match("GET", "key")).Return(
			valkeymock.Result(valkeymock.ValkeyBlobString("value")),
		)
		value, ok, err := cache.NewNativeTransport(client).Get(t.Context(), "key")
		if err != nil || !ok || string(value) != "value" {
			t.Fatalf("get hit = (%q, %v, %v)", value, ok, err)
		}
	})

	t.Run("get miss", func(t *testing.T) {
		t.Parallel()
		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Do(gomock.Any(), valkeymock.Match("GET", "key")).Return(
			valkeymock.Result(valkeymock.ValkeyNil()),
		)
		value, ok, err := cache.NewNativeTransport(client).Get(t.Context(), "key")
		if err != nil || ok || value != nil {
			t.Fatalf("get miss = (%q, %v, %v)", value, ok, err)
		}
	})

	t.Run("get failure", func(t *testing.T) {
		t.Parallel()
		backend := errors.New("valkey unavailable")
		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Do(gomock.Any(), valkeymock.Match("GET", "key")).Return(valkeymock.ErrorResult(backend))
		if _, _, err := cache.NewNativeTransport(client).Get(t.Context(), "key"); !errors.Is(err, backend) {
			t.Fatalf("get failure = %v", err)
		}
	})

	t.Run("set if newer", func(t *testing.T) {
		t.Parallel()
		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Do(gomock.Any(), valkeymock.MatchFn(func(command []string) bool {
			return len(command) == 7 && command[0] == "EVAL" && command[2] == "1" &&
				command[3] == "key" && command[4] == "value" && command[5] == "42" && command[6] == "1500"
		})).Return(valkeymock.Result(valkeymock.ValkeyInt64(1)))
		if err := cache.NewNativeTransport(client).SetIfNewer(t.Context(), "key", []byte("value"), 1500*time.Millisecond, 42); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()
		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Do(gomock.Any(), valkeymock.Match("DEL", "key")).Return(
			valkeymock.Result(valkeymock.ValkeyInt64(1)),
		)
		if err := cache.NewNativeTransport(client).Delete(t.Context(), "key"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("publish", func(t *testing.T) {
		t.Parallel()
		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Do(gomock.Any(), valkeymock.Match("PUBLISH", "channel", "value")).Return(
			valkeymock.Result(valkeymock.ValkeyInt64(1)),
		)
		if err := cache.NewNativeTransport(client).Publish(t.Context(), "channel", []byte("value")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestNativeTransportSubscriptionOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("message", func(t *testing.T) {
		t.Parallel()

		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Receive(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ valkeygo.Completed, handler func(valkeygo.PubSubMessage)) error {
				handler(valkeygo.PubSubMessage{Message: "settings changed"})
				return nil
			},
		)

		messages, errorsOut := cache.NewNativeTransport(client).Subscribe(
			context.Background(),
			"settings:test",
		)
		if message := <-messages; string(message) != "settings changed" {
			t.Fatalf("message = %q, want settings changed", message)
		}
		if err, ok := <-errorsOut; ok {
			t.Fatalf("subscription error = %v", err)
		}
	})

	t.Run("backend error", func(t *testing.T) {
		t.Parallel()

		backendError := errors.New("valkey unavailable")
		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Receive(gomock.Any(), gomock.Any(), gomock.Any()).Return(backendError)

		_, errorsOut := cache.NewNativeTransport(client).Subscribe(
			context.Background(),
			"settings:test",
		)
		if err := <-errorsOut; !errors.Is(err, backendError) {
			t.Fatalf("subscription error = %v, want backend error", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client := valkeymock.NewClient(gomock.NewController(t))
		client.EXPECT().Receive(gomock.Any(), gomock.Any(), gomock.Any()).Return(context.Canceled)

		_, errorsOut := cache.NewNativeTransport(client).Subscribe(ctx, "settings:test")
		if err, ok := <-errorsOut; ok {
			t.Fatalf("canceled subscription error = %v", err)
		}
	})
}
