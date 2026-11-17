# Task 6: owner-local agterm session navigation

## Implementation and safety boundary

- `desktop/src/agtermadapter.{h,cpp}` owns asynchronous QProcess lifetime and an injectable runner. Only the fixed `agtermctl` executable is used, never a shell. Observer metadata cannot supply executable, socket or arguments. `artel --agterm-socket PATH` overrides the desktop's own `AGTERM_SOCKET`; missing/non-absolute socket fails closed, with no implicit default-socket fallback.
- Accepted hint is exactly three full UUIDs: window/session/pane. The window hint is not authoritative: inherited environment is explicitly unverified. Neither navigation nor hints bind a project, assign a writer or demonstrate the identity of the Pi process in a pane.
- Each explicit click probes `version --json` and `session select --help`, then enumerates `window list --json` and window-scoped trees. The supported dialect is deliberately limited to app version **0.40.0** and the required select flags. Unknown version/format/capability is unavailable, not a fallback to names, prefixes or active targets.
- All open windows are searched (at most 16 enumerated windows), including those outside the hinted/frontmost window. Exactly one full session+pane match with `realized: true` is required. Enumeration and trees are repeated once before selection to accommodate a move. Ambiguity, disappearance or an unrealized session fails closed; no session is created/restored/realized by this adapter.
- The only side-effect command is `session select --target FULL_SESSION --window FULL_CURRENT_WINDOW --json --socket OWNER_SOCKET`. Its response must acknowledge the requested session ID. No pane focus, type/mail, new, restore, restart, overlays, shell, systemd or compositor activation. A failed select is not retried.
- One workflow at a time, a shared 2 s watchdog, and a cumulative 1 MiB stdout/stderr budget cover the whole attempt (including both discovery passes). Cleanup kills/reaps the owned process; bounded kill wait is at most 100 ms. Stderr and CLI errors are never shown. Diagnostics are fixed translated text.
- `ObserverClient` copies only validated hints, recomputes navigability from online/fresh/unrevoked projection, and rechecks observer/runtime IDs on invocation. Missing/malformed/stale/offline hints cannot launch a process. Project/database/lifetime changes cancel pending navigation; late callbacks are fenced by QObject pointer and request generation. Manual editor and observer owner-mutation paths are not reused.
- Production QML adds a keyboard-accessible session-only action and an inherited/unverified hint warning. RU/EN text explicitly disclaims exact pane focus. Observer polling continues independently when navigation fails.

## Automated evidence

All commands below passed on the final Task 6 source tree:

- `make lint test build`: Go lint/tests, clang-format, production QML lint/format, desktop builds; ArtelTests **32 passed**, ClientTests **32 passed**, zero failures/skips.
- `make observer-check`: typecheck, 11 unit tests and 4 real-process public SDK/PTY tests, OpenAPI parser test; zero failures/skips.
- `make test-e2e`: existing isolated manual acceptance still passes with the new adapter linked. This is not the full-stack observer acceptance scheduled for Task 7.
- `git diff --check`: PASS. Changed-file private-key/GitHub/GitLab/provider credential pattern scan: PASS; manual review confirmed whitelisted UUID-only terminal hints and fixed diagnostics, no real credentials or private session metadata in fixtures.
- Impeccable mechanical detector on `desktop/src/ObserverPanel.qml`: `[]`.

New tests in `desktop/tests/artel_tests.cpp` and `agtermfixture.h` cover:

- Exact argv/call order for version, capability help, both window lists, every window-scoped tree and the sole select, always with an explicit owner socket.
- Inherited/wrong hinted window, target outside the frontmost window, cross-window move, pane role swap without focus, missing session/pane/window UUIDs, injection-like IDs/extra socket fields, missing target, ambiguous match, disappeared/unrealized session.
- Unsupported version/capability, malformed version/window/tree formats, oversized output (including cumulative tree output), excessive window enumeration, stalled callback, select error without retry, generation cancellation and post-destruction callbacks.
- An actual private fake `agtermctl` executable verifies every argv and response in order. Additional actual subprocess fixtures exit with secret-bearing stderr, stall past the deadline, SIGKILL themselves, and exceed stdout/stderr limits. Diagnostics never expose the canary. Each child PID is confirmed absent from `/proc` after completion. Only private fixture files/processes are created.
- Production QML keyboard and pointer activation, disabled absent/stale hints, session-only warning, unchanged dirty manual draft/project and zero domain mutations. Polling still succeeds after unsupported navigation.

A separate private-HOME/offscreen invocation of `observerNavigationProductionQml` passed (3/0/0 including setup/cleanup). Its fictional 1280×820 screenshot was inspected at `/tmp/artel-task6-navigation.png`; all new action/warning text is readable. No real owner paths, credentials or agents appear. Screenshot is local evidence, not a shipped artifact.

## Read-only CLI inspection and limitations

Installed/default-socket `agtermctl version --json` reported app/installed **0.40.0**, commit `85ceed8c77dd532905b96f71d2752c5139a5a380`. Read-only `session select --help`, `window list --help` and `tree --help` confirmed the flags. An explicit version probe against this execution environment's inherited `AGTERM_SOCKET` returned connection refused; it was not repaired or replaced. No actual tree/window enumeration or selection of working sessions was performed. Runtime Qt is **6.12.0**; supported desktop baseline remains Qt 6.8+.

There remains an unavoidable race between the last tree and select in this CLI dialect. Selection is session-only and best effort, not atomic pane identity/focus or a Wayland foreground guarantee. Unknown agterm versions intentionally remain unavailable until explicitly validated. Real Omarchy/Wayland foreground smoke, user-session pilot, remote CI, final packaging and full-stack observer recovery are not claimed here.
