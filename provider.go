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
	// StateMissing is an absent override or retained version tombstone; resolution continues.
	StateMissing State = iota
	// StateValue contains encoded bytes, including valid zero values.
	StateValue
	// StateCleared explicitly stops fallback without yielding a typed value.
	StateCleared
)

// Record is the provider-neutral persisted representation.
type Record struct {
	// Scope identifies the owner; it does not authorize access.
	Scope Scope
	// Key is the stable namespace/name persistence identifier.
	Key string
	// State distinguishes a value, a cleared override and a missing tombstone.
	State State
	// Data contains encoded value bytes; sensitive values require caller-owned encryption.
	Data []byte
	// CodecID identifies the serialization/envelope contract.
	CodecID string
	// CodecVersion identifies the version needed to interpret encoded bytes.
	CodecVersion uint32
	// Version is the monotonically increasing version at one scope/key coordinate.
	Version uint64
	// UpdatedAt is the timestamp supplied with the change or assigned by the provider.
	UpdatedAt time.Time
}

// Capabilities declares behavior a provider implements natively.
type Capabilities struct {
	// CompareAndSet reports support for expected-version writes.
	CompareAndSet bool
	// AtomicBulk reports all-or-nothing mutation batches.
	AtomicBulk bool
	// History reports retained change-history support.
	History bool
	// Snapshots reports coherent bulk reads suitable for snapshot capture.
	Snapshots bool
	// Subscriptions reports an optional invalidation subscription capability, not durable delivery.
	Subscriptions bool
}

// Atomicity declares whether a multi-setting operation may accept a provider
// without all-or-nothing semantics.
type Atomicity uint8

const (
	// RequireAtomic requires provider all-or-nothing bulk writes and is the zero default.
	RequireAtomic Atomicity = iota
	// AllowNonAtomic permits the provider's native batch semantics, including partial commits.
	AllowNonAtomic
)

// Action identifies a mutation operation.
type Action uint8

const (
	// ActionSet persists encoded value bytes.
	ActionSet Action = iota + 1
	// ActionClear persists a cleared marker that blocks fallback.
	ActionClear
	// ActionInherit removes the override while retaining version history.
	ActionInherit
)

// Change is required audit metadata for a mutation.
type Change struct {
	// Actor identifies the caller responsible for a change; never include secret values.
	Actor string
	// Reason explains a change without embedding sensitive setting data.
	Reason string
	// At is the change timestamp; zero lets the provider choose its clock.
	At time.Time
}

// Mutation is an encoded, provider-neutral setting write.
type Mutation struct {
	// Scope identifies the owner; it does not authorize access.
	Scope Scope
	// Key is the stable namespace/name persistence identifier.
	Key string
	// Action selects set, clear or inherit.
	Action Action
	// Data contains encoded value bytes; sensitive values require caller-owned encryption.
	Data []byte
	// CodecID identifies the serialization/envelope contract.
	CodecID string
	// CodecVersion identifies the version needed to interpret encoded bytes.
	CodecVersion uint32
	// ExpectedVersion is a borrowed optional CAS precondition; nil means unconditional, zero
	// means no prior version.
	ExpectedVersion *uint64
	// Sensitive requests omission of value bytes from new audit events, not encryption or erasure.
	Sensitive bool
	// Change supplies required actor/reason and an optional timestamp.
	Change Change
}

// AuditValue holds encoded audit bytes or an explicit redaction marker; safety
// depends on correct sensitivity classification, not the struct alone.
type AuditValue struct {
	// State distinguishes a value, a cleared override and a missing tombstone.
	State State
	// Data contains encoded audit bytes and is omitted when Redacted is true.
	Data []byte
	// Redacted marks omitted sensitive value bytes; it is not proof of source authentication.
	Redacted bool
}

// ChangeRecord describes a retained append-only audit event. Returned records
// may be edited by callers without changing provider history. Codec metadata
// preserves the identity needed to interpret old bytes after definitions evolve.
type ChangeRecord struct {
	// Scope identifies the owner; it does not authorize access.
	Scope Scope
	// Key is the stable namespace/name persistence identifier.
	Key string
	// Action selects set, clear or inherit.
	Action Action
	// Version is the monotonically increasing version at one scope/key coordinate.
	Version uint64
	// CodecID identifies the serialization/envelope contract.
	CodecID string
	// CodecVersion identifies the version needed to interpret encoded bytes.
	CodecVersion uint32
	// Before is the pre-mutation audit value.
	Before AuditValue
	// After is the post-mutation audit value.
	After AuditValue
	// Actor identifies the caller responsible for a change; never include secret values.
	Actor string
	// Reason explains a change without embedding sensitive setting data.
	Reason string
	// At is the effective provider-assigned or caller-supplied change timestamp.
	At time.Time
}

// HistoryQuery bounds audit reads.
type HistoryQuery struct {
	// Scope identifies the owner; it does not authorize access.
	Scope Scope
	// Key optionally restricts history to one stable identifier; empty selects all keys.
	Key string
	// Limit bounds results to 1–1000; ordering is provider-specific.
	Limit int
}

// Provider is the read/write storage seam; memory and Snapshot are non-durable
// implementations. Implementations must be safe for concurrent calls, honor
// nonnil contexts cooperatively, preserve advertised atomicity, and return
// caller-owned encoded buffers. Callers must not mutate borrowed inputs during
// calls. Scope validation is not tenant authorization; deployments own access
// control, connection lifetimes and encryption. Error text must not expose values.
// Unsupported operations return ErrUnsupported; no common retry policy is added.
type Provider interface {
	// Capabilities reports native guarantees; callers must check required capabilities before
	// selecting an operation.
	Capabilities() Capabilities
	// Get reads one coordinate; false denotes no override, though a provider may return a
	// version tombstone.
	Get(context.Context, Scope, string) (Record, bool, error)
	// BulkGet reads the scopes/keys Cartesian product, including retained tombstones where
	// supported; Snapshots promises coherence.
	BulkGet(context.Context, []Scope, []string) ([]Record, error)
	// Apply validates and commits one mutation; implementations preserve monotonic versions and
	// sticky audit sensitivity.
	Apply(context.Context, Mutation) (Record, error)
	// BulkApply validates batches and preserves AtomicBulk when advertised; errors may include
	// provider-specific commit uncertainty.
	BulkApply(context.Context, []Mutation) ([]Record, error)
	// History reads at most the requested limit; ordering is provider-specific and data must be
	// safe to return.
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
