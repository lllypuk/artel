# Artel desktop design system — provisional native primitives

Tasks 1–6 of the desktop redesign, 2026-11-18. This describes implemented reusable
primitives, the team-first shell, observer inspector, connection management,
manual workspace and narrow/scaled hardening, not a completed redesign. Full
native full-stack/package acceptance and final documentation follow in later tasks. Mode: Operate,
native Qt Quick, canon direction with Zed-quality hierarchy/density rather than
editor imitation.

Authority: [product](PRODUCT.md), [approved brief](docs/design/20261118-artel-desktop-brief.md),
[selected composition and provenance](docs/design/reference/README.md).
The fictional HTML reference is not a native acceptance screenshot. No third-party
UI assets or fonts are copied.

## Tokens and appearance

`desktop/src/ui/Theme.qml` is a per-window QtObject, not a settings singleton.
Its typed Appearance input reads existing accent/radius only. It never writes
settings, accesses credentials or starts requests/processes. Language remains
owned by the existing Appearance translator; component strings come from callers.
A Basic Control supplies the native system UI font without a hardcoded family.
Callers can supply a larger systemFont; fontScale supports 1–3, with invalid or
out-of-range input falling back to 1.

| Role | Value / behavior |
| --- | --- |
| canvas | #1d2026 |
| panel | #282d35 |
| raised / hover / pressed | #333a45 |
| seam / field border | #505a69, 1 logical unit |
| text | #eef0f4 |
| muted / placeholder | #bac2cf |
| disabled text | #a3adbc |
| critical | #ffb4b4, always accompanied by action/status text |
| warning | #f0cc83, always accompanied by status text |
| selected / primary | opaque six-digit Appearance accent; fallback #a8c7fa |
| selected text | #1d2026 or white, whichever has higher WCAG contrast |
| focus | accent if contrast against raised is ≥3, otherwise #a8c7fa |
| primary focus border | selected foreground, so focus does not disappear into the accent fill |
| radius | Appearance 0–12; malformed/out-of-range values use 6 |
| spacing | 4 / 8 / 12 / 16 / 24 / 32 |
| typography | body max(14, system pixel size), secondary ≥12, title 18/24 relative to body |
| dimensions | controls ≥36, row token ≥52, grow for larger fonts |

Tests independently compute WCAG luminance/contrast, requiring ≥4.5 for all text
roles on canvas/panel/raised and for selected text; focus requires ≥3. Thus large
text also exceeds its ≥3 minimum. White, black, yellow and blue accents, malformed
and transparent inputs, and radius 0/6/12/invalid are covered. Primary pressed
state uses neutral raised/text rather than an unreadable accent/foreground pair.

## Components

All resources ship in `resources.qrc`; components import QtQuick.Controls.Basic
so later reuse does not accidentally inherit stock Material backgrounds.

- ActionButton: primary, secondary, quiet and destructive roles; text-based
  accessible name; hover/pressed/disabled/focus; keyboard Tab/Space support.
  One primary action per local action group. Destructive actions remain separate.
- Field: mouse selection, visible focus border, contrast-safe placeholder and
  selection colors. The caller supplies a persistent visible label and accessible name.
- LongText: wrapping plain text only, selection/copy, the same field treatment.
  The caller supplies a scroll container when bounded content needs scrolling.
- FilterBar: flexible search field with accessible name and a bundled authored
  20-unit SVG search icon (1.5 stroke), loaded only from qrc. No glyph/emoji icons,
  external assets, network fonts or filesystem checkout dependency.
- StatusLabel: neutral/warning/critical text, wrapping and plain-text rendering;
  callers must explain the status in words, not merely supply a severity color.
- Section: flat panel with seam, heading and layout slot; usable for inspector
  sections. Avoid nested card stacks.
- EmptyState: title/explanation, optional error emphasis; recovery actions belong
  to the caller, not hardcoded navigation or new backend behavior.

No animations, shadows, decorative metrics, navigation logic or duplicate editor
state are introduced. Buttons do not elide critical action text; layout consumers
must provide room or adapt their arrangement rather than clip controls.

## Native verification and evidence

`ArtelTests::nativePrimitives` loads a test-only composition of production qrc
components. It checks live RU/EN translation, 100/150/200% UI-font scale,
increased native font, geometry for scaled action rows, focus/Tab/Space,
disabled behavior, pressed colors, plain-text mode, resource icon readiness,
contrast and malformed appearance input. The fixture is in a separate test qrc,
not the shipped application resource tree.

Reviewed native offscreen/software renders (fictional text only):

