pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

TextField {
    id: control
    required property Theme theme
    implicitWidth: 240
    implicitHeight: Math.max(theme.controlHeight, contentHeight + 16)
    padding: theme.space2
    color: enabled ? theme.text : theme.disabled
    placeholderTextColor: theme.muted
    selectionColor: theme.selected
    selectedTextColor: theme.selectedText
    font.family: theme.systemFont.family
    font.pixelSize: theme.body
    selectByMouse: true
    background: Rectangle {
        color: control.theme.canvas
        radius: control.theme.radius
        border.width: control.activeFocus ? 2 : 1
        border.color: control.activeFocus ? control.theme.focus : control.theme.border
    }
}
