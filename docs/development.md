# Developing Artel v2

Artel is a local manual panel with opt-in Pi observation, not an agent orchestrator.
Read [AGENTS.md](../AGENTS.md) and this stable development contract before changing
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
for explicit rebind. `unbind` and `revoke` take `--id UUID --expected-version N`.
All accept the owner endpoint/token-file options above and `--idempotency-key UUID`;
retain the same key/body for uncertain mutation retries. A conflict requires fresh
list/version and an explicit decision, not silent retry. Desktop offers the same
controls without a credential textbox and keeps manual drafts independent.

For prune, inspect `artelctl observer list --json`. In the target observer, review
all `runtimes` with `prune_eligible: true` and confirm each `registration.runtime_id`.
Run `artelctl observer prune --id OBSERVER_UUID --expected-version N --runtime-id
RUNTIME_UUID`, repeating `--runtime-id` once for every additional confirmed ID.
The CLI never fetches or approves IDs on your behalf. The owner `POST
/v1/observers/{observer_id}/prune` JSON body must contain `expected_version` and
`expected_runtime_ids` (1–8 unique canonical non-nil UUIDs, in any order). With the original key, version and equivalent set, replay returns the original
result even after retirement. A changed version or set under a recorded key
returns `409 idempotency_conflict`. With a new key, a stale version or exact set
returns `409 version_conflict` without retiring anything, including when a new
candidate registers without a version bump. Refresh the registry and reconfirm.
When listing against an older daemon, absent `prune_eligible` means unknown, not
ineligible.

Upgrade the daemon, desktop and CLI together; the old version-only request now
returns 400 and the updated clients do not fall back. There is no data migration.

## Opt-in delivery (owner commission to one bound Pi session)

Delivery is off by default and separate from observation: the same Unix listener
serves `/delivery/v1/*` under a dedicated delivery credential, and the existing
observer token can never read commissions. One installation holds at most one
non-revoked binding; enable stays `pending` until a writer with the exact bound
runtime/project/session/generation and original journal ID attaches.

Select the exact observer/runtime/project/session UUIDs from
`artelctl observer list --json`, then:

```sh
"$ARTELCTL" delivery enable --id OBSERVER_UUID --runtime-id RUNTIME_UUID \
  --project-id PROJECT_UUID --pi-session-id SESSION_UUID \
  --observer-socket "$ARTEL_SOCKET" \
  --output "$(dirname "$ARTEL_CONFIG")/delivery.json" \
  --expected-version N --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
"$ARTELCTL" delivery revoke --id OBSERVER_UUID --binding-id BINDING_UUID \
  --output "$(dirname "$ARTEL_CONFIG")/delivery.json" \
  --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
```

Enable persists a private pending record (original request, retry key, and a
reference to the delivery token it writes next to the config directory) before
the first HTTP request; a lost response is recovered with
`delivery enable --resume PENDING_PATH` using the original key — never regenerate
a credential to retry. The confirmed writer config and the delivery token land
in the same private directory as the observer config (the writer discovers
`delivery.json` next to its `ARTEL_OBSERVER_CONFIG`) only after server
confirmation; the extension picks them up without restarting the bound runtime.
JSON output is public IDs/paths only.

In the desktop task screen the owner types a commission, sees the exact
assignment and a warning that the text goes to the provider configured in Pi as
context, and sends explicitly. Statuses are `stored` → «Сохранено»,
`unknown` → «Исход неизвестен», `observed` → «Pi начал подготовку хода».
`observed` only means the exact `before_agent_start` observation; an unknown
outcome never triggers an automatic resend, and the same request with the same
key returns the original record. Closing the task or archiving the project
permanently blocks undelivered commissions; reopening does not resume them.

Isolated end-to-end proof: `python3 -B integrations/pi-observer/tests/delivery_pty.py`
with a real temporary daemon and pinned Pi; see
`docs/experiments/pi-bridge-delivery/product-slice-evidence.md` for scope and
recovery limits.
Reusing a legacy prune key with an exact-set body returns an idempotency conflict.
If a legacy prune outcome is uncertain, inspect the current registry and any
available prior result rather than blindly submitting another prune.

