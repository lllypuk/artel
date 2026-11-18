# Observer delivery and exact-tree local acceptance (Task 8)

## Delivered boundary

The feature branch ships three inert files under `/usr/share/artel/pi-observer/`:
`index.mjs`, `package.json`, `README.md`. They are not in Pi auto-discovery.
`build.mjs` bundles Artel TypeScript with esbuild 0.25.12; only Node built-ins and
`@earendil-works/pi-coding-agent` public API remain external. Pi's public extension
loader supplies that API. No Pi implementation, TypeScript compiler, source map,
node_modules, checkout path, config, credential or database is in the payload.
The exact package manifest rejects any extra file before extraction.

Node >=22.23.2 is an optional Arch runtime dependency for the observer only;
separately installed Pi must be exactly 1.5.2. Both are development/test dependencies,
not dependencies of the delivered Go/Qt executables. The unchanged user service
has `ExecStart=/usr/bin/arteld` without an observer listener. No install hooks,
auto-enable, global settings, package installation or service activation occur.

Source snapshots include only the named observer sources/manifest/lockfile/build
script/README. Dist, node_modules and a fictional private config are explicitly
excluded in regression tests, even in an otherwise unignored test repository.
Build dependencies are installed by `npm ci --ignore-scripts` inside the temporary
makepkg workspace; application tests require explicit package-local preparation.
Missing tools/dependencies fail preflight rather than skipping checks.

## Commands and observed results

Local run: 2026-11-18 (+05). Versions: Go go1.26.3, golangci-lint 2.10.1,
Qt 6.12.0, Node 22.23.2, npm 12.0.2, Python 3.14.8, TypeScript 5.9.3,
esbuild 0.25.12, pinned Pi 1.5.2. Linux/Arch, unprivileged/offscreen.

- `npm --prefix integrations/pi-observer install --save-dev --save-exact --ignore-scripts esbuild@0.25.12`
  and `npm --prefix integrations/pi-observer install --package-lock-only --ignore-scripts`:
  package-local preparation/lock update, PASS; audit reported zero vulnerabilities.
- `npm --prefix integrations/pi-observer run check`: PASS, lint/typecheck,
  11 unit tests, build, 4 public SDK/real PTY process tests, zero skips.
- `make lint test`: PASS, documentation/delivery smoke, Go and Qt tests.
- `make check`: PASS twice, including final-tree `scripts/preflight --observer`,
  Go race tests, build, observer-check, native acceptance, full-stack observer
  Pi/daemon/production-QML recovery and both intentional-failure cleanup probes.
  Final counts: 14 Python tests, Qt ArtelTests 32 / ClientTests 32, native
  Acceptance 3, observer unit 11 / SDK+PTY 4; mandatory checks have zero skips.
- `make package-check`: PASS twice, including after preflight/link regression
  fixes. Builds a disposable package from current sources, inspects exact payload,
  regular types, root ownership, safe modes, ELF executables, desktop entry/unit
  and absence of original checkout path in every payload file. Nothing installed.
- `python3 -B tests/staged_acceptance.py STAGING` is invoked by package-check:
  extracted daemon/CLI/desktop run with an empty PATH (no Pi/Node discovery),
  private HOME/data and an ephemeral numeric-loopback port. Desktop remains alive
  offscreen; snapshot retains a preseeded v1 database UUID; disk user_version is 2
  after shutdown. This staged migration uses the historical v1 schema with identity;
  populated projects/tasks/queue/events/receipts migration and rollback tests also
  pass in the Go suite, not claimed as populated staged-package coverage.
- The same staging check passes the extracted index.mjs path to the real public
  Pi SDK loader with private resources/deterministic provider and a Unix HTTP
  oracle. Registration/reports are required and secret output/provider leakage
  is rejected. No relative checkout or Artel dev-tree module is available at the
  artifact path. Real PTY and full-stack recovery additionally use copied bundles
  in private temporary installation directories.
- Documented provision/resume/list commands run against a disposable actual daemon;
  config is confirmed and output contains no secret. Existing public status/private
  snapshot/invalid-argument smoke still passes.
- Link regression copies docs/API into a private tree, moves the active plan to
  its completed location there (or uses the already moved plan), then resolves
  active Markdown/backtick links. Active instructions use `docs/development.md`,
  not a soon-to-move or not-yet-existing completed observer-plan path. Cleanup
  inventory's historical deleted filenames are not treated as live backtick links.
