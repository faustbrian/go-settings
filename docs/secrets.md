# Secret handling

Mark secret-bearing keys with `WithSensitive`. Audit history omits their bytes,
export redacts them unless explicitly opted in, and `audit.Read` applies a
second redaction boundary.

`EncryptionCodec` delegates authenticated encryption and all key ownership to
a caller-supplied `Cipher`. Increment its codec version when the envelope
changes. This package never stores or fetches keys and is not a secrets manager.
Never put values in IDs, actor/reason fields, logs, traces, or metric labels.

Classify settings that contain credentials as `ClassSecret` and configure
unavailable, stale, and expired reads to fail closed. Security-sensitive
settings may use a default only when it is explicitly deny-safe. A
`SnapshotStore` containing either class requires caller-owned authenticated
encryption; the snapshot wire format itself is not encryption.
