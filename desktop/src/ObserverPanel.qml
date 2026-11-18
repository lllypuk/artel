pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts
import Artel
import "ui" as UI

Pane {
    id: root
    required property ObserverClient client
    required property UI.Theme theme
    objectName: "observerPanel"
    onVisibleChanged: {
        if (visible)
            Qt.callLater(list.positionViewAtBeginning);
    }
    function revealControl(control: Item) {
        if (!visible || !control)
            return;
        let ancestor = control.parent;
        while (ancestor && ancestor !== list.contentItem)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        const top = control.mapToItem(list.contentItem, 0, 0).y;
        const bottom = top + Math.min(control.height, root.theme.controlHeight);
        if (top < list.contentY)
            list.contentY = top - 8;
        else if (bottom > list.contentY + list.height)
            list.contentY = bottom - list.height + 8;
    }
    Connections {
        target: root.Window.window
        function onActiveFocusItemChanged() {
            root.revealControl(root.Window.window.activeFocusItem);
        }
    }
    padding: 24
    palette.windowText: theme.text
    palette.text: theme.text
    font.pixelSize: theme.body
    background: Rectangle {
        color: root.theme.panel
    }
    ListView {
        id: list
        objectName: "observerList"
        anchors.fill: parent
        clip: true
        spacing: 16
        model: root.client.connectionRows
        ScrollBar.vertical: ScrollBar {}
        header: ColumnLayout {
            width: list.width
            spacing: 12
            Label {
                text: qsTr("Manage connections")
                font.pixelSize: root.theme.screenTitle
                color: root.theme.text
            }
            UI.StatusLabel {
                theme: root.theme
                text: qsTr("Pi activity does not mean a task is complete.")
                Layout.fillWidth: true
            }
            Label {
                text: qsTr("Provision credentials with artelctl observer provision. This panel never accepts secrets.")
                color: root.theme.muted
                wrapMode: Text.Wrap
                Layout.fillWidth: true
            }
            GridLayout {
                columns: root.theme.body > 18 ? 1 : 3
                Layout.fillWidth: true
                Label {
                    text: qsTr("Observation scope")
                    color: root.theme.muted
                }
                ComboBox {
                    objectName: "observerScope"
                    model: [qsTr("All observers and candidates"), qsTr("Selected project")]
                    currentIndex: root.client.onlyProject ? 1 : 0
                    onActivated: root.client.onlyProject = currentIndex === 1
                    Layout.fillWidth: true
                    Accessible.name: qsTr("Observation scope")
                }
                UI.ActionButton {
                    theme: root.theme
                    objectName: "observerRefresh"
                    text: qsTr("Refresh")
                    enabled: !root.client.busy
                    onClicked: root.client.reload()
                }
            }
            GridLayout {
                columns: root.theme.body > 18 ? 1 : 2
                Layout.fillWidth: true
                Label {
                    text: qsTr("Connection records")
                    color: root.theme.muted
                }
                ComboBox {
                    objectName: "observerRecordFilter"
                    model: [qsTr("All observers and candidates"), qsTr("Current binding"), qsTr("Candidate runtime"), qsTr("Connection history")]
                    currentIndex: ["", "bound", "candidate", "history"].indexOf(root.client.viewFilter)
                    onActivated: root.client.viewFilter = ["", "bound", "candidate", "history"][currentIndex]
                    Accessible.name: qsTr("Connection records")
                    Layout.fillWidth: true
                }
            }
            Label {
                text: qsTr("Project: %1").arg(root.client.projectName || "—")
                textFormat: Text.PlainText
                color: root.theme.muted
                wrapMode: Text.Wrap
                Layout.fillWidth: true
            }
            UI.StatusLabel {
                theme: root.theme
                objectName: "observerConnection"
                text: root.client.online ? qsTr("Observation online") : qsTr("Observation offline / unknown")
                severity: root.client.online ? "neutral" : "warning"
                Layout.fillWidth: true
            }
            UI.StatusLabel {
                theme: root.theme
                objectName: "observerMessage"
                text: root.client.message
                visible: text.length > 0
                severity: "warning"
                Layout.fillWidth: true
            }
            UI.StatusLabel {
                theme: root.theme
                visible: !root.client.canBind
                text: qsTr("Select an active project to bind a runtime. Manual editor selection guards still apply.")
                Layout.fillWidth: true
            }
            UI.StatusLabel {
                theme: root.theme
                objectName: "navigationMessage"
                text: root.client.navigation.message
                visible: text.length > 0
                Layout.fillWidth: true
            }
            UI.EmptyState {
                objectName: "observerEmptyState"
                Layout.fillWidth: true
                visible: list.count === 0
                theme: root.theme
                title: qsTr("No matching connection records")
                explanation: qsTr("Change the connection filters, or provision an observer using the CLI.")
            }
        }
        delegate: ColumnLayout {
            id: row
            required property var record
            readonly property var modelData: record
            property bool expanded: false
            width: list.width - 16
            spacing: 8
            Rectangle {
                Layout.fillWidth: true
                implicitHeight: 1
                color: root.theme.border
            }
            RowLayout {
                Layout.fillWidth: true
                Label {
                    text: row.modelData.label
                    color: root.theme.text
                    textFormat: Text.PlainText
                    font.bold: true
                    Layout.fillWidth: true
                    wrapMode: Text.Wrap
                }
                UI.ActionButton {
                    theme: root.theme
                    kind: "quiet"
                    objectName: "observerMetadataToggle_" + row.modelData.runtime_id
                    text: row.expanded ? qsTr("Hide metadata") : qsTr("Show metadata")
                    onClicked: row.expanded = !row.expanded
                }
            }
            Label {
                text: row.modelData.category_text
                color: root.theme.muted
                textFormat: Text.PlainText
                Layout.fillWidth: true
            }
            UI.StatusLabel {
                theme: root.theme
                text: row.modelData.status_text
                Layout.fillWidth: true
            }
            UI.LongText {
                objectName: "observerMetadata_" + row.modelData.runtime_id
                visible: row.expanded
                theme: root.theme
                readOnly: true
                text: qsTr("Observer: %1\nRuntime: %2\nPi session: %3\nPi / bridge: %4 / %5\nProvider / model: %6 / %7").arg(row.modelData.observer_id).arg(row.modelData.runtime_id || "—").arg(row.modelData.pi_session_id || "—").arg(row.modelData.pi_version || "—").arg(row.modelData.bridge_version || "—").arg(row.modelData.provider || "—").arg(row.modelData.model || "—")
                Accessible.name: qsTr("Connection metadata")
                Layout.fillWidth: true
                wrapMode: Text.WrapAnywhere
            }
            Label {
                visible: row.expanded
                text: qsTr("Last seen: %1").arg(row.record.last_seen || "—")
                textFormat: Text.PlainText
                color: root.theme.muted
                wrapMode: Text.WrapAnywhere
                Layout.fillWidth: true
            }
            Flow {
                Layout.fillWidth: true
                spacing: 8
                UI.ActionButton {
                    theme: root.theme
                    objectName: "observerOpen_" + row.modelData.runtime_id
                    text: qsTr("Open declared agterm session")
                    enabled: row.modelData.navigable && !root.client.navigation.busy
                    onClicked: root.client.openSession(row.modelData.observer_id, row.modelData.runtime_id)
                }
                UI.ActionButton {
                    theme: root.theme
                    objectName: "observerBind_" + row.modelData.runtime_id
                    text: qsTr("Bind to selected project")
                    enabled: root.client.online && !root.client.busy && row.modelData.bindable
                    onClicked: root.ask("bind", row.modelData)
                }
                UI.ActionButton {
                    theme: root.theme
                    kind: "quiet"
                    objectName: "observerUnbind_" + row.modelData.runtime_id
                    text: qsTr("Unbind")
                    enabled: root.client.online && !root.client.busy && row.modelData.has_binding && !row.modelData.revoked
                    onClicked: root.ask("unbind", row.modelData)
                }
                UI.ActionButton {
                    theme: root.theme
                    kind: "destructive"
                    objectName: "observerRevoke_" + row.modelData.runtime_id
                    text: qsTr("Revoke")
                    enabled: root.client.online && !root.client.busy && !row.modelData.revoked
                    onClicked: root.ask("revoke", row.modelData)
                }
                UI.ActionButton {
                    theme: root.theme
                    kind: "quiet"
                    objectName: "observerPrune_" + row.modelData.runtime_id
                    text: qsTr("Prune runtime records")
                    enabled: root.client.online && !root.client.busy && row.modelData.prunable
                    onClicked: root.ask("prune", row.modelData)
                }
            }
        }
        footer: Label {
            width: list.width
            visible: list.count > 0
            text: qsTr("Terminal hints may be inherited and are unverified. Opens a session only; exact pane focus is not guaranteed.")
            color: root.theme.muted
            font.pixelSize: root.theme.secondary
            wrapMode: Text.Wrap
            Layout.fillWidth: true
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
        Overlay.modal: Rectangle {
            color: Qt.rgba(root.theme.canvas.r, root.theme.canvas.g, root.theme.canvas.b, 0.7)
        }
        background: Rectangle {
            color: root.theme.panel
            border.color: root.theme.border
            radius: root.theme.radius
        }
        footer: DialogButtonBox {
            UI.ActionButton {
                theme: root.theme
                objectName: "observerConfirmAction"
                text: qsTr("Confirm observation change")
                DialogButtonBox.buttonRole: DialogButtonBox.AcceptRole
            }
            UI.ActionButton {
                theme: root.theme
                objectName: "observerCancelAction"
                text: qsTr("Cancel")
                DialogButtonBox.buttonRole: DialogButtonBox.RejectRole
            }
            background: Rectangle {
                color: root.theme.panel
            }
        }
        onAccepted: root.client.confirm()
        onRejected: root.client.cancelConfirmation()
        contentItem: ScrollView {
            id: confirmationScroll
            clip: true
            implicitHeight: Math.min(confirmationText.implicitHeight, Math.max(80, root.height - 180))
            contentWidth: availableWidth
            ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
            UI.StatusLabel {
                id: confirmationText
                theme: root.theme
                objectName: "observerConfirmationText"
                text: root.client.confirmation
                width: confirmationScroll.availableWidth
            }
        }
    }
}
