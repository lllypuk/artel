# Native full-stack acceptance

Run `make test-e2e` (or `make check` for all local gates). The runner builds its
own daemon and C++ Qt Test harness with production Main.qml, Panel, ApiClient and
Appearance. It uses offscreen/software rendering, an empty environment, private
HOME/runtime/data/token paths and ephemeral loopback ports. No installed service,
owner settings, real project, external agent or database is used.

The scenario drives real QML mouse/keyboard input to create a project and three
tasks, edit Cyrillic criteria, enqueue/reorder, start and complete manually. An
independent authenticated HTTP client checks snapshot fields, versions, queue and
audit events. It destroys/recreates the QML engine while the daemon stays live,
then restarts that daemon on the same database and verifies exact snapshot
identity before reconnecting to its newly allocated port.

Two clients exercise stale-version conflict with draft preservation and a
concurrent queue-reorder/start race. A temporary forwarding proxy drops a real
successful mutation response after commit, then checks byte-identical retries
and exactly one persisted event. SSE updates and reconnect after missed events
are asserted without relying solely on API tests.

Qt treats QML warnings as failures. HTTP and assertions have bounded waits; the
runner has a 180-second watchdog. QProcess RAII stops only the owned daemon;
Linux parent-death signaling covers fatal harness exits. All daemon directories
are under the disposable HOME. A second, intentionally failing invocation must
return exactly 1 and leave neither its recorded PID nor data directory alive.
Its expected failure output stays private; the runner reports its cleanup check.
Any unexpected assertion, timeout, crash or cleanup failure returns nonzero.

## Opt-in observer acceptance

The same mandatory runner also executes `observer_acceptance.py`: actual temporary
arteld/CLI provisioning, the pinned Pi 1.5.2 TUI on a PTY with the shipping extension,
a numeric-loopback deterministic provider, and repeated production-QML processes.
Install the pinned test dependencies explicitly with `npm ci --ignore-scripts`
in `integrations/pi-observer` first; absence is a preflight failure, never a skip.
No Pi/Node dependency is introduced into the manual runtime.

The independent owner HTTP oracle checks candidate/bound identities and activity,
working/overlapping tools/UI wait/settled, daemon SIGKILL/restart, offline latest-state
catch-up, same-runtime reconnect, fork/new/reload without inherited binding, explicit
rebind, Pi SIGKILL to stale, old-generation rejection, prune/restart retirement,
revocation and database replacement with the same credential verifier. A private
Unix forwarding fixture loses an accepted report ACK; the real bridge's identical
retry is observed as duplicate. A second wire-level duplicate preserves last_seen.
The real 8-payload and 1024-identity quotas are exhausted via HTTP, then CLI
provisions a new credential for an explicit bind and revocation of the old one.
A populated manual snapshot (tasks, queue, versions, event cursor) is unchanged.
No agterm environment or terminal hint is provided. QML is recreated against the
actual server at candidate, working, waiting, idle, bound/reconnected and stale
states; it is not fed synthetic observer JSON.

Only a whitelisted child environment is passed: private HOME/XDG/Pi resources,
fictional provider, no real credentials/global extensions/skills. PTY output is
bounded; HTTP, Qt and process waits have deadlines. The full observer scenario
has a 240-second watchdog. An exec wrapper arms Linux parent-death SIGKILL for
all owned processes. Finally blocks reap children and close server threads/PTYs;
the negative invocation must fail exactly 1, remove its temporary tree and leave
none of its recorded daemon/Pi/Qt/CLI PIDs. A process crash test does not establish
power-loss durability. See `docs/experiments/pi-observer/recovery.md` for evidence.

This is native offscreen interaction coverage, not a claim of an interactive
Omarchy desktop smoke test or completed remote CI/package validation.
