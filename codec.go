package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

// Codec converts a typed setting value to and from its persisted bytes.
// ID and Version form a stable persistence contract. Custom implementations and
// mutable typed inputs must be safe for the consumer's concurrency. There is no
// context, universal size bound, panic containment or automatic buffer erasure.
// Public setting operations suppress some decode/encode details, but direct
// codec errors (notably standard-library parsers) may include input text.
type Codec[T any] interface {
	// ID returns a stable encoding or cipher identity.
	ID() string
	// Version returns a nonzero persistence contract edition for valid definitions.
	Version() uint32
	// Encode serializes the value; callers must treat returned bytes according to the
	// implementation's ownership contract.
	Encode(T) ([]byte, error)
	// Decode interprets bytes; direct parser errors may contain input text. Mutable outputs and
	// input borrowing are implementation-defined.
	Decode([]byte) (T, error)
}

// Cipher is implemented by caller-owned encryption and key-management code.
// Implementations must produce authenticated ciphertext in production.
type Cipher interface {
	// ID returns a stable encoding or cipher identity.
	ID() string
	// Seal authenticates/encrypts bytes using caller-owned keys; this package does not verify
	// cryptographic strength.
	Seal([]byte) ([]byte, error)
	// Open authenticates before returning plaintext; failure must not expose
	// ciphertext/plaintext or key material.
	Open([]byte) ([]byte, error)
}

// EncryptionCodec composes typed serialization with caller-owned encryption.
// The explicit version must change whenever the cipher or envelope contract
// changes.
type EncryptionCodec[T any] struct {
	inner   Codec[T]
	cipher  Cipher
	version uint32
}

// NewEncryptionCodec borrows inner and cipher without validation or key
// ownership. A valid codec needs nonnil collaborators, nonempty cipher identity
// and nonzero version. It does not verify authenticated-encryption strength or
// erase plaintext; custom collaborators must be concurrency-safe.
func NewEncryptionCodec[T any](inner Codec[T], cipher Cipher, version uint32) EncryptionCodec[T] {
	return EncryptionCodec[T]{inner: inner, cipher: cipher, version: version}
}

// ID returns encrypted:cipher:inner identity, or empty when either collaborator is nil.
func (codec EncryptionCodec[T]) ID() string {
	if codec.cipher == nil || codec.inner == nil {
		return ""
	}
	return "encrypted:" + codec.cipher.ID() + ":" + codec.inner.ID()
}

// Version returns the explicit envelope edition.
func (codec EncryptionCodec[T]) Version() uint32 { return codec.version }

// Encode serializes then seals the value. Collaborator error details are suppressed, but
// plaintext is not erased and collaborator buffers are not copied.
func (codec EncryptionCodec[T]) Encode(value T) ([]byte, error) {
	if codec.inner == nil || codec.cipher == nil || codec.version == 0 || codec.cipher.ID() == "" {
		return nil, fmt.Errorf("settings: invalid encryption codec")
	}
	plain, err := codec.inner.Encode(value)
	if err != nil {
		return nil, fmt.Errorf("settings: encode value before encryption")
	}
	sealed, err := codec.cipher.Seal(plain)
	if err != nil {
		return nil, fmt.Errorf("settings: encrypt value")
	}
	return sealed, nil
}

// Decode opens then decodes the ciphertext, returning zero on failure and suppressing
// collaborator error details. Plaintext is not erased.
func (codec EncryptionCodec[T]) Decode(data []byte) (T, error) {
	var zero T
	if codec.inner == nil || codec.cipher == nil || codec.version == 0 || codec.cipher.ID() == "" {
		return zero, fmt.Errorf("settings: invalid encryption codec")
	}
	plain, err := codec.cipher.Open(data)
	if err != nil {
		return zero, fmt.Errorf("settings: decrypt value")
	}
	value, err := codec.inner.Decode(plain)
	if err != nil {
		return zero, fmt.Errorf("settings: decode decrypted value")
	}
	return value, nil
}

// IntCodec persists signed 64-bit integers as canonical decimal text.
type IntCodec struct{}

// ID returns the stable int64 codec identity.
func (IntCodec) ID() string { return "int64" }

// Version returns codec edition 1.
func (IntCodec) Version() uint32 { return 1 }

// Encode allocates canonical base-10 signed text.
func (IntCodec) Encode(value int64) ([]byte, error) {
	return []byte(strconv.FormatInt(value, 10)), nil
}

// Decode accepts strconv.ParseInt base-10 syntax within int64 range; its direct error may
// contain input text.
func (IntCodec) Decode(data []byte) (int64, error) {
	return strconv.ParseInt(string(data), 10, 64)
}

// StringCodec persists arbitrary string bytes without framing or UTF-8 validation.
type StringCodec struct{}

// ID returns the stable string codec identity.
func (StringCodec) ID() string { return "string" }

// Version returns codec edition 1.
func (StringCodec) Version() uint32 { return 1 }

// Encode copies string bytes without UTF-8 validation or a codec-local size cap.
func (StringCodec) Encode(value string) ([]byte, error) {
	return []byte(value), nil
}

// Decode copies bytes into a string without UTF-8 validation or a codec-local size cap.
func (StringCodec) Decode(data []byte) (string, error) { return string(data), nil }

// BoolCodec persists booleans as the canonical strings true and false.
type BoolCodec struct{}

// ID returns the stable bool codec identity.
func (BoolCodec) ID() string { return "bool" }

// Version returns codec edition 1.
func (BoolCodec) Version() uint32 { return 1 }

// Encode allocates true or false text.
func (BoolCodec) Encode(value bool) ([]byte, error) {
	return []byte(strconv.FormatBool(value)), nil
}

