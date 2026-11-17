#include "apiclient.h"

#include <QDir>
#include <QJsonDocument>
#include <QJsonParseError>
#include <QNetworkProxy>
#include <QRegularExpression>
#include <QStandardPaths>
#include <QUrlQuery>
#include <QUuid>
#include <cmath>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

namespace {
constexpr qsizetype responseLimit = 32 * 1024 * 1024;
constexpr qsizetype frameLimit = 64 * 1024;
bool integer(const QJsonValue &value) {
  const double number = value.toDouble(-1);
  return value.isDouble() && number >= 0 && number <= 9007199254740991.0 &&
         std::floor(number) == number;
}
bool uuid(const QString &value) { return !QUuid(value).isNull(); }
bool validSnapshot(const QJsonObject &obj) {
  if (!uuid(obj.value("instance_id").toString()) ||
      !integer(obj.value("event_seq")))
    return false;
  for (const auto &collection : {"projects", "tasks", "queues"}) {
    if (!obj.value(collection).isArray())
      return false;
    for (const auto &row : obj.value(collection).toArray()) {
      if (!row.isObject())
        return false;
      const auto object = row.toObject();
      if (QString(collection) == "queues") {
        if (!uuid(object.value("project_id").toString()) ||
            !integer(object.value("queue_version")) ||
            !object.value("task_ids").isArray())
          return false;
        for (const auto &id : object.value("task_ids").toArray())
          if (!id.isString() || !uuid(id.toString()))
            return false;
      } else {
        if (!uuid(object.value("id").toString()) ||
            !integer(object.value("version")))
          return false;
        const bool project = QString(collection) == "projects";
        for (const auto &key :
             project
                 ? QStringList{"name", "root_path", "description", "created_at",
                               "updated_at"}
                 : QStringList{"title", "description", "acceptance_criteria",
                               "state", "created_at", "updated_at"})
          if (!object.value(key).isString())
            return false;
        if (project && (!object.value("archived").isBool() ||
                        !integer(object.value("queue_version"))))
          return false;
        if (!project && (!uuid(object.value("project_id").toString()) ||
                         !QStringList{"backlog", "queued", "in_progress",
                                      "done", "cancelled"}
                              .contains(object.value("state").toString())))
          return false;
      }
    }
  }
  return true;
}
// Open directory components without following symlinks, then validate the
// opened inode (not a racy QFileInfo check). Never expose bytes to QML.
QByteArray credential(const QString &path) {
  if (!QDir::isAbsolutePath(path))
    return {};
  int directory = ::open("/", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
  const auto parts = path.split('/', Qt::SkipEmptyParts);
  if (directory < 0 || parts.isEmpty()) {
    if (directory >= 0)
      ::close(directory);
    return {};
  }
  for (int i = 0; i < parts.size() - 1; ++i) {
    const int next = ::openat(directory, parts[i].toUtf8().constData(),
                              O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    ::close(directory);
    directory = next;
    if (directory < 0)
      return {};
  }
  struct stat parent{};
  if (::fstat(directory, &parent) != 0 || parent.st_uid != ::getuid() ||
      (parent.st_mode & 0777) != 0700) {
    ::close(directory);
    return {};
  }
  const int fd = ::openat(directory, parts.last().toUtf8().constData(),
                          O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
  ::close(directory);
  if (fd < 0)
    return {};
  struct stat info{};
  QByteArray result;
  if (::fstat(fd, &info) == 0 && S_ISREG(info.st_mode) &&
      info.st_uid == ::getuid() && (info.st_mode & 0777) == 0600 &&
      info.st_size >= 32 && info.st_size <= 256) {
    result.resize(info.st_size);
    if (::read(fd, result.data(), result.size()) != result.size())
      result.clear();
  }
  ::close(fd);
  result = result.trimmed();
  static const QRegularExpression valid("^[A-Za-z0-9_\\-+/=]{32,256}$");
  return valid.match(QString::fromLatin1(result)).hasMatch() ? result
                                                             : QByteArray{};
}
} // namespace

int ObjectModel::rowCount(const QModelIndex &parent) const {
  return parent.isValid() ? 0 : m_rows.size();
}
QVariant ObjectModel::data(const QModelIndex &index, int role) const {
  if (!index.isValid() || index.row() < 0 || index.row() >= m_rows.size() ||
      role != Qt::UserRole + 1)
    return {};
  return get(index.row());
}
QHash<int, QByteArray> ObjectModel::roleNames() const {
  return {{Qt::UserRole + 1, "record"}};
}
QVariantMap ObjectModel::get(int row) const {
  return row >= 0 && row < m_rows.size() ? m_rows[row].toObject().toVariantMap()
                                         : QVariantMap{};
}
void ObjectModel::replace(const QJsonArray &rows) {
  beginResetModel();
  m_rows = rows;
  endResetModel();
}
ApiClient::ApiClient(QObject *parent) : QObject(parent) {
  setObjectName("apiClient");
  m_projects.setObjectName("projectsModel");
  m_tasks.setObjectName("tasksModel");
  m_queues.setObjectName("queuesModel");
  m_network.setProxy(QNetworkProxy::NoProxy);
  m_reconnect.setSingleShot(true);
  m_reconnect.setInterval(1000);
  m_coalesce.setSingleShot(true);
  m_coalesce.setInterval(30);
  connect(&m_reconnect, &QTimer::timeout, this, &ApiClient::reload);
  connect(&m_coalesce, &QTimer::timeout, this, &ApiClient::reload);
}
ApiClient::~ApiClient() { stop(); }
QNetworkReply *ApiClient::ownerRequest(QNetworkAccessManager &network,
                                       const QByteArray &method,
                                       const QString &path,
                                       const QByteArray &body,
                                       const QByteArray &key) const {
  if (m_token.isEmpty() || !validEndpoint(m_endpoint))
    return nullptr;
  QUrl url = m_endpoint;
  url.setPath(path);
  QNetworkRequest req(url);
  req.setRawHeader("Authorization", "Bearer " + m_token);
  req.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
  req.setAttribute(QNetworkRequest::RedirectPolicyAttribute,
                   QNetworkRequest::ManualRedirectPolicy);
  if (!key.isEmpty())
    req.setRawHeader("Idempotency-Key", key);
  return network.sendCustomRequest(req, method, body);
}
QString ApiClient::defaultTokenPath() {
  return QStandardPaths::writableLocation(
             QStandardPaths::GenericConfigLocation) +
         "/artel/local-owner.token";
}
bool ApiClient::validEndpoint(const QUrl &url) {
  return url.isValid() && url.scheme() == "http" && url.host() == "127.0.0.1" &&
         url.port() > 0 && url.port() <= 65535 && url.userInfo().isEmpty() &&
         url.query().isEmpty() && !url.hasFragment() &&
         (url.path().isEmpty() || url.path() == "/");
}
bool ApiClient::configure(const QUrl &endpoint, const QString &tokenFile) {
  if (pending() || m_uncertain)
    return false;
  stop();
  m_token.clear();
  m_historyError.clear();
  m_projects.replace({});
  m_tasks.replace({});
  m_queues.replace({});
  m_instance.clear();
  m_sequence = m_cursor = 0;
  m_updated = {};
  if (!validEndpoint(endpoint)) {
    fail(tr("Invalid local endpoint"));
    return false;
  }
  m_token = credential(tokenFile);
  if (m_token.isEmpty()) {
    fail(tr("Owner credential unavailable or unsafe"));
    return false;
  }
  m_endpoint = endpoint;
  m_stopped = false;
  m_error.clear();
  m_status = "disconnected";
  emit changed();
  return true;
}
void ApiClient::setTimeout(int milliseconds) {
  m_timeout = qMax(10, milliseconds);
}
void ApiClient::cancel(QNetworkReply *&reply) {
  if (!reply)
    return;
  auto *old = reply;
  reply = nullptr;
  old->disconnect(this);
  old->abort();
  old->deleteLater();
}
void ApiClient::stop() {
  m_stopped = true;
  ++m_generation;
  m_reconnect.stop();
  m_coalesce.stop();
  m_reloadAgain = false;
  if (m_mutation)
    m_uncertain = true;
  cancel(m_snapshot);
  cancelHistory();
  cancel(m_stream);
  cancel(m_mutation);
  m_online = false;
  m_status = "disconnected";
  emit changed();
}
QNetworkReply *ApiClient::request(const QByteArray &method, const QString &path,
                                  const QByteArray &body,
                                  const QByteArray &key) {
  QUrl url = m_endpoint;
  const auto relative = QUrl(path);
  url.setPath(relative.path());
  url.setQuery(relative.query());
  QNetworkRequest req(url);
  req.setAttribute(QNetworkRequest::RedirectPolicyAttribute,
                   QNetworkRequest::ManualRedirectPolicy);
  req.setRawHeader("Authorization", "Bearer " + m_token);
  req.setRawHeader("Accept", "application/json");
  if (!key.isEmpty()) {
    req.setHeader(QNetworkRequest::ContentTypeHeader, "application/json");
    req.setRawHeader("Idempotency-Key", key);
  }
  auto *reply = m_network.sendCustomRequest(req, method, body);
  reply->setReadBufferSize(frameLimit);
  reply->setProperty("bytes", QByteArray{});
  auto *deadline = new QTimer(reply);
  deadline->setSingleShot(true);
  deadline->setInterval(m_timeout);
  connect(deadline, &QTimer::timeout, reply, [reply] { reply->abort(); });
  deadline->start();
  return reply;
}
void ApiClient::fail(const QString &message) {
  m_online = false;
  m_status = "stale";
  m_error = message;
  emit changed();
}
void ApiClient::reload() {
  if (m_stopped)
    return;
  if (m_snapshot) {
    m_reloadAgain = true;
    return;
  }
  m_reconnect.stop();
  m_status = "loading";
  auto *reply = request("GET", "/v1/snapshot");
  m_snapshot = reply;
  const auto generation = m_generation;
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray();
    bytes += reply->readAll();
    if (bytes.size() > responseLimit) {
      reply->setProperty("oversized", true);
      reply->abort();
      return;
    }
    reply->setProperty("bytes", bytes);
  });
  connect(reply, &QNetworkReply::finished, this,
          [this, reply, generation] { snapshotFinished(reply, generation); });
  emit changed();
}
void ApiClient::snapshotFinished(QNetworkReply *reply, quint64 generation) {
  reply->deleteLater();
  if (generation != m_generation || reply != m_snapshot)
    return;
  m_snapshot = nullptr;
  const auto bytes = reply->property("bytes").toByteArray();
  QJsonParseError parse;
  const auto doc = QJsonDocument::fromJson(bytes, &parse);
  const int code =
      reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
  if (code != 200 || reply->error() != QNetworkReply::NoError ||
      !reply->header(QNetworkRequest::ContentTypeHeader)
           .toString()
           .startsWith("application/json") ||
      reply->property("oversized").toBool() ||
      parse.error != QJsonParseError::NoError || !doc.isObject() ||
      !validSnapshot(doc.object())) {
    cancel(m_stream);
    fail(code == 401 ? tr("Owner authorization failed")
                     : tr("Snapshot unavailable or invalid"));
    m_reconnect.start();
    return;
  }
  const auto obj = doc.object();
  const auto instance = obj.value("instance_id").toString();
  const auto sequence = obj.value("event_seq").toInteger();
  if (instance == m_instance && sequence < m_sequence) {
    fail(tr("Outdated snapshot rejected"));
    m_reconnect.start();
    return;
  }
  if (instance != m_instance) {
    cancel(m_stream);
    m_cursor = 0;
  }
  m_instance = instance;
  m_sequence = sequence;
  m_projects.replace(obj.value("projects").toArray());
  m_tasks.replace(obj.value("tasks").toArray());
  m_queues.replace(obj.value("queues").toArray());
  m_updated = QDateTime::currentDateTimeUtc();
  m_online = true;
  m_error.clear();
  m_status = "online";
  if (!m_stream) {
    m_cursor = sequence;
    subscribe();
  }
  if (m_reloadAgain || m_cursor > m_sequence) {
    m_reloadAgain = false;
    m_coalesce.start();
  }
  emit changed();
}
void ApiClient::subscribe() {
  const QString path = "/v1/stream?instance_id=" + m_instance +
                       "&after=" + QString::number(m_cursor);
  auto *reply = request("GET", path);
  m_stream = reply;
  const auto generation = m_generation;
  // SSE heartbeats arrive every 15 seconds. Use an idle deadline, not an
  // overall stream deadline, and do not rely on Qt's per-transfer timeout.
  auto *idle = reply->findChild<QTimer *>();
  idle->setInterval(qMax(m_timeout, 20000));
  idle->start();
  connect(reply, &QIODevice::readyRead, this, [this, reply, generation, idle] {
    idle->start();
    streamReady(reply, generation);
  });
  connect(reply, &QNetworkReply::finished, this, [this, reply, generation] {
    reply->deleteLater();
    if (generation != m_generation || reply != m_stream)
      return;
    m_stream = nullptr;
    fail(tr("Connection lost; cached data is read-only"));
    m_reconnect.start();
  });
}
void ApiClient::streamReady(QNetworkReply *reply, quint64 generation) {
  if (generation != m_generation || reply != m_stream)
    return;
  if (reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() !=
          200 ||
      !reply->header(QNetworkRequest::ContentTypeHeader)
           .toString()
           .startsWith("text/event-stream")) {
    reply->abort();
    return;
  }
  auto buffer = reply->property("bytes").toByteArray() + reply->readAll();
  // Normalize CRLF only after complete lines, preserving chunk boundaries.
  buffer.replace("\r\n", "\n");
  qsizetype end = 0;
  while ((end = buffer.indexOf("\n\n")) >= 0) {
    if (end > frameLimit) {
      reply->abort();
      return;
    }
    const auto frame = buffer.left(end);
    buffer.remove(0, end + 2);
    QByteArray id, event, data;
    for (const auto &line : frame.split('\n')) {
      auto value = line.mid(line.indexOf(':') + 1);
      if (value.startsWith(' '))
        value.remove(0, 1);
      if (line.startsWith("id:"))
        id = value;
      else if (line.startsWith("event:"))
        event = value;
      else if (line.startsWith("data:"))
        data += value + '\n';
    }
    if (id.isEmpty() && event.isEmpty() && data.isEmpty())
      continue;
    bool ok = false;
    const auto seq = id.toLongLong(&ok);
    const auto doc = QJsonDocument::fromJson(data);
    if (!ok || seq <= m_cursor || event != "invalidation" || !doc.isObject() ||
        !doc.object().value("kind").isString() ||
        !uuid(doc.object().value("project_id").toString())) {
      reply->abort();
      return;
    }
    m_cursor = seq;
    if (!m_coalesce.isActive())
      m_coalesce.start();
  }
  if (buffer.size() > frameLimit) {
    reply->abort();
    return;
  }
  reply->setProperty("bytes", buffer);
}
void ApiClient::cancelHistory() { cancel(m_historyReply); }
void ApiClient::loadHistory(const QString &project, const QString &task,
                            qint64 after) {
  cancelHistory();
  if (!m_online || m_stopped || !uuid(project) ||
      (!task.isEmpty() && !uuid(task)))
    return;
  const auto instance = m_instance;
  auto *reply =
      request("GET", "/v1/events?instance_id=" + instance +
                         "&after=" + QString::number(after) +
                         "&limit=20&project_id=" + project +
                         (task.isEmpty() ? QString{} : "&task_id=" + task));
  m_historyReply = reply;
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray() + reply->readAll();
    if (bytes.size() > responseLimit) {
      reply->setProperty("oversized", true);
      reply->abort();
    } else {
      reply->setProperty("bytes", bytes);
    }
  });
  connect(
      reply, &QNetworkReply::finished, this, [this, reply, instance, after] {
        reply->deleteLater();
        if (reply != m_historyReply)
          return;
        m_historyReply = nullptr;
        const auto obj =
            QJsonDocument::fromJson(reply->property("bytes").toByteArray())
                .object();
        const auto events = obj.value("events");
        bool valid = reply->error() == QNetworkReply::NoError &&
                     reply->attribute(QNetworkRequest::HttpStatusCodeAttribute)
                             .toInt() == 200 &&
                     reply->header(QNetworkRequest::ContentTypeHeader)
                         .toString()
                         .startsWith("application/json") &&
                     !reply->property("oversized").toBool() &&
                     instance == m_instance &&
                     obj.value("instance_id").toString() == instance &&
                     events.isArray() && integer(obj.value("next_cursor")) &&
                     obj.value("next_cursor").toInteger() >= after &&
                     events.toArray().size() <= 20;
        qint64 previous = after;
        for (const auto &event : events.toArray()) {
          const auto row = event.toObject();
          valid = valid && integer(row.value("seq")) &&
                  row.value("seq").toInteger() > previous &&
                  row.value("seq").toInteger() <=
                      obj.value("next_cursor").toInteger() &&
                  uuid(row.value("project_id").toString()) &&
                  row.value("action").isString() &&
                  row.value("source").toString() == "owner_manual" &&
                  row.value("timestamp").isString() &&
                  row.value("before").isObject() &&
                  row.value("after").isObject();
          previous = row.value("seq").toInteger();
        }
        if (valid) {
          m_historyError.clear();
          emit historyReceived(events.toArray().toVariantList(),
                               obj.value("next_cursor").toInteger(),
                               obj.value("next_cursor").toInteger() <
                                   m_sequence);
        } else {
          m_historyError = tr("History unavailable or invalid");
        }
        emit changed();
      });
  emit changed();
}
void ApiClient::setDraft(const QVariantMap &draft) {
  m_draft = draft;
  ++m_draftRevision;
  emit changed();
}
void ApiClient::discardDraft() {
  if (pending() || m_uncertain)
    return;
  m_draft.clear();
  ++m_draftRevision;
  m_conflict = false;
  emit changed();
}
bool ApiClient::save(const QString &method, const QString &path,
                     const QVariantMap &payload) {
  if (!m_online || m_stopped || pending() || m_uncertain)
    return false;
  static const QRegularExpression route(
      "^/v1/(projects|tasks)(/[0-9a-fA-F-]{36})?(/tasks|/queue|/transition)?$");
  if (!QStringList{"POST", "PATCH", "PUT"}.contains(method) ||
      !route.match(path).hasMatch())
    return false;
  const auto body = QJsonDocument(QJsonObject::fromVariantMap(payload))
                        .toJson(QJsonDocument::Compact);
  if (body.size() > 256 * 1024)
    return false;
  m_method = method.toLatin1();
  m_path = path;
  m_body = body;
  m_key = QUuid::createUuid().toString(QUuid::WithoutBraces).toLatin1();
  m_mutationInstance = m_instance;
  m_sentRevision = m_draftRevision;
  m_conflict = false;
  sendMutation();
  return true;
}
bool ApiClient::retry() {
  if (!m_uncertain || !m_online || pending() || m_stopped ||
      m_mutationInstance != m_instance)
    return false;
  sendMutation();
  return true;
}
bool ApiClient::abandonUncertain() {
  if (!canAbandon())
    return false;
  m_uncertain = false;
  m_conflict = false;
  m_method.clear();
  m_path.clear();
  m_body.clear();
  m_key.clear();
  m_mutationInstance.clear();
  m_error.clear();
  emit changed();
  return true;
}
void ApiClient::sendMutation() {
  auto *reply = request(m_method, m_path, m_body, m_key);
  m_mutation = reply;
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray() + reply->readAll();
    if (bytes.size() > responseLimit) {
      reply->setProperty("oversized", true);
      reply->abort();
      return;
    }
    reply->setProperty("bytes", bytes);
  });
  connect(reply, &QNetworkReply::finished, this, [this, reply] {
    reply->deleteLater();
    if (reply != m_mutation)
      return;
    m_mutation = nullptr;
    const int code =
        reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
    const auto doc =
        QJsonDocument::fromJson(reply->property("bytes").toByteArray());
    const auto obj = doc.object();
    const bool validResult =
        m_path.endsWith("/queue")
            ? uuid(obj.value("project_id").toString()) &&
                  integer(obj.value("queue_version")) &&
                  obj.value("task_ids").isArray()
            : uuid(obj.value("id").toString()) && integer(obj.value("version"));
    if (code >= 200 && code < 300 && reply->error() == QNetworkReply::NoError &&
        reply->header(QNetworkRequest::ContentTypeHeader)
            .toString()
            .startsWith("application/json") &&
        !reply->property("oversized").toBool() && validResult) {
      m_uncertain = false;
      m_conflict = false;
      if (m_draftRevision == m_sentRevision)
        m_draft.clear();
      m_error.clear();
      emit mutationSucceeded(obj.toVariantMap(), m_mutationInstance);
      reload();
    } else {
      // A transport error, 5xx, redirect or unusable success can follow commit.
      m_uncertain = code == 0 || code >= 500 || (code >= 200 && code < 400) ||
                    reply->error() == QNetworkReply::OperationCanceledError;
      m_conflict = code == 409;
      m_error =
          m_uncertain ? tr("Save outcome unknown; retry the same request")
          : m_conflict
              ? (dirty()
                     ? tr("Conflict; draft preserved. Reload before editing "
                          "again")
                     : tr("Conflict; reload before trying the action again"))
              : tr("Save rejected; draft preserved");
      if (code == 401) {
        fail(tr("Owner authorization failed; draft preserved"));
        m_reconnect.start();
      }
    }
    emit changed();
  });
  emit changed();
}
