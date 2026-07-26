# Comparison

| Concern | Configuration | Settings | Feature flags |
| --- | --- | --- | --- |
| Lifecycle | Process boot | Runtime application data | Runtime rollout |
| Ownership | Deployment | Global/tenant/user/resource | Target context |
| Precedence | Files/env/sources | Explicit owner chain | Evaluation rules |
| Persistence | Deployment system | Durable provider | Flag platform |
| Audit | Deployment history | Actor/reason/version | Flag events |

Use `config` for process connections, `settings` for persisted
preferences, a feature-flag system for rollout, policy code for authorization,
and domain code for business rules.
