import QtQuick
import QtQuick.Controls
import QtQuick.Controls.Material

ApplicationWindow {
    objectName: "mainWindow"
    width: 960
    height: 680
    minimumWidth: 640
    minimumHeight: 460
    visible: true
    title: qsTr("Artel — foundation")
    Material.theme: Material.Dark
    Material.accent: "#FFD60A"

    Label {
        objectName: "foundationLabel"
        anchors.centerIn: parent
        text: qsTr("Foundation skeleton — projects and tasks are not available yet.")
    }
}
