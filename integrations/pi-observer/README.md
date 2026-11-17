# Artel Pi observer

Opt-in observation only. Supported host: Pi 1.5.2, Node >=22.23.2.
No tools, commands, messages, prompts, model selection or global installation.
Without ARTEL_OBSERVER_CONFIG the extension is inert. Invalid/pending/private-file
permissions or unsupported host capabilities disable observation, not Pi.

The package manifest exports src/index.ts. A local package artifact can be made
with `npm pack --pack-destination /ABSOLUTE/PRIVATE/STAGING` from this directory;
extract it into private staging and explicitly load its `package/src/index.ts`
with Pi's `-e /ABSOLUTE/STAGING/package/src/index.ts`. Do not install into global
settings or `.pi/extensions`. Artifact delivery/standalone checks are a later gate.

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

Development: `npm ci` locally, then `npm run check`, or repository
`make observer-check`. Tests use pinned public SDK and a real isolated Pi TUI,
local deterministic provider and private Unix HTTP oracle. Production daemon/QML
end-to-end acceptance and installed artifact gates belong to subsequent tasks.
