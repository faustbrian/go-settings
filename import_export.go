package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ExportFormat is the stable media identifier for settings documents.
const ExportFormat = "go-settings/export"

// ExportOptions controls schema metadata and sensitive-value handling.
type ExportOptions struct {
	// Schema is the application-owned schema identity; nonempty for export.
	Schema string
	// At sets ExportedAt; zero uses the current UTC time.
	At time.Time
	// IncludeSensitive opts into secret-bearing bytes and defaults; callers must protect the
	// resulting document.
	IncludeSensitive bool
}

// DefinitionDescriptor preserves definition and default codec metadata.
type DefinitionDescriptor struct {
	// StableID is the stable namespace/name key identity.
	StableID string `json:"stable_id"`
	// Namespace is the definition namespace.
	Namespace string `json:"namespace"`
	// Name is the definition name within its namespace.
	Name string `json:"name"`
	// CodecID identifies the encoding contract.
	CodecID string `json:"codec_id"`
	// CodecVersion identifies the encoding contract version.
	CodecVersion uint32 `json:"codec_version"`
	// Documentation carries descriptive definition text.
	Documentation string `json:"documentation,omitempty"`
	// DisplayName is a changeable human label, not persistence identity.
	DisplayName string `json:"display_name,omitempty"`
	// Sensitive records the definition sensitivity classification.
	Sensitive bool `json:"sensitive,omitempty"`
	// HasDefault distinguishes no default from an encoded zero or empty default.
	HasDefault bool `json:"has_default,omitempty"`
	// Default contains encoded default bytes when not redacted; custom definitions control
	// buffer ownership.
	Default []byte `json:"default,omitempty"`
	// Redacted marks an omitted sensitive default; Import ignores this descriptor.
	Redacted bool `json:"redacted,omitempty"`
}

// ExportEntry is one owner-scoped persisted value.
type ExportEntry struct {
	// Scope identifies the persisted owner, not authorization.
	Scope Scope `json:"scope"`
	// Key is the stable key identity.
	Key string `json:"key"`
	// State selects value, clear or inherit on import.
	State State `json:"state"`
	// Data holds encoded value bytes; export/import copy record payloads.
	Data []byte `json:"data,omitempty"`
	// CodecID identifies the encoding contract.
	CodecID string `json:"codec_id"`
	// CodecVersion identifies the encoding contract version.
	CodecVersion uint32 `json:"codec_version"`
	// Version is source provenance only; import does not use it as a CAS precondition.
	Version uint64 `json:"source_version"`
	// Redacted marks omitted bytes; redacted entries cannot be imported.
	Redacted bool `json:"redacted,omitempty"`
}

// ExportDocument is the versioned portable settings format.
type ExportDocument struct {
	// Format must equal ExportFormat for import.
	Format string `json:"format"`
	// Version is the document format version; import accepts 1.
	Version uint32 `json:"version"`
	// Schema is the application-owned schema identity; nonempty for export.
	Schema string `json:"schema"`
	// ExportedAt records export time; import does not enforce freshness.
	ExportedAt time.Time `json:"exported_at"`
	// Definitions are descriptive metadata; Import uses its registry rather than trusting these
	// descriptors.
	Definitions []DefinitionDescriptor `json:"definitions"`
	// Entries carry owner-scoped writes; import accepts 1–1000 unique coordinates.
	Entries []ExportEntry `json:"entries"`
}

