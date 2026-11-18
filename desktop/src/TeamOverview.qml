pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls.Basic
import QtQuick.Layouts
import Artel
import "ui" as UI

Item {
    id: overview
    required property WorkspaceController workspace
    required property ObserverClient client
    required property UI.Theme theme
    required property var projects
    readonly property TeamOverviewModel team: workspace.team
    readonly property var selected: workspace.selectedObserver
    signal openConnections
    ColumnLayout {
        anchors.fill: parent
        anchors.margins: 24
        spacing: 16
        RowLayout {
            Layout.fillWidth: true
            Label {
                text: qsTr("Team")
                color: overview.theme.text
                font.pixelSize: overview.theme.screenTitle
                font.weight: Font.DemiBold
                Layout.fillWidth: true
            }
            UI.ActionButton {
                objectName: "teamConnectionsButton"
                theme: overview.theme
                text: qsTr("Manage connections")
                onClicked: overview.openConnections()
            }
        }
        UI.StatusLabel {
            objectName: "teamConnectionStatus"
            theme: overview.theme
            Layout.fillWidth: true
            text: overview.client.loading && overview.team.total === 0 ? qsTr("Loading observations…") : overview.team.cached ? qsTr("Observation unavailable — cached activity is unknown") : qsTr("Connected observations · manual tasks stay separate")
            severity: overview.team.cached ? "warning" : "neutral"
        }
        UI.FilterBar {
            objectName: "teamFilterBar"
            theme: overview.theme
            Layout.fillWidth: true
            label: qsTr("Search team or project")
            onTextEdited: overview.team.search = text
        }
        RowLayout {
            Layout.fillWidth: true
            Label {
                text: qsTr("Project")
                color: overview.theme.muted
            }
            ComboBox {
                id: projectFilter
                objectName: "teamProjectFilter"
                Layout.fillWidth: true
                model: [
                    {
                        id: "",
                        name: qsTr("All projects")
                    }
                ].concat(overview.projects)
                textRole: "name"
                valueRole: "id"
                Accessible.name: qsTr("Project")
                onActivated: overview.team.projectId = currentValue
            }
            Label {
                text: qsTr("Activity / freshness")
                color: overview.theme.muted
            }
            ComboBox {
                objectName: "teamActivityFilter"
                Layout.fillWidth: true
                model: [qsTr("All observations"), qsTr("Working"), qsTr("Available"), qsTr("Waiting for input in Pi"), qsTr("Current activity unknown"), qsTr("Observation stopped"), qsTr("Fresh"), qsTr("Stale")]
                Accessible.name: qsTr("Activity / freshness")
                onActivated: overview.team.activity = ["", "working", "idle", "waiting_user", "unknown", "stopped", "fresh", "stale"][currentIndex]
            }
        }
        RowLayout {
            Layout.fillWidth: true
            Layout.fillHeight: true
            spacing: 24
            ColumnLayout {
                Layout.fillWidth: true
                Layout.fillHeight: true
                spacing: 0
                Label {
                    objectName: "teamCounts"
                    text: qsTr("Whole team: %1 · Shown: %2").arg(overview.team.total).arg(overview.team.shown)
                    color: overview.theme.muted
                    Layout.bottomMargin: 12
                }
                ListView {
                    id: roster
                    objectName: "teamList"
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    clip: true
                    model: overview.team
                    spacing: 1
                    activeFocusOnTab: true
                    ScrollBar.vertical: ScrollBar {}
                    delegate: ItemDelegate {
                        id: member
                        required property var record
                        width: roster.width
                        height: Math.max(64, info.implicitHeight + 20)
                        objectName: "teamMember_" + member.record.observer_id
                        highlighted: overview.selected.observer_id === member.record.observer_id && overview.selected.runtime_id === member.record.runtime_id
                        Accessible.name: member.record.label + ". " + member.record.status_text
                        onClicked: overview.workspace.selectObserver(member.record.observer_id, member.record.runtime_id)
                        background: Rectangle {
                            color: member.highlighted ? overview.theme.raised : member.hovered ? overview.theme.panel : overview.theme.canvas
                            radius: overview.theme.radius
                            border.width: member.activeFocus ? 2 : 0
                            border.color: overview.theme.focus
                        }
                        contentItem: ColumnLayout {
                            id: info
                            spacing: 6
                            RowLayout {
                                Label {
                                    text: member.record.label
                                    textFormat: Text.PlainText
                                    color: overview.theme.text
                                    font.pixelSize: overview.theme.body
                                    font.weight: Font.DemiBold
                                    elide: Text.ElideRight
                                    Layout.fillWidth: true
                                }
                                Label {
                                    text: member.record.status_text
                                    color: member.record.status === "unknown" || member.record.status === "inconsistent" ? overview.theme.warning : overview.theme.text
                                    font.pixelSize: overview.theme.secondary
                                }
                            }
                            RowLayout {
                                Label {
                                    text: member.record.project_name
                                    textFormat: Text.PlainText
                                    color: overview.theme.muted
                                    elide: Text.ElideRight
                                    Layout.fillWidth: true
                                }
                                Label {
                                    text: member.record.age_text
                                    color: overview.theme.muted
                                    font.pixelSize: overview.theme.secondary
                                }
                            }
                        }
                    }
                    UI.EmptyState {
                        objectName: "teamEmptyState"
                        anchors.centerIn: parent
                        width: parent.width
                        visible: overview.team.shown === 0 && !overview.client.loading
                        theme: overview.theme
                        title: overview.team.total > 0 ? qsTr("No matching observations") : qsTr("No connected team yet")
                        explanation: overview.team.total > 0 ? qsTr("Change the search or filters to see the team.") : qsTr("Provision an observer with artelctl, then bind its runtime in Connections. No agents are launched here.")
                    }
                }
            }
            Rectangle {
                implicitWidth: 1
                Layout.fillHeight: true
                color: overview.theme.border
            }
            ScrollView {
                id: inspectorScroll
                Layout.preferredWidth: 280
                Layout.maximumWidth: 280
                Layout.fillWidth: false
                Layout.fillHeight: true
                contentWidth: availableWidth
                clip: true
                ColumnLayout {
                    width: inspectorScroll.availableWidth
                    spacing: 12
                    Label {
                        text: qsTr("Observer details")
                        color: overview.theme.text
                        font.pixelSize: overview.theme.panelTitle
                    }
                    UI.EmptyState {
                        visible: !overview.selected.observer_id
                        theme: overview.theme
                        Layout.fillWidth: true
                        title: qsTr("Select an observer")
                        explanation: qsTr("Inspect a connection without changing the manual project or task.")
                    }
                    Label {
                        visible: !!overview.selected.observer_id
                        text: overview.selected.label || ""
                        textFormat: Text.PlainText
                        color: overview.theme.text
                        font.pixelSize: overview.theme.panelTitle
                        wrapMode: Text.Wrap
                        Layout.fillWidth: true
                    }
                    UI.StatusLabel {
                        visible: !!overview.selected.observer_id
                        theme: overview.theme
                        text: overview.selected.status_text || ""
                        Layout.fillWidth: true
                    }
                    Label {
                        visible: !!overview.selected.observer_id
                        text: overview.selected.project_name || ""
                        textFormat: Text.PlainText
                        color: overview.theme.muted
                        wrapMode: Text.Wrap
                        Layout.fillWidth: true
                    }
                    UI.ActionButton {
                        objectName: "teamOpenProjectButton"
                        visible: !!overview.selected.observer_id
                        theme: overview.theme
                        text: qsTr("Open project")
                        enabled: !!overview.selected.project_available && !overview.team.cached
                        onClicked: overview.workspace.openObserverProject(overview.selected.observer_id, overview.selected.runtime_id, overview.selected.project_id, overview.selected.version)
                    }
                    UI.StatusLabel {
                        objectName: "teamProjectAvailability"
                        visible: !!overview.selected.observer_id && !overview.selected.project_available
                        theme: overview.theme
                        text: overview.selected.project_archived ? qsTr("Archived project — manual workspace is read-only") : qsTr("Project unavailable — no automatic rebinding")
                        severity: "warning"
                        Layout.fillWidth: true
                    }
                    UI.ActionButton {
                        objectName: "teamOpenSessionButton"
                        visible: !!overview.selected.observer_id
                        theme: overview.theme
                        text: qsTr("Open declared agterm session")
                        enabled: !!overview.selected.navigable && !overview.client.navigation.busy
                        onClicked: overview.workspace.openObserverSession(overview.selected.observer_id, overview.selected.runtime_id, overview.selected.version)
                        Layout.fillWidth: true
                    }
                    UI.StatusLabel {
                        objectName: "teamNavigationMessage"
                        theme: overview.theme
                        visible: text.length > 0
                        text: overview.client.navigation.message
                        Layout.fillWidth: true
                    }
                    UI.StatusLabel {
                        visible: !!overview.selected.observer_id
                        theme: overview.theme
                        text: qsTr("Terminal hints may be inherited and are unverified. Opens a session only; exact pane focus is not guaranteed.")
                        Layout.fillWidth: true
                    }
                    UI.LongText {
                        objectName: "teamObserverMetadata"
                        visible: !!overview.selected.observer_id
                        theme: overview.theme
                        readOnly: true
                        text: qsTr("Last seen: %1\nAge: %2\nLast reported activity (not current): %3\nProvider / model: %4 / %5\nPi / bridge: %6 / %7\nObserver: %8\nRuntime: %9\nVersion: %10").arg(overview.selected.last_seen || "—").arg(overview.selected.age_text || "—").arg(overview.selected.last_activity_text || "—").arg(overview.selected.provider || "—").arg(overview.selected.model || "—").arg(overview.selected.pi_version || "—").arg(overview.selected.bridge_version || "—").arg(overview.selected.observer_id || "—").arg(overview.selected.runtime_id || "—").arg(overview.selected.version || "—")
                        Accessible.name: qsTr("Connection metadata")
                        wrapMode: Text.Wrap
                        Layout.fillWidth: true
                    }
                }
            }
        }
        Rectangle {
            Layout.fillWidth: true
            implicitHeight: 1
            color: overview.theme.border
        }
        Label {
            objectName: "teamFooter"
            text: qsTr("Unbound observers: %1 · Candidates and history are in Connections").arg(overview.team.unbound)
            color: overview.theme.muted
            wrapMode: Text.Wrap
            Layout.fillWidth: true
            font.pixelSize: overview.theme.secondary
        }
    }
}
