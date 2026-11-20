#include "apiclient.h"
#include <QFile>
#include <QJsonDocument>
#include <QProcess>
#include <QSignalSpy>
#include <QTcpServer>
#include <QTcpSocket>
#include <QTemporaryDir>
#include <QtTest>
#include <functional>

namespace {
const QString instanceID = "11111111-1111-4111-8111-111111111111";
const QString projectID = "22222222-2222-4222-8222-222222222222";
const QString taskID = "33333333-3333-4333-8333-333333333333";
const QString bindingID = "44444444-4444-4444-8444-444444444444";
const QString messageID = "55555555-5555-4555-8555-555555555555";
const QByteArray secret(64, 'a');
QByteArray messageBody(const QString &status = "stored") {
  return QJsonDocument(QJsonObject{{"id", messageID},
                                   {"task_id", taskID},
                                   {"delivery_binding_id", bindingID},
                                   {"text", "line one\nline two"},
                                   {"status", status},
                                   {"blocked", false},
                                   {"created_at", "2026-11-17T12:00:00Z"}})
      .toJson(QJsonDocument::Compact);
}
QByteArray idempotencyKey(const QByteArray &request) {
  const QRegularExpression expression(
      "Idempotency-Key: ([0-9a-fA-F-]{36})",
      QRegularExpression::CaseInsensitiveOption);
  const auto match = expression.match(QString::fromLatin1(request));
  return match.hasMatch() ? match.captured(1).toLatin1() : QByteArray{};
}
QByteArray snapshot(qint64 seq = 0, const QString &instance = instanceID,
                    bool withProject = false) {
  QJsonArray projects;
  if (withProject)
    projects.append(QJsonObject{{"id", projectID},
                                {"version", 1},
                                {"queue_version", 1},
                                {"name", "Example"},
                                {"root_path", "/example"},
                                {"description", ""},
                                {"archived", false},
                                {"created_at", "2026-11-17T00:00:00Z"},
                                {"updated_at", "2026-11-17T00:00:00Z"}});
  return QJsonDocument(QJsonObject{{"instance_id", instance},
                                   {"event_seq", seq},
                                   {"projects", projects},
                                   {"tasks", QJsonArray{}},
                                   {"queues", QJsonArray{}}})
      .toJson(QJsonDocument::Compact);
}
void respond(QTcpSocket *socket, int status, const QByteArray &body,
             const QByteArray &extra = {}) {
  socket->write("HTTP/1.1 " + QByteArray::number(status) +
                " Test\r\nContent-Type: application/json\r\nContent-Length: " +
                QByteArray::number(body.size()) + "\r\nConnection: close\r\n" +
                extra + "\r\n" + body);
  socket->disconnectFromHost();
}
class Fixture : public QObject {
public:
  QTemporaryDir directory;
  QTcpServer server;
  QPointer<QTcpSocket> stream;
  QList<QByteArray> requests;
  std::function<void(QTcpSocket *, QByteArray)> handler;
  QByteArray state = snapshot();
  int streamCount = 0;
  Fixture() {
    QFile::setPermissions(directory.path(), QFile::ReadOwner |
                                                QFile::WriteOwner |
                                                QFile::ExeOwner);
    QFile token(tokenPath());
    if (token.open(QIODevice::WriteOnly)) {
      token.write(secret + '\n');
      token.close();
      token.setPermissions(QFile::ReadOwner | QFile::WriteOwner);
    }
    server.listen(QHostAddress::LocalHost, 0);
    connect(&server, &QTcpServer::newConnection, this, [this] {
      while (auto *socket = server.nextPendingConnection()) {
        connect(socket, &QTcpSocket::disconnected, socket,
                &QObject::deleteLater);
        connect(socket, &QTcpSocket::readyRead, this, [this, socket] {
          auto bytes =
              socket->property("request").toByteArray() + socket->readAll();
          socket->setProperty("request", bytes);
          const auto end = bytes.indexOf("\r\n\r\n");
          if (end < 0 || socket->property("handled").toBool())
            return;
          const QRegularExpression length(
              "Content-Length: (\\d+)",
              QRegularExpression::CaseInsensitiveOption);
          const auto match = length.match(QString::fromLatin1(bytes.left(end)));
          if (bytes.size() - end - 4 < match.captured(1).toInt())
            return;
          socket->setProperty("handled", true);
          requests.append(bytes);
          if (handler) {
            handler(socket, bytes);
            return;
          }
          normal(socket, bytes);
        });
      }
    });
  }
  QString tokenPath() const { return directory.filePath("owner.token"); }
  QUrl endpoint() const {
    return QUrl("http://127.0.0.1:" + QString::number(server.serverPort()));
  }
  void normal(QTcpSocket *socket, const QByteArray &request) {
    if (request.startsWith("GET /v1/snapshot "))
      respond(socket, 200, state);
    else if (request.startsWith("GET /v1/stream?")) {
      stream = socket;
      ++streamCount;
      socket->write(
          "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: "
          "close\r\n\r\n: connected\n\n");
    } else
      respond(socket, 409, "{\"code\":\"conflict\",\"message\":\"safe\"}");
  }
  void publishEvent(int seq) {
    if (stream)
      stream->write("id: " + QByteArray::number(seq) +
                    "\nevent: invalidation\ndata: "
                    "{\"kind\":\"project_updated\",\"project_id\":\"" +
                    projectID.toUtf8() + "\"}\n\n");
  }
};
} // namespace