- [English, 800×700, 100%](docs/screenshots/primitives-en.png): rounded radius 6,
  blue accent, keyboard focus, all button roles and plain-text editor.
- [Russian, 800×700, 100%](docs/screenshots/primitives-ru.png): live private
  Appearance refresh, bundled yellow accent/radius 0, complete localized controls.
- [Russian, 1000×900, 200% UI-font scale](docs/screenshots/primitives-ru-large.png):
  action rows remain separated; warning and empty-state labels fit without clipping.

All three images were opened and visually inspected. Initial untranslated fixture
labels were corrected; focus is visible, fields/buttons have consistent alignment,
and long text remains literal. These are component evidence, not the whole-screen
DPI/layout acceptance matrix required in later tasks. Actual OS 150/200% device
scaling and full application screens remain future gates, not claimed complete.

Capture without reading user settings or a checkout working directory:

```sh
ROOT="$PWD"
FIXTURE_HOME=$(mktemp -d)
mkdir -m 700 "$FIXTURE_HOME/runtime"
(cd "$FIXTURE_HOME" && env -i PATH="$PATH" LANG=C.UTF-8 HOME="$FIXTURE_HOME" \
  XDG_RUNTIME_DIR="$FIXTURE_HOME/runtime" QT_QPA_PLATFORM=offscreen \
  QT_QUICK_BACKEND=software ARTEL_PRIMITIVE_SCREENSHOT="$ROOT/docs/screenshots/primitives" \
  "$ROOT/desktop/build/tests/artel_tests" nativePrimitives -maxwarnings 0)
rm -rf "$FIXTURE_HOME"
```

Task gates: `make lint test build` passed with Go 1.26.3, golangci-lint 2.10.1,
Qt 6.12.0 and clang-format 23.1.3. Recursive UI/test-fixture qmllint has zero
warnings and qmlformat is enforced. Existing manual/observer Qt tests remain
intact. No owner daemon, data, token, desktop settings or real Pi/agterm sessions
were used. Full `make check`/package acceptance is reserved for subsequent tasks.

## Team-first shell (Task 3)

Main.qml retains the lifetime Panel/ObserverClient and uses the C++
WorkspaceController supplied by main.cpp. The startup route is Team, including
an empty registry; no sample data is inserted into the application. Persistent
navigation leads to projects and the existing manual workspace. Connections still
opens the preserved management drawer; its detailed redesign is not claimed here.

TeamOverview.qml uses a virtualized roster, separate text/project/activity filters,
whole-team versus shown counts, unbound footer and a stable-ID read-only selection.
The 64-unit two-line roster places status and relative age opposite label/project;
no UUID/model/version clutter is printed in rows. The initial inspector offers a
guarded project transition; richer observation details remain Task 4. Thin seams
separate navigation and the 280-unit inspector. Narrow/scaled adaptation remains
Task 6, not acceptance inferred from these desktop-size captures.

Read-only Team navigation retains editor text/base identity and shows a return
banner. Save/Escape are scoped to the workspace; global close protection remains.
Observation runs for a visible, non-minimized team screen or open management drawer;
manual owner streaming stays independent. A dedicated read-only loading property
avoids mistaking an observation read for a pending mutation. RU/EN changes now
refresh roster presentation without resetting the model or selection.

Production-QML fictional fixtures, captured with offscreen/software Qt and private
HOME/XDG, opened and visually reviewed:

- [English, 1280×820](docs/screenshots/team-overview-en-1280.png)
- [English, 1440×900](docs/screenshots/team-overview-en-1440.png)
- [Russian, 1280×820](docs/screenshots/team-overview-ru-1280.png)
- [Russian, 1440×900](docs/screenshots/team-overview-ru-1440.png)

All four show five bound observers (plus excluded runtime candidates), selected
details and a stale observation with explicitly unknown current activity. Initial
review caught untranslated roster labels after live language changes and an
over-wide inspector; both were fixed and captures repeated. Final frames have
readable labels, aligned two-line rows and no clipped actions at these sizes.
These are authored fixture renders, not owner data or copied reference assets.

Reproduce with the private-HOME invocation above, replacing the test selector with
`teamOverviewScreenshots` and the output variable with
`ARTEL_TEAM_SCREENSHOT="$ROOT/docs/screenshots/team-overview"`.

Task 3 gates: `make lint test build`, `make observer-check`, and `make test-e2e`
PASS; Qt suites 57+32, zero qmllint warnings, existing manual semantic assertions
retained. New production-QML tests exercise startup, empty/loading/offline/recovery,
keyboard navigation/search, draft return and hidden shortcuts, route/hide/minimize
polling lifecycle, management activation and no server mutations from reading.
No global settings, owner process/data, real Pi session or agterm navigation used.

