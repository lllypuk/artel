pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material
import QtQuick.Layouts
import Artel

Item {
    id: manual
    required property Panel panel
    required property Appearance appearance
    required property WorkspaceController workspace
    signal openConnections
    signal discardRequested
    signal abandonRequested
    component PanelButton: Button {
        Material.roundedScale: manual.appearance.radius >= 12 ? Material.MediumScale : manual.appearance.radius >= 8 ? Material.SmallScale : manual.appearance.radius > 0 ? Material.ExtraSmallScale : Material.NotRounded
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
                text: manual.panel.connection
                Layout.fillWidth: true
            }
            Label {
                text: qsTr("Updated: %1").arg(manual.panel.updated)
            }
            PanelButton {
                objectName: "observersButton"
                text: qsTr("Pi observations")
                onClicked: manual.openConnections()
            }
            PanelButton {
                objectName: "reloadButton"
                text: qsTr("Reload")
                onClicked: manual.panel.reload()
            }
        }
        Label {
            objectName: "errorLabel"
            text: manual.panel.message
            textFormat: Text.PlainText
            visible: text.length > 0
            wrapMode: Text.Wrap
            Layout.fillWidth: true
            color: "#ffb4ab"
            Accessible.name: text
        }
        RowLayout {
            visible: manual.panel.uncertain || manual.panel.conflict
            PanelButton {
                objectName: "retryButton"
                text: qsTr("Retry same save")
                visible: manual.panel.uncertain
                enabled: manual.panel.retryable
                onClicked: manual.panel.retry()
            }
            PanelButton {
                objectName: "abandonButton"
                text: qsTr("Abandon old save, keep draft")
                visible: manual.panel.canAbandon
                enabled: manual.panel.canAbandon
                onClicked: manual.abandonRequested()
            }
            PanelButton {
                objectName: "rebaseButton"
                text: qsTr("Use reloaded version, keep draft")
                visible: manual.panel.conflict && manual.panel.dirty
                enabled: manual.panel.writable
                onClicked: manual.panel.rebase()
            }
        }
        RowLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            spacing: 16
            ColumnLayout {
                Layout.preferredWidth: 200
                Layout.fillHeight: true
                Label {
                    text: qsTr("Projects")
                    font.pixelSize: 20
                }
                CheckBox {
                    objectName: "archivedFilter"
                    text: qsTr("Include archived")
                    checked: manual.panel.showArchived
                    onToggled: manual.panel.showArchived = checked
                }
                PanelButton {
                    objectName: "createProjectButton"
                    text: qsTr("Create project")
                    enabled: manual.panel.writable && !manual.panel.dirty
                    onClicked: {
                        manual.workspace.returnToEditor();
                        manual.panel.editProject(true);
                    }
                }
                ListView {
                    id: projectList
                    objectName: "projectList"
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    clip: true
                    spacing: 8
                    model: manual.panel.projects
                    delegate: ItemDelegate {
                        id: projectRow
                        required property var modelData
                        width: projectList.width
                        height: 120
                        objectName: "project_" + projectRow.modelData.id
                        highlighted: manual.panel.project.id === projectRow.modelData.id
                        enabled: !manual.panel.dirty && !manual.panel.uncertain && !manual.panel.busy
                        Accessible.name: projectRow.modelData.name
                        onClicked: manual.workspace.openProject(projectRow.modelData.id)
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
                Layout.preferredWidth: 280
                Layout.fillHeight: true
                visible: !!manual.panel.project.id
                Label {
                    objectName: "projectName"
                    text: manual.panel.project.name || ""
                    textFormat: Text.PlainText
                    font.pixelSize: 20
                    wrapMode: Text.Wrap
                    Layout.fillWidth: true
                }
                Label {
                    text: manual.panel.project.root_path || ""
                    textFormat: Text.PlainText
                    elide: Text.ElideMiddle
                    Layout.fillWidth: true
                }
                Label {
                    text: manual.panel.project.description || ""
                    textFormat: Text.PlainText
                    wrapMode: Text.Wrap
                    maximumLineCount: 3
                    elide: Text.ElideRight
                    Layout.fillWidth: true
                }
                Label {
                    objectName: "pathWarning"
                    text: qsTr("Path unavailable")
                    visible: manual.panel.project.pathAvailable === false
                    color: "#ffb4ab"
                }
                Label {
                    text: qsTr("Archived — read-only")
                    visible: !!manual.panel.project.archived
                }
                RowLayout {
                    PanelButton {
                        objectName: "editProjectButton"
                        text: qsTr("Edit project")
                        enabled: manual.panel.writable && !manual.panel.dirty && !manual.panel.project.archived
                        onClicked: manual.panel.editProject(false)
                    }
                    PanelButton {
                        objectName: "archiveButton"
                        text: manual.panel.project.archived ? qsTr("Unarchive") : qsTr("Archive")
                        enabled: manual.panel.canArchive
                        onClicked: manual.panel.archive()
                    }
                }
                PanelButton {
                    objectName: "createTaskButton"
                    text: qsTr("Create task")
                    enabled: manual.panel.writable && !manual.panel.dirty && !manual.panel.project.archived
                    onClicked: manual.panel.editTask(true)
                }
                ListView {
                    id: taskList
                    objectName: "taskList"
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    clip: true
                    model: manual.panel.tasks
                    spacing: 5
                    delegate: ItemDelegate {
                        id: taskRow
                        required property var modelData
                        width: taskList.width
                        height: taskRow.modelData.sectionStart ? 122 : 100
                        objectName: "task_" + taskRow.modelData.id
                        highlighted: manual.panel.task.id === taskRow.modelData.id
                        enabled: !manual.panel.dirty && !manual.panel.uncertain && !manual.panel.busy
                        onClicked: manual.panel.selectTask(taskRow.modelData.id)
                        Accessible.name: taskRow.modelData.title
                        contentItem: ColumnLayout {
                            Label {
                                text: taskRow.modelData.stateLabel
                                visible: !!taskRow.modelData.sectionStart
                                font.pixelSize: 12
                                color: manual.appearance.accent
                            }
                            Label {
                                text: taskRow.modelData.position >= 0 ? qsTr("%1. %2").arg(taskRow.modelData.position + 1).arg(taskRow.modelData.title) : taskRow.modelData.title
                                textFormat: Text.PlainText
                                elide: Text.ElideRight
                                Layout.fillWidth: true
                            }
                            RowLayout {
                                visible: taskRow.modelData.state === "backlog" || taskRow.modelData.state === "queued"
                                enabled: manual.panel.writable && !manual.panel.project.archived
                                PanelButton {
                                    objectName: "queue_" + taskRow.modelData.id
                                    text: taskRow.modelData.state === "queued" ? qsTr("Remove") : qsTr("Enqueue")
                                    onClicked: manual.panel.queue(taskRow.modelData.id, 0)
                                }
                                PanelButton {
                                    objectName: "up_" + taskRow.modelData.id
                                    text: qsTr("Up")
                                    enabled: taskRow.modelData.position > 0
                                    onClicked: manual.panel.queue(taskRow.modelData.id, -1)
                                }
                                PanelButton {
                                    objectName: "down_" + taskRow.modelData.id
                                    text: qsTr("Down")
                                    enabled: !!taskRow.modelData.canDown
                                    onClicked: manual.panel.queue(taskRow.modelData.id, 1)
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
                        text: manual.panel.editorKind === "project" ? qsTr("Project editor") : manual.panel.editorKind === "task" ? qsTr("Task editor") : qsTr("Task details")
                        font.pixelSize: 20
                    }
                    ColumnLayout {
                        visible: manual.panel.editorKind !== ""
                        Layout.fillWidth: true
                        TextField {
                            objectName: "nameField"
                            Layout.fillWidth: true
                            placeholderText: manual.panel.editorKind === "project" ? qsTr("Project name") : qsTr("Task title")
                            Accessible.name: placeholderText
                            text: manual.panel.editor.name || manual.panel.editor.title || ""
                            onTextEdited: manual.panel.field(manual.panel.editorKind === "project" ? "name" : "title", text)
                        }
                        TextField {
                            objectName: "rootField"
                            Layout.fillWidth: true
                            visible: manual.panel.editorKind === "project"
                            placeholderText: qsTr("Absolute project directory")
                            Accessible.name: placeholderText
                            text: manual.panel.editor.root_path || ""
                            onTextEdited: manual.panel.field("root_path", text)
                        }
                        TextArea {
                            objectName: "descriptionField"
                            Layout.fillWidth: true
                            Layout.preferredHeight: 140
                            placeholderText: qsTr("Description")
                            Accessible.name: placeholderText
                            textFormat: TextEdit.PlainText
                            wrapMode: TextEdit.Wrap
                            text: manual.panel.editor.description || ""
                            onTextChanged: {
                                if (activeFocus)
                                    manual.panel.field("description", text);
                            }
                        }
                        TextArea {
                            objectName: "criteriaField"
                            visible: manual.panel.editorKind === "task"
                            Layout.fillWidth: true
                            Layout.preferredHeight: 140
                            placeholderText: qsTr("Acceptance criteria")
                            Accessible.name: placeholderText
                            textFormat: TextEdit.PlainText
                            wrapMode: TextEdit.Wrap
                            text: manual.panel.editor.acceptance_criteria || ""
                            onTextChanged: {
                                if (activeFocus)
                                    manual.panel.field("acceptance_criteria", text);
                            }
                        }
                        RowLayout {
                            PanelButton {
                                objectName: "saveButton"
                                text: qsTr("Save")
                                enabled: manual.panel.saveable
                                onClicked: manual.panel.save()
                            }
                            PanelButton {
                                objectName: "discardButton"
                                text: qsTr("Discard draft")
                                enabled: !manual.panel.uncertain && !manual.panel.busy
                                onClicked: manual.discardRequested()
                            }
                        }
                        Label {
                            objectName: "conflictCurrent"
                            visible: manual.panel.conflict
                            text: qsTr("Current server text: %1").arg(manual.panel.currentText)
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
                        visible: manual.panel.editorKind === "" && !!manual.panel.task.id
                        Layout.fillWidth: true
                        Label {
                            objectName: "taskTitle"
                            text: manual.panel.task.title || ""
                            textFormat: Text.PlainText
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                            font.bold: true
                        }
                        Label {
                            text: manual.panel.stateLabel(manual.panel.task.state || "")
                        }
                        Label {
                            text: manual.panel.task.description || ""
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
                            text: manual.panel.task.acceptance_criteria || ""
                            textFormat: Text.PlainText
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                        }
                        PanelButton {
                            objectName: "editTaskButton"
                            text: qsTr("Edit task")
                            enabled: manual.panel.writable && !manual.panel.project.archived
                            onClicked: manual.panel.editTask(false)
                        }
                        Flow {
                            Layout.fillWidth: true
                            spacing: 6
                            PanelButton {
                                objectName: "startButton"
                                text: qsTr("Start manually")
                                enabled: manual.panel.writable && manual.panel.allowed("in_progress")
                                onClicked: manual.panel.transition("in_progress")
                            }
                            PanelButton {
                                objectName: "doneButton"
                                text: qsTr("Complete manually")
                                enabled: manual.panel.writable && manual.panel.allowed("done")
                                onClicked: manual.panel.transition("done")
                            }
                            PanelButton {
                                objectName: "cancelTaskButton"
                                text: qsTr("Cancel task")
                                enabled: manual.panel.writable && manual.panel.allowed("cancelled")
                                onClicked: manual.panel.transition("cancelled")
                            }
                            PanelButton {
                                objectName: "reopenButton"
                                text: qsTr("Return to backlog")
                                enabled: manual.panel.writable && manual.panel.allowed("backlog")
                                onClicked: manual.panel.transition("backlog")
                            }
                        }
                        Label {
                            text: qsTr("History — owner manual actions")
                            font.bold: true
                        }
                        Repeater {
                            model: manual.panel.history
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
                            enabled: manual.panel.historyMore && !manual.panel.historyLoading
                            onClicked: manual.panel.moreHistory()
                        }
                    }
                    Label {
                        visible: !manual.panel.project.id && !manual.panel.dirty
                        text: qsTr("Select a project or create one to begin. No agents are run by this panel.")
                        wrapMode: Text.Wrap
                        Layout.fillWidth: true
                    }
                }
            }
        }
    }
}
