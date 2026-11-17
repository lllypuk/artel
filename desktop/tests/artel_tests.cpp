#include "apiclient.h"
#include "appearance.h"
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
    for (bool project : {false, true})
      for (bool create : {false, true})
        for (bool retry : {false, true}) {
          const auto name =
              QString("%1-%2-%3")
                  .arg(project ? "project" : "task", create ? "create" : "edit",
                       retry ? "retry" : "reply");
          QTest::newRow(qPrintable(name)) << project << create << retry;
        }
  }
  void retainedEditorAdoptsConfirmedIdentity() {
    QFETCH(bool, project);
    QFETCH(bool, create);
    QFETCH(bool, retry);
    UiFixture fixture;
    QPointer<QTcpSocket> held;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (!request.startsWith("GET"))
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
    UiFixture::respond(
        held, 200,
        QJsonDocument(QJsonObject{{"id", id}, {"version", version}}).toJson());
    held = nullptr;
    QTRY_VERIFY(!client.pending() && !panel.uncertain() && !client.loading());
    QCOMPARE(panel.editor(), retained);
    QVERIFY(panel.dirty());
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