## Observer inspector and connection management (Task 4)

Team details now contain the selected binding's project, current status, relative
age, full original timestamp, explicitly historical last activity, provider/model,
Pi/bridge versions and stable observer/runtime/version metadata. Technical text is
read-only selectable plain text; a scrollable inspector keeps actions above the
long metadata. Archived versus unavailable projects have distinct text notices.
A claimed agterm session opens only through WorkspaceController's stable-ID/version
check and the existing ObserverClient freshness/revocation checks and fixed adapter.
Management's project/category filters never scope that lookup or manual selection.
There are no new commands, network owners or credentials in QML.

Connections uses the shared semantic surfaces, status labels, action roles,
metadata disclosure and confirmation buttons. Scope and record-category filters
separate current bindings, runtime candidates and history. Registrations without
runtime remain explicitly unbound, not fabricated active members. Revoked,
superseded, stale and offline records never advertise current working activity.
Bind/unbind/revoke/prune retain explicit confirmations; the captured observer,
runtime, database, manual project, CAS version, generation and binding identity are
rechecked before dispatch. Presentation filters are not authorization boundaries.

Reviewed fictional production-QML captures (private HOME/XDG, offscreen/software):

- Inspector: [EN1280](docs/screenshots/team-inspector-en-1280.png),
  [EN1440](docs/screenshots/team-inspector-en-1440.png),
  [RU1280](docs/screenshots/team-inspector-ru-1280.png),
  [RU1440](docs/screenshots/team-inspector-ru-1440.png), at 1280×820 / 1440×900.
- Connections, 1280×820: [EN binding](docs/screenshots/connections-en-binding.png),
  [candidate](docs/screenshots/connections-en-candidate.png),
  [empty history](docs/screenshots/connections-en-empty.png),
  [selectable metadata](docs/screenshots/connections-en-metadata.png),
  [confirmation](docs/screenshots/connections-en-confirmation.png).
- Russian connections, 1280×820:
  [binding](docs/screenshots/connections-ru-binding.png),
  [candidate](docs/screenshots/connections-ru-candidate.png),
  [empty history](docs/screenshots/connections-ru-empty.png),
  [empty candidates](docs/screenshots/connections-ru-empty-candidates.png),
  [revoked](docs/screenshots/connections-ru-revoked.png),
  [offline](docs/screenshots/connections-ru-offline.png).

All 15 frames were visually inspected individually or in native capture contact
sheets. Review corrected stock light popup scrims, accidental opaque overlay and
unlocalized historical activity. Final surfaces retain visible action text and
separate candidates/history from the team roster. UUIDs intentionally wrap in the
narrow technical inspector and remain copyable; full narrow/device-scale acceptance
belongs to Task 6. No HTML/browser render is counted as native evidence.

Reproduce using the private-HOME invocation above with selectors
`teamOverviewScreenshots observerInspectorAndManagementScreenshots` and variables
`ARTEL_TEAM_SCREENSHOT="$ROOT/docs/screenshots/team-inspector"` and
`ARTEL_CONNECTION_SCREENSHOT="$ROOT/docs/screenshots/connections"`.

Task 4 gates: `make lint test build` and `make observer-check` PASS with the same
Go/Qt/lint/Pi/Node versions recorded above; Qt suites 61+32, zero qmllint warnings.
Production-QML regressions cover project-A-filter/project-B navigation with draft
A unchanged, filtered selection, stale/revoked navigation rejection, archived and
missing projects, malformed cached projection, metadata/confirm/cancel and observer
mutation while an independent manual save remains pending. Existing CAS rebind,
unbind, revoke, prune and old-database delayed-reply assertions remain intact.
Same-version binding replacement and disappeared-runtime confirmations send no
mutation. No owner daemon/data/token/settings or real sessions were used.

## Manual projects, tasks and history (Task 5)

ManualWorkspace.qml now uses Basic controls and shared Theme/ActionButton/Field/
LongText/StatusLabel/EmptyState primitives rather than Material buttons. Compact
project rows show names and manual-state counts, not raw ISO timestamps. Task
groups retain ordered queues and independent Remove/Up/Down controls; backlog
rows hide irrelevant ordering actions. Thin seams distinguish project list,
workspace and inspector. One primary creation action belongs to the task list;
Save or the applicable manual start/complete action belongs to the inspector.
Destructive archive/cancel/discard actions have a separate semantic treatment.

