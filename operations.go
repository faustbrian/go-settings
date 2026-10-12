package settings

import (
	"context"
	"fmt"
)

// Set encodes, validates, and persists a typed value.
func Set[T any](ctx context.Context, provider Provider, scope Scope, key Key[T], value T, change Change) (Record, error) {
	mutation, err := PrepareSet(scope, key, value, nil, change)
	if err != nil {
		return Record{}, err
	}
	return provider.Apply(ctx, mutation)
}

// CompareAndSet writes only when the current version equals expected. A nil
// expected version performs an unconditional versioned write. Expected is
// borrowed for the synchronous provider call; zero requires no prior version,
// not merely an absent override after an inherit tombstone.
func CompareAndSet[T any](ctx context.Context, provider Provider, scope Scope, key Key[T], value T, expected *uint64, change Change) (Record, error) {
	mutation, err := PrepareSet(scope, key, value, expected, change)
	if err != nil {
		return Record{}, err
	}
	return provider.Apply(ctx, mutation)
}

// PrepareSet validates and encodes a typed value for a heterogeneous bulk
// operation. The codec controls Data ownership and expected remains a borrowed
// pointer; do not change either before applying the prepared mutation.
func PrepareSet[T any](scope Scope, key Key[T], value T, expected *uint64, change Change) (Mutation, error) {
	if err := scope.Validate(); err != nil {
		return Mutation{}, err
	}
	if err := key.ValidateDefinition(); err != nil {
		return Mutation{}, err
	}
	if err := key.validateValue(value); err != nil {
		return Mutation{}, fmt.Errorf("%w: %w", ErrInvalidValue, err)
	}
	data, err := key.codec.Encode(value)
	if err != nil {
		return Mutation{}, fmt.Errorf("%w: encode %s", ErrInvalidValue, key.StableID())
	}
	mutation := Mutation{
		Scope: scope, Key: key.StableID(), Action: ActionSet, Data: data,
		CodecID: key.CodecID(), CodecVersion: key.CodecVersion(),
		ExpectedVersion: expected, Sensitive: key.Sensitive(), Change: change,
	}
	if err := ValidateMutation(mutation); err != nil {
		return Mutation{}, err
	}
	return mutation, nil
}

// Clear stores an explicit cleared marker that stops fallback resolution.
func Clear[T any](ctx context.Context, provider Provider, scope Scope, key Key[T], change Change) (Record, error) {
	mutation, err := prepareState(scope, key, ActionClear, nil, change)
	if err != nil {
		return Record{}, err
	}
	return provider.Apply(ctx, mutation)
}

// CompareAndClear stores a cleared marker only at the expected version.
func CompareAndClear[T any](ctx context.Context, provider Provider, scope Scope, key Key[T], expected uint64, change Change) (Record, error) {
	mutation, err := prepareState(scope, key, ActionClear, &expected, change)
	if err != nil {
		return Record{}, err
	}
	return provider.Apply(ctx, mutation)
}

func prepareState[T any](scope Scope, key Key[T], action Action, expected *uint64, change Change) (Mutation, error) {
	if err := key.ValidateDefinition(); err != nil {
		return Mutation{}, err
	}
	mutation := Mutation{
		Scope: scope, Key: key.StableID(), Action: ActionClear,
		CodecID: key.CodecID(), CodecVersion: key.CodecVersion(),
		ExpectedVersion: expected, Sensitive: key.Sensitive(), Change: change,
	}
	mutation.Action = action
	if err := ValidateMutation(mutation); err != nil {
		return Mutation{}, err
	}
	return mutation, nil
}

// Inherit removes an owner's value so resolution continues down the chain.
func Inherit[T any](ctx context.Context, provider Provider, scope Scope, key Key[T], change Change) (Record, error) {
	mutation, err := prepareState(scope, key, ActionInherit, nil, change)
	if err != nil {
		return Record{}, err
	}
	return provider.Apply(ctx, mutation)
}

// CompareAndInherit removes an override only at the expected version.
func CompareAndInherit[T any](ctx context.Context, provider Provider, scope Scope, key Key[T], expected uint64, change Change) (Record, error) {
	mutation, err := prepareState(scope, key, ActionInherit, &expected, change)
	if err != nil {
		return Record{}, err
	}
	return provider.Apply(ctx, mutation)
}

// Bulk applies prepared heterogeneous mutations with an explicit atomicity
// requirement. It validates all mutations before delegating but does not create
// its own transaction, retry or copy input payloads. AllowNonAtomic permits
// provider-specific partial commits; inspect both results and error.
func Bulk(ctx context.Context, provider Provider, mutations []Mutation, atomicity Atomicity) ([]Record, error) {
	if atomicity == RequireAtomic && !provider.Capabilities().AtomicBulk {
		return nil, fmt.Errorf("%w: atomic bulk", ErrUnsupported)
	}
	for _, mutation := range mutations {
		if err := ValidateMutation(mutation); err != nil {
			return nil, err
		}
	}
	return provider.BulkApply(ctx, mutations)
}
