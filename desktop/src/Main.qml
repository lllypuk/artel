pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts
import Artel
import "ui" as UI

ApplicationWindow {
    id: window
    required property Panel panel
    required property Appearance appearance
    required property WorkspaceController workspace
    property bool allowClose: false
    objectName: "mainWindow"
    width: 1280
    height: 820
    minimumWidth: 1000
    minimumHeight: 680
    visible: true
    title: qsTr("Artel — team and projects")
    color: appTheme.canvas
    font.pixelSize: appTheme.body
    palette.window: appTheme.canvas
    palette.windowText: appTheme.text
    palette.base: appTheme.panel
    palette.text: appTheme.text
    palette.button: appTheme.raised
    palette.buttonText: appTheme.text
    palette.highlight: appTheme.selected
    palette.highlightedText: appTheme.selectedText
    Material.theme: Material.Dark
    Material.accent: appearance.accent
    UI.Theme {
        id: appTheme
        objectName: "appTheme"
        appearance: window.appearance
    }
    Binding {
        target: window.workspace
        property: "windowVisible"
        value: window.visible
    }
    Binding {
        target: window.workspace
        property: "minimized"
        value: window.visibility === Window.Minimized
    }
    onClosing: close => {
        if (!workspace.closeAllowed) {
            close.accepted = allowClose;
            if (!allowClose)
                closeDialog.open();
        }
    }
    Shortcut {
        sequences: [StandardKey.Save]
        onActivated: window.workspace.saveShortcut()
    }
    Shortcut {
        sequences: [StandardKey.Cancel]
        enabled: window.workspace.route === "workspace"
        onActivated: {
            if (window.panel.dirty)
                discardDialog.open();
            else
                window.workspace.escapeShortcut();
        }
    }
    Shortcut {
        sequences: [StandardKey.Quit]
        onActivated: window.close()
    }
    Dialog {
        id: closeDialog
        objectName: "closeDialog"
        title: qsTr("Close with unsaved changes?")
        width: Math.min(600, window.width - 40)
        anchors.centerIn: parent
        modal: true
        standardButtons: Dialog.Discard | Dialog.Cancel
        onDiscarded: {
            window.allowClose = true;
            window.close();
        }
        Label {
            width: parent.width
            wrapMode: Text.Wrap
            text: qsTr("Draft will be lost. An unknown save may already be committed.")
        }
    }
    Dialog {
        id: discardDialog
        objectName: "discardDialog"
        title: qsTr("Discard draft?")
        width: Math.min(480, window.width - 40)
        anchors.centerIn: parent
        modal: true
        standardButtons: Dialog.Discard | Dialog.Cancel
        onDiscarded: window.panel.discard()
    }
    Dialog {
        id: abandonDialog
        objectName: "abandonDialog"
        title: qsTr("Abandon the old save?")
        width: Math.min(600, window.width - 40)
        anchors.centerIn: parent
        modal: true
        standardButtons: Dialog.Yes | Dialog.Cancel
        onAccepted: window.panel.abandonUncertain()
        Label {
            width: parent.width
            wrapMode: Text.Wrap
            text: qsTr("The save may have committed in the old database. It will not be retried in the replacement database. The draft stays here: copy it before discarding it to continue.")
        }
    }
    Drawer {
        id: observerDrawer
        objectName: "observerDrawer"
        edge: Qt.RightEdge
        width: Math.min(680, window.width - 40)
        height: window.height
        padding: 0
        background: Rectangle {
            color: appTheme.panel
        }
        Overlay.modal: Rectangle {
            color: Qt.rgba(appTheme.canvas.r, appTheme.canvas.g, appTheme.canvas.b, 0.7)
        }
        onOpened: window.workspace.connectionsOpen = true
        onClosed: window.workspace.connectionsOpen = false
        ObserverPanel {
            anchors.fill: parent
            client: window.panel.observers
            theme: appTheme
        }
    }
    RowLayout {
        anchors.fill: parent
        spacing: 0
        Rectangle {
            Layout.preferredWidth: 184
            Layout.fillHeight: true
            color: appTheme.panel
            ColumnLayout {
                anchors.fill: parent
                anchors.margins: 12
                spacing: 8
                Label {
                    text: "Artel"
                    color: appTheme.text
                    font.pixelSize: appTheme.panelTitle
                    Layout.topMargin: 8
                    Layout.bottomMargin: 20
                }
                UI.ActionButton {
                    objectName: "teamNavButton"
                    theme: appTheme
                    text: qsTr("Team")
                    kind: window.workspace.route === "team" ? "primary" : "quiet"
                    Layout.fillWidth: true
                    onClicked: window.workspace.showTeam()
                }
                UI.ActionButton {
                    objectName: "projectsNavButton"
                    theme: appTheme
                    text: qsTr("Projects")
                    kind: window.workspace.route !== "team" ? "primary" : "quiet"
                    Layout.fillWidth: true
                    onClicked: window.workspace.showProjects()
                }
                Rectangle {
                    Layout.fillWidth: true
                    Layout.topMargin: 12
                    Layout.bottomMargin: 8
                    implicitHeight: 1
                    color: appTheme.border
                }
                ListView {
                    id: navProjects
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    clip: true
                    spacing: 4
                    model: window.panel.projects
                    delegate: UI.ActionButton {
                        id: navProject
                        required property var modelData
                        theme: appTheme
                        width: navProjects.width
                        text: navProject.modelData.name
                        elideText: true
                        kind: "quiet"
                        objectName: "navProject_" + navProject.modelData.id
                        enabled: !window.panel.dirty && !window.panel.busy && !window.panel.uncertain
                        onClicked: window.workspace.openProject(navProject.modelData.id)
                    }
                    ScrollBar.vertical: ScrollBar {}
                }
                UI.ActionButton {
                    objectName: "connectionsNavButton"
                    theme: appTheme
                    text: qsTr("Connections")
                    kind: "quiet"
                    Layout.fillWidth: true
                    onClicked: observerDrawer.open()
                }
            }
        }
        Rectangle {
            Layout.fillHeight: true
            implicitWidth: 1
            color: appTheme.border
        }
        ColumnLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            spacing: 0
            Rectangle {
                visible: window.workspace.draftHidden
                Layout.fillWidth: true
                implicitHeight: draftRow.implicitHeight + 16
                color: appTheme.raised
                RowLayout {
                    id: draftRow
                    anchors.fill: parent
                    anchors.margins: 8
                    Label {
                        text: qsTr("Draft kept in this window")
                        color: appTheme.text
                        wrapMode: Text.Wrap
                        Layout.fillWidth: true
                    }
                    UI.ActionButton {
                        objectName: "returnToEditorButton"
                        theme: appTheme
                        text: qsTr("Return to editing")
                        onClicked: window.workspace.returnToEditor()
                    }
                }
            }
            TeamOverview {
                objectName: "teamOverview"
                Layout.fillWidth: true
                Layout.fillHeight: true
                visible: window.workspace.route === "team"
                workspace: window.workspace
                client: window.panel.observers
                projects: window.panel.projects
                theme: appTheme
                onOpenConnections: observerDrawer.open()
            }
            ManualWorkspace {
                Layout.fillWidth: true
                Layout.fillHeight: true
                visible: window.workspace.route !== "team"
                panel: window.panel
                appearance: window.appearance
                workspace: window.workspace
                onOpenConnections: observerDrawer.open()
                onDiscardRequested: discardDialog.open()
                onAbandonRequested: abandonDialog.open()
            }
        }
    }
}