## Submitted Git candidates and imported revmux reviews

A submitted result is an immutable owner assertion about one exact `base`/`head`
pair. The repository identity is the canonical absolute Git common-dir, not the
project root or current worktree `HEAD`. Importing a review changes neither task
state nor queue/assignment. `range_source: operator` records the provenance of
the SHA claim; it is not independent Git or CI verification. Report references
are selectable display data only: Artel never opens a path/URI or copies raw
findings into SQLite.

For an isolated manual cycle, keep the temporary daemon and private variables
from the first example. Create a project/task in the desktop, copy its UUID from
the task card, and configure the explicitly opted-in delivery binding above.
From source, run `make observer-build`, then launch Pi in a **separate ordinary
terminal**, without agterm:

```sh
ARTEL_OBSERVER_CONFIG="$ARTEL_CONFIG" pi -e "$PWD/integrations/pi-observer/dist/index.mjs"
```

Send one commission containing that exact task UUID through the card. Pi runs
only in that terminal; Artel does not launch it or infer a commit/result from
activity. After the owner has literal full commit SHAs, create a private pre-run
binding and submit the selected candidate (all placeholders must be replaced
with values from this disposable instance):

```sh
mkdir -m 700 "$ARTEL_TMP/review"
SUBMIT_JSON=$("$ARTELCTL" results submit --task-id "$TASK_ID" --repo "$REPO" \
  --base "$BASE" --head "$HEAD" --summary "Ready for owner review" \
  --expected-result-id "" --binding "$ARTEL_TMP/review/first.binding.json" \
  --revmux-task "$REVMUX_TASK" --run "$RUN_ID" --request-id "$SUBMIT_REQUEST_ID" \
  --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN")
RESULT_ID=$(printf '%s\n' "$SUBMIT_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
```

Before the external run, the binding fixes repo/task/run/base/head. Run revmux
yourself outside Artel in a clean worktree with both SHAs as literals, then put
its completed `manifest.json` and `findings.json` in one local directory. Import
only normalized evidence, naming every required `name=executor` pair explicitly:

```sh
ATTACH_JSON=$("$ARTELCTL" reviews attach --task-id "$TASK_ID" --result-id "$RESULT_ID" \
  --binding "$ARTEL_TMP/review/first.binding.json" --report-dir "$REPORT_DIR" \
  --required-source reviewer=pi --request-id "$ATTACH_REQUEST_ID" \
  --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN")
```

Repeat the **same command with the same request ID** after an uncertain response.
An identical already-attached repo/task/run returns the original review without
another event. The card distinguishes loading, read failure and a confirmed
empty history, and shows full SHAs, run ID, provenance, report reference,
required-source completeness, findings/pre-existing/open-question counts and
prior candidates. Incomplete/degraded evidence and zero findings are not owner
acceptance.

After a new commit, submit its full SHA with `--expected-result-id "$RESULT_ID"`,
a new binding path/run and a new request ID. Capture the returned new result ID
for subsequent decisions. The old review remains attached to the old candidate
and is visible in history; it does not review the new SHA. A late identical
`reviews attach` for the old result is still historical. Accept or return only
through the card's existing manual actions: they use the current result ID from
the loaded view, and a conflict requires rereading rather than automatic retry.
The contract is in the [owner API](../api/openapi.yaml).

`make test-e2e` explicitly invokes the Qt slot `reviewedTaskResult` in two
isolated modes. The standalone mode proves the UI/SSE/CAS/restart/history
contract without Pi. `integrations/pi-observer/tests/result_pty.py` then runs
the same slot against one shared temporary daemon/task with a real bound Pi
PTY and deterministic local 4a provider. Production QML sends the commission
and observes its receipt before the **test runner** creates Git commits and
imports a synthetic manifest/findings pair; Pi answers `Fictional answer`.
The runner owns and cleans up daemon/Pi/Qt, uses a private environment without
agterm, and never launches revmux. This proves delivery plus the exact-result
contract, not creation of a commit by an agent or a real revmux demonstration.

