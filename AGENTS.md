# Engineering Policy

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

## Scope And Authority

- This file is the canonical policy for the complete repository.
- Package policies MAY add stricter domain rules but MUST NOT weaken this file.
- `CLAUDE.md` and tool-specific files MUST point here rather than duplicate it.
- Historical implementation plans belong in repository history or issue
  tracking, not in the released source tree. Current checks MUST pass.

## Repository Structure

- The public root module MUST live at the repository root.
- Intentional optional or test modules MAY live in explicit nested directories.
- Commands MUST live under `cmd/`; private shared code MUST live under
  `internal/`; root automation MUST live under `scripts/`.
- Public module paths MUST match their repository-relative directories beneath
  the module path declared by the root `go.mod`.
- Every module MUST be declared in `modules.json`, and every package MUST be
  declared in `packages.json`.
- Independently releasable modules MUST retain independent `go.mod` files and
  directory-prefixed semantic-version tags.
- Cross-module dependencies MUST remain acyclic and MUST use public contracts.
- Permanent `replace` directives, sibling repositories, and absolute developer
  paths are forbidden in releasable modules.

## Design

- Prefer standard-library interfaces and explicit composition over hidden
  registration, global state, reflection-driven wiring, or service locators.
- Public APIs MUST make ownership, cancellation, retries, timeouts, resource
  limits, error semantics, and concurrency behavior observable.
- Interfaces SHOULD be defined by consumers and MUST remain narrowly scoped.
- Optional integrations SHOULD be adapters or nested modules, not mandatory
  dependencies of a core package.
- Breaking protocol or specification ambiguities MUST be documented as explicit
  decisions and covered by tests.

## Safety And Concurrency

- Shared mutable state MUST have one documented synchronization owner.
- Goroutines MUST have explicit lifetime, cancellation, and shutdown behavior.
  Fire-and-forget goroutines are forbidden. Leak tests are required only when
  the changed behavior can affect goroutine lifetime.
- Channels MUST have documented ownership and closure rules.
- Locks MUST NOT be held across caller callbacks, network IO, blocking channel
  operations, or unbounded work.
- Every external operation MUST accept or derive a bounded `context.Context`.
- Response bodies, files, rows, transactions, timers, tickers, connections,
  and temporary resources MUST be closed on every path.
- Integer conversions, sizes, offsets, recursion, decompression, and allocation
  from untrusted input MUST be bounded before allocation or conversion.
- Secrets and credentials MUST NOT appear in errors, logs, traces, snapshots,
  fixtures, mutation reports, or generated artifacts.

## Testing

- Behavioral changes MUST include meaningful tests before completion.
- Tests MUST assert outcomes, invariants, errors, cleanup, and state transitions;
  line execution without behavioral assertions is not acceptable coverage.
- Assurance MUST be proportional to the changed contract and its material
  risks:
  - **Tier A** covers documentation, metadata, registration, and generated
    documentation without runtime behavior. Validate the affected structure,
    links, examples, or generation and inspect the final diff.
  - **Tier B** covers internal behavior without a public contract change. Run a
    focused behavior test, affected package or module tests, applicable format
    and static checks, and one complete review. A reasonably bounded repository
    gate SHOULD run; unrelated expensive checks MAY remain scheduled.
  - **Tier C** covers public APIs, lifecycle, security, persistence, and
    concurrency. Require an observable regression or characterization test,
    focused behavior, API compatibility where applicable, directly affected
    package and integration tests, direct owned reverse consumers, and one
    independent complete-diff review.
  - **Tier D** covers public releases and ecosystem milestones. Bind immutable
    release or milestone inputs once and run only the relevant compatibility,
    composition, consumer, and aggregate checks.
- Race, fuzz, mutation, leak, performance, conformance, external-service,
  clean-consumer, release-rehearsal, and aggregate checks MUST run only when
  they exercise a material risk or the applicable Tier D boundary. They MUST
  NOT block an unrelated change merely because the check exists.
- Coverage and mutation results MUST be interpreted by behavioral risk. Fixed
  percentages are not universal completion requirements.
- Parsers and hostile boundaries SHOULD use bounded fuzzing and deterministic
  regressions when malformed-input risk is material.
- Concurrent code SHOULD use race and targeted stress or leak checks when the
  changed behavior can affect synchronization or lifetime.
- Specification claims SHOULD use pinned official fixtures or independent
  implementations when conformance is part of the changed contract.
- Performance checks MUST compare equivalent behavior and record enough context
  to reproduce the result when performance is a stated acceptance boundary.

## Required Commands

- `make inventory` validates repository and package manifests.
- `make check` runs the exact contract for every repository module.
- `make ci` runs the complete repository contract.
- Local commands and CI MUST use the same scripts and thresholds.
- Missing tools, services, packages, profiles, mutants, or reports MUST fail
  only when they are required by the applicable assurance tier and changed
  contract.
- NilAway is advisory; its findings MUST remain visible and tracked against a
  no-regression baseline.

