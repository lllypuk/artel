#include "apiclient.h"
#include "appearance.h"
#include "panel.h"
#include "workspacecontroller.h"
#include <QFile>
#include <QInputMethodEvent>
#include <QJsonDocument>
#include <QNetworkProxy>
#include <QProcess>
#include <QQmlApplicationEngine>
#include <QQuickItem>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QTcpServer>
#include <QTcpSocket>
#include <QTemporaryDir>
#include <QUuid>
#include <QtTest>
#include <memory>
#include <signal.h>
#include <sys/prctl.h>
#include <unistd.h>

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
  // Network replies can expose controls before the next layout/render pass.
  // Let Qt polish their hit regions before issuing an actual mouse click.
  QTest::qWait(30);
  auto *control = item(window, name);
  if (!control || !control->isVisible() || !control->isEnabled())
    return false;
  QTest::mouseClick(
      window, Qt::LeftButton, {},
      control
          ->mapToScene(
              QPointF(control->width() / 2,
                      name.startsWith("task_") ? 20.0 : control->height() / 2))
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
struct Daemon {
  QTemporaryDir dir{QDir::homePath() + "/daemon-XXXXXX"};
  QProcess process;
  QUrl endpoint;
  QString tokenPath() const { return dir.filePath("private/owner.token"); }
  ~Daemon() { stop(); }
  void stop() {
    if (process.state() == QProcess::NotRunning)
      return;
    process.terminate();
    if (!process.waitForFinished(5000)) {
      process.kill();
      process.waitForFinished(5000);
    }
  }
  bool start() {
    auto env = QProcessEnvironment::systemEnvironment();
    env.insert("HOME", dir.path());
    process.setProcessEnvironment(env);
    // Even a fatal Qt assertion or watchdog kill must not orphan the daemon.
    const auto parent = getpid();
    process.setChildProcessModifier([parent] {
      if (prctl(PR_SET_PDEATHSIG, SIGTERM) != 0 || getppid() != parent)
        _exit(125);
    });
    process.start(qEnvironmentVariable("ARTEL_TEST_DAEMON"),
                  {"--data-dir", dir.filePath("data"), "--token-file",
                   tokenPath(), "--listen", "127.0.0.1:0"});
    if (!process.waitForStarted(5000))
      return false;
    QByteArray output;
    QElapsedTimer timer;
    timer.start();
    while (timer.elapsed() < 5000) {
      process.waitForReadyRead(50);
      output += process.readAllStandardOutput();
      if (output.contains("arteld: listening ") && output.contains('\n')) {
        endpoint = QUrl("http://" +
                        QString::fromUtf8(output).trimmed().section(' ', -1));
        return ApiClient::validEndpoint(endpoint);
      }
    }
    return false;
  }
};
// This independent HTTP oracle checks the wire DTO, not the viewmodel's cache.
struct Oracle {
  QNetworkAccessManager network;
  QUrl endpoint;
  QByteArray token;
  Oracle(const QUrl &url, const QString &file) : endpoint(url) {
    network.setProxy(QNetworkProxy::NoProxy);
    QFile credential(file);
    if (credential.open(QIODevice::ReadOnly))
      token = credential.readAll().trimmed();
  }
  QNetworkReply *send(const QByteArray &method, const QString &path,
                      const QJsonObject &body = {},
                      const QByteArray &key = {}) {
    QNetworkRequest request(endpoint.resolved(QUrl(path)));
    request.setTransferTimeout(5000);
    request.setRawHeader("Authorization", "Bearer " + token);
    request.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
    request.setAttribute(QNetworkRequest::RedirectPolicyAttribute,
                         QNetworkRequest::ManualRedirectPolicy);
    if (method != "GET")
      request.setRawHeader(
          "Idempotency-Key",
          key.isEmpty()
              ? QUuid::createUuid().toString(QUuid::WithoutBraces).toUtf8()
              : key);
    return network.sendCustomRequest(
        request, method,
        method == "GET" ? QByteArray{} : QJsonDocument(body).toJson());
  }
  QJsonObject get(const QString &path) {
    auto *reply = send("GET", path);
    QElapsedTimer timer;
    timer.start();
    while (!reply->isFinished() && timer.elapsed() < 6000)
      QTest::qWait(10);
    const bool ok =
        reply->isFinished() &&
        reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() ==
            200;
    const auto result = QJsonDocument::fromJson(reply->readAll()).object();
    reply->abort();
    reply->deleteLater();
    return ok ? result : QJsonObject{};
  }
};
QJsonObject find(const QJsonArray &rows, const QString &id) {
  for (const auto &row : rows)
    if (row.toObject().value("id").toString() == id)
      return row.toObject();
  return {};
}
struct Window {
  ApiClient client;
  Panel panel{&client};
  WorkspaceController workspace{&panel};
  Appearance appearance;
  QQmlApplicationEngine engine;
  QQuickWindow *window = nullptr;
  Window(const Daemon &daemon)
      : appearance(daemon.dir.path(), daemon.dir.filePath("language")) {
    if (!client.configure(daemon.endpoint, daemon.tokenPath()))
      return;
    engine.setInitialProperties(
        {{"workspace", QVariant::fromValue(&workspace)},
         {"panel", QVariant::fromValue(&panel)},
         {"appearance", QVariant::fromValue(&appearance)}});
    engine.load(QUrl("qrc:/Main.qml"));
    if (engine.rootObjects().size() == 1)
      window = qobject_cast<QQuickWindow *>(engine.rootObjects().first());
    client.reload();
  }
};
// Forward real requests to arteld, but discard the first successful mutation
// response only AFTER its commit. Capture retries to prove identical key/body.
class LostResponse : public QTcpServer {
public:
  Oracle &upstream;
  QList<QByteArray> mutations;
  QList<int> statuses;
  explicit LostResponse(Oracle &oracle) : upstream(oracle) {
    connect(this, &QTcpServer::newConnection, this, [this] {
      while (hasPendingConnections()) {
        auto *socket = nextPendingConnection();
        auto bytes = std::make_shared<QByteArray>();
        connect(socket, &QTcpSocket::disconnected, socket,
                &QObject::deleteLater);
        connect(socket, &QTcpSocket::readyRead, this, [this, socket, bytes] {
          *bytes += socket->readAll();
          const auto split = bytes->indexOf("\r\n\r\n");
          if (split < 0)
            return;
          int length = 0;
          QByteArray key;
          for (const auto &line : bytes->left(split).split('\n')) {
            if (line.toLower().startsWith("content-length:"))
              length = line.mid(15).trimmed().toInt();
            if (line.toLower().startsWith("idempotency-key:"))
              key = line.mid(16).trimmed();
          }
          if (bytes->size() < split + 4 + length ||
              socket->property("sent").toBool())
            return;
          socket->setProperty("sent", true);
          const auto first = bytes->split('\n').first().split(' ');
          const bool mutation = first[0] != "GET";
          const bool stream = first[1].startsWith("/v1/stream?");
          if (mutation)
            mutations.append(*bytes);
          auto *reply = upstream.send(
              first[0], QString::fromUtf8(first[1]),
              QJsonDocument::fromJson(bytes->mid(split + 4)).object(), key);
          if (stream) {
            socket->write(
                "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n"
                "Connection: close\r\n\r\n");
            connect(reply, &QNetworkReply::readyRead, socket,
                    [reply, socket] { socket->write(reply->readAll()); });
            connect(socket, &QTcpSocket::disconnected, reply,
                    [reply] { reply->abort(); });
          }
          connect(
              reply, &QNetworkReply::finished, socket,
              [this, reply, socket, mutation, stream] {
                const int status =
                    reply->attribute(QNetworkRequest::HttpStatusCodeAttribute)
                        .toInt();
                if (mutation)
                  statuses.append(status);
                const auto body =
                    reply->isOpen() ? reply->readAll() : QByteArray{};
                if (!stream && (!mutation || mutations.size() > 1))
                  socket->write("HTTP/1.1 " + QByteArray::number(status) +
                                " OK\r\nContent-Type: application/json\r\n"
                                "Connection: close\r\nContent-Length: " +
                                QByteArray::number(body.size()) + "\r\n\r\n" +
                                body);
                socket->disconnectFromHost();
                reply->deleteLater();
              });
        });
      }
    });
  }
};
} // namespace

