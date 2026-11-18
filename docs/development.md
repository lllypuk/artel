# Developing Artel v2

Artel is a local manual panel with opt-in Pi observation, not an agent orchestrator.
Read [CLAUDE.md](../CLAUDE.md) and this stable development contract before changing
code. Architecture and experiments describe wider future work, not permission to
implement delivery, Product roles, chat, MR verification or remote access.
Historical completed bot plans are not active instructions.

## Toolchain and gates

Use Go 1.26.3, golangci-lint 2.10.1, C++17, Qt 6.8+ (base/declarative,
Quick Controls, Qt Test and QML tools), clang-format, make and Python 3.
`make preflight` checks tools without installing anything. Set `GOLANGCI_LINT`
to a private pinned binary if needed; `QMAKE`/`QT_BINS` override Qt discovery.
Do not change the host toolchain or desktop settings to make a check pass.
Observer development additionally requires Node >=22.23.2, npm, pinned Pi 1.5.2
and Linux Python PTY support. Install dependencies only in this workspace:
`npm --prefix integrations/pi-observer ci --ignore-scripts`. The lockfile pins
Pi, TypeScript, esbuild and transitive dependencies. `make observer-build` checks
versions and bundles Artel code, leaving only Node built-ins and the public Pi
API external. No runtime Pi/Node dependency is added to daemon/CLI/desktop.

- After each code change: `make lint test`.
- Task minimum: `make lint test build`, plus task-specific gates.
- Full acceptance: `make check` (lint, Go/Qt tests, race, build, observer lint/typecheck/
  unit/public SDK/PTY and production-QML full-stack recovery acceptance).
- Arch delivery: `make package-check`; requires base-devel, Go, Qt
  base/declarative/SVG/Wayland, Node/npm, Python, desktop-file-utils, systemd and libarchive.
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

## Opt-in observer operations

Keep the isolated variables above. Provisioning may precede listener activation.
To accept reports, restart only your temporary daemon with the same arguments plus
`--observer-socket "$ARTEL_SOCKET"`. Use an absolute socket path in a private 0700
directory (socket 0600); a short path avoids Unix path-length limits.
Set `ARTEL_SOCKET="$ARTEL_TMP/observer/observer.sock"` and
`ARTEL_CONFIG="$ARTEL_TMP/observer/config.json"`.

<!-- smoke:observer-cli -->
```sh
"$ARTELCTL" observer provision --name "Fictional observer" --observer-socket "$ARTEL_SOCKET" --output "$ARTEL_CONFIG" --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
"$ARTELCTL" observer provision --resume "$ARTEL_CONFIG" --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
"$ARTELCTL" observer list --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
```

Provision persists a private pending config/token before owner PUT. A lost response
or interrupted confirmation is recovered by `--resume` with the saved payload/key;
never regenerate a credential to retry. Only confirmed config is usable. Never
print its token or put it in argv, prompts, provider settings or screenshots.
JSON output is public IDs/results only; syntax errors exit 2, API/auth errors 1.

Explicitly load the installed artifact in a separately installed supported Pi:
`ARTEL_OBSERVER_CONFIG="$ARTEL_CONFIG" pi -e /usr/share/artel/pi-observer/index.mjs`.
From source use `make observer-build` and the absolute path to
`integrations/pi-observer/dist/index.mjs`, or copy the three dist files elsewhere.
Do not place it in `.pi/extensions` or global settings. No config means inert;
unsupported version/capabilities or unsafe/pending config disables observation.

Use the list JSON to select full observer/runtime/project UUIDs and current observer
version. `artelctl observer bind --id UUID --runtime-id UUID --project-id UUID
--expected-version N` selects a candidate; repeat with another candidate/project
for explicit rebind. `unbind`, `revoke`, `prune` take `--id UUID --expected-version N`.
All accept the owner endpoint/token-file options above and `--idempotency-key UUID`;
retain the same key/body for uncertain mutation retries. A conflict requires fresh
list/version and an explicit decision, not silent retry. Desktop offers the same
controls without a credential textbox and keeps manual drafts independent.

Identity separates database UUID, credential-scoped observer UUID, Pi session UUID,
fresh runtime UUID and server generation. New/reload/resume/fork requires a new
explicit binding; daemon reconnect retains runtime. Reports never assign writers,
complete tasks, send messages, verify MRs or deliver work. Owner-only projection
polls every 2 seconds while visible; reports heartbeat every 5 seconds, coalesce
at least 250 ms, one in-flight/latest pending, request deadline 2 seconds and
reconnect backoff 1–30 seconds. Freshness expires at 20 seconds; daemon restart is
stale until a new report. Activity is not business completion or crash detection
without delay. Observer state has no domain event/SSE cursor effects.

