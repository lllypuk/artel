#include "apiclient.h"
#include <QQmlApplicationEngine>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QTemporaryDir>
#include <QtTest>

class ArtelTests : public QObject {
  Q_OBJECT
private slots:
  void initTestCase() { QTest::failOnWarning(); }
  void productionQmlLoads() {
    QQuickStyle::setStyle("Material");
    QQmlApplicationEngine engine;
    QStringList warnings;
    connect(&engine, &QQmlEngine::warnings, this,
            [&warnings](const QList<QQmlError> &errors) {
              for (const auto &error : errors)
                warnings.append(error.toString());
            });
    engine.load(QUrl(QStringLiteral("qrc:/Main.qml")));
    QCOMPARE(engine.rootObjects().size(), 1);
    auto *window = qobject_cast<QQuickWindow *>(engine.rootObjects().first());
    QVERIFY(window);
    QCOMPARE(window->objectName(), QString("mainWindow"));
    auto *label = window->findChild<QObject *>("foundationLabel");
    QVERIFY(label);
    QVERIFY(label->property("text").toString().contains("not available yet"));
    QVERIFY2(warnings.isEmpty(), qPrintable(warnings.join('\n')));
  }
  void clientDiagnosticsReachProductionQml() {
    QTemporaryDir directory;
    ApiClient client;
    QVERIFY(!client.configure(QUrl("http://127.0.0.1:7331"),
                              directory.filePath("missing.token")));
    QQmlApplicationEngine engine;
    engine.setInitialProperties({{"connectionStatus", client.status()},
                                 {"connectionError", client.error()}});
    engine.load(QUrl(QStringLiteral("qrc:/Main.qml")));
    QCOMPARE(engine.rootObjects().size(), 1);
    auto *window = engine.rootObjects().first();
    auto *label = window->findChild<QObject *>("connectionLabel");
    QVERIFY(label);
    QVERIFY(label->property("text").toString().contains("credential"));
    QVERIFY(!label->property("text").toString().contains(directory.path()));
    window->setProperty("connectionError", QString{});
    window->setProperty("connectionStatus", QStringLiteral("online"));
    QCOMPARE(label->property("text").toString(), QString("API: online"));
  }
  void missingQmlFails() {
    QQmlApplicationEngine engine;
    engine.setOutputWarningsToStandardError(false);
    QSignalSpy warnings(&engine, &QQmlEngine::warnings);
    QTest::ignoreMessage(QtWarningMsg,
                         "QQmlApplicationEngine failed to load component");
    engine.load(QUrl(QStringLiteral("qrc:/Missing.qml")));
    QVERIFY(engine.rootObjects().isEmpty());
    QVERIFY(!warnings.isEmpty());
  }
};

QTEST_MAIN(ArtelTests)
#include "artel_tests.moc"
