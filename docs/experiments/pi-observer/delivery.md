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
