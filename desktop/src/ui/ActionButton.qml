pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic

Button {
    id: control
    required property Theme theme
    // primary | secondary | quiet | destructive
    property string kind: "secondary"
    property bool elideText: false
    readonly property color surface: !enabled ? theme.panel : down ? theme.raised : kind === "primary" ? theme.selected : hovered ? theme.raised : kind === "quiet" ? "transparent" : theme.panel
    readonly property color foreground: !enabled ? theme.disabled : down ? theme.text : kind === "primary" ? theme.selectedText : kind === "destructive" ? theme.critical : theme.text
    readonly property color focusColor: kind === "primary" && enabled && !down ? theme.selectedText : theme.focus
    // Measure unwrapped text independently; a constrained visible label must
    // not feed its wrapped width back into the button's preferred width.
    Text {
        id: naturalLabel
        visible: false
        Accessible.ignored: true
        text: control.text
        font: control.font
        textFormat: Text.PlainText
    }
    implicitWidth: Math.max(80, Math.ceil(naturalLabel.implicitWidth) + leftPadding + rightPadding)
    implicitHeight: Math.max(theme.controlHeight, contentItem.implicitHeight + 16)
    padding: theme.space3
    horizontalPadding: theme.space3
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
        wrapMode: control.elideText ? Text.NoWrap : Text.Wrap
        elide: control.elideText ? Text.ElideRight : Text.ElideNone
    }
    background: Rectangle {
        color: control.surface
        radius: control.theme.radius
        border.width: control.visualFocus ? 2 : 1
        border.color: control.visualFocus ? control.focusColor : control.kind === "quiet" ? "transparent" : control.theme.border
    }
}
