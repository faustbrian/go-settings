package valkey_test

import (
	"context"
	"errors"
	"testing"

	cache "github.com/faustbrian/golib/pkg/settings/valkey"
	valkeygo "github.com/valkey-io/valkey-go"
	valkeymock "github.com/valkey-io/valkey-go/mock"
	"go.uber.org/mock/gomock"
)

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
