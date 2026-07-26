package settings

import (
	"fmt"
	"strings"
)

// Definition is the type-erased metadata contract used by registries and
// providers. Consumers retain type safety through Key[T].
type Definition interface {
	StableID() string
	Namespace() string
	Name() string
	CodecID() string
	CodecVersion() uint32
	Documentation() string
	DisplayName() string
	Sensitive() bool
	DefaultEncoded() ([]byte, bool, error)
	ValidateEncoded([]byte) error
	ValidateDefinition() error
}

// Key is a typed setting definition with a stable persistence identifier.
type Key[T any] struct {
	namespace     string
	name          string
	codec         Codec[T]
	documentation string
	displayName   string
	sensitive     bool
	defaultValue  T
	hasDefault    bool
	validate      func(T) error
}

// KeyOption customizes a typed key definition.
type KeyOption[T any] func(*Key[T])

// NewKey defines a key. Namespace and name together form its stable ID.
func NewKey[T any](namespace, name string, codec Codec[T], options ...KeyOption[T]) Key[T] {
	key := Key[T]{namespace: namespace, name: name, codec: codec}
	for _, option := range options {
		option(&key)
	}

	return key
}

// WithDocumentation records operator-facing documentation.
func WithDocumentation[T any](documentation string) KeyOption[T] {
	return func(key *Key[T]) { key.documentation = documentation }
}

// WithDisplayName records a changeable human-facing label. It never affects
// the stable persistence identifier.
func WithDisplayName[T any](displayName string) KeyOption[T] {
	return func(key *Key[T]) { key.displayName = displayName }
}

// WithDefault declares a typed fallback value.
func WithDefault[T any](value T) KeyOption[T] {
	return func(key *Key[T]) {
		key.defaultValue = value
		key.hasDefault = true
	}
}

// WithValidation declares validation applied to defaults and writes.
func WithValidation[T any](validate func(T) error) KeyOption[T] {
	return func(key *Key[T]) { key.validate = validate }
}

// WithSensitive marks persisted and audit values as secret-bearing.
func WithSensitive[T any]() KeyOption[T] {
	return func(key *Key[T]) { key.sensitive = true }
}

func (key Key[T]) StableID() string  { return key.namespace + "/" + key.name }
func (key Key[T]) Namespace() string { return key.namespace }
func (key Key[T]) Name() string      { return key.name }
func (key Key[T]) CodecID() string {
	if key.codec == nil {
		return ""
	}
	return key.codec.ID()
}
func (key Key[T]) CodecVersion() uint32 {
	if key.codec == nil {
		return 0
	}
	return key.codec.Version()
}
func (key Key[T]) Documentation() string { return key.documentation }
func (key Key[T]) DisplayName() string   { return key.displayName }
func (key Key[T]) Sensitive() bool       { return key.sensitive }

// ValidateDefinition verifies stable identity, codec metadata, validation,
// and defaults before a definition enters a registry or write operation.
func (key Key[T]) ValidateDefinition() error {
	if key.namespace == "" || key.name == "" || len(key.namespace) > 128 ||
		len(key.name) > 383 || strings.ContainsAny(key.namespace+key.name, "\x00\r\n") ||
		strings.ContainsRune(key.namespace, '/') || key.codec == nil || key.CodecID() == "" ||
		key.CodecVersion() == 0 {
		return fmt.Errorf("%w: key metadata", ErrInvalidDefinition)
	}
	if key.hasDefault {
		if err := key.validateValue(key.defaultValue); err != nil {
			return fmt.Errorf("%w: default", ErrInvalidDefinition)
		}
		if _, err := key.codec.Encode(key.defaultValue); err != nil {
			return fmt.Errorf("%w: default encoding", ErrInvalidDefinition)
		}
	}
	return nil
}

func (key Key[T]) DefaultEncoded() ([]byte, bool, error) {
	if !key.hasDefault {
		return nil, false, nil
	}
	if err := key.validateValue(key.defaultValue); err != nil {
		return nil, true, err
	}
	data, err := key.codec.Encode(key.defaultValue)
	return data, true, err
}

func (key Key[T]) ValidateEncoded(data []byte) error {
	if err := key.ValidateDefinition(); err != nil {
		return err
	}
	value, err := key.codec.Decode(data)
	if err != nil {
		return fmt.Errorf("%w: decode %s", ErrInvalidValue, key.StableID())
	}
	return key.validateValue(value)
}

func (key Key[T]) String() string { return key.StableID() }

func (key Key[T]) validateValue(value T) error {
	if key.validate == nil {
		return nil
	}
	if err := key.validate(value); err != nil {
		return fmt.Errorf("settings: validate %s: %w", key.StableID(), err)
	}

	return nil
}