Editor fields have persistent labels. Read-only task title, description, criteria
and conflict text are selectable plain text; history changes remain selectable
and expandable. Presentation-only displayTimestamp uses local QLocale formatting,
while the original timestamp and all DTO/state/version/idempotency data remain
unchanged. Inapplicable state transitions are hidden; a queued non-head still
shows disabled Start with an explicit queue-head/no-current-task explanation.
Archived/read-only and unavailable-path notices remain discoverable.

New production-QML tests cover EN/RU action visibility, hidden backlog ordering,
copyable literal text, original/localized history timestamps, history expansion,
overview return with unchanged draft/task identity/version, hidden Save/Escape,
and snapshot refresh while the description field is focused. The real-daemon
CRUD test now clicks queue Up controls and verifies non-head disabled Start and
its explanation. Its mouse helper targets task titles rather than the independent
queue buttons at the row's geometric center; reordered/state-dependent controls
receive a layout pass before mouse input. Existing CRUD/criteria/start/done/cancel/
reopen/archive, two-client409/rebase, long text, keyboard/close/discard, history
pagination, delayed create/retry identity and database-replacement abandonment
assertions remain intact and pass.

Reviewed fictional production-QML captures, all 1280×820 offscreen/software:

- Projects: [EN](docs/screenshots/manual-redesign-en-projects.png),
  [RU](docs/screenshots/manual-redesign-ru-projects.png).
- Workspace: [EN](docs/screenshots/manual-redesign-en-workspace.png),
  [RU](docs/screenshots/manual-redesign-ru-workspace.png).
- Editor: [EN](docs/screenshots/manual-redesign-en-editor.png),
  [RU](docs/screenshots/manual-redesign-ru-editor.png).
- Expanded history: [EN](docs/screenshots/manual-redesign-en-history.png),
  [RU](docs/screenshots/manual-redesign-ru-history.png).
- [Queue/head actions](docs/screenshots/manual-redesign-queued.png): real temporary
  daemon, fictional Cyrillic project/tasks and private temporary workspace path.

All nine final frames were opened individually or in a native-render contact
sheet. Review corrected oversized read-only text areas, low-contrast queue buttons
on selected rows, and an irrelevant Task details heading in the project overview.
Final normal-size controls and editor labels fit, literal markup stays literal,
and history uses subdued local timestamps. Narrow/scaled/large-content hardening
is Task6, not acceptance inferred from these captures. No web detector or HTML
render is treated as Qt evidence.

Reproduce with the private-HOME invocation above, selectors
`manualPresentationAndScreenshots realQmlCrudQueueConflictHistoryAndClose`,
`ARTEL_MANUAL_SCREENSHOT="$ROOT/docs/screenshots/manual-redesign"` and
`ARTEL_TEST_DAEMON="$ROOT/build/bin/arteld"`.

Task5 gates: `make lint test build` and `make test-e2e` PASS; Qt62+32, zero
qmllint warnings, Go1.26.3/golangci-lint2.10.1/Qt6.12.0/clang-format23.1.3.
Full-stack observer Pi1.5.2/Node22.23.2 recovery and intentional-failure owned-process
cleanup pass. Standalone capture rerun from a private cwd/HOME also passes.
Only private fixtures were used; owner daemon/data/token/settings, real sessions,
API/store contracts and network owners are untouched.

## Compact layout and large-content hardening (Task 6)

Below 1050 logical content units (excluding navigation), or above an 18-unit UI
body font, Team switches between its roster and same-window inspector. Back to
team clears only the observer selection. Manual projects, task list and details
occupy separate same-window surfaces; Back to tasks retains task identity, and
Task details reopens it. WorkspaceController owns the manual detail selection;
any editor stays visible on the compact manual surface and returning from Team
restores that editor, never an independently copied draft. The minimum window
remains 1000×680. Wide, normal-font surfaces retain their split layout.

Task-list project headers and connection-management headings are inside their
virtualized ListViews, so long text/large fonts cannot consume all row space.
The task header starts at the beginning on showing the list. Inspector metadata
and manual editors scroll; long description/criteria fields grow with content,
not paint outside a fixed-height box. Visible field labels remain above inputs.
Tab/Backtab leave long-text fields; focus reveals the relevant field/caret/action
in manual details, team inspector and connections. No infinite animation is used.
Long navigation/project headings may elide; full observer/project labels remain
copyable in inspector metadata and project editor. Critical action labels never
elide. Buttons measure an independent unwrapped native Text label and account
for actual horizontal padding, avoiding Basic-style short-action wrapping.

