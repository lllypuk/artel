pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts
import Artel

ApplicationWindow {
    id: window
    required property Panel panel
    required property Appearance appearance
    property bool allowClose: false
    objectName: "mainWindow"
    width: 1280
    height: 820
    minimumWidth: 1000
    minimumHeight: 680
    visible: true
    title: qsTr("Artel — manual project panel")
    Material.theme: Material.Dark
    Material.accent: appearance.accent
    Material.roundedScale: appearance.radius >= 12 ? Material.MediumScale : appearance.radius >= 8 ? Material.SmallScale : appearance.radius > 0 ? Material.ExtraSmallScale : Material.NotRounded
    component PanelButton: Button {
        Material.roundedScale: window.Material.roundedScale
    }
    onClosing: close => {
        if (panel.dirty || panel.uncertain) {
            close.accepted = allowClose;
            if (!allowClose)
                closeDialog.open();
        }
    }
    Shortcut {
        sequences: [StandardKey.Save]
        onActivated: window.panel.save()
    }
    Shortcut {
        sequences: [StandardKey.Cancel]
        onActivated: {
            if (window.panel.dirty)
                discardDialog.open();
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
    ColumnLayout {
        anchors.fill: parent
        anchors.margins: 16
        spacing: 10
        RowLayout {
            Label {
                text: qsTr("Artel")
                font.pixelSize: 26
            }
            Label {
                objectName: "connectionLabel"
                text: window.panel.connection
                Layout.fillWidth: true
            }
            Label {
                text: qsTr("Updated: %1").arg(window.panel.updated)
            }
            PanelButton {
                objectName: "reloadButton"
                text: qsTr("Reload")
                onClicked: window.panel.reload()
            }
        }
        Label {
            objectName: "errorLabel"
            text: window.panel.message
            textFormat: Text.PlainText
            visible: text.length > 0
            wrapMode: Text.Wrap
            Layout.fillWidth: true
            color: "#ffb4ab"
            Accessible.name: text
        }
        RowLayout {
            visible: window.panel.uncertain || window.panel.conflict
            PanelButton {
                objectName: "retryButton"
                text: qsTr("Retry same save")
                visible: window.panel.uncertain
                enabled: window.panel.retryable
                onClicked: window.panel.retry()
            }
            PanelButton {
                objectName: "abandonButton"
                text: qsTr("Abandon old save, keep draft")
                visible: window.panel.canAbandon
                enabled: window.panel.canAbandon
                onClicked: abandonDialog.open()
            }
            PanelButton {
                objectName: "rebaseButton"
                text: qsTr("Use reloaded version, keep draft")
                visible: window.panel.conflict && window.panel.dirty
                enabled: window.panel.writable
                onClicked: window.panel.rebase()
            }
        }
        RowLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            spacing: 16
            ColumnLayout {
                Layout.preferredWidth: 260
                Layout.fillHeight: true
                Label {
                    text: qsTr("Projects")
                    font.pixelSize: 20
                }
                CheckBox {
                    objectName: "archivedFilter"
                    text: qsTr("Include archived")
                    checked: window.panel.showArchived
                    onToggled: window.panel.showArchived = checked
                }
                PanelButton {
                    objectName: "createProjectButton"
                    text: qsTr("Create project")
                    enabled: window.panel.writable && !window.panel.dirty
                    onClicked: window.panel.editProject(true)
                }
                ListView {
                    id: projectList
                    objectName: "projectList"
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    clip: true
                    spacing: 8
                    model: window.panel.projects
                    delegate: ItemDelegate {
                        id: projectRow
                        required property var modelData
                        width: projectList.width
                        height: 120
                        objectName: "project_" + projectRow.modelData.id
                        highlighted: window.panel.project.id === projectRow.modelData.id
                        enabled: !window.panel.dirty && !window.panel.uncertain && !window.panel.busy
                        Accessible.name: projectRow.modelData.name
                        onClicked: window.panel.selectProject(projectRow.modelData.id)
                        contentItem: ColumnLayout {
                            Label {
                                text: projectRow.modelData.name
                                textFormat: Text.PlainText
                                elide: Text.ElideRight
                                Layout.fillWidth: true
                                font.bold: true
                            }
                            Label {
                                text: projectRow.modelData.counts
                                wrapMode: Text.Wrap
                                Layout.fillWidth: true
                                font.pixelSize: 12
                            }
                            Label {
                                text: projectRow.modelData.updated_at
                                font.pixelSize: 11
                                elide: Text.ElideRight
                                Layout.fillWidth: true
                            }
                        }
                    }
                }
            }
            ColumnLayout {
                Layout.preferredWidth: 330
                Layout.fillHeight: true
                visible: !!window.panel.project.id
                Label {
                    objectName: "projectName"
                    text: window.panel.project.name || ""
                    textFormat: Text.PlainText
                    font.pixelSize: 20
                    wrapMode: Text.Wrap
                    Layout.fillWidth: true
                }
                Label {
                    text: window.panel.project.root_path || ""
                    textFormat: Text.PlainText
                    elide: Text.ElideMiddle
                    Layout.fillWidth: true
                }
                Label {
                    text: window.panel.project.description || ""
                    textFormat: Text.PlainText
                    wrapMode: Text.Wrap
                    maximumLineCount: 3
                    elide: Text.ElideRight
                    Layout.fillWidth: true
                }
                Label {
                    objectName: "pathWarning"
                    text: qsTr("Path unavailable")
                    visible: window.panel.project.pathAvailable === false
                    color: "#ffb4ab"
                }
                Label {
                    text: qsTr("Archived — read-only")
                    visible: !!window.panel.project.archived
                }
                RowLayout {
                    PanelButton {
                        objectName: "editProjectButton"
                        text: qsTr("Edit project")
                        enabled: window.panel.writable && !window.panel.dirty && !window.panel.project.archived
                        onClicked: window.panel.editProject(false)
                    }
                    PanelButton {
                        objectName: "archiveButton"
                        text: window.panel.project.archived ? qsTr("Unarchive") : qsTr("Archive")
                        enabled: window.panel.canArchive
                        onClicked: window.panel.archive()
                    }
                }
                PanelButton {
                    objectName: "createTaskButton"
                    text: qsTr("Create task")
                    enabled: window.panel.writable && !window.panel.dirty && !window.panel.project.archived
                    onClicked: window.panel.editTask(true)
                }
                ListView {
                    id: taskList
                    objectName: "taskList"
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    clip: true
                    model: window.panel.tasks
                    spacing: 5
                    delegate: ItemDelegate {
                        id: taskRow
                        required property var modelData
                        width: taskList.width
                        height: taskRow.modelData.sectionStart ? 122 : 100
                        objectName: "task_" + taskRow.modelData.id
                        highlighted: window.panel.task.id === taskRow.modelData.id
                        enabled: !window.panel.dirty && !window.panel.uncertain && !window.panel.busy
                        onClicked: window.panel.selectTask(taskRow.modelData.id)
                        Accessible.name: taskRow.modelData.title
                        contentItem: ColumnLayout {
                            Label {
                                text: taskRow.modelData.stateLabel
                                visible: !!taskRow.modelData.sectionStart
                                font.pixelSize: 12
                                color: window.appearance.accent
                            }
                            Label {
                                text: taskRow.modelData.position >= 0 ? qsTr("%1. %2").arg(taskRow.modelData.position + 1).arg(taskRow.modelData.title) : taskRow.modelData.title
                                textFormat: Text.PlainText
                                elide: Text.ElideRight
                                Layout.fillWidth: true
                            }
                            RowLayout {
                                visible: taskRow.modelData.state === "backlog" || taskRow.modelData.state === "queued"
                                enabled: window.panel.writable && !window.panel.project.archived
                                PanelButton {
                                    objectName: "queue_" + taskRow.modelData.id
                                    text: taskRow.modelData.state === "queued" ? qsTr("Remove") : qsTr("Enqueue")
                                    onClicked: window.panel.queue(taskRow.modelData.id, 0)
                                }
                                PanelButton {
                                    objectName: "up_" + taskRow.modelData.id
                                    text: qsTr("Up")
                                    enabled: taskRow.modelData.position > 0
                                    onClicked: window.panel.queue(taskRow.modelData.id, -1)
                                }
                                PanelButton {
                                    objectName: "down_" + taskRow.modelData.id
                                    text: qsTr("Down")
                                    enabled: !!taskRow.modelData.canDown
                                    onClicked: window.panel.queue(taskRow.modelData.id, 1)
                                }
                            }
                        }
                    }
                }
            }
            ScrollView {
                objectName: "detailsScroll"
                Layout.fillWidth: true
                Layout.fillHeight: true
                clip: true
                contentWidth: availableWidth
                ColumnLayout {
                    width: parent.width
                    spacing: 12
                    Label {
                        text: window.panel.editorKind === "project" ? qsTr("Project editor") : window.panel.editorKind === "task" ? qsTr("Task editor") : qsTr("Task details")
                        font.pixelSize: 20
                    }
                    ColumnLayout {
                        visible: window.panel.editorKind !== ""
                        Layout.fillWidth: true
                        TextField {
                            objectName: "nameField"
                            Layout.fillWidth: true
                            placeholderText: window.panel.editorKind === "project" ? qsTr("Project name") : qsTr("Task title")
                            Accessible.name: placeholderText
                            text: window.panel.editor.name || window.panel.editor.title || ""
                            onTextEdited: window.panel.field(window.panel.editorKind === "project" ? "name" : "title", text)
                        }
                        TextField {
                            objectName: "rootField"
                            Layout.fillWidth: true
                            visible: window.panel.editorKind === "project"
                            placeholderText: qsTr("Absolute project directory")
                            Accessible.name: placeholderText
                            text: window.panel.editor.root_path || ""
                            onTextEdited: window.panel.field("root_path", text)
                        }
                        TextArea {
                            objectName: "descriptionField"
                            Layout.fillWidth: true
                            Layout.preferredHeight: 140
                            placeholderText: qsTr("Description")
                            Accessible.name: placeholderText
                            textFormat: TextEdit.PlainText
                            wrapMode: TextEdit.Wrap
                            text: window.panel.editor.description || ""
                            onTextChanged: {
                                if (activeFocus)
                                    window.panel.field("description", text);
                            }
                        }
                        TextArea {
                            objectName: "criteriaField"
                            visible: window.panel.editorKind === "task"
                            Layout.fillWidth: true
                            Layout.preferredHeight: 140
                            placeholderText: qsTr("Acceptance criteria")
                            Accessible.name: placeholderText
                            textFormat: TextEdit.PlainText
                            wrapMode: TextEdit.Wrap
                            text: window.panel.editor.acceptance_criteria || ""
                            onTextChanged: {
                                if (activeFocus)
                                    window.panel.field("acceptance_criteria", text);
                            }
                        }
                        RowLayout {
                            PanelButton {
                                objectName: "saveButton"
                                text: qsTr("Save")
                                enabled: window.panel.saveable
                                onClicked: window.panel.save()
                            }
                            PanelButton {
                                objectName: "discardButton"
                                text: qsTr("Discard draft")
                                enabled: !window.panel.uncertain && !window.panel.busy
                                onClicked: discardDialog.open()
                            }
                        }
                        Label {
                            objectName: "conflictCurrent"
                            visible: window.panel.conflict
                            text: qsTr("Current server text: %1").arg(window.panel.currentText)
                            textFormat: Text.PlainText
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                        }
                        Label {
                            text: qsTr("Draft stays in this window until saved or discarded.")
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                        }
                    }
                    ColumnLayout {
                        visible: window.panel.editorKind === "" && !!window.panel.task.id
                        Layout.fillWidth: true
                        Label {
                            objectName: "taskTitle"
                            text: window.panel.task.title || ""
                            textFormat: Text.PlainText
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                            font.bold: true
                        }
                        Label {
                            text: window.panel.stateLabel(window.panel.task.state || "")
                        }
                        Label {
                            text: window.panel.task.description || ""
                            textFormat: Text.PlainText
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                        }
                        Label {
                            text: qsTr("Acceptance criteria")
                            font.bold: true
                        }
                        Label {
                            objectName: "taskCriteria"
                            text: window.panel.task.acceptance_criteria || ""
                            textFormat: Text.PlainText
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                        }
                        PanelButton {
                            objectName: "editTaskButton"
                            text: qsTr("Edit task")
                            enabled: window.panel.writable && !window.panel.project.archived
                            onClicked: window.panel.editTask(false)
                        }
                        Flow {
                            Layout.fillWidth: true
                            spacing: 6
                            PanelButton {
                                objectName: "startButton"
                                text: qsTr("Start manually")
                                enabled: window.panel.writable && window.panel.allowed("in_progress")
                                onClicked: window.panel.transition("in_progress")
                            }
                            PanelButton {
                                objectName: "doneButton"
                                text: qsTr("Complete manually")
                                enabled: window.panel.writable && window.panel.allowed("done")
                                onClicked: window.panel.transition("done")
                            }
                            PanelButton {
                                objectName: "cancelTaskButton"
                                text: qsTr("Cancel task")
                                enabled: window.panel.writable && window.panel.allowed("cancelled")
                                onClicked: window.panel.transition("cancelled")
                            }
                            PanelButton {
                                objectName: "reopenButton"
                                text: qsTr("Return to backlog")
                                enabled: window.panel.writable && window.panel.allowed("backlog")
                                onClicked: window.panel.transition("backlog")
                            }
                        }
                        Label {
                            text: qsTr("History — owner manual actions")
                            font.bold: true
                        }
                        Repeater {
                            model: window.panel.history
                            delegate: ColumnLayout {
                                id: auditRow
                                required property var modelData
                                Layout.fillWidth: true
                                Label {
                                    text: qsTr("%1 · %2 · #%3").arg(auditRow.modelData.timestamp).arg(auditRow.modelData.label).arg(auditRow.modelData.seq)
                                    textFormat: Text.PlainText
                                    wrapMode: Text.Wrap
                                    Layout.fillWidth: true
                                }
                                CheckBox {
                                    id: expandChange
                                    text: qsTr("Show changes")
                                    Accessible.name: text
                                }
                                ScrollView {
                                    visible: expandChange.checked
                                    Layout.fillWidth: true
                                    Layout.preferredHeight: Math.min(320, auditText.implicitHeight)
                                    TextArea {
                                        id: auditText
                                        text: auditRow.modelData.details
                                        readOnly: true
                                        selectByMouse: true
                                        textFormat: TextEdit.PlainText
                                        wrapMode: TextEdit.Wrap
                                        Accessible.name: qsTr("Changed fields")
                                    }
                                }
                            }
                        }
                        PanelButton {
                            objectName: "historyMoreButton"
                            text: qsTr("Load more history")
                            enabled: window.panel.historyMore && !window.panel.historyLoading
                            onClicked: window.panel.moreHistory()
                        }
                    }
                    Label {
                        visible: !window.panel.project.id && !window.panel.dirty
                        text: qsTr("Select a project or create one to begin. No agents are run by this panel.")
                        wrapMode: Text.Wrap
                        Layout.fillWidth: true
                    }
                }
            }
        }
    }
}
