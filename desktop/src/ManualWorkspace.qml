pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts
import Artel
import "ui" as UI

Item {
    id: manual
    required property Panel panel
    required property Appearance appearance
    required property WorkspaceController workspace
    signal openConnections
    signal discardRequested
    signal abandonRequested

    readonly property alias theme: theme
    UI.Theme {
        id: theme
        appearance: manual.appearance
    }
    component PanelButton: UI.ActionButton {
        theme: manual.theme
        kind: "quiet"
    }
    component Caption: Label {
        font.family: manual.theme.systemFont.family
        font.pixelSize: manual.theme.body
        color: theme.text
        textFormat: Text.PlainText
    }
    component Toggle: CheckBox {
        font.pixelSize: theme.secondary
        palette.windowText: theme.text
        palette.text: theme.text
        palette.button: theme.panel
        palette.buttonText: theme.text
        palette.highlight: theme.selected
        palette.highlightedText: theme.selectedText
        Accessible.name: text
    }
    component CopyText: UI.LongText {
        theme: manual.theme
        readOnly: true
        implicitHeight: contentHeight
        padding: 0
        background: null
        Layout.fillWidth: true
    }
    ColumnLayout {
        anchors.fill: parent
        anchors.margins: theme.space4
        spacing: theme.space3
        RowLayout {
            Caption {
                text: qsTr("Projects")
                font.pixelSize: theme.screenTitle
            }
            Caption {
                objectName: "connectionLabel"
                text: manual.panel.connection
                color: theme.muted
                Layout.fillWidth: true
            }
            Caption {
                text: qsTr("Updated: %1").arg(manual.panel.updated)
                font.pixelSize: theme.secondary
                color: theme.muted
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
        UI.StatusLabel {
            theme: manual.theme
            objectName: "errorLabel"
            text: manual.panel.message
            visible: text.length > 0
            severity: "critical"
            Layout.fillWidth: true
        }
        Flow {
            Layout.fillWidth: true
            visible: manual.panel.uncertain || manual.panel.conflict
            spacing: theme.space2
            PanelButton {
                objectName: "retryButton"
                text: qsTr("Retry same save")
                kind: "primary"
                visible: manual.panel.uncertain
                enabled: manual.panel.retryable
                onClicked: manual.panel.retry()
            }
            PanelButton {
                objectName: "abandonButton"
                text: qsTr("Abandon old save, keep draft")
                kind: "destructive"
                visible: manual.panel.canAbandon
                enabled: manual.panel.canAbandon
                onClicked: manual.abandonRequested()
            }
            PanelButton {
                objectName: "rebaseButton"
                text: qsTr("Use reloaded version, keep draft")
                kind: "secondary"
                visible: manual.panel.conflict && manual.panel.dirty
                enabled: manual.panel.writable
                onClicked: manual.panel.rebase()
            }
        }
        RowLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            spacing: theme.space4
            ColumnLayout {
                Layout.preferredWidth: 220
                Layout.maximumWidth: 220
                Layout.fillHeight: true
                Toggle {
                    objectName: "archivedFilter"
                    text: qsTr("Include archived")
                    checked: manual.panel.showArchived
                    onToggled: manual.panel.showArchived = checked
                }
                PanelButton {
                    objectName: "createProjectButton"
                    text: qsTr("Create project")
                    kind: manual.panel.project.id ? "secondary" : "primary"
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
                    spacing: theme.space1
                    model: manual.panel.projects
                    delegate: ItemDelegate {
                        id: projectRow
                        required property var modelData
                        width: projectList.width
                        height: 80
                        padding: theme.space2
                        objectName: "project_" + projectRow.modelData.id
                        highlighted: manual.panel.project.id === projectRow.modelData.id
                        enabled: !manual.panel.dirty && !manual.panel.uncertain && !manual.panel.busy
                        Accessible.name: projectRow.modelData.name
                        onClicked: manual.workspace.openProject(projectRow.modelData.id)
                        background: Rectangle {
                            color: projectRow.highlighted ? theme.selected : projectRow.hovered ? theme.raised : theme.panel
                            radius: theme.radius
                            border.width: projectRow.activeFocus ? 2 : 0
                            border.color: projectRow.highlighted ? theme.selectedText : theme.focus
                        }
                        contentItem: ColumnLayout {
                            spacing: theme.space1
                            Caption {
                                text: projectRow.modelData.name
                                color: projectRow.highlighted ? theme.selectedText : theme.text
                                elide: Text.ElideRight
                                Layout.fillWidth: true
                                font.bold: true
                            }
                            Caption {
                                text: projectRow.modelData.counts
                                color: projectRow.highlighted ? theme.selectedText : theme.muted
                                wrapMode: Text.Wrap
                                Layout.fillWidth: true
                                font.pixelSize: theme.secondary
                            }
                        }
                    }
                }
            }
            Rectangle {
                Layout.fillHeight: true
                implicitWidth: 1
                color: theme.border
            }
            ColumnLayout {
                Layout.preferredWidth: 300
                Layout.maximumWidth: 320
                Layout.fillHeight: true
                visible: !!manual.panel.project.id
                Caption {
                    objectName: "projectName"
                    text: manual.panel.project.name || ""
                    font.pixelSize: theme.panelTitle
                    wrapMode: Text.Wrap
                    Layout.fillWidth: true
                }
                Caption {
                    text: manual.panel.project.root_path || ""
                    color: theme.muted
                    elide: Text.ElideMiddle
                    Layout.fillWidth: true
                }
                Caption {
                    text: manual.panel.project.description || ""
                    wrapMode: Text.Wrap
                    maximumLineCount: 3
                    elide: Text.ElideRight
                    Layout.fillWidth: true
                }
                UI.StatusLabel {
                    theme: manual.theme
                    objectName: "pathWarning"
                    text: qsTr("Path unavailable")
                    visible: manual.panel.project.pathAvailable === false
                    severity: "warning"
                }
                Caption {
                    text: qsTr("Archived — read-only")
                    visible: !!manual.panel.project.archived
                    color: theme.warning
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
                        kind: manual.panel.project.archived ? "secondary" : "destructive"
                        enabled: manual.panel.canArchive
                        onClicked: manual.panel.archive()
                    }
                }
                PanelButton {
                    objectName: "createTaskButton"
                    text: qsTr("Create task")
                    kind: "primary"
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
                    spacing: theme.space1
                    delegate: ItemDelegate {
                        id: taskRow
                        required property var modelData
                        width: taskList.width
                        height: taskRow.modelData.sectionStart ? 116 : 92
                        padding: theme.space2
                        objectName: "task_" + taskRow.modelData.id
                        highlighted: manual.panel.task.id === taskRow.modelData.id
                        enabled: !manual.panel.dirty && !manual.panel.uncertain && !manual.panel.busy
                        onClicked: manual.panel.selectTask(taskRow.modelData.id)
                        Accessible.name: taskRow.modelData.title
                        background: Rectangle {
                            color: taskRow.highlighted ? theme.selected : taskRow.hovered ? theme.raised : theme.panel
                            radius: theme.radius
                            border.width: taskRow.activeFocus ? 2 : 0
                            border.color: taskRow.highlighted ? theme.selectedText : theme.focus
                        }
                        contentItem: ColumnLayout {
                            Caption {
                                text: taskRow.modelData.stateLabel
                                visible: !!taskRow.modelData.sectionStart
                                font.pixelSize: theme.secondary
                                color: taskRow.highlighted ? theme.selectedText : theme.muted
                            }
                            Caption {
                                text: taskRow.modelData.position >= 0 ? qsTr("%1. %2").arg(taskRow.modelData.position + 1).arg(taskRow.modelData.title) : taskRow.modelData.title
                                color: taskRow.highlighted ? theme.selectedText : theme.text
                                elide: Text.ElideRight
                                Layout.fillWidth: true
                            }
                            RowLayout {
                                visible: taskRow.modelData.state === "backlog" || taskRow.modelData.state === "queued"
                                enabled: manual.panel.writable && !manual.panel.project.archived
                                PanelButton {
                                    objectName: "queue_" + taskRow.modelData.id
                                    kind: "secondary"
                                    text: taskRow.modelData.state === "queued" ? qsTr("Remove") : qsTr("Enqueue")
                                    onClicked: manual.panel.queue(taskRow.modelData.id, 0)
                                }
                                PanelButton {
                                    objectName: "up_" + taskRow.modelData.id
                                    kind: "secondary"
                                    text: qsTr("Up")
                                    visible: taskRow.modelData.state === "queued"
                                    enabled: taskRow.modelData.position > 0
                                    onClicked: manual.panel.queue(taskRow.modelData.id, -1)
                                }
                                PanelButton {
                                    objectName: "down_" + taskRow.modelData.id
                                    kind: "secondary"
                                    text: qsTr("Down")
                                    visible: taskRow.modelData.state === "queued"
                                    enabled: !!taskRow.modelData.canDown
                                    onClicked: manual.panel.queue(taskRow.modelData.id, 1)
                                }
                            }
                        }
                    }
                }
            }
            Rectangle {
                visible: !!manual.panel.project.id
                Layout.fillHeight: true
                implicitWidth: 1
                color: theme.border
            }
            ScrollView {
                objectName: "detailsScroll"
                Layout.fillWidth: true
                Layout.fillHeight: true
                clip: true
                contentWidth: availableWidth
                ColumnLayout {
                    width: parent.width
                    spacing: theme.space3
                    Caption {
                        text: manual.panel.editorKind === "project" ? qsTr("Project editor") : manual.panel.editorKind === "task" ? qsTr("Task editor") : qsTr("Task details")
                        visible: manual.panel.editorKind !== "" || !!manual.panel.task.id
                        font.pixelSize: theme.panelTitle
                    }
                    ColumnLayout {
                        visible: manual.panel.editorKind !== ""
                        Layout.fillWidth: true
                        Caption {
                            text: manual.panel.editorKind === "project" ? qsTr("Project name") : qsTr("Task title")
                        }
                        UI.Field {
                            theme: manual.theme
                            objectName: "nameField"
                            Layout.fillWidth: true
                            placeholderText: manual.panel.editorKind === "project" ? qsTr("Project name") : qsTr("Task title")
                            Accessible.name: placeholderText
                            text: manual.panel.editor.name || manual.panel.editor.title || ""
                            onTextEdited: manual.panel.field(manual.panel.editorKind === "project" ? "name" : "title", text)
                        }
                        Caption {
                            text: qsTr("Absolute project directory")
                            visible: manual.panel.editorKind === "project"
                        }
                        UI.Field {
                            theme: manual.theme
                            objectName: "rootField"
                            Layout.fillWidth: true
                            visible: manual.panel.editorKind === "project"
                            placeholderText: qsTr("Absolute project directory")
                            Accessible.name: placeholderText
                            text: manual.panel.editor.root_path || ""
                            onTextEdited: manual.panel.field("root_path", text)
                        }
                        Caption {
                            text: qsTr("Description")
                        }
                        UI.LongText {
                            theme: manual.theme
                            objectName: "descriptionField"
                            Layout.fillWidth: true
                            Layout.preferredHeight: 140
                            placeholderText: qsTr("Description")
                            Accessible.name: placeholderText
                            text: manual.panel.editor.description || ""
                            onTextChanged: {
                                if (activeFocus)
                                    manual.panel.field("description", text);
                            }
                        }
                        Caption {
                            text: qsTr("Acceptance criteria")
                            visible: manual.panel.editorKind === "task"
                        }
                        UI.LongText {
                            theme: manual.theme
                            objectName: "criteriaField"
                            visible: manual.panel.editorKind === "task"
                            Layout.fillWidth: true
                            Layout.preferredHeight: 140
                            placeholderText: qsTr("Acceptance criteria")
                            Accessible.name: placeholderText
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
                                kind: "primary"
                                enabled: manual.panel.saveable
                                onClicked: manual.panel.save()
                            }
                            PanelButton {
                                objectName: "discardButton"
                                text: qsTr("Discard draft")
                                kind: "destructive"
                                enabled: !manual.panel.uncertain && !manual.panel.busy
                                onClicked: manual.discardRequested()
                            }
                        }
                        CopyText {
                            objectName: "conflictCurrent"
                            visible: manual.panel.conflict
                            text: qsTr("Current server text: %1").arg(manual.panel.currentText)
                        }
                        Caption {
                            text: qsTr("Draft stays in this window until saved or discarded.")
                            color: theme.muted
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                        }
                    }
                    ColumnLayout {
                        visible: manual.panel.editorKind === "" && !!manual.panel.task.id
                        Layout.fillWidth: true
                        CopyText {
                            objectName: "taskTitle"
                            text: manual.panel.task.title || ""
                            font.pixelSize: manual.theme.panelTitle
                        }
                        UI.StatusLabel {
                            theme: manual.theme
                            text: manual.panel.stateLabel(manual.panel.task.state || "")
                        }
                        CopyText {
                            objectName: "taskDescription"
                            text: manual.panel.task.description || ""
                        }
                        Caption {
                            text: qsTr("Acceptance criteria")
                            font.bold: true
                        }
                        CopyText {
                            objectName: "taskCriteria"
                            text: manual.panel.task.acceptance_criteria || ""
                        }
                        PanelButton {
                            objectName: "editTaskButton"
                            text: qsTr("Edit task")
                            kind: "secondary"
                            enabled: manual.panel.writable && !manual.panel.project.archived
                            onClicked: manual.panel.editTask(false)
                        }
                        Flow {
                            Layout.fillWidth: true
                            spacing: theme.space2
                            PanelButton {
                                objectName: "startButton"
                                text: qsTr("Start manually")
                                kind: "primary"
                                visible: manual.panel.task.state === "queued"
                                enabled: manual.panel.writable && manual.panel.allowed("in_progress")
                                onClicked: manual.panel.transition("in_progress")
                            }
                            PanelButton {
                                objectName: "doneButton"
                                text: qsTr("Complete manually")
                                kind: "primary"
                                visible: manual.panel.task.state === "in_progress"
                                enabled: manual.panel.writable && manual.panel.allowed("done")
                                onClicked: manual.panel.transition("done")
                            }
                            PanelButton {
                                objectName: "cancelTaskButton"
                                text: qsTr("Cancel task")
                                kind: "destructive"
                                visible: ["backlog", "queued", "in_progress"].includes(manual.panel.task.state)
                                enabled: manual.panel.writable && manual.panel.allowed("cancelled")
                                onClicked: manual.panel.transition("cancelled")
                            }
                            PanelButton {
                                objectName: "reopenButton"
                                text: qsTr("Return to backlog")
                                visible: ["in_progress", "done", "cancelled"].includes(manual.panel.task.state)
                                enabled: manual.panel.writable && manual.panel.allowed("backlog")
                                onClicked: manual.panel.transition("backlog")
                            }
                        }
                        Caption {
                            objectName: "queueStartNotice"
                            text: qsTr("Only the queue head can start, with no task already in progress.")
                            visible: manual.panel.task.state === "queued" && !manual.panel.allowed("in_progress")
                            color: theme.muted
                            wrapMode: Text.Wrap
                            Layout.fillWidth: true
                        }
                        Rectangle {
                            Layout.fillWidth: true
                            implicitHeight: 1
                            color: theme.border
                        }
                        Caption {
                            text: qsTr("History — owner manual actions")
                            font.bold: true
                        }
                        Repeater {
                            model: manual.panel.history
                            delegate: ColumnLayout {
                                id: auditRow
                                required property var modelData
                                Layout.fillWidth: true
                                Caption {
                                    text: qsTr("%1 · %2 · #%3").arg(auditRow.modelData.displayTimestamp).arg(auditRow.modelData.label).arg(auditRow.modelData.seq)
                                    wrapMode: Text.Wrap
                                    color: theme.muted
                                    font.pixelSize: theme.secondary
                                    Layout.fillWidth: true
                                }
                                Toggle {
                                    id: expandChange
                                    objectName: "historyChanges_" + auditRow.modelData.seq
                                    text: qsTr("Show changes")
                                }
                                ScrollView {
                                    visible: expandChange.checked
                                    Layout.fillWidth: true
                                    Layout.preferredHeight: Math.min(320, auditText.implicitHeight)
                                    UI.LongText {
                                        id: auditText
                                        theme: manual.theme
                                        text: auditRow.modelData.details
                                        readOnly: true
                                        Accessible.name: qsTr("Changed fields")
                                    }
                                }
                            }
                        }
                        PanelButton {
                            objectName: "historyMoreButton"
                            text: qsTr("Load more history")
                            visible: manual.panel.historyMore
                            enabled: manual.panel.historyMore && !manual.panel.historyLoading
                            onClicked: manual.panel.moreHistory()
                        }
                    }
                    UI.EmptyState {
                        theme: manual.theme
                        Layout.fillWidth: true
                        visible: !manual.panel.project.id && !manual.panel.dirty
                        title: qsTr("Projects")
                        explanation: qsTr("Select a project or create one to begin. No agents are run by this panel.")
                    }
                }
            }
        }
    }
}
