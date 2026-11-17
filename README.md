# Artel v2

A local, native project/task panel: Go API + CLI, SQLite storage and a standalone C++/Qt Quick desktop. Manual-only foundation development is in progress. The former Telegram/Mattermost personal bot has been removed; see [cleanup inventory](docs/legacy-cleanup.md).

## Current slice

- `arteld`: loopback HTTP with public `/healthz`, private owner credential bootstrap, data-directory lock, versioned SQLite storage (`artel.sqlite`) and graceful SIGINT/SIGTERM shutdown. `/v1/*` requires Bearer auth and exposes projects, tasks, queues, consistent snapshots, paginated audit history and replayable SSE.
- Internal domain/app/store: manual transitions, queue CAS, archive rules, atomic audit events and idempotency receipts; exposed over HTTP and exercised by isolated integration/concurrency tests.
- `artelctl --help` / `--version`, `status --json`, and `snapshot --json` (unsupported arguments exit 2).
- `artel`: native window explicitly displaying that it is a foundation skeleton.

Manual CRUD is available through the API; there is no desktop API client yet. No demo data or claimed agent activity. Pi integration, MR orchestration, remote access and deployment are outside this slice. Closing the UI does not stop the independently launched daemon.

## Build and checks

Dependencies: Go **1.26.3**, golangci-lint **2.10.1**, Qt **6.8+** (base/declarative, Quick Controls, Qt Test, qmllint and qmlformat), C++17 compiler, make and clang-format. Preflight diagnoses missing tools; it never installs host packages.

```sh
make lint test build
make test-race
```

If the host linter differs, install the pinned version into a private tools directory and set `GOLANGCI_LINT=/path/to/golangci-lint`. Qt tools are resolved via `qmake6 -query QT_INSTALL_BINS`; overrides: `QMAKE`, `QT_BINS`. `JOBS` defaults to 2.

Run separately (no service is enabled or started by build scripts):

```sh
./build/bin/arteld
./build/bin/artelctl status --json
./build/bin/artelctl snapshot --json
./desktop/build/artel
```

The daemon defaults to `${XDG_DATA_HOME:-$HOME/.local/share}/artel/v2` and `${XDG_CONFIG_HOME:-$HOME/.config}/artel/local-owner.token`. First startup creates a 32-byte random owner secret (64 hex characters plus newline), never replaces an existing credential, and refuses unsafe ownership/permissions or symlinks. Private directories must be 0700 and the token file 0600; existing permissions are not silently repaired. Do not print or copy the token into command arguments, URLs or logs.

Independent absolute `arteld --data-dir PATH --token-file PATH --listen 127.0.0.1:PORT` overrides support isolated instances (`PORT=0` reports the actual ephemeral address). CLI supports `status|snapshot --json --endpoint http://127.0.0.1:PORT --token-file PATH`; status reads public liveness, snapshot reads the private credential internally. Requests have bounded timeouts, no proxies/redirects, and nonzero exits on failures. Browser Origin requests and mismatched Host headers are rejected. The validated domain contract is in [api/openapi.yaml](api/openapi.yaml).

Tests run Qt offscreen and daemon processes with temporary paths/HOME and ephemeral ports; no running daemon is required. Standard window-manager close is currently the only UI action. Future slices add the asynchronous desktop client and manual screens. SQLite uses WAL, foreign keys, FULL synchronous commits and a persistent database instance UUID; incompatible databases fail startup without recreation. Do not point the daemon at a legacy bot database.

Mutations require a UUID `Idempotency-Key`; edits/transitions require expected versions, and leaving queued also requires the queue version. Retry uncertain outcomes with the same key/payload. Snapshot queues are an array of `{project_id,queue_version,task_ids}` records. Audit `before`/`after` objects map `object-kind:UUID` keys to changed fields, including versions. History scans at most `limit` events (1–500, default 100) and advances `next_cursor` even when filters match nothing; task filters include queue events that change that task. SSE uses `instance_id` and `after` from a snapshot, sends compact invalidations, polls the persisted log every 100 ms and heartbeats every 15 seconds. Each stream buffers at most 100 events and disconnects writes blocked for 2 seconds; reconnect with a new snapshot or replay cursor. Wrong-instance/future cursors return `409 reset_required` before streaming. Snapshots exceeding 32 MiB fail with 413, never partial data. The OpenAPI 3.0.3 document is checked using the pinned Go kin-openapi validator (no downloaded CLI validator).

See [implementation plan](docs/plans/20261117-artel-v2-foundation-desktop.md) and [architecture direction](docs/architecture/20261117-artel-v2-direction.md). `docs/plans/completed/` contains historical bot plans, not active instructions. `docs/experiments/` is research, not a runtime dependency.