// Decode accepts only exact lowercase true or false, returning false on error.
func (BoolCodec) Decode(data []byte) (bool, error) {
	switch string(data) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("settings: invalid canonical boolean")
	}
}

// Decimal is an exact base-10 value. Its scale is preserved.
type Decimal string

var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// DecimalCodec persists exact non-exponent decimal strings.
type DecimalCodec struct{}

// ID returns the stable decimal codec identity.
func (DecimalCodec) ID() string { return "decimal" }

// Version returns codec edition 1.
func (DecimalCodec) Version() uint32 { return 1 }

// Encode validates non-exponent decimal syntax, preserving scale and optional minus without
// normalization.
func (DecimalCodec) Encode(value Decimal) ([]byte, error) {
	if !decimalPattern.MatchString(string(value)) {
		return nil, fmt.Errorf("settings: invalid decimal")
	}
	return []byte(value), nil
}

// Decode returns the text and a syntax error when invalid; inspect the error before using the
// returned value.
func (codec DecimalCodec) Decode(data []byte) (Decimal, error) {
	value := Decimal(data)
	_, err := codec.Encode(value)
	return value, err
}

// DurationCodec persists durations as canonical Go duration strings.
type DurationCodec struct{}

// ID returns the stable duration codec identity.
func (DurationCodec) ID() string { return "duration" }

// Version returns codec edition 1.
func (DurationCodec) Version() uint32 { return 1 }

// Encode allocates Go duration text.
func (DurationCodec) Encode(value time.Duration) ([]byte, error) {
	return []byte(value.String()), nil
}

// Decode accepts time.ParseDuration syntax; direct parse errors may include input text.
func (DurationCodec) Decode(data []byte) (time.Duration, error) {
	return time.ParseDuration(string(data))
}

// TimeCodec persists instants as UTC RFC 3339 values with nanoseconds.
type TimeCodec struct{}

// ID returns the stable time codec identity.
func (TimeCodec) ID() string { return "time" }

// Version returns codec edition 1.
func (TimeCodec) Version() uint32 { return 1 }

// Encode normalizes to UTC RFC3339Nano text, dropping monotonic-clock metadata.
func (TimeCodec) Encode(value time.Time) ([]byte, error) {
	return []byte(value.UTC().Format(time.RFC3339Nano)), nil
}

// Decode parses RFC3339Nano and normalizes to UTC; direct parse errors may include input text.
func (TimeCodec) Decode(data []byte) (time.Time, error) {
	value, err := time.Parse(time.RFC3339Nano, string(data))
	return value.UTC(), err
}

// EnumCodec persists a caller-defined closed string set.
type EnumCodec[T ~string] struct {
	id      string
	allowed map[T]struct{}
}

// NewEnumCodec constructs a named enum codec with an explicit allowed set.
func NewEnumCodec[T ~string](id string, values ...T) EnumCodec[T] {
	allowed := make(map[T]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}
	return EnumCodec[T]{id: id, allowed: allowed}
}

// ID returns enum: plus the caller-supplied identity.
func (codec EnumCodec[T]) ID() string { return "enum:" + codec.id }

// Version returns codec edition 1.
func (EnumCodec[T]) Version() uint32 { return 1 }

// Encode requires exact membership in the constructed set and copies text bytes.
func (codec EnumCodec[T]) Encode(value T) ([]byte, error) {
	if _, ok := codec.allowed[value]; !ok {
		return nil, fmt.Errorf("settings: invalid enum value")
	}
	return []byte(value), nil
}

// Decode returns the input text and membership error when invalid; inspect the error before
// using the value.
func (codec EnumCodec[T]) Decode(data []byte) (T, error) {
	value := T(data)
	_, err := codec.Encode(value)
	return value, err
}

// StringListCodec persists string lists as JSON arrays with a bounded size.
type StringListCodec struct{}

// ID returns the stable string-list codec identity.
func (StringListCodec) ID() string { return "string-list" }

// Version returns codec edition 1.
func (StringListCodec) Version() uint32 { return 1 }

// Encode uses encoding/json for at most 10000 strings; nil encodes as null and encoded byte
// size is not capped here.
func (StringListCodec) Encode(value []string) ([]byte, error) {
	if len(value) > 10_000 {
		return nil, fmt.Errorf("settings: string list exceeds 10000 items")
	}
	return json.Marshal(value)
}

// Decode accepts one JSON value of at most 1 MiB and at most 10000 strings; malformed/trailing
// input returns nil and an error.
func (codec StringListCodec) Decode(data []byte) ([]string, error) {
	var value []string
	if err := decodeJSON(data, &value); err != nil {
		return nil, err
	}
	if _, err := codec.Encode(value); err != nil {
		return nil, err
	}
	return value, nil
}

// JSONCodec delegates structured serialization to encoding/json, including custom
// marshalers. It is not a protocol canonicalization or authentication scheme.
type JSONCodec[T any] struct{}

// ID returns json; applications must distinguish incompatible T contracts through
// definition/version governance.
func (JSONCodec[T]) ID() string { return "json" }

// Version returns codec edition 1.
func (JSONCodec[T]) Version() uint32 { return 1 }

// Encode delegates to encoding/json, including caller marshalers, without a codec-local
// output-size cap.
func (JSONCodec[T]) Encode(value T) ([]byte, error) { return json.Marshal(value) }

// Decode accepts one JSON value of at most 1 MiB, allows unknown object fields and delegates
// custom unmarshalers. A decode error may leave a partial T.
func (JSONCodec[T]) Decode(data []byte) (T, error) {
	var value T
	err := decodeJSON(data, &value)
	return value, err
}

func decodeJSON(data []byte, target any) error {
	if len(data) > 1<<20 {
		return fmt.Errorf("settings: JSON value exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("settings: trailing JSON data")
	}
	return nil
}
