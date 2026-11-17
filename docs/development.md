# Developing Artel v2

Artel is a local, manual-only prototype, not an agent orchestrator. Read
[CLAUDE.md](../CLAUDE.md) and the [foundation plan](plans/20261117-artel-v2-foundation-desktop.md)
before changing code. Architecture and experiments describe future work, not
permission to implement Pi, Product roles, chat, MR verification or remote access.
Historical completed bot plans are not active instructions.

## Toolchain and gates

Use Go 1.26.3, golangci-lint 2.10.1, C++17, Qt 6.8+ (base/declarative,
Quick Controls, Qt Test and QML tools), clang-format, make and Python 3.
`make preflight` checks tools without installing anything. Set `GOLANGCI_LINT`
to a private pinned binary if needed; `QMAKE`/`QT_BINS` override Qt discovery.
Do not change the host toolchain or desktop settings to make a check pass.

- After each code change: `make lint test`.
- Task minimum: `make lint test build`, plus task-specific gates.
- Full acceptance: `make check` (lint, Go/Qt tests, race, build, native e2e).
- Arch delivery: `make package-check`; requires base-devel, Go, Qt
  base/declarative/SVG/Wayland, Python, desktop-file-utils, systemd and libarchive.
  Run unprivileged; no installation or service activation occurs.
- Before commit: `git diff --check`, content review and secret/privacy review.

`make test` includes documentation/CLI smoke tests. Tests build their own daemon
and CLI and execute the marked examples below against private temporary paths.
`make test-e2e` drives production QML with a real daemon; it includes an intentional
failure/cleanup probe. Qt tests are offscreen/software-rendered, with bounded
watchdogs. No running API, real credentials or production data are required.

## Isolated CLI example

Build with `make build`. To try a disposable instance, set these variables in a
shell (not in a shared or production directory):

```sh
ARTEL_TMP=$(mktemp -d)
chmod 700 "$ARTEL_TMP"
export HOME="$ARTEL_TMP/home"
export XDG_CONFIG_HOME="$HOME/config" XDG_DATA_HOME="$HOME/data"
mkdir -m 700 "$HOME"
export ARTELD="$PWD/build/bin/arteld" ARTELCTL="$PWD/build/bin/artelctl"
export ARTEL_DATA="$ARTEL_TMP/data" ARTEL_TOKEN="$ARTEL_TMP/private/owner.token"
```

Run the first command below in that shell. It stays in the foreground and prints
`arteld: listening 127.0.0.1:PORT` without the credential. In a second shell with
the same exported variables, set `ARTEL_ENDPOINT=http://127.0.0.1:PORT` using the
reported port, then run the two CLI commands. The test harness supplies these
variables and captures the actual ephemeral port automatically.

<!-- smoke:temporary-cli -->
```sh
"$ARTELD" --data-dir "$ARTEL_DATA" --token-file "$ARTEL_TOKEN" --listen 127.0.0.1:0
"$ARTELCTL" status --json --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
"$ARTELCTL" snapshot --json --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
```

Expected status: `{"status":"ok"}`. A fresh snapshot contains an `instance_id`,
`event_seq: 0` and empty `projects`, `tasks`, `queues` arrays. The CLI reads the
credential itself; never use token contents in arguments, logs or URLs. Missing
credentials make snapshot fail nonzero; public status still works. Unsupported
arguments exit 2; transport/auth/protocol failures exit 1.

Optionally launch the desktop in the second shell:

```sh
./desktop/build/artel --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
```

UI launch does not start a server, and closing it does not stop one. Stop the foreground daemon with Ctrl+C
before removing only this example's temporary directory. Never use broad pkill,
remove an existing data directory, or read another installation's token.

## Implementation map and invariants

- `internal/local`: private XDG paths/overrides, credential checks and lifetime
  data-directory lock. Only numeric IPv4 loopback is accepted. No remote mode.
- `internal/domain`, `internal/app`, `internal/store`: validation and transactional
  SQLite ownership. Immediate transactions commit mutation, event and receipt
  together. CAS protects task/project/queue versions; no-op does not append events.
  WAL/FULL and versioned migrations preserve confirmed state across restarts;
  incompatible databases fail instead of being recreated. No legacy DB migration.
- `internal/api`, `api/openapi.yaml`: authenticated CRUD/snapshot/history/SSE.
  Browser Origins, unsafe Hosts, unknown fields and oversized payloads are rejected.
  Pinned kin-openapi tests check all operations. Database identity scopes cursors.
- `desktop/src`: asynchronous C++ networking, independent drafts and models;
  QML owns layout/actions, never credentials, SQL or shell. Appearance adapters
  only read/watch settings. English fallback works without Omarchy files.
- `tests/e2e`: production-QML workflow, independent HTTP oracle, restart, CAS races,
  disconnected recovery and byte-identical lost-response retry. All fixtures are
  temporary; screenshots use fictional data only.

Queue replacement is atomic and only contains queued/backlog tasks of one active
project. Only the first queued task can start; at most one task is in progress.
Archive requires no queued/in-progress tasks; archived objects are read-only.
Root paths are canonical and unique among active projects; no Git/scanning runs.
Manual completion means only the owner marked it complete, never verified work.

Offline is cached-in-memory read-only, with no hidden mutation queue. Drafts
survive refresh/conflict in the window but are not persisted across closing it.
A 409 requires explicit version adoption after comparison. An unknown result
retries the original key/payload; editing does not create a duplicate retry.
Database replacement resets cursors and blocks replay of an old draft/receipt.
See README for shortcuts, wire limits and audit pagination semantics.

## Delivery and remaining acceptance

The Arch package contains three executables, desktop entry/icon, user service,
license and attribution, not credentials or data. Package-check snapshots current
source rather than stale HEAD and independently inspects payload/modes/ownership.
The user service is foreground and independent of UI; no install/start hooks exist.

CI defines Go and unprivileged Arch full-stack/package jobs. Local success or
workflow YAML is not evidence of remote CI; any later publication needs exact-HEAD
CI. This plan does not install packages, enable a service, merge, push main or deploy.
Normal-session Omarchy desktop smoke and installation are owner decisions, not
claims made by offscreen tests. No macOS support, remote access, agents, scheduler,
Product workflow, MR checks or operational start/stop actions are implemented.
Events/receipts are not pruned; snapshots are complete with a 32 MiB limit, and
mutations currently load/persist the full store snapshot: this is not a large-scale
or hardware-failure durability guarantee. External legacy installations/volumes
remain untouched; see [cleanup inventory](legacy-cleanup.md).