## Evidence Validity And Reuse

- Evidence validity MUST be determined by the applicable behavior-affecting
  inputs, not by a commit hash, branch name, timestamp, or repository-history
  shape alone.
- A gate fingerprint MUST include the inputs that can affect that gate's
  result. Repository catalogs and aggregate manifests MUST use module-scoped
  projections so nested-module changes do not invalidate unrelated root-module
  evidence.
- Mutable progress ledgers, plans, review notes, and prose MUST NOT be hashed as
  runtime or release provenance. Evidence already bound to immutable source and
  a CI run MUST NOT acquire recursive hashes merely because it is referenced by
  another report.
- Commit hashes MAY be recorded for traceability, but MUST NOT be the sole
  evidence cache key or invalidation condition.
- Go toolchain revision metadata MAY be retained when a build, test, profile,
  or diagnostic artifact requires it. That metadata is descriptive only and
  MUST NOT make an otherwise identical gate-input fingerprint stale.
- A history rewrite, rebase, squash, reset, repository reinitialization,
  metadata-only commit, or unrelated-file change MUST NOT invalidate evidence
  when the complete gate-input fingerprint is unchanged.
- Agents MUST NOT rerun an expensive gate solely to attach an already proven
  result to a new `HEAD`.
- Agents MUST NOT restart the complete package matrix after a force-push,
  rebase, squash, reset, or other history-only change. Previously verified
  package results SHOULD be reused when their applicable inputs are unchanged.
- After a change, agents MUST rerun only the gates, modules, packages, and
  reverse dependants affected by the changed contract or material risk.
- Reused evidence MUST retain its immutable input identity and result. Reuse
  MUST NOT rewrite history to pretend the gate executed again.
- Long-running aggregate checks SHOULD checkpoint independently valid units
  when doing so materially improves recovery; routine local work MUST NOT add
  checkpoint or provenance ceremony without a demonstrated need.
- Evidence MUST NOT be reused when input identity cannot be proven. Missing,
  incomplete, manually asserted, or ambiguous fingerprints make the evidence
  stale and require execution.
- Mutation evidence MAY be reused only when the changed behavior and verifier
  inputs are unchanged; mutation records MUST NOT become a routine release or
  documentation gate.
- If repository tooling invalidates evidence solely because `HEAD` changed,
  agents MUST correct the evidence model instead of launching a repository-wide
  rerun with no changed gate inputs.

## CI And Workflows

- `.github/workflows/ci.yml` is the only owned GitHub Actions workflow.
- Package-local workflows MUST NOT be added.
- Actions and external tools MUST be pinned to immutable versions.
- Every selected module MUST have an attributable result. A durable evidence
  artifact is required only when the applicable Tier D boundary or an external
  audit contract requires one.
- The stable required job MUST fail for failed, cancelled, skipped, or missing
  module results.
- Required checks MUST NOT use `continue-on-error`, `|| true`, permissive
  thresholds, or warning substitutions.

## Dependencies And Supply Chain

- Dependencies MUST be necessary, maintained, license-compatible, and pinned to
  reviewed current versions.
- Standard-library functionality MUST NOT be wrapped merely to create an owned
  abstraction; wrappers require a stable policy or portability boundary.
- Generated code and vendored corpora MUST record source, version, checksum,
  license, generation command, and update procedure.
- Release checks MUST be selected by the released artifact and its material
  risks. Vulnerability, secret, and license checks apply to distributable code;
  SBOM, provenance, and clean-consumer checks apply only when the release or
  downstream adoption contract requires them.

## Documentation

- Public identifiers MUST have useful Go documentation describing semantics,
  invariants, ownership, errors, concurrency, and caveats where relevant.
- Comments MUST explain why a constraint or non-obvious implementation exists;
  they MUST NOT narrate obvious syntax.
- Every public module MUST provide a concise entry point and enough guidance to
  adopt its supported contract. Additional tutorials, examples, limitations,
  security notes, FAQs, and release notes SHOULD be added when the module's
  users or risks require them.
- Executable documentation and examples MUST compile when they are part of the
  supported contract; prose-only changes require structural validation only.

## Changelogs

- Every user-visible change MUST update the affected module `CHANGELOG.md` in
  the same commit.
- Entries MUST describe behavior and migration impact, not internal activity.
- Changes to multiple modules MUST update every affected changelog.
- Unreleased entries MUST NOT be silently rewritten or removed.
- Generated, dependency, security, compatibility, and deprecation changes
  require entries only when they alter user-visible behavior, adoption, or
  support expectations.

## Completion

- Run the narrowest affected gates during development and only the release
  gates required by the applicable assurance tier before declaring completion.
- Re-run affected gates after the final source, test, dependency, documentation,
  workflow, or generated-file change.
- Report exact commands and results. A skipped, blocked, stale, or warning-only
  applicable gate is not a pass; an inapplicable heavy gate is not a blocker.
