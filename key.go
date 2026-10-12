package settings

import (
	"fmt"
	"strings"
)

// Definition is the type-erased metadata contract used by registries and
// providers. Consumers retain type safety through Key[T].
type Definition interface {
	// StableID returns stable namespace/name persistence identity.
	StableID() string
	// Namespace returns the definition namespace.
	Namespace() string
	// Name returns the definition name.
	Name() string
	// CodecID returns the stable encoding identity.
	CodecID() string
	// CodecVersion returns the encoding edition.
	CodecVersion() uint32
	// Documentation returns descriptive caller-facing text.
	Documentation() string
	// DisplayName returns a mutable human label unrelated to persistence identity.
	DisplayName() string
	// Sensitive reports whether new audit/export values must be redacted.
	Sensitive() bool
	// DefaultEncoded returns encoded default bytes and whether a default exists, including when
	// encoding fails.
	DefaultEncoded() ([]byte, bool, error)
	// ValidateEncoded checks bytes against this definition without rendering their value in errors.
	ValidateEncoded([]byte) error
	// ValidateDefinition checks stable identity, codec and default contracts; implementations
	// must remain stable after registration.
	ValidateDefinition() error
}

// Key is a typed setting definition with a stable persistence identifier.
// Its codec, validation callback and reference-bearing default are borrowed.
// Treat a constructed Key and those collaborators as immutable; concurrent use
// is safe only if the borrowed code and values are safe. Zero Key is invalid.
type Key[T any] struct {
	namespace     string
	name          string
	codec         Codec[T]
	documentation string
	displayName   string
	sensitive     bool
	class         SettingClass
	defaultValue  T
	hasDefault    bool
	validate      func(T) error
}

// SettingClass selects freshness and failure policy for a definition.
type SettingClass uint8

const (
	// ClassStandard selects the standard explicitly configured runtime policy.
	ClassStandard SettingClass = iota + 1
	// ClassSecret classifies secret-bearing definitions and marks them sensitive.
	ClassSecret
	// ClassSecuritySensitive classifies security-policy definitions and marks them sensitive;
	// safe fallback is application-owned.
	ClassSecuritySensitive
)

// KeyOption customizes a typed key definition.
type KeyOption[T any] func(*Key[T])

// NewKey defines a key without validating it. Namespace and name form its stable
// ID without normalization. Options run synchronously in order; nil options
// panic and custom options are not panic-contained. ValidateDefinition or
// Register must admit the definition before use.
func NewKey[T any](namespace, name string, codec Codec[T], options ...KeyOption[T]) Key[T] {
	key := Key[T]{namespace: namespace, name: name, codec: codec, class: ClassStandard}
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

// WithDefault borrows a typed fallback value, including zero. Direct Resolve
// returns it without deep copying or revalidation; do not mutate referenced
// maps/slices after constructing the key. Runtime captures its encoded default.
func WithDefault[T any](value T) KeyOption[T] {
	return func(key *Key[T]) {
		key.defaultValue = value
		key.hasDefault = true
	}
}

// WithValidation declares synchronous validation of defaults, decoded stored
// values and writes. Nil disables validation. The callback may run concurrently
// and is not panic-contained; errors may be wrapped and must not expose secrets.
func WithValidation[T any](validate func(T) error) KeyOption[T] {
	return func(key *Key[T]) { key.validate = validate }
}

// WithSensitive selects ClassSecret and audit/export redaction, not encryption.
func WithSensitive[T any]() KeyOption[T] {
	return func(key *Key[T]) {
		key.sensitive = true
		key.class = ClassSecret
	}
}

// WithClass assigns a definition to an explicit fleet failure-policy class.
// Selecting a secret/security-sensitive class sets sensitivity. Selecting
// ClassStandard later does not clear a sensitivity flag set by earlier options.
func WithClass[T any](class SettingClass) KeyOption[T] {
	return func(key *Key[T]) {
		key.class = class
		if class == ClassSecret || class == ClassSecuritySensitive {
			key.sensitive = true
		}
	}
}

// StableID returns namespace/name without normalization.
func (key Key[T]) StableID() string { return key.namespace + "/" + key.name }

// Namespace returns the namespace supplied to NewKey.
func (key Key[T]) Namespace() string { return key.namespace }

// Name returns the name supplied to NewKey.
func (key Key[T]) Name() string { return key.name }

// CodecID returns the codec identity, or empty for a nil codec.
func (key Key[T]) CodecID() string {
	if key.codec == nil {
		return ""
	}
	return key.codec.ID()
}

// CodecVersion returns the codec edition, or zero for a nil codec.
func (key Key[T]) CodecVersion() uint32 {
	if key.codec == nil {
		return 0
	}
	return key.codec.Version()
}

// Documentation returns descriptive text supplied by WithDocumentation.
func (key Key[T]) Documentation() string { return key.documentation }

// DisplayName returns the human label supplied by WithDisplayName.
func (key Key[T]) DisplayName() string { return key.displayName }

// Sensitive reports the effective audit/export redaction flag.
func (key Key[T]) Sensitive() bool { return key.sensitive }

// Class returns the explicit runtime-policy class.
func (key Key[T]) Class() SettingClass { return key.class }

// ClassOf returns a definition's fleet policy class. Definitions compiled
// against the older metadata contract remain standard during mixed rollouts.
func ClassOf(definition Definition) SettingClass {
	classified, ok := definition.(interface{ Class() SettingClass })
	if !ok {
		return ClassStandard
	}
	return classified.Class()
}

// ValidateDefinition verifies stable identity, codec metadata, validation,
// and defaults before a definition enters a registry or write operation.
func (key Key[T]) ValidateDefinition() error {
	if key.namespace == "" || key.name == "" || len(key.namespace) > 128 ||
		len(key.name) > 383 || strings.ContainsAny(key.namespace+key.name, "\x00\r\n") ||
		strings.ContainsRune(key.namespace, '/') || key.codec == nil || key.CodecID() == "" ||
		key.CodecVersion() == 0 {
		return fmt.Errorf("%w: key metadata", ErrInvalidDefinition)
	}
	if key.class != ClassStandard && key.class != ClassSecret && key.class != ClassSecuritySensitive {
		return fmt.Errorf("%w: setting class", ErrInvalidDefinition)
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

// DefaultEncoded validates and encodes the borrowed default; false means no default and nil
// error. A zero Key has no default.
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

// ValidateEncoded validates the definition, decodes bytes and invokes custom validation;
// decode errors omit raw value bytes.
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

// String returns StableID without rendering a value.
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
