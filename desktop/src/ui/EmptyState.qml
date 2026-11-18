pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts

ColumnLayout {
    id: control
    required property Theme theme
    required property string title
    property string explanation: ""
    property bool error: false
    spacing: theme.space2
    Label {
        text: control.title
        color: control.error ? control.theme.critical : control.theme.text
        font.family: control.theme.systemFont.family
        font.pixelSize: control.theme.panelTitle
        textFormat: Text.PlainText
        wrapMode: Text.Wrap
        Layout.fillWidth: true
    }
    StatusLabel {
        theme: control.theme
        text: control.explanation
        visible: text.length > 0
        Layout.fillWidth: true
    }
}