## Owner questions and asynchronous answers (stage 4b)

This is a local code slice for one owner and one delivery grant, not a global
inbox, installation or pilot. It is disabled unless the owner creates a **new**
grant with the explicit option; an existing grant cannot be upgraded in place:

```sh
"$ARTELCTL" delivery enable --id OBSERVER_UUID --runtime-id RUNTIME_UUID \
  --project-id PROJECT_UUID --pi-session-id SESSION_UUID \
  --observer-socket "$ARTEL_SOCKET" --allow-owner-questions \
  --output "$(dirname "$ARTEL_CONFIG")/delivery.json" \
  --expected-version N --endpoint "$ARTEL_ENDPOINT" --token-file "$ARTEL_TOKEN"
```

The confirmed config records `allow_owner_questions: true`, and attach fixes
capability `owner_questions_v1` to the accepted writer. `delivery enable
--resume PENDING_PATH` replays the original option and key; it cannot add or
change the option. Revoke the old grant and explicitly enable a new one to
change this choice. Observation config alone grants neither questions nor
delivery, and the owner credential is never given to Pi.

The selected task screen exposes its task UUID for copying. The owner supplies
that exact UUID to Pi in a stage-4a commission or manually. Only then can the
opted-in extension's `artel_ask_owner` tool be called with `task_id` and `text`;
it does not search tasks. The tool waits only for question storage and returns
its question ID and `stored`, never the owner's answer. An unconfirmed ACK means
storage is unconfirmed, not successful. A new tool call is a new question; there
is no text deduplication or automatic retry after restart.

The owner answers once in the selected task. The immutable answer and its row in
the existing delivery queue commit atomically. Writer readiness is not required
to save an answer while the question, task/project and original grant remain
eligible. The answer later enters the **same Pi session** as custom message
`artel-owner-answer`, with separate JSON `question` and `answer` fields in the
conversation context, only
when that session is idle. It does not replace manual input or follow a changed
assignment. Question state `open` / `answered` / `closed` is separate from answer
delivery state `stored` / `unknown` / `observed` and its block reason. An answer
POST replay confirms the original stored answer, not current delivery status.
`observed` means only the matching custom `before_agent_start` receipt for the
question/message/attempt/session; it does not prove provider receipt, agent
completion or task completion. An unknown outcome never auto-resends. Restart
may republish an already saved receipt without another Pi call, but cannot
recover a missing journal or turn an uncertain spend into a safe retry.

Closing the task, archiving the project, revoking the grant or confirming an
assignment change closes unanswered questions and permanently blocks affected
undelivered answers with `task_closed`, `project_archived` or
`delivery_revoked`. Saved question/answer history and an already recorded
`observed` remain readable. Reopen/unarchive or a new grant does not reopen the
old queue. Stopping Pi alone does not close a question: an answer can be saved
offline, but a replacement runtime cannot inherit the grant even with the same
Pi-session UUID. Close/archive/revoke before verify prevents a new authorized
Pi call; after verify it cannot promise to cancel that call. A matching receipt
can still be accepted after close/archive under the unchanged grant; after
revoke or assignment change a first receipt is rejected and existing `observed`
is not downgraded. A revoked delivery credential cannot read old history.

Question and answer text is sent to the provider configured in Pi as part of the
conversation context. The UI warning is explicit: this does not confirm delivery
to that provider or completion of the Pi turn. Artel does not execute the text,
grant permissions, run agents or change task state; the new Pi turn retains the
ordinary capabilities and risks of its own runtime.