class Acceptance : public QObject {
  Q_OBJECT
private slots:
  void initTestCase() { QQuickStyle::setStyle("Material"); }
  void init() { QTest::failOnWarning(); }
  // Invoked by observer_acceptance.py while a real isolated Pi is alive.
  void observerExternal() {
    QVERIFY2(
        !qEnvironmentVariableIsEmpty("ARTEL_OBSERVER_ENDPOINT"),
        "Run observer acceptance through tests/e2e/observer_acceptance.py");
    Daemon daemon;
    daemon.endpoint = QUrl(qEnvironmentVariable("ARTEL_OBSERVER_ENDPOINT"));
    const auto privateDir = daemon.dir.filePath("private");
    QVERIFY(QDir().mkpath(privateDir));
    // Do not rely on the invoking shell's umask: the client requires 0700.
    QVERIFY(QFile::setPermissions(
        privateDir, QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner));
    QVERIFY(QFile::copy(qEnvironmentVariable("ARTEL_OBSERVER_OWNER_TOKEN"),
                        daemon.tokenPath()));
    Oracle oracle(daemon.endpoint, daemon.tokenPath());
    const auto expected =
        QJsonDocument::fromJson(qgetenv("ARTEL_OBSERVER_EXPECTED")).object();
    const auto baseline = oracle.get("/v1/snapshot");
    QVERIFY(!baseline.isEmpty());
    const auto before = oracle.get("/v1/observers");
    Window ui(daemon);
    QVERIFY(ui.window);
    QCOMPARE(ui.workspace.route(), QString("team"));
    QVERIFY(item(ui.window, "teamOverview")->isVisible());
    QTRY_VERIFY_WITH_TIMEOUT(ui.panel.writable(), 5000);
    auto *observers = ui.panel.observers();
    QTRY_VERIFY_WITH_TIMEOUT(observers->online(), 5000);
    auto matches = [&] {
      for (const auto &value : observers->rows()) {
        const auto row = value.toMap();
        if (row.value("runtime_id").toString() !=
            expected.value("runtime_id").toString())
          continue;
        for (auto it = expected.begin(); it != expected.end(); ++it)
          if (row.value(it.key()) != it.value().toVariant())
            return false;
        return true;
      }
      return false;
    };
    QTRY_VERIFY_WITH_TIMEOUT(matches(), 5000);
    int total = 0, unbound = 0;
    for (const auto &value : before.value("observers").toArray()) {
      const auto record = value.toObject();
      if (record.value("revoked").toBool())
        continue;
      if (record.value("binding").toObject().isEmpty())
        ++unbound;
      else
        ++total;
    }
    QTRY_COMPARE(ui.workspace.team()->total(), total);
    QCOMPARE(ui.workspace.team()->unbound(), unbound);
    QCOMPARE(ui.workspace.team()->shown(), total);
    // One observer can have many candidates/history runtimes, but only its
    // current non-revoked binding belongs to the whole-team roster.
    const auto selectedProject = ui.panel.project();
    const auto selectedTask = ui.panel.task();
    ui.workspace.team()->setSearch("definitely-no-fictional-observer-match");
    QCOMPARE(ui.workspace.team()->shown(), 0);
    QCOMPARE(ui.workspace.team()->total(), total);
    ui.workspace.team()->setSearch({});
    QCOMPARE(ui.workspace.team()->shown(), total);
    QVERIFY(click(ui.window, "projectsNavButton"));
    QCOMPARE(ui.workspace.route(), QString("projects"));
    QVERIFY(click(ui.window, "teamNavButton"));
    QCOMPARE(ui.panel.project(), selectedProject);
    QCOMPARE(ui.panel.task(), selectedTask);
    QVERIFY(click(ui.window, "connectionsNavButton"));
    QTRY_VERIFY(ui.workspace.connectionsOpen());
    QVERIFY(item(ui.window, "observerList"));
    QVERIFY(item(ui.window, "observerList")->isVisible());
    const auto projection = oracle.get("/v1/observers");
    bool oracleMatch = false;
    for (const auto &observer : projection.value("observers").toArray())
      for (const auto &value :
           observer.toObject().value("runtimes").toArray()) {
        const auto runtime = value.toObject();
        if (runtime.value("registration").toObject().value("runtime_id") !=
            expected.value("runtime_id"))
          continue;
        oracleMatch = true;
        for (auto it = expected.begin(); it != expected.end(); ++it)
          if (it.key() != "runtime_id" && it.key() != "navigable")
            QCOMPARE(runtime.value(it.key()), it.value());
      }
    QVERIFY(oracleMatch);
    // Polling/filters/routes must not write either the manual domain or owner
    // observer controls. Heartbeats may legitimately update runtime metadata.
    QCOMPARE(oracle.get("/v1/snapshot"), baseline);
    for (const auto &value : before.value("observers").toArray()) {
      const auto previous = value.toObject();
      bool found = false;
      for (const auto &current : projection.value("observers").toArray()) {
        const auto record = current.toObject();
        if (record.value("observer_id") != previous.value("observer_id"))
          continue;
        found = true;
        for (const auto &key : {"version", "generation", "binding", "revoked"})
          QCOMPARE(record.value(key), previous.value(key));
      }
      QVERIFY(found);
    }
    QVERIFY(!ui.panel.dirty());
    QVERIFY(!ui.panel.uncertain());
  }
  // Invoked by questions_pty.py against the same real daemon and original Pi.
  void ownerQuestionRoundTrip() {
    const auto endpoint = qEnvironmentVariable("ARTEL_QUESTION_ENDPOINT");
    const auto token = qEnvironmentVariable("ARTEL_QUESTION_OWNER_TOKEN");
    const auto project = qEnvironmentVariable("ARTEL_QUESTION_PROJECT");
    const auto task = qEnvironmentVariable("ARTEL_QUESTION_TASK");
    const auto questionText = qEnvironmentVariable("ARTEL_QUESTION_TEXT");
    const auto answerText = qEnvironmentVariable("ARTEL_QUESTION_ANSWER");
    const auto commission = qEnvironmentVariable("ARTEL_QUESTION_COMMISSION");
    QVERIFY2(!endpoint.isEmpty() && !token.isEmpty() && !project.isEmpty() &&
                 !task.isEmpty() && !questionText.isEmpty() &&
                 !answerText.isEmpty(),
             "Run ownerQuestionRoundTrip through questions_pty.py");
    QVERIFY((!commission.isEmpty() && commission.contains(task)) ||
            commission.isEmpty());
    Daemon daemon;
    daemon.endpoint = QUrl(endpoint);
    const auto privateDir = daemon.dir.filePath("private");
    QVERIFY(QDir().mkpath(privateDir));
    QVERIFY(QFile::setPermissions(
        privateDir, QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner));
    QVERIFY(QFile::copy(token, daemon.tokenPath()));
    Oracle oracle(daemon.endpoint, daemon.tokenPath());
    const auto baseline = oracle.get("/v1/snapshot");
    const auto baselineTask =
        find(baseline.value("tasks").toArray(), task).value("state").toString();
    QVERIFY(!baselineTask.isEmpty());
    Window ui(daemon);
    QVERIFY(ui.window);
    QTRY_VERIFY_WITH_TIMEOUT(ui.panel.writable(), 5000);
    QVERIFY(click(ui.window, "projectsNavButton"));
    QVERIFY(ui.panel.selectProject(project));
    QVERIFY(ui.panel.selectTask(task));
    QTRY_COMPARE_WITH_TIMEOUT(
        item(ui.window, "taskUuid")->property("text").toString(), task, 5000);
    if (!commission.isEmpty()) {
      QTRY_VERIFY_WITH_TIMEOUT(
          ui.panel.assignment().value("canSend").toBool() ||
              ui.panel.assignment().value("available").toBool(),
          15000);
      QVERIFY(enter(ui.window, "commissionField", commission));
      QVERIFY(click(ui.window, "sendCommissionButton"));
      QTRY_COMPARE_WITH_TIMEOUT(ui.panel.messages().size(), 1, 10000);
    }
    auto question = [&]() -> QVariantMap {
      for (const auto &value : ui.panel.questions()) {
        const auto row = value.toMap();
        if (row.value("text").toString() == questionText)
          return row;
      }
      return {};
    };
    // The selected, initially empty task polls in the new question without a
    // task reselection. The original tool turn must settle before answering.
    QTRY_VERIFY_WITH_TIMEOUT(!question().isEmpty(), 20000);
    const auto historyReason =
        qEnvironmentVariable("ARTEL_QUESTION_HISTORY_REASON");
    if (!historyReason.isEmpty()) {
      QCOMPARE(question().value("status").toString(), QString("answered"));
      QCOMPARE(question().value("answer_text").toString(), answerText);
      QCOMPARE(question().value("answer_status").toString(),
               QString("unknown"));
      QCOMPARE(question().value("answer_block_reason").toString(),
               historyReason);
      const auto id = question().value("id").toString();
      const auto *warning = item(ui.window, "answerBlockReason_" + id);
      QVERIFY(warning && warning->isVisible());
      QVERIFY(!ui.panel.assignment().value("enabled").toBool());
      return;
    }
    const auto tracePath = qEnvironmentVariable("ARTEL_QUESTION_TRACE");
    if (!tracePath.isEmpty()) {
      auto toolTurnSettled = [&] {
        QFile trace(tracePath);
        if (!trace.open(QIODevice::ReadOnly))
          return false;
        const auto bytes = trace.readAll();
        const auto tool = bytes.lastIndexOf("\"event\":\"tool_execution_end\"");
        const auto settled = bytes.lastIndexOf("\"event\":\"agent_settled\"");
        return tool >= 0 && settled > tool;
      };
      QTRY_VERIFY_WITH_TIMEOUT(toolTurnSettled(), 15000);
    }
    QCOMPARE(question().value("status").toString(), QString("open"));
    const auto questionID = question().value("id").toString();
    QVERIFY(enter(ui.window, "answerField_" + questionID, answerText));
    const auto stopDirectory =
        qEnvironmentVariable("ARTEL_QUESTION_STOP_DIRECTORY");
    if (!stopDirectory.isEmpty()) {
      QFile request(stopDirectory + "/stop-request");
      QVERIFY(request.open(QIODevice::WriteOnly));
      request.write(questionID.toUtf8());
      request.close();
      QTRY_VERIFY_WITH_TIMEOUT(QFile::exists(stopDirectory + "/writer-stopped"),
                               20000);
      QTRY_VERIFY_WITH_TIMEOUT(!ui.panel.assignment().value("ready").toBool(),
                               10000);
    }
    auto *answerButton = item(ui.window, "answerButton_" + questionID);
    QVERIFY(answerButton && answerButton->isEnabled());
    answerButton->forceActiveFocus();
    QTest::keyClick(ui.window, Qt::Key_Space);
    QTRY_COMPARE_WITH_TIMEOUT(question().value("status").toString(),
                              QString("answered"), 10000);
    QTRY_COMPARE_WITH_TIMEOUT(question().value("answer_text").toString(),
                              answerText, 5000);
    const bool expectObserved =
        qEnvironmentVariable("ARTEL_QUESTION_EXPECT_OBSERVED") == "1";
    QTRY_COMPARE_WITH_TIMEOUT(
        question().value("answer_status").toString(),
        expectObserved ? QString("observed") : QString("stored"), 20000);
    QVERIFY(item(ui.window, "answerStatus_" + questionID)->isVisible());
    const auto instance = baseline.value("instance_id").toString();
    const auto wire =
        oracle.get("/v1/tasks/" + task + "/questions?instance_id=" + instance +
                   "&after=0&limit=5");
    const auto expectedTotal =
        qEnvironmentVariableIntValue("ARTEL_QUESTION_TOTAL") <= 0
            ? 1
            : qEnvironmentVariableIntValue("ARTEL_QUESTION_TOTAL");
    QCOMPARE(wire.value("questions").toArray().size(), expectedTotal);
    int pairs = 0;
    for (const auto &value : wire.value("questions").toArray()) {
      const auto row = value.toObject();
      if (row.value("text").toString() == questionText) {
        ++pairs;
        QCOMPARE(row.value("answer_text").toString(), answerText);
        QCOMPARE(row.value("answer_status").toString(),
                 expectObserved ? QString("observed") : QString("stored"));
      }
    }
    QCOMPARE(pairs, 1);
    const auto after = oracle.get("/v1/snapshot");
    QCOMPARE(
        find(after.value("tasks").toArray(), task).value("state").toString(),
        baselineTask);
    QVERIFY(!ui.panel.uncertain());
  }
  void restartConcurrencyAndRecovery() {
    Daemon daemon;
    QVERIFY(daemon.dir.isValid());
    QVERIFY(daemon.start());
    const auto probe = qEnvironmentVariable("ARTEL_E2E_FAILURE_PROBE");
    if (!probe.isEmpty()) {
      QFile output(probe);
      QVERIFY(output.open(QIODevice::WriteOnly));
      output.write(QByteArray::number(daemon.process.processId()) + "\n" +
                   daemon.dir.path().toUtf8() + "\n");
      output.close();
      QVERIFY2(false, "Intentional assertion failure for cleanup verification");
    }
    Oracle oracle(daemon.endpoint, daemon.tokenPath());
    auto ui = std::make_unique<Window>(daemon);
    QVERIFY(ui->window);
    QTRY_VERIFY(ui->panel.writable());
    QCOMPARE(ui->workspace.route(), QString("team"));
    QTRY_VERIFY(ui->panel.observers()->online());
    QCOMPARE(ui->workspace.team()->total(), 0);
    QCOMPARE(ui->workspace.team()->unbound(), 0);
    QVERIFY(item(ui->window, "teamEmptyState")->isVisible());
    QCOMPARE(oracle.get("/v1/snapshot")["event_seq"].toInteger(), 0);
    QVERIFY(click(ui->window, "projectsNavButton"));
    QCOMPARE(ui->workspace.route(), QString("projects"));
    QTest::qWait(100);
    QVERIFY(QDir(daemon.dir.path()).mkdir("workspace"));
    QVERIFY(click(ui->window, "createProjectButton"));
    QVERIFY(enter(ui->window, "nameField", "Acceptance project"));
    QVERIFY(enter(ui->window, "rootField", daemon.dir.filePath("workspace")));
    QVERIFY(click(ui->window, "saveButton"));
    QTRY_COMPARE(ui->panel.projects().size(), 1);
    QTRY_VERIFY(!ui->panel.dirty());
    const auto project = ui->panel.projects().first().toMap()["id"].toString();
    QTRY_VERIFY(item(ui->window, "project_" + project));
    QVERIFY(click(ui->window, "project_" + project));
    QTRY_COMPARE(ui->panel.project().value("id").toString(), project);
    QStringList ids;
    for (int i = 0; i < 3; ++i) {
      QVERIFY(click(ui->window, "createTaskButton"));
      QTRY_COMPARE(ui->panel.editorKind(), QString("task"));
      QVERIFY(enter(ui->window, "nameField", QString("Task %1").arg(i + 1)));
      QTRY_VERIFY(ui->panel.saveable());
      QVERIFY(click(ui->window, "saveButton"));
      QTRY_COMPARE(ui->panel.tasks().size(), i + 1);
      QTRY_VERIFY(!ui->panel.dirty());
      for (const auto &row : ui->panel.tasks()) {
        const auto id = row.toMap()["id"].toString();
        if (!ids.contains(id))
          ids.append(id);
      }
    }
    QVERIFY(click(ui->window, "task_" + ids[0]));
    QVERIFY(click(ui->window, "editTaskButton"));
    QVERIFY(enter(ui->window, "criteriaField", "Проверено вручную владельцем"));
    QVERIFY(click(ui->window, "saveButton"));
    QTRY_VERIFY(!ui->panel.dirty());
    for (const auto &id : ids) {
      QTRY_VERIFY(item(ui->window, "queue_" + id));
      QVERIFY(click(ui->window, "queue_" + id));
      QTRY_VERIFY(!ui->client.pending() && !ui->client.loading());
    }
    QTRY_VERIFY(item(ui->window, "up_" + ids[2]));
    QVERIFY(click(ui->window, "up_" + ids[2]));
    QTRY_VERIFY(!ui->client.pending() && !ui->client.loading());
    QVERIFY(click(ui->window, "task_" + ids[0]));
    QVERIFY(click(ui->window, "startButton"));
    QTRY_COMPARE(ui->panel.task()["state"].toString(), QString("in_progress"));
    QVERIFY(click(ui->window, "doneButton"));
    QTRY_COMPARE(ui->panel.task()["state"].toString(), QString("done"));
    auto before = oracle.get("/v1/snapshot");
    QVERIFY(!before.isEmpty());
    const auto instance = before["instance_id"].toString();
    QCOMPARE(
        before["queues"].toArray().first().toObject()["task_ids"].toArray(),
        QJsonArray({ids[2], ids[1]}));
    QCOMPARE(find(before["tasks"].toArray(), ids[0])["acceptance_criteria"]
                 .toString(),
             QString("Проверено вручную владельцем"));
    const auto historyPath =
        "/v1/events?instance_id=" + instance + "&after=0&limit=500";
    auto history = oracle.get(historyPath)["events"].toArray();
    QCOMPARE(history.size(), 11);
    QStringList transitions;
    for (const auto &event : history) {
      const auto audit = event.toObject();
      QCOMPARE(audit["source"].toString(), QString("owner_manual"));
      if (audit["action"] == "task.transition" && audit["task_id"] == ids[0])
        transitions.append(audit["after"]
                               .toObject()["task:" + ids[0]]
                               .toObject()["state"]
                               .toString());
    }
    QCOMPARE(transitions, QStringList({"in_progress", "done"}));

    // Destroy the actual QML engine and client, not merely hide the window.
    QVERIFY(ui->window->close());
    ui.reset();
    QCOMPARE(daemon.process.state(), QProcess::Running);
    QCOMPARE(oracle.get("/v1/snapshot"), before);
    ui = std::make_unique<Window>(daemon);
    QVERIFY(ui->window);
    QTRY_VERIFY(ui->panel.writable());
    QCOMPARE(ui->client.instance(), instance);
    QCOMPARE(ui->workspace.route(), QString("team"));
    QVERIFY(click(ui->window, "projectsNavButton"));
    QCOMPARE(oracle.get("/v1/snapshot"), before);
    QVERIFY(ui->panel.selectProject(project));
    QVERIFY(ui->panel.selectTask(ids[0]));
    QCOMPARE(ui->panel.task()["state"].toString(), QString("done"));
    QCOMPARE(ui->panel.tasks().size(), 3);
    daemon.stop();
    QCOMPARE(daemon.process.exitCode(), 0);
    QTRY_VERIFY(!ui->client.online());
    QVERIFY(!item(ui->window, "createTaskButton")->isEnabled());
    QVERIFY(daemon.start());
    oracle.endpoint = daemon.endpoint;
    QCOMPARE(oracle.get("/v1/snapshot"), before);
    // :0 may change port, so explicitly reconnect to the newly advertised
    // endpoint.
    QVERIFY(ui->client.configure(daemon.endpoint, daemon.tokenPath()));
    ui->client.reload();
    QTRY_VERIFY(ui->panel.writable());
    QCOMPARE(ui->client.instance(), instance);
    QCOMPARE(ui->client.sequence(), before["event_seq"].toInteger());
    QCOMPARE(ui->panel.task()["version"].toLongLong(),
             find(before["tasks"].toArray(), ids[0])["version"].toInteger());

    // Both editors capture the same version. SSE refresh must not rebase draft.
    QVERIFY(click(ui->window, "editTaskButton"));
    QVERIFY(enter(ui->window, "descriptionField", "Unsaved local text"));
    const auto oldVersion = ui->panel.task()["version"].toLongLong();
    ApiClient other;
    QVERIFY(other.configure(daemon.endpoint, daemon.tokenPath()));
    other.reload();
    QTRY_VERIFY(other.online());
    QVERIFY(other.save(
        "PATCH", "/v1/tasks/" + ids[0],
        {{"expected_version", oldVersion}, {"description", "Other editor"}}));
    QTRY_VERIFY(!other.pending() && !other.loading());
    // No explicit reload: invalidation/replay must update the visible object.
    QTRY_COMPARE(ui->panel.task()["description"].toString(),
                 QString("Other editor"));
    QCOMPARE(ui->panel.editor()["description"].toString(),
             QString("Unsaved local text"));
    QVERIFY(click(ui->window, "saveButton"));
    QTRY_VERIFY(ui->panel.conflict());
    QVERIFY(item(ui->window, "rebaseButton")->isVisible());
    QCOMPARE(ui->panel.editor()["description"].toString(),
             QString("Unsaved local text"));
    ui->panel.discard();

    // Simultaneous queue CAS and start from the same snapshot: one winner.
    before = oracle.get("/v1/snapshot");
    const QJsonValue qv =
        before["projects"].toArray().first().toObject()["queue_version"];
    auto *reorder = oracle.send("PUT", "/v1/projects/" + project + "/queue",
                                {{"expected_queue_version", qv},
                                 {"task_ids", QJsonArray({ids[1], ids[2]})}});
    auto *start =
        oracle.send("POST", "/v1/tasks/" + ids[2] + "/transition",
                    {{"expected_version",
                      find(before["tasks"].toArray(), ids[2])["version"]},
                     {"expected_queue_version", qv},
                     {"state", "in_progress"}});
    QTRY_VERIFY(reorder->isFinished() && start->isFinished());
    QList<int> statuses{
        reorder->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt(),
        start->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt()};
    std::sort(statuses.begin(), statuses.end());
    QCOMPARE(statuses, QList<int>({200, 409}));
    reorder->deleteLater();
    start->deleteLater();
    auto after = oracle.get("/v1/snapshot");
    QCOMPARE(after["event_seq"].toInteger(),
             before["event_seq"].toInteger() + 1);
    QTRY_COMPARE(ui->client.sequence(), after["event_seq"].toInteger());
    QTRY_COMPARE(other.sequence(), after["event_seq"].toInteger());

    // Force a real committed response loss and retry the identical request.
    LostResponse proxy(oracle);
    QVERIFY(proxy.listen(QHostAddress::LocalHost, 0));
    ApiClient retry;
    QVERIFY(retry.configure(
        QUrl(QString("http://127.0.0.1:%1").arg(proxy.serverPort())),
        daemon.tokenPath()));
    retry.reload();
    QTRY_VERIFY(retry.online());
    const auto seq = after["event_seq"].toInteger();
    QVERIFY(retry.save(
        "PATCH", "/v1/tasks/" + ids[0],
        {{"expected_version",
          find(after["tasks"].toArray(), ids[0])["version"].toVariant()},
         {"description", "Committed despite lost response"}}));
    QTRY_VERIFY(retry.uncertain());
    QCOMPARE(proxy.statuses, QList<int>({200}));
    QCOMPARE(oracle.get("/v1/snapshot")["event_seq"].toInteger(), seq + 1);
    QVERIFY(retry.retry());
    QTRY_VERIFY(!retry.pending() && !retry.uncertain());
    QCOMPARE(proxy.statuses, QList<int>({200, 200}));
    QCOMPARE(proxy.mutations.size(), 2);
    // Never let Qt's comparison printer expose the Authorization header.
    QVERIFY2(proxy.mutations[0] == proxy.mutations[1],
             "Retry changed its original request bytes");
    after = oracle.get("/v1/snapshot");
    QCOMPARE(after["event_seq"].toInteger(), seq + 1);
    QTRY_COMPARE(ui->panel.task()["description"].toString(),
                 QString("Committed despite lost response"));
    // Reconnect after missing an event; snapshot + stream closes the gap.
    ui->client.stop();
    QVERIFY(other.save(
        "PATCH", "/v1/tasks/" + ids[0],
        {{"expected_version",
          find(after["tasks"].toArray(), ids[0])["version"].toVariant()},
         {"title", "Changed while disconnected"}}));
    QTRY_VERIFY(!other.pending() && !other.loading());
    QVERIFY(ui->client.configure(daemon.endpoint, daemon.tokenPath()));
    ui->client.reload();
    QTRY_COMPARE(ui->panel.task()["title"].toString(),
                 QString("Changed while disconnected"));
    after = oracle.get("/v1/snapshot");
    QTRY_COMPARE(ui->client.sequence(), after["event_seq"].toInteger());
    history = oracle.get(historyPath)["events"].toArray();
    QCOMPARE(history.size(), after["event_seq"].toInt());
    QCOMPARE(ui->client.instance(), instance);
  }
};

int main(int argc, char **argv) {
  QGuiApplication app(argc, argv);
  app.setQuitOnLastWindowClosed(false);
  Acceptance tests;
  return QTest::qExec(&tests, argc, argv);
}
#include "acceptance.moc"
