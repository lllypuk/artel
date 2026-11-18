pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts

Pane {
    id: control
    required property Theme theme
    property string title: ""
    default property alias body: bodyLayout.data
    padding: theme.space4
    background: Rectangle {
        color: control.theme.panel
        border.color: control.theme.border
    }
    contentItem: ColumnLayout {
        spacing: control.theme.space3
        Label {
            text: control.title
            visible: text.length > 0
            color: control.theme.text
            font.family: control.theme.systemFont.family
            font.pixelSize: control.theme.panelTitle
            textFormat: Text.PlainText
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }
        ColumnLayout {
            id: bodyLayout
            spacing: control.theme.space2
            Layout.fillWidth: true
        }
    }
}
