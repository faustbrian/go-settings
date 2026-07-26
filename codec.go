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
// ID and Version form a stable persistence contract.
type Codec[T any] interface {
	ID() string
	Version() uint32
	Encode(T) ([]byte, error)
	Decode([]byte) (T, error)
}

// Cipher is implemented by caller-owned encryption and key-management code.
// Implementations must produce authenticated ciphertext in production.
type Cipher interface {
	ID() string
	Seal([]byte) ([]byte, error)
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

// NewEncryptionCodec constructs an encrypted codec without owning keys.
func NewEncryptionCodec[T any](inner Codec[T], cipher Cipher, version uint32) EncryptionCodec[T] {
	return EncryptionCodec[T]{inner: inner, cipher: cipher, version: version}
}

func (codec EncryptionCodec[T]) ID() string {
	if codec.cipher == nil || codec.inner == nil {
		return ""
	}
	return "encrypted:" + codec.cipher.ID() + ":" + codec.inner.ID()
}
func (codec EncryptionCodec[T]) Version() uint32 { return codec.version }
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

func (IntCodec) ID() string      { return "int64" }
func (IntCodec) Version() uint32 { return 1 }
func (IntCodec) Encode(value int64) ([]byte, error) {
	return []byte(strconv.FormatInt(value, 10)), nil
}
func (IntCodec) Decode(data []byte) (int64, error) {
	return strconv.ParseInt(string(data), 10, 64)
}

// StringCodec persists UTF-8 strings without additional framing.
type StringCodec struct{}

func (StringCodec) ID() string      { return "string" }
func (StringCodec) Version() uint32 { return 1 }
func (StringCodec) Encode(value string) ([]byte, error) {
	return []byte(value), nil
}
func (StringCodec) Decode(data []byte) (string, error) { return string(data), nil }

// BoolCodec persists booleans as the canonical strings true and false.
type BoolCodec struct{}

func (BoolCodec) ID() string      { return "bool" }
func (BoolCodec) Version() uint32 { return 1 }
func (BoolCodec) Encode(value bool) ([]byte, error) {
	return []byte(strconv.FormatBool(value)), nil
}
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

func (DecimalCodec) ID() string      { return "decimal" }
func (DecimalCodec) Version() uint32 { return 1 }
func (DecimalCodec) Encode(value Decimal) ([]byte, error) {
	if !decimalPattern.MatchString(string(value)) {
		return nil, fmt.Errorf("settings: invalid decimal")
	}
	return []byte(value), nil
}
func (codec DecimalCodec) Decode(data []byte) (Decimal, error) {
	value := Decimal(data)
	_, err := codec.Encode(value)
	return value, err
}

// DurationCodec persists durations as canonical Go duration strings.
type DurationCodec struct{}

func (DurationCodec) ID() string      { return "duration" }
func (DurationCodec) Version() uint32 { return 1 }
func (DurationCodec) Encode(value time.Duration) ([]byte, error) {
	return []byte(value.String()), nil
}
func (DurationCodec) Decode(data []byte) (time.Duration, error) {
	return time.ParseDuration(string(data))
}

// TimeCodec persists instants as UTC RFC 3339 values with nanoseconds.
type TimeCodec struct{}

func (TimeCodec) ID() string      { return "time" }
func (TimeCodec) Version() uint32 { return 1 }
func (TimeCodec) Encode(value time.Time) ([]byte, error) {
	return []byte(value.UTC().Format(time.RFC3339Nano)), nil
}
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

func (codec EnumCodec[T]) ID() string { return "enum:" + codec.id }
func (EnumCodec[T]) Version() uint32  { return 1 }
func (codec EnumCodec[T]) Encode(value T) ([]byte, error) {
	if _, ok := codec.allowed[value]; !ok {
		return nil, fmt.Errorf("settings: invalid enum value")
	}
	return []byte(value), nil
}
func (codec EnumCodec[T]) Decode(data []byte) (T, error) {
	value := T(data)
	_, err := codec.Encode(value)
	return value, err
}

// StringListCodec persists string lists as JSON arrays with a bounded size.
type StringListCodec struct{}

func (StringListCodec) ID() string      { return "string-list" }
func (StringListCodec) Version() uint32 { return 1 }
func (StringListCodec) Encode(value []string) ([]byte, error) {
	if len(value) > 10_000 {
		return nil, fmt.Errorf("settings: string list exceeds 10000 items")
	}
	return json.Marshal(value)
}
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

// JSONCodec persists caller-defined structured values as deterministic JSON.
type JSONCodec[T any] struct{}

func (JSONCodec[T]) ID() string                     { return "json" }
func (JSONCodec[T]) Version() uint32                { return 1 }
func (JSONCodec[T]) Encode(value T) ([]byte, error) { return json.Marshal(value) }
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
