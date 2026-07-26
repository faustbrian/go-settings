// Package migration provides resumable, idempotent setting-definition
// evolution for renames, codec transformations, and default changes.
package migration

import (
	"context"
	"fmt"
	"sync"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
)

// Kind identifies a migration step contract.
type Kind string

const (
	KindRename        Kind = "rename"
	KindTransform     Kind = "transform"
	KindDefaultChange Kind = "default-change"
)

// Step is an immutable, self-identifying migration operation.
type Step struct {
	id         string
	kind       Kind
	from       settings.Definition
	to         settings.Definition
	transform  func([]byte) ([]byte, error)
	oldDefault []byte
	newDefault []byte
}

// Rename moves persisted owner values between stable identifiers atomically.
func Rename(id string, from, to settings.Definition) Step {
	return Step{id: id, kind: KindRename, from: from, to: to}
}

// Transform upgrades persisted bytes and codec metadata in place. The target
// definition's codec contract is the idempotency marker.
func Transform(id string, from, to settings.Definition, transform func([]byte) ([]byte, error)) Step {
	return Step{id: id, kind: KindTransform, from: from, to: to, transform: transform}
}

// ChangeDefault records an interpretable definition-only default transition.
func ChangeDefault(id string, definition settings.Definition, oldValue, newValue []byte) Step {
	return Step{
		id: id, kind: KindDefaultChange, to: definition,
		oldDefault: append([]byte(nil), oldValue...), newDefault: append([]byte(nil), newValue...),
	}
}

// ID returns the stable, plan-local step identifier.
func (step Step) ID() string { return step.id }

// Kind returns the step's evolution contract.
func (step Step) Kind() Kind { return step.kind }

// Plan is a schema transition with stable provenance.
type Plan struct {
	ID         string
	FromSchema string
	ToSchema   string
	Steps      []Step
}

// Journal persists completion checkpoints independently of value versions.
type Journal interface {
	Completed(context.Context, string, string, settings.Scope) (bool, error)
	MarkCompleted(context.Context, string, string, settings.Scope, time.Time) error
}

// Report summarizes one resumable execution.
type Report struct {
	Completed int
	Skipped   int
}

// Run executes a plan for explicit owners. Steps remain idempotent even when
// the write commits immediately before a checkpoint failure.
func Run(ctx context.Context, provider settings.Provider, journal Journal, plan Plan, scopes []settings.Scope, change settings.Change) (Report, error) {
	if err := validatePlan(plan, scopes); err != nil {
		return Report{}, err
	}
	var report Report
	for _, scope := range scopes {
		for _, step := range plan.Steps {
			completed, err := journal.Completed(ctx, plan.ID, step.id, scope)
			if err != nil {
				return report, fmt.Errorf("settings migration read checkpoint: %w", err)
			}
			if completed {
				report.Skipped++
				continue
			}
			if err := applyStep(ctx, provider, scope, step, change); err != nil {
				return report, fmt.Errorf("settings migration %s at %s: %w", step.id, scope, err)
			}
			at := change.At
			if at.IsZero() {
				at = time.Now().UTC()
			}
			if err := journal.MarkCompleted(ctx, plan.ID, step.id, scope, at); err != nil {
				return report, fmt.Errorf("settings migration write checkpoint: %w", err)
			}
			report.Completed++
		}
	}
	return report, nil
}

