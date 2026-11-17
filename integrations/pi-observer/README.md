# Artel Pi observer

Opt-in observation only. Supported host: Pi 1.5.2, Node >=22.23.2.
No tools, commands, messages, prompts, model selection or global installation.
Without ARTEL_OBSERVER_CONFIG the extension is inert. Invalid/pending/private-file
permissions or unsupported host capabilities disable observation, not Pi.

The delivered package contains index.mjs, package.json and this README under
/usr/share/artel/pi-observer/, outside Pi auto-discovery. Explicitly load with
`ARTEL_OBSERVER_CONFIG=/ABSOLUTE/PRIVATE/config.json pi -e /usr/share/artel/pi-observer/index.mjs`.
Pi itself must be separately installed at exactly 1.5.2; Node >=22.23.2 is optional
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