class ClientTests : public QObject {
  Q_OBJECT
private slots:
  void initTestCase() { QTest::failOnWarning(); }
  void credentialAndEndpointBoundaries() {
    Fixture fixture;
    ApiClient client;
    for (const auto &url :
         {"https://127.0.0.1:7331", "http://localhost:7331",
          "http://127.0.0.1:0", "http://user@127.0.0.1:1",
          "http://127.0.0.1:1/path", "http://127.0.0.1:1?token=x"})
      QVERIFY(!client.configure(QUrl(url), fixture.tokenPath()));
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    QFile::setPermissions(fixture.tokenPath(), QFile::ReadOwner |
                                                   QFile::WriteOwner |
                                                   QFile::ReadOther);
    QVERIFY(!client.configure(fixture.endpoint(), fixture.tokenPath()));
    QFile::setPermissions(fixture.tokenPath(),
                          QFile::ReadOwner | QFile::WriteOwner);
    QVERIFY(
        QFile::link(fixture.tokenPath(), fixture.directory.filePath("link")));
    QVERIFY(!client.configure(fixture.endpoint(),
                              fixture.directory.filePath("link")));
    QFile::setPermissions(fixture.directory.path(),
                          QFile::ReadOwner | QFile::WriteOwner |
                              QFile::ExeOwner | QFile::ExeOther);
    QVERIFY(!client.configure(fixture.endpoint(), fixture.tokenPath()));
    QVERIFY(!client.error().contains(QString::fromLatin1(secret)));
    for (int index = 0; index < client.metaObject()->propertyCount(); ++index)
      QVERIFY(!QString::fromLatin1(client.metaObject()->property(index).name())
                   .contains("token", Qt::CaseInsensitive));
  }
  void snapshotModelsAuthAndSse() {
    Fixture fixture;
    fixture.state = snapshot(0, instanceID, true);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    QTRY_VERIFY(fixture.stream);
    QCOMPARE(client.projects()->rowCount(), 1);
    QCOMPARE(client.projects()->get(0).value("name").toString(),
             QString("Example"));
    QCOMPARE(client.projects()->get(-1), QVariantMap{});
    QCOMPARE(client.projects()->rowCount(client.projects()->index(0, 0)), 0);
    QVERIFY(client.lastUpdated().isValid());
    QVERIFY(
        fixture.requests.first().contains("Authorization: Bearer " + secret));
    client.setDraft({{"title", "Несохранённое"}});
    fixture.state = snapshot(2, instanceID, true);
    fixture.publishEvent(1);
    fixture.publishEvent(2);
    QTRY_COMPARE(client.sequence(), 2);
    QCOMPARE(client.draft().value("title").toString(),
             QString("Несохранённое"));
    QCOMPARE(fixture.requests.size(), 3); // One coalesced snapshot.
    client.stop();
    QVERIFY(!client.online());
    QVERIFY(!client.loading());
    QVERIFY(!client.save("POST", "/v1/projects", {{"name", "offline"}}));
    QCOMPARE(client.projects()->rowCount(), 1);
  }
  void rejectsInvalidResponses_data() {
    QTest::addColumn<int>("status");
    QTest::addColumn<QByteArray>("body");
    QTest::newRow("malformed") << 200 << QByteArray("{bad");
    QTest::newRow("truncated") << 200 << QByteArray("{\"instance_id\":");
    QTest::newRow("empty-object") << 200 << QByteArray("{}");
    QTest::newRow("unauthorized") << 401 << secret;
    QTest::newRow("redirect") << 302 << QByteArray{};
    auto obj = QJsonDocument::fromJson(snapshot()).object();
    obj["event_seq"] = -1;
    QTest::newRow("negative-sequence") << 200 << QJsonDocument(obj).toJson();
    obj = QJsonDocument::fromJson(snapshot()).object();
    obj["projects"] = QJsonArray{QJsonObject{{"id", projectID}}};
    QTest::newRow("invalid-row") << 200 << QJsonDocument(obj).toJson();
    QTest::newRow("oversized") << 200 << QByteArray(32 * 1024 * 1024 + 1, ' ');
  }
  void rejectsInvalidResponses() {
    QFETCH(int, status);
    QFETCH(QByteArray, body);
    Fixture fixture;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &) {
      respond(socket, status, body, "Location: http://127.0.0.1:1/leak\r\n");
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(!client.error().isEmpty());
    QVERIFY(!client.online());
    QCOMPARE(client.projects()->rowCount(), 0);
    QCOMPARE(fixture.requests.size(), 1);
    QVERIFY(!client.error().contains(QString::fromLatin1(secret)));
    client.stop();
  }
  void timeoutDoesNotBlockEventLoop() {
    Fixture fixture;
    fixture.handler = [](QTcpSocket *, const QByteArray &) {};
    ApiClient client;
    client.setTimeout(60);
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTimer timer;
    QSignalSpy ticks(&timer, &QTimer::timeout);
    timer.start(5);
    QTRY_VERIFY(!client.error().isEmpty());
    QVERIFY(ticks.size() > 1);
    QVERIFY(!client.loading());
    client.stop();
  }
  void generationsAndMonotonicSnapshots() {
    Fixture oldFixture, newFixture;
    QPointer<QTcpSocket> delayed;
    oldFixture.handler = [&](QTcpSocket *socket, const QByteArray &) {
      delayed = socket;
    };
    ApiClient client;
    QVERIFY(client.configure(oldFixture.endpoint(), oldFixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(delayed);
    newFixture.state = snapshot(10, instanceID, true);
    QVERIFY(client.configure(newFixture.endpoint(), newFixture.tokenPath()));
    client.reload();
    QTRY_COMPARE(client.sequence(), 10);
    if (delayed && delayed->state() == QAbstractSocket::ConnectedState)
      respond(delayed, 200, snapshot(99));
    QTest::qWait(50);
    QCOMPARE(client.sequence(), 10);
    QCOMPARE(client.projects()->rowCount(), 1);
    newFixture.state = snapshot(9);
    client.reload();
    QTRY_VERIFY(client.error().contains("Outdated"));
    QCOMPARE(client.sequence(), 10);
    QCOMPARE(client.projects()->rowCount(), 1);
    client.stop();
  }
  void reconnectAndInstanceReset() {
    Fixture fixture;
    fixture.state = snapshot(7);
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(fixture.stream);
    const QString other = "33333333-3333-4333-8333-333333333333";
    fixture.state = snapshot(0, other);
    fixture.stream->disconnectFromHost();
    QTRY_VERIFY(!client.online());
    QTRY_COMPARE_WITH_TIMEOUT(client.instance(), other, 4000);
    QTRY_COMPARE(fixture.streamCount, 2);
    QCOMPARE(client.sequence(), 0);
    QVERIFY(fixture.requests.last().contains("after=0"));
    QVERIFY(fixture.requests.last().contains(other.toUtf8()));
    client.stop();
  }
  void malformedAndTruncatedStream_data() {
    QTest::addColumn<QByteArray>("frame");
    QTest::newRow("invalid-json")
        << QByteArray("id: 1\nevent: invalidation\ndata: {bad}\n\n");
    QTest::newRow("invalid-id")
        << QByteArray("id: nope\nevent: invalidation\ndata: {}\n\n");
    QTest::newRow("huge-frame") << QByteArray(65537, 'x');
    QTest::newRow("truncated-frame")
        << QByteArray("id: 1\nevent: invalidation\ndata: {");
  }
  void malformedAndTruncatedStream() {
    QFETCH(QByteArray, frame);
    Fixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(fixture.stream);
    fixture.stream->write(frame);
    if (frame.endsWith('{'))
      fixture.stream->disconnectFromHost();
    QTRY_VERIFY(!client.online());
    QCOMPARE(client.sequence(), 0);
    client.stop();
  }
  void streamResetRequiresNewSnapshot() {
    Fixture fixture;
    int subscriptions = 0;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/stream?") && subscriptions++ == 0) {
        fixture.state = snapshot(4);
        respond(socket, 409,
                "{\"code\":\"reset_required\",\"message\":\"reset\"}");
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_COMPARE_WITH_TIMEOUT(client.sequence(), 4, 4000);
    QTRY_VERIFY(fixture.stream);
    QVERIFY(fixture.requests.last().contains("after=4"));
    client.stop();
  }
  void fragmentedSseAndReloadDuringInflightSnapshot() {
    Fixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(fixture.stream);
    fixture.state = snapshot(1);
    fixture.stream->write("id: 1\r");
    QTest::qWait(10);
    QCOMPARE(client.sequence(), 0);
    fixture.stream->write("\nevent: invalidation\r\ndata: "
                          "{\"kind\":\"update\",\"project_id\":\"" +
                          projectID.toUtf8() + "\"}\r\n\r");
    QTest::qWait(10);
    QCOMPARE(client.sequence(), 0);
    fixture.stream->write("\n");
    QTRY_COMPARE(client.sequence(), 1);
    QPointer<QTcpSocket> delayed;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &) {
      delayed = socket;
    };
    client.reload();
    QTRY_VERIFY(delayed);
    client.reload();
    const int before = fixture.requests.size();
    fixture.handler = {};
    respond(delayed, 200, snapshot(1));
    QTRY_COMPARE(fixture.requests.size(), before + 1);
    QTRY_VERIFY(!client.loading());
    client.stop();
  }
  void mutationErrors_data() {
    QTest::addColumn<int>("status");
    QTest::addColumn<QByteArray>("body");
    QTest::addColumn<bool>("unknown");
    QTest::newRow("unauthorized") << 401 << secret << false;
    QTest::newRow("invalid") << 400 << secret << false;
    QTest::newRow("server-error") << 500 << secret << true;
    QTest::newRow("redirect") << 302 << QByteArray{} << true;
    QTest::newRow("bad-success") << 201 << QByteArray("{}") << true;
    QTest::newRow("truncated-success") << 201 << QByteArray("{\"id\":") << true;
    QTest::newRow("timeout") << 0 << QByteArray{} << true;
  }
  void mutationErrors() {
    QFETCH(int, status);
    QFETCH(QByteArray, body);
    QFETCH(bool, unknown);
    Fixture fixture;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST")) {
        if (status)
          respond(socket, status, body,
                  "Location: http://127.0.0.1:1/leak\r\n");
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    client.setTimeout(100);
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    client.setDraft({{"name", "Unsaved"}});
    QVERIFY(client.save("POST", "/v1/projects", client.draft()));
    QTRY_VERIFY(!client.pending());
    QCOMPARE(client.uncertain(), unknown);
    if (status == 401)
      QVERIFY(!client.online());
    QVERIFY(client.dirty());
    QVERIFY(!client.error().contains(QString::fromLatin1(secret)));
    client.stop();
  }
  void conflictPreservesDraftAndVersions() {
    Fixture fixture;
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    client.setDraft({{"description", "my draft"}});
    QVERIFY(
        client.save("PATCH", "/v1/projects/" + projectID,
                    {{"expected_version", 1}, {"description", "my draft"}}));
    QVERIFY(!client.save("PATCH", "/v1/projects/" + projectID, {}));
    QTRY_VERIFY(client.conflict());
    QVERIFY(client.dirty());
    QVERIFY(!client.uncertain());
    QVERIFY(fixture.requests.last().contains("\"expected_version\":1"));
    client.reload();
    QTRY_VERIFY(!client.loading());
    QVERIFY(client.dirty());
    client.discardDraft();
    QVERIFY(!client.dirty());
    client.stop();
  }
  void unknownOutcomeReusesRequestAndPreservesNewDraft() {
    Fixture fixture;
    QList<QByteArray> mutations;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (!request.startsWith("POST")) {
        fixture.normal(socket, request);
        return;
      }
      mutations.append(request);
      if (mutations.size() == 1)
        socket->disconnectFromHost(); // Simulated commit followed by lost
                                      // response.
      else {
        QTimer::singleShot(60, socket, [socket] {
          respond(socket, 201,
                  "{\"id\":\"" + projectID.toUtf8() + "\",\"version\":1}");
        });
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    client.setDraft({{"name", "old draft"}});
    QVERIFY(client.save("POST", "/v1/projects", {{"name", "original"}}));
    QTRY_VERIFY(client.uncertain());
    QVERIFY(client.dirty());
    QVERIFY(!client.configure(fixture.endpoint(), fixture.tokenPath()));
    QVERIFY(!client.save("POST", "/v1/projects", {{"name", "different"}}));
    QVERIFY(!client.canAbandon());
    QVERIFY(!client.abandonUncertain());
    client.discardDraft();
    QVERIFY(client.dirty());
    QVERIFY(client.retry());
    client.setDraft({{"name", "new draft"}});
    QTRY_VERIFY(!client.pending());
    QVERIFY(!client.uncertain());
    QCOMPARE(mutations.size(), 2);
    QCOMPARE(mutations[0], mutations[1]);
    QCOMPARE(client.draft().value("name").toString(), QString("new draft"));
    client.stop();
  }
  void unknownOutcomeCannotReplayIntoDifferentDatabase() {
    Fixture fixture;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST"))
        socket->disconnectFromHost();
      else
        fixture.normal(socket, request);
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(client.save("POST", "/v1/projects", {{"name", "original"}}));
    QTRY_VERIFY(client.uncertain());
    fixture.state = snapshot(0, "33333333-3333-4333-8333-333333333333");
    client.reload();
    QTRY_COMPARE(client.instance(),
                 QString("33333333-3333-4333-8333-333333333333"));
    QVERIFY(!client.retry());
    QVERIFY(client.canAbandon());
    QVERIFY(client.abandonUncertain());
    QVERIFY(!client.uncertain());
    QVERIFY(!client.retry());
    QVERIFY(!client.abandonUncertain());
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.stop();
  }
  void commissionUnknownOutcomeReusesKeyAndRefusesRepeat() {
    Fixture fixture;
    int posts = 0;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST")) {
        ++posts;
        if (posts == 1) {
          socket->disconnectFromHost(); // Commit followed by a lost reply.
          return;
        }
        QTimer::singleShot(60, socket,
                           [socket] { respond(socket, 200, messageBody()); });
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(client.sendMessage(taskID, "line one\nline two", bindingID));
    QTRY_VERIFY(client.uncertain());
    // A repeated explicit action is refused while the first one is unresolved.
    QVERIFY(!client.sendMessage(taskID, "different", bindingID));
    QCOMPARE(posts, 1);
    QVERIFY(client.retry());
    QTRY_VERIFY(!client.uncertain());
    QCOMPARE(posts, 2);
    QCOMPARE(idempotencyKey(fixture.requests.at(fixture.requests.size() - 2)),
             idempotencyKey(fixture.requests.last()));
    client.stop();
  }
  void commissionTimeoutReusesSameKey() {
    Fixture fixture;
    bool silent = true;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST") && silent)
        return; // The deadline aborts the request without any response.
      if (request.startsWith("POST")) {
        QTimer::singleShot(60, socket,
                           [socket] { respond(socket, 200, messageBody()); });
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    client.setTimeout(100);
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(client.sendMessage(taskID, "commission", bindingID));
    QTRY_VERIFY(client.uncertain());
    QVERIFY(client.error().contains("same key"));
    silent = false;
    QVERIFY(client.retry());
    QTRY_VERIFY(!client.uncertain());
    QCOMPARE(idempotencyKey(fixture.requests.at(fixture.requests.size() - 2)),
             idempotencyKey(fixture.requests.last()));
    client.stop();
  }
  void commissionIncompatibleAndStaleResponses() {
    Fixture fixture;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST"))
        respond(socket, 200, "{}"); // Old daemon without the contract.
      else
        fixture.normal(socket, request);
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(client.sendMessage(taskID, "commission", bindingID));
    QTRY_VERIFY(client.uncertain());
    QVERIFY(client.error().contains("same key"));
    // Replacement of the database pins the original instance: no replay.
    fixture.state = snapshot(0, "33333333-3333-4333-8333-333333333333");
    client.reload();
    QTRY_COMPARE(client.instance(),
                 QString("33333333-3333-4333-8333-333333333333"));
    QVERIFY(!client.retry());
    QVERIFY(client.canAbandon());
    QVERIFY(client.abandonUncertain());
    QVERIFY(!client.uncertain());
    client.stop();
  }
  void questionAnswerDoesNotClearAnExistingEditorConflict() {
    Fixture fixture;
    const QString question = "66666666-6666-4666-8666-666666666666";
    bool reject = true;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("POST") && request.contains("/answer ")) {
        if (reject) {
          respond(socket, 409,
                  R"({"code":"already_answered","message":"No new answer"})");
        } else {
          respond(socket, 200,
                  QJsonDocument(QJsonObject{{"question_id", question},
                                            {"message_id", messageID},
                                            {"status", "stored"}})
                      .toJson());
        }
      } else {
        fixture.normal(socket, request);
      }
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    const QVariantMap draft{{"description", "An unrelated project draft"}};
    client.setDraft(draft);
    QVERIFY(client.save("PATCH", "/v1/projects/" + projectID,
                        {{"expected_version", 1},
                         {"description", draft.value("description")}}));
    QTRY_VERIFY(client.conflict());
    QSignalSpy accepted(&client, &ApiClient::answerAccepted);
    QVERIFY(client.answerQuestion(taskID, question, "Example answer"));
    QVERIFY(client.conflict());
    QTRY_VERIFY(!client.pending());
    QVERIFY(client.conflict());
    QVERIFY(!client.uncertain());
    QCOMPARE(accepted.count(), 0);
    QCOMPARE(client.draft(), draft);
    reject = false;
    QVERIFY(client.answerQuestion(taskID, question, "A valid answer"));
    QTRY_COMPARE(accepted.count(), 1);
    QVERIFY(client.conflict());
    QCOMPARE(client.draft(), draft);
    client.stop();
  }
  void questionAnswerUnknownOutcomeReusesExactRequest_data() {
    QTest::addColumn<bool>("legacy");
    QTest::newRow("instance-mismatch") << false;
    QTest::newRow("legacy-missing-route") << true;
  }
  void questionAnswerUnknownOutcomeReusesExactRequest() {
    QFETCH(bool, legacy);
    const QString question = "66666666-6666-4666-8666-666666666666";
    Fixture fixture;
    QList<QByteArray> answers;
    const QString replacement = "77777777-7777-4777-8777-777777777777";
    QString serving = instanceID;
    QPointer<QTcpSocket> delayed;
    bool holdSnapshot = false;
    fixture.handler = [&](QTcpSocket *socket, const QByteArray &request) {
      if (request.startsWith("GET /v1/snapshot ") && holdSnapshot) {
        delayed = socket;
        return;
      }
      if (request.startsWith("POST") && request.contains("/answer ")) {
        answers.append(request);
        if (answers.size() == 1) {
          socket->disconnectFromHost();
          return;
        }
        const auto payload = QJsonDocument::fromJson(
                                 request.mid(request.indexOf("\r\n\r\n") + 4))
                                 .object();
        if (serving == replacement && legacy) {
          respond(
              socket, 404,
              "{\"code\":\"not_found\",\"message\":\"endpoint not found\"}");
          return;
        }
        if (payload.value("instance_id").toString() != serving) {
          respond(socket, 409, "{\"code\":\"instance_mismatch\"}");
          return;
        }
        respond(socket, 200,
                QJsonDocument(QJsonObject{{"question_id", question},
                                          {"message_id", messageID},
                                          {"status", "stored"}})
                    .toJson(QJsonDocument::Compact));
        return;
      }
      if (request.startsWith("GET") && request.contains("/questions?")) {
        respond(socket, 200,
                "{\"questions\":[],\"next_after\":0,\"has_more\":false}");
        return;
      }
      fixture.normal(socket, request);
    };
    ApiClient client;
    QVERIFY(client.configure(fixture.endpoint(), fixture.tokenPath()));
    client.reload();
    QTRY_VERIFY(client.online());
    QSignalSpy accepted(&client, &ApiClient::answerAccepted);
    QVERIFY(!client.answerQuestion(taskID, question, QString(QChar(0xd800))));
    QVERIFY(client.answerQuestion(taskID, question, "line one\nline two"));
    QTRY_VERIFY(client.uncertain());
    QVERIFY(!client.answerQuestion(taskID, question, "different"));
    QCOMPARE(answers.size(), 1);
    holdSnapshot = true;
    client.reload(); // An A snapshot was already in flight before the swap.
    QTRY_VERIFY(delayed);
    serving = replacement;
    QVERIFY(client.retry());
    QTRY_VERIFY(!client.pending());
    QVERIFY(client.uncertain());
    QCOMPARE(client.instance(), instanceID);
    QCOMPARE(accepted.count(), 0);
    QVERIFY(!client.retry());
    respond(delayed, 200, snapshot(0, instanceID));
    delayed = nullptr;
    QTRY_VERIFY(delayed); // A post-rejection snapshot is required.
    QCOMPARE(client.instance(), instanceID);
    QVERIFY(!client.retry());
    holdSnapshot = false;
    respond(delayed, 200, snapshot(0, replacement));
    QTRY_COMPARE(client.instance(), replacement);
    QVERIFY(client.uncertain());
    QVERIFY(!client.retry());
    QCOMPARE(answers.size(), 2);
    serving = instanceID;
    client.reload();
    QTRY_COMPARE(client.instance(), instanceID);
    QVERIFY(client.retry());
    QTRY_VERIFY(!client.uncertain());
    QCOMPARE(accepted.count(), 1);
    QCOMPARE(answers.size(), 3);
    QCOMPARE(answers[0], answers[1]);
    QCOMPARE(answers[0], answers[2]);
    QVERIFY(answers[0].contains("\"text\":\"line one\\nline two\""));
    QVERIFY(!idempotencyKey(answers[0]).isEmpty());
    client.stop();
  }
  void realTemporaryDaemon() {
    QTemporaryDir directory;
    QVERIFY(directory.isValid());
    QProcess daemon;
    auto environment = QProcessEnvironment::systemEnvironment();
    environment.insert("HOME", directory.path());
    environment.remove("XDG_CONFIG_HOME");
    environment.remove("XDG_DATA_HOME");
    daemon.setProcessEnvironment(environment);
    daemon.setProgram(qEnvironmentVariable("ARTEL_TEST_DAEMON"));
    daemon.setArguments(
        {"--data-dir", directory.filePath("data"), "--token-file",
         directory.filePath("private/owner.token"), "--listen", "127.0.0.1:0"});
    daemon.start();
    QVERIFY(daemon.waitForStarted(5000));
    // Cleanup is scoped to this QProcess even if an assertion below fails.
    struct Cleanup {
      QProcess &process;
      ~Cleanup() {
        process.terminate();
        if (!process.waitForFinished(5000)) {
          process.kill();
          process.waitForFinished(5000);
        }
      }
    } cleanup{daemon};
    QByteArray output;
    QTRY_VERIFY_WITH_TIMEOUT((output += daemon.readAllStandardOutput())
                                 .contains("arteld: listening "),
                             5000);
    const QString address =
        QString::fromUtf8(output).trimmed().section(' ', -1);
    ApiClient client;
    QVERIFY(client.configure(QUrl("http://" + address),
                             directory.filePath("private/owner.token")));
    client.reload();
    QTRY_VERIFY(client.online());
    QVERIFY(uuidIsValid(client.instance()));
    client.setDraft({{"name", "Fixture project"}});
    QSignalSpy saved(&client, &ApiClient::mutationSucceeded);
    QVERIFY(client.save(
        "POST", "/v1/projects",
        {{"name", "Fixture project"}, {"root_path", directory.path()}}));
    QTRY_COMPARE(saved.size(), 1);
    QTRY_COMPARE(client.projects()->rowCount(), 1);
    QVERIFY(!client.dirty());
    QCOMPARE(client.projects()->get(0).value("name").toString(),
             QString("Fixture project"));
    const auto id = client.projects()->get(0).value("id").toString();
    QVERIFY(client.save("POST", "/v1/projects/" + id + "/tasks",
                        {{"title", "First task"}}));
    QTRY_COMPARE(saved.size(), 2);
    QTRY_COMPARE(client.tasks()->rowCount(), 1);
    const auto taskID = client.tasks()->get(0).value("id").toString();
    QCOMPARE(client.tasks()->get(0).value("state").toString(),
             QString("backlog"));
    QVERIFY(client.save("PUT", "/v1/projects/" + id + "/queue",
                        {{"expected_queue_version",
                          client.projects()->get(0).value("queue_version")},
                         {"task_ids", QStringList{taskID}}}));
    QTRY_COMPARE(saved.size(), 3);
    QTRY_COMPARE(client.tasks()->get(0).value("state").toString(),
                 QString("queued"));
    QCOMPARE(client.queues()->rowCount(), 1);
    QCOMPARE(client.queues()->get(0).value("task_ids").toList(),
             QVariantList{taskID});
    client.stop();
    QVERIFY(!output.contains(secret));
  }

private:
  static bool uuidIsValid(const QString &value) {
    return !QUuid(value).isNull();
  }
};
QTEST_GUILESS_MAIN(ClientTests)
#include "apiclient_tests.moc"
