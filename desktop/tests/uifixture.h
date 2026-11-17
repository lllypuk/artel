#pragma once
#include <QFile>
#include <QJsonDocument>
#include <QPointer>
#include <QTcpServer>
#include <QTcpSocket>
#include <QTemporaryDir>
#include <QUrl>
#include <functional>

// Fault injection is local to this ephemeral HTTP listener, never a real API.
class UiFixture : public QObject {
public:
  QTemporaryDir directory;
  QTcpServer server;
  QList<QByteArray> mutations;
  std::function<void(QTcpSocket *, const QByteArray &)> handler;
  const QString instance = "11111111-1111-4111-8111-111111111111";
  const QString project = "22222222-2222-4222-8222-222222222222";
  const QString task = "33333333-3333-4333-8333-333333333333";
  QByteArray state;
  UiFixture() {
    QFile::setPermissions(directory.path(), QFile::ReadOwner |
                                                QFile::WriteOwner |
                                                QFile::ExeOwner);
    QFile token(tokenPath());
    if (token.open(QIODevice::WriteOnly)) {
      token.write(QByteArray(64, 'a'));
      token.close();
      token.setPermissions(QFile::ReadOwner | QFile::WriteOwner);
    }
    const QString time = "2026-11-17T12:00:00Z";
    state =
        QJsonDocument(
            QJsonObject{
                {"instance_id", instance},
                {"event_seq", 25},
                {"projects", QJsonArray{QJsonObject{
                                 {"id", project},
                                 {"name", "Example project"},
                                 {"root_path", "/example/workspace"},
                                 {"description", "A manual project panel"},
                                 {"version", 1},
                                 {"queue_version", 1},
                                 {"archived", false},
                                 {"created_at", time},
                                 {"updated_at", time}}}},
                {"tasks",
                 QJsonArray{QJsonObject{
                     {"id", task},
                     {"project_id", project},
                     {"title", "Design the first screen"},
                     {"description", "Keep actions explicit and data durable."},
                     {"acceptance_criteria",
                      "Two clients show conflicts without losing drafts."},
                     {"state", "backlog"},
                     {"version", 1},
                     {"created_at", time},
                     {"updated_at", time}}}},
                {"queues",
                 QJsonArray{QJsonObject{{"project_id", project},
                                        {"queue_version", 1},
                                        {"task_ids", QJsonArray{}}}}}})
            .toJson(QJsonDocument::Compact);
    server.listen(QHostAddress::LocalHost, 0);
    connect(&server, &QTcpServer::newConnection, this, [this] {
      while (auto *socket = server.nextPendingConnection()) {
        connect(socket, &QTcpSocket::disconnected, socket,
                &QObject::deleteLater);
        connect(socket, &QTcpSocket::readyRead, this, [this, socket] {
          const auto bytes =
              socket->property("input").toByteArray() + socket->readAll();
          socket->setProperty("input", bytes);
          const auto end = bytes.indexOf("\r\n\r\n");
          if (end < 0 || socket->property("handled").toBool())
            return;
          int length = 0;
          for (const auto &line : bytes.left(end).split('\n'))
            if (line.toLower().startsWith("content-length:"))
              length = line.mid(15).trimmed().toInt();
          if (bytes.size() < end + 4 + length)
            return;
          socket->setProperty("handled", true);
          if (!bytes.startsWith("GET"))
            mutations.append(bytes);
          if (handler)
            handler(socket, bytes);
          else
            normal(socket, bytes);
        });
      }
    });
  }
  QString tokenPath() const { return directory.filePath("owner.token"); }
  QUrl endpoint() const {
    return QUrl("http://127.0.0.1:" + QString::number(server.serverPort()));
  }
  static void respond(QTcpSocket *socket, int status, const QByteArray &body) {
    socket->write(
        "HTTP/1.1 " + QByteArray::number(status) +
        " Test\r\nContent-Type: application/json\r\nContent-Length: " +
        QByteArray::number(body.size()) + "\r\nConnection: close\r\n\r\n" +
        body);
    socket->disconnectFromHost();
  }
  void normal(QTcpSocket *socket, const QByteArray &request) {
    if (request.startsWith("GET /v1/snapshot "))
      respond(socket, 200, state);
    else if (request.startsWith("GET /v1/stream?"))
      socket->write("HTTP/1.1 200 OK\r\nContent-Type: "
                    "text/event-stream\r\n\r\n: connected\n\n");
    else if (request.startsWith("GET /v1/events?"))
      respond(
          socket, 200,
          QJsonDocument(
              QJsonObject{{"instance_id", instance},
                          {"events", QJsonArray{QJsonObject{
                                         {"seq", 25},
                                         {"action", "task.create"},
                                         {"project_id", project},
                                         {"task_id", task},
                                         {"source", "owner_manual"},
                                         {"timestamp", "2026-11-17T12:00:00Z"},
                                         {"before", QJsonObject{}},
                                         {"after", QJsonObject{}}}}},
                          {"next_cursor", 25}})
              .toJson());
    else
      respond(socket, 400,
              "{\"code\":\"invalid_request\",\"message\":\"safe\"}");
  }
};