// Export builds a portable document using one provider BulkGet for 1–1000
// coordinates. Coherence depends on the provider; no additional transaction or
// full record validation is added. Definitions remain in input order; entries
// are sorted by owner/key. Secret bytes/defaults are omitted unless opted in.
// The document is not authenticated or encrypted; custom default buffers may
// remain borrowed and callers own protection of exported data.
func Export(ctx context.Context, provider Provider, scopes []Scope, definitions []Definition, options ExportOptions) (ExportDocument, error) {
	if options.Schema == "" {
		return ExportDocument{}, fmt.Errorf("settings: export schema is required")
	}
	if len(scopes) == 0 || len(definitions) == 0 || len(scopes)*len(definitions) > 1000 {
		return ExportDocument{}, fmt.Errorf("settings: export size must be between 1 and 1000 coordinates")
	}
	keys := make([]string, 0, len(definitions))
	descriptors := make([]DefinitionDescriptor, 0, len(definitions))
	definitionByKey := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		if definition == nil {
			return ExportDocument{}, fmt.Errorf("settings: nil export definition")
		}
		if _, exists := definitionByKey[definition.StableID()]; exists {
			return ExportDocument{}, fmt.Errorf("%w: %s", ErrDuplicateDefinition, definition.StableID())
		}
		definitionByKey[definition.StableID()] = definition
		keys = append(keys, definition.StableID())
		defaultData, hasDefault, err := definition.DefaultEncoded()
		if err != nil {
			return ExportDocument{}, err
		}
		descriptor := DefinitionDescriptor{
			StableID: definition.StableID(), Namespace: definition.Namespace(), Name: definition.Name(),
			CodecID: definition.CodecID(), CodecVersion: definition.CodecVersion(),
			Documentation: definition.Documentation(), DisplayName: definition.DisplayName(),
			Sensitive: definition.Sensitive(), HasDefault: hasDefault,
		}
		if hasDefault && definition.Sensitive() && !options.IncludeSensitive {
			descriptor.Redacted = true
		} else {
			descriptor.Default = defaultData
		}
		descriptors = append(descriptors, descriptor)
	}
	records, err := provider.BulkGet(ctx, scopes, keys)
	if err != nil {
		return ExportDocument{}, err
	}
	entries := make([]ExportEntry, 0, len(records))
	for _, record := range records {
		definition, ok := definitionByKey[record.Key]
		if !ok {
			return ExportDocument{}, fmt.Errorf("settings: provider returned unknown key")
		}
		entry := ExportEntry{
			Scope: record.Scope, Key: record.Key, State: record.State,
			CodecID: record.CodecID, CodecVersion: record.CodecVersion, Version: record.Version,
		}
		if definition.Sensitive() && record.State == StateValue && !options.IncludeSensitive {
			entry.Redacted = true
		} else {
			entry.Data = append([]byte(nil), record.Data...)
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(left, right ExportEntry) int {
		if scopeOrder := strings.Compare(left.Scope.String(), right.Scope.String()); scopeOrder != 0 {
			return scopeOrder
		}
		return strings.Compare(left.Key, right.Key)
	})
	at := options.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return ExportDocument{
		Format: ExportFormat, Version: 1, Schema: options.Schema, ExportedAt: at,
		Definitions: descriptors, Entries: entries,
	}, nil
}

// ImportOptions controls schema compatibility and change metadata.
type ImportOptions struct {
	// ExpectedSchema optionally requires exact schema equality; empty disables this comparison.
	ExpectedSchema string
	// Atomicity selects the provider atomic-bulk requirement; its zero value is RequireAtomic.
	Atomicity Atomicity
	// Change supplies fresh actor/reason/timestamp metadata for imported writes.
	Change Change
}

// Import validates all 1–1000 unique entries before applying a provider batch.
// It uses registry definitions, not document descriptors, and ignores source
// versions/ExportedAt: these are new unconditional writes with fresh Change
// metadata. Redacted entries are rejected. Atomicity depends on the selected
// provider capability; no retry, authentication or authorization is added.
func Import(ctx context.Context, provider Provider, registry *Registry, document ExportDocument, options ImportOptions) ([]Record, error) {
	if document.Format != ExportFormat || document.Version != 1 || document.Schema == "" {
		return nil, fmt.Errorf("settings: unsupported import format")
	}
	if options.ExpectedSchema != "" && document.Schema != options.ExpectedSchema {
		return nil, fmt.Errorf("settings: import schema mismatch")
	}
	if len(document.Entries) == 0 || len(document.Entries) > 1000 {
		return nil, fmt.Errorf("settings: import size must be between 1 and 1000")
	}
	if options.Atomicity == RequireAtomic && !provider.Capabilities().AtomicBulk {
		return nil, fmt.Errorf("%w: atomic import", ErrUnsupported)
	}
	mutations := make([]Mutation, 0, len(document.Entries))
	seen := make(map[string]struct{}, len(document.Entries))
	for _, entry := range document.Entries {
		coordinate := entry.Scope.String() + "\x00" + entry.Key
		if _, ok := seen[coordinate]; ok {
			return nil, fmt.Errorf("%w: duplicate import coordinate", ErrInvalidMutation)
		}
		seen[coordinate] = struct{}{}
		if entry.Redacted {
			return nil, fmt.Errorf("settings: redacted values cannot be imported")
		}
		definition, ok := registry.Lookup(entry.Key)
		if !ok || definition.CodecID() != entry.CodecID ||
			definition.CodecVersion() != entry.CodecVersion {
			return nil, fmt.Errorf("%w: import definition for %s", ErrIncompatibleDefinition, entry.Key)
		}
		action := ActionSet
		switch entry.State {
		case StateValue:
			if err := definition.ValidateEncoded(entry.Data); err != nil {
				return nil, err
			}
		case StateCleared:
			action = ActionClear
		case StateMissing:
			action = ActionInherit
		default:
			return nil, fmt.Errorf("%w: import state", ErrInvalidMutation)
		}
		mutation := Mutation{
			Scope: entry.Scope, Key: entry.Key, Action: action,
			CodecID: entry.CodecID, CodecVersion: entry.CodecVersion,
			Sensitive: definition.Sensitive(), Change: options.Change,
		}
		if action == ActionSet {
			mutation.Data = append([]byte(nil), entry.Data...)
		}
		if err := ValidateMutation(mutation); err != nil {
			return nil, err
		}
		mutations = append(mutations, mutation)
	}
	return provider.BulkApply(ctx, mutations)
}
