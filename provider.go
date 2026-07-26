package settings

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// State is the persisted state of a setting at one owner.
type State uint8

const (
	StateMissing State = iota
	StateValue
	StateCleared
)

// Record is the provider-neutral persisted representation.
type Record struct {
	Scope        Scope
	Key          string
	State        State
	Data         []byte
	CodecID      string
	CodecVersion uint32
	Version      uint64
	UpdatedAt    time.Time
}

// Capabilities declares behavior a provider implements natively.
type Capabilities struct {
	CompareAndSet bool
	AtomicBulk    bool
	History       bool
	Snapshots     bool
	Subscriptions bool
}

// Atomicity declares whether a multi-setting operation may accept a provider
// without all-or-nothing semantics.
type Atomicity uint8

const (
	RequireAtomic Atomicity = iota
	AllowNonAtomic
)

// Action identifies a mutation operation.
type Action uint8

const (
	ActionSet Action = iota + 1
	ActionClear
	ActionInherit
)

// Change is required audit metadata for a mutation.
type Change struct {
	Actor  string
	Reason string
	At     time.Time
}

// Mutation is an encoded, provider-neutral setting write.
type Mutation struct {
	Scope           Scope
	Key             string
	Action          Action
	Data            []byte
	CodecID         string
	CodecVersion    uint32
	ExpectedVersion *uint64
	Sensitive       bool
	Change          Change
}

// AuditValue is a safely renderable value in a change record.
type AuditValue struct {
	State    State
	Data     []byte
	Redacted bool
}

// ChangeRecord is an immutable audit event. Codec metadata keeps old records
// interpretable after definitions evolve.
type ChangeRecord struct {
	Scope        Scope
	Key          string
	Action       Action
	Version      uint64
	CodecID      string
	CodecVersion uint32
	Before       AuditValue
	After        AuditValue
	Actor        string
	Reason       string
	At           time.Time
}

// HistoryQuery bounds audit reads.
type HistoryQuery struct {
	Scope Scope
	Key   string
	Limit int
}

// Provider is the minimal durable storage contract.
type Provider interface {
	Capabilities() Capabilities
	Get(context.Context, Scope, string) (Record, bool, error)
	BulkGet(context.Context, []Scope, []string) ([]Record, error)
	Apply(context.Context, Mutation) (Record, error)
	BulkApply(context.Context, []Mutation) ([]Record, error)
	History(context.Context, HistoryQuery) ([]ChangeRecord, error)
}

// ValidateMutation enforces provider-boundary isolation, resource limits, and
// auditable write metadata without rendering sensitive values.
func ValidateMutation(mutation Mutation) error {
	if err := mutation.Scope.Validate(); err != nil {
		return err
	}
	if mutation.Key == "" || len(mutation.Key) > 512 ||
		strings.ContainsAny(mutation.Key, "\x00\r\n") {
		return fmt.Errorf("%w: key", ErrInvalidMutation)
	}
	if mutation.Action != ActionSet && mutation.Action != ActionClear &&
		mutation.Action != ActionInherit {
		return fmt.Errorf("%w: action", ErrInvalidMutation)
	}
	if mutation.CodecID == "" || len(mutation.CodecID) > 255 || mutation.CodecVersion == 0 {
		return fmt.Errorf("%w: codec", ErrInvalidMutation)
	}
	if mutation.Action == ActionSet && len(mutation.Data) > 1<<20 {
		return fmt.Errorf("%w: value exceeds 1 MiB", ErrInvalidMutation)
	}
	if mutation.Action != ActionSet && len(mutation.Data) != 0 {
		return fmt.Errorf("%w: non-value data", ErrInvalidMutation)
	}
	if mutation.Change.Actor == "" || mutation.Change.Reason == "" ||
		len(mutation.Change.Actor) > 255 || len(mutation.Change.Reason) > 2000 ||
		strings.ContainsRune(mutation.Change.Actor, '\x00') ||
		strings.ContainsRune(mutation.Change.Reason, '\x00') {
		return ErrInvalidChange
	}
	return nil
}
