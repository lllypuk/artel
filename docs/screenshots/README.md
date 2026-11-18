# Native screenshot evidence

All PNGs here contain fictional test data, never owner data. Qt production QML
and C++ presentation owners render through offscreen/software fixtures with private
HOME/XDG and ephemeral HTTP/daemon instances. No browser render is native evidence.
The [design system](../../DESIGN.md) records the complete matrix and capture commands;
[development](../development.md) records gates and delivery limitations.

## Current desktop

- Startup Team: [EN1280](team-overview-en-1280.png),
  [RU1440](team-overview-ru-1440.png).
- Observer details: [EN1280](team-inspector-en-1280.png),
  [RU1440](team-inspector-ru-1440.png).
- Connections: [binding](connections-en-binding.png),
  [candidate](connections-ru-candidate.png),
  [confirmation](connections-en-confirmation.png),
  [revoked](connections-ru-revoked.png), [offline](connections-ru-offline.png).
- Projects: [EN](manual-redesign-en-projects.png), [RU](manual-redesign-ru-projects.png).
- Manual workspace: [EN](manual-redesign-en-workspace.png),
  [RU](manual-redesign-ru-workspace.png).
- Editor and history: [RU editor](manual-redesign-ru-editor.png),
  [EN history](manual-redesign-en-history.png).
- Narrow 1000×680: [EN team](layout-dpi1-1000-en-font14-team.png),
  [RU inspector](layout-dpi1-1000-ru-font14-inspector.png),
  [RU retained draft](layout-dpi1-1000-ru-font14-editor.png).
- Device scale150%: [EN1440/font21 connections](layout-dpi1.5-1440-en-font21-connections.png).
- Device scale200%: [RU1280/font28 long header](layout-dpi2-1280-ru-font28-tasks.png),
  [RU1280/font28 editor](layout-dpi2-1280-ru-font28-editor.png).
- Recovery states, EN1280×820: [loading](final-state-loading.png),
  [empty](final-state-empty.png), [offline](final-state-offline.png),
  [unknown result](final-state-unknown.png).
- Conflict with retained Cyrillic draft, EN1280×820: [409](final-state-conflict.png).

The `layout-dpi{1,1.5,2}-*` set contains84 captures: four size/language/font
combinations × seven states × three device scales. The automated matrix covers
more combinations than the saved captures (72 test rows/288 roster cases).
Large-font headers and metadata intentionally scroll. Accessible names, keyboard
and contrast assertions do not certify physical compositor or screen-reader use.

## Historical evidence

[manual-panel.png](manual-panel.png) and [manual-panel-ru.png](manual-panel-ru.png)
show the pre-redesign prototype. They remain historical evidence, not the current
startup screen. The selected [canon reference](../design/reference/README.md) is
an authored HTML composition, not an application screenshot.

## Capture recovery states

After `make build test`, use a private environment, not the running owner service:

```sh
ROOT="$PWD"
FIXTURE_HOME=$(mktemp -d)
mkdir -m 700 "$FIXTURE_HOME/runtime"
(cd "$FIXTURE_HOME" && env -i PATH="$PATH" LANG=C.UTF-8 HOME="$FIXTURE_HOME" \
  XDG_RUNTIME_DIR="$FIXTURE_HOME/runtime" QT_QPA_PLATFORM=offscreen \
  QT_QUICK_BACKEND=software ARTEL_STATE_SCREENSHOT="$ROOT/docs/screenshots/final-state" \
  ARTEL_TEST_DAEMON="$ROOT/build/bin/arteld" \
  "$ROOT/desktop/build/tests/artel_tests" teamEmptyLoadingOfflineRecovery \
  unknownSaveRetryAndHistoryPagination realQmlCrudQueueConflictHistoryAndClose -maxwarnings 0)
rm -rf "$FIXTURE_HOME"
```

Capture flags are opt-in test hooks, not product functionality. No real Pi/agterm
navigation, owner token/database or desktop setting is needed. Retain full-size PNGs;
contact sheets are review aids only and are not shipped runtime resources.
