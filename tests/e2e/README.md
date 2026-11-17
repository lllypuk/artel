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

This is native offscreen interaction coverage, not a claim of an interactive
Omarchy desktop smoke test or completed remote CI/package validation.
