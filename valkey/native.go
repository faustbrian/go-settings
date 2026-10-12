package valkey

import (
	"context"
	"strconv"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"
)

// Decimal length/lexical comparison preserves uint64 ordering without Lua's
// floating-point conversion. Equal versions must not replace or renew a record;
// TestNativeTransportExecutesVersionedCacheCommands guards the command contract.
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

// NativeTransport adapts a borrowed official valkey-go client, whose concurrency
// and connection lifecycle remain caller-owned. Zero transport cannot perform IO;
// subscription contexts own spawned goroutines and no values are redacted here.
type NativeTransport struct{ client valkeygo.Client }

// NewNativeTransport constructs a transport without taking ownership of the
// client's lifecycle.
func NewNativeTransport(client valkeygo.Client) *NativeTransport {
	return &NativeTransport{client: client}
}

// Get reads through the borrowed client, translating Valkey nil to absent.
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

// SetIfNewer uses an atomic Lua decimal-version comparison and millisecond TTL; equal/older
// versions do not replace entries. Caller must supply suitable TTL and trusted record
// encoding.
func (transport *NativeTransport) SetIfNewer(ctx context.Context, key string, value []byte, ttl time.Duration, version uint64) error {
	command := transport.client.B().Eval().Script(setIfNewerScript).Numkeys(1).Key(key).
		Arg(string(value)).Arg(strconv.FormatUint(version, 10)).
		Arg(strconv.FormatInt(ttl.Milliseconds(), 10)).Build()
	return transport.client.Do(ctx, command).Error()
}

// Delete removes one key using the borrowed client.
func (transport *NativeTransport) Delete(ctx context.Context, key string) error {
	return transport.client.Do(ctx, transport.client.B().Del().Key(key).Build()).Error()
}

// Publish sends a pub/sub message without durable acknowledgement.
func (transport *NativeTransport) Publish(ctx context.Context, channel string, value []byte) error {
	return transport.client.Do(ctx, transport.client.B().Publish().Channel(channel).
		Message(string(value)).Build()).Error()
}

// Subscribe starts one context-owned Receive goroutine with 64-message buffering, dropping new
// messages when full. Both channels close when Receive returns; cancel the context to stop it.
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
