# Scopes and precedence

Chains are ordered highest to lowest. There is no implicit global fallback.

```go
chain := settings.Chain(
    settings.User(userID), settings.Resource(projectID),
    settings.Tenant(tenantID), settings.Global(),
)
```

`Set` stores a value, including zero. `Clear` stops fallback. `Inherit` removes
an override and resumes fallback. No record yields a declared default or
`StatusMissing`; malformed bytes yield `StatusInvalid` without exposing data.
Duplicate owners and empty chains are rejected. Scope identifiers are bounded.
