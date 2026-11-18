import QtQuick
import QtQuick.Controls.Basic
import Artel

QtObject {
    id: theme
    // One instance per window; Appearance remains the read-only settings owner.
    property Appearance appearance: null
    property string accentInput: appearance ? appearance.accent : ""
    property real radiusInput: appearance ? Number(appearance.radius) : 6.0
    readonly property Control fontSource: Control {}
    property font systemFont: fontSource.font
    property real fontScale: 1
    readonly property real scale: isFinite(fontScale) && fontScale >= 1 && fontScale <= 3 ? fontScale : 1
    readonly property color canvas: "#1d2026"
    readonly property color panel: "#282d35"
    readonly property color raised: "#333a45"
    readonly property color border: "#505a69"
    readonly property color text: "#eef0f4"
    readonly property color muted: "#bac2cf"
    readonly property color disabled: "#a3adbc"
    readonly property color critical: "#ffb4b4"
    readonly property color warning: "#f0cc83"
    readonly property color fallbackAccent: "#a8c7fa"
    readonly property color accent: validAccent(accentInput)
    readonly property color focus: contrast(accent, raised) >= 3 ? accent : fallbackAccent
    readonly property color selected: accent
    readonly property color selectedText: contrast(canvas, selected) >= 4.5 && contrast(canvas, selected) >= contrast("#ffffff", selected) ? canvas : contrast("#ffffff", selected) >= 4.5 ? "#ffffff" : "#000000"
    readonly property int radius: isFinite(radiusInput) && radiusInput >= 0 && radiusInput <= 12 ? Math.round(radiusInput) : 6
    readonly property int space1: 4
    readonly property int space2: 8
    readonly property int space3: 12
    readonly property int space4: 16
    readonly property int space6: 24
    readonly property int space8: 32
    readonly property real body: Math.max(14, systemFont.pixelSize > 0 ? systemFont.pixelSize : systemFont.pointSize > 0 ? systemFont.pointSize * 96 / 72 : 14) * scale
    readonly property real secondary: Math.max(12, body * 12 / 14)
    readonly property real panelTitle: body * 18 / 14
    readonly property real screenTitle: body * 24 / 14
    readonly property real controlHeight: Math.max(36, body + 16)
    readonly property real rowHeight: Math.max(52, body + 28)

    function validAccent(value: string): color {
        return /^#[0-9a-fA-F]{6}$/.test(value) ? Qt.color(value) : fallbackAccent;
    }
    function luminance(value: color): real {
        function linear(channel: real): real {
            return channel <= 0.04045 ? channel / 12.92 : Math.pow((channel + 0.055) / 1.055, 2.4);
        }
        return 0.2126 * linear(value.r) + 0.7152 * linear(value.g) + 0.0722 * linear(value.b);
    }
    function contrast(a: color, b: color): real {
        const x = luminance(a);
        const y = luminance(b);
        return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
    }
}
