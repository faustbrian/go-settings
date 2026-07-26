package settings

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ExportFormat is the stable media identifier for settings documents.
const ExportFormat = "go-settings/export"

// ExportOptions controls schema metadata and sensitive-value handling.
type ExportOptions struct {
	Schema           string
	At               time.Time
	IncludeSensitive bool
}

// DefinitionDescriptor preserves definition and default codec metadata.
type DefinitionDescriptor struct {
	StableID      string `json:"stable_id"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	CodecID       string `json:"codec_id"`
	CodecVersion  uint32 `json:"codec_version"`
	Documentation string `json:"documentation,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	Sensitive     bool   `json:"sensitive,omitempty"`
	HasDefault    bool   `json:"has_default,omitempty"`
	Default       []byte `json:"default,omitempty"`
	Redacted      bool   `json:"redacted,omitempty"`
}

// ExportEntry is one owner-scoped persisted value.
type ExportEntry struct {
	Scope        Scope  `json:"scope"`
	Key          string `json:"key"`
	State        State  `json:"state"`
	Data         []byte `json:"data,omitempty"`
	CodecID      string `json:"codec_id"`
	CodecVersion uint32 `json:"codec_version"`
	Version      uint64 `json:"source_version"`
	Redacted     bool   `json:"redacted,omitempty"`
}

// ExportDocument is the versioned portable settings format.
type ExportDocument struct {
	Format      string                 `json:"format"`
	Version     uint32                 `json:"version"`
	Schema      string                 `json:"schema"`
	ExportedAt  time.Time              `json:"exported_at"`
	Definitions []DefinitionDescriptor `json:"definitions"`
	Entries     []ExportEntry          `json:"entries"`
}

// Export captures an internally consistent portable document.
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
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Scope.String() == entries[j].Scope.String() {
			return entries[i].Key < entries[j].Key
		}
		return entries[i].Scope.String() < entries[j].Scope.String()
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
	ExpectedSchema string
	Atomicity      Atomicity
	Change         Change
}

// Import validates the whole document before applying any mutation.
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
