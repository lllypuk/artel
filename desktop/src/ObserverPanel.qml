pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Artel

Pane {
    id: root
    required property ObserverClient client
    objectName: "observerPanel"
    padding: 16
    ColumnLayout {
        anchors.fill: parent
        spacing: 10
        Label {
            text: qsTr("Pi observations")
            font.pixelSize: 24
        }
        Label {
            text: qsTr("Pi activity does not mean a task is complete.")
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }
        Label {
            text: qsTr("Provision credentials with artelctl observer provision. This panel never accepts secrets.")
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }
        RowLayout {
            ComboBox {
                id: scope
                objectName: "observerScope"
                model: [qsTr("All observers and candidates"), qsTr("Selected project")]
                currentIndex: root.client.onlyProject ? 1 : 0
                onActivated: root.client.onlyProject = currentIndex === 1
                Layout.fillWidth: true
                Accessible.name: qsTr("Observation scope")
            }
            Button {
                objectName: "observerRefresh"
                text: qsTr("Refresh")
                enabled: !root.client.busy
                onClicked: root.client.reload()
            }
        }
        Label {
            text: qsTr("Project: %1").arg(root.client.projectName || "—")
            textFormat: Text.PlainText
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }
        Label {
            objectName: "observerConnection"
            text: root.client.online ? qsTr("Observation online") : qsTr("Observation offline / unknown")
            Accessible.name: text
        }
        Label {
            objectName: "observerMessage"
            text: root.client.message
            textFormat: Text.PlainText
            visible: text.length > 0
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }
        Label {
            visible: !root.client.canBind
            text: qsTr("Select an active project to bind a runtime. Manual editor selection guards still apply.")
            wrapMode: Text.Wrap
            Layout.fillWidth: true
        }
        ListView {
            id: list
            objectName: "observerList"
            Layout.fillHeight: true
            Layout.fillWidth: true
            clip: true
            spacing: 16
            model: root.client.rows
            ScrollBar.vertical: ScrollBar {}
            Label {
                anchors.centerIn: parent
                visible: list.count === 0
                text: root.client.onlyProject ? qsTr("No observation is bound to the selected project.") : qsTr("No observers. Provision one using the CLI.")
                width: parent.width
                wrapMode: Text.Wrap
            }
            delegate: ColumnLayout {
                id: row
                required property var modelData
                width: list.width - 16
                spacing: 4
                Label {
                    text: row.modelData.label
                    textFormat: Text.PlainText
                    font.bold: true
                    Layout.fillWidth: true
                    wrapMode: Text.Wrap
                }
                Label {
                    text: row.modelData.status_text
                    textFormat: Text.PlainText
                    Layout.fillWidth: true
                    wrapMode: Text.Wrap
                }
                Label {
                    text: qsTr("Observer: %1\nRuntime: %2\nPi session: %3\nPi / bridge: %4 / %5\nProvider / model: %6 / %7\nLast seen: %8").arg(row.modelData.observer_id).arg(row.modelData.runtime_id || "—").arg(row.modelData.pi_session_id || "—").arg(row.modelData.pi_version || "—").arg(row.modelData.bridge_version || "—").arg(row.modelData.provider || "—").arg(row.modelData.model || "—").arg(row.modelData.last_seen || "—")
                    textFormat: Text.PlainText
                    wrapMode: Text.WrapAnywhere
                    Layout.fillWidth: true
                }
                Flow {
                    Layout.fillWidth: true
                    spacing: 6
                    Button {
                        objectName: "observerBind_" + row.modelData.runtime_id
                        text: qsTr("Bind to selected project")
                        enabled: root.client.online && !root.client.busy && row.modelData.bindable
                        onClicked: root.ask("bind", row.modelData)
                    }
                    Button {
                        objectName: "observerUnbind_" + row.modelData.runtime_id
                        text: qsTr("Unbind")
                        enabled: root.client.online && !root.client.busy && row.modelData.has_binding
                        onClicked: root.ask("unbind", row.modelData)
                    }
                    Button {
                        objectName: "observerRevoke_" + row.modelData.runtime_id
                        text: qsTr("Revoke")
                        enabled: root.client.online && !root.client.busy && !row.modelData.revoked
                        onClicked: root.ask("revoke", row.modelData)
                    }
                    Button {
                        objectName: "observerPrune_" + row.modelData.runtime_id
                        text: qsTr("Prune inactive runtimes")
                        enabled: root.client.online && !root.client.busy
                        onClicked: root.ask("prune", row.modelData)
                    }
                }
            }
        }
    }
    function ask(action: string, record: var) {
        if (client.prepare(action, record.observer_id, record.runtime_id || ""))
            confirmationDialog.open();
    }
    Dialog {
        id: confirmationDialog
        objectName: "observerConfirmation"
        title: qsTr("Confirm observation change")
        anchors.centerIn: parent
        width: Math.min(540, root.width - 32)
        modal: true
        standardButtons: Dialog.Yes | Dialog.Cancel
        onAccepted: root.client.confirm()
        onRejected: root.client.cancelConfirmation()
        Label {
            objectName: "observerConfirmationText"
            text: root.client.confirmation
            textFormat: Text.PlainText
            width: parent.width
            wrapMode: Text.Wrap
        }
    }
}
