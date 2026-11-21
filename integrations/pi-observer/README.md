# Artel Pi observer

Opt-in observation only. The test SDK is pinned to Pi 1.5.2; runtime activation checks capabilities and requires Node >=22.23.2.
Observation supplies no tools, commands, messages, prompts, model selection or
global installation. The separate, explicitly enabled delivery modes below are
the only exceptions. Without ARTEL_OBSERVER_CONFIG the extension is inert.
Invalid/pending/private-file
permissions or unsupported host capabilities disable observation, not Pi.

The delivered package contains index.mjs, package.json and this README under
/usr/share/artel/pi-observer/, outside Pi auto-discovery. Explicitly load with
`ARTEL_OBSERVER_CONFIG=/ABSOLUTE/PRIVATE/config.json pi -e /usr/share/artel/pi-observer/index.mjs`.
Pi must be separately installed; Node >=22.23.2 is optional
for this integration only. Daemon/CLI/desktop start without either runtime.

For local builds run `npm ci --ignore-scripts` then `npm run build` in this package.
Copy the three dist files into private staging and load /ABSOLUTE/STAGING/index.mjs.
The bundle contains only Artel code with Node built-ins and the public Pi API
external (provided by the Pi extension loader); it needs no Artel checkout,
node_modules, TypeScript compiler or dev dependencies at runtime. Do not install
into global settings or `.pi/extensions`. No automatic service activation.

Set ARTEL_OBSERVER_CONFIG to the absolute private config path confirmed by
`artelctl observer provision` (or `provision --resume`). Never put the token in
argv, a prompt, a model/provider config, or terminal output. Private config and
token files must be 0600 in owner-only 0700 directories. A pending config is not
usable. The observer token does not isolate arbitrary code running as the same
Unix UID.

Enable the daemon's separate Unix observer listener explicitly. The extension
only connects to the locally provisioned socket and pins the database UUID before
registration and every report cycle. New/reload/resume/fork creates a candidate
runtime and needs explicit owner binding; reconnect does not create another Pi,
credential or runtime. No project is inferred from cwd or terminal environment.

Statuses are local footer text, never messages or model context. Offline retries
are bounded (1–30 seconds with jitter, 2-second request deadline); one uncertain
report and current state replace history. Heartbeats are every 5 seconds, changes
coalesce for at least 250 ms. Unknown/lost stop delivery becomes stale server-side.
Activity is not task completion or writer authorization.

Authentication, database mismatch, retired/superseded runtime, protocol and capacity
failures stop retries for that incarnation and require owner action. Payload quota
requires safe owner prune; identity quota requires explicit new provisioning,
connection, binding and old credential revocation. Observer quota is a capacity
blocker, not automatic garbage collection. Terminal UUID hints are unverified
claims only; no agterm commands are run by this extension.

Development: `npm ci --ignore-scripts` locally, then `npm run check`, or repository
`make observer-check`. Tests use pinned public SDK and a real isolated Pi TUI,
local deterministic provider and private Unix HTTP oracle. `make check` additionally
runs production daemon/QML recovery acceptance; `make package-check` exercises the
extracted artifact through the public Pi loader and starts staged binaries without
Pi/Node. No remote CI, normal-session Wayland or live owner pilot is implied.

Owner operations: provision/resume/list/bind/unbind/revoke/prune use artelctl's
owner API. Bind/rebind and destructive observer controls require explicit CAS
version. Never bind automatically from cwd, session, terminal or model. At 1024
lifetime identities explicitly provision/connect/bind a replacement and revoke the
old credential; prune only frees full payload slots (8), not retirement identities.
The 256-observer cap includes revoked credentials. Navigation is desktop-only,
checked session selection, not guaranteed pane focus or foreground activation.
See the repository's stable docs/development.md for the full operating contract.

Owner commissions (stage 4a), a separate opt-in mode from observation: it
delivers one owner's task message into the exactly bound Pi session and needs
its own confirmed `delivery.json` plus delivery token written by
`artelctl delivery enable` next to the observer config; observation config
alone never activates it. The two modes are independent: disabling observation
leaves a confirmed delivery grant active (the writer talks to the daemon
directly), and disabling delivery never affects telemetry — each mode has its
own credential and config. The writer takes a
kernel-exclusive lock (unix bind), CAS-attaches, spends one delivery right,
transmits synchronously only while idle, and publishes the observed receipt
idempotently. Busy sessions are never prompted, manual input is never replaced,
and an unknown outcome never auto-resends. Without confirmed config the
extension stays inert.

Owner questions (stage 4b) require another explicit choice when creating a new
delivery grant: add `--allow-owner-questions` to the full `artelctl delivery
enable` command documented in docs/development.md. Resume keeps that original
choice; an existing grant cannot be changed in place. Observation config alone
does not opt in. The confirmed delivery config carries the bit and the writer
must attach with `owner_questions_v1`; without that grant/capability the tool is
inactive and the daemon rejects question creation.

The owner copies the selected Artel task UUID from Qt and explicitly hands it to
Pi in a commission or manually. Only then `artel_ask_owner` accepts that exact
`task_id` plus question `text`; it cannot search tasks and never receives the
owner credential. The tool finishes after storing the question, returning its ID
and `stored`, not an answer. An unconfirmed ACK is not success, and no restart or
text match automatically creates another question.

The owner can save one immutable answer without writer readiness while the
question/task/project and original grant remain eligible. Answer and queue row
commit together. When the original Pi session is idle, the writer sends a new
`artel-owner-answer` custom message containing the question and answer as
context. It does not interrupt a busy session, replace manual input or follow a
new assignment. Question `open`/`answered`/`closed` is independent of answer
`stored`/`unknown`/`observed`; `observed` is only a matching custom
`before_agent_start` receipt, not provider delivery or turn completion. Unknown
outcomes never auto-resend, and saved-receipt recovery makes no new Pi call.

Close/archive/revoke or assignment change closes unanswered questions and blocks
undelivered answers permanently; saved history and existing `observed` remain.
Reopen/unarchive or a new grant does not resume old answers. A stopped original
Pi does not prevent saving, but a replacement runtime with the same Pi-session
UUID cannot inherit the grant. Revocation denies the old delivery credential
access to history. Already authorized Pi calls are not promised to be cancelled.
Both texts go to the provider configured in Pi as conversation context; Artel
does not execute them, grant permissions or change task state.

Question/answer text is limited to 4 KiB UTF-8 without NUL, POST bodies to
32 KiB, and owner pages to five rows/320 KiB. Text stays out of observer
telemetry, domain events/SSE and logs. No global inbox, editing, TTL, broadcast,
remote access or blocking wait is provided. SQLite upgrades atomically from v3
to v4; failed migration rolls back, while a successful v4 database is rejected
by old v3 binaries and has no in-place downgrade. False/absent opt-in preserves
stage-4a wire/receipt compatibility. The base extension handles ordinary 4a
commissions only; an old daemon rejects opted-in enable without creating a grant
or confirmed config. See docs/development.md and the owner-questions evidence
for the complete contract and acceptance boundary.
