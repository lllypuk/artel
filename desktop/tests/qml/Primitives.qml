pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts
import Artel
import "qrc:/ui" as UI

ApplicationWindow {
    id: window
    required property Appearance appearance
    property real fontScale: 1
    width: 800
    height: 700
    visible: true
    color: tokens.canvas
    UI.Theme {
        id: tokens
        objectName: "theme"
        appearance: window.appearance
        fontScale: window.fontScale
    }
    ColumnLayout {
        anchors.fill: parent
        anchors.margins: tokens.space6
        spacing: tokens.space4
        UI.Section {
            theme: tokens
            title: "Artel"
            Layout.fillWidth: true
            UI.FilterBar {
                theme: tokens
                label: qsTr("Projects")
                Layout.fillWidth: true
            }
            Label {
                text: qsTr("Description")
                color: tokens.text
                font.pixelSize: tokens.body
            }
            UI.LongText {
                objectName: "longText"
                theme: tokens
                text: "Fictional fixture — Кириллица 🌿\n<plain text, not HTML>"
                Layout.fillWidth: true
            }
            RowLayout {
                UI.ActionButton {
                    objectName: "primary"
                    theme: tokens
                    kind: "primary"
                    text: qsTr("Save")
                }
                UI.ActionButton {
                    objectName: "secondary"
                    theme: tokens
                    text: qsTr("Cancel")
                }
                UI.ActionButton {
                    objectName: "quiet"
                    theme: tokens
                    kind: "quiet"
                    text: qsTr("Edit task")
                }
            }
            RowLayout {
                UI.ActionButton {
                    objectName: "destructive"
                    theme: tokens
                    kind: "destructive"
                    text: qsTr("Cancel task")
                }
                UI.ActionButton {
                    objectName: "disabled"
                    theme: tokens
                    text: qsTr("Save")
                    enabled: false
                }
            }
            UI.StatusLabel {
                theme: tokens
                severity: "warning"
                text: qsTr("Offline — cached data is read-only")
                Layout.fillWidth: true
            }
        }
        UI.EmptyState {
            theme: tokens
            title: qsTr("No task selected")
            explanation: qsTr("Description")
            Layout.fillWidth: true
        }
        Item {
            Layout.fillHeight: true
        }
    }
}
