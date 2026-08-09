package valkey

import (
	"context"
	"strconv"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

const setIfNewerScript = `
local current = redis.call('GET', KEYS[1])
if current then
  local version = string.match(current, '"Version":(%d+)')
  if version and (#version > #ARGV[2] or (#version == #ARGV[2] and version >= ARGV[2])) then
    return 0
  end
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[3])
return 1
`

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

func (transport *NativeTransport) SetIfNewer(ctx context.Context, key string, value []byte, ttl time.Duration, version uint64) error {
	command := transport.client.B().Eval().Script(setIfNewerScript).Numkeys(1).Key(key).
		Arg(string(value)).Arg(strconv.FormatUint(version, 10)).
		Arg(strconv.FormatInt(ttl.Milliseconds(), 10)).Build()
	return transport.client.Do(ctx, command).Error()
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
