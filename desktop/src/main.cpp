#include "apiclient.h"
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
  parser.process(app);
  ApiClient client;
  const bool configured = client.configure(QUrl(parser.value("endpoint")),
                                           parser.value("token-file"));
  QQmlApplicationEngine engine;
  engine.setInitialProperties({{"connectionStatus", client.status()},
                               {"connectionError", client.error()}});
  QObject::connect(&client, &ApiClient::changed, &engine, [&] {
    for (auto *root : engine.rootObjects()) {
      root->setProperty("connectionStatus", client.status());
      root->setProperty("connectionError", client.error());
    }
  });
  engine.load(QUrl(QStringLiteral("qrc:/Main.qml")));
  if (engine.rootObjects().isEmpty())
    return 1;
  if (configured)
    client.reload();
  return app.exec();
}
