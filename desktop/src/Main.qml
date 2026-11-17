import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material

ApplicationWindow {
    id: window

    objectName: "mainWindow"
    width: 960
    height: 680
    minimumWidth: 640
    minimumHeight: 460
    visible: true
    property string connectionStatus: "disconnected"
    property string connectionError: ""

    title: qsTr("Artel — foundation")
    Material.theme: Material.Dark
    Material.accent: "#FFD60A"

    Label {
        objectName: "connectionLabel"
        anchors.top: parent.top
        anchors.horizontalCenter: parent.horizontalCenter
        text: qsTr("API: %1").arg(window.connectionError || window.connectionStatus)
    }

    Label {
        objectName: "foundationLabel"
        anchors.centerIn: parent
        text: qsTr("Foundation skeleton — projects and tasks are not available yet.")
    }
}
