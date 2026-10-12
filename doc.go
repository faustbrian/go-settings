// Package settings provides typed, persisted runtime settings with explicit
// ownership, precedence, provenance, and optimistic concurrency. Start with
// NewKey, Chain and Resolve for direct reads, or NewRuntime for policy-bounded
// process-local snapshots. Clear blocks fallback; Inherit removes an override.
//
// Scope identifiers and sensitivity flags are not authorization or encryption.
// Applications own tenant admission, connection lifetimes, authenticated secret
// storage and the concurrency safety of custom codecs, definitions and callbacks.
// Direct Resolve reads owners sequentially; Capture relies on the provider's
// bulk coherence. Captured encoded bytes are copied, but decoded values and
// mutable defaults are not universally deep-copied.
//
// The core uses the standard library; optional postgres and valkey packages
// introduce their client dependencies. Runtime.Start/Shutdown own background
// reconciliation, not borrowed provider connections or externally initiated IO.
package settings
