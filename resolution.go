package settings

import (
	"context"
	"fmt"
)

// Status distinguishes all observable resolution outcomes.
type Status uint8

const (
	StatusMissing Status = iota
	StatusStored
	StatusInherited
	StatusDefaulted
	StatusCleared
	StatusInvalid
)

// PathStep records one owner visited during deterministic resolution.
type PathStep struct {
	Scope   Scope
	State   State
	Version uint64
}

// Result is a typed effective value and its full provenance.
type Result[T any] struct {
	Value   T
	Status  Status
	Owner   Scope
	Version uint64
	Path    []PathStep
}

// Resolve evaluates a typed key against a caller-declared precedence chain.
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
