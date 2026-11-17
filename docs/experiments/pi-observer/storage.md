# Observer storage validation (Task 2)

Implemented separately from manual domain persistence in `internal/store/observer*.go`
and `observer_schema.sql`. New databases and populated v1 databases migrate to
user_version 2 inside one immediate transaction. The immutable historical v1
fixture is `internal/store/testdata/v1.sql`; migration tests do not reconstruct it
from the current schema. Database UUID, projects, queued tasks, queue ordering,
events and exact manual receipt keys/bodies survive upgrade and reopening.
Injected upgrade DDL failure rolls back both tables and version; malformed,
foreign-application, incomplete and future-version databases are refused.

## Storage invariants exercised

- Authentication, registration identity/quota checks, binding CAS, generation
  fencing, revocation and retirement checks execute inside SQLite transactions.
  Independent Store connections exercise concurrent registration quota, binding
  CAS and identical report races; bind/revoke and prune/register/report races
  have explicit result/state assertions.
- Owner receipts are principal-scoped and independent of domain receipt keys.
  They retain only a canonical request hash and public result, not verifier or
  metadata. Owner audit retains the latest 256 entries per observer. Heartbeats
  use neither receipt namespace nor audit.
- The 10,000-report test ends with one runtime/identity and unchanged counts:
  one domain event, one domain receipt, two observer owner receipts and two audit
  entries (provision + bind). The complete manual snapshot is unchanged. Duplicate
  or out-of-order packets do not consume state; same seq/different body conflicts.
- The real registration/prune loop reaches 1024 lifetime identities (one bound
  payload, 1023 retirement markers). Further registration fails with identity
  capacity; bound heartbeat and immutable registration replay continue working.
  Payload capacity is separately tested at eight; concurrent registration cannot
  allocate a ninth. The global 256-observer quota includes revoked observers.
- Retirement survives daemon-store reopen and repeated prune. Payload metadata
  is removed, only the principal/runtime key and retired bit remain. Bound stopped
  payloads cannot be pruned. Faults in registration, report, binding, provisioning,
  receipt, audit and retirement writes leave no partial successful mutation.
- Freshness uses process-local time.Time values with production monotonic time;
  fake-clock tests cover the exact 20-second boundary, duplicate non-renewal,
  generation reset, unrelated candidates and prune preserving bound freshness.
  Reopening starts with an empty cache: replaying the last report remains stale;
  a new accepted sequence restores freshness. Wall last_seen is display-only.
- Whitelisted DTOs validate UUIDs, strings/control characters, counters, enums,
  metadata size and terminal hints. Projection contains no verifier or seq hash.

## Commands and results

- `make lint test`: PASS (0 lint issues; Go suites; 12 Python checks;
  production-QML ArtelTests 22/22 and ClientTests 32/32, no skips).
- `make test-race`: PASS, including independent-connection concurrency tests.
- `make build`: PASS for daemon, CLI and desktop.
- `make observer-check`: PASS (Pi 1.5.2 / Node 22.23.2; TypeScript lint/typecheck,
  unit test, two private SDK/PTY probe tests, observer OpenAPI validation).
- `git diff --check`: PASS.

Versions: Go 1.26.3, golangci-lint 2.10.1 (private existing installation),
Qt 6.12.0, clang-format 23.1.3. The combined final command reached PASS for lint,
test, race and build, then the harness's 240-second command deadline interrupted
observer preflight. `make observer-check` was rerun separately and passed fully.
No checks were weakened or skipped. Earlier standalone full gate runs also passed.

## Boundaries

This is a storage-only slice: no observer listener, CLI provisioning, production
extension, owner observation UI or rate-limiting middleware is exposed yet.
Those remain subsequent tasks. Public observer methods accept a SHA-256 verifier
from the future isolated middleware, not the owner token. Monotonic caches belong
to each Store lifetime, matching the daemon's one-store lifetime; SQLite remains
the cross-connection authority for mutations. Owner receipts remain durable for
lost-response replay; only owner audit is pruned. No heartbeat history is retained.
Process-level HTTP restart/recovery and full-stack acceptance are not claimed by
these storage tests. No package was installed, service activated, host settings
changed, real credentials used, or live database opened.