- `git diff --check`: PASS. Targeted Python scan of changed/new files for private
  key blocks, provider/GitHub token patterns, long credential assignments and
  private artifact filenames: PASS. Manual content review of source/tests/docs,
  package recipe/manifest, CI, lockfile and new build/staging scripts: PASS.
  Package-lock additions are the pinned esbuild dependency and platform variants.

Initial review found an overescaped link regex and replaced the weak path assertion
with an actual private-tree move test. That stronger test correctly identified
intentionally deleted names in the legacy cleanup inventory; live links remain
checked while historical deleted backtick names are excluded. Final gates above
were rerun after these fixes. No existing checks/timeouts were weakened.

## Handoff and limitations

Operating instructions and implemented API boundaries: stable
[development contract](../../development.md). Historical foundation and observer
experiments remain preserved. Root/Claude/Copilot instructions agree: observation
only, independent owner manual state/drafts, no delivery or agent control.

No merge, push, remote CI, deployment, live owner Pi pilot, service activation,
host package installation, real agterm session switching or normal-session
Wayland/foreground smoke is claimed. CI now explicitly prepares pinned Node and
package-local dependencies in its ephemeral Arch job, then runs the mandatory
observer/full-stack/package gates; YAML is not an executed remote CI result.
Same-UID code is not sandboxed; process recovery is not power-loss durability.
The 2-second no-Pi desktop check is startup evidence, not an interactive desktop
pilot. SQLite event/receipt growth and full manual snapshot limitations remain.

The real ralphex plan move happens outside this task iteration. The copied-tree
post-move regression passed; the final live-tree link rerun after the runner moves
its plan is deferred to acceptance (not claimed executed early). At that point run
`python3 -B -m unittest discover -s tests -p 'test_documentation.py'`. Installation,
normal-session desktop/agterm smoke and real-session provisioning require the
owner's separate decision. Do not auto-connect a working user session.

## Post-review corrections (2026-11-18)

The three successful round-01 sources plus the successful adversarial retry
reported four minor defects: stale contract status, nil UUID acceptance that could
invalidate the desktop registry, case-folded JSON aliases overriding exact fields,
and UTF-16 length counting rejecting valid non-BMP metadata. All four were fixed:
non-nil canonical UUID checks now agree across storage/config/bridge/OpenAPI;
observer HTTP checks exact per-operation and nested terminal keys before decoding;
Qt counts Unicode code points (including the contract's 128-code-point label
limit); the contract identifies the implemented surfaces and owner references.

Regression tests failed on the old nil/alias implementation and on ten non-BMP
projection cases before fixes. Coverage includes every request field across all
six mutation operations, nested terminal aliases, no mutation on invalid CAS,
no registry capacity consumed by nil registrations, config/bridge UUID agreement,
and Unicode bounds below/at/above the limit. Existing valid requests/unbind and
full-stack lifecycle/recovery coverage remain enabled.

`make lint test` passed. The first full `make check` exposed a fixture dependency
on the caller's umask: the external-QML test copied a token into a directory
created as 0755 under umask 022. The fixture now explicitly sets that temporary
directory to 0700; production credential validation was not relaxed. A complete
`umask 022; GOTOOLCHAIN=go1.26.3 GOLANGCI_LINT=/tmp/artel-pi-bridge-Fd40ok1E/tools/golangci-lint make check package-check`
then passed: 14 Python tests, all Go/race tests, 47 Artel Qt results, 32 client Qt
results, 12 observer unit tests, 4 real SDK/PTY tests, native and full-stack recovery
acceptance plus both intentional-failure cleanup probes, no mandatory skips.
Staged no-Pi startup/migration, standalone extension and package inspection passed.

The disposable package build emitted the existing qmake generated-metatypes
warning, a transitive `node-domexception` npm deprecation warning and a nonfatal
libfakeroot diagnostic. Subsequent generated-file build and independent package
payload/type/ownership/mode checks passed; these host/dependency diagnostics are
recorded, not presented as a warning-free package build. No packages/services
were installed or activated. No push, merge, remote CI, live pilot or new independent
review of these fixes was performed.
