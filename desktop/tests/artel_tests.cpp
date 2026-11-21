#include "agtermfixture.h"
#include "apiclient.h"
#include "appearance.h"
#include "observerfixture.h"
#include "panel.h"
#include "uifixture.h"
#include "workspacecontroller.h"
#include <QAccessible>
#include <QClipboard>
#include <QFile>
#include <QFileInfo>
#include <QInputMethodEvent>
#include <QJsonDocument>
#include <QProcess>
#include <QQmlApplicationEngine>
#include <QQmlIncubationController>
#include <QQuickItem>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QTcpServer>
#include <QTcpSocket>
#include <QTemporaryDir>
#include <QUrlQuery>
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
  // Task rows contain independent queue buttons below their title. Select the
  // title area, not the geometric center which may legitimately hit Remove/Up.
  const auto y = name.startsWith("task_") ? 20.0 : control->height() / 2;
  QTest::mouseClick(
      window, Qt::LeftButton, {},
      control->mapToScene(QPointF(control->width() / 2, y)).toPoint());
  return true;
}
bool enter(QQuickWindow *window, const QString &name, const QString &text) {
  auto *control = item(window, name);
  if (!control)
    return false;
  control->forceActiveFocus();
  QTest::keyClick(window, Qt::Key_A, Qt::ControlModifier);
  // Key handling and text changes can replace a QML delegate.
  control = item(window, name);
  if (!control)
    return false;
  QInputMethodEvent event;
  event.setCommitString(text);
  QCoreApplication::sendEvent(control, &event);
  control = item(window, name);
  return control && control->property("text").toString() == text;
}
void writeFile(const QString &path, const QByteArray &bytes) {
  QFile file(path);
  QVERIFY(file.open(QIODevice::WriteOnly));
  QCOMPARE(file.write(bytes), bytes.size());
}
QQuickWindow *load(QQmlApplicationEngine &engine, Panel &panel,
                   Appearance &appearance, bool manual = true) {
  auto *workspace = new WorkspaceController(&panel, &engine);
  engine.setInitialProperties(
      {{"panel", QVariant::fromValue(&panel)},
       {"workspace", QVariant::fromValue(workspace)},
       {"appearance", QVariant::fromValue(&appearance)}});
  engine.load(QUrl("qrc:/Main.qml"));
  if (manual && !engine.rootObjects().isEmpty()) {
    auto *window = qobject_cast<QQuickWindow *>(engine.rootObjects().first());
    if (!window || !click(window, "projectsNavButton"))
      return nullptr;
    workspace->returnToEditor();
  }
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
  void teamRosterProjectionAndClock() {
    ObserverFixture fixture;
    auto record = fixture.record();
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", fixture.project}});
    auto runtimes = record.value("runtimes").toArray();
    auto bound = runtimes.first().toObject();
    bound.insert("disposition", "bound");
    runtimes[0] = bound;
    auto extra = runtimes.last().toObject();
    auto reg = extra.value("registration").toObject();
    reg.insert("runtime_id", "77777777-7777-4777-8777-777777777777");
    extra.insert("registration", reg);
    runtimes.append(extra);
    record.insert("runtimes", runtimes); // One binding plus two candidates.
    record.insert("private-token", "must-not-escape");
    fixture.record(record);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    auto now = QDateTime::fromString("2026-11-17T12:02:00Z", Qt::ISODate);
    TeamOverviewModel model(&panel, nullptr, [&] { return now; });
    auto *o = panel.observers();
    o->setActive(true);
    QTRY_VERIFY(o->online());
    QCOMPARE(model.total(), 1);
    QCOMPARE(model.shown(), 1);
    QCOMPARE(model.counts().value("working").toInt(), 1);
    auto row = model.find(fixture.observer, fixture.runtime);
    QCOMPARE(row.value("age_seconds").toInt(), 120);
    QCOMPARE(row.value("project_name").toString(), QString("Example project"));
    QVERIFY(!QJsonDocument::fromVariant(o->projection())
                 .toJson()
                 .contains("must-not-escape"));
    auto copy = o->projection();
    copy.clear();
    QCOMPARE(o->projection().size(), 1);
    QSignalSpy resets(&model, &QAbstractItemModel::modelReset);
    const int reads = fixture.reads;
    now = now.addSecs(60);
    model.tick();
    QCOMPARE(model.find(fixture.observer, fixture.runtime)
                 .value("age_seconds")
                 .toInt(),
             180);
    QCOMPARE(resets.size(), 0);
    QTest::qWait(100); // Allow any erroneous request to reach the fixture.
    QCOMPARE(fixture.reads, reads);
    QCOMPARE(model.counts().value("working").toInt(), 1);
    model.setSearch("example PROJECT");
    QCOMPARE(model.shown(), 1);
    model.setSearch("no matches");
    QCOMPARE(model.shown(), 0);
    QCOMPARE(model.total(), 1);
    model.setSearch("");
    model.setProjectId(fixture.task);
    QCOMPARE(model.shown(), 0);
    QCOMPARE(panel.project().value("id").toString(), fixture.project);
    model.setProjectId("");
    model.setActivity("working");
    QCOMPARE(model.shown(), 1);
    model.setActivity("idle");
    QCOMPARE(model.shown(), 0);
    model.setActivity("");
    o->setOnlyProject(true);
    QCOMPARE(model.total(), 1); // Management filters are independent.
    o->setActive(false);
    QVERIFY(model.cached());
    QCOMPARE(model.counts().value("unknown").toInt(), 1);
    row = model.find(fixture.observer, fixture.runtime);
    QCOMPARE(row.value("activity").toString(), QString("unknown"));
    QCOMPARE(row.value("last_activity").toString(), QString("working"));
    QCOMPARE(row.value("freshness").toString(), QString("unknown"));
    QVERIFY(fixture.mutations.isEmpty());
    QTemporaryDir dir;
    writeFile(dir.filePath("language"), "ru\n");
    Appearance appearance(dir.path(), dir.filePath("language"));
    QCoreApplication::installTranslator(&appearance);
    QVERIFY(model.find(fixture.observer, fixture.runtime)
                .value("age_text")
                .toString()
                .contains("мин"));
    QCoreApplication::removeTranslator(&appearance);
  }
  void teamRosterMembershipAndScale() {
    ObserverFixture fixture;
    const auto base = fixture.record();
    const auto baseRuntime =
        base.value("runtimes").toArray().first().toObject();
    QJsonArray observers;
    const QStringList activities{"working", "idle",    "waiting_user",
                                 "working", "working", "unknown"};
    for (int i = 0; i < 256; ++i) {
      auto o = base;
      o.insert("observer_id",
               QUuid::createUuid().toString(QUuid::WithoutBraces));
      o.insert("label", "Одинаковое имя 🚀");
      auto r = baseRuntime;
      r.insert("disposition", "bound");
      r.insert("activity", activities[i % 6]);
      if (i % 6 == 3)
        r.insert("freshness", "stale");
      if (i % 6 == 4) {
        r.insert("freshness", "stopped");
        r.insert("lifecycle", "stopped");
      }
      o.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                      {"project_id", fixture.project}});
      o.insert("runtimes", QJsonArray{r});
      if (i == 0)
        o.insert("revoked", true);
      if (i == 1) {
        o.remove("binding");
        o.insert("runtimes", QJsonArray{});
      }
      if (i == 2) {
        o.remove("binding");
        r.insert("disposition", "superseded");
        o.insert("runtimes", QJsonArray{r});
      }
      if (i == 3)
        o.insert("runtimes",
                 QJsonArray{}); // Missing bound runtime: explicit error.
      if (i == 4)
        o.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                        {"project_id", fixture.task}});
      observers.append(o);
    }
    fixture.registry.insert("observers", observers);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    TeamOverviewModel model(&panel);
    auto *o = panel.observers();
    o->setActive(true);
    QTRY_VERIFY(o->online());
    QCOMPARE(model.total(), 253);
    QCOMPARE(model.rowCount(), 253);
    QCOMPARE(model.unbound(), 2);
    int sum = 0;
    for (const auto &n : model.counts())
      sum += n.toInt();
    QCOMPARE(sum, model.total());
    QCOMPARE(model.counts().value("inconsistent").toInt(), 1);
    QCOMPARE(model.counts().value("working").toInt(), 42);
    QCOMPARE(model.counts().value("idle").toInt(), 42);
    QCOMPARE(model.counts().value("waiting_user").toInt(), 42);
    QCOMPARE(model.counts().value("stopped").toInt(), 42);
    QCOMPARE(model.counts().value("unknown").toInt(), 84);
    for (int i = 0; i < model.rowCount(); ++i) {
      const auto row = model.data(model.index(i), Qt::UserRole).toMap();
      if (row.value("freshness") == "stale") {
        QCOMPARE(row.value("status").toString(), QString("unknown"));
        QCOMPARE(row.value("activity").toString(), QString("unknown"));
      }
    }
    const auto missing =
        observers.at(4).toObject().value("observer_id").toString();
    const auto row = model.find(missing, fixture.runtime);
    QVERIFY(!row.value("project_available").toBool());
    QVERIFY(row.value("project_name").toString().contains(fixture.task));
    QCOMPARE(row.value("status").toString(), QString("stopped"));
    model.setActivity("waiting_user");
    QCOMPARE(model.shown(), model.counts().value("waiting_user").toInt());
    model.setActivity("stale");
    QVERIFY(model.shown() > 0);
    o->setActive(false);
    model.setActivity("");
    fixture.registry.insert("observers", QJsonArray{});
    o->setActive(true);
    QTRY_VERIFY(o->online());
    QCOMPARE(model.total(), 0);
    QCOMPARE(model.unbound(), 0);
  }
  void teamCacheExpiryAndArchivedProject() {
    ObserverFixture fixture;
    auto record = fixture.record();
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", fixture.project}});
    auto runtime = record.value("runtimes").toArray().first().toObject();
    runtime.insert("disposition", "bound");
    record.insert("runtimes", QJsonArray{runtime});
    fixture.record(record);
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    auto project = snapshot.value("projects").toArray().first().toObject();
    project.insert("archived", true);
    snapshot.insert("projects", QJsonArray{project});
    fixture.state = QJsonDocument(snapshot).toJson();
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.projects().isEmpty());
    TeamOverviewModel team(&panel);
    auto *o = panel.observers();
    o->setActive(true);
    QTRY_VERIFY(o->online());
    const auto row = team.find(fixture.observer, fixture.runtime);
    QCOMPARE(row.value("project_name").toString(), QString("Example project"));
    QVERIFY(!row.value("project_available").toBool());
    QVERIFY(row.value("project_archived").toBool());
    // Hold the next poll: cache expires even if the UI clock is running and
    // no failure response has arrived yet. Never use a real observer service.
    QPointer<QTcpSocket> delayed;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/observers "))
        delayed = socket;
      else
        fixture.normal(socket, request);
    };
    o->setTimeout(10000);
    o->reload();
    QTRY_VERIFY(delayed);
    QSignalSpy resets(&team, &QAbstractItemModel::modelReset);
    QTest::qWait(4050);
    team.tick();
    QVERIFY(team.cached());
    QCOMPARE(team.counts().value("unknown").toInt(), 1);
    QCOMPARE(team.counts().value("working").toInt(), 0);
    QCOMPARE(resets.size(), 0);
    UiFixture::respond(delayed, 200, fixture.bytes());
    QTRY_VERIFY(!team.cached());
    QCOMPARE(team.counts().value("working").toInt(), 1);
    QVERIFY(fixture.mutations.isEmpty());
  }
  void workspaceNavigationAndSelection() {
    ObserverFixture fixture;
    auto record = fixture.record();
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", fixture.project}});
    auto runtimes = record.value("runtimes").toArray();
    auto first = runtimes.first().toObject();
    first.insert("disposition", "bound");
    runtimes[0] = first;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    WorkspaceController workspace(&panel);
    QCOMPARE(workspace.route(), QString("team"));
    QVERIFY(!panel.observers()->active());
    client.reload();
    QTRY_VERIFY(client.online());
    workspace.setWindowVisible(true);
    QTRY_VERIFY(panel.observers()->online());
    QVERIFY(workspace.selectObserver(fixture.observer, fixture.runtime));
    workspace.team()->setSearch("no match");
    QVERIFY(workspace.selectedObserver().isEmpty());
    workspace.team()->setSearch("");
    QVERIFY(workspace.selectedObserver().isEmpty()); // No implicit reselection.
    QVERIFY(workspace.selectObserver(fixture.observer, fixture.runtime));
    auto second = record;
    const auto otherId = QUuid::createUuid().toString(QUuid::WithoutBraces);
    second.insert("observer_id", otherId);
    fixture.registry.insert("observers", QJsonArray{record, second});
    panel.observers()->reload();
    QTRY_COMPARE(workspace.team()->rowCount(), 2);
    fixture.registry.insert("observers", QJsonArray{second, record});
    panel.observers()->reload();
    QTRY_COMPARE(workspace.team()
                     ->data(workspace.team()->index(0), Qt::UserRole)
                     .toMap()
                     .value("observer_id")
                     .toString(),
                 otherId);
    QCOMPARE(workspace.selectedObserver().value("observer_id").toString(),
             fixture.observer);
    QCOMPARE(workspace.selectedObserver().value("runtime_id").toString(),
             fixture.runtime);
    QVERIFY(!workspace.openObserverProject(fixture.observer, fixture.runtime,
                                           fixture.project, 99));
    QVERIFY(workspace.openObserverProject(fixture.observer, fixture.runtime,
                                          fixture.project, 1));
    QCOMPARE(workspace.route(), QString("workspace"));
    QVERIFY(!panel.observers()->active());
    workspace.setConnectionsOpen(true);
    QTRY_VERIFY(panel.observers()->online());
    workspace.setMinimized(true);
    QVERIFY(!panel.observers()->active());
    workspace.setMinimized(false);
    QTRY_VERIFY(panel.observers()->online());
    workspace.setConnectionsOpen(false);
    QVERIFY(!panel.observers()->active());
    workspace.showTeam();
    QTRY_VERIFY(panel.observers()->online());
    record.insert("binding", QJsonObject{{"runtime_id", fixture.otherRuntime},
                                         {"project_id", fixture.project}});
    record.insert("version", 2);
    fixture.record(record);
    panel.observers()->reload();
    QTRY_VERIFY(workspace.selectedObserver().isEmpty());
    QVERIFY(!workspace.openObserverProject(fixture.observer, fixture.runtime,
                                           fixture.project, 1));
    workspace.setWindowVisible(false);
    QVERIFY(!panel.observers()->active());
    QVERIFY(fixture.mutations.isEmpty());
  }
  void workspaceManualGuards_data() {
    QTest::addColumn<bool>("conflict");
    QTest::newRow("conflict") << true;
    QTest::newRow("unknown-cross-database") << false;
  }
  void workspaceManualGuards() {
    QFETCH(bool, conflict);
    ObserverFixture fixture;
    ApiClient client;
    client.setTimeout(500);
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    WorkspaceController workspace(&panel);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(workspace.openProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(panel.editTask());
    panel.field("title", "Protected manual text");
    const auto draft = panel.editor();
    workspace.showTeam();
    QVERIFY(workspace.draftHidden());
    QVERIFY(!workspace.closeAllowed());
    QVERIFY(!workspace.saveShortcut());
    QVERIFY(!workspace.escapeShortcut());
    QVERIFY(!workspace.openProject(fixture.project));
    workspace.showProjects();
    QVERIFY(workspace.editorVisible());
    QVERIFY(!workspace.draftHidden());
    QVERIFY(!workspace.escapeShortcut());
    QCOMPARE(panel.editor(), draft);
    workspace.returnToEditor();
    QVERIFY(!workspace.escapeShortcut()); // Never silently discard dirty text.
    QPointer<QTcpSocket> pending;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("PATCH /v1/tasks/"))
        pending = socket;
      else
        fixture.observation(socket, request);
    };
    QVERIFY(workspace.saveShortcut());
    QTRY_VERIFY(pending);
    workspace.showTeam();
    QVERIFY(!workspace.closeAllowed());
    QVERIFY(!workspace.openProject(fixture.project));
    QVERIFY(!workspace.saveShortcut());
    QCOMPARE(panel.editor(), draft);
    if (conflict)
      UiFixture::respond(pending, 409, "{\"code\":\"version_conflict\"}");
    QTRY_VERIFY(!client.pending());
    if (conflict)
      QVERIFY(panel.conflict());
    else
      QVERIFY(panel.uncertain());
    QVERIFY(!workspace.closeAllowed());
    QVERIFY(!workspace.openProject(fixture.project));
    QVERIFY(!workspace.escapeShortcut());
    QCOMPARE(panel.editor(), draft);
    QCOMPARE(fixture.mutations.size(), 1);
    if (!conflict) {
      auto state = QJsonDocument::fromJson(fixture.state).object();
      state.insert("instance_id", "77777777-7777-4777-8777-777777777777");
      fixture.state = QJsonDocument(state).toJson();
      client.reload();
      QTRY_VERIFY(panel.canAbandon());
      QVERIFY(!workspace.saveShortcut());
      QVERIFY(!workspace.openProject(fixture.project));
      QVERIFY(panel.abandonUncertain());
      QCOMPARE(panel.editor(), draft);
      QVERIFY(!workspace.openProject(fixture.project)); // Draft still guarded.
      workspace.returnToEditor();
      QVERIFY(!workspace.escapeShortcut());
    }
  }
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
    for (const auto &accent : {"#ffffff", "#000000", "#808080", "#FFD60A",
                               "#a8c7fa", "invalid", "#00ffffff"}) {
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
    largerFont.setPointSize(15);
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
  void teamStartupNavigationPollingAndDraft() {
    ObserverFixture fixture;
    auto record = fixture.record();
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", fixture.project}});
    auto runtimes = record.value("runtimes").toArray();
    auto bound = runtimes.first().toObject();
    bound.insert("disposition", "bound");
    runtimes[0] = bound;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    QVERIFY(workspace);
    QCOMPARE(workspace->route(), QString("team"));
    QVERIFY(item(window, "teamOverview")->isVisible());
    QVERIFY(!item(window, "createProjectButton")->isVisible());
    QVERIFY(panel.project().isEmpty());
    client.reload();
    QTRY_VERIFY(client.online());
    auto *observer = panel.observers();
    QTRY_VERIFY(observer->online());
    QCOMPARE(workspace->team()->total(), 1);
    QCOMPARE(workspace->team()->unbound(), 0);
    QCOMPARE(observer->rows().size(), 2); // Candidate is not a team member.
    const auto member = "teamMember_" + fixture.observer;
    QTRY_VERIFY(item(window, member));
    item(window, member)->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Space);
    QCOMPARE(workspace->selectedObserver().value("observer_id").toString(),
             fixture.observer);
    QVERIFY(panel.project().isEmpty());
    QTest::qWait(50); // Polish the newly populated scrollable inspector.
    QVERIFY(click(window, "teamOpenProjectButton"));
    QCOMPARE(workspace->route(), QString("workspace"));
    QCOMPARE(panel.project().value("id").toString(), fixture.project);
    QVERIFY(!observer->active());
    const int reads = fixture.reads;
    QTest::qWait(2100);
    QCOMPARE(fixture.reads, reads);
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(click(window, "editTaskButton"));
    QVERIFY(
        enter(window, "nameField", "Retain this draft across team navigation"));
    const auto draft = client.draft();
    QVERIFY(click(window, "teamNavButton"));
    QTRY_VERIFY(observer->active());
    QTRY_VERIFY(observer->online());
    QVERIFY(workspace->draftHidden());
    QVERIFY(item(window, "returnToEditorButton")->isVisible());
    QTest::keyClick(window, Qt::Key_S, Qt::ControlModifier);
    QTest::keyClick(window, Qt::Key_Escape);
    QCOMPARE(client.draft(), draft);
    QVERIFY(!client.pending());
    QVERIFY(!window->findChild<QObject *>("discardDialog")
                 ->property("visible")
                 .toBool());
    QVERIFY(click(window, member));
    QVERIFY(click(window, "teamOpenProjectButton")); // Existing manual guard.
    QCOMPARE(workspace->route(), QString("team"));
    QCOMPARE(client.draft(), draft);
    auto *filter = item(window, "teamFilterBar");
    QVERIFY(filter);
    QQuickItem *search = nullptr;
    for (auto *child : filter->childItems())
      if (child->inherits("QQuickTextField"))
        search = child;
    QVERIFY(search);
    search->forceActiveFocus();
    QInputMethodEvent input;
    input.setCommitString("no-match-fictional");
    QCoreApplication::sendEvent(search, &input);
    QTRY_COMPARE(workspace->team()->shown(), 0);
    QVERIFY(item(window, "teamEmptyState")->isVisible());
    QCOMPARE(item(window, "teamEmptyState")->property("title").toString(),
             QString("No matching observations"));
    QVERIFY(workspace->selectedObserver().isEmpty());
    QCOMPARE(panel.task().value("id").toString(), fixture.task);
    QCOMPARE(client.draft(), draft);
    QTest::keyClick(window, Qt::Key_A, Qt::ControlModifier);
    QTest::keyClick(window, Qt::Key_Backspace);
    QTRY_COMPARE(workspace->team()->shown(), 1);
    item(window, "teamNavButton")->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Tab);
    QVERIFY(item(window, "projectsNavButton")->hasActiveFocus());
    window->setVisibility(QWindow::Minimized);
    QTRY_VERIFY(!observer->active());
    QVERIFY(!workspace->team()->visible());
    window->showNormal();
    QTRY_VERIFY(observer->active());
    QTRY_VERIFY(observer->online());
    window->hide();
    QTRY_VERIFY(!observer->active());
    window->show();
    QTRY_VERIFY(observer->active());
    QTRY_VERIFY(observer->online());
    QVERIFY(click(window, "returnToEditorButton"));
    QCOMPARE(workspace->route(), QString("workspace"));
    QCOMPARE(client.draft(), draft);
    QVERIFY(item(window, "nameField")->isVisible());
    QVERIFY(!observer->active());
    QVERIFY(click(window, "observersButton"));
    QTRY_VERIFY(workspace->connectionsOpen());
    QTRY_VERIFY(observer->active());
    auto *drawer = window->findChild<QObject *>("observerDrawer");
    QVERIFY(drawer);
    QVERIFY(QMetaObject::invokeMethod(drawer, "close"));
    QTRY_VERIFY(!workspace->connectionsOpen());
    QTRY_VERIFY(!observer->active());
    QCOMPARE(client.draft(), draft);
    QVERIFY(fixture.mutations.isEmpty());
  }
  void teamEmptyLoadingOfflineRecovery() {
    ObserverFixture fixture;
    fixture.registry.insert("observers", QJsonArray{});
    const auto capture = qEnvironmentVariable("ARTEL_STATE_SCREENSHOT");
    QPointer<QTcpSocket> delayed;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/observers ")) {
        ++fixture.reads;
        delayed = socket;
      } else
        fixture.normal(socket, request);
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(delayed);
    QVERIFY(item(window, "teamConnectionStatus")
                ->property("text")
                .toString()
                .contains("Loading"));
    QVERIFY(!item(window, "teamEmptyState")->isVisible());
    if (!capture.isEmpty()) {
      QTest::qWait(100);
      QVERIFY(window->grabWindow().save(capture + "-loading.png"));
    }
    UiFixture::respond(delayed, 503, "{}");
    QTRY_VERIFY(!panel.observers()->loading());
    QVERIFY(!panel.observers()->projectionLoaded());
    QCOMPARE(item(window, "teamEmptyState")->property("title").toString(),
             QString("Observation registry unavailable"));
    delayed.clear();
    panel.observers()->reload();
    QTRY_VERIFY(delayed);
    UiFixture::respond(delayed, 200, fixture.bytes());
    QTRY_VERIFY(panel.observers()->online());
    QVERIFY(item(window, "teamEmptyState")->isVisible());
    QCOMPARE(item(window, "teamEmptyState")->property("title").toString(),
             QString("No connected team yet"));
    QCOMPARE(panel.observers()->projection().size(), 0);
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    QCOMPARE(workspace->team()->total(), 0);
    QCOMPARE(workspace->team()->shown(), 0);
    if (!capture.isEmpty()) {
      QTest::qWait(100);
      QVERIFY(window->grabWindow().save(capture + "-empty.png"));
    }
    QVERIFY(panel.project().isEmpty());
    delayed.clear();
    panel.observers()->reload();
    QTRY_VERIFY(delayed);
    QCOMPARE(item(window, "teamEmptyState")->property("title").toString(),
             QString("No connected team yet"));
    QVERIFY(item(window, "teamEmptyState")->isVisible());
    UiFixture::respond(delayed, 503, "{}");
    QTRY_VERIFY(!panel.observers()->online());
    QVERIFY(workspace->team()->cached());
    QVERIFY(item(window, "teamConnectionStatus")
                ->property("text")
                .toString()
                .contains("unknown"));
    if (!capture.isEmpty()) {
      QTest::qWait(100);
      QVERIFY(window->grabWindow().save(capture + "-offline.png"));
    }
    delayed.clear();
    panel.observers()->reload();
    QTRY_VERIFY(delayed);
    UiFixture::respond(delayed, 200, fixture.bytes());
    QTRY_VERIFY(panel.observers()->online());
    QVERIFY(!workspace->team()->cached());
    QVERIFY(click(window, "projectsNavButton"));
    QVERIFY(item(window, "createProjectButton")->isVisible());
    QVERIFY(!panel.observers()->active());
    QVERIFY(fixture.mutations.isEmpty());
  }
  void responsiveNativeMatrix_data() {
    QTest::addColumn<QSize>("size");
    QTest::addColumn<QString>("language");
    QTest::addColumn<int>("fontSize");
    for (const auto size :
         {QSize(1000, 680), QSize(1280, 820), QSize(1440, 900)})
      for (const auto &language : {QString("en"), QString("ru")})
        for (const int fontSize : {14, 21, 28, 32})
          QTest::newRow(qPrintable(QString("%1-%2-font%3")
                                       .arg(size.width())
                                       .arg(language)
                                       .arg(fontSize)))
              << size << language << fontSize;
  }
  void responsiveNativeMatrix() {
    QFETCH(QSize, size);
    QFETCH(QString, language);
    QFETCH(int, fontSize);
    ObserverFixture fixture;
    auto prototype = fixture.record();
    prototype.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                            {"project_id", fixture.project}});
    auto runtimes = prototype.value("runtimes").toArray();
    auto bound = runtimes.first().toObject();
    bound.insert("disposition", "bound");
    bound.insert("last_seen",
                 QDateTime::currentDateTimeUtc().toString(Qt::ISODate));
    runtimes[0] = bound;
    // A full candidate set still represents only one team member.
    while (runtimes.size() < 8) {
      auto candidate = runtimes.last().toObject();
      auto registration = candidate.value("registration").toObject();
      registration.insert("runtime_id",
                          QString("%1-7777-4777-8777-777777777777")
                              .arg(runtimes.size(), 8, 10, QChar('0')));
      candidate.insert("registration", registration);
      runtimes.append(candidate);
    }
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    auto project = snapshot.value("projects").toArray().first().toObject();
    project.insert("name",
                   QString::fromUtf8("Очень длинный проект 😀 / ").repeated(5));
    project.insert("root_path",
                   "/fictional/" + QString("long-path/").repeated(30));
    project.insert("description",
                   QString::fromUtf8("Описание проекта / ").repeated(25));
    QJsonArray projects{project};
    for (int i = 0; i < 40; ++i) {
      auto other = project;
      other.insert("id", QString("%1-2222-4222-8222-222222222222")
                             .arg(i + 20, 8, 10, QChar('0')));
      projects.append(other);
    }
    snapshot.insert("projects", projects);
    fixture.state = QJsonDocument(snapshot).toJson();
    prototype.insert("runtimes", runtimes);
    prototype.insert("label",
                     QString::fromUtf8("Длинное имя 😀 / ").repeated(7));
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    writeFile(fixture.directory.filePath("language"), language.toUtf8());
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QCoreApplication::installTranslator(&appearance);
    const auto translatorGuard =
        qScopeGuard([&] { QCoreApplication::removeTranslator(&appearance); });
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    window->resize(size);
    QFont font;
    font.setPixelSize(fontSize);
    for (const auto *name : {"appTheme", "manualTheme"}) {
      auto *theme = window->findChild<QObject *>(name);
      QVERIFY(theme);
      QVERIFY(theme->setProperty("systemFont", font));
    }
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    client.reload();
    QTRY_VERIFY(panel.writable());
    auto fits = [&](const char *name) {
      auto *control = item(window, name);
      if (!control || !control->isVisible() || control->width() <= 0 ||
          control->height() <= 0)
        return false;
      const auto rect = control->mapRectToScene(control->boundingRect());
      for (auto *parent = control->parentItem(); parent;
           parent = parent->parentItem()) {
        if (!parent->clip())
          continue;
        const auto clip = parent->mapRectToScene(parent->boundingRect())
                              .adjusted(-1, -1, 1, 1);
        if (!clip.contains(rect))
          return false;
      }
      return rect.left() >= -1 && rect.top() >= -1 &&
             rect.right() <= window->width() + 1 &&
             rect.bottom() <= window->height() + 1;
    };
    auto reveal = [&](const char *scrollName, const char *controlName) {
      auto *scroll = item(window, scrollName);
      auto *control = item(window, controlName);
      if (!scroll || !control)
        return false;
      auto *flick = scroll->property("contentY").isValid()
                        ? scroll
                        : scroll->property("contentItem").value<QQuickItem *>();
      if (!flick)
        return false;
      const auto offset = control->mapToScene(QPointF()).y() -
                          scroll->mapToScene(QPointF()).y();
      const auto *header = flick->property("headerItem").value<QQuickItem *>();
      const double minimum = header ? header->y() : 0.0;
      const auto maximum = std::max(
          minimum, minimum + flick->property("contentHeight").toDouble() -
                       flick->height());
      flick->setProperty(
          "contentY",
          std::clamp(flick->property("contentY").toDouble() + offset - 8,
                     minimum, maximum));
      return true;
    };
    const auto prefix = qEnvironmentVariable("ARTEL_LAYOUT_SCREENSHOT");
    const bool captureRow =
        (size.width() == 1000 && fontSize == 14) ||
        (size.width() == 1280 && fontSize == 28 && language == "ru") ||
        (size.width() == 1440 && fontSize == 21 && language == "en");
    auto capture = [&](const QString &state) {
      if (prefix.isEmpty() || !captureRow)
        return true;
      const auto filename = prefix + "-" +
                            QString::fromLatin1(QTest::currentDataTag()) + "-" +
                            state + ".png";
      return window->grabWindow().save(filename);
    };
    for (const int count : {0, 1, 12, 256}) {
      QJsonArray records;
      for (int i = 0; i < count; ++i) {
        auto record = prototype;
        record.insert("observer_id", QString("%1-4444-4444-8444-444444444444")
                                         .arg(i + 10, 8, 10, QChar('0')));
        records.append(record);
      }
      fixture.registry.insert("observers", records);
      panel.observers()->reload();
      QTRY_VERIFY(panel.observers()->online());
      QTRY_COMPARE(workspace->team()->total(), count);
      QTest::qWait(25);
      for (const auto *name : {"teamNavButton", "projectsNavButton",
                               "connectionsNavButton", "teamConnectionsButton",
                               "teamList", "teamFooter", "teamActivityFilter"})
        QVERIFY2(fits(name), name);
      workspace->team()->setSearch("absent-result");
      QCOMPARE(workspace->team()->shown(), 0);
      QTRY_VERIFY(item(window, "teamEmptyState")->isVisible());
      workspace->team()->setSearch("");
      if (count == 256) {
        // Virtualization: no subtree for every runtime/observer, even with 8
        // each.
        QVERIFY(item(window, "teamList")
                    ->childItems()
                    .first()
                    ->childItems()
                    .size() < 80);
        QVERIFY(capture("team"));
      }
    }
    QVERIFY(click(window, "teamMember_00000010-4444-4444-8444-444444444444"));
    QTest::qWait(30);
    const bool compact =
        item(window, "teamOverview")->property("compact").toBool();
    if (compact) {
      QVERIFY(!item(window, "teamList")->isVisible());
      QVERIFY(fits("teamBackButton"));
      auto *backLabel = item(window, "teamBackButton")
                            ->property("contentItem")
                            .value<QQuickItem *>();
      QVERIFY(backLabel);
      QCOMPARE(backLabel->property("lineCount").toInt(), 1);
      item(window, "teamBackButton")->forceActiveFocus(Qt::TabFocusReason);
      QTest::keyClick(window, Qt::Key_Space);
      QTRY_VERIFY(item(window, "teamList")->isVisible());
      QVERIFY(click(window, "teamMember_00000010-4444-4444-8444-444444444444"));
    }
    QVERIFY(reveal("teamInspectorScroll", "teamOpenProjectButton"));
    QTest::qWait(30);
    QVERIFY(fits("teamOpenProjectButton"));
    QVERIFY(capture("inspector"));
    // Light/dark private accent inputs preserve selected and focus contrast.
    auto *theme = window->findChild<QObject *>("appTheme");
    for (const auto &accent : {QString("#ffffff"), QString("#000000")}) {
      theme->setProperty("accentInput", accent);
      auto *button = item(window, "teamNavButton");
      button->forceActiveFocus(Qt::TabFocusReason);
      QVERIFY(button->hasActiveFocus());
      auto *accessible = QAccessible::queryAccessibleInterface(button);
      QVERIFY(accessible);
      QVERIFY(!accessible->text(QAccessible::Name).isEmpty());
      const QColor foreground = button->property("foreground").value<QColor>();
      const QColor surface = button->property("surface").value<QColor>();
      auto luminance = [](QColor c) {
        auto linear = [](double v) {
          return v <= 0.04045 ? v / 12.92 : std::pow((v + 0.055) / 1.055, 2.4);
        };
        return .2126 * linear(c.redF()) + .7152 * linear(c.greenF()) +
               .0722 * linear(c.blueF());
      };
      const auto a = luminance(foreground), b = luminance(surface);
      QVERIFY((std::max(a, b) + .05) / (std::min(a, b) + .05) >= 4.5);
      QCOMPARE(button->property("focusColor").value<QColor>(), foreground);
    }
    theme->setProperty("accentInput", "#a8c7fa");
    // Cached fallback stays explicit and does not collapse the full roster.
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/observers "))
        UiFixture::respond(socket, 503, "{}");
      else
        fixture.normal(socket, request);
    };
    panel.observers()->reload();
    QTRY_VERIFY(!panel.observers()->online());
    QVERIFY(workspace->team()->cached());
    QCOMPARE(workspace->team()->total(), 256);
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      fixture.observation(socket, request);
    };
    panel.observers()->reload();
    QTRY_VERIFY(panel.observers()->online());
    workspace->showProjects();
    QTest::qWait(30);
    QVERIFY(fits("createProjectButton"));
    QVERIFY(capture("projects"));
    QVERIFY(workspace->openProject(fixture.project));
    workspace->showManualList();
    QTest::qWait(30);
    QVERIFY(reveal("taskList", "createTaskButton"));
    QTest::qWait(30);
    QVERIFY(fits("createTaskButton"));
    QVERIFY(capture("tasks"));
    QVERIFY(panel.selectTask(fixture.task));
    QTest::qWait(30);
    QVERIFY(reveal("detailsScroll", "editTaskButton"));
    QTest::qWait(30);
    QVERIFY(fits("editTaskButton"));
    QVERIFY(capture("workspace"));
    if (compact) {
      QVERIFY(reveal("detailsScroll", "manualBackButton"));
      QTest::qWait(20);
      QVERIFY(click(window, "manualBackButton"));
      QTest::qWait(20);
      QVERIFY(fits("taskList"));
      QCOMPARE(panel.task().value("id").toString(), fixture.task);
      QVERIFY(click(window, "task_" + fixture.task));
      QTRY_VERIFY(item(window, "manualBackButton")->isVisible());
    }
    panel.editTask(false);
    QTest::qWait(30);
    QVERIFY(enter(window, "descriptionField",
                  QString::fromUtf8("Long plain text <b>Кириллица 😀</b>\n")
                      .repeated(40)));
    QVERIFY(enter(window, "criteriaField",
                  QString::fromUtf8("Критерий 😀 / ").repeated(120)));
    QTest::qWait(30);
    const auto draft = panel.editor();
    QVERIFY(reveal("detailsScroll", "saveButton"));
    QTest::qWait(30);
    QVERIFY(fits("saveButton"));
    QVERIFY(fits("discardButton"));
    // Tab/Backtab must reveal the focused field/action without mouse scrolling.
    item(window, "saveButton")->forceActiveFocus(Qt::TabFocusReason);
    QTest::keyClick(window, Qt::Key_Tab, Qt::ShiftModifier);
    QTRY_VERIFY(item(window, "criteriaField")->hasActiveFocus());
    QTest::keyClick(window, Qt::Key_Tab);
    QTRY_VERIFY(item(window, "saveButton")->hasActiveFocus());
    QVERIFY(fits("saveButton"));
    QVERIFY(capture("editor"));
    QVERIFY(click(window, "teamNavButton"));
    QTest::keyClick(window, Qt::Key_S, Qt::ControlModifier);
    QTest::keyClick(window, Qt::Key_Escape);
    QCOMPARE(panel.editor(), draft);
    QVERIFY(click(window, "returnToEditorButton"));
    QCOMPARE(panel.editor(), draft);
    // Connections remain independent of the long dirty editor.
    QVERIFY(click(window, "connectionsNavButton"));
    QTRY_VERIFY(panel.observers()->online());
    QTest::qWait(300);
    QVERIFY(capture("connections"));
    // Keyboard focus reveals management actions below long metadata/header.
    const auto pruneName = ("observerPrune_" + fixture.runtime).toUtf8();
    auto *prune = item(window, QString::fromUtf8(pruneName));
    QVERIFY(prune);
    prune->forceActiveFocus(Qt::TabFocusReason);
    QTest::qWait(30);
    QVERIFY(fits(pruneName.constData()));
    const auto pruneRect = prune->mapRectToScene(prune->boundingRect());
    auto *management = item(window, "observerPanel");
    const auto panelRect =
        management->mapRectToScene(management->boundingRect());
    QVERIFY(pruneRect.left() >= panelRect.left());
    QVERIFY(pruneRect.right() <= panelRect.right());
    QCOMPARE(panel.editor(), draft);
    QVERIFY(fixture.mutations.isEmpty());
    client.stop();
    // Focus/scroll can schedule virtualized delegates just before teardown.
    // Stop polling and finish incubation before destroying the QML engine;
    // failOnWarning must still catch genuine lifetime/resource regressions.
    workspace->setWindowVisible(false);
    auto *incubator = engine.incubationController();
    QVERIFY(incubator);
    incubator->incubateFor(1000);
    QTRY_COMPARE_WITH_TIMEOUT(incubator->incubatingObjectCount(), 0, 3000);
  }
  void teamOverviewScreenshots() {
    ObserverFixture fixture;
    QJsonArray records;
    const QStringList labels{"Interface developer", "Продакт · планирование",
                             "Backend reviewer", "Documentation",
                             "Build checks"};
    const QStringList activities{"working", "waiting_user", "idle", "working",
                                 "idle"};
    for (int i = 0; i < labels.size(); ++i) {
      auto record = fixture.record();
      record.insert("observer_id", QString("%1-4444-4444-8444-444444444444")
                                       .arg(i + 10, 8, 10, QChar('0')));
      record.insert("label", labels[i]);
      record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                           {"project_id", fixture.project}});
      auto runtimes = record.value("runtimes").toArray();
      auto bound = runtimes.first().toObject();
      bound.insert("disposition", "bound");
      bound.insert("activity", activities[i]);
      bound.insert("freshness", i == 3 ? "stale" : "fresh");
      bound.insert("last_seen",
                   QDateTime::currentDateTimeUtc().addSecs(-i * 120).toString(
                       Qt::ISODate));
      runtimes[0] = bound;
      record.insert("runtimes", runtimes);
      records.append(record);
    }
    fixture.registry.insert("observers", records);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QCoreApplication::installTranslator(&appearance);
    QQmlApplicationEngine engine;
    QObject::connect(&appearance, &Appearance::changed, &engine,
                     &QQmlEngine::retranslate);
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.observers()->online());
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    QCOMPARE(workspace->team()->total(), 5);
    const auto prefix = qEnvironmentVariable("ARTEL_TEAM_SCREENSHOT");
    for (const auto &language : {QString("en"), QString("ru")}) {
      writeFile(fixture.directory.filePath("language"), language.toUtf8());
      appearance.refresh();
      emit panel.changed();
      for (const auto &size : {QSize(1280, 820), QSize(1440, 900)}) {
        window->resize(size);
        QTRY_VERIFY(
            item(window, "teamMember_00000010-4444-4444-8444-444444444444"));
        QVERIFY(
            click(window, "teamMember_00000010-4444-4444-8444-444444444444"));
        QTest::qWait(80);
        auto *member =
            item(window, "teamMember_00000010-4444-4444-8444-444444444444");
        QCOMPARE(
            member->property("record").toMap().value("status_text").toString(),
            language == "ru" ? QString::fromUtf8("Работает")
                             : QString("Working"));
        for (const auto *name :
             {"teamCounts", "teamFooter", "teamOpenProjectButton",
              "teamConnectionsButton", "teamActivityFilter"}) {
          auto *control = item(window, name);
          QVERIFY(control && control->isVisible());
          const auto rect = control->mapRectToScene(control->boundingRect());
          QVERIFY(rect.left() >= 0 && rect.right() <= window->width());
          QVERIFY(rect.top() >= 0 && rect.bottom() <= window->height());
        }
        auto *metadata = item(window, "teamObserverMetadata");
        QVERIFY(metadata && metadata->isVisible());
        QVERIFY(metadata->property("readOnly").toBool());
        QCOMPARE(metadata->property("textFormat").toInt(), int(Qt::PlainText));
        QVERIFY(
            metadata->property("text").toString().contains("fictional-model"));
        QVERIFY(!item(window, "teamOpenSessionButton")->isEnabled());
        if (!prefix.isEmpty())
          QVERIFY(window->grabWindow().save(prefix + "-" + language + "-" +
                                            QString::number(size.width()) +
                                            ".png"));
      }
    }
    QVERIFY(fixture.mutations.isEmpty());
    QCoreApplication::removeTranslator(&appearance);
  }
  void russianCatalogCoversVisibleQmlStrings() {
    QFile catalog(":/i18n/ru.json");
    QVERIFY(catalog.open(QIODevice::ReadOnly));
    const auto strings = QJsonDocument::fromJson(catalog.readAll()).object();
    QVERIFY(!strings.isEmpty());
    const QRegularExpression expression("qsTr\\(\"([^\"]*)\"");
    for (const auto *path :
         {":/Main.qml", ":/ManualWorkspace.qml", ":/TeamOverview.qml"}) {
      QFile qml(path);
      QVERIFY(qml.open(QIODevice::ReadOnly));
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
  }
  void teamCrossProjectNavigationIgnoresManagementFilters() {
    ObserverFixture fixture;
    AgtermFixture terminal;
    const QString projectB = "77777777-7777-4777-8777-777777777777";
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    auto projects = snapshot.value("projects").toArray();
    auto second = projects.first().toObject();
    second.insert("id", projectB);
    second.insert("name", "Project B");
    projects.append(second);
    snapshot.insert("projects", projects);
    fixture.state = QJsonDocument(snapshot).toJson();
    auto record = fixture.record();
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", projectB}});
    auto runtimes = record.value("runtimes").toArray();
    auto runtime = runtimes.first().toObject();
    runtime.insert("disposition", "bound");
    auto registration = runtime.value("registration").toObject();
    registration.insert("terminal_hint",
                        QJsonObject::fromVariantMap(terminal.hint()));
    runtime.insert("registration", registration);
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
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(panel.editTask());
    panel.field("title", "Cross-project navigation keeps draft A");
    const auto draft = client.draft();
    auto *o = panel.observers();
    o->setOnlyProject(true);
    o->setViewFilter("history");
    QTRY_VERIFY(o->online());
    QVERIFY(o->rows().isEmpty());
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    QTRY_COMPARE(workspace->team()->total(), 1);
    QVERIFY(click(window, "teamMember_" + fixture.observer));
    QVERIFY(item(window, "teamOpenSessionButton")->isEnabled());
    QPointer<QTcpSocket> pendingRevoke;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST /v1/observers/") &&
          request.contains("/revoke "))
        pendingRevoke = socket;
      else
        fixture.observation(socket, request);
    };
    QVERIFY(o->prepare("revoke", fixture.observer, fixture.runtime));
    QVERIFY(o->confirm());
    QTRY_VERIFY(pendingRevoke);
    QVERIFY(!item(window, "teamOpenSessionButton")->isEnabled());
    QVERIFY(
        !workspace->openObserverSession(fixture.observer, fixture.runtime, 1));
    QVERIFY(terminal.calls.isEmpty());
    UiFixture::respond(pendingRevoke, 409, "{}");
    QTRY_VERIFY(item(window, "teamOpenSessionButton")->isEnabled());
    QTest::qWait(50);
    QVERIFY(click(window, "teamOpenSessionButton"));
    QTRY_VERIFY(!terminal.calls.isEmpty());
    QCOMPARE(terminal.calls.last().at(1), QString("select"));
    QCOMPARE(panel.project().value("id").toString(), fixture.project);
    QCOMPARE(client.draft(), draft);
    // A filtered-out selection must never act on the next visible row.
    const auto calls = terminal.calls.size();
    workspace->team()->setSearch("no match");
    QVERIFY(workspace->selectedObserver().isEmpty());
    QVERIFY(
        !workspace->openObserverSession(fixture.observer, fixture.runtime, 1));
    workspace->team()->setSearch("");
    QVERIFY(workspace->selectObserver(fixture.observer, fixture.runtime));
    runtime.insert("freshness", "stale");
    runtimes[0] = runtime;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    o->reload();
    QTRY_VERIFY(!item(window, "teamOpenSessionButton")->isEnabled());
    QCOMPARE(item(window, "teamObserverFreshness")->property("text").toString(),
             QString("Freshness: Stale"));
    QVERIFY(
        !workspace->openObserverSession(fixture.observer, fixture.runtime, 1));
    o->openSession(fixture.observer, fixture.runtime);
    QCOMPARE(terminal.calls.size(), calls);
    record.insert("revoked", true);
    fixture.record(record);
    o->reload();
    QTRY_COMPARE(workspace->team()->total(), 0);
    QVERIFY(workspace->selectedObserver().isEmpty());
    o->openSession(fixture.observer, fixture.runtime);
    QCOMPARE(terminal.calls.size(), calls);
    QCOMPARE(client.draft(), draft);
    QCOMPARE(fixture.mutations.size(), 1);
    QVERIFY(fixture.mutations.first().contains("/revoke "));
  }
  void observerPendingNavigation_data() {
    QTest::addColumn<QString>("change");
    for (const auto *name :
         {"revoked", "binding", "version", "generation", "runtime", "hint",
          "read-failure", "database", "owner-database", "stale", "expiry",
          "heartbeat", "after-dispatch"})
      QTest::newRow(name) << QString(name);
  }
  void observerPendingNavigation() {
    QFETCH(QString, change);
    ObserverFixture fixture;
    AgtermFixture terminal;
    const auto projectB = fixture.bindToOtherProject();
    auto record = fixture.record();
    auto runtimes = record.value("runtimes").toArray();
    auto runtime = runtimes.first().toObject();
    auto registration = runtime.value("registration").toObject();
    auto hint = QJsonObject::fromVariantMap(terminal.hint());
    registration.insert("terminal_hint", hint);
    runtime.insert("registration", registration);
    runtimes[0] = runtime;
    record.insert("runtimes", runtimes);
    fixture.record(record);

    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    ObserverClient observer(&client);
    observer.setProject({{"id", fixture.project}, {"name", "Project A"}});
    observer.setTimeout(10000);
    observer.setActive(true);
    client.reload();
    QTRY_VERIFY(observer.cacheCurrent());
    auto *nav = observer.navigation();
    nav->setSocket("/fictional/owner.sock");
    AgtermAdapter::Done releaseTree, replyToSelect;
    QByteArray finalTree;
    int selections = 0;
    nav->setRunner([&](const QStringList &args, AgtermAdapter::Done done) {
      const auto reply = terminal.reply(args);
      if (args.first() == "tree" && terminal.lists == 2 &&
          args.contains(terminal.otherWindow)) {
        finalTree = reply;
        releaseTree = std::move(done);
      } else if (args.first() == "session" && args.value(1) == "select" &&
                 args.contains("--target")) {
        ++selections; // Do not count the select --help capability probe.
        QCOMPARE(args.at(args.indexOf("--target") + 1), terminal.session);
        QCOMPARE(args.at(args.indexOf("--window") + 1), terminal.otherWindow);
        if (change == "after-dispatch")
          replyToSelect = std::move(done);
        else
          done(true, reply);
      } else {
        done(true, reply);
      }
    });
    qint64 cacheAge = 0;
    if (change == "expiry")
      observer.setCacheAgeSource([&cacheAge] { return cacheAge; });
    QVERIFY(observer.cacheCurrent());
    const auto row = observer.rows().first().toMap();
    QCOMPARE(row.value("binding_project_id").toString(), projectB);
    QVERIFY(!row.value("selected_project").toBool());
    observer.openSession(fixture.observer, fixture.runtime);
    QVERIFY(nav->busy());
    QVERIFY(releaseTree);
    QVERIFY(!finalTree.isEmpty());
    QCOMPARE(selections, 0);
    if (change == "after-dispatch") {
      releaseTree(true, finalTree);
      QCOMPARE(selections, 1);
      QVERIFY(replyToSelect);
      QVERIFY(nav->busy());
    }

    const QString replacement = "88888888-8888-4888-8888-888888888888";
    if (change == "revoked" || change == "after-dispatch")
      record.insert("revoked", true);
    else if (change == "binding")
      record.insert("binding", QJsonObject{{"runtime_id", fixture.otherRuntime},
                                           {"project_id", projectB}});
    else if (change == "version" || change == "generation")
      record.insert(change, 2);
    else if (change == "runtime") {
      registration.insert("runtime_id", replacement);
      runtime.insert("registration", registration);
    } else if (change == "hint") {
      hint.insert("pane_id", replacement);
      registration.insert("terminal_hint", hint);
      runtime.insert("registration", registration);
    } else if (change == "stale")
      runtime.insert("freshness", "stale");
    else if (change == "heartbeat") {
      runtime.insert("last_seen", "2026-11-17T12:00:01Z");
      runtime.insert("activity", "idle");
      observer.setViewFilter("history");
      observer.setOnlyProject(true);
      QVERIFY(observer.rows().isEmpty());
      QVERIFY(nav->busy());
    }
    runtimes[0] = runtime;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    if (change == "read-failure") {
      fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
        if (request.startsWith("GET /v1/observers "))
          fixture.respond(socket, 500, "{}");
        else
          fixture.normal(socket, request);
      };
    } else if (change == "database" || change == "owner-database") {
      fixture.registry.insert("database_instance_id", replacement);
      if (change == "owner-database") {
        auto state = QJsonDocument::fromJson(fixture.state).object();
        state.insert("instance_id", replacement);
        fixture.state = QJsonDocument(state).toJson();
        client.reload();
        QTRY_COMPARE(client.instance(), replacement);
        QTRY_VERIFY(observer.cacheCurrent());
      }
    }
    if (change == "expiry") {
      cacheAge = 4000;
      QVERIFY(!observer.cacheCurrent());
      QVERIFY(observer.online());
      QVERIFY(nav->busy()); // The two-second adapter deadline hasn't fired.
    } else if (change != "owner-database") {
      observer.reload();
      QTRY_VERIFY(!observer.loading());
      QCOMPARE(observer.online(),
               change != "read-failure" && change != "database");
    }

    if (change == "after-dispatch") {
      QVERIFY(nav->busy());
      QVERIFY(nav->message().isEmpty());
      replyToSelect(true, AgtermFixture::response({{"id", terminal.session}}));
    } else {
      const bool cancelledEarly = !nav->busy();
      releaseTree(true, finalTree);
      QCOMPARE(selections, change == "heartbeat" ? 1 : 0);
      QCOMPARE(cancelledEarly, change != "heartbeat" && change != "expiry");
    }
    QVERIFY(!nav->busy());
    if (change == "heartbeat" || change == "after-dispatch")
      QVERIFY(nav->message().contains("exact pane focus"));
    else
      QCOMPARE(nav->message(),
               QString("Observation changed or expired during "
                       "agterm lookup. Refresh and try again."));
    QVERIFY(fixture.mutations.isEmpty());
  }
  void teamInspectorUnavailableProjectsAndMalformedProjection() {
    ObserverFixture fixture;
    auto record = fixture.record();
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", fixture.project}});
    auto runtime = record.value("runtimes").toArray().first().toObject();
    runtime.insert("disposition", "bound");
    record.insert("runtimes", QJsonArray{runtime});
    fixture.record(record);
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    auto project = snapshot.value("projects").toArray().first().toObject();
    project.insert("archived", true);
    snapshot.insert("projects", QJsonArray{project});
    fixture.state = QJsonDocument(snapshot).toJson();
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.observers()->online());
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    QVERIFY(workspace->selectObserver(fixture.observer, fixture.runtime));
    QTest::qWait(50);
    QVERIFY(item(window, "teamProjectAvailability")->isVisible());
    QVERIFY(item(window, "teamProjectAvailability")
                ->property("text")
                .toString()
                .contains("Archived"));
    QVERIFY(!item(window, "teamOpenProjectButton")->isEnabled());
    QVERIFY(panel.projects().isEmpty());
    auto *filter = item(window, "teamProjectFilter");
    QTRY_COMPARE(filter->property("count").toInt(), 2);
    workspace->team()->setProjectId(fixture.project);
    QTRY_COMPARE(filter->property("currentValue").toString(), fixture.project);
    snapshot.insert("projects", QJsonArray{});
    fixture.state = QJsonDocument(snapshot).toJson();
    client.reload();
    QTRY_VERIFY(
        !workspace->selectedObserver().value("project_archived").toBool());
    QTRY_VERIFY(workspace->team()->projectId().isEmpty());
    QCOMPARE(filter->property("currentIndex").toInt(), 0);
    QVERIFY(workspace->selectedObserver()
                .value("project_name")
                .toString()
                .contains(fixture.project));
    QVERIFY(item(window, "teamProjectAvailability")
                ->property("text")
                .toString()
                .contains("unavailable"));
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/observers "))
        UiFixture::respond(socket, 200, "{malformed");
      else
        fixture.observation(socket, request);
    };
    panel.observers()->reload();
    QTRY_VERIFY(workspace->team()->cached());
    QCOMPARE(workspace->selectedObserver().value("status").toString(),
             QString("unknown"));
    QVERIFY(!item(window, "teamOpenSessionButton")->isEnabled());
    QVERIFY(!item(window, "teamOpenProjectButton")->isEnabled());
    QVERIFY(fixture.mutations.isEmpty());
  }
  void projectsRouteKeepsVisibleEditorShortcuts() {
    ObserverFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(client.online());
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    QVERIFY(workspace->openProject(fixture.project));
    QVERIFY(workspace->openTask(fixture.task));
    QVERIFY(panel.editTask());
    QVERIFY(click(window, "projectsNavButton"));
    QCOMPARE(workspace->route(), QString("projects"));
    QVERIFY(!workspace->draftHidden());
    QTRY_VERIFY(item(window, "nameField")->isVisible());
    QTest::keyClick(window, Qt::Key_Escape);
    auto *dialog = window->findChild<QObject *>("discardDialog");
    QVERIFY(dialog);
    QTRY_VERIFY(dialog->property("opened").toBool());
    QVERIFY(QMetaObject::invokeMethod(dialog, "close"));
    QTRY_VERIFY(!dialog->property("visible").toBool());
    QVERIFY(enter(window, "nameField", "Preserved Projects shortcut"));
    QPointer<QTcpSocket> pending;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("PATCH /v1/tasks/"))
        pending = socket;
      else
        fixture.observation(socket, request);
    };
    QTest::keyClick(window, Qt::Key_S, Qt::ControlModifier);
    QTRY_VERIFY(pending);
    UiFixture::respond(pending, 409, "{}");
    QTRY_VERIFY(!panel.busy());
  }
  void teamProjectClickDoesNotFollowRebind() {
    ObserverFixture fixture;
    const QString projectB = "77777777-7777-4777-8777-777777777777";
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    auto projects = snapshot.value("projects").toArray();
    auto second = projects.first().toObject();
    second.insert("id", projectB);
    second.insert("name", "Project B");
    projects.append(second);
    snapshot.insert("projects", projects);
    fixture.state = QJsonDocument(snapshot).toJson();
    auto record = fixture.record();
    auto bound = record.value("runtimes").toArray().first().toObject();
    bound.insert("disposition", "bound");
    record.insert("runtimes", QJsonArray{bound});
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", fixture.project}});
    fixture.record(record);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.observers()->online());
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    QVERIFY(workspace->selectObserver(fixture.observer, fixture.runtime));
    auto *button = item(window, "teamOpenProjectButton");
    QTRY_VERIFY(button->isEnabled() && button->width() > 0);
    QTest::qWait(50);
    const auto point =
        button->mapToScene({button->width() / 2, button->height() / 2})
            .toPoint();
    QTest::mousePress(window, Qt::LeftButton, {}, point);
    QVERIFY(button->property("pressed").toBool());
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", projectB}});
    record.insert("version", 2);
    fixture.record(record);
    panel.observers()->reload();
    QTRY_COMPARE(workspace->selectedObserver().value("project_id").toString(),
                 projectB);
    QTest::mouseRelease(window, Qt::LeftButton, {}, point);
    QCOMPARE(workspace->route(), QString("team"));
    QVERIFY(panel.project().isEmpty());
    QVERIFY(click(window, "teamOpenProjectButton"));
    QTRY_COMPARE(panel.project().value("id").toString(), projectB);
  }
  void connectionMetadataSurvivesHeartbeat() {
    ObserverFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    auto *o = panel.observers();
    QTRY_VERIFY(o->online());
    QVERIFY(click(window, "teamConnectionsButton"));
    auto *drawer = window->findChild<QObject *>("observerDrawer");
    QVERIFY(drawer);
    QTRY_VERIFY(drawer->property("opened").toBool());
    const auto toggle = "observerMetadataToggle_" + fixture.runtime;
    const auto metadata = "observerMetadata_" + fixture.runtime;
    QTRY_VERIFY(item(window, toggle) && item(window, toggle)->isVisible());
    QTest::qWait(50);
    QVERIFY(click(window, toggle));
    QTRY_VERIFY(item(window, metadata)->isVisible());
    QPointer<QQuickItem> editor = item(window, metadata);
    editor->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_A, Qt::ControlModifier);
    const auto selection = editor->property("selectedText").toString();
    QVERIFY(!selection.isEmpty());
    auto record = fixture.record();
    auto runtimes = record.value("runtimes").toArray();
    auto runtime = runtimes.first().toObject();
    const QString seen = "2026-11-17T13:00:00Z";
    runtime.insert("last_seen", seen);
    runtimes[0] = runtime;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    o->reload();
    QTRY_COMPARE(o->projection()
                     .first()
                     .toMap()
                     .value("runtimes")
                     .toList()
                     .first()
                     .toMap()
                     .value("last_seen")
                     .toString(),
                 seen);
    QVERIFY(editor && editor->isVisible());
    QCOMPARE(editor->property("selectedText").toString(), selection);
  }
  void observerUnbindConfirmationInProductionQml_data() {
    QTest::addColumn<QByteArray>("language");
    QTest::addColumn<QString>("projectLabel");
    QTest::newRow("en") << QByteArray("en") << QString("Bound project ID:");
    QTest::newRow("ru") << QByteArray("ru")
                        << QString("ID привязанного проекта:");
  }
  void observerUnbindConfirmationInProductionQml() {
    QFETCH(QByteArray, language);
    QFETCH(QString, projectLabel);
    ObserverFixture fixture;
    const auto projectB = fixture.bindToOtherProject();
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    const auto languageFile = fixture.directory.filePath("language");
    writeFile(languageFile, language);
    Appearance appearance(fixture.directory.path(), languageFile);
    QCoreApplication::installTranslator(&appearance);
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(panel.editTask());
    panel.field("title", "Keep the unrelated manual draft");
    const auto draft = client.draft();
    auto *o = panel.observers();
    QTRY_VERIFY(o->online());
    QVERIFY(click(window, "teamConnectionsButton"));
    auto *drawer = window->findChild<QObject *>("observerDrawer");
    QVERIFY(drawer);
    QTRY_VERIFY(drawer->property("opened").toBool());
    o->setViewFilter("candidate");
    const auto button = "observerUnbind_" + fixture.otherRuntime;
    QTRY_VERIFY(item(window, button) && item(window, button)->isVisible());
    QVERIFY(click(window, button));
    auto *dialog = window->findChild<QObject *>("observerConfirmation");
    QVERIFY(dialog && dialog->property("visible").toBool());
    auto *label = dialog->findChild<QObject *>("observerConfirmationText");
    QVERIFY(label);
    QCOMPARE(label->property("textFormat").toInt(), int(Qt::PlainText));
    const auto text = label->property("text").toString();
    QVERIFY(text.contains(projectLabel + " " + projectB));
    QVERIFY(text.contains(fixture.runtime));
    QVERIFY(!text.contains(fixture.otherRuntime));
    QVERIFY(!text.contains(fixture.project));
    QVERIFY(!text.contains("Example project"));
    auto record = fixture.record();
    record.insert("version", 2);
    fixture.record(record);
    o->reload();
    QTRY_COMPARE(o->projection().first().toMap().value("version").toInt(), 2);
    QVERIFY(QMetaObject::invokeMethod(dialog, "accept"));
    QVERIFY(!o->busy());
    QVERIFY(fixture.mutations.isEmpty());
    QCOMPARE(fixture.record().value("binding"), record.value("binding"));
    QCOMPARE(client.draft(), draft);
    QCOMPARE(panel.project().value("id").toString(), fixture.project);
  }
  void observerRevokedCannotUnbind() {
    ObserverFixture fixture;
    fixture.bindToOtherProject();
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    auto *o = panel.observers();
    QTRY_VERIFY(o->online());
    QVERIFY(click(window, "teamConnectionsButton"));
    const auto unbind = "observerUnbind_" + fixture.otherRuntime;
    const auto prune = "observerPrune_" + fixture.otherRuntime;
    QTRY_VERIFY(item(window, unbind) && item(window, unbind)->isEnabled());
    QTRY_VERIFY(item(window, prune));
    QCOMPARE(item(window, prune)->property("text").toString(),
             QString("Prune runtime records"));
    QVERIFY(o->prepare("unbind", fixture.observer, fixture.otherRuntime));
    auto record = fixture.record();
    record.insert("revoked", true);
    fixture.record(record);
    o->reload();
    QTRY_VERIFY(!o->loading() && o->online());
    QVERIFY(item(window, unbind));
    QVERIFY(!item(window, unbind)->isEnabled());
    QVERIFY(!o->confirm());
    QVERIFY(!o->prepare("unbind", fixture.observer, fixture.otherRuntime));
    QVERIFY(!o->busy());
    QVERIFY(fixture.mutations.isEmpty());
  }
  void observerUnbindRevalidatesBindingInsteadOfClickedRow() {
    ObserverFixture fixture;
    const auto projectB = fixture.bindToOtherProject();
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    auto *o = panel.observers();
    o->setActive(true);
    QTRY_VERIFY(o->online());
    auto record = fixture.record();
    for (const auto &field :
         {QString("version"), QString("generation"), QString("binding")}) {
      QVERIFY(o->prepare("unbind", fixture.observer, fixture.otherRuntime));
      const auto text = o->confirmation();
      QVERIFY(text.contains(projectB));
      QVERIFY(text.contains(fixture.runtime));
      QVERIFY(!text.contains(fixture.otherRuntime));
      if (field == "binding")
        record.insert(field, QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", fixture.project}});
      else
        record.insert(field, record.value(field).toInt() + 1);
      fixture.record(record);
      o->reload();
      QTRY_VERIFY(!o->loading());
      QVERIFY(!o->confirm());
      QVERIFY(!o->busy());
      QVERIFY(fixture.mutations.isEmpty());
      QCOMPARE(fixture.record().value("binding"), record.value("binding"));
      o->reload();
      QTRY_VERIFY(o->online());
    }
    QVERIFY(o->prepare("unbind", fixture.observer, fixture.otherRuntime));
    const auto text = o->confirmation();
    auto runtimes = record.value("runtimes").toArray();
    runtimes.removeLast();
    record.insert("runtimes", runtimes);
    fixture.record(record);
    QVERIFY(panel.selectProject(projectB));
    o->setOnlyProject(true);
    o->setViewFilter("candidate");
    QTRY_VERIFY(o->online() && !o->loading());
    QVERIFY(o->rows().isEmpty());
    QCOMPARE(o->confirmation(), text);
    QVERIFY(o->confirm());
    QTRY_COMPARE(fixture.mutations.size(), 1);
    const auto request = fixture.mutations.first();
    QVERIFY(request.startsWith("PATCH /v1/observers/" +
                               fixture.observer.toUtf8() + "/binding "));
    QCOMPARE(
        QJsonDocument::fromJson(request.mid(request.indexOf("\r\n\r\n") + 4))
            .object(),
        (QJsonObject{{"expected_version", 2},
                     {"runtime_id", QJsonValue::Null},
                     {"project_id", QJsonValue::Null}}));
    QVERIFY(!fixture.record().contains("binding"));
  }
  void observerConfirmationCapturesBindingAndIdentity() {
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
    QVERIFY(o->prepare("revoke", fixture.observer, fixture.runtime));
    auto record = fixture.record();
    // Even a malformed same-version replacement must invalidate captured
    // intent.
    record.insert("binding", QJsonObject{{"runtime_id", fixture.otherRuntime},
                                         {"project_id", fixture.project}});
    fixture.record(record);
    o->reload();
    QTRY_VERIFY(!o->loading());
    QVERIFY(!o->confirm());
    QVERIFY(fixture.mutations.isEmpty());
    o->reload();
    QTRY_VERIFY(o->online());
    QVERIFY(o->prepare("prune", fixture.observer, fixture.runtime));
    auto runtimes = record.value("runtimes").toArray();
    runtimes.removeFirst();
    record.insert("runtimes", runtimes);
    fixture.record(record);
    o->reload();
    QTRY_VERIFY(!o->loading());
    QVERIFY(!o->confirm());
    QVERIFY(fixture.mutations.isEmpty());
    o->setViewFilter("candidate");
    QCOMPARE(o->rows().size(), 0);
    o->setViewFilter("bound");
    QCOMPARE(o->rows().size(), 1);
    // Category follows current binding; a malformed disposition is still not a
    // second member.
    o->setViewFilter("history");
    QCOMPARE(o->rows().size(), 0);
    o->setViewFilter("invalid");
    QCOMPARE(o->viewFilter(), QString("history"));
    record.remove("binding");
    record.insert("runtimes", QJsonArray{});
    fixture.record(record);
    o->setViewFilter("candidate");
    o->reload();
    QTRY_COMPARE(o->rows().size(), 1);
    const auto registration = o->rows().first().toMap();
    QVERIFY(
        registration.value("category_text").toString().contains("no runtime"));
    QVERIFY(!registration.value("bindable").toBool());
    QVERIFY(!registration.value("navigable").toBool());
    QVERIFY(fixture.mutations.isEmpty());
  }
  void observerInspectorAndManagementScreenshots() {
    ObserverFixture fixture;
    auto record = fixture.record();
    record.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                         {"project_id", fixture.project}});
    auto runtimes = record.value("runtimes").toArray();
    auto runtime = runtimes.first().toObject();
    runtime.insert("disposition", "bound");
    runtimes[0] = runtime;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QCoreApplication::installTranslator(&appearance);
    QQmlApplicationEngine engine;
    QObject::connect(&appearance, &Appearance::changed, &engine,
                     &QQmlEngine::retranslate);
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    auto *o = panel.observers();
    QTRY_VERIFY(o->online());
    QVERIFY(click(window, "teamConnectionsButton"));
    QTRY_VERIFY(window->findChild<QObject *>("observerDrawer")
                    ->property("opened")
                    .toBool());
    const auto prefix = qEnvironmentVariable("ARTEL_CONNECTION_SCREENSHOT");
    const auto capture = [&](const QString &name) {
      QTest::qWait(80);
      auto *refresh = item(window, "observerRefresh");
      QVERIFY(refresh && refresh->isVisible());
      const auto bounds = refresh->mapRectToScene(refresh->boundingRect());
      QVERIFY(bounds.right() <= window->width());
      QVERIFY(bounds.bottom() <= window->height());
      if (!prefix.isEmpty())
        QVERIFY(window->grabWindow().save(prefix + "-" + name + ".png"));
    };
    for (const auto &language : {QString("en"), QString("ru")}) {
      writeFile(fixture.directory.filePath("language"), language.toUtf8());
      appearance.refresh();
      o->setViewFilter("bound");
      QCOMPARE(o->rows().size(), 1);
      capture(language + "-binding");
      if (language == "en") {
        QVERIFY(click(window, "observerMetadataToggle_" + fixture.runtime));
        auto *metadata = item(window, "observerMetadata_" + fixture.runtime);
        QVERIFY(metadata && metadata->isVisible());
        QVERIFY(metadata->property("readOnly").toBool());
        QCOMPARE(metadata->property("textFormat").toInt(), int(Qt::PlainText));
        capture("en-metadata");
        QVERIFY(click(window, "observerMetadataToggle_" + fixture.runtime));
        QTest::qWait(50);
        QVERIFY(click(window, "observerBind_" + fixture.runtime));
        auto *confirmation =
            window->findChild<QObject *>("observerConfirmation");
        QVERIFY(confirmation && confirmation->property("visible").toBool());
        capture("en-confirmation");
        QVERIFY(QMetaObject::invokeMethod(confirmation, "reject"));
        QTRY_VERIFY(!confirmation->property("visible").toBool());
        QTest::qWait(200);
        QVERIFY(o->confirmation().isEmpty());
      }
      o->setViewFilter("candidate");
      QCOMPARE(o->rows().size(), 1);
      capture(language + "-candidate");
      o->setViewFilter("history");
      QVERIFY(o->rows().isEmpty());
      capture(language + "-empty");
    }
    auto noCandidates = record;
    noCandidates.insert("runtimes", QJsonArray{runtime});
    fixture.record(noCandidates);
    o->setViewFilter("candidate");
    o->reload();
    QTRY_VERIFY(!o->loading());
    QVERIFY(o->rows().isEmpty());
    capture("ru-empty-candidates");
    record.insert("revoked", true);
    fixture.record(record);
    o->setViewFilter("history");
    o->reload();
    QTRY_COMPARE(o->rows().size(), 2);
    QTRY_VERIFY(
        !item(window, "observerRevoke_" + fixture.runtime)->isEnabled());
    capture("ru-revoked");
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/observers "))
        UiFixture::respond(socket, 503, "{}");
      else
        fixture.observation(socket, request);
    };
    o->reload();
    QTRY_VERIFY(!o->online());
    QVERIFY(!item(window, "observerPrune_" + fixture.runtime)->isEnabled());
    capture("ru-offline");
    QVERIFY(fixture.mutations.isEmpty());
    QCoreApplication::removeTranslator(&appearance);
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
    auto *drawer = window->findChild<QObject *>("observerDrawer");
    QVERIFY(drawer);
    QTRY_VERIFY(drawer->property("opened").toBool());
    const auto bindName = "observerBind_" + fixture.runtime;
    QTRY_VERIFY(item(window, bindName) && item(window, bindName)->isEnabled());
    QVERIFY(click(window, bindName));
    QTRY_VERIFY(!observer->confirmation().isEmpty());
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
  void observerPruneDisabledOnLegacyDaemon() {
    ObserverFixture fixture;
    auto record = fixture.record();
    auto runtimes = record.value("runtimes").toArray();
    for (int i = 0; i < runtimes.size(); ++i) {
      auto runtime = runtimes[i].toObject();
      runtime.remove("prune_eligible");
      runtimes[i] = runtime;
    }
    record.insert("runtimes", runtimes);
    fixture.record(record);
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST /v1/observers/") &&
          request.contains("/prune ")) {
        const auto body = QJsonDocument::fromJson(
                              request.mid(request.indexOf("\r\n\r\n") + 4))
                              .object();
        // The legacy endpoint would accept a version-only prune.
        const bool legacy = body == QJsonObject{{"expected_version", 1}};
        UiFixture::respond(socket, legacy ? 200 : 400,
                           legacy ? "{\"version\":2,\"pruned_count\":2}"
                                  : "{\"code\":\"invalid_request\"}");
      } else {
        fixture.observation(socket, request);
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance, false);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(client.online());
    auto *o = panel.observers();
    QTRY_VERIFY(o->online());
    QVERIFY(click(window, "teamConnectionsButton"));
    QTRY_COMPARE(o->rows().size(), 2);
    const auto pruneName = "observerPrune_" + fixture.runtime;
    QTRY_VERIFY(item(window, pruneName));
    QVERIFY(!item(window, pruneName)->isEnabled());
    QVERIFY(!o->prepare("prune", fixture.observer, fixture.runtime));
    QVERIFY(o->confirmation().isEmpty());
    QVERIFY(fixture.mutations.isEmpty());
  }
  void observerPruneConfirmsUnfilteredSetAndRejectsRaces() {
    ObserverFixture fixture;
    const auto initial = fixture.record();
    auto record = initial;
    auto runtimes = record.value("runtimes").toArray();
    auto hidden = runtimes.last().toObject();
    hidden.insert("disposition", "superseded");
    runtimes[1] = hidden;
    record.insert("runtimes", runtimes);
    fixture.record(record);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    auto *o = panel.observers();
    o->setActive(true);
    o->setViewFilter("candidate");
    QTRY_VERIFY(o->online());
    QCOMPARE(o->rows().size(), 1);
    QVERIFY(o->prepare("prune", fixture.observer, fixture.runtime));
    const auto text = o->confirmation();
    QVERIFY(text.contains(fixture.observer));
    QVERIFY(text.contains(fixture.runtime + "\n" + fixture.otherRuntime));
    QVERIFY(!text.contains("Project:"));
    QVERIFY(panel.selectProject(fixture.project));
    o->setOnlyProject(true);
    QTRY_VERIFY(o->online() && !o->loading());
    QCOMPARE(o->confirmation(), text);
    const auto reads = fixture.reads;
    o->setProject(QVariantMap{{"id", fixture.project}, {"name", "Renamed"}});
    QVERIFY(o->cacheCurrent());
    QVERIFY(!o->loading());
    QCOMPARE(fixture.reads, reads);
    QCOMPARE(o->confirmation(), text);
    // A newly registered candidate is unknown to the UI until the next read.
    auto added = hidden;
    auto reg = added.value("registration").toObject();
    reg.insert("runtime_id", "88888888-8888-4888-8888-888888888888");
    added.insert("registration", reg);
    runtimes.append(added);
    record.insert("runtimes", runtimes);
    fixture.record(record);
    QVERIFY(o->confirm());
    QTRY_COMPARE(fixture.mutations.size(), 1);
    const auto request = fixture.mutations.first();
    QCOMPARE(
        QJsonDocument::fromJson(request.mid(request.indexOf("\r\n\r\n") + 4))
            .object(),
        (QJsonObject{{"expected_version", 1},
                     {"expected_runtime_ids",
                      QJsonArray{fixture.runtime, fixture.otherRuntime}}}));
    QTRY_VERIFY(o->online() && !o->loading());
    QVERIFY(o->message().contains("confirm the runtime IDs again"));
    QCOMPARE(fixture.record(), record);
    QVERIFY(o->prepare("prune", fixture.observer, fixture.runtime));
    runtimes.removeLast();
    record.insert("runtimes", runtimes);
    fixture.record(record);
    o->reload();
    QTRY_VERIFY(o->online() && !o->loading());
    QVERIFY(!o->confirm());
    QVERIFY(o->message().contains("Refresh and confirm again"));
    QCOMPARE(fixture.mutations.size(), 1);
    o->reload();
    QTRY_VERIFY(o->online() && !o->loading());
    QVERIFY(o->prepare("prune", fixture.observer, fixture.runtime));
    // A wrong-typed or missing flag on even a hidden row disables all prune.
    added.insert("prune_eligible", "true");
    runtimes.append(added);
    record.insert("runtimes", runtimes);
    fixture.record(record);
    o->setOnlyProject(false);
    o->reload();
    QTRY_VERIFY(o->online() && !o->loading());
    QVERIFY(!o->rows().first().toMap().value("prunable").toBool());
    QVERIFY(!o->confirm());
    QCOMPARE(fixture.mutations.size(), 1);
    o->reload();
    QTRY_VERIFY(o->online() && !o->loading());
    QVERIFY(!o->prepare("prune", fixture.observer, fixture.runtime));
    record.insert("runtimes", QJsonArray{});
    fixture.record(record);
    o->reload();
    QTRY_VERIFY(o->online() && !o->loading());
    QVERIFY(!o->prepare("prune", fixture.observer, ""));
  }
  void observerPruneLocalizedConfirmationScreenshots() {
    ObserverFixture fixture;
    auto record = fixture.record();
    const auto prototype =
        record.value("runtimes").toArray().first().toObject();
    record.insert("runtimes", QJsonArray{prototype});
    fixture.record(record);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    writeFile(fixture.directory.filePath("language"), "ru");
    appearance.refresh();
    QCoreApplication::installTranslator(&appearance);
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    window->resize(1000, 680);
    client.reload();
    QTRY_VERIFY(client.online());
    auto *drawer = window->findChild<QObject *>("observerDrawer");
    QVERIFY(drawer && QMetaObject::invokeMethod(drawer, "open"));
    QTRY_VERIFY(drawer->property("opened").toBool());
    auto *o = panel.observers();
    QTRY_VERIFY(o->online() && !o->loading());
    const auto prefix = qEnvironmentVariable("ARTEL_PRUNE_SCREENSHOT");
    QStringList ids{fixture.runtime};
    for (const int count : {1, 8}) {
      if (count == 8) {
        QJsonArray rows{prototype};
        for (int i = 1; i < 8; ++i) {
          auto runtime = prototype;
          auto reg = runtime.value("registration").toObject();
          ids.append(fixture.runtime.chopped(2) + "0" + QString::number(i));
          reg.insert("runtime_id", ids.last());
          runtime.insert("registration", reg);
          rows.append(runtime);
        }
        record.insert("runtimes", rows);
        fixture.record(record);
        o->reload();
        QTRY_VERIFY(o->online() && !o->loading());
      }
      QVERIFY(o->prepare("prune", fixture.observer, fixture.runtime));
      auto *dialog = window->findChild<QObject *>("observerConfirmation");
      QVERIFY(dialog && QMetaObject::invokeMethod(dialog, "open"));
      QTRY_VERIFY(dialog->property("opened").toBool());
      auto *label = item(window, "observerConfirmationText");
      QVERIFY(label && label->isVisible());
      const auto text = label->property("text").toString();
      QVERIFY(text.contains(QString::fromUtf8("ID запусков для удаления")));
      const auto lines = text.split('\n');
      for (const auto &id : ids)
        QVERIFY(lines.contains(id));
      QCOMPARE(label->property("textFormat").toInt(), int(Qt::PlainText));
      QTest::qWait(80); // Let the offscreen scene polish and render the dialog.
      if (!prefix.isEmpty())
        QVERIFY(window->grabWindow().save(prefix + QString::number(count) +
                                          ".png"));
      for (const auto *name :
           {"observerConfirmAction", "observerCancelAction"}) {
        auto *button = item(window, name);
        QVERIFY(button && button->isVisible() && button->isEnabled());
        const auto bounds = button->mapRectToScene(button->boundingRect());
        QVERIFY2(
            QRectF(0, 0, window->width(), window->height()).contains(bounds),
            qPrintable(QString("%1 at %2,%3 (%4x%5)")
                           .arg(name)
                           .arg(bounds.x())
                           .arg(bounds.y())
                           .arg(bounds.width())
                           .arg(bounds.height())));
      }
      QVERIFY(QMetaObject::invokeMethod(dialog, "reject"));
      QTRY_VERIFY(!dialog->property("visible").toBool());
    }
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
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    QVERIFY(click(window, "observersButton"));
    auto *o = panel.observers();
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
    QTRY_VERIFY(item(window, "observerRevoke_" + fixture.otherRuntime));
    QVERIFY(click(window, "observerRevoke_" + fixture.otherRuntime));
    QVERIFY(QMetaObject::invokeMethod(
        window->findChild<QObject *>("observerConfirmation"), "accept"));
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
    auto initial = fixture.record();
    initial.insert("binding", QJsonObject{{"runtime_id", fixture.runtime},
                                          {"project_id", fixture.project}});
    auto initialRuntimes = initial.value("runtimes").toArray();
    auto bound = initialRuntimes.first().toObject();
    bound.insert("disposition", "bound");
    initialRuntimes[0] = bound;
    initial.insert("runtimes", initialRuntimes);
    fixture.record(initial);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    auto *o = panel.observers();
    TeamOverviewModel team(&panel);
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
    QCOMPARE(team.total(), 0);
    const QString replacement = "77777777-7777-4777-8777-777777777777";
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    snapshot.insert("instance_id", replacement);
    fixture.state = QJsonDocument(snapshot).toJson();
    fixture.registry.insert("database_instance_id", replacement);
    auto row = fixture.record();
    row.insert("label", "Replacement observer");
    row.insert("binding", QJsonObject{{"runtime_id", fixture.otherRuntime},
                                      {"project_id", fixture.project}});
    auto replacementRuntimes = row.value("runtimes").toArray();
    for (int i = 0; i < replacementRuntimes.size(); ++i) {
      auto runtime = replacementRuntimes[i].toObject();
      runtime.insert("disposition", i == 1 ? "bound" : "superseded");
      replacementRuntimes[i] = runtime;
    }
    row.insert("runtimes", replacementRuntimes);
    fixture.record(row);
    client.reload();
    QTRY_COMPARE(client.instance(), replacement);
    QTRY_VERIFY(o->online());
    QCOMPARE(o->rows().first().toMap().value("label").toString(),
             QString("Replacement observer"));
    QCOMPARE(team.unbound(), 0);
    QCOMPARE(team.total(), 1);
    QVERIFY(team.find(fixture.observer, fixture.runtime).isEmpty());
    QVERIFY(!team.find(fixture.observer, fixture.otherRuntime).isEmpty());
    if (old && old->state() == QAbstractSocket::ConnectedState)
      UiFixture::respond(old, 200, oldBytes);
    QTest::qWait(100);
    QCOMPARE(o->rows().first().toMap().value("label").toString(),
             QString("Replacement observer"));
    QCOMPARE(team.unbound(), 0);
    QCOMPARE(team.total(), 1);
    QVERIFY(team.find(fixture.observer, fixture.runtime).isEmpty());
    QVERIFY(!team.find(fixture.observer, fixture.otherRuntime).isEmpty());
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
  void inconsistentReviewWarningDoesNotBlameRequiredSources() {
    UiFixture fixture;
    const QString resultID = "44444444-4444-4444-8444-444444444444";
    const QString reviewID = "55555555-5555-4555-8555-555555555555";
    const QString sha(40, 'a');
    const QJsonObject result{{"id", resultID},
                             {"task_id", fixture.task},
                             {"repo", "/example/repo/.git"},
                             {"base", sha},
                             {"head", sha},
                             {"summary", "Fictional candidate"},
                             {"created_at", "2026-11-20T11:00:00Z"}};
    QJsonObject review = result;
    review.remove("summary");
    const QJsonObject pair{{"name", "reviewer"}, {"executor", "codex"}};
    const auto maximum = qint64{9007199254740991};
    const QJsonObject source{{"name", "reviewer"}, {"executor", "codex"},
                             {"required", true},   {"responded", true},
                             {"degraded", false},  {"findings", maximum},
                             {"pre_existing", 0},  {"open_questions", 0}};
    review.insert("id", reviewID);
    review.insert("result_id", resultID);
    review.insert("revmux_task", "legacy");
    review.insert("run", "run-01");
    review.insert("range_source", "operator");
    review.insert("report_ref", "/example/report");
    review.insert("complete", true);
    review.insert("required_sources", QJsonArray{pair});
    review.insert("sources", QJsonArray{source});
    review.insert("incomplete_reasons", QJsonArray{});
    review.insert("findings", maximum);
    review.insert("pre_existing", 0);
    review.insert("open_questions", 0);
    review.insert("evidence_consistent", false);
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      const auto prefix = "GET /v1/tasks/" + fixture.task.toUtf8() + "/results";
      QJsonObject response;
      if (request.startsWith(prefix + "/current "))
        response = {{"result", result}, {"reviews", QJsonArray{}}};
      else if (request.startsWith(prefix + "/current/reviews?"))
        response = {{"result_id", resultID},
                    {"reviews", QJsonArray{review}},
                    {"next_after", reviewID},
                    {"has_more", false}};
      else {
        fixture.normal(socket, request);
        return;
      }
      UiFixture::respond(socket, 200, QJsonDocument(response).toJson());
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    const auto languagePath = fixture.directory.filePath("language");
    writeFile(languagePath, "en\n");
    Appearance appearance(fixture.directory.path(), languagePath);
    QCoreApplication::installTranslator(&appearance);
    QQmlApplicationEngine engine;
    connect(&appearance, &Appearance::changed, &engine,
            &QQmlEngine::retranslate);
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    const auto warningName = "currentReviewOutcome_" + reviewID;
    QTRY_VERIFY(item(window, warningName));
    const auto *warning = item(window, warningName);
    QVERIFY(warning->isVisible());
    QCOMPARE(warning->property("severity").toString(), QString("warning"));
    QCOMPARE(warning->property("text").toString(),
             QString("Review evidence inconsistent"));
    writeFile(languagePath, "ru\n");
    appearance.refresh();
    QTRY_COMPARE(item(window, warningName)->property("text").toString(),
                 QString::fromUtf8("Данные ревью несогласованы"));
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
      QTest::qWait(
          30); // ListView header geometry follows the selected project.
      QVERIFY(click(window, "createTaskButton"));
      QTRY_COMPARE(panel.editorKind(), QString("task"));
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
    QTest::qWait(50);
    QVERIFY(click(window, "up_" + ids[2]));
    QTRY_VERIFY(!client.pending() && !client.loading());
    QCOMPARE(panel.tasks()[1].toMap().value("id").toString(), ids[2]);
    QTest::qWait(50);
    QVERIFY(click(window, "up_" + ids[2]));
    QTRY_VERIFY(!client.pending() && !client.loading());
    QCOMPARE(panel.tasks()[0].toMap().value("id").toString(), ids[2]);
    QTest::qWait(50);
    QVERIFY(click(window, "task_" + ids[0]));
    QTRY_COMPARE(panel.task().value("id").toString(), ids[0]);
    QTest::qWait(50);
    QVERIFY(item(window, "startButton")->isVisible());
    QVERIFY(!item(window, "startButton")->isEnabled());
    QVERIFY(item(window, "queueStartNotice")->isVisible());
    QVERIFY(!item(window, "doneButton")->isVisible());
    QVERIFY(click(window, "task_" + ids[2]));
    QTRY_VERIFY(panel.allowed("in_progress"));
    // State-dependent actions need a rendered layout pass before a mouse hit.
    QTest::qWait(50);
    if (qEnvironmentVariableIsSet("ARTEL_MANUAL_SCREENSHOT"))
      QVERIFY(window->grabWindow().save(
          qEnvironmentVariable("ARTEL_MANUAL_SCREENSHOT") + "-queued.png"));
    QVERIFY(click(window, "startButton"));
    QTRY_COMPARE(panel.task().value("state").toString(),
                 QString("in_progress"));
    QTRY_VERIFY(panel.allowed("done"));
    QVERIFY(click(window, "doneButton"));
    QTRY_COMPARE(panel.task().value("state").toString(), QString("done"));
    QCOMPARE(panel.stateLabel("done"), QString("Completed manually"));
    QTRY_VERIFY(!panel.historyLoading());
    QTRY_VERIFY(!panel.history().isEmpty());
    QCOMPARE(panel.history().last().toMap().value("source").toString(),
             QString("owner_manual"));
    QVERIFY(!panel.canArchive());
    QVERIFY(!item(window, "archiveButton")->isEnabled());
    QTRY_VERIFY(panel.allowed("backlog"));
    QVERIFY(click(window, "reopenButton"));
    QTRY_COMPARE(panel.task().value("state").toString(), QString("backlog"));
    QTRY_VERIFY(panel.allowed("cancelled"));
    QVERIFY(click(window, "cancelTaskButton"));
    QTRY_COMPARE(panel.task().value("state").toString(), QString("cancelled"));
    QTRY_VERIFY(panel.allowed("backlog"));
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
    if (const auto capture = qEnvironmentVariable("ARTEL_STATE_SCREENSHOT");
        !capture.isEmpty()) {
      QTest::qWait(100);
      QVERIFY(window->grabWindow().save(capture + "-conflict.png"));
    }
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
  void taskCommissionWarningSendHistoryAndStatuses() {
    UiFixture fixture;
    const QString binding = "44444444-4444-4444-8444-444444444444";
    const QString session = "55555555-5555-4555-8555-555555555555";
    const QString message = "66666666-6666-4666-8666-666666666666";
    bool bound = false, silentPost = false, ready = true;
    int posts = 0, absentQuestions = 0;
    QString status = "stored";
    QPointer<QTcpSocket> hanging;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/tasks/") &&
          request.contains("/delivery ")) {
        QJsonObject projection{{"task_id", fixture.task},
                               {"project_id", fixture.project},
                               {"binding", QJsonValue::Null}};
        if (bound) {
          projection.insert("ready", ready);
          projection.insert(
              "binding", QJsonObject{{"delivery_binding_id", binding},
                                     {"observer_id", fixture.instance},
                                     {"runtime_id", fixture.instance},
                                     {"project_id", fixture.project},
                                     {"pi_session_id", session},
                                     {"journal_id", fixture.instance},
                                     {"generation", 1},
                                     {"status", "active"},
                                     {"created_at", "2026-11-17T12:00:00Z"}});
        }
        UiFixture::respond(socket, 200, QJsonDocument(projection).toJson());
      } else if (request.startsWith("GET /v1/tasks/") &&
                 request.contains("/messages?")) {
        const QJsonObject row{{"id", message},
                              {"task_id", fixture.task},
                              {"delivery_binding_id", binding},
                              {"text", "Do the thing"},
                              {"status", status},
                              {"blocked", false},
                              {"created_at", "2026-11-17T12:00:00Z"}};
        const bool known =
            request.contains(("/tasks/" + fixture.task + "/").toUtf8());
        UiFixture::respond(
            socket, 200,
            QJsonDocument(
                QJsonObject{{"messages",
                             known && (request.contains("ids=") || posts > 0)
                                 ? QJsonArray{row}
                                 : QJsonArray{}},
                            {"next_after", 0}})
                .toJson());
      } else if (request.startsWith("GET /v1/tasks/") &&
                 request.contains("/questions?")) {
        ++absentQuestions;
        UiFixture::respond(socket, 404, "404 page not found");
      } else if (request.startsWith("POST /v1/tasks/")) {
        ++posts;
        if (silentPost) {
          hanging = socket;
          return;
        }
        UiFixture::respond(
            socket, 200,
            QJsonDocument(QJsonObject{{"id", message},
                                      {"task_id", fixture.task},
                                      {"delivery_binding_id", binding},
                                      {"text", "Do the thing"},
                                      {"status", "stored"},
                                      {"blocked", false},
                                      {"created_at", "2026-11-17T12:00:00Z"}})
                .toJson());
      } else {
        fixture.normal(socket, request);
      }
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
    // No delivery enabled: the reason is shown, sending stays disabled and
    // history is legitimately empty.
    QTRY_VERIFY(item(window, "assignmentWarning")->isVisible());
    QVERIFY(item(window, "assignmentWarning")
                ->property("text")
                .toString()
                .contains("not enabled"));
    QVERIFY(!item(window, "sendCommissionButton")->isEnabled());
    QVERIFY(!panel.sendCommission());
    QVERIFY(panel.messages().isEmpty());
    QTRY_VERIFY(absentQuestions > 0);
    QVERIFY(panel.message().isEmpty());
    // The binding becomes active and ready: assignment line plus the warning
    // that the text goes to the provider configured in Pi, shown before send.
    bound = true;
    panel.reload();
    // The bounded projection poll picks up the enabled assignment.
    QTRY_VERIFY_WITH_TIMEOUT(item(window, "assignmentLine")->isVisible(),
                             10000);
    QVERIFY(item(window, "assignmentLine")
                ->property("text")
                .toString()
                .contains(session));
    QVERIFY(item(window, "providerWarning")->isVisible());
    QVERIFY(enter(window, "commissionField", "Do the thing"));
    QCOMPARE(panel.commissionText(), QString("Do the thing"));
    // LongText releases Tab to keyboard navigation per the design contract.
    item(window, "commissionField")->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Tab);
    QVERIFY(window->activeFocusItem() != item(window, "commissionField"));
    QVERIFY(item(window, "sendCommissionButton")->isEnabled());
    silentPost = true;
    QVERIFY(click(window, "sendCommissionButton"));
    // Exactly one commission per explicit send.
    QTRY_COMPARE(posts, 1);
    // A repeated action is impossible while the previous send is unresolved;
    // the action and the button are disabled no-ops.
    QVERIFY(!panel.sendCommission());
    QVERIFY(!item(window, "sendCommissionButton")->isEnabled());
    QCOMPARE(posts, 1);
    // Resolve the hanging send; history reloads and keeps the commission text
    // only until the server confirms the accepted commission.
    UiFixture::respond(
        hanging, 200,
        QJsonDocument(QJsonObject{{"id", message},
                                  {"task_id", fixture.task},
                                  {"delivery_binding_id", binding},
                                  {"text", "Do the thing"},
                                  {"status", "stored"},
                                  {"blocked", false},
                                  {"created_at", "2026-11-17T12:00:00Z"}})
            .toJson());
    QTRY_VERIFY(!client.pending());
    QTRY_COMPARE(panel.messages().size(), 1);
    QCOMPARE(panel.messages().first().toMap().value("statusLabel").toString(),
             QString("Stored"));
    QCOMPARE(panel.commissionText(), QString());
    // The bounded poll picks up the observed status and updates the existing
    // row instead of appending a new one.
    status = "observed";
    QTRY_VERIFY_WITH_TIMEOUT(
        panel.messages().first().toMap().value("status").toString() ==
            "observed",
        9000);
    QCOMPARE(panel.messages().size(), 1);
    QCOMPARE(panel.messages().first().toMap().value("statusLabel").toString(),
             QString("Pi started preparing the turn"));
    // Selection change drops the commission history of the other task.
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    auto tasks = snapshot.value("tasks").toArray();
    auto other = tasks.first().toObject();
    other.insert("id", "77777777-7777-4777-8777-777777777777");
    tasks.append(other);
    snapshot.insert("tasks", tasks);
    fixture.state = QJsonDocument(snapshot).toJson();
    panel.reload();
    QTRY_VERIFY(!client.loading());
    QVERIFY(panel.selectTask("77777777-7777-4777-8777-777777777777"));
    QTRY_VERIFY(!panel.messagesLoading());
    QVERIFY(panel.messages().isEmpty());
    QVERIFY(!item(window, "messageStatus_" + message));
    // Returning to the original task reloads its history from the server.
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_COMPARE(panel.messages().size(), 1);
    // Stale writer readiness blocks sending with an explicit reason.
    ready = false;
    QVERIFY(panel.selectTask("77777777-7777-4777-8777-777777777777"));
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_VERIFY(item(window, "assignmentWarning")->isVisible());
    QTRY_VERIFY(item(window, "assignmentWarning")
                    ->property("text")
                    .toString()
                    .contains("not ready"));
    QVERIFY(!panel.sendCommission());
    // A terminal task refuses the commission; the reason stays visible.
    auto closed = QJsonDocument::fromJson(fixture.state).object();
    auto closedTasks = closed.value("tasks").toArray();
    auto closedFirst = closedTasks.first().toObject();
    closedFirst.insert("state", "done");
    closedTasks[0] = closedFirst;
    closed.insert("tasks", closedTasks);
    fixture.state = QJsonDocument(closed).toJson();
    panel.reload();
    QTRY_COMPARE(panel.task().value("state").toString(), QString("done"));
    QTRY_VERIFY(item(window, "assignmentWarning")
                    ->property("text")
                    .toString()
                    .contains("closed"));
    QVERIFY(!panel.sendCommission());
    client.stop();
  }
  void ownerAnswerClosedRetryRestoresOwnerWrites_data() {
    QTest::addColumn<bool>("firstRejection");
    QTest::addColumn<bool>("russian");
    QTest::newRow("unknown-retry") << false << false;
    QTest::newRow("first-post-and-close-guard") << true << false;
    QTest::newRow("first-post-and-close-guard-ru") << true << true;
  }
  void ownerAnswerClosedRetryRestoresOwnerWrites() {
    QFETCH(bool, firstRejection);
    QFETCH(bool, russian);
    UiFixture fixture;
    const QString question = "44444444-4444-4444-8444-444444444444";
    const QString binding = "55555555-5555-4555-8555-555555555555";
    const QString otherTask = "66666666-6666-4666-8666-666666666666";
    const QString replacement = "77777777-7777-4777-8777-777777777777";
    const QString draft = "Answer to keep after closure\nLiteral <b>text</b>";
    const auto answerPath = ("POST /v1/tasks/" + fixture.task + "/questions/" +
                             question + "/answer ")
                                .toUtf8();
    const auto otherTaskPath = ("GET /v1/tasks/" + otherTask + "/").toUtf8();
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    auto tasks = snapshot.value("tasks").toArray();
    auto other = tasks.first().toObject();
    other.insert("id", otherTask);
    other.insert("title", "Unrelated task");
    tasks.append(other);
    snapshot.insert("tasks", tasks);
    fixture.state = QJsonDocument(snapshot).toJson();
    const auto originalState = fixture.state;
    const QString time = "2026-11-17T12:00:00Z";
    QJsonObject row{{"id", question},
                    {"task_id", fixture.task},
                    {"delivery_binding_id", binding},
                    {"pi_session_id", fixture.instance},
                    {"text", "Example question"},
                    {"status", "open"},
                    {"created_at", time}};
    const QJsonObject grant{{"delivery_binding_id", binding},
                            {"observer_id", fixture.instance},
                            {"runtime_id", fixture.instance},
                            {"project_id", fixture.project},
                            {"pi_session_id", fixture.instance},
                            {"journal_id", fixture.instance},
                            {"generation", 1},
                            {"status", "active"},
                            {"allow_owner_questions", true},
                            {"created_at", time}};
    bool revoked = false;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith(answerPath)) {
        if (!revoked) {
          socket->disconnectFromHost(); // Lost before storing an answer.
        } else if (fixture.mutations.size() == 2) {
          UiFixture::respond(socket, 409, "{\"code\":\"closed\"}");
        } else {
          UiFixture::respond(socket, 409,
                             "{\"code\":\"closed\","
                             "\"message\":\"delivery operation rejected\"}");
        }
      } else if (request.contains("/questions?")) {
        const auto rows =
            request.startsWith(otherTaskPath) || request.contains("after=1&")
                ? QJsonArray{}
                : QJsonArray{row};
        UiFixture::respond(socket, 200,
                           QJsonDocument(QJsonObject{{"next_after", 1},
                                                     {"has_more", false},
                                                     {"questions", rows}})
                               .toJson());
      } else if (request.contains("/delivery ")) {
        UiFixture::respond(
            socket, 200,
            QJsonDocument(
                QJsonObject{{"task_id", request.startsWith(otherTaskPath)
                                            ? otherTask
                                            : fixture.task},
                            {"project_id", fixture.project},
                            {"ready", false},
                            {"binding",
                             revoked ? QJsonValue::Null : QJsonValue(grant)}})
                .toJson());
      } else if (request.startsWith("GET") && request.contains("/messages?")) {
        UiFixture::respond(socket, 200, "{\"messages\":[],\"next_after\":0}");
      } else if (request.startsWith("PATCH /v1/projects/")) {
        UiFixture::respond(
            socket, 200,
            ("{\"id\":\"" + fixture.project + "\",\"version\":2}").toUtf8());
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    QSignalSpy accepted(&client, &ApiClient::answerAccepted);
    QSignalSpy saved(&client, &ApiClient::mutationSucceeded);
    const auto languagePath = fixture.directory.filePath("language");
    writeFile(languagePath, russian ? "ru\n" : "en\n");
    Appearance appearance(fixture.directory.path(), languagePath);
    QCoreApplication::installTranslator(&appearance);
    QQmlApplicationEngine engine;
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_COMPARE(panel.questions().size(), 1);
    QTRY_VERIFY(panel.questions().first().toMap().value("canAnswer").toBool());
    QTRY_VERIFY(item(window, "answerField_" + question));
    QVERIFY(enter(window, "answerField_" + question, draft));
    if (firstRejection) {
      auto *workspace =
          window->property("workspace").value<WorkspaceController *>();
      QVERIFY(workspace && !workspace->closeAllowed());
      QVERIFY(!panel.dirty());
      QVERIFY(panel.selectTask(otherTask)); // No manual navigation guard.
      workspace->showTeam();
      auto *dialog = window->findChild<QObject *>("closeDialog");
      QVERIFY(dialog);
      window->close();
      QTRY_VERIFY(dialog->property("visible").toBool());
      QVERIFY(window->isVisible());
      QVERIFY(QMetaObject::invokeMethod(dialog, "reject"));
      QTRY_VERIFY(!dialog->property("visible").toBool());
      QVERIFY(panel.selectTask(fixture.task));
      workspace->returnToEditor();
      QTRY_VERIFY(
          panel.questions().size() == 1 &&
          panel.questions().first().toMap().value("canAnswer").toBool());
      QTRY_VERIFY(item(window, "answerField_" + question));
      auto *field = item(window, "answerField_" + question);
      QCOMPARE(field->property("text").toString(), draft);
      auto *label = item(window, "answerLabel_" + question);
      QVERIFY(label && label->isVisible());
      QVERIFY(label->y() + label->height() <= field->y());
      const QString labelText = russian ? QStringLiteral("Ответ для Pi")
                                        : QStringLiteral("Answer for Pi");
      QCOMPARE(label->property("text").toString(), labelText);
      auto *accessible = QAccessible::queryAccessibleInterface(field);
      QVERIFY(accessible);
      QCOMPARE(accessible->text(QAccessible::Name), labelText);
      QCOMPARE(fixture.mutations.size(), 0);
      // Another owner already changed the server state; this window still
      // has an open card when the first answer is rejected.
      revoked = true;
      row.insert("status", "closed");
      QVERIFY(panel.answerQuestion(question));
      QTRY_VERIFY(!client.pending());
      QCOMPARE(fixture.mutations.size(), 1);
      QCOMPARE(panel.message(),
               russian ? QStringLiteral("Вопрос закрыт. Ответ не записан, "
                                        "черновик сохранён.")
                       : QStringLiteral("Question is closed; answer not "
                                        "recorded, draft preserved"));
    } else {
      QVERIFY(panel.answerQuestion(question));
      QTRY_VERIFY(panel.uncertain() && !client.pending());
      QCOMPARE(fixture.mutations.size(), 1);
      QVERIFY(!panel.selectTask(fixture.task));
      QVERIFY(!panel.editProject(false));

      revoked = true;
      row.insert("status", "closed");
      // A malformed error carries no proof that replay was checked.
      QVERIFY(panel.retry());
      QTRY_VERIFY(!client.pending());
      QCOMPARE(fixture.mutations.size(), 2);
      QVERIFY(panel.uncertain());
      QCOMPARE(accepted.count(), 0);
      QTRY_VERIFY(client.canRetry());

      QVERIFY(panel.retry());
      QTRY_VERIFY(!client.pending());
      QCOMPARE(fixture.mutations.size(), 3);
      QCOMPARE(fixture.mutations[0], fixture.mutations[1]);
      QCOMPARE(fixture.mutations[0], fixture.mutations[2]);
    }
    QCOMPARE(client.instance(), fixture.instance);
    QCOMPARE(accepted.count(), 0);
    QVERIFY(!panel.uncertain());
    QVERIFY(!client.canRetry());
    QVERIFY(panel.writable());
    QCOMPARE(panel.answerText(question), draft);

    const auto visible = [&](const QString &name) {
      const auto *control = item(window, name + "_" + question);
      return control && control->isVisible();
    };
    const auto verifyClosedDraft = [&] {
      QTRY_VERIFY(visible("answerDraft"));
      QVERIFY(visible("answerDraftLabel"));
      QVERIFY(!visible("answerLabel"));
      QVERIFY(!visible("answerField"));
      QVERIFY(!visible("answerButton"));
      QVERIFY(!visible("answerText"));
      QVERIFY(!item(window, "answerButton_" + question)->isEnabled());
      QVERIFY(!panel.answerQuestion(question));
      auto *copy = item(window, "answerDraft_" + question);
      QVERIFY(copy->property("readOnly").toBool());
      QCOMPARE(copy->property("textFormat").toInt(), int(Qt::PlainText));
      QGuiApplication::clipboard()->clear();
      copy->forceActiveFocus();
      QTest::keyClick(window, Qt::Key_A, Qt::ControlModifier);
      QTest::keyClick(window, Qt::Key_C, Qt::ControlModifier);
      QCOMPARE(QGuiApplication::clipboard()->text(), draft);
    };
    QTRY_VERIFY(!visible("answerField")); // Polled closed, not reselected.
    verifyClosedDraft();

    QVERIFY(panel.selectTask(otherTask));
    QTRY_VERIFY(!panel.questionsLoading());
    QVERIFY(panel.questions().isEmpty());
    QVERIFY(!visible("answerDraft"));
    QVERIFY(panel.answerText(question).isEmpty());
    QVERIFY(!panel.answerQuestion(question));
    QVERIFY(panel.selectTask(fixture.task));
    QCOMPARE(panel.answerText(question), draft);
    verifyClosedDraft();

    // Even matching task/question IDs cannot show the old draft in another DB.
    snapshot.insert("instance_id", replacement);
    fixture.state = QJsonDocument(snapshot).toJson();
    client.reload();
    QTRY_COMPARE(client.instance(), replacement);
    QTRY_VERIFY(visible("questionText"));
    QVERIFY(!visible("answerDraft"));
    QVERIFY(!visible("answerDraftLabel"));
    QVERIFY(panel.answerText(question).isEmpty());
    QVERIFY(!panel.answerQuestion(question));
    fixture.state = originalState;
    client.reload();
    QTRY_COMPARE(client.instance(), fixture.instance);
    verifyClosedDraft();
    const int answerPosts = firstRejection ? 1 : 3;
    QCOMPARE(fixture.mutations.size(), answerPosts);

    QVERIFY(panel.editProject(false));
    panel.field("description", "Unrelated project edit");
    QVERIFY(panel.save());
    QTRY_COMPARE(saved.count(), 1);
    QCOMPARE(fixture.mutations.size(), answerPosts + 1);
    QVERIFY(fixture.mutations.last().startsWith("PATCH /v1/projects/"));
    QCOMPARE(panel.answerText(question), draft);
    if (firstRejection) {
      auto *workspace =
          window->property("workspace").value<WorkspaceController *>();
      QVERIFY(!workspace->closeAllowed());
      window->close();
      auto *dialog = window->findChild<QObject *>("closeDialog");
      QTRY_VERIFY(dialog->property("visible").toBool());
      QVERIFY(QMetaObject::invokeMethod(dialog, "discarded"));
      QTRY_VERIFY(!window->isVisible());
    }
    client.stop();
  }
  void ownerQuestionOfflineSelectionAndFullTailDiscovery() {
    UiFixture fixture;
    const auto row = [&](int position) {
      return QJsonObject{
          {"id", QUuid::createUuid().toString(QUuid::WithoutBraces)},
          {"task_id", fixture.task},
          {"delivery_binding_id", fixture.instance},
          {"pi_session_id", fixture.instance},
          {"text", QString("Fictional question %1").arg(position)},
          {"status", "open"},
          {"created_at", "2026-11-17T12:00:00Z"}};
    };
    QJsonArray questions;
    for (int i = 0; i < 5; ++i)
      questions.append(row(i));
    bool offline = false;
    QList<int> cursors;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (offline && request.startsWith("GET /v1/snapshot ")) {
        UiFixture::respond(socket, 503, "{}");
      } else if (request.contains("/questions?")) {
        const QUrlQuery query(
            QUrl(QString::fromUtf8(request.split(' ').at(1))));
        if (query.hasQueryItem("ids")) {
          UiFixture::respond(
              socket, 200,
              R"({"questions":[],"next_after":0,"has_more":false})");
          return;
        }
        const int after = query.queryItemValue("after").toInt();
        cursors.append(after);
        QJsonArray batch;
        const int end = std::min(after + 5, static_cast<int>(questions.size()));
        for (int i = after; i < end; ++i)
          batch.append(questions.at(i));
        UiFixture::respond(
            socket, 200,
            QJsonDocument(QJsonObject{{"questions", batch},
                                      {"next_after", end},
                                      {"has_more", end < questions.size()}})
                .toJson());
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(client.online());
    const auto sequence = client.sequence();
    QVERIFY(panel.selectProject(fixture.project));
    offline = true;
    client.reload();
    QTRY_VERIFY(!client.online());
    QVERIFY(panel.selectTask(fixture.task));
    QVERIFY(cursors.isEmpty());
    offline = false;
    QTRY_VERIFY_WITH_TIMEOUT(client.online(), 4000);
    QCOMPARE(client.instance(), fixture.instance);
    QCOMPARE(client.sequence(), sequence);
    QTRY_COMPARE(panel.questions().size(), 5);
    QVERIFY(!panel.questionsMore());
    const auto sixth = row(5);
    questions.append(sixth);
    QTRY_COMPARE_WITH_TIMEOUT(panel.questions().size(), 6, 5000);
    QCOMPARE(panel.questions().last().toMap().value("id").toString(),
             sixth.value("id").toString());
    QCOMPARE(cursors.first(), 0);
    QVERIFY(cursors.contains(5));
    QVERIFY(!panel.questionsMore());
  }

  void ownerQuestionPaginationAnswerAndStatuses() {
    UiFixture fixture;
    const QString binding = "44444444-4444-4444-8444-444444444444";
    const QString session = "55555555-5555-4555-8555-555555555555";
    const QString answerMessage = "66666666-6666-4666-8666-666666666666";
    const QString foreign = "99999999-9999-4999-8999-999999999999";
    QStringList ids;
    QJsonArray questions;
    for (int i = 0; i < 6; ++i) {
      const auto id = QUuid::createUuid().toString(QUuid::WithoutBraces);
      ids.append(id);
      questions.append(
          QJsonObject{{"id", id},
                      {"task_id", fixture.task},
                      {"delivery_binding_id", binding},
                      {"pi_session_id", session},
                      {"text", QString("Literal <b>question</b> %1").arg(i)},
                      {"status", "open"},
                      {"created_at", "2026-11-17T12:00:00Z"}});
    }
    int posts = 0, pageReads = 0;
    bool includeForeign = false, revoked = false, holdStatuses = false;
    QList<QStringList> pollBatches;
    QPointer<QTcpSocket> heldStatus, heldPage;
    QByteArray heldPageBody;
    bool holdPage = true;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/tasks/") &&
          request.contains("/delivery ")) {
        if (revoked) {
          UiFixture::respond(
              socket, 200,
              QJsonDocument(QJsonObject{{"task_id", fixture.task},
                                        {"project_id", fixture.project},
                                        {"binding", QJsonValue::Null},
                                        {"ready", false}})
                  .toJson());
          return;
        }
        UiFixture::respond(
            socket, 200,
            QJsonDocument(
                QJsonObject{
                    {"task_id", fixture.task},
                    {"project_id", fixture.project},
                    {"ready", false},
                    {"binding",
                     QJsonObject{{"delivery_binding_id", binding},
                                 {"observer_id", fixture.instance},
                                 {"runtime_id", fixture.instance},
                                 {"project_id", fixture.project},
                                 {"pi_session_id", session},
                                 {"journal_id", fixture.instance},
                                 {"generation", 1},
                                 {"status", "active"},
                                 {"allow_owner_questions", true},
                                 {"created_at", "2026-11-17T12:00:00Z"}}}})
                .toJson());
      } else if (request.startsWith("GET /v1/tasks/") &&
                 request.contains("/messages?")) {
        UiFixture::respond(socket, 200, "{\"messages\":[],\"next_after\":0}");
      } else if (request.startsWith("GET /v1/tasks/") &&
                 request.contains("/questions?")) {
        const QUrl url(
            QString::fromUtf8(request.split('\n').first().split(' ')[1]));
        const QUrlQuery query(url);
        QJsonArray result;
        qint64 next = 0;
        bool more = false;
        if (query.hasQueryItem("ids")) {
          const auto requested =
              query.queryItemValue("ids").split(',', Qt::SkipEmptyParts);
          if (holdStatuses) {
            heldStatus = socket;
            return;
          }
          pollBatches.append(requested);
          for (const auto &id : requested)
            for (const auto &value : questions)
              if (value.toObject().value("id").toString() == id)
                result.append(value);
          if (includeForeign) {
            auto row = questions.first().toObject();
            row.insert("id", foreign);
            row.insert("text", "Foreign row must not be appended");
            result.append(row);
          }
        } else {
          ++pageReads;
          const int after = query.queryItemValue("after").toInt();
          const int end = static_cast<int>(
              std::min<qsizetype>(after + 5, questions.size()));
          for (int i = after; i < end; ++i)
            result.append(questions.at(i));
          next = end;
          more = end < questions.size();
          if (after != 0 && holdPage) {
            heldPage = socket;
            heldPageBody = QJsonDocument(QJsonObject{{"questions", result},
                                                     {"next_after", next},
                                                     {"has_more", more}})
                               .toJson();
            return;
          }
        }
        UiFixture::respond(socket, 200,
                           QJsonDocument(QJsonObject{{"questions", result},
                                                     {"next_after", next},
                                                     {"has_more", more}})
                               .toJson());
      } else if (request.startsWith("POST /v1/tasks/") &&
                 request.contains("/answer ")) {
        ++posts;
        if (posts == 1) {
          socket->disconnectFromHost();
          return;
        }
        auto answered = questions.first().toObject();
        answered.insert("status", "answered");
        answered.insert("answer_message_id", answerMessage);
        answered.insert("answer_text", "answer <b>plain</b>");
        answered.insert("answer_status", "unknown");
        answered.insert("answer_block_reason", "delivery_revoked");
        answered.insert("answered_at", "2026-11-17T12:01:00Z");
        questions[0] = answered;
        UiFixture::respond(
            socket, 200,
            QJsonDocument(QJsonObject{{"question_id", ids[0]},
                                      {"message_id", answerMessage},
                                      {"status", "stored"}})
                .toJson());
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    QCoreApplication::installTranslator(&appearance);
    connect(&appearance, &Appearance::changed, &engine,
            &QQmlEngine::retranslate);
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_COMPARE(panel.questions().size(), 5);
    QVERIFY(panel.questionsMore());
    auto *moreButton = item(window, "questionsMoreButton");
    QVERIFY(moreButton && moreButton->isEnabled());
    auto question = [&](const QString &id) {
      for (const auto &value : panel.questions())
        if (value.toMap().value("id").toString() == id)
          return value.toMap();
      return QVariantMap{};
    };
    QVERIFY(!panel.assignment().value("ready").toBool());
    QVERIFY(!panel.assignment().value("available").toBool());
    auto *questionText = item(window, "questionText_" + ids[0]);
    QVERIFY(questionText);
    QCOMPARE(questionText->property("textFormat").toInt(),
             static_cast<int>(Qt::PlainText));
    auto *providerWarning = item(window, "answerProviderWarning_" + ids[0]);
    QVERIFY(providerWarning && providerWarning->isVisible());
    // A real backlog does not trigger background page reads. The status
    // timer is active, but only the explicit action advances the cursor.
    QTRY_VERIFY_WITH_TIMEOUT(!pollBatches.isEmpty(), 5000);
    QCOMPARE(pageReads, 1);
    QCOMPARE(panel.questions().size(), 5);
    panel.moreQuestions();
    QTRY_VERIFY(heldPage);
    auto newer = QJsonDocument::fromJson(fixture.state).object();
    const auto sequence = client.sequence();
    newer.insert("event_seq", sequence + 1);
    fixture.state = QJsonDocument(newer).toJson();
    client.reload();
    QTRY_COMPARE(client.sequence(), sequence + 1);
    QVERIFY(panel.questionsLoading());
    QVERIFY(heldPage);
    holdPage = false;
    UiFixture::respond(heldPage, 200, heldPageBody);
    QTRY_COMPARE(panel.questions().size(), 6);
    QCOMPARE(pageReads, 2);
    QVERIFY(!panel.questionsMore());
    QVERIFY(enter(window, "answerField_" + ids[1], "Other recipient draft"));
    const auto fieldName = "answerField_" + ids[0];
    const auto validationName = "answerValidation_" + ids[0];
    const auto answerButtonName = "answerButton_" + ids[0];
    const QString exact(2048,
                        QChar(0x0416)); // 4,096 UTF-8 bytes, not code units.
    QVERIFY(enter(window, fieldName, exact));
    QVERIFY(item(window, answerButtonName)->isEnabled());
    QVERIFY(!item(window, validationName)->isVisible());
    QVERIFY(enter(window, fieldName, exact + QStringLiteral("a")));
    QVERIFY(!item(window, answerButtonName)->isEnabled());
    QVERIFY(item(window, validationName)->isVisible());
    QVERIFY(item(window, validationName)
                ->property("text")
                .toString()
                .contains("4,096 UTF-8"));
    const auto assertWarning = [&](const QString &text) {
      QTRY_COMPARE(item(window, validationName)->property("text").toString(),
                   text);
      QCOMPARE(item(window, fieldName)->property("text").toString(),
               exact + "a");
      QVERIFY(!item(window, answerButtonName)->isEnabled());
    };
    writeFile(fixture.directory.filePath("language"), "ru\n");
    appearance.refresh();
    assertWarning(
        QString::fromUtf8("Ответ превышает ограничение в 4096 байт UTF-8"));
    writeFile(fixture.directory.filePath("language"), "en\n");
    appearance.refresh();
    assertWarning("Answer exceeds the 4,096 UTF-8 byte limit");
    QVERIFY(!panel.answerQuestion(ids[0]));
    panel.discardTerminalAnswerDraft(
        ids[0]); // Cannot discard an open card here.
    QCOMPARE(panel.answerText(ids[0]), exact + "a");
    QCOMPARE(posts, 0);
    QTest::keyClick(window, Qt::Key_A, Qt::ControlModifier);
    QTest::keyClick(window, Qt::Key_Backspace);
    QPointer<QQuickItem> editor = item(window, fieldName);
    QVERIFY(editor);
    editor->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_A);
    QTest::keyClick(window, Qt::Key_N);
    QVERIFY(editor && editor->hasActiveFocus());
    QCOMPARE(editor->property("text").toString(), QString("an"));
    QCOMPARE(editor->property("cursorPosition").toInt(), 2);
    // Change a loaded row through the production timer (not a direct refresh).
    auto updated = questions.at(0).toObject();
    updated.insert("pi_session_id", fixture.instance);
    questions[0] = updated;
    QTRY_COMPARE_WITH_TIMEOUT(
        question(ids[0]).value("pi_session_id").toString(), fixture.instance,
        7000);
    QTRY_VERIFY_WITH_TIMEOUT(std::any_of(pollBatches.cbegin(),
                                         pollBatches.cend(),
                                         [&](const auto &batch) {
                                           return batch == QStringList{ids[5]};
                                         }),
                             7000);
    QSet<QString> polled;
    for (const auto &batch : pollBatches) {
      QVERIFY(batch.size() >= 1 && batch.size() <= 5);
      for (const auto &id : batch)
        polled.insert(id);
    }
    QCOMPARE(polled, QSet<QString>(ids.cbegin(), ids.cend()));
    QVERIFY(editor && editor == item(window, "answerField_" + ids[0]));
    QVERIFY(editor->hasActiveFocus());
    QCOMPARE(editor->property("cursorPosition").toInt(), 2);
    QTest::keyClick(window, Qt::Key_S);
    QInputMethodEvent remainder;
    remainder.setCommitString("wer <b>plain</b>");
    QCoreApplication::sendEvent(window, &remainder);
    QVERIFY(editor && editor->hasActiveFocus());
    QCOMPARE(panel.answerText(ids[0]), QString("answer <b>plain</b>"));
    QCOMPARE(panel.answerText(ids[1]), QString("Other recipient draft"));
    {
      auto *button = item(window, answerButtonName);
      // Writer readiness is not answer eligibility.
      QVERIFY(button && button->isEnabled());
      button->forceActiveFocus();
    }
    QTest::keyClick(window, Qt::Key_Space);
    QTRY_VERIFY(panel.uncertain());
    QCOMPARE(posts, 1);
    QVERIFY(!panel.answerQuestion(ids[0]));
    const auto answerButtonDisabled = [&] {
      const auto *button = item(window, answerButtonName);
      return button && !button->isEnabled();
    };
    QTRY_VERIFY(answerButtonDisabled());
    panel.setAnswerText(ids[0], "Newer draft survives the replay");
    // An independent GET may already show a server outcome when the exact ACK
    // arrives.
    auto saved = questions.at(0).toObject();
    saved.insert("status", "answered");
    saved.insert("answer_message_id", answerMessage);
    saved.insert("answer_text", "answer <b>plain</b>");
    saved.insert("answer_status", "unknown");
    saved.insert("answer_block_reason", "delivery_revoked");
    saved.insert("answered_at", "2026-11-17T12:01:00Z");
    questions[0] = saved;
    client.refreshQuestionStatuses(fixture.task, {ids[0]});
    QTRY_COMPARE(question(ids[0]).value("answer_status").toString(),
                 QString("unknown"));
    holdStatuses = true;
    QVERIFY(panel.retry());
    QTRY_VERIFY(!panel.uncertain() && heldStatus);
    QTRY_COMPARE(question(ids[0]).value("status").toString(),
                 QString("answered"));
    QCOMPARE(question(ids[0]).value("answer_status").toString(),
             QString("unknown"));
    QCOMPARE(question(ids[0]).value("answered_at").toString(),
             QString("2026-11-17T12:01:00Z"));
    QCOMPARE(question(ids[0]).value("answer_block_reason").toString(),
             QString("delivery_revoked"));
    auto stale = saved;
    stale.insert("answer_status", "stored");
    stale.remove("answer_block_reason");
    QSignalSpy staleRead(&client, &ApiClient::questionStatusesReceived);
    UiFixture::respond(
        heldStatus, 200,
        QJsonDocument(QJsonObject{{"questions", QJsonArray{stale}},
                                  {"next_after", 1},
                                  {"has_more", false}})
            .toJson());
    QTRY_COMPARE(staleRead.count(), 1);
    QCOMPARE(question(ids[0]).value("answer_status").toString(),
             QString("unknown"));
    QCOMPARE(question(ids[0]).value("answer_block_reason").toString(),
             QString("delivery_revoked"));
    holdStatuses = false;
    QCOMPARE(posts, 2);
    QCOMPARE(fixture.mutations.size(), 2);
    QCOMPARE(fixture.mutations[0], fixture.mutations[1]);
    QCOMPARE(question(ids[0]).value("answer_text").toString(),
             QString("answer <b>plain</b>"));
    QCOMPARE(panel.answerText(ids[0]),
             QString("Newer draft survives the replay"));
    QCOMPARE(panel.answerText(ids[1]), QString("Other recipient draft"));

    const auto *answeredDraft = item(window, "answerDraft_" + ids[0]);
    QVERIFY(answeredDraft && answeredDraft->isVisible());
    QVERIFY(answeredDraft->property("readOnly").toBool());
    QCOMPARE(answeredDraft->property("text").toString(),
             panel.answerText(ids[0]));
    const auto *savedAnswer = item(window, "answerText_" + ids[0]);
    QVERIFY(savedAnswer && savedAnswer->isVisible());
    QCOMPARE(savedAnswer->property("text").toString(),
             QString("answer <b>plain</b>"));
    QVERIFY(!item(window, answerButtonName)->isVisible());
    QVERIFY(!panel.answerQuestion(ids[0]));
    auto *discard = item(window, "discardAnswerDraft_" + ids[0]);
    QVERIFY(discard && discard->isVisible() && discard->isEnabled());
    discard->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Space);
    QTRY_VERIFY(panel.answerText(ids[0]).isEmpty());
    QVERIFY(!answeredDraft->isVisible());
    QCOMPARE(panel.answerText(ids[1]), QString("Other recipient draft"));
    QCOMPARE(savedAnswer->property("text").toString(),
             QString("answer <b>plain</b>"));
    QCOMPARE(posts, 2);

    auto observed = questions.first().toObject();
    observed.insert("answer_status", "observed");
    observed.remove("answer_block_reason");
    questions[0] = observed;
    client.refreshQuestionStatuses(fixture.task, {ids[0]});
    QTRY_COMPARE(question(ids[0]).value("answer_status").toString(),
                 QString("observed"));
    auto replayed = observed;
    replayed.insert("answer_status", "stored");
    questions[0] = replayed;
    auto blocked = questions.at(1).toObject();
    blocked.insert("status", "answered");
    blocked.insert("answer_message_id", answerMessage);
    blocked.insert("answer_text", "Blocked answer");
    blocked.insert("answer_status", "unknown");
    blocked.insert("answer_block_reason", "delivery_revoked");
    blocked.insert("answered_at", "2026-11-17T12:02:00Z");
    revoked = true;
    // Loaded history refreshes even when the current projection has no grant.
    QTRY_VERIFY_WITH_TIMEOUT(!panel.assignment().value("enabled").toBool(),
                             7000);
    questions[1] = blocked;
    QTRY_COMPARE_WITH_TIMEOUT(
        question(ids[1]).value("answer_status").toString(), QString("unknown"),
        7000);
    QCOMPARE(question(ids[0]).value("answer_status").toString(),
             QString("observed"));
    QCOMPARE(panel.questions().size(), 6);
    QVERIFY(question(foreign).isEmpty());
    auto *answerStatus = item(window, "answerStatus_" + ids[1]);
    QVERIFY(answerStatus && answerStatus->isVisible());
    auto *blockReason = item(window, "answerBlockReason_" + ids[1]);
    QVERIFY(blockReason && blockReason->isVisible());
    QVERIFY(blockReason->property("text").toString().contains("not active"));
    includeForeign = true;
    client.refreshQuestionStatuses(fixture.task, {ids[0], ids[1]});
    QTRY_VERIFY(!client.questionStatusesLoading());
    QVERIFY(question(foreign).isEmpty());
    client.loadQuestions(fixture.task, 0);
    QTRY_VERIFY(!panel.questionsLoading());
    QCOMPARE(panel.questions().size(), 6);
    QCOMPARE(question(ids[0]).value("answer_status").toString(),
             QString("observed"));
    client.stop();
  }
  void ownerQuestionReplacementAndReconnectFences_data() {
    QTest::addColumn<QString>("mode");
    QTest::newRow("page") << "page";
    QTest::newRow("ids") << "ids";
  }
  void ownerQuestionReplacementAndReconnectFences() {
    QFETCH(QString, mode);
    UiFixture fixture;
    const QString otherTask = "77777777-7777-4777-8777-777777777777";
    const QString replacement = "88888888-8888-4888-8888-888888888888";
    const QString binding = "44444444-4444-4444-8444-444444444444";
    const QString questionID = "66666666-6666-4666-8666-666666666666";
    const QString answerMessage = "99999999-9999-4999-8999-999999999999";
    const QJsonObject openQuestion{{"id", questionID},
                                   {"task_id", fixture.task},
                                   {"delivery_binding_id", binding},
                                   {"pi_session_id", fixture.instance},
                                   {"text", "Original question"},
                                   {"status", "open"},
                                   {"created_at", "2026-11-17T12:00:00Z"}};
    QJsonArray questions{openQuestion};
    auto snapshot = QJsonDocument::fromJson(fixture.state).object();
    auto tasks = snapshot.value("tasks").toArray();
    auto other = tasks.first().toObject();
    other.insert("id", otherTask);
    tasks.append(other);
    snapshot.insert("tasks", tasks);
    fixture.state = QJsonDocument(snapshot).toJson();
    const auto originalState = fixture.state;
    QString delay;
    const bool loseAnswerAck = mode == "ids";
    QUrlQuery captured;
    const auto replyToRead = [&](QTcpSocket *socket, const QUrlQuery &query) {
      const auto db = QJsonDocument::fromJson(fixture.state)
                          .object()
                          .value("instance_id")
                          .toString();
      const auto pin = query.queryItemValue("instance_id");
      if (pin.isEmpty()) {
        UiFixture::respond(socket, 400, "{\"code\":\"invalid_request\"}");
      } else if (pin != db) {
        UiFixture::respond(socket, 409, "{\"code\":\"instance_mismatch\"}");
      } else {
        UiFixture::respond(socket, 200,
                           QJsonDocument(QJsonObject{{"questions", questions},
                                                     {"next_after", 1},
                                                     {"has_more", false}})
                               .toJson());
      }
    };
    QPointer<QTcpSocket> delayedRead, delayedSnapshot, stream;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/snapshot ") && delay == "snapshot") {
        delay.clear();
        delayedSnapshot = socket;
      } else if (request.startsWith("POST") && request.contains("/answer ")) {
        if (loseAnswerAck)
          socket->disconnectFromHost();
        else
          UiFixture::respond(
              socket, 409,
              R"({"code":"instance_mismatch","message":"Different database"})");
      } else if (request.startsWith("POST /v1/projects ")) {
        UiFixture::respond(
            socket, 201,
            ("{\"id\":\"" + fixture.project + "\",\"version\":1}").toUtf8());
      } else if (request.startsWith("GET /v1/stream?")) {
        stream = socket;
        fixture.normal(socket, request);
      } else if (request.startsWith("GET /v1/tasks/") &&
                 request.contains("/delivery ")) {
        UiFixture::respond(
            socket, 200,
            QJsonDocument(
                QJsonObject{
                    {"task_id", fixture.task},
                    {"project_id", fixture.project},
                    {"ready", true},
                    {"binding",
                     QJsonObject{{"delivery_binding_id", binding},
                                 {"observer_id", fixture.instance},
                                 {"runtime_id", fixture.instance},
                                 {"project_id", fixture.project},
                                 {"pi_session_id", fixture.instance},
                                 {"journal_id", fixture.instance},
                                 {"generation", 1},
                                 {"status", "active"},
                                 {"allow_owner_questions", true},
                                 {"created_at", "2026-11-17T12:00:00Z"}}}})
                .toJson());
      } else if (request.startsWith("GET /v1/tasks/") &&
                 request.contains("/messages?")) {
        UiFixture::respond(socket, 200, "{\"messages\":[],\"next_after\":0}");
      } else if (request.startsWith("GET /v1/tasks/") &&
                 request.contains("/questions?")) {
        const QUrlQuery query(
            QUrl(QString::fromUtf8(request.split('\n').first().split(' ')[1])));
        if (delay == "read") {
          captured = query;
          delay.clear();
          delayedRead = socket;
          return;
        }
        replyToRead(socket, query);
      } else {
        fixture.normal(socket, request);
      }
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
    QTRY_COMPARE(panel.questions().size(), 1);
    auto currentQuestion = [&] {
      const auto rows = panel.questions();
      return rows.isEmpty() ? QVariantMap{} : rows.first().toMap();
    };
    QVERIFY(enter(window, "answerField_" + questionID,
                  "Original database answer draft"));

    // A first-page reply delivered as the selection changes cannot repopulate
    // the newly selected task or carry its answer draft to that recipient.
    delay = "read";
    client.loadQuestions(fixture.task, 0);
    QTRY_VERIFY(delayedRead);
    UiFixture::respond(delayedRead, 200,
                       QJsonDocument(QJsonObject{{"questions", questions},
                                                 {"next_after", 1},
                                                 {"has_more", false}})
                           .toJson());
    QVERIFY(panel.selectTask(otherTask));
    QTest::qWait(50);
    QVERIFY(panel.questions().isEmpty());
    QVERIFY(panel.answerText(questionID).isEmpty());
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_COMPARE(panel.questions().size(), 1);
    QVERIFY(enter(window, "answerField_" + questionID,
                  "Original database answer draft"));

    QTRY_VERIFY(stream);
    stream->disconnectFromHost();
    QTRY_VERIFY(!client.online());
    QTRY_VERIFY_WITH_TIMEOUT(client.online(), 5000);
    QTRY_COMPARE(panel.questions().size(), 1);
    QTRY_COMPARE(
        item(window, "answerField_" + questionID)->property("text").toString(),
        QString("Original database answer draft"));

    delay = "read";
    if (mode == "page")
      client.loadQuestions(fixture.task, 0);
    else
      client.refreshQuestionStatuses(fixture.task, {questionID});
    QTRY_VERIFY(delayedRead);
    auto replaced = openQuestion;
    replaced.insert("status", "answered");
    replaced.insert("answer_message_id", answerMessage);
    replaced.insert("answer_text", "Replacement database answer");
    replaced.insert("answer_status", "observed");
    replaced.insert("answered_at", "2026-11-17T12:01:00Z");
    questions = QJsonArray{replaced};
    snapshot = QJsonDocument::fromJson(fixture.state).object();
    snapshot.insert("instance_id", replacement);
    fixture.state = QJsonDocument(snapshot).toJson();
    delay = "snapshot";
    client.reload();
    QTRY_VERIFY(delayedSnapshot);
    QCOMPARE(captured.queryItemValue("instance_id"), fixture.instance);
    if (mode == "page") {
      QVERIFY(!captured.hasQueryItem("ids"));
      QCOMPARE(captured.queryItemValue("after"), QString("0"));
      QCOMPARE(captured.queryItemValue("limit"), QString("5"));
    } else {
      QCOMPARE(captured.queryItemValue("ids"), questionID);
      QVERIFY(!captured.hasQueryItem("after"));
      QVERIFY(!captured.hasQueryItem("limit"));
    }
    replyToRead(delayedRead, captured);
    QTest::qWait(50);
    QCOMPARE(currentQuestion().value("status").toString(), QString("open"));
    QCOMPARE(panel.answerText(questionID),
             QString("Original database answer draft"));
    QCOMPARE(
        item(window, "answerField_" + questionID)->property("text").toString(),
        QString("Original database answer draft"));
    UiFixture::respond(delayedSnapshot, 200, fixture.state);
    QTRY_COMPARE(client.instance(), replacement);
    QTRY_COMPARE(currentQuestion().value("status").toString(),
                 QString("answered"));
    QVERIFY(panel.answerText(questionID).isEmpty());

    questions = QJsonArray{openQuestion};
    fixture.state = originalState;
    client.reload();
    QTRY_COMPARE(client.instance(), fixture.instance);
    QTRY_COMPARE(currentQuestion().value("status").toString(), QString("open"));
    QTRY_COMPARE(panel.answerText(questionID),
                 QString("Original database answer draft"));
    QTRY_COMPARE(
        item(window, "answerField_" + questionID)->property("text").toString(),
        QString("Original database answer draft"));
    // Exercise the POST window with identical task/question IDs in the new DB.
    // A first mismatch is not a domain conflict; a lost ACK stays fenced.
    const auto draft = QString("Original database answer draft");
    const auto originalKey =
        fixture.instance + "/" + fixture.task + "/" + questionID;
    QTRY_VERIFY(currentQuestion().value("canAnswer").toBool());
    panel.discardDetachedAnswerDraft(
        originalKey); // A current key is not recoverable here.
    QCOMPARE(panel.answerText(questionID), draft);
    fixture.state = QJsonDocument(snapshot).toJson();
    QVERIFY(panel.answerQuestion(questionID));
    QTRY_VERIFY(!panel.busy());
    QCOMPARE(fixture.mutations.size(), 1);
    QCOMPARE(panel.uncertain(), loseAnswerAck);
    if (loseAnswerAck)
      client.reload();
    QTRY_COMPARE(client.instance(), replacement);
    QTRY_COMPARE(currentQuestion().value("status").toString(), QString("open"));
    QTRY_COMPARE(
        item(window, "answerField_" + questionID)->property("text").toString(),
        QString());
    QVERIFY(panel.answerText(questionID).isEmpty());
    QVERIFY(!panel.answerQuestion(questionID));
    QVERIFY(!panel.retry());
    QVERIFY(!panel.conflict());
    auto *workspace =
        window->property("workspace").value<WorkspaceController *>();
    QVERIFY(!workspace->closeAllowed());
    workspace->showTeam();
    QTRY_VERIFY(item(window, "recoverAnswerDraftsButton")->isVisible());
    QVERIFY(click(window, "recoverAnswerDraftsButton"));
    auto *dialog = window->findChild<QObject *>("answerRecoveryDialog");
    QVERIFY(dialog);
    QTRY_VERIFY(dialog->property("opened").toBool());
    auto *recovered = item(window, "detachedAnswerDraft_" + questionID);
    QVERIFY(recovered && recovered->isVisible());
    QVERIFY(recovered->property("readOnly").toBool());
    QCOMPARE(recovered->property("textFormat").toInt(), int(Qt::PlainText));
    QCOMPARE(recovered->property("text").toString(), draft);
    QGuiApplication::clipboard()->clear();
    auto *copy = item(window, "copyDetachedAnswerDraft_" + questionID);
    QVERIFY(copy && copy->isVisible() && copy->isEnabled());
    copy->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Space);
    QTRY_COMPARE(QGuiApplication::clipboard()->text(), draft);
    if (loseAnswerAck) {
      QVERIFY(!item(window, "discardDetachedAnswerDraft_" + questionID)
                   ->isEnabled());
      panel.discardDetachedAnswerDraft(originalKey);
      QCOMPARE(panel.detachedAnswerDrafts().size(), 1);
      QVERIFY(panel.uncertain());
      QVERIFY(!panel.editProject(true));
      QSignalSpy recoveryClosed(dialog, SIGNAL(closed()));
      QVERIFY(QMetaObject::invokeMethod(dialog, "close"));
      QTRY_COMPARE(recoveryClosed.count(), 1);
      workspace->returnToEditor();
      auto *abandonButton = item(window, "abandonButton");
      QVERIFY(abandonButton && abandonButton->isVisible() &&
              abandonButton->isEnabled());
      abandonButton->forceActiveFocus();
      QTest::keyClick(window, Qt::Key_Space);
      auto *abandon = window->findChild<QObject *>("abandonDialog");
      QVERIFY(abandon);
      QTRY_VERIFY(abandon->property("visible").toBool());
      QSignalSpy abandonClosed(abandon, SIGNAL(closed()));
      QVERIFY(QMetaObject::invokeMethod(abandon, "accept"));
      QTRY_COMPARE(abandonClosed.count(), 1);
      QTRY_VERIFY(!panel.uncertain());
      QVERIFY(!workspace->closeAllowed());
      QVERIFY(click(window, "recoverAnswerDraftsButton"));
      QTRY_VERIFY(dialog->property("opened").toBool());
    }
    auto *discard = item(window, "discardDetachedAnswerDraft_" + questionID);
    QVERIFY(discard && discard->isVisible() && discard->isEnabled());
    discard->forceActiveFocus();
    QTest::keyClick(window, Qt::Key_Space);
    QTRY_VERIFY(!dialog->property("visible").toBool());
    QVERIFY(workspace->closeAllowed());
    QVERIFY(!item(window, "recoverAnswerDraftsButton")->isVisible());
    QCOMPARE(fixture.mutations.size(),
             1); // Recovery never invokes the owner API.
    QSignalSpy saved(&client, &ApiClient::mutationSucceeded);
    QVERIFY(panel.editProject(true));
    panel.field("name", "New unrelated project");
    panel.field("root_path", "/fictional/workspace");
    QVERIFY(panel.save());
    QTRY_COMPARE(saved.count(), 1);
    QCOMPARE(fixture.mutations.size(), 2);
    client.stop();
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
    if (const auto capture = qEnvironmentVariable("ARTEL_STATE_SCREENSHOT");
        !capture.isEmpty()) {
      QTest::qWait(100);
      QVERIFY(window->grabWindow().save(capture + "-unknown.png"));
    }
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
    const auto id = create ? QString("55555555-5555-4555-8555-555555555555")
                           : (project ? fixture.project : fixture.task);
    const bool taskCreate = !project && create;
    const QString question = "66666666-6666-4666-8666-666666666666";
    const QJsonObject answered{{"id", question},
                               {"task_id", fixture.task},
                               {"delivery_binding_id", fixture.project},
                               {"pi_session_id", fixture.instance},
                               {"text", "Task A question"},
                               {"status", "answered"},
                               {"created_at", "2026-11-17T12:00:00Z"},
                               {"answer_message_id", id},
                               {"answer_text", "Task A answer"},
                               {"answer_status", "observed"},
                               {"answered_at", "2026-11-17T12:01:00Z"}};
    QPointer<QTcpSocket> held, heldHistory, heldPage, heldStatus;
    bool holdHistory = false, holdQuestions = false;
    QByteArray newTaskPage;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (holdHistory && request.startsWith("GET /v1/events?")) {
        heldHistory = socket;
      } else if (!request.startsWith("GET")) {
        held = socket;
      } else if (taskCreate && request.contains("/questions?")) {
        const bool oldTask =
            request.startsWith("GET /v1/tasks/" + fixture.task.toUtf8() + "/");
        if (oldTask && holdQuestions) {
          (request.contains("&ids=") ? heldStatus : heldPage) = socket;
          return;
        }
        if (!oldTask)
          newTaskPage = request;
        UiFixture::respond(
            socket, 200,
            QJsonDocument(
                QJsonObject{{"questions",
                             oldTask ? QJsonArray{answered} : QJsonArray{}},
                            {"next_after", oldTask ? 1 : 0},
                            {"has_more", false}})
                .toJson());
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    client.reload();
    QTRY_VERIFY(panel.writable());
    QVERIFY(panel.selectProject(fixture.project));
    QVERIFY(panel.selectTask(fixture.task));
    QTRY_VERIFY(!panel.history().isEmpty());
    if (taskCreate) {
      QTRY_COMPARE(panel.questions().size(), 1);
      QCOMPARE(
          panel.questions().first().toMap().value("answer_text").toString(),
          QString("Task A answer"));
    }
    bool selectionClearedAtReply = false, questionsClearedAtReply = false;
    connect(&client, &ApiClient::mutationSucceeded, &panel, [&] {
      selectionClearedAtReply = panel.task().isEmpty() &&
                                panel.history().isEmpty() &&
                                !panel.historyLoading();
      questionsClearedAtReply = panel.questions().isEmpty() &&
                                !panel.questionsLoading() &&
                                !client.questionStatusesLoading();
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
    } else if (taskCreate) {
      holdQuestions = true;
      client.loadQuestions(fixture.task, 1);
      client.refreshQuestionStatuses(fixture.task, {question});
      QTRY_VERIFY(heldPage && heldStatus);
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
    } else if (taskCreate) {
      QVERIFY(questionsClearedAtReply);
      QTRY_VERIFY(!heldPage ||
                  heldPage->state() == QAbstractSocket::UnconnectedState);
      QTRY_VERIFY(!heldStatus ||
                  heldStatus->state() == QAbstractSocket::UnconnectedState);
      QCOMPARE(panel.task().value("id").toString(), id);
      QTRY_VERIFY(newTaskPage.startsWith("GET /v1/tasks/" + id.toUtf8() +
                                         "/questions?"));
      QVERIFY(newTaskPage.contains("&after=0&"));
      QTRY_VERIFY(!panel.questionsLoading());
      QVERIFY(panel.questions().isEmpty());
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
    if (taskCreate) {
      QCOMPARE(panel.task().value("id").toString(), id);
      QVERIFY(panel.questions().isEmpty());
    }
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
  void manualPresentationAndScreenshots() {
    UiFixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    Panel panel(&client);
    Appearance appearance(fixture.directory.path(),
                          fixture.directory.filePath("language"));
    QQmlApplicationEngine engine;
    QCoreApplication::installTranslator(&appearance);
    connect(&appearance, &Appearance::changed, &engine,
            &QQmlEngine::retranslate);
    connect(&appearance, &Appearance::changed, &panel, &Panel::changed);
    auto *window = load(engine, panel, appearance);
    QVERIFY(window);
    client.reload();
    QTRY_VERIFY(panel.writable());
    const auto prefix = qEnvironmentVariable("ARTEL_MANUAL_SCREENSHOT");
    auto capture = [&](const QString &state) {
      QTest::qWait(100);
      const auto image = window->grabWindow();
      return !image.isNull() &&
             image.save(prefix.isEmpty()
                            ? fixture.directory.filePath(state + ".png")
                            : prefix + "-" + state + ".png");
    };
    for (const auto &language : {QString("en"), QString("ru")}) {
      writeFile(fixture.directory.filePath("language"), language.toUtf8());
      appearance.refresh();
      QTRY_COMPARE(appearance.language(), language);
      QVERIFY(panel.selectProject(""));
      QVERIFY(capture(language + "-projects"));
      QVERIFY(panel.selectProject(fixture.project));
      QVERIFY(panel.selectTask(fixture.task));
      QTRY_VERIFY(!panel.history().isEmpty());
      QTest::qWait(50);
      QVERIFY(!item(window, "startButton")->isVisible());
      QVERIFY(!item(window, "doneButton")->isVisible());
      QVERIFY(!item(window, "reopenButton")->isVisible());
      QVERIFY(item(window, "cancelTaskButton")->isVisible());
      QVERIFY(!item(window, "up_" + fixture.task)->isVisible());
      QVERIFY(!item(window, "down_" + fixture.task)->isVisible());
      auto *description = item(window, "taskDescription");
      QVERIFY(description->property("readOnly").toBool());
      QVERIFY(description->property("selectByMouse").toBool());
      QCOMPARE(description->property("textFormat").toInt(),
               static_cast<int>(Qt::PlainText));
      QCOMPARE(panel.history().first().toMap().value("timestamp").toString(),
               QString("2026-11-17T12:00:00Z"));
      QCOMPARE(
          panel.history().first().toMap().value("displayTimestamp").toString(),
          QLocale().toString(
              QDateTime::fromString("2026-11-17T12:00:00Z", Qt::ISODate)
                  .toLocalTime(),
              QLocale::ShortFormat));
      QVERIFY(capture(language + "-workspace"));
      QVERIFY(click(window, "historyChanges_25"));
      QVERIFY(capture(language + "-history"));
      QVERIFY(click(window, "editTaskButton"));
      QVERIFY(enter(window, "descriptionField",
                    "Literal <b>draft</b> · Кириллица · 😀"));
      QVERIFY(
          enter(window, "criteriaField", "Keep manual decisions explicit."));
      const auto draft = panel.editor();
      const auto base = panel.task();
      QVERIFY(capture(language + "-editor"));
      QVERIFY(click(window, "teamNavButton"));
      QTest::keyClick(window, Qt::Key_S, Qt::ControlModifier);
      QTest::keyClick(window, Qt::Key_Escape);
      QCOMPARE(panel.editor(), draft);
      QCOMPARE(panel.task(), base);
      QVERIFY(fixture.mutations.isEmpty());
      QVERIFY(click(window, "returnToEditorButton"));
      QCOMPARE(panel.editor(), draft);
      QCOMPARE(panel.task(), base);
      // Snapshot refresh must not overwrite focused text or advance its base.
      item(window, "descriptionField")->forceActiveFocus();
      panel.reload();
      QTRY_VERIFY(!client.loading());
      QCOMPARE(panel.editor(), draft);
      QCOMPARE(item(window, "descriptionField")->property("text").toString(),
               draft.value("description").toString());
      panel.discard();
      QVERIFY(!panel.dirty());
    }
    QCoreApplication::removeTranslator(&appearance);
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