TeamOverviewModel updates changed identity ranges with row insert/remove signals,
preserving unchanged prefix/suffix identities and avoiding asynchronous-delegate
reset cancellation during rapid filtering. Same-identity snapshot/clock refreshes
use dataChanged, not a new delegate tree. Selection still uses observer/runtime IDs,
not row indices; no requests or freshness extension come from presentation ticks.

### Native matrix and provenance

responsiveNativeMatrix loads production qrc Main.qml and real C++ presentation
owners, backed only by an ephemeral fictional HTTP fixture, private HOME/XDG and
no real agterm/Pi/settings. All 24 combinations of 1000×680, 1280×820, 1440×900;
EN/RU; and UI fonts 14/21/28/32 logical units pass. Each tests 0/1/12/256 observers,
eight runtimes each, duplicate 128-unit-bound Cyrillic/non-BMP labels, 41 projects,
long paths/descriptions/criteria, no matches, offline cached fallback/recovery,
stable selection/back, retained dirty editor, hidden Save/Escape, Tab/Backtab,
accessible action names and white/black accent selection/focus contrast. Geometry
assertions include every clipping ancestor, not just the outer window. Management
Prune focus is revealed below long headers without dispatching a mutation. The
256-observer fixture has a bounded visible delegate count, not 2048 QML subtrees.

The whole test is rerun with QT_SCALE_FACTOR=1, 1.5 and 2 (actual offscreen device
pixel ratios, not a resize of a screenshot): 72 test rows / 288 roster cases.
NativePrimitives additionally retains its independent ≥4.5 text/≥3 focus contrast,
bright/dark accent, radius and keyboard checks. No host settings are changed.

84 final native PNGs were opened in three contact sheets, with additional full-size
inspection of long task-header and management renders. The complete capture naming
scheme under docs/screenshots is:

layout-dpi{1,1.5,2}-{1000-en-font14,1000-ru-font14,1280-ru-font28,1440-en-font21}-{connections,editor,inspector,projects,tasks,team,workspace}.png

Each DPI has the same four layout/language/font rows and seven states. These links
sample the inspected matrix, not substitute HTML or historical screenshots:

| Device scale | Narrow team | Long task header | Dirty editor | Connections |
| --- | --- | --- | --- | --- |
| 100% | [EN1000](docs/screenshots/layout-dpi1-1000-en-font14-team.png) | [RU1280/font28](docs/screenshots/layout-dpi1-1280-ru-font28-tasks.png) | [RU1000](docs/screenshots/layout-dpi1-1000-ru-font14-editor.png) | [EN1440/font21](docs/screenshots/layout-dpi1-1440-en-font21-connections.png) |
| 150% | [EN1000](docs/screenshots/layout-dpi1.5-1000-en-font14-team.png) | [RU1280/font28](docs/screenshots/layout-dpi1.5-1280-ru-font28-tasks.png) | [RU1000](docs/screenshots/layout-dpi1.5-1000-ru-font14-editor.png) | [EN1440/font21](docs/screenshots/layout-dpi1.5-1440-en-font21-connections.png) |
| 200% | [EN1000](docs/screenshots/layout-dpi2-1000-en-font14-team.png) | [RU1280/font28](docs/screenshots/layout-dpi2-1280-ru-font28-tasks.png) | [RU1000](docs/screenshots/layout-dpi2-1000-ru-font14-editor.png) | [EN1440/font21](docs/screenshots/layout-dpi2-1440-en-font21-connections.png) |

Review corrected short-action wrapping, header clipping/scroll-origin handling,
large-text overflow and premature captures during the existing drawer transition.
At larger fonts headers/metadata intentionally require scrolling; ordinary details
stay in the same window and the explicit Back remains available at the top. This
is automated offscreen Qt/software mouse/keyboard/accessibility-interface evidence,
not a physical monitor/compositor or screen-reader certification. No real user
services/sessions, current system theme or data were read or modified.

Reproduce using the private-HOME invocation above with selector
responsiveNativeMatrix, QT_SCALE_FACTOR=1 (then 1.5 and 2), and
ARTEL_LAYOUT_SCREENSHOT="$ROOT/docs/screenshots/layout-dpi1" (matching each scale).
All 24 rows execute even when no capture prefix is supplied. Task6 gates:
make lint test build and make observer-check PASS; Qt86+32, qmllint zero warnings,
Go1.26.3/golangci-lint2.10.1/Qt6.12.0/clang-format23.1.3/Pi1.5.2/Node22.23.2.
Final full-stack/package acceptance remains Task7; no such completion is claimed.