Each question and answer is non-empty valid UTF-8 without NUL and at most 4 KiB;
question/answer POST bodies are at most 32 KiB. Owner question pages contain at
most five rows and 320 KiB. Qt polls for new questions when caught up and
refreshes loaded status rows in batches of five. When a page has a real backlog,
use **Load more questions** rather than loading the rest in the background.
Unsent answer drafts are window-local and guarded on close, not written to disk.
If the daemon switches databases, open **Recover answer drafts** in the sidebar
and use **Copy answer draft** for the draft tied to the original database. If a
save is still uncertain, you can copy the draft but cannot discard it. To stop
tracking that save after a database switch, choose **Abandon old save, keep draft**
and confirm: the save may already have committed in the original database.
**Discard answer draft** removes only the local text once no save is pending or
uncertain. Copying, abandoning, and discarding never resend an answer to the
replacement database.
The delivery `next` response remains bounded to
64 KiB. Text is absent from observer telemetry, domain events/SSE and logs.
There is no editing, TTL, broadcast, remote access, blocking wait in Pi or
automatic task transition.

Compatibility and SQLite rollback are deliberately one-way. Opening a v3
database atomically migrates it through v4 to v5 and retains existing
commissions, attempts, receipts and identity while adding submitted results and
reviews; a failed migration rolls back to v3. After a successful migration, old
v3 and 4b/v4 binaries refuse the v5 database without changing its data or
identity: in-place downgrade is unsupported.
Absent/false `allow_owner_questions` preserves the v3 enable/revoke wire and
receipt hashes and ordinary stage-4a delivery. The base extension can continue
ordinary commissions but cannot ask questions or receive answer text. Opted-in
writer requests pin the database in every step, and a receipt replay checks
the current writer and assignment. A new
opted-in CLI request to an old daemon is rejected without a server grant or
confirmed writer config.

Contract: [owner and delivery API](../api/openapi.yaml). The isolated real-Pi /
daemon / production-QML oracle is
`python3 -B integrations/pi-observer/tests/questions_pty.py`. Both local final
gates passed on the previously recorded candidate; those results do not cover
review fixes, re-review, manual acceptance or a pilot. See
[owner-questions evidence](experiments/pi-bridge-delivery/owner-questions-evidence.md#task-6-повтор-после-двух-исправлений-в-тестах).

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
restart, message delivery or automatic agent launch. Observation, opt-in task delivery and question/answer exchange use the Pi extension, not terminal automation. A running, explicitly connected Pi session in the owner's usual terminal does not need an agterm hint. Without agterm there is no shortcut to its window. This separation is not a claim of macOS or Windows support.

Contracts: [owner API](../api/openapi.yaml), [observer API](../api/observer-openapi.yaml).
Implementation evidence lives in [observer experiments](experiments/pi-observer/README.md)
and its storage/http/extension/desktop/navigation/recovery records. Completed plans
are historical evidence, not active runtime instructions; this stable page remains
the entry point when ralphex archives a plan.

## Desktop presentation contract

Startup always opens Team, including an empty observer registry. One non-revoked
observer with a binding contributes one roster row; additional candidates belong
to Connections. Stale/offline rows retain identity but report current activity as
unknown; stopped is not completed. Filters and stable-ID selection never mutate
manual state. Counts distinguish the full roster from filtered results.

Main.qml owns one lifetime Panel/ObserverClient/WorkspaceController. The C++
TeamOverviewModel consumes sanitized existing projection data, not SQLite or a
second network client. WorkspaceController owns safe route/selection changes.
Observation polls only while Team or Connections is visible and the window is not
minimized. Owner SSE and manual requests/drafts remain independent.

Team navigation preserves a dirty/conflicted/uncertain manual editor and its base
identity. Save/Cancel affect only the visible manual editor; global close retains
confirmation guards. Project changes and observer confirmations recheck current
identity/CAS state. There is no background save or draft persistence across exit.

Shared Theme/Basic Controls use the native UI font and read-only Appearance inputs;
QML receives no credential. At narrow content widths or larger fonts, details use
the same window with Back, not a new read modal. Long editors/metadata scroll.
See [DESIGN.md](../DESIGN.md) for tokens, keyboard/contrast/matrix evidence and
[fictional screenshots](screenshots/README.md) for reproducible private captures.
These stable links do not depend on a plan remaining in its active directory.

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
