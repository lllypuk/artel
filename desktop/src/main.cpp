#include <QGuiApplication>
#include <QQmlApplicationEngine>
#include <QQuickStyle>
#include <QUrl>

int main(int argc, char *argv[]) {
  QGuiApplication app(argc, argv);
  app.setApplicationName("artel");
  app.setApplicationVersion("0.1.0-dev");
  app.setDesktopFileName("artel");
  QQuickStyle::setStyle("Material");
  QQmlApplicationEngine engine;
  engine.load(QUrl(QStringLiteral("qrc:/Main.qml")));
  if (engine.rootObjects().isEmpty())
    return 1;
  return app.exec();
}
