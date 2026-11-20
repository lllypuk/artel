#include "apiclient.h"
#include "appearance.h"
#include "panel.h"
#include "workspacecontroller.h"
#include <QFile>
#include <QInputMethodEvent>
#include <QJsonDocument>
#include <QNetworkProxy>
#include <QPointer>
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
  QJsonObject get(const QString &path) { return request("GET", path); }
  QJsonObject request(const QByteArray &method, const QString &path,
                      const QJsonObject &body = {}) {
    auto *reply = send(method, path, body);
    QElapsedTimer timer;
    timer.start();
    while (!reply->isFinished() && timer.elapsed() < 6000)
      QTest::qWait(10);
    const int status =
        reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
    const bool ok = reply->isFinished() &&
                    (status == 200 || (method != "GET" && status == 201));
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
  Window(const Daemon &daemon, const QUrl &endpoint = {})
      : appearance(daemon.dir.path(), daemon.dir.filePath("language")) {
    if (!client.configure(endpoint.isValid() ? endpoint : daemon.endpoint,
                          daemon.tokenPath()))
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
struct ProcessResult {
  int code = -1;
  QByteArray output;
  QByteArray error;
};
ProcessResult runProcess(const QString &program, const QStringList &arguments) {
  QProcess process;
  process.start(program, arguments);
  if (!process.waitForStarted(5000))
    return {};
  process.waitForFinished(15000);
  if (process.state() != QProcess::NotRunning) {
    process.kill();
    process.waitForFinished(5000);
  }
  return {process.exitCode(), process.readAllStandardOutput(),
          process.readAllStandardError()};
}
QString git(const QString &repo, const QStringList &arguments) {
  const auto result = runProcess("git", QStringList{"-C", repo} + arguments);
  return result.code == 0 ? QString::fromUtf8(result.output).trimmed()
                          : QString();
}
bool writeJson(const QString &path, const QJsonObject &value) {
  QFile file(path);
  return file.open(QIODevice::WriteOnly) &&
         file.write(QJsonDocument(value).toJson(QJsonDocument::Compact)) > 0;
}
QString writeReviewFixture(const QString &directory, const QString &task,
                           const QString &run) {
  if (!QDir().mkpath(directory))
    return {};
  const auto agent = [](const QString &name, const QString &executor) {
    return QJsonObject{{"name", name},
                       {"lenses", QJsonArray{"bugs"}},
                       {"executor", executor},
                       {"requested_model", "fictional"},
                       {"actual_model", "fictional"},
                       {"effort", "high"},
                       {"tokens", 1},
                       {"raised", 0},
                       {"degraded", false}};
  };
  const QJsonArray agents{agent("reviewer", "pi"), agent("optional", "codex")};
  const QJsonObject finding{{"id", "fictional-finding"},
                            {"file", "fictional.cpp"},
                            {"line", 1},
                            {"end_line", 0},
                            {"severity", "minor"},
                            {"confidence", 100},
                            {"title", "Synthetic title"},
                            {"body", "Synthetic body"},
                            {"fix", "Synthetic fix"},
                            {"sources", QJsonArray{"reviewer"}},
                            {"lenses", QJsonArray{"bugs"}},
                            {"verdict", "confirmed"}};
  const QJsonObject question{{"id", "fictional-question"},
                             {"sources", QJsonArray{"optional"}}};
  const QJsonObject manifest{{"task", task},
                             {"run", run},
                             {"profile", "pi-codex"},
                             {"scope_path", "fictional-scope"},
                             {"started_at", "2026-11-20T11:00:00Z"},
                             {"finished_at", "2026-11-20T12:00:00Z"},
                             {"duration_ms", 1},
                             {"tokens", 2},
                             {"agents", agents},
                             {"degraded", QJsonValue::Null},
                             {"prompts", QJsonArray{}},
                             {"stages", QJsonArray{}}};
  const QJsonObject findings{
      {"scope", QJsonObject{{"task", task},
                            {"run", run},
                            {"scope_path", "fictional-scope"}}},
      {"sources", QJsonObject{{"expected", 2},
                              {"reported", 2},
                              {"degraded", QJsonArray{}},
                              {"agents", agents}}},
      {"findings", QJsonArray{finding}},
      {"open_questions", QJsonArray{question}},
      {"pre_existing", QJsonArray{}},
      {"immaterial", QJsonArray{}},
      {"stats", QJsonObject{{"started_at", "2026-11-20T11:00:00Z"},
                            {"finished_at", "2026-11-20T12:00:00Z"},
                            {"duration_ms", 1},
                            {"tokens", 2},
                            {"stages", QJsonArray{}}}}};
  return writeJson(directory + "/manifest.json", manifest) &&
                 writeJson(directory + "/findings.json", findings)
             ? directory
             : QString();
}
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
// A real forwarding proxy holds one result read after the daemon answered.
// This makes the old-view CAS guard observable without synthetic QML data.
class ResultDelayProxy : public QTcpServer {
public:
  Oracle &upstream;
  bool holdNext = false;
  bool failNext = false;
  int delayed = 0;
  int failures = 0;
  explicit ResultDelayProxy(Oracle &oracle) : upstream(oracle) {
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
          const QString path = QString::fromUtf8(first[1]);
          const bool resultRead =
              first[0] == "GET" && path.contains("/results?");
          const bool delay = resultRead && holdNext;
          const bool fail = resultRead && failNext;
          if (delay)
            holdNext = false;
          if (fail)
            failNext = false;
          auto *reply = upstream.send(
              first[0], path,
              QJsonDocument::fromJson(bytes->mid(split + 4)).object(), key);
          const bool stream = path.startsWith("/v1/stream?");
          if (stream) {
            socket->write(
                "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n"
                "Connection: close\r\n\r\n");
            connect(reply, &QNetworkReply::readyRead, socket,
                    [reply, socket] { socket->write(reply->readAll()); });
          }
          connect(socket, &QTcpSocket::disconnected, reply,
                  [reply] { reply->abort(); });
          connect(reply, &QNetworkReply::finished, socket,
                  [this, reply, socket, delay, fail, stream] {
                    const int status =
                        fail ? 503
                             : reply
                                   ->attribute(
                                       QNetworkRequest::HttpStatusCodeAttribute)
                                   .toInt();
                    const auto body =
                        stream            ? QByteArray{}
                        : fail            ? QByteArray(
                                                "{\"code\":\"unavailable\",\"message\":"
                                                "\"controlled failure\"}")
                        : reply->isOpen() ? reply->readAll()
                                          : QByteArray{};
                    const auto response =
                        "HTTP/1.1 " + QByteArray::number(status) +
                        " OK\r\nContent-Type: application/json\r\n"
                        "Connection: close\r\nContent-Length: " +
                        QByteArray::number(body.size()) + "\r\n\r\n" + body;
                    if (fail)
                      ++failures;
                    if (delay) {
                      ++delayed;
                      held.append({socket, response});
                    } else if (!stream) {
                      socket->write(response);
                      socket->disconnectFromHost();
                    }
                    reply->deleteLater();
                  });
        });
      }
    });
  }
  void release() {
    for (auto &pending : held) {
      if (pending.first) {
        pending.first->write(pending.second);
        pending.first->disconnectFromHost();
      }
    }
    held.clear();
  }

