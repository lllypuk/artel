# Pi observer: public lifecycle evidence (Task 1)

This page preserves the original Task 1 public-hook evidence, not an operating
contract. The implemented observer now has separate [storage](storage.md),
[HTTP/CLI](http-cli.md), [extension](extension.md), [desktop](desktop.md),
[navigation](navigation.md), [recovery](recovery.md) and [delivery](delivery.md)
evidence. Use the stable [development contract](../../development.md) for current
commands and boundaries. Source build/tests require the private Node/Pi toolchain;
delivered daemon/CLI/desktop run without it. No prompt delivery or production Pi
configuration is implemented.

## Reproduce

Required: Linux, Node >=22.23.2, npm, Python 3 with Unix PTY modules and the normal
Artel Go/Qt toolchain. Pi test dependency is exactly 1.5.2. No host installation,
real API key, user settings, global extensions, running service or desktop needed.

```sh
npm --prefix integrations/pi-observer ci --ignore-scripts --no-audit --no-fund
make observer-check
make lint test build
```

`observer-check` checks exact installed top-level versions against package-lock,
Node minimum, platform and PTY dependencies before TypeScript lint/typecheck,
unit tests, real SDK/PTY tests, and kin-openapi parser/schema validation. Missing
dependencies fail, never skip or install automatically. `npm ci` writes only the
package-local workspace. npm registry access is needed for dependency preparation;
provider fixtures use only their ephemeral numeric-loopback server.

Read implementation references: Pi 1.5.2 `docs/extensions.md` (including resource
lifetime, lifecycle reasons, UI wait and concurrency), `docs/sdk.md`,
`docs/custom-provider.md`, `examples/sdk/06-extensions.ts`,
`examples/sdk/12-full-control.ts`, `examples/sdk/13-session-runtime.ts`,
`examples/extensions/timed-confirm.ts`, and `shutdown-command.ts`. Public types
and real dispatch, not private runner emission, are the oracle.

## Observed lifecycle matrix

| Action in actual Pi PTY | Old runtime | New runtime | Pi session identity |
|---|---|---|---|
| startup | none | `session_start(startup)` | initial |
| reload via public command context | `session_shutdown(reload)` | `session_start(reload)` | same |
| new | `session_shutdown(new)` | `session_start(new)` | different |
| resume original session | `session_shutdown(resume)` | `session_start(resume)` | original |
| fork at persisted user entry | `session_shutdown(fork)` | `session_start(fork)` | different |
| quit | `session_shutdown(quit)` | none | unchanged |
| SIGKILL | no graceful hook promised | none | parent cleans private files |

Every start allocates a fresh random runtime UUID in the probe, including reload
and resume. This is bridge identity policy, not a Pi-supplied runtime ID.
The PTY harness asserts the exact alternating shutdown/start sequence and five
unique runtime IDs. Lifecycle commands never trigger provider requests. Initial
startup is observed without a prompt, with zero provider calls and zero messages.

A user-authored fictional prompt makes exactly two requests to the deterministic
provider: one response calling two fixture tools, then a final text response.
Public tool hooks expose two active IDs, followed by individual removals.
`agent_end` precedes `agent_settled`; the latter sees `ctx.isIdle() == true`.
A timed public `ctx.ui.confirm()` emits `ui_prompt_start` and `ui_prompt_end` in the
real TUI; the probe discards title/body. A separate public SDK session loads the
same extensions, selects the fixture provider, and verifies a real settled run,
empty discovered skills/context and absence of startup messages.

## Isolation, bounds and output

- Every subprocess gets a constructed environment, private temporary HOME, XDG
  directories, PI_CODING_AGENT_DIR, cwd and session files; developer environment,
  authentication, project skills and global extensions are not copied.
- CLI explicitly disables discovery, MCP, built-in tools and context files, and
  uses offline startup. The selected provider is exclusively the fixture with a
  fictional credential and ephemeral loopback URL. Fixture tools only delay and
  return fictional text; they do not execute shell commands or create children.
- The observer probe only subscribes. Its unit fixture rejects access to any
  control API. Factory creates no trace/resources; shutdown is synchronous,
  idempotent and measured below 250 ms. This proves the probe's cleanup budget,
  not future network-client shutdown or a 250 ms whole-Pi exit guarantee.
- SDK subprocess has a 20 s watchdog; PTY waits have 15 s deadlines, captured
  terminal output is capped at 1 MiB, exit/reap has a 3 s deadline. Finally blocks
  kill/reap only owned process groups, close PTY/server/thread and remove the
  enclosing temporary directory. A separate startup/SIGKILL case verifies reaping
  and parent-owned cleanup without relying on a shutdown hook.
- Trace whitelist: `event`, `runtime`, `session`, `idle`, `tools`, `waiting`,
  optional `reason`. No tool IDs/names/args/results, dialog text, prompts, provider
  payloads, credentials, environment or session history is serialized. Terminal
  data contains fictional fixture content only, is memory-bounded and is shown
  only on failure; it is not committed. Standard output contains gate summaries,
  versions and test outcomes, never owner secrets.

## Executable contract

`api/observer-openapi.yaml` defines five owner operations and three Unix-only
observer operations with distinct auth requirements, strict DTOs, activity and
freshness enums, immutable registration, version CAS, generation/seq fencing,
error vocabulary, quota/prune rules and durable pending/confirmed provisioning
recovery. Pre-registration context returns candidate/generation 0 and the database
UUID without allocating an identity, allowing the required database pin check
before registration; retired IDs still fail. Transport, time, privacy and transaction invariants are documented as
contract extensions; schema validation alone does not prove server enforcement.
`internal/api/observer_contract_test.go` parses/validates the document, checks auth
separation and positive/negative payloads (including mixed-null binding, private
fields, malformed identities and sequence zero). Existing `api/openapi.yaml`
remains unchanged until server implementation. Later owner integration must reuse
these DTOs/references and test consistency rather than fork the contract.

## Recorded local results

- Pi 1.5.2 (public VERSION and package manifest); Node v22.23.2.
- Dependency preparation: `npm install --prefix integrations/pi-observer
  --ignore-scripts --no-audit --no-fund`; lockfile records exact dependency tree.
  A subsequent `npm --prefix integrations/pi-observer ci --ignore-scripts
  --no-audit --no-fund` and the same gates also passed.
- `make observer-check`: PASS (TypeScript lint/typecheck, 1 unit test, 2 real
  SDK/PTY tests, OpenAPI validation and payload cases; no skipped tests).
- `make lint test build`: PASS (golangci-lint 2.10.1, Go/Python/Qt regression tests,
  Go executables and native Qt build).
- Initial schema validation caught unquoted commas in inline YAML descriptions;
  fixed quoting and reran the gates. No checks were disabled.

No real daemon observer, remote CI, full-stack acceptance, power-loss durability,
user session observation or normal Omarchy/Wayland/agterm smoke is claimed. PTY
is lifecycle evidence, not desktop acceptance. Shipping extension behavior and
network recovery must be proved separately in later tasks.
