package settings

import (
	"fmt"
	"strings"
)

// ScopeKind identifies a class of setting owner.
type ScopeKind string

const (
	// ScopeGlobal identifies the single owner with no ID.
	ScopeGlobal ScopeKind = "global"
	// ScopeTenant identifies an application-defined tenant.
	ScopeTenant ScopeKind = "tenant"
	// ScopeUser identifies an application-defined user.
	ScopeUser ScopeKind = "user"
	// ScopeResource identifies an application-defined resource.
	ScopeResource ScopeKind = "resource"
)

// Scope identifies one isolated owner of persisted settings.
type Scope struct {
	// Kind selects global, tenant, user or resource ownership.
	Kind ScopeKind `json:"kind"`
	// ID is empty for global and a nonempty identifier of at most 255 bytes otherwise.
	ID string `json:"id,omitempty"`
}

// Global returns the global scope with no identifier.
func Global() Scope { return Scope{Kind: ScopeGlobal} }

// Tenant returns a tenant scope; Validate must reject invalid identifiers before persistence.
func Tenant(id string) Scope { return Scope{Kind: ScopeTenant, ID: id} }

// User returns a user scope; it does not authenticate the user.
func User(id string) Scope { return Scope{Kind: ScopeUser, ID: id} }

// Resource returns a resource scope; it does not authorize access.
func Resource(id string) Scope { return Scope{Kind: ScopeResource, ID: id} }

// String renders kind:id, or global without an ID; it does not validate the scope.
func (scope Scope) String() string {
	if scope.Kind == ScopeGlobal {
		return string(scope.Kind)
	}
	return string(scope.Kind) + ":" + scope.ID
}

// Validate rejects unsafe, ambiguous, or oversized persisted identifiers.
func (scope Scope) Validate() error {
	if scope.Kind != ScopeGlobal && scope.Kind != ScopeTenant &&
		scope.Kind != ScopeUser && scope.Kind != ScopeResource {
		return fmt.Errorf("%w: unknown kind", ErrInvalidScope)
	}
	if scope.Kind == ScopeGlobal && scope.ID != "" {
		return fmt.Errorf("%w: global scope has an identifier", ErrInvalidScope)
	}
	if scope.Kind != ScopeGlobal && (scope.ID == "" || len(scope.ID) > 255) {
		return fmt.Errorf("%w: identifier length", ErrInvalidScope)
	}
	if strings.ContainsAny(scope.ID, "\x00\r\n") {
		return fmt.Errorf("%w: identifier characters", ErrInvalidScope)
	}
	return nil
}

// ResolutionChain declares precedence from highest to lowest priority. Its
// zero value is invalid for resolution; no global owner is implicitly appended.
type ResolutionChain struct{ scopes []Scope }

// Chain copies an explicit precedence chain without validating it; resolution
// rejects empty chains, duplicate owners and invalid scopes.
func Chain(scopes ...Scope) ResolutionChain {
	return ResolutionChain{scopes: append([]Scope(nil), scopes...)}
}

// Scopes returns a defensive copy of the precedence chain.
func (chain ResolutionChain) Scopes() []Scope {
	return append([]Scope(nil), chain.scopes...)
}

func (chain ResolutionChain) validate() error {
	if len(chain.scopes) == 0 {
		return fmt.Errorf("%w: empty", ErrInvalidChain)
	}
	seen := make(map[Scope]struct{}, len(chain.scopes))
	for _, scope := range chain.scopes {
		if err := scope.Validate(); err != nil {
			return err
		}
		if _, ok := seen[scope]; ok {
			return fmt.Errorf("%w: duplicate %s", ErrInvalidChain, scope)
		}
		seen[scope] = struct{}{}
	}
	return nil
}
