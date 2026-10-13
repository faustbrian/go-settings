# Secret handling

[Documentation index](README.md) · [Repository entry](../README.md)

Mark secret-bearing keys with `WithSensitive`. Audit history omits their bytes,
export redacts them unless explicitly opted in, and `audit.Read` applies a
second redaction boundary. This affects newly classified writes, not historical
erasure or value encryption. Typed defaults can retain mutable references;
actor/reason metadata and custom error text are not automatically sanitized.

`EncryptionCodec` delegates authenticated encryption and all key ownership to
a caller-supplied `Cipher`. Increment its codec version when the envelope
changes. This package never stores or fetches keys and is not a secrets manager.
Never put values in IDs, actor/reason fields, logs, traces, or metric labels.

Classify settings that contain credentials as `ClassSecret` and configure
unavailable, stale, and expired reads to fail closed. Security-sensitive
settings may use a default only when it is explicitly deny-safe. A
`SnapshotStore` containing either class requires caller-owned authenticated
encryption; the snapshot wire format itself is not encryption.

Choose the next guide in the [documentation index](README.md)
or return to the [repository entry](../README.md) for installation and ownership.
