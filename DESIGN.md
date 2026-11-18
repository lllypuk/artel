# Artel desktop design system — provisional native primitives

Task 1 of the desktop redesign, 2026-11-18. This describes implemented reusable
primitives, not a completed redesign. The existing Main.qml startup, manual flows
and ObserverPanel are unchanged; shell, roster and workspace migration follow in
later tasks. Mode: Operate, native Qt Quick, canon direction with Zed-quality
hierarchy/density rather than editor imitation.

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
