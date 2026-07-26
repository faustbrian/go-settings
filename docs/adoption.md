# Adoption guide

1. Exclude boot configuration, feature rollouts, permissions, and rules.
2. Assign stable namespaces, key IDs, and codec versions.
3. Define validation, docs, defaults, display labels, and sensitivity.
4. Write exact precedence chains for each use case.
5. Deploy PostgreSQL schema and dual-read legacy data where needed.
6. Import with an application schema ID and verify provenance.
7. Move writes to compare-and-set with audit metadata.
8. Add Valkey only after choosing staleness and outage policy.
9. Capture request/job snapshots where repeated reads must agree.
10. Retire legacy storage after rollback and recovery drills.