func validatePlan(plan Plan, scopes []settings.Scope) error {
	if plan.ID == "" || plan.FromSchema == "" || plan.ToSchema == "" ||
		plan.FromSchema == plan.ToSchema || len(plan.Steps) == 0 || len(scopes) == 0 {
		return fmt.Errorf("settings migration: invalid plan")
	}
	seen := make(map[string]struct{}, len(plan.Steps))
	for _, step := range plan.Steps {
		if step.id == "" || step.to == nil {
			return fmt.Errorf("settings migration: invalid step")
		}
		if _, ok := seen[step.id]; ok {
			return fmt.Errorf("settings migration: duplicate step %s", step.id)
		}
		seen[step.id] = struct{}{}
		switch step.kind {
		case KindRename:
			if step.from == nil || step.from.StableID() == step.to.StableID() {
				return fmt.Errorf("settings migration: invalid rename")
			}
		case KindTransform:
			if step.from == nil || step.transform == nil ||
				step.from.StableID() != step.to.StableID() ||
				(step.from.CodecID() == step.to.CodecID() &&
					step.from.CodecVersion() == step.to.CodecVersion()) {
				return fmt.Errorf("settings migration: invalid transform")
			}
		case KindDefaultChange:
			if err := step.to.ValidateEncoded(step.oldDefault); err != nil {
				return fmt.Errorf("settings migration: invalid old default")
			}
			if err := step.to.ValidateEncoded(step.newDefault); err != nil {
				return fmt.Errorf("settings migration: invalid new default")
			}
		default:
			return fmt.Errorf("settings migration: unknown step kind")
		}
	}
	for _, scope := range scopes {
		if err := scope.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func applyStep(ctx context.Context, provider settings.Provider, scope settings.Scope, step Step, change settings.Change) error {
	switch step.kind {
	case KindRename:
		return applyRename(ctx, provider, scope, step, change)
	case KindTransform:
		return applyTransform(ctx, provider, scope, step, change)
	case KindDefaultChange:
		return nil
	default:
		return fmt.Errorf("unknown step")
	}
}

func applyRename(ctx context.Context, provider settings.Provider, scope settings.Scope, step Step, change settings.Change) error {
	if !provider.Capabilities().AtomicBulk {
		return fmt.Errorf("%w: atomic rename", settings.ErrUnsupported)
	}
	source, sourcePresent, err := provider.Get(ctx, scope, step.from.StableID())
	if err != nil {
		return err
	}
	target, targetPresent, err := provider.Get(ctx, scope, step.to.StableID())
	if err != nil {
		return err
	}
	if !sourcePresent {
		return nil
	}
	if targetPresent {
		return fmt.Errorf("rename target already exists at version %d", target.Version)
	}
	if source.CodecID != step.from.CodecID() || source.CodecVersion != step.from.CodecVersion() {
		return fmt.Errorf("source codec contract mismatch")
	}
	action := settings.ActionSet
	var data []byte
	if source.State == settings.StateCleared {
		action = settings.ActionClear
	} else {
		data = append([]byte(nil), source.Data...)
		if step.from.CodecID() != step.to.CodecID() || step.from.CodecVersion() != step.to.CodecVersion() {
			return fmt.Errorf("rename cannot change codec contract")
		}
		if err := step.to.ValidateEncoded(data); err != nil {
			return err
		}
	}
	zero := uint64(0)
	sourceVersion := source.Version
	_, err = provider.BulkApply(ctx, []settings.Mutation{
		{
			Scope: scope, Key: step.to.StableID(), Action: action, Data: data,
			CodecID: step.to.CodecID(), CodecVersion: step.to.CodecVersion(),
			ExpectedVersion: &zero, Sensitive: step.to.Sensitive(), Change: change,
		},
		{
			Scope: scope, Key: step.from.StableID(), Action: settings.ActionInherit,
			CodecID: step.from.CodecID(), CodecVersion: step.from.CodecVersion(),
			ExpectedVersion: &sourceVersion, Sensitive: step.from.Sensitive(), Change: change,
		},
	})
	return err
}

func applyTransform(ctx context.Context, provider settings.Provider, scope settings.Scope, step Step, change settings.Change) error {
	record, present, err := provider.Get(ctx, scope, step.from.StableID())
	if err != nil || !present {
		return err
	}
	if record.CodecID == step.to.CodecID() && record.CodecVersion == step.to.CodecVersion() {
		return nil
	}
	if record.CodecID != step.from.CodecID() || record.CodecVersion != step.from.CodecVersion() {
		return fmt.Errorf("transform source codec contract mismatch")
	}
	action := settings.ActionSet
	var data []byte
	if record.State == settings.StateCleared {
		action = settings.ActionClear
	} else {
		data, err = step.transform(record.Data)
		if err != nil {
			return fmt.Errorf("transform value: %w", err)
		}
		if err := step.to.ValidateEncoded(data); err != nil {
			return err
		}
	}
	expected := record.Version
	_, err = provider.Apply(ctx, settings.Mutation{
		Scope: scope, Key: step.to.StableID(), Action: action, Data: data,
		CodecID: step.to.CodecID(), CodecVersion: step.to.CodecVersion(),
		ExpectedVersion: &expected, Sensitive: step.to.Sensitive(), Change: change,
	})
	return err
}

// MemoryJournal is a deterministic concurrent checkpoint journal for tests.
type MemoryJournal struct {
	mu        sync.RWMutex
	completed map[string]time.Time
}

// NewMemoryJournal constructs an empty journal.
func NewMemoryJournal() *MemoryJournal {
	return &MemoryJournal{completed: make(map[string]time.Time)}
}

func journalKey(plan, step string, scope settings.Scope) string {
	return plan + "\x00" + step + "\x00" + scope.String()
}

func (journal *MemoryJournal) Completed(_ context.Context, plan, step string, scope settings.Scope) (bool, error) {
	journal.mu.RLock()
	defer journal.mu.RUnlock()
	_, ok := journal.completed[journalKey(plan, step, scope)]
	return ok, nil
}

func (journal *MemoryJournal) MarkCompleted(_ context.Context, plan, step string, scope settings.Scope, at time.Time) error {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.completed[journalKey(plan, step, scope)] = at
	return nil
}
