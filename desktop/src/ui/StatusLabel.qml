import QtQuick
import QtQuick.Controls.Basic

Label {
    required property Theme theme
    // The caller supplies a localized factual status, never color alone.
    property string severity: "neutral"
    color: severity === "critical" ? theme.critical : severity === "warning" ? theme.warning : theme.muted
    font.family: theme.systemFont.family
    font.pixelSize: theme.secondary
    textFormat: Text.PlainText
    wrapMode: Text.Wrap
    Accessible.name: text
}