Credentials are per-observer SHA-256 verifiers server-side; observer tokens cannot
access owner/task APIs. Scoped token is not a sandbox against arbitrary same-UID
code reading files. Metadata is limited to IDs, cwd, versions, provider/model,
activity/tool count and typed terminal UUID hints; no prompt/response, tool names/
arguments/output, UI prompt text, history, environment or provider credentials.
Database replacement, auth, retired runtime and capacity failures stop transmission
for owner action, never automatic provisioning or binding.

Limits: 256 observers including revoked, 8 full runtimes and 1024 lifetime identities
per credential, 256 audit entries per observer. Prune removes only eligible unbound/
superseded/stopped payloads, never the bound runtime/manual history; permanent
retirement markers prevent resurrection and still consume the identity quota.
At identity exhaustion explicitly provision/connect/bind a new observer then revoke
the old one; no binding transfer or automatic GC. At 256 observers stop for owner
decision. Revocation immediately blocks all observer requests.

Navigation is a best-effort local adapter for the fail-closed agtermctl 0.40.0
dialect. Desktop `--agterm-socket PATH` (or its own AGTERM_SOCKET) supplies the local
socket, never metadata. The explicit “Open claimed agterm session” action checks
all open window trees and complete session/pane UUIDs, with bounded output/deadline;
unknown/unrealized/ambiguous targets refuse navigation. Hints can be inherited and
are unverified, not proof of process location. Session-only selection promises
neither exact pane focus nor Wayland foreground activation. No shell, restore,
restart, message delivery or automatic agent launch. Observation works without agterm.

Contracts: [owner API](../api/openapi.yaml), [observer API](../api/observer-openapi.yaml).
Implementation evidence lives in [observer experiments](experiments/pi-observer/README.md)
and its storage/http/extension/desktop/navigation/recovery records. Completed plans
are historical evidence, not active runtime instructions; this stable page remains
the entry point when ralphex archives a plan.

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
If an old request is uncertain after replacement, the owner can explicitly
acknowledge abandonment without resending it. Its draft remains available to
copy, then discard before starting a new edit; abandonment does not prove that
the old database rejected the request. This action is unavailable for a pending
request or uncertainty within the same database.
When text changes during a successful save or retry, the retained editor adopts
that response's object ID and version without overwriting the newer text. Its next
save updates the confirmed object rather than creating another one.
See README for shortcuts, wire limits and audit pagination semantics.

## Delivery and remaining acceptance

The Arch package contains three executables, desktop entry/icon, user service,
license/attribution and inert `/usr/share/artel/pi-observer/{index.mjs,package.json,README.md}`,
not credentials or data. Node and separately installed Pi 1.5.2 are optional runtime
requirements only for explicitly loading the observer. No Pi auto-discovery files
are installed. Package-check runs staged daemon/CLI/desktop with an empty PATH
(no Pi/Node), upgrades an isolated v1 database, and loads the extracted bundle
through the pinned public SDK with a deterministic local provider. Package-check snapshots current
source rather than stale HEAD and independently inspects payload/modes/ownership.
The user service is foreground and independent of UI; no install/start hooks exist.

CI keeps the Go lint/test/race/build job enabled. By explicit owner decision on
2026-11-18, the remote `native-full-stack` job is temporarily disabled for the
first local-use pilot; its steps are retained, not treated as passing. PR #8's
initial CI passed all tests/full-stack recovery but package-check failed because
`systemd-analyze --user verify` could not resolve RuntimeDirectory inside the Arch
container. Re-enable the job after providing an isolated user runtime for unit
verification and proving its complete check/package-check path in CI. Do not
remove or skip unit verification merely to obtain green CI.

Local `make check` and `make package-check` remain mandatory and unchanged.
Publication/merge still requires exact-HEAD results from the enabled Go CI;
skipped remote native checks are an explicitly accepted pilot limitation, not
proof of remote package validation. Local success/workflow YAML never substitute
for actual CI. Installation, service activation and deployment require separate
owner decisions.
Normal-session Omarchy desktop smoke and installation are owner decisions, not
claims made by offscreen tests. No macOS support, remote access, agent control, scheduler,
Product workflow, MR checks or operational start/stop actions are implemented.
Events/receipts are not pruned; snapshots are complete with a 32 MiB limit, and
mutations currently load/persist the full store snapshot: this is not a large-scale
or hardware-failure durability guarantee. External legacy installations/volumes
remain untouched; see [cleanup inventory](legacy-cleanup.md).
