#include "apiclient.h"
#include "appearance.h"
#include "panel.h"
#include "workspacecontroller.h"
#include <QCommandLineParser>
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
  QCommandLineParser parser;
  parser.addHelpOption();
  parser.addVersionOption();
  parser.addOption(
      {"endpoint", "Local API endpoint", "url", "http://127.0.0.1:7331"});
  parser.addOption({"token-file", "Private owner credential file", "path",
                    ApiClient::defaultTokenPath()});
  parser.addOption({"agterm-socket", "Owner-local agterm control socket",
                    "path", qEnvironmentVariable("AGTERM_SOCKET")});
  parser.process(app);
  ApiClient client;
  const bool configured = client.configure(QUrl(parser.value("endpoint")),
                                           parser.value("token-file"));
  Appearance appearance;
  app.installTranslator(&appearance);
  Panel panel(&client);
  panel.observers()->navigation()->setSocket(parser.value("agterm-socket"));
  QObject::connect(&appearance, &Appearance::changed, &panel, &Panel::changed);
  QObject::connect(&appearance, &Appearance::changed, panel.observers(),
                   &ObserverClient::changed);
  WorkspaceController workspace(&panel);
  QQmlApplicationEngine engine;
  QObject::connect(&appearance, &Appearance::changed, &engine,
                   &QQmlEngine::retranslate);
  engine.setInitialProperties(
      {{"workspace", QVariant::fromValue(&workspace)},
       {"panel", QVariant::fromValue(&panel)},
       {"appearance", QVariant::fromValue(&appearance)}});
  engine.load(QUrl(QStringLiteral("qrc:/Main.qml")));
  if (engine.rootObjects().isEmpty())
    return 1;
  if (configured)
    client.reload();
  return app.exec();
}
