pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts

RowLayout {
    id: control
    required property Theme theme
    property alias text: search.text
    property string label: ""
    spacing: theme.space2
    Image {
        objectName: "searchIcon"
        source: "qrc:/ui/icons/search.svg"
        sourceSize.width: 20
        sourceSize.height: 20
        Layout.preferredWidth: 20
        Layout.preferredHeight: 20
        Accessible.ignored: true
    }
    Field {
        id: search
        theme: control.theme
        placeholderText: control.label
        Accessible.name: control.label
        Layout.fillWidth: true
    }
}
