# Artel v2

A local, native project/task panel: Go API + CLI, SQLite storage and a standalone C++/Qt Quick desktop. Manual-only foundation development is in progress. The former Telegram/Mattermost personal bot has been removed; see [cleanup inventory](docs/legacy-cleanup.md).

## Current slice

- `arteld`: HTTP liveness at `127.0.0.1:7331/healthz` only.
- `artelctl --help` / `--version` (unsupported arguments exit 2).
- `artel`: native window explicitly displaying that it is a foundation skeleton.

No CRUD, persistence, authorization or API client yet. No demo data or claimed agent activity. Pi integration, MR orchestration, remote access and deployment are outside this slice. Closing the UI does not stop the independently launched daemon.

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
./build/bin/artelctl --help
./desktop/build/artel
```

Tests run Qt offscreen with a temporary HOME and no bot/provider environment; no running daemon is required. Standard window-manager close is currently the only UI action. Future slices add local auth, transactional storage, API, asynchronous client and manual screens in that order.

See [implementation plan](docs/plans/20261117-artel-v2-foundation-desktop.md) and [architecture direction](docs/architecture/20261117-artel-v2-direction.md). `docs/plans/completed/` contains historical bot plans, not active instructions. `docs/experiments/` is research, not a runtime dependency.
