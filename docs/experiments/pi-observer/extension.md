# Opt-in Pi observer extension evidence (Task 4)

## Implemented

`integrations/pi-observer/src/index.ts` is the shipping extension entry point,
exported by the package manifest. Its factory only subscribes to notification
hooks. Config/token reads, resources and networking start at `session_start`.
Without config it is inert. The existing confirmed/private-artifact reader is
used directly; pending, unsafe and unsupported configurations disable observation.
The version gate requires Pi 1.5.2 and Node >=22.23.2; public context capabilities
are checked before connecting. Reviewed the local extensions reference in full,
package/dependency reference, and status-line/model-status examples. Required
lifecycle hooks were already established against pinned Pi in Task 1 and are
exercised again here with the shipping implementation.

`src/client.ts` owns fixed-authority Unix HTTP, a 2-second absolute deadline,
16-KiB request/response limits, response identity validation, database UUID pin,
immutable registration replay, generation fencing and sequence namespaces.
Context precedes registration and every telemetry cycle. Lost ACK retains the
exact uncertain packet; events retain only current state. There is one request
in flight, one pending immutable report, and no tool-output/event history.
Reports use monotonic scheduling, 5-second heartbeat, 260-ms coalescing and
1–30-second capped exponential reconnect with jitter. Event bursts cannot bypass
backoff or starve a scheduled update. Graceful shutdown cancels existing work and
makes a best-effort stopped report only when the rate budget permits, bounded by
240 ms. Lost stop remains stale rather than inferred completion.

Generation conflict performs one own-context reconciliation; unchanged/regressed
generation fails closed. A confirmed generation change discards the old packet
and starts the new sequence at one. Superseded, retired, unauthorized, database
mismatch, protocol/sequence failures and capacity errors stop retries for that
incarnation. Payload/identity/observer quota diagnostics are distinct fixed text,
not server-body echoes. No provisioning, project inference or automatic rebind.

Activity uses active tool IDs, settled/isIdle reconciliation and the public UI
waiting span. Metadata is limited to version/session/runtime, cwd, provider/model
identifiers and optional complete UUID terminal hints. No agterm process is run.
No prompts, UI dialog text, tool name/arguments/output, provider credentials,
session contents or arbitrary environment are serialized. Local status uses only
footer status, not transcript/model messages. No control API/tool/command/model
registration is accessed by the shipping factory.

## Executed validation

- `make lint test build`: PASS (Go 1.26.3, pinned golangci-lint 2.10.1 via
  GOLANGCI_LINT, Qt 6.12.0, clang-format 23.1.3).
- `make observer-check`: PASS (Node 22.23.2, pinned Pi 1.5.2, TypeScript 5.9.3;
  preflight, lint/typecheck, 11 unit tests, real SDK/PTY tests, OpenAPI test).
- `python3 -B tests/pty_probe.py` from the package: repeated startup/shutdown/
  reload/new/resume/fork loops PASS; no mandatory skips. The pre-existing hook
  probe remains an independent lifecycle trace, not a substitute for shipping
  extension HTTP assertions.
- `git diff --check`, substantive source/test review and scoped secret/privacy
  scan: PASS. No production credentials or dialog data in changes.

Unit tests cover uncertain report and registration replies, byte-identical replay,
accepted/duplicate ACK, generation change/conflict, delayed requests, single-flight,
event storms while offline, capped retry counts, terminal errors, database/identity
pin before registration, idempotent bounded abort/cleanup, stopped delivery,
fixed Host/auth, oversized response, quota redaction and metadata/hint allowlists.

Real public SDK and TUI processes load `src/index.ts` explicitly, use private
HOME/XDG/agent/cwd/session paths and a loopback deterministic provider. A private
Unix HTTP oracle asserts shipping registration/report bodies, two simultaneous
tools, waiting/idle transitions, fresh runtime on every lifecycle boundary,
listener disappearance/reconnect without new runtime, rate limits, no unsolicited
provider request, no token in argv/output/provider payload, and cleanup after
SIGKILL. Fixture tool/wait durations are deliberately longer than the production
coalescing window; no validation timeout was relaxed.

An additional real-daemon test builds disposable arteld/artelctl binaries,
provisions via the actual CLI, starts the shipping extension in a public SDK
session, then SIGKILL/restarts arteld with the same private database and socket.
Owner projection proves preserved database/runtime identity, stale/unknown after
restart, and fresh after a new heartbeat, with one runtime and zero unsolicited
provider calls. Processes are reaped, private files/sockets removed and local HTTP
oracles explicitly bypass proxies. An intentional PTY assertion failure must
return nonzero, reap its child, close servers and leave its temporary directory
empty; this negative cleanup test also passes.

SDK `dispose()` alone does not deliver extension lifecycle shutdown. The SDK
fixture now explicitly emits public `session_shutdown` before disposing; actual
TUI lifecycle does this itself. This fixes the test fixture rather than weakening
shutdown/resource assertions.

## Boundaries

This task does not implement observer QML, navigation, full-stack QML acceptance,
installed/bundled artifact verification, global extension installation, service
activation, live user observation, Wayland smoke or remote CI. Package artifact
instructions are local explicit-load instructions, not a claim that final
standalone delivery gates have run. Those remain later tasks/owner actions.
Same-UID code is not sandboxed. SIGKILL tests do not establish power-loss durability.
