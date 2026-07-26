package settings

import "errors"

var (
	// ErrDuplicateDefinition reports a repeated stable key identifier.
	ErrDuplicateDefinition = errors.New("settings: duplicate definition")
	// ErrIncompatibleDefinition reports definitions that share an identifier
	// but disagree about their codec contract.
	ErrIncompatibleDefinition = errors.New("settings: incompatible definition")
	// ErrInvalidDefinition reports unsafe or incomplete key metadata.
	ErrInvalidDefinition = errors.New("settings: invalid definition")
	// ErrInvalidScope reports malformed or unsupported owner identifiers.
	ErrInvalidScope = errors.New("settings: invalid scope")
	// ErrInvalidChain reports duplicate owners or an empty resolution chain.
	ErrInvalidChain = errors.New("settings: invalid resolution chain")
	// ErrConflict reports a failed compare-and-set operation.
	ErrConflict = errors.New("settings: version conflict")
	// ErrInvalidValue reports a value that cannot be encoded, decoded, or
	// validated under its registered definition.
	ErrInvalidValue = errors.New("settings: invalid value")
	// ErrInvalidChange reports missing or unsafe audit metadata.
	ErrInvalidChange = errors.New("settings: invalid change metadata")
	// ErrInvalidMutation reports an unsafe provider write request.
	ErrInvalidMutation = errors.New("settings: invalid mutation")
	// ErrUnsupported reports a provider capability that is unavailable.
	ErrUnsupported = errors.New("settings: unsupported capability")
)
