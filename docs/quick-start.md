# Quick start

Define keys once, register them during application assembly, and inject a
provider into request or job code. Never place precedence in mutable globals.

```go
registry := settings.NewRegistry()
_ = registry.RegisterNamespace(settings.NewNamespace("billing", "Billing"))
dueDays := settings.NewKey("billing", "invoice.due_days", settings.IntCodec{},
    settings.WithDefault[int64](14),
    settings.WithValidation(func(value int64) error {
        if value < 0 || value > 365 { return errors.New("outside 0..365") }
        return nil
    }),
)
if err := registry.Register(dueDays); err != nil { return err }
_, err := settings.Set(ctx, provider, settings.Tenant("acme"), dueDays, 30,
    settings.Change{Actor: "user:42", Reason: "invoice policy update"})
```

Capture a snapshot at a request or job boundary when repeated reads must agree:

```go
snapshot, err := settings.Capture(ctx, provider, chain, dueDays)
effective, err := settings.ResolveSnapshot(snapshot, dueDays, chain)
```