private:
  QList<QPair<QPointer<QTcpSocket>, QByteArray>> held;
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
  // The runner, not an agent, creates the commits and synthetic report. The
  // standalone slot owns its daemon; result_pty.py instead supplies one live
  // daemon/task with an already-bound Pi PTY for the shared 4a/result chain.
  void reviewedTaskResult() {
    Daemon daemon;
    QVERIFY(daemon.dir.isValid());
    const bool sharedDelivery =
        !qEnvironmentVariable("ARTEL_RESULT_ENDPOINT").isEmpty();
    QString project = qEnvironmentVariable("ARTEL_RESULT_PROJECT");
    QString task = qEnvironmentVariable("ARTEL_RESULT_TASK");
    QString otherTask;
    QString repo = qEnvironmentVariable("ARTEL_RESULT_REPO");
    ProcessResult result;
    if (sharedDelivery) {
      const auto token = qEnvironmentVariable("ARTEL_RESULT_OWNER_TOKEN");
      QVERIFY2(!project.isEmpty() && !task.isEmpty() && !repo.isEmpty() &&
                   !token.isEmpty(),
               "Run the shared reviewedTaskResult through result_pty.py");
      daemon.endpoint = QUrl(qEnvironmentVariable("ARTEL_RESULT_ENDPOINT"));
      const auto privateDir = daemon.dir.filePath("private");
      QVERIFY(QDir().mkpath(privateDir));
      QVERIFY(QFile::setPermissions(
          privateDir, QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner));
      QVERIFY(QFile::copy(token, daemon.tokenPath()));
    } else {
      QVERIFY(daemon.start());
      repo = daemon.dir.filePath("review-repository");
      result = runProcess("git", {"init", "-q", repo});
      QVERIFY2(result.code == 0, result.error.constData());
    }
    Oracle oracle(daemon.endpoint, daemon.tokenPath());
    ResultDelayProxy proxy(oracle);
    QVERIFY(proxy.listen(QHostAddress::LocalHost, 0));
    const QUrl proxyEndpoint(
        QString("http://127.0.0.1:%1").arg(proxy.serverPort()));
    if (!sharedDelivery) {
      project = oracle
                    .request("POST", "/v1/projects",
                             {{"name", "Fictional reviewed result"},
                              {"root_path", repo}})
                    .value("id")
                    .toString();
      task = oracle
                 .request("POST", "/v1/projects/" + project + "/tasks",
                          {{"title", "Review exact candidate"},
                           {"description", "Original task description"}})
                 .value("id")
                 .toString();
      otherTask = oracle
                      .request("POST", "/v1/projects/" + project + "/tasks",
                               {{"title", "Other task during loading"}})
                      .value("id")
                      .toString();
      QVERIFY(!task.isEmpty() && !otherTask.isEmpty());
      QVERIFY(!oracle
                   .request("PUT", "/v1/projects/" + project + "/queue",
                            {{"expected_queue_version", 1},
                             {"task_ids", QJsonArray{task}}})
                   .isEmpty());
    }

    auto ui = std::make_unique<Window>(daemon, proxyEndpoint);
    QVERIFY(ui->window);
    QTRY_VERIFY(ui->panel.writable());
    QVERIFY(click(ui->window, "projectsNavButton"));
    QVERIFY(ui->panel.selectProject(project));
    QVERIFY(ui->panel.selectTask(task));
    QTRY_VERIFY_WITH_TIMEOUT(ui->panel.resultsLoaded(), 5000);
    QVERIFY(item(ui->window, "noTaskResult")->isVisible());
    QVERIFY(!item(ui->window, "resultError")->isVisible());
    QVERIFY(item(ui->window, "commissionField"));
    if (sharedDelivery) {
      const auto commission = "Fictional result commission for " + task;
      QTRY_VERIFY_WITH_TIMEOUT(
          ui->panel.assignment().value("available").toBool(), 15000);
      QVERIFY(enter(ui->window, "commissionField", commission));
      QVERIFY(click(ui->window, "sendCommissionButton"));
      QTRY_COMPARE_WITH_TIMEOUT(ui->panel.messages().size(), 1, 10000);
      QTRY_COMPARE_WITH_TIMEOUT(
          ui->panel.messages().first().toMap().value("status").toString(),
          QString("observed"), 20000);
      const auto message =
          ui->panel.messages().first().toMap().value("id").toString();
      QVERIFY(item(ui->window, "messageStatus_" + message)->isVisible());
      QCOMPARE(ui->panel.task().value("state").toString(), QString("queued"));
    } else {
      ui->panel.setCommissionText("Fictional commission text");
      QTRY_COMPARE(
          item(ui->window, "commissionField")->property("text").toString(),
          QString("Fictional commission text"));
    }

    // In the shared chain these commits follow the observed commission. They
    // remain runner-created test input, never a claim that Pi made a commit.
    QCOMPARE(git(repo, {"config", "user.name", "Fictional Runner"}), QString());
    QCOMPARE(git(repo, {"config", "user.email", "runner@example.invalid"}),
             QString());
    QFile candidate(repo + "/candidate.txt");
    QVERIFY(candidate.open(QIODevice::WriteOnly));
    candidate.write("base\n");
    candidate.close();
    QCOMPARE(git(repo, {"add", "candidate.txt"}), QString());
    QCOMPARE(git(repo, {"commit", "-q", "-m", "Runner-created base"}),
             QString());
    const auto base = git(repo, {"rev-parse", "HEAD"});
    QCOMPARE(base.size(), 40);
    QVERIFY(candidate.open(QIODevice::WriteOnly));
    candidate.write("first head\n");
    candidate.close();
    QCOMPARE(git(repo, {"commit", "-q", "-am", "Runner-created first head"}),
             QString());
    const auto firstHead = git(repo, {"rev-parse", "HEAD"});
    QVERIFY(candidate.open(QIODevice::WriteOnly));
    candidate.write("second head\n");
    candidate.close();
    QCOMPARE(git(repo, {"commit", "-q", "-am", "Runner-created second head"}),
             QString());
    const auto secondHead = git(repo, {"rev-parse", "HEAD"});
    QVERIFY(firstHead != secondHead && secondHead.size() == 40);

    QTRY_VERIFY(ui->panel.allowed("in_progress"));
    QVERIFY(click(ui->window, "startButton"));
    QTRY_COMPARE(ui->panel.task().value("state").toString(),
                 QString("in_progress"));

    const auto bindings = daemon.dir.filePath("bindings");
    QVERIFY(QDir().mkpath(bindings));
    QVERIFY(QFile::setPermissions(
        bindings, QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner));
    const auto cli = qEnvironmentVariable("ARTEL_TEST_CLI");
    QVERIFY2(!cli.isEmpty(), "Run reviewedTaskResult through scripts/e2e");
    const auto firstBinding = bindings + "/first.json";
    const auto firstRequest =
        QUuid::createUuid().toString(QUuid::WithoutBraces);
    const QStringList firstSubmit{"results",
                                  "submit",
                                  "--task-id",
                                  task,
                                  "--repo",
                                  repo,
                                  "--base",
                                  base,
                                  "--head",
                                  firstHead,
                                  "--summary",
                                  "Fictional first candidate",
                                  "--expected-result-id",
                                  "",
                                  "--binding",
                                  firstBinding,
                                  "--revmux-task",
                                  "fictional-review",
                                  "--run",
                                  "run-01",
                                  "--request-id",
                                  firstRequest,
                                  "--endpoint",
                                  daemon.endpoint.toString(),
                                  "--token-file",
                                  daemon.tokenPath()};
    result = runProcess(cli, firstSubmit);
    QVERIFY2(result.code == 0, result.error.constData());
    const auto first = QJsonDocument::fromJson(result.output).object();
    const auto firstID = first.value("id").toString();
    QVERIFY(!firstID.isEmpty());
    QTRY_COMPARE(ui->panel.currentResult().toMap().value("id").toString(),
                 firstID);

    const auto report = writeReviewFixture(daemon.dir.filePath("report"),
                                           "fictional-review", "run-01");
    QVERIFY(!report.isEmpty());
    const auto attachRequest =
        QUuid::createUuid().toString(QUuid::WithoutBraces);
    const QStringList attach{"reviews",           "attach",
                             "--task-id",         task,
                             "--result-id",       firstID,
                             "--binding",         firstBinding,
                             "--report-dir",      report,
                             "--required-source", "reviewer=pi",
                             "--request-id",      attachRequest,
                             "--endpoint",        daemon.endpoint.toString(),
                             "--token-file",      daemon.tokenPath()};
    result = runProcess(cli, attach);
    QVERIFY2(result.code == 0, result.error.constData());
    const auto firstAttach = result.output;
    const auto review = QJsonDocument::fromJson(firstAttach).object();
    const auto reviewID = review.value("id").toString();
    QVERIFY(review.value("complete").toBool() && !reviewID.isEmpty());
    result = runProcess(cli, attach);
    QVERIFY2(result.code == 0, result.error.constData());
    QCOMPARE(result.output, firstAttach);
    QTRY_COMPARE(ui->panel.resultHistory().toList().size(), 1);
    QTRY_VERIFY(item(ui->window, "currentReviewRun_" + reviewID));
    QCOMPARE(item(ui->window, "resultBase")->property("text").toString(),
             QString("Base: %1").arg(base));
    QCOMPARE(item(ui->window, "resultHead")->property("text").toString(),
             QString("Head: %1").arg(firstHead));
    QVERIFY(item(ui->window, "resultRepo")
                ->property("text")
                .toString()
                .contains(first.value("repo").toString()));
    QVERIFY(item(ui->window, "currentReviewRun_" + reviewID)
                ->property("text")
                .toString()
                .contains("run-01"));
    QVERIFY(item(ui->window, "currentReviewReport_" + reviewID)
                ->property("text")
                .toString()
                .contains(report));
    QVERIFY(item(ui->window, "currentReviewProvenance_" + reviewID)
                ->property("text")
                .toString()
                .contains("operator"));
    QVERIFY(item(ui->window, "currentReviewOutcome_" + reviewID)->isVisible());
    QVERIFY(item(ui->window, "currentReviewSummary_" + reviewID)
                ->property("text")
                .toString()
                .contains("Open questions: 1"));
    QVERIFY(!item(ui->window, "noTaskResult")->isVisible());

    // A failed read is an explicit error, never an empty-result claim.
    proxy.failNext = true;
    ui->panel.reload();
    QTRY_COMPARE(proxy.failures, 1);
    QTRY_VERIFY(!ui->panel.resultsError().isEmpty());
    QVERIFY(!ui->panel.resultsLoaded());
    QVERIFY(!item(ui->window, "noTaskResult")->isVisible());
    QVERIFY(item(ui->window, "resultError")->isVisible());
    ui->panel.reload();
    QTRY_COMPARE(ui->panel.currentResult().toMap().value("id").toString(),
                 firstID);

    if (!sharedDelivery) {
      // Switching selection cancels a held read; its late response cannot leak.
      proxy.holdNext = true;
      QVERIFY(ui->panel.selectTask(otherTask));
      QTRY_COMPARE(proxy.delayed, 1);
      QVERIFY(ui->panel.resultsLoading());
      QVERIFY(ui->panel.currentResult().toMap().isEmpty());
      QVERIFY(ui->panel.selectTask(task));
      QTRY_COMPARE(ui->panel.currentResult().toMap().value("id").toString(),
                   firstID);
      proxy.release();
      QTest::qWait(50);
      QCOMPARE(ui->panel.currentResult().toMap().value("id").toString(),
               firstID);
    }

    // SSE invalidates the result, not either kind of local draft. While the
    // replacement read is held, the old view cannot accept the new candidate.
    QVERIFY(click(ui->window, "editTaskButton"));
    QVERIFY(enter(ui->window, "descriptionField", "Unsaved result-test draft"));
    ui->panel.setCommissionText("Fictional commission kept across SSE");
    const auto secondBinding = bindings + "/second.json";
    const QStringList secondSubmit{
        "results",
        "submit",
        "--task-id",
        task,
        "--repo",
        repo,
        "--base",
        base,
        "--head",
        secondHead,
        "--summary",
        "Fictional second candidate",
        "--expected-result-id",
        firstID,
        "--binding",
        secondBinding,
        "--revmux-task",
        "fictional-review",
        "--run",
        "run-02",
        "--request-id",
        QUuid::createUuid().toString(QUuid::WithoutBraces),
        "--endpoint",
        daemon.endpoint.toString(),
        "--token-file",
        daemon.tokenPath()};
    proxy.holdNext = true;
    result = runProcess(cli, secondSubmit);
    QVERIFY2(result.code == 0, result.error.constData());
    const auto second = QJsonDocument::fromJson(result.output).object();
    const auto secondID = second.value("id").toString();
    QVERIFY(!secondID.isEmpty() && secondID != firstID);
    QTRY_COMPARE(proxy.delayed, sharedDelivery ? 1 : 2);
    QVERIFY(!ui->panel.resultsLoaded());
    QCOMPARE(ui->panel.editor().value("description").toString(),
             QString("Unsaved result-test draft"));
    QCOMPARE(ui->panel.commissionText(),
             QString("Fictional commission kept across SSE"));
    ui->panel.discard();
    QVERIFY(!ui->panel.allowed("done"));
    const auto blockedSnapshot = oracle.get("/v1/snapshot");
    QVERIFY(!click(ui->window, "doneButton"));
    QCOMPARE(oracle.get("/v1/snapshot").value("event_seq"),
             blockedSnapshot.value("event_seq"));
    proxy.release();
    QTRY_COMPARE(ui->panel.currentResult().toMap().value("id").toString(),
                 secondID);
    QCOMPARE(ui->panel.commissionText(),
             QString("Fictional commission kept across SSE"));
    QCOMPARE(item(ui->window, "resultHead")->property("text").toString(),
             QString("Head: %1").arg(secondHead));
    QTRY_COMPARE(ui->panel.resultHistory().toList().size(), 2);

    // Existing owner actions accept and return using the newly loaded ID.
    QTRY_VERIFY(ui->panel.allowed("done"));
    QVERIFY(click(ui->window, "doneButton"));
    QTRY_COMPARE(ui->panel.task().value("state").toString(), QString("done"));
    QTRY_VERIFY(ui->panel.allowed("backlog"));
    QVERIFY(click(ui->window, "reopenButton"));
    QTRY_COMPARE(ui->panel.task().value("state").toString(),
                 QString("backlog"));
    QTRY_COMPARE(ui->panel.currentResult().toMap().value("id").toString(),
                 secondID);

    // Both modes prove the old review remains visible only as history. The
    // standalone slot also restarts its owned daemon and production QML.
    QVERIFY(item(ui->window, "resultHistory_" + firstID));
    QVERIFY(item(ui->window, "historyReviewRun_" + reviewID));
    QCOMPARE(item(ui->window, "historyResultHead_" + firstID)
                 ->property("text")
                 .toString(),
             QString("Head: %1").arg(firstHead));
    QCOMPARE(item(ui->window, "resultHead")->property("text").toString(),
             QString("Head: %1").arg(secondHead));
    if (!sharedDelivery) {
      QVERIFY(ui->window->close());
      ui.reset();
      daemon.stop();
      QVERIFY(daemon.start());
      oracle.endpoint = daemon.endpoint;
      ui = std::make_unique<Window>(daemon, proxyEndpoint);
      QVERIFY(ui->window);
      QTRY_VERIFY(ui->panel.writable());
      QVERIFY(click(ui->window, "projectsNavButton"));
      QVERIFY(ui->panel.selectProject(project));
      QVERIFY(ui->panel.selectTask(task));
      QTRY_COMPARE(ui->panel.currentResult().toMap().value("id").toString(),
                   secondID);
      QTRY_COMPARE(ui->panel.resultHistory().toList().size(), 2);
      QCOMPARE(ui->panel.task().value("state").toString(), QString("backlog"));
      QVERIFY(item(ui->window, "resultHistory_" + firstID));
      QVERIFY(item(ui->window, "historyReviewRun_" + reviewID));
      QCOMPARE(item(ui->window, "historyResultHead_" + firstID)
                   ->property("text")
                   .toString(),
               QString("Head: %1").arg(firstHead));
      QCOMPARE(item(ui->window, "resultHead")->property("text").toString(),
               QString("Head: %1").arg(secondHead));
    }
    const auto wire =
        oracle.get("/v1/tasks/" + task + "/results?after=0&limit=50");
    QCOMPARE(wire.value("current_result_id").toString(), secondID);
    QCOMPARE(wire.value("entries").toArray().size(), 3);
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
