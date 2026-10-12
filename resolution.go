package settings

import (
	"context"
	"fmt"
)

// Status distinguishes all observable resolution outcomes.
type Status uint8

const (
	// StatusMissing means no stored winner or declared default exists.
	StatusMissing Status = iota
	// StatusStored means the highest-priority owner supplied a value.
	StatusStored
	// StatusInherited means a later owner supplied the value.
	StatusInherited
	// StatusDefaulted means all visited owners were missing and a definition default was used.
	StatusDefaulted
	// StatusCleared means an explicit clear stopped resolution.
	StatusCleared
	// StatusInvalid means chain, provider, codec or validation failed; inspect the accompanying error.
	StatusInvalid
)

// PathStep records one owner visited during deterministic resolution.
type PathStep struct {
	// Scope is the owner visited at this step.
	Scope Scope
	// State records missing, stored or cleared state at this owner.
	State State
	// Version is the selected or visited record version; zero for a missing/default outcome.
	Version uint64
}

// Result is a typed effective value and its full provenance.
type Result[T any] struct {
	// Value is meaningful for stored, inherited or defaulted results; reference-bearing T may
	// alias a default or codec output.
	Value T
	// Status identifies the resolution outcome.
	Status Status
	// Owner is the winning persisted owner, or zero when no persisted owner wins.
	Owner Scope
	// Version is the selected or visited record version; zero for a missing/default outcome.
	Version uint64
	// Path contains visited owners in precedence order; errors may return only a prefix.
	Path []PathStep
}

// Resolve evaluates the explicit chain in order, stopping at a clear, a value
// or an error. It performs separate provider reads, not an atomic snapshot.
// Provider/decode/validation failures return StatusInvalid and a partial Path;
// invalid stored data never falls through to defaults. Construct a valid key
// and supply nonnil provider/context; callbacks are synchronous and uncontained.
// Default values are borrowed and returned without deep copying or revalidation.
func Resolve[T any](ctx context.Context, provider Provider, key Key[T], chain ResolutionChain) (Result[T], error) {
	if err := chain.validate(); err != nil {
		return Result[T]{Status: StatusInvalid}, err
	}
	path := make([]PathStep, 0, len(chain.scopes))
	for index, scope := range chain.scopes {
		record, ok, err := provider.Get(ctx, scope, key.StableID())
		if err != nil {
			return Result[T]{Status: StatusInvalid, Path: path}, err
		}
		if !ok {
			path = append(path, PathStep{Scope: scope, State: StateMissing})
			continue
		}
		path = append(path, PathStep{Scope: scope, State: record.State, Version: record.Version})
		if record.State == StateCleared {
			return Result[T]{Status: StatusCleared, Owner: scope, Version: record.Version, Path: path}, nil
		}
		if record.CodecID != key.CodecID() || record.CodecVersion != key.CodecVersion() {
			return Result[T]{Status: StatusInvalid, Owner: scope, Version: record.Version, Path: path},
				fmt.Errorf("%w: codec contract for %s", ErrInvalidValue, key.StableID())
		}
		value, err := key.codec.Decode(record.Data)
		if err != nil {
			return Result[T]{Status: StatusInvalid, Owner: scope, Version: record.Version, Path: path},
				fmt.Errorf("%w: decode %s", ErrInvalidValue, key.StableID())
		}
		if err := key.validateValue(value); err != nil {
			return Result[T]{Status: StatusInvalid, Owner: scope, Version: record.Version, Path: path}, err
		}
		status := StatusStored
		if index > 0 {
			status = StatusInherited
		}
		return Result[T]{Value: value, Status: status, Owner: scope, Version: record.Version, Path: path}, nil
	}
	if key.hasDefault {
		return Result[T]{Value: key.defaultValue, Status: StatusDefaulted, Path: path}, nil
	}
	return Result[T]{Status: StatusMissing, Path: path}, nil
}
