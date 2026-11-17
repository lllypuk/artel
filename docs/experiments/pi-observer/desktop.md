# Observer desktop evidence (Task 5)

## Implementation

- `desktop/src/observerclient.{h,cpp}` owns a separate network manager, bounded
  projection, latest-state model, polling timer and serialized observer mutation.
  It shares only the C++ owner transport credential with `ApiClient`; it does not
  call manual save/retry/discard or alter snapshot/event cursors.
- `ObserverPanel.qml`, reached from the main window's Pi observations button,
  shows all candidates/runtimes or only the selected project's bound runtime.
  IDs, versions, provider/model, last-seen, activity and freshness are distinct.
  Bind/rebind, unbind, revoke and prune require confirmation. Provisioning is a
  CLI instruction, never a credential field. All dynamic text is plain text.
- Project selection comes from `Panel` after its existing dirty/pending/uncertain
  guards. Confirmation captures database/project/runtime/observer/version and
  rechecks them immediately before the independent CAS mutation. Archived or
  absent projects cannot be bound. New selection/database invalidates old reads
  and confirmations. Mutation responses never update rows directly: a new owner
  projection is authoritative, including after conflicts and lost ACKs.
- Poll every 2 seconds only while the drawer/window is visible and not minimized;
  one read in flight, 2-second request deadline, capped 2–30-second failure
  backoff, 32-MiB response cap. Failed refresh displays unknown/offline rather
  than preserving a fresh badge. This does not modify server freshness rules.
- DTO validation/whitelist excludes unknown fields and never forwards server
  error bodies, credentials, cwd, tool data or raw registration JSON to QML.
  RU/EN strings and status retranslation use the existing read-only appearance
  adapter. Native Qt buttons support keyboard actions; scope has an accessible
  label. No desktop settings are written.

## Automated evidence

Final commands passed:

- `make lint test build`: Go tests and lint, clang-format/QML checks, Python
  regressions; Qt ArtelTests 28 passed / 0 failed / 0 skipped, ClientTests 32
  passed / 0 failed / 0 skipped; Go CLI/daemon and Qt desktop builds.
- `make observer-check`: pinned dependency preflight, TypeScript checks, unit
  suite, public SDK/PTY probe and real extension process suite, OpenAPI test.
  No mandatory test skipped.
- `git diff --check` and focused diff/privacy review.
- Impeccable mechanical detector on Main.qml and ObserverPanel.qml: `[]`.

Versions observed by preflight: Go 1.26.3, Qt 6.12.0, clang-format 23.1.3,
  golangci-lint 2.10.1. Observer runtime dependencies remain those pinned in
  the extension package (Pi 1.5.2).

New tests exercise production QML keyboard binding and confirmations, two
runtime candidates, project-only filtering, CAS conflict without silent retry,
rebind/supersession, unbind, revoke, prune, archived/missing project, independent
manual dirty/pending/uncertain saves, stale/offline, malformed/secret-bearing
responses, one-read-in-flight, hidden polling cancellation, delayed read and
owner database replacement fencing. Fixtures use ephemeral loopback HTTP and
private temporary credential files; their data and identifiers are fictional.
Existing manual-panel and client regressions continue to pass.

## Visual evidence and limits

The production-QML test was also run separately with `env -i`, a temporary HOME
and 0700 XDG_RUNTIME_DIR, `QT_QPA_PLATFORM=offscreen` and software rendering.
Only `observerProductionQmlPreservesDraftAndShowsCandidates` was selected, with
`ARTEL_OBSERVER_SCREENSHOT=/tmp/artel-observers-task5.png`; its fixture cleans
its own resources. The optional screenshot variable is test-binary-only, not a
new desktop runtime setting. Native popup exit transitions finish before capture.

Inspected English 1280×820 and Russian 1000×680 screenshots: readable identity
and activity/freshness rows, explicit controls, wrapped Russian actions and
scrollable remaining rows; no real paths, agents or credentials. Images remain
local temporary artifacts, not distribution payloads.

This is an owner HTTP fixture plus production QML, not evidence of the complete
Pi→daemon→QML acceptance. Task 7 still owns that full-stack oracle/recovery gate.
Navigation is not implemented here (Task 6). No normal Wayland/desktop session,
real owner agent, remote CI, installation, service or global configuration was
changed or claimed tested.
