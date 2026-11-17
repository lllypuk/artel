# Observer full-stack recovery acceptance

Task 7 evidence, 2026-11-17. Local native Linux only; no working user session,
installed service, desktop settings, package installation or global Pi resources
were touched. Production code was not changed in this task.

## Executable fixture

`tests/e2e/observer_acceptance.py` owns an ephemeral arteld (Unix observer socket +
loopback owner endpoint), real CLI provisioning, a pinned Pi TUI in a PTY loading
the shipping extension, a loopback-only deterministic provider, and independent
owner HTTP assertions. The fixture launches the existing Qt acceptance executable
with `observerExternal` against this actual server. Main.qml/ObserverPanel and the
real C++ ObserverClient are loaded, not a replacement QML view or JSON stub.

Every Qt invocation checks the actual runtime ID and requested activity,
disposition/freshness against both the viewmodel and an independent HTTP oracle;
the production observer drawer/list is visible and navigation is unavailable
without agterm hints. It also asserts clean manual draft/uncertain state. Qt
warnings are failures. Multiple desktop processes establish UI restart coverage.

The shared test-only Pi provider refuses non-numeric-loopback URLs. An explicit
3-second tool/UI delay makes real working/waiting states observable across the
process boundary; the original SDK/PTY suites retain their default timings.

## Observed assertions

- CLI writes confirmed private config, real Pi registers a candidate, owner binds
  it explicitly; overlapping fixture tools report working, UI confirmation reports
  waiting_user, public settled/reconciliation reports idle. No initial prompt is
  started by the observer.
- A private Unix forwarding server discards the first accepted report ACK. The
  shipping bridge retries the same packet, and the actual daemon returns duplicate.
  A separate oracle duplicate preserves last_seen; an old-generation packet fails.
- Daemon SIGKILL and a four-second outage leave Pi running; daemon reopening keeps
  database/runtime IDs and initially returns stale/unknown. The original runtime
  catches up without another Pi process, prompt, registration or owner bind.
- Public TUI fork/new/reload each create a new candidate, keep the previous binding
  until an explicit owner rebind, and then report for the chosen runtime.
- Pi SIGKILL becomes stale after the production freshness interval; it does not
  clear the binding or complete a task. QML independently observes stale.
- Pruned superseded registrations remain retired across daemon restart. The current
  binding is retained. HTTP registration fills eight payload records; prune frees
  payloads but repeated registration/prune fills all 1024 identities. Each distinct
  capacity error is checked. The bound heartbeat still works at the identity limit.
- CLI provisions a new observer; it begins unbound and its Pi begins candidate.
  Explicit bind precedes revocation of the old credential. No binding is copied.
- Replacing the database at the same address, and giving it the same principal ID
  and verifier, still leaves zero registered runtimes: the database UUID pin, not
  merely a missing credential, prevents telemetry. The original DB is then restored
  for a fresh candidate; revoking its live token prevents API access and does not
  trigger credential/runtime recreation.
- A populated manual snapshot (two tasks, ordered queue, project/task/queue versions,
  database identity and domain event cursor) remains byte-equivalent as decoded JSON
  throughout telemetry, recovery, quotas and revocation.

## Isolation, bounds and negative acceptance

Child environments are constructed from a whitelist, with private HOME, XDG and Pi
agent directories. No developer credentials, settings, providers, extensions or
skills are imported. Pi is offline and selects only the deterministic loopback
provider; fixture tools contain fictional output. Provider payload and captured
PTY/CLI/Qt output are checked for observer-token leakage. No token is in argv.
PTY output is capped at 1 MiB. HTTP, process startup, Qt assertions and all polls
have deadlines. The runner adds a 240-second total watchdog and a 70-second
negative watchdog. Linux parent-death SIGKILL is armed in an exec wrapper for every
owned CLI/daemon/Pi/Qt child, with a parent-PID race check; finally blocks kill/reap
owned children, close PTYs and shut down/join provider/proxy threads.

The intentional-failure subprocess reaches actual Pi registration and QML before
raising its designated assertion. Its supervisor requires exit 1 and that exact
assertion, an empty temporary-resource directory, and absence of every recorded
owned process PID (including CLI and Qt). No mandatory test is skipped; invoking
the Qt observer slot without its external fixture fails explicitly.

## Commands/results

Final changed-code tree:

- `make check` — PASS (lint, Python/Go/Qt tests, Go race, daemon/CLI/desktop build,
  existing native manual acceptance, new full-stack observer acceptance and both
  intentional-failure cleanup probes).
- `make observer-check` — PASS (pinned dependency preflight, TypeScript typecheck,
  unit/public-hook tests, four real SDK/PTY process tests, OpenAPI contract check;
  zero skips).
- `for n in 1 2 3; do timeout --signal=TERM --kill-after=10s 240s python3 -B tests/e2e/observer_acceptance.py; done`
  — three consecutive PASS after the final code changes, in addition to make check.
- `git diff --check` — PASS.

Versions: Go 1.26.3, Qt 6.12.0, clang-format 23.1.3, golangci-lint 2.10.1,
Node 22.23.2, pinned Pi 1.5.2. No dependency or timeout was relaxed to pass.

## Limits / handoff

Process crash/restart is not proof of power-loss durability. Offscreen production
QML plus a real PTY is not an Omarchy/Wayland foreground test. This fixture proves
explicit manual re-provisioning after payload/identity limits; the separate store
suite covers the 256-observer ceiling and concurrency/fake-clock edge cases.
No live owner Pi, agterm session switching, remote CI, package payload/standalone
artifact or installed no-Pi baseline is claimed here. Task 8 remains open for
packaging, stable documentation links and exact-tree delivery acceptance.
