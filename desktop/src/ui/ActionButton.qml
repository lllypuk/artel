pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

Button {
    id: control
    required property Theme theme
    // primary | secondary | quiet | destructive
    property string kind: "secondary"
    readonly property color surface: !enabled ? theme.panel : down ? theme.raised : kind === "primary" ? theme.selected : hovered ? theme.raised : kind === "quiet" ? "transparent" : theme.panel
    readonly property color foreground: !enabled ? theme.disabled : down ? theme.text : kind === "primary" ? theme.selectedText : kind === "destructive" ? theme.critical : theme.text
    readonly property color focusColor: kind === "primary" && enabled && !down ? theme.selectedText : theme.focus
    implicitWidth: Math.max(80, contentItem.implicitWidth + 24)
    implicitHeight: Math.max(theme.controlHeight, contentItem.implicitHeight + 16)
    padding: theme.space3
    verticalPadding: theme.space2
    font.family: theme.systemFont.family
    font.pixelSize: theme.body
    focusPolicy: Qt.StrongFocus
    Accessible.name: text
    contentItem: Text {
        text: control.text
        font: control.font
        color: control.foreground
        horizontalAlignment: Text.AlignHCenter
        verticalAlignment: Text.AlignVCenter
        textFormat: Text.PlainText
    }
    background: Rectangle {
        color: control.surface
        radius: control.theme.radius
        border.width: control.visualFocus ? 2 : 1
        border.color: control.visualFocus ? control.focusColor : control.kind === "quiet" ? "transparent" : control.theme.border
    }
}
