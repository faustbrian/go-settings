package settings

import (
	"fmt"
	"strings"
	"sync"
)

// Registry stores immutable setting definitions and is safe for concurrent
// registration and lookup.
type Registry struct {
	mu          sync.RWMutex
	definitions map[string]Definition
	namespaces  map[string]NamespaceDefinition
}

// NamespaceDefinition documents a stable definition namespace.
type NamespaceDefinition struct {
	ID            string
	Documentation string
}

// NewNamespace defines registry-level namespace documentation.
func NewNamespace(id, documentation string) NamespaceDefinition {
	return NamespaceDefinition{ID: id, Documentation: documentation}
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		definitions: make(map[string]Definition),
		namespaces:  make(map[string]NamespaceDefinition),
	}
}

// RegisterNamespace adds namespace metadata without coupling it to key labels.
func (registry *Registry) RegisterNamespace(namespace NamespaceDefinition) error {
	if namespace.ID == "" || len(namespace.ID) > 128 || strings.ContainsAny(namespace.ID, "/\x00\r\n") {
		return fmt.Errorf("%w: namespace", ErrInvalidDefinition)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.namespaces[namespace.ID]; exists {
		return fmt.Errorf("%w: namespace %s", ErrDuplicateDefinition, namespace.ID)
	}
	registry.namespaces[namespace.ID] = namespace
	return nil
}

// Register adds a definition, rejecting duplicate and incompatible stable IDs.
func (registry *Registry) Register(definition Definition) error {
	if definition == nil {
		return fmt.Errorf("%w: nil", ErrInvalidDefinition)
	}
	if err := definition.ValidateDefinition(); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()

	existing, ok := registry.definitions[definition.StableID()]
	if !ok {
		registry.definitions[definition.StableID()] = definition
		return nil
	}
	if existing.CodecID() != definition.CodecID() ||
		existing.CodecVersion() != definition.CodecVersion() {
		return fmt.Errorf("%w: %s", ErrIncompatibleDefinition, definition.StableID())
	}

	return fmt.Errorf("%w: %s", ErrDuplicateDefinition, definition.StableID())
}

// Lookup returns a definition by stable ID.
func (registry *Registry) Lookup(stableID string) (Definition, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()

	definition, ok := registry.definitions[stableID]
	return definition, ok
}
