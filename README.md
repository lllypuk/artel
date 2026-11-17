# Artel v2

A local, native project/task panel: Go API + CLI, SQLite storage and a standalone C++/Qt Quick desktop. Manual-only foundation development is in progress. The former Telegram/Mattermost personal bot has been removed; see [cleanup inventory](docs/legacy-cleanup.md).

## Current slice

- `arteld`: loopback HTTP with public `/healthz`, private owner credential bootstrap, data-directory lock and graceful SIGINT/SIGTERM shutdown. `/v1/*` requires Bearer auth; domain routes currently return 501.
- `artelctl --help` / `--version`, `status --json`, and `snapshot --json` (snapshot awaits the domain API; unsupported arguments exit 2).
- `artel`: native window explicitly displaying that it is a foundation skeleton.

No CRUD, SQLite persistence or desktop API client yet. No demo data or claimed agent activity. Pi integration, MR orchestration, remote access and deployment are outside this slice. Closing the UI does not stop the independently launched daemon.

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
./desktop/build/artel
```

The daemon defaults to `${XDG_DATA_HOME:-$HOME/.local/share}/artel/v2` and `${XDG_CONFIG_HOME:-$HOME/.config}/artel/local-owner.token`. First startup creates a 32-byte random owner secret (64 hex characters plus newline), never replaces an existing credential, and refuses unsafe ownership/permissions or symlinks. Private directories must be 0700 and the token file 0600; existing permissions are not silently repaired. Do not print or copy the token into command arguments, URLs or logs.

Independent absolute `arteld --data-dir PATH --token-file PATH --listen 127.0.0.1:PORT` overrides support isolated instances (`PORT=0` reports the actual ephemeral address). CLI supports `status|snapshot --json --endpoint http://127.0.0.1:PORT --token-file PATH`; status reads public liveness, snapshot reads the private credential internally. Requests have bounded timeouts, no proxies/redirects, and nonzero exits on failures. Browser Origin requests and mismatched Host headers are rejected. The planned domain contract is in [api/openapi.yaml](api/openapi.yaml).

Tests run Qt offscreen and daemon processes with temporary paths/HOME and ephemeral ports; no running daemon is required. Standard window-manager close is currently the only UI action. Future slices add transactional storage, domain API, asynchronous desktop client and manual screens in that order.

See [implementation plan](docs/plans/20261117-artel-v2-foundation-desktop.md) and [architecture direction](docs/architecture/20261117-artel-v2-direction.md). `docs/plans/completed/` contains historical bot plans, not active instructions. `docs/experiments/` is research, not a runtime dependency.
