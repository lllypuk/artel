#include "agtermfixture.h"
#include "apiclient.h"
#include "appearance.h"
#include "observerfixture.h"
#include "panel.h"
#include "uifixture.h"
#include <QFile>
#include <QFileInfo>
#include <QInputMethodEvent>
#include <QJsonDocument>
#include <QProcess>
#include <QQmlApplicationEngine>
#include <QQuickItem>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QTcpServer>
#include <QTcpSocket>
#include <QTemporaryDir>
#include <QtTest>
#include <cmath>

namespace {
QQuickItem *item(QQuickItem *root, const QString &name) {
  if (root->objectName() == name)
    return root;
  for (auto *child : root->childItems())
    if (auto *found = item(child, name))
      return found;
  return nullptr;
}
QQuickItem *item(QQuickWindow *window, const QString &name) {
  return item(window->contentItem(), name);
}
bool click(QQuickWindow *window, const QString &name) {
  auto *control = item(window, name);
  if (!control || !control->isVisible() || !control->isEnabled())
    return false;
  QTest::mouseClick(
      window, Qt::LeftButton, {},
      control->mapToScene(QPointF(control->width() / 2, control->height() / 2))
          .toPoint());
  return true;
}
bool enter(QQuickWindow *window, const QString &name, const QString &text) {
  auto *control = item(window, name);
  if (!control)
    return false;
  control->forceActiveFocus();
  QTest::keyClick(window, Qt::Key_A, Qt::ControlModifier);
  QInputMethodEvent event;
  event.setCommitString(text);
  QCoreApplication::sendEvent(control, &event);
  return control->property("text").toString() == text;
}
void writeFile(const QString &path, const QByteArray &bytes) {
  QFile file(path);
  QVERIFY(file.open(QIODevice::WriteOnly));
  QCOMPARE(file.write(bytes), bytes.size());
}
QQuickWindow *load(QQmlApplicationEngine &engine, Panel &panel,
                   Appearance &appearance) {
  engine.setInitialProperties(
      {{"panel", QVariant::fromValue(&panel)},
       {"appearance", QVariant::fromValue(&appearance)}});
  engine.load(QUrl("qrc:/Main.qml"));
  return engine.rootObjects().size() == 1
             ? qobject_cast<QQuickWindow *>(engine.rootObjects().first())
             : nullptr;
}
struct Daemon {
  QTemporaryDir dir;
  QProcess process;
  ~Daemon() {
    if (process.state() != QProcess::NotRunning) {
      process.terminate();
      if (!process.waitForFinished(5000)) {
        process.kill();
        process.waitForFinished(5000);
      }
    }
  }
  void start() {
    auto env = QProcessEnvironment::systemEnvironment();
    env.insert("HOME", dir.path());
    process.setProcessEnvironment(env);
    process.start(qEnvironmentVariable("ARTEL_TEST_DAEMON"),
                  {"--data-dir", dir.filePath("data"), "--token-file",
                   dir.filePath("private/owner.token"), "--listen",
                   "127.0.0.1:0"});
  }
};
} // namespace

