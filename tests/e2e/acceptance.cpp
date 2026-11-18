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
    if (window)
      click(window, "projectsNavButton");
    workspace.returnToEditor();
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
    Window ui(daemon);
    QVERIFY(ui.window);
    QTRY_VERIFY_WITH_TIMEOUT(ui.panel.writable(), 5000);
    QVERIFY(click(ui.window, "observersButton"));
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
    QVERIFY(!ui.panel.dirty());
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
    QStringList ids;
    for (int i = 0; i < 3; ++i) {
      QVERIFY(click(ui->window, "createTaskButton"));
      QVERIFY(enter(ui->window, "nameField", QString("Task %1").arg(i + 1)));
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
