package valkey

import (
	"context"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

// NativeTransport adapts the official valkey-go client.
type NativeTransport struct{ client valkeygo.Client }

// NewNativeTransport constructs a transport without taking ownership of the
// client's lifecycle.
func NewNativeTransport(client valkeygo.Client) *NativeTransport {
	return &NativeTransport{client: client}
}

func (transport *NativeTransport) Get(ctx context.Context, key string) ([]byte, bool, error) {
	data, err := transport.client.Do(ctx, transport.client.B().Get().Key(key).Build()).AsBytes()
	if valkeygo.IsValkeyNil(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (transport *NativeTransport) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return transport.client.Do(ctx, transport.client.B().Set().Key(key).
		Value(string(value)).Px(ttl).Build()).Error()
}

func (transport *NativeTransport) Delete(ctx context.Context, key string) error {
	return transport.client.Do(ctx, transport.client.B().Del().Key(key).Build()).Error()
}

func (transport *NativeTransport) Publish(ctx context.Context, channel string, value []byte) error {
	return transport.client.Do(ctx, transport.client.B().Publish().Channel(channel).
		Message(string(value)).Build()).Error()
}

func (transport *NativeTransport) Subscribe(ctx context.Context, channel string) (<-chan []byte, <-chan error) {
	messages := make(chan []byte, 64)
	errorsOut := make(chan error, 1)
	go func() {
		defer close(messages)
		defer close(errorsOut)
		err := transport.client.Receive(ctx,
			transport.client.B().Subscribe().Channel(channel).Build(),
			func(message valkeygo.PubSubMessage) {
				select {
				case messages <- []byte(message.Message):
				default:
				}
			})
		if err != nil && ctx.Err() == nil {
			errorsOut <- err
		}
	}()
	return messages, errorsOut
}