class ArtelTests : public QObject {
  Q_OBJECT
private slots:
  void initTestCase() { QQuickStyle::setStyle("Material"); }
  void init() { QTest::failOnWarning(); }
  void nativePrimitives() {
    QTemporaryDir dir;
    writeFile(dir.filePath("language"), "en\n");
    Appearance appearance(dir.path(), dir.filePath("language"));
    QCoreApplication::installTranslator(&appearance);
    QQmlApplicationEngine engine;
    connect(&appearance, &Appearance::changed, &engine,
            &QQmlEngine::retranslate);
    engine.setInitialProperties(
        {{"appearance", QVariant::fromValue(&appearance)}});
    engine.load(QUrl("qrc:/tests/Primitives.qml"));
    QCOMPARE(engine.rootObjects().size(), 1);
    auto *window = qobject_cast<QQuickWindow *>(engine.rootObjects().first());
    QVERIFY(window);
    auto *theme = window->findChild<QObject *>("theme");
    QVERIFY(theme);
    QCOMPARE(theme->property("accent").value<QColor>(),
             QColor(appearance.accent()));
    QCOMPARE(theme->property("radius").toInt(), appearance.radius());
    auto contrast = [](QColor a, QColor b) {
      auto luminance = [](QColor c) {
        auto linear = [](double v) {
          return v <= 0.04045 ? v / 12.92 : std::pow((v + 0.055) / 1.055, 2.4);
        };
        return 0.2126 * linear(c.redF()) + 0.7152 * linear(c.greenF()) +
               0.0722 * linear(c.blueF());
      };
      double x = luminance(a), y = luminance(b);
      return (std::max(x, y) + 0.05) / (std::min(x, y) + 0.05);
    };
    // Override presentation inputs; never change host theme/settings.
    for (const auto &accent :
         {"#ffffff", "#000000", "#FFD60A", "#a8c7fa", "invalid", "#00ffffff"}) {
      theme->setProperty("accentInput", accent);
      for (int radius : {0, 6, 12, -1, 100}) {
        theme->setProperty("radiusInput", radius);
        QCOMPARE(theme->property("radius").toInt(),
                 radius >= 0 && radius <= 12 ? radius : 6);
        for (const auto &surface : {"canvas", "panel", "raised"}) {
          const auto background = theme->property(surface).value<QColor>();
          for (const auto &foreground :
               {"text", "muted", "disabled", "critical", "warning"})
            QVERIFY2(contrast(theme->property(foreground).value<QColor>(),
                              background) >= 4.5,
                     qPrintable(QString("%1 on %2").arg(foreground, surface)));
        }
        QVERIFY(contrast(theme->property("selectedText").value<QColor>(),
                         theme->property("selected").value<QColor>()) >= 4.5);
        QVERIFY(contrast(theme->property("focus").value<QColor>(),
                         theme->property("raised").value<QColor>()) >= 3);
      }
    }
    theme->setProperty("accentInput", "invalid");
    QCOMPARE(theme->property("accent").value<QColor>(), QColor("#a8c7fa"));
    theme->setProperty("accentInput", "#a8c7fa");
    theme->setProperty("radiusInput", 6);
    auto *primary = item(window, "primary");
    QVERIFY(primary);
    primary->forceActiveFocus(Qt::TabFocusReason);
    QTRY_VERIFY(primary->property("visualFocus").toBool());
    QSignalSpy clicked(primary, SIGNAL(clicked()));
    QTest::keyClick(window, Qt::Key_Space);
    QCOMPARE(clicked.size(), 1);
    auto *disabled = item(window, "disabled");
    QVERIFY(disabled);
    QSignalSpy disabledClicked(disabled, SIGNAL(clicked()));
    QVERIFY(!click(window, "disabled"));
    QCOMPARE(disabledClicked.size(), 0);
    for (const auto &name :
         {"primary", "secondary", "quiet", "destructive", "disabled"}) {
      auto *button = item(window, name);
      QVERIFY(button);
      for (bool pressed : {false, true}) {
        button->setProperty("down", pressed);
        QVERIFY(contrast(button->property("foreground").value<QColor>(),
                         button->property("surface").value<QColor>()) >= 4.5);
        QVERIFY(contrast(button->property("focusColor").value<QColor>(),
                         button->property("surface").value<QColor>()) >= 3);
      }
      button->setProperty("down", false);
    }
    QCOMPARE(item(window, "longText")->property("textFormat").toInt(), 0);
    QTRY_COMPARE(item(window, "searchIcon")->property("status").toInt(), 1);
    primary->forceActiveFocus(Qt::TabFocusReason);
    QTest::keyClick(window, Qt::Key_Tab);
    QTRY_VERIFY(item(window, "secondary")->hasActiveFocus());
    const auto base = primary->property("font").value<QFont>().pixelSize();
    for (double scale : {1.0, 1.5, 2.0}) {
      window->setProperty("fontScale", scale);
      QTRY_VERIFY(primary->height() >=
                  theme->property("controlHeight").toReal());
      QCOMPARE(primary->property("font").value<QFont>().pixelSize(),
               qRound(base * scale));
    }
    QFont largerFont = theme->property("systemFont").value<QFont>();
    largerFont.setPixelSize(20);
    theme->setProperty("systemFont", largerFont);
    QCOMPARE(theme->property("body").toReal(), 40.0);
    // Restore the native system default after checking increased UI font.
    theme->setProperty("systemFont", QGuiApplication::font());
    window->setProperty("fontScale", -1);
    QCOMPARE(theme->property("scale").toReal(), 1.0);
    const auto capture = qEnvironmentVariable("ARTEL_PRIMITIVE_SCREENSHOT");
    for (const auto &language : {"en", "ru"}) {
      writeFile(dir.filePath("language"), QByteArray(language) + '\n');
      appearance.refresh();
      QTRY_COMPARE(appearance.language(), QString(language));
      engine.retranslate();
      QCOMPARE(primary->property("text").toString(),
               QString(language) == "ru" ? QString::fromUtf8("Сохранить")
                                         : QString("Save"));
      QTest::qWait(50);
      if (!capture.isEmpty())
        QVERIFY(window->grabWindow().save(capture + "-" + language + ".png"));
      if (QString(language) == "ru") {
        window->resize(1000, 900);
        window->setProperty("fontScale", 2.0);
        QTest::qWait(50);
        auto *cancel = item(window, "secondary");
        auto *quiet = item(window, "quiet");
        QVERIFY(cancel->mapToScene(QPointF()).x() >=
                primary->mapToScene(QPointF(primary->width(), 0)).x());
        QVERIFY(quiet->mapToScene(QPointF(quiet->width(), 0)).x() <
                window->width());
        if (!capture.isEmpty())
          QVERIFY(window->grabWindow().save(capture + "-ru-large.png"));
      }
    }
    QCoreApplication::removeTranslator(&appearance);
  }
  void agtermNavigationContract() {
    for (const auto &mode :
         {"", "move", "swap", "version", "capability", "oversize", "malformed",
          "windows", "ambiguous", "missing", "disappeared", "unrealized",
          "pane", "killed", "timeout", "window-format", "tree-format",
          "tree-oversize"}) {
      AgtermFixture fixture;
      fixture.mode = mode;
      AgtermAdapter adapter;
      fixture.install(&adapter);
      adapter.open(fixture.hint());
      QTRY_VERIFY_WITH_TIMEOUT(!adapter.busy(), 3000);
      const bool success = QString(mode).isEmpty() || QString(mode) == "move" ||
                           QString(mode) == "swap";
      QCOMPARE(adapter.message().contains("opened"), success);
      for (const auto &args : fixture.calls) {
        QCOMPARE(args.mid(args.size() - 2),
                 QStringList({"--socket", "/fictional/owner.sock"}));
        QVERIFY(args.first() == "version" || args.first() == "window" ||
                args.first() == "tree" ||
                args.mid(0, 2) == QStringList({"session", "select"}));
        QVERIFY(!args.contains("active"));
      }
      if (success) {
        QCOMPARE(fixture.calls.size(), 9);
        QCOMPARE(fixture.calls.first(),
                 QStringList({"version", "--json", "--socket",
                              "/fictional/owner.sock"}));
        QCOMPARE(fixture.calls.at(1),
                 QStringList({"session", "select", "--help", "--socket",
                              "/fictional/owner.sock"}));
        for (int offset : {2, 5}) {
          QCOMPARE(fixture.calls.at(offset),
                   QStringList({"window", "list", "--json", "--socket",
                                "/fictional/owner.sock"}));
          QCOMPARE(fixture.calls.at(offset + 1),
                   QStringList({"tree", "--window", fixture.window, "--json",
                                "--socket", "/fictional/owner.sock"}));
          QCOMPARE(
              fixture.calls.at(offset + 2),
              QStringList({"tree", "--window", fixture.otherWindow, "--json",
                           "--socket", "/fictional/owner.sock"}));
        }
        QCOMPARE(fixture.calls.last(),
                 QStringList({"session", "select", "--target", fixture.session,
                              "--window", fixture.otherWindow, "--json",
                              "--socket", "/fictional/owner.sock"}));
      } else {
        for (const auto &args : fixture.calls)
          QVERIFY(args.first() != "session" || args.contains("--help"));
      }
    }
    AgtermFixture fixture;
    AgtermAdapter adapter;
    fixture.install(&adapter);
    for (const auto &key : {"window_id", "session_id", "pane_id"}) {
      auto hint = fixture.hint();
      hint[key] = "active; touch /tmp/not-allowed";
      adapter.open(hint);
      QVERIFY(fixture.calls.isEmpty());
      hint.remove(key);
      adapter.open(hint);
      QVERIFY(fixture.calls.isEmpty());
    }
    auto hint = fixture.hint();
    hint.insert("socket", "/hostile");
    adapter.open(hint);
    QVERIFY(fixture.calls.isEmpty());
    fixture.mode = "select-failure";
    adapter.open(fixture.hint());
    QCOMPARE(fixture.calls.size(), 9);
    QVERIFY(adapter.message().contains("unavailable"));
    QVERIFY(!adapter.message().contains("SECRET"));
    QVERIFY(!adapter.busy());
  }
  void agtermCancellationFencesLateReplies() {
    AgtermFixture fixture;
    AgtermAdapter::Done late;
    auto adapter = std::make_unique<AgtermAdapter>();
    adapter->setSocket("/fictional/socket");
    adapter->setRunner([&late](const QStringList &, AgtermAdapter::Done done) {
      late = done;
    });
    adapter->open(fixture.hint());
    QVERIFY(adapter->busy());
    adapter->cancel();
    QVERIFY(!adapter->busy());
    QVERIFY(adapter->message().isEmpty());
    const auto response =
        AgtermFixture::response({{"app", QJsonObject{{"version", "0.40.0"}}}});
    late(true, response);
    QVERIFY(!adapter->busy());
    adapter->open(fixture.hint());
    adapter.reset();
    late(true, response); // Deleted QObject must not be accessed.
  }
  void agtermRealProcessBoundary() {
    QTemporaryDir dir;
    QVERIFY(dir.isValid());
    const auto path = dir.filePath("agtermctl");
    const auto oldPath = qgetenv("PATH");
    struct Restore {
      QByteArray path;
      ~Restore() { qputenv("PATH", path); }
    } restore{oldPath};
    qputenv("PATH", dir.path().toUtf8());
    // A real executable checks every argv in order, not just a mock callback.
    AgtermFixture oracle;
    AgtermAdapter reference;
    oracle.install(&reference);
    reference.open(oracle.hint());
    QVERIFY(reference.message().contains("opened"));
    QJsonArray scenario;
    AgtermFixture responses;
    for (const auto &args : oracle.calls)
      scenario.append(
          QJsonObject{{"args", QJsonArray::fromStringList(args)},
                      {"output", QString::fromUtf8(responses.reply(args))}});
    writeFile(dir.filePath("scenario.json"), QJsonDocument(scenario).toJson());
    writeFile(
        path,
        "#!/usr/bin/python3\n"
        "import sys,json,pathlib\n"
        "root=pathlib.Path(sys.argv[0]).parent\n"
        "log=root/'calls.jsonl'\n"
        "n=len(log.read_text().splitlines()) if log.exists() else 0\n"
        "expected=json.loads((root/'scenario.json').read_text())[n]\n"
        "assert sys.argv[1:]==expected['args']\n"
        "with log.open('a') as f: f.write(json.dumps(sys.argv[1:])+'\\n')\n"
        "print(expected['output'])\n");
    QVERIFY(QFile::setPermissions(path, QFileDevice::ReadOwner |
                                            QFileDevice::WriteOwner |
                                            QFileDevice::ExeOwner));
    {
      AgtermAdapter adapter;
      adapter.setSocket("/fictional/owner.sock");
      adapter.open(oracle.hint());
      QTRY_VERIFY_WITH_TIMEOUT(!adapter.busy(), 3000);
      QVERIFY(adapter.message().contains("opened"));
      QFile log(dir.filePath("calls.jsonl"));
      QVERIFY(log.open(QIODevice::ReadOnly));
      QCOMPARE(log.readAll().count('\n'), oracle.calls.size());
    }
    for (const auto &body :
         {QByteArray(
              "import sys\nsys.stderr.write('SECRET-CANARY')\nsys.exit(1)\n"),
          QByteArray("import time\ntime.sleep(20)\n"),
          QByteArray("import os,signal\nos.kill(os.getpid(),signal.SIGKILL)\n"),
          QByteArray("import sys\nsys.stdout.write('x'*1100000)\n"),
          QByteArray("import sys\nsys.stderr.write('x'*1100000)\n")}) {
      writeFile(path, "#!/usr/bin/python3\nimport os,pathlib,sys\n"
                      "(pathlib.Path(sys.argv[0]).parent/"
                      "'child.pid').write_text(str(os.getpid()))\n" +
                          body);
      QVERIFY(QFile::setPermissions(path, QFileDevice::ReadOwner |
                                              QFileDevice::WriteOwner |
                                              QFileDevice::ExeOwner));
      AgtermFixture fixture;
      AgtermAdapter adapter;
      adapter.setSocket(dir.filePath("private.sock"));
      adapter.open(fixture.hint());
      QTRY_VERIFY_WITH_TIMEOUT(!adapter.busy(), 3000);
      QVERIFY(adapter.message().contains("unavailable"));
      QVERIFY(!adapter.message().contains("SECRET"));
      QFile pid(dir.filePath("child.pid"));
      QVERIFY(pid.open(QIODevice::ReadOnly));
      const auto child = QString::fromUtf8(pid.readAll());
      QVERIFY(!QFileInfo::exists("/proc/" + child));
    }
  }
  void russianCatalogCoversVisibleQmlStrings() {
    QFile catalog(":/i18n/ru.json"), qml(":/Main.qml");
    QVERIFY(catalog.open(QIODevice::ReadOnly));
    QVERIFY(qml.open(QIODevice::ReadOnly));
    const auto strings = QJsonDocument::fromJson(catalog.readAll()).object();
    QVERIFY(!strings.isEmpty());
    const QRegularExpression expression("qsTr\\(\"([^\"]*)\"");
    auto matches = expression.globalMatch(QString::fromUtf8(qml.readAll()));
    while (matches.hasNext()) {
      const auto key =
          QJsonDocument::fromJson(
              ("[\"" + matches.next().captured(1) + "\"]").toUtf8())
              .array()
              .first()
              .toString();
      QVERIFY2(strings.contains(key), qPrintable(key));
    }
  }
  void observerCatalogCoversQmlAndClient() {
    QFile catalog(":/i18n/ru.json"), qml(":/ObserverPanel.qml");
    QVERIFY(catalog.open(QIODevice::ReadOnly));
    QVERIFY(qml.open(QIODevice::ReadOnly));
    const auto strings = QJsonDocument::fromJson(catalog.readAll()).object();
    const QRegularExpression expression("qsTr\\(\"([^\"]*)\"");
    auto matches = expression.globalMatch(QString::fromUtf8(qml.readAll()));
    while (matches.hasNext()) {
      const auto key =
          QJsonDocument::fromJson(
              ("[\"" + matches.next().captured(1) + "\"]").toUtf8())
              .array()
              .first()
              .toString();
      QVERIFY2(strings.contains(key), qPrintable(key));
    }
  }
  void observerNavigationProductionQml() {
    ObserverFixture fixture;
    AgtermFixture terminal;
    auto record = fixture.record();
    auto runtimes = record.value("runtimes").toArray();
    auto runtime = runtimes.first().toObject();
    auto reg = runtime.value("registration").toObject();
    reg.insert("terminal_hint", QJsonObject::fromVariantMap(terminal.hint()));
    runtime.insert("registration", reg);
    runtimes[0] = runtime;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    terminal.install(panel.observers()->navigation());
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(panel.editTask());
    panel.field("title", "Navigation must preserve this draft");
    const auto draft = client.draft();
    QVERIFY(click(window, "observersButton"));
    auto *o = panel.observers();
    QTRY_VERIFY(o->online());
    const auto button = "observerOpen_" + fixture.runtime;
    QTRY_VERIFY(item(window, button));
    QVERIFY(item(window, button)->isEnabled());
    QVERIFY(!item(window, "observerOpen_" + fixture.otherRuntime)->isEnabled());
    // Keyboard activation uses the exact same production handler as a click.
    item(window, button)->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Space);
    QVERIFY(o->navigation()->message().contains("exact pane focus"));
    if (qEnvironmentVariableIsSet("ARTEL_NAV_SCREENSHOT"))
      QVERIFY(window->grabWindow().save(
          qEnvironmentVariable("ARTEL_NAV_SCREENSHOT")));
    QCOMPARE(terminal.calls.last().at(1), QString("select"));
    QCOMPARE(client.draft(), draft);
    QCOMPARE(panel.project().value("id").toString(), fixture.project);
    terminal.mode = "version";
    QVERIFY(click(window, button));
    QVERIFY(o->navigation()->message().contains("unavailable"));
    const int reads = fixture.reads;
    o->reload();
    QTRY_VERIFY(fixture.reads > reads);
    QTRY_VERIFY(o->online());
    QCOMPARE(client.draft(), draft);
    // A stale/expired hint cannot be launched, including direct invocation.
    runtime.insert("freshness", "stale");
    runtimes[0] = runtime;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    o->reload();
    QTRY_VERIFY(!item(window, button)->isEnabled());
    const auto calls = terminal.calls.size();
    o->openSession(fixture.observer, fixture.runtime);
    QCOMPARE(terminal.calls.size(), calls);
    QCOMPARE(client.draft(), draft);
    QVERIFY(fixture.mutations.isEmpty());
  }
  void observerProductionQmlPreservesDraftAndShowsCandidates() {
    ObserverFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(panel.editTask());
    panel.field("title", "A private unsaved draft");
    const auto draft = client.draft();
    QVERIFY(click(window, "observersButton"));
    auto *observer = panel.observers();
    QTRY_VERIFY(observer->active());
    QTRY_VERIFY(observer->online());
    QCOMPARE(observer->rows().size(), 2);
    observer->setOnlyProject(true);
    QVERIFY(observer->rows().isEmpty());
    observer->setOnlyProject(false);
    QTRY_VERIFY(item(window, "observerBind_" + fixture.runtime));
    auto *bindButton = item(window, "observerBind_" + fixture.runtime);
    bindButton->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Space);
    QVERIFY(!observer->confirmation().isEmpty());
    auto *dialog = window->findChild<QObject *>("observerConfirmation");
    QVERIFY(dialog);
    QVERIFY(QMetaObject::invokeMethod(dialog, "accept"));
    QTRY_COMPARE(fixture.mutations.size(), 1);
    QTRY_VERIFY(observer->online());
    QVERIFY(observer->rows().first().toMap().value("bound_runtime").toBool());
    observer->setOnlyProject(true);
    QCOMPARE(observer->rows().size(), 1);
    observer->setOnlyProject(false);
    QCOMPARE(client.draft(), draft);
    QCOMPARE(panel.project().value("id").toString(), fixture.project);
    QVERIFY(!panel.selectProject("77777777-7777-4777-8777-777777777777"));
    QCOMPARE(client.draft(), draft);
    QVERIFY(!client.pending());
    QVERIFY(fixture.mutations.first().startsWith("PATCH /v1/observers/"));
    QVERIFY(fixture.mutations.first().contains("Idempotency-Key:"));
    QVERIFY(!fixture.mutations.first().contains("A private unsaved draft"));
    QTRY_VERIFY(!dialog->property("visible").toBool());
    QTest::qWait(
        200); // Finish Qt's native popup exit transition before capture.
    const auto image = window->grabWindow();
    QVERIFY(!image.isNull());
    const auto path = qEnvironmentVariable("ARTEL_OBSERVER_SCREENSHOT");
    if (!path.isEmpty())
      QVERIFY(image.save(path));
    writeFile(fixture.directory.filePath("language"), "ru\n");
    QCoreApplication::installTranslator(&appearance);
    QObject::connect(&appearance, &Appearance::changed, observer,
                     &ObserverClient::changed);
    appearance.refresh();
    engine.retranslate();
    QVERIFY(observer->rows()
                .first()
                .toMap()
                .value("status_text")
                .toString()
                .contains(QString::fromUtf8("Привязан")));
    QVERIFY(item(window, "observerConnection")
                ->property("text")
                .toString()
                .contains(QString::fromUtf8("Наблюдение")));
    window->resize(1000, 680);
    QTest::qWait(100);
    if (!path.isEmpty())
      QVERIFY(window->grabWindow().save(path + ".ru.png"));
    QCoreApplication::removeTranslator(&appearance);
  }
  void observerCasRebindRevokePruneAndManualPendingAreIndependent() {
    ObserverFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    auto *o = panel.observers();
    o->setActive(true);
    QTRY_VERIFY(o->online());
    QVERIFY(o->prepare("bind", fixture.observer, fixture.runtime));
    // Another owner's version wins. No hidden CAS adoption.
    auto record = fixture.record();
    record.insert("version", 2);
    fixture.record(record);
    QVERIFY(o->confirm());
    QTRY_COMPARE(fixture.mutations.size(), 1);
    QTRY_VERIFY(o->online());
    QVERIFY(!fixture.record().contains("binding"));
    QVERIFY(o->message().contains("project unavailable"));
    QVERIFY(o->prepare("bind", fixture.observer, fixture.runtime));
    QVERIFY(o->confirm());
    QVERIFY(!o->prepare("revoke", fixture.observer, fixture.runtime));
    QTRY_COMPARE(fixture.mutations.size(), 2);
    QTRY_VERIFY(o->online());
    QVERIFY(o->prepare("bind", fixture.observer, fixture.otherRuntime));
    QVERIFY(o->confirm());
    QTRY_COMPARE(fixture.mutations.size(), 3);
    QTRY_VERIFY(o->online());
    QCOMPARE(o->rows().first().toMap().value("disposition").toString(),
             QString("superseded"));
    QVERIFY(!o->prepare("bind", fixture.observer, fixture.runtime));
    QVERIFY(o->prepare("prune", fixture.observer, fixture.otherRuntime));
    QVERIFY(o->confirm());
    QTRY_COMPARE(fixture.mutations.size(), 4);
    QTRY_VERIFY(o->online());
    QCOMPARE(o->rows().size(), 1);
    QVERIFY(o->prepare("unbind", fixture.observer, fixture.otherRuntime));
    QVERIFY(o->confirm());
    QTRY_COMPARE(fixture.mutations.size(), 5);
    QTRY_VERIFY(o->online());
    QVERIFY(!fixture.record().contains("binding"));
    QCOMPARE(o->rows().first().toMap().value("disposition").toString(),
             QString("superseded"));
    // An unrelated manual save remains pending while observer revoke completes.
    QPointer<QTcpSocket> pending;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("PATCH /v1/tasks/"))
        pending = socket;
      else
        fixture.observation(socket, request);
    };
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(panel.editTask());
    panel.field("title", "Manual pending");
    const auto draft = client.draft();
    QVERIFY(panel.save());
    QTRY_VERIFY(pending);
    QVERIFY(client.pending());
    QVERIFY(o->prepare("revoke", fixture.observer, fixture.otherRuntime));
    QVERIFY(o->confirm());
    QTRY_VERIFY(fixture.record().value("revoked").toBool());
    QTRY_VERIFY(o->online());
    QVERIFY(client.pending());
    QCOMPARE(client.draft(), draft);
    QVERIFY(!o->prepare("bind", fixture.observer, fixture.otherRuntime));
    UiFixture::respond(pending, 409, "{\"code\":\"version_conflict\"}");
    QTRY_VERIFY(!client.pending());
    QCOMPARE(client.draft(), draft);
  }
  void observerOldReadCannotUpdateReplacementDatabase() {
    ObserverFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    auto *o = panel.observers();
    QPointer<QTcpSocket> old;
    int requests = 0;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/observers ")) {
        ++requests;
        if (requests == 1)
          old = socket;
        else
          fixture.observation(socket, request);
      } else
        fixture.normal(socket, request);
    };
    o->setActive(true);
    QTRY_VERIFY(old);
    for (int i = 0; i < 20; ++i)
      o->reload();
    QCOMPARE(requests, 1); // Exactly one projection request in flight.
    const auto oldBytes = fixture.bytes();
    const QString replacement = "77777777-7777-4777-8777-777777777777";
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    snapshot.insert("instance_id", replacement);
    fixture.state = QJsonDocument(snapshot).toJson();
    fixture.registry.insert("database_instance_id", replacement);
    auto row = fixture.record();
    row.insert("label", "Replacement observer");
    fixture.record(row);
    client.reload();
    QTRY_COMPARE(client.instance(), replacement);
    QTRY_VERIFY(o->online());
    QCOMPARE(o->rows().first().toMap().value("label").toString(),
             QString("Replacement observer"));
    if (old && old->state() == QAbstractSocket::ConnectedState)
      UiFixture::respond(old, 200, oldBytes);
    QTest::qWait(100);
    QCOMPARE(o->rows().first().toMap().value("label").toString(),
             QString("Replacement observer"));
  }
  void observerArchiveMissingProjectUnknownSaveAndMalformedReply() {
    ObserverFixture fixture;
    ApiClient client;
    client.setTimeout(100);
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    auto *o = panel.observers();
    o->setActive(true);
    QTRY_VERIFY(o->online());
    QVERIFY(o->prepare("bind", fixture.observer, fixture.runtime));
    auto archived = panel.project();
    archived.insert("archived", true);
    o->setProject(archived);
    QVERIFY(!o->confirm());
    QVERIFY(!o->canBind());
    QVERIFY(!o->prepare("bind", fixture.observer, fixture.runtime));
    o->setProject({});
    QVERIFY(!o->canBind());
    o->setProject(panel.project());
    QTRY_VERIFY(o->online());
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(panel.editTask());
    panel.field("title", "Uncertain manual draft");
    const auto draft = client.draft();
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("PATCH /v1/tasks/"))
        return; // lost manual ACK
      fixture.observation(socket, request);
    };
    QVERIFY(panel.save());
    QTRY_VERIFY(client.uncertain());
    QVERIFY(o->prepare("bind", fixture.observer, fixture.runtime));
    QVERIFY(o->confirm());
    QTRY_VERIFY(fixture.record().contains("binding"));
    QTRY_VERIFY(o->online());
    QVERIFY(client.uncertain());
    QCOMPARE(client.draft(), draft);
    // A malformed response (including hostile error/secret text) never reaches
    // UI.
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/observers "))
        UiFixture::respond(socket, 200,
                           "{\"secret\":\"private-token-never-display\"}");
      else
        fixture.normal(socket, request);
    };
    o->reload();
    QTRY_VERIFY(!o->online());
    QVERIFY(!o->message().contains("private-token"));
    QVERIFY(!QJsonDocument::fromVariant(o->rows()).toJson().contains(
        "private-token"));
    QCOMPARE(client.draft(), draft);
    QVERIFY(client.uncertain());
  }
  void observerMetadataUsesUnicodeCodePoints_data() {
    QTest::addColumn<QString>("field");
    QTest::addColumn<int>("length");
    QTest::addColumn<bool>("valid");
    for (const auto *field :
         {"model", "provider", "pi_version", "bridge_version", "label"}) {
      const int limit = QString(field) == "label" ? 128 : 256;
      for (int length : {limit / 2 + 1, limit, limit + 1}) {
        const auto name = QString("%1-%2").arg(field).arg(length);
        QTest::newRow(qPrintable(name))
            << QString(field) << length << (length <= limit);
      }
    }
  }
  void observerMetadataUsesUnicodeCodePoints() {
    QFETCH(QString, field);
    QFETCH(int, length);
    QFETCH(bool, valid);
    ObserverFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    auto record = fixture.record();
    // U+1F680 occupies two UTF-16 units but one Unicode code point.
    const auto value = QString::fromUtf8("\xf0\x9f\x9a\x80").repeated(length);
    if (field == "label") {
      record.insert(field, value);
    } else {
      auto runtimes = record.value("runtimes").toArray();
      auto runtime = runtimes.first().toObject();
      if (field == "pi_version" || field == "bridge_version") {
        auto registration = runtime.value("registration").toObject();
        registration.insert(field, value);
        runtime.insert("registration", registration);
      } else {
        runtime.insert(field, value);
      }
      runtimes[0] = runtime;
      record.insert("runtimes", runtimes);
    }
    fixture.record(record);
    auto *observer = panel.observers();
    observer->setActive(true);
    QTRY_VERIFY(fixture.reads > 0);
    QTRY_VERIFY(observer->online() || !observer->message().isEmpty());
    QCOMPARE(observer->online(), valid);
    if (valid) {
      QCOMPARE(observer->rows().size(), 2);
      QCOMPARE(observer->rows().first().toMap().value(field).toString(), value);
      QVERIFY(observer->prepare("bind", fixture.observer, fixture.runtime));
    } else {
      QVERIFY(!observer->prepare("bind", fixture.observer, fixture.runtime));
    }
    observer->setActive(false);
  }
  void observerOfflineDelayedReplyReplacementAndWhitelist() {
    ObserverFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    auto *o = panel.observers();
    o->setTimeout(200);
    o->setActive(true);
    QTRY_VERIFY(o->online());
    auto record = fixture.record();
    record.insert("secret", "must-not-reach-model");
    auto runtimes = record.value("runtimes").toArray();
    auto runtime = runtimes.first().toObject();
    runtime.insert("freshness", "stale");
    runtime.insert("token", "must-not-reach-model");
    runtimes[0] = runtime;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    const int before = fixture.reads;
    o->reload();
    QTRY_VERIFY(fixture.reads > before);
    QTRY_COMPARE(o->rows().first().toMap().value("freshness").toString(),
                 QString("stale"));
    QVERIFY(!QJsonDocument::fromVariant(o->rows()).toJson().contains(
        "must-not-reach-model"));
    QPointer<QTcpSocket> delayed;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/observers "))
        delayed = socket;
      else
        fixture.normal(socket, request);
    };
    o->reload();
    QTRY_VERIFY(delayed);
    // A project change cancels the old read and invalidates confirmation.
    QVERIFY(o->prepare("bind", fixture.observer, fixture.runtime));
    o->setProject({});
    QVERIFY(!o->confirm());
    QVERIFY(!o->canBind());
    QTRY_VERIFY(!o->online());
    QCOMPARE(o->rows().first().toMap().value("freshness").toString(),
             QString("unknown"));
    QVERIFY(!o->prepare("bind", fixture.observer, fixture.runtime));
    QVERIFY(!o->message().contains(QString(64, 'a')));
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      fixture.observation(socket, request);
    };
    fixture.registry.insert("database_instance_id",
                            "77777777-7777-4777-8777-777777777777");
    o->reload();
    QTRY_VERIFY(o->rows().isEmpty());
    QVERIFY(!o->online());
    QVERIFY(o->message().contains("database changed"));
    o->setActive(false);
    const int stoppedReads = fixture.reads;
    QTest::qWait(2100);
    QCOMPARE(fixture.reads, stoppedReads);
    fixture.registry.insert("database_instance_id", fixture.instance);
    o->setActive(true);
    QTRY_VERIFY(o->online());
    QCOMPARE(o->rows().size(), 2);
  }
  void productionQmlLoadsOfflineAndRedactsErrors() {
    QTemporaryDir dir;
    ApiClient client;
    QVERIFY(!client.configure(QUrl("http://127.0.0.1:7331"),
                              dir.filePath("missing")));
    Panel panel(&client);
    Appearance appearance(dir.path(), dir.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    QTRY_VERIFY(window->isVisible());
    QCOMPARE(window->objectName(), QString("mainWindow"));
    QVERIFY(item(window, "errorLabel")
                ->property("text")
                .toString()
                .contains("credential"));
    QVERIFY(!item(window, "errorLabel")
                 ->property("text")
                 .toString()
                 .contains(dir.path()));
    QVERIFY(!item(window, "createProjectButton")->isEnabled());
    QVERIFY(!panel.editProject(true));
    QVERIFY(!panel.save());
  }
  void themeLanguageAndFallbackAreLive() {
    QTemporaryDir dir;
    QVERIFY(QDir(dir.path()).mkpath("theme"));
    writeFile(dir.filePath("theme/colors.toml"), "accent = '#123456'\n");
    writeFile(dir.filePath("language"), "ru\n");
    writeFile(dir.filePath("rounding"), "#!/bin/sh\nprintf '{\"int\":6}'\n");
    QFile::setPermissions(dir.filePath("rounding"), QFile::ReadOwner |
                                                        QFile::WriteOwner |
                                                        QFile::ExeOwner);
    Appearance appearance(dir.path(), dir.filePath("language"),
                          dir.filePath("rounding"));
    QCoreApplication::installTranslator(&appearance);
    ApiClient client;
    Panel panel(&client);
    QQmlApplicationEngine engine;
    connect(&appearance, &Appearance::changed, &engine,
            &QQmlEngine::retranslate);
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    QCOMPARE(appearance.accent(), QString("#123456"));
    QCOMPARE(appearance.language(), QString("ru"));
    QCOMPARE(item(window, "createProjectButton")->property("text").toString(),
             QString("Создать проект"));
    QTRY_COMPARE(appearance.radius(), 6);
    QSignalSpy unchanged(&appearance, &Appearance::changed);
    appearance.refresh();
    QCOMPARE(unchanged.size(), 0);
    // Atomic replacement drops inode watches; the parent watch re-arms them.
    writeFile(dir.filePath("new-language"), "de\n");
    QVERIFY(QFile::remove(dir.filePath("language")));
    QVERIFY(
        QFile::rename(dir.filePath("new-language"), dir.filePath("language")));
    QTRY_COMPARE(appearance.language(), QString("en"));
    QTRY_COMPARE(
        item(window, "createProjectButton")->property("text").toString(),
        QString("Create project"));
    writeFile(dir.filePath("theme/colors.toml"), "accent = '#abcdef'\n");
    QTRY_COMPARE(appearance.accent(), QString("#abcdef"));
    writeFile(dir.filePath("theme/colors.toml"), "accent = 'invalid'\n");
    QTRY_COMPARE(appearance.accent(), QString("#FFD60A"));
    QCoreApplication::removeTranslator(&appearance);
  }
  void realQmlCrudQueueConflictHistoryAndClose() {
    Daemon daemon;
    daemon.start();
    QVERIFY(daemon.process.waitForStarted(5000));
    QByteArray output;
    QTRY_VERIFY_WITH_TIMEOUT((output += daemon.process.readAllStandardOutput())
                                 .contains("arteld: listening "),
                             5000);
    ApiClient client;
    const QUrl endpoint("http://" +
                        QString::fromUtf8(output).trimmed().section(' ', -1));
    QVERIFY(
        client.configure(endpoint, daemon.dir.filePath("private/owner.token")));
    Panel panel(&client);
    Appearance appearance(daemon.dir.path(), daemon.dir.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QTest::qWait(100);
    QVERIFY(QDir(daemon.dir.path()).mkdir("workspace"));
    QVERIFY(click(window, "createProjectButton"));
    QTRY_COMPARE(panel.editorKind(), QString("project"));
    QVERIFY(enter(window, "nameField", "Учебный проект"));
    QVERIFY(enter(window, "rootField", daemon.dir.filePath("workspace")));
    QTest::keyClick(window, Qt::Key_Tab);
    QVERIFY(window->activeFocusItem() != item(window, "rootField"));
    QVERIFY(enter(window, "descriptionField", "Пример <b>обычного текста</b>"));
    QTest::keyClick(window, Qt::Key_S, Qt::ControlModifier);
    QTRY_COMPARE(panel.projects().size(), 1);
    QTRY_VERIFY(!panel.dirty());
    const auto projectID =
        panel.projects().first().toMap().value("id").toString();
    QTRY_VERIFY(item(window, "project_" + projectID));
    QVERIFY(click(window, "project_" + projectID));
    QCOMPARE(panel.project().value("name").toString(),
             QString("Учебный проект"));
    QStringList ids;
    for (int i = 0; i < 3; ++i) {
      QVERIFY(click(window, "createTaskButton"));
      QVERIFY(enter(window, "nameField", QString("Задача %1").arg(i + 1)));
      QVERIFY(enter(window, "criteriaField", "Кириллица: проверка приёмки"));
      QVERIFY(click(window, "saveButton"));
      QTRY_COMPARE(panel.tasks().size(), i + 1);
      QTRY_VERIFY(!panel.dirty());
      for (const auto &row : panel.tasks()) {
        const auto id = row.toMap().value("id").toString();
        if (!ids.contains(id))
          ids.append(id);
      }
    }
    // Queue controls use complete ordered lists and the captured queue CAS.
    for (const auto &id : ids) {
      QTRY_VERIFY(item(window, "queue_" + id));
      QVERIFY(click(window, "queue_" + id));
      QTRY_VERIFY(!client.pending() && !client.loading());
      QTRY_COMPARE(client.tasks()->rowCount(), 3);
      QTRY_COMPARE(panel.tasks().last().toMap().value("state").toString(),
                   QString("queued"));
    }
    QVERIFY(panel.queue(ids[2], -1));
    QTRY_VERIFY(!client.pending() && !client.loading());
    QCOMPARE(panel.tasks()[1].toMap().value("id").toString(), ids[2]);
    QVERIFY(panel.queue(ids[2], -1));
    QTRY_VERIFY(!client.pending() && !client.loading());
    QCOMPARE(panel.tasks()[0].toMap().value("id").toString(), ids[2]);
    QVERIFY(click(window, "task_" + ids[2]));
    QTRY_VERIFY(panel.allowed("in_progress"));
    QVERIFY(click(window, "startButton"));
    QTRY_COMPARE(panel.task().value("state").toString(),
                 QString("in_progress"));
    QVERIFY(click(window, "doneButton"));
    QTRY_COMPARE(panel.task().value("state").toString(), QString("done"));
    QCOMPARE(panel.stateLabel("done"), QString("Completed manually"));
    QTRY_VERIFY(!panel.historyLoading());
    QTRY_VERIFY(!panel.history().isEmpty());
    QCOMPARE(panel.history().last().toMap().value("source").toString(),
             QString("owner_manual"));
    QVERIFY(!panel.canArchive());
    QVERIFY(!item(window, "archiveButton")->isEnabled());
    QVERIFY(click(window, "reopenButton"));
    QTRY_COMPARE(panel.task().value("state").toString(), QString("backlog"));
    QVERIFY(click(window, "cancelTaskButton"));
    QTRY_COMPARE(panel.task().value("state").toString(), QString("cancelled"));
    QVERIFY(click(window, "reopenButton"));
    QTRY_COMPARE(panel.task().value("state").toString(), QString("backlog"));
    for (int i = 0; i < 2; ++i) {
      QVERIFY(panel.queue(ids[i], 0));
      QTRY_VERIFY(!client.pending() && !client.loading());
    }
    QVERIFY(click(window, "editProjectButton"));
    QVERIFY(enter(window, "descriptionField", "Обновлённое описание проекта"));
    QVERIFY(click(window, "saveButton"));
    QTRY_VERIFY(!panel.dirty() && !client.loading());
    QTRY_COMPARE(panel.project().value("description").toString(),
                 QString("Обновлённое описание проекта"));
    QVERIFY(panel.canArchive());
    QVERIFY(click(window, "archiveButton"));
    QTRY_VERIFY(panel.project().value("archived").toBool());
    QVERIFY(!item(window, "editProjectButton")->isEnabled());
    QVERIFY(!item(window, "createTaskButton")->isEnabled());
    QVERIFY(!panel.editTask(true));
    QVERIFY(!panel.editProject(false));
    QVERIFY(!panel.queue(ids[2], 0));
    QVERIFY(!panel.allowed("cancelled"));
    QVERIFY(click(window, "archivedFilter"));
    QVERIFY(panel.showArchived());
    QVERIFY(click(window, "archiveButton"));
    QTRY_VERIFY(!panel.project().value("archived").toBool());
    QVERIFY(QDir(daemon.dir.path()).rename("workspace", "moved-workspace"));
    panel.reload();
    QTRY_VERIFY(!client.loading());
    QVERIFY(item(window, "pathWarning")->isVisible());
    QCOMPARE(panel.tasks().size(), 3);
    // A second HTTP client edits the selected task while this editor retains
    // its original version. The first save must conflict, not overwrite.
    QVERIFY(click(window, "editTaskButton"));
    const QString draft = "Несохранённый текст " + QString(600, QChar(0x044f));
    QVERIFY(enter(window, "descriptionField", draft));
    QVERIFY(enter(window, "criteriaField", "Обновлённые критерии приёмки"));
    ApiClient other;
    QVERIFY(
        other.configure(endpoint, daemon.dir.filePath("private/owner.token")));
    other.reload();
    QTRY_VERIFY(other.online());
    QVERIFY(other.save("PATCH", "/v1/tasks/" + ids[2],
                       {{"expected_version", panel.task().value("version")},
                        {"description", "Другой редактор"}}));
    QTRY_VERIFY(!other.pending() && !other.loading());
    QVERIFY(click(window, "saveButton"));
    QTRY_VERIFY(panel.conflict());
    QCOMPARE(panel.editor().value("description").toString(), draft);
    QVERIFY(click(window, "reloadButton"));
    QTRY_VERIFY(!client.loading());
    QTRY_COMPARE(panel.task().value("description").toString(),
                 QString("Другой редактор"));
    QVERIFY(click(window, "rebaseButton"));
    QVERIFY(click(window, "saveButton"));
    QTRY_VERIFY(!panel.dirty());
    QTRY_COMPARE(panel.task().value("description").toString(), draft);
    QCOMPARE(panel.task().value("acceptance_criteria").toString(),
             QString("Обновлённые критерии приёмки"));
    // In-memory cached data remains readable, but offline mutation is disabled.
    QVERIFY(click(window, "editTaskButton"));
    QVERIFY(enter(window, "descriptionField", "Черновик без связи"));
    client.stop();
    QVERIFY(!item(window, "saveButton")->isEnabled());
    QCOMPARE(panel.editor().value("description").toString(),
             QString("Черновик без связи"));
    window->close();
    QTRY_VERIFY(window->isVisible());
    auto *dialog = window->findChild<QObject *>("closeDialog");
    QVERIFY(dialog);
    QVERIFY(dialog->property("visible").toBool());
    QVERIFY(QMetaObject::invokeMethod(dialog, "reject"));
    QTRY_VERIFY(!dialog->property("visible").toBool());
    QTest::qWait(250);
    QVERIFY(window->isVisible());
    // Escape uses a confirmation too; single-letter typing never quits.
    item(window, "descriptionField")->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Escape);
    auto *discard = window->findChild<QObject *>("discardDialog");
    QTRY_VERIFY(discard->property("visible").toBool());
    QVERIFY(QMetaObject::invokeMethod(discard, "discarded"));
    QVERIFY(QMetaObject::invokeMethod(discard, "close"));
    QTRY_VERIFY(!panel.dirty());
    other.stop();
  }
  void unknownSaveRetryAndHistoryPagination() {
    UiFixture fixture;
    bool drop = true, invalidHistory = false;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST")) {
        if (drop)
          socket->disconnectFromHost();
        else
          UiFixture::respond(
              socket, 201,
              ("{\"id\":\"" + fixture.project + "\",\"version\":1}").toUtf8());
      } else if (request.startsWith("GET /v1/events?")) {
        if (invalidHistory) {
          UiFixture::respond(socket, 200, "{\"events\":[]}");
          return;
        }
        const bool second = request.contains("after=20&");
        QJsonArray events;
        for (int i = second ? 21 : 1; i <= (second ? 25 : 20); ++i)
          events.append(QJsonObject{{"seq", i},
                                    {"action", "task.edit"},
                                    {"project_id", fixture.project},
                                    {"task_id", fixture.task},
                                    {"source", "owner_manual"},
                                    {"timestamp", "2026-11-17T12:00:00Z"},
                                    {"before", QJsonObject{}},
                                    {"after", QJsonObject{}}});
        UiFixture::respond(
            socket, 200,
            QJsonDocument(QJsonObject{{"instance_id", fixture.instance},
                                      {"events", events},
                                      {"next_cursor", second ? 25 : 20}})
                .toJson());
      } else
        fixture.normal(socket, request);
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_COMPARE(panel.history().size(), 20);
    QVERIFY(panel.historyMore());
    QCOMPARE(panel.history().first().toMap().value("label").toString(),
             QString("Task edited"));
    QVERIFY(
        panel.history().first().toMap().value("details").toString().contains(
            "Before:"));
    QTest::qWait(100);
    auto *scroll = item(window, "detailsScroll");
    auto *flickable = scroll->property("contentItem").value<QQuickItem *>();
    QVERIFY(flickable);
    flickable->setProperty("contentY",
                           flickable->property("contentHeight").toReal() -
                               flickable->height());
    QTest::qWait(100);
    QVERIFY(click(window, "historyMoreButton"));
    QTRY_COMPARE(panel.history().size(), 25);
    QVERIFY(!panel.historyMore());
    invalidHistory = true;
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_VERIFY(!panel.historyLoading());
    QVERIFY(panel.message().contains("History unavailable"));
    QVERIFY(panel.history().isEmpty());
    QVERIFY(panel.historyMore());
    invalidHistory = false;
    panel.moreHistory();
    QTRY_COMPARE(panel.history().size(), 20);
    QVERIFY(!panel.message().contains("History unavailable"));
    // The UI displays uncertainty and can only retry the original request.
    flickable->setProperty("contentY", 0);
    QVERIFY(panel.editTask(false));
    QVERIFY(enter(window, "descriptionField", "Unknown result draft"));
    QVERIFY(click(window, "saveButton"));
    // Task saves use PATCH rather than POST.
    QTRY_VERIFY(!client.pending());
    QVERIFY(panel.dirty());
    panel.discard();
    QVERIFY(click(window, "createProjectButton"));
    QVERIFY(enter(window, "nameField", "Unknown save"));
    QVERIFY(enter(window, "rootField", "/example/workspace"));
    QVERIFY(click(window, "saveButton"));
    QTRY_VERIFY(panel.uncertain());
    QVERIFY(!item(window, "saveButton")->isEnabled());
    QVERIFY(item(window, "retryButton")->isVisible());
    QVERIFY(!panel.selectProject(""));
    const auto original = fixture.mutations.last();
    drop = false;
    QVERIFY(click(window, "retryButton"));
    QTRY_VERIFY(!panel.uncertain() && !client.pending());
    QCOMPARE(fixture.mutations.last(), original);
    QVERIFY(!panel.dirty());
    client.stop();
  }
  void retainedEditorAdoptsConfirmedIdentity_data() {
    QTest::addColumn<bool>("project");
    QTest::addColumn<bool>("create");
    QTest::addColumn<bool>("retry");
    QTest::addColumn<bool>("discard");
    for (bool project : {false, true})
      for (bool create : {false, true})
        for (bool retry : {false, true}) {
          const auto name =
              QString("%1-%2-%3")
                  .arg(project ? "project" : "task", create ? "create" : "edit",
                       retry ? "retry" : "reply");
          QTest::newRow(qPrintable(name))
              << project << create << retry << false;
          if (project && create)
            QTest::newRow(qPrintable(name + "-discard"))
                << project << create << retry << true;
        }
  }
  void retainedEditorAdoptsConfirmedIdentity() {
    QFETCH(bool, project);
    QFETCH(bool, create);
    QFETCH(bool, retry);
    QFETCH(bool, discard);
    UiFixture fixture;
    QPointer<QTcpSocket> held, heldHistory;
    bool holdHistory = false;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (holdHistory && request.startsWith("GET /v1/events?"))
        heldHistory = socket;
      else if (!request.startsWith("GET"))
        held = socket;
      else
        fixture.normal(socket, request);
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_VERIFY(!panel.history().isEmpty());
    bool selectionClearedAtReply = false;
    connect(&client, &ApiClient::mutationSucceeded, &panel, [&] {
      selectionClearedAtReply = panel.task().isEmpty() &&
                                panel.history().isEmpty() &&
                                !panel.historyLoading();
    });
    QVERIFY(project ? panel.editProject(create) : panel.editTask(create));
    const QString field = project ? "name" : "title";
    panel.field(field, "Submitted text");
    QVERIFY(panel.save());
    QTRY_VERIFY(held);
    QVERIFY(
        !panel.abandonUncertain()); // A pending request cannot be forgotten.
    panel.field(field, "Newer draft during save");
    if (retry) {
      held->disconnectFromHost();
      held = nullptr;
      QTRY_VERIFY(panel.uncertain());
      QVERIFY(!panel.abandonUncertain()); // Same database: retry only.
      QVERIFY(panel.retry());
      QTRY_VERIFY(held);
      QCOMPARE(fixture.mutations.size(), 2);
      QCOMPARE(fixture.mutations[0], fixture.mutations[1]);
      panel.field(field, "Newer draft during retry");
    }
    const auto retained = panel.editor();
    const auto id = create ? QString("55555555-5555-4555-8555-555555555555")
                           : (project ? fixture.project : fixture.task);
    const int version = create ? 1 : 2;
    // Simulate the committed object in the next snapshot as well as its reply.
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    const QString collection = project ? "projects" : "tasks";
    auto rows = snapshot.value(collection).toArray();
    auto row = rows.first().toObject();
    row.insert("id", id);
    row.insert("version", version);
    row.insert(field, "Submitted text");
    if (create)
      rows.append(row);
    else
      rows[0] = row;
    snapshot.insert(collection, rows);
    snapshot.insert("event_seq", 26);
    fixture.state = QJsonDocument(snapshot).toJson();
    if (project && create) {
      // A previous task's in-flight history must be cancelled at adoption,
      // not merely hidden after the next snapshot has arrived.
      holdHistory = true;
      client.loadHistory(fixture.project, fixture.task, 0);
      QTRY_VERIFY(heldHistory);
      QVERIFY(panel.historyLoading());
    }
    UiFixture::respond(
        held, 200,
        QJsonDocument(QJsonObject{{"id", id}, {"version", version}}).toJson());
    held = nullptr;
    QTRY_VERIFY(!client.pending() && !panel.uncertain() && !client.loading());
    QCOMPARE(panel.editor(), retained);
    QVERIFY(panel.dirty());
    if (project && create) {
      QVERIFY(selectionClearedAtReply);
      QTRY_VERIFY(!heldHistory ||
                  heldHistory->state() == QAbstractSocket::UnconnectedState);
      QCOMPARE(panel.project().value("id").toString(), id);
      QVERIFY(panel.task().isEmpty());
      QVERIFY(panel.history().isEmpty());
    } else if (project) {
      // Editing the current project must retain its selected task.
      QCOMPARE(panel.task().value("id").toString(), fixture.task);
    }
    if (discard) {
      panel.discard();
      QVERIFY(!panel.dirty());
      QVERIFY(!panel.editTask(false));
      QVERIFY(!panel.allowed("cancelled"));
      QVERIFY(!panel.transition("cancelled"));
      QCOMPARE(fixture.mutations.size(), retry ? 2 : 1);
      client.stop();
      return;
    }
    QVERIFY(panel.save());
    QTRY_VERIFY(held);
    const auto request = fixture.mutations.last();
    const auto expectedPath =
        "PATCH /v1/" + collection.toUtf8() + "/" + id.toUtf8() + " ";
    QVERIFY(request.startsWith(expectedPath));
    const auto body =
        QJsonDocument::fromJson(request.mid(request.indexOf("\r\n\r\n") + 4))
            .object();
    QCOMPARE(body.value("expected_version").toInt(), version);
    QCOMPARE(body.value(field).toString(), retained.value(field).toString());
    UiFixture::respond(
        held, 200,
        QJsonDocument(QJsonObject{{"id", id}, {"version", version + 1}})
            .toJson());
    QTRY_VERIFY(!client.pending());
    QVERIFY(!panel.dirty());
    QVERIFY(!panel.conflict());
    QCOMPARE(fixture.mutations.size(), retry ? 3 : 2);
    if (project && create) {
      QVERIFY(!panel.editTask(false));
      QVERIFY(!panel.allowed("cancelled"));
      QVERIFY(!panel.transition("cancelled"));
      QVERIFY(panel.task().isEmpty());
      QVERIFY(panel.history().isEmpty());
    }
    client.stop();
  }
  void replacedDatabaseAllowsAcknowledgedAbandonment_data() {
    QTest::addColumn<bool>("draft");
    QTest::newRow("editor-draft") << true;
    QTest::newRow("action-without-draft") << false;
  }
  void replacedDatabaseAllowsAcknowledgedAbandonment() {
    QFETCH(bool, draft);
    UiFixture fixture;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (!request.startsWith("GET"))
        socket->disconnectFromHost();
      else
        fixture.normal(socket, request);
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QVERIFY(panel.selectProject(fixture.project));
    if (draft) {
      QVERIFY(panel.editTask(true));
      panel.field("title", "Keep this text from the old database");
      QVERIFY(panel.save());
    } else {
      QVERIFY(panel.archive());
    }
    const auto originalDraft = panel.editor();
    QTRY_VERIFY(panel.uncertain());
    QVERIFY(!panel.canAbandon());
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    snapshot.insert("instance_id", "44444444-4444-4444-8444-444444444444");
    fixture.state = QJsonDocument(snapshot).toJson();
    client.reload();
    QTRY_VERIFY(panel.canAbandon());
    QVERIFY(!panel.retryable());
    QVERIFY(!panel.retry());
    panel.discard();
    QCOMPARE(panel.editor(), originalDraft);
    QVERIFY(click(window, "abandonButton"));
    auto *dialog = window->findChild<QObject *>("abandonDialog");
    QVERIFY(dialog);
    QTRY_VERIFY(dialog->property("visible").toBool());
    QVERIFY(QMetaObject::invokeMethod(dialog, "reject"));
    QVERIFY(panel.uncertain());
    QVERIFY(click(window, "abandonButton"));
    QTRY_VERIFY(dialog->property("visible").toBool());
    QVERIFY(QMetaObject::invokeMethod(dialog, "accept"));
    QVERIFY(!panel.uncertain());
    QCOMPARE(panel.editor(), originalDraft);
    QVERIFY(!panel.retry());
    QCOMPARE(fixture.mutations.size(),
             1);            // Neither confirmation nor retry replays.
    QVERIFY(!panel.save()); // Old draft never silently migrates to the new DB.
    if (draft) {
      QVERIFY(panel.dirty());
      QVERIFY(!panel.rebase());
      QVERIFY(!panel.selectProject(""));
      panel.discard();
    }
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.editTask(true));
    panel.field("title", "A deliberate new save");
    QVERIFY(panel.save());
    QTRY_COMPARE(fixture.mutations.size(), 2);
    client.stop();
  }
  void databaseReplacementPreservesButDoesNotSaveOldDraft() {
    UiFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(panel.editTask(false));
    QVERIFY(enter(window, "descriptionField",
                  "Draft belongs to the original database"));
    auto object = QJsonDocument::fromJson(fixture.state).object();
    object.insert("instance_id", "44444444-4444-4444-8444-444444444444");
    fixture.state = QJsonDocument(object).toJson();
    client.reload();
    QTRY_COMPARE(client.instance(),
                 QString("44444444-4444-4444-8444-444444444444"));
    QVERIFY(!panel.saveable());
    QVERIFY(!panel.save());
    QVERIFY(!panel.rebase());
    QVERIFY(panel.message().contains("Database changed"));
    QCOMPARE(panel.editor().value("description").toString(),
             QString("Draft belongs to the original database"));
    panel.discard();
    QVERIFY(!panel.dirty());
    client.stop();
  }
  void fixtureScreenshot() {
    UiFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_VERIFY(!panel.history().isEmpty());
    // Render only made-up data and a fixture path, never a real HOME/token.
    QTest::qWait(250);
    const auto image = window->grabWindow();
    QVERIFY(!image.isNull());
    QCOMPARE(image.size(), QSize(1280, 820));
    const auto path = qEnvironmentVariable("ARTEL_SCREENSHOT");
    QVERIFY(image.save(path.isEmpty() ? fixture.directory.filePath("screen.png")
                                      : path));
    QCoreApplication::installTranslator(&appearance);
    connect(&appearance, &Appearance::changed, &engine,
            &QQmlEngine::retranslate);
    connect(&appearance, &Appearance::changed, &panel, &Panel::changed);
    writeFile(fixture.directory.filePath("language"), "ru\n");
    QTRY_COMPARE(appearance.language(), QString("ru"));
    QTRY_COMPARE(
        item(window, "createProjectButton")->property("text").toString(),
        QString("Создать проект"));
    QTest::qWait(250);
    const auto russian = window->grabWindow();
    QVERIFY(!russian.isNull());
    QVERIFY(russian.save(path.isEmpty()
                             ? fixture.directory.filePath("screen-ru.png")
                             : QFileInfo(path).absolutePath() +
                                   "/manual-panel-ru.png"));
    QCoreApplication::removeTranslator(&appearance);
    client.stop();
  }
  void missingQmlFails() {
    QQmlApplicationEngine engine;
    engine.setOutputWarningsToStandardError(false);
    QSignalSpy warnings(&engine, &QQmlEngine::warnings);
    QTest::ignoreMessage(QtWarningMsg,
                         "QQmlApplicationEngine failed to load component");
    engine.load(QUrl("qrc:/Missing.qml"));
    QVERIFY(engine.rootObjects().isEmpty());
    QVERIFY(!warnings.isEmpty());
  }
};
QTEST_MAIN(ArtelTests)
#include "artel_tests.moc"
