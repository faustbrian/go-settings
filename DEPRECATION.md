# Deprecation Policy

Deprecations MUST identify the replacement, reason, migration steps, and
earliest removal version. Public Go identifiers use a valid `Deprecated:` doc
paragraph and corresponding changelog entry.

At `v1` and later, a supported replacement SHOULD exist for at least one minor
release before removal. Security or correctness defects MAY require faster
removal when continued support would be unsafe; the release notes must explain
the exception.

Silent behavior changes, undocumented aliases, and indefinite deprecated code
are prohibited. Deprecations are checked during compatibility and release
review.

## Runtime.Close

`Runtime.Close(ctx)` is deprecated in favor of `Runtime.Shutdown(ctx)` because
context-aware complete shutdown follows the ecosystem `Shutdown(ctx)`
vocabulary; `Close` is reserved for immediate synchronous `io.Closer`-style
release. Replace the method name without changing call order or context budget.
The compatibility method delegates to `Shutdown`.

Removal is permitted only in an authorized next major release after `Shutdown`
has been publicly consumable for the longer of 180 days and two stable minor
releases that retain `Close(ctx)`. Owned-consumer and clean-consumer migration
proof must pass before removal.
