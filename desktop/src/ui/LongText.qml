pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

TextArea {
    id: control
    required property Theme theme
    implicitWidth: 320
    implicitHeight: Math.max(100, contentHeight + 24)
    padding: theme.space3
    color: enabled ? theme.text : theme.disabled
    placeholderTextColor: theme.muted
    selectionColor: theme.selected
    selectedTextColor: theme.selectedText
    font.family: theme.systemFont.family
    font.pixelSize: theme.body
    textFormat: TextEdit.PlainText
    wrapMode: TextEdit.Wrap
    selectByMouse: true
    Keys.onTabPressed: event => {
        nextItemInFocusChain(true).forceActiveFocus(Qt.TabFocusReason);
        event.accepted = true;
    }
    Keys.onBacktabPressed: event => {
        nextItemInFocusChain(false).forceActiveFocus(Qt.BacktabFocusReason);
        event.accepted = true;
    }
    background: Rectangle {
        color: control.theme.canvas
        radius: control.theme.radius
        border.width: control.activeFocus ? 2 : 1
        border.color: control.activeFocus ? control.theme.focus : control.theme.border
    }
}
