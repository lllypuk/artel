#include "apiclient.h"

#include <QDir>
#include <QJsonDocument>
#include <QJsonParseError>
#include <QNetworkProxy>
#include <QRegularExpression>
#include <QStandardPaths>
#include <QUrlQuery>
#include <QUuid>
#include <algorithm>
#include <cmath>
#include <fcntl.h>
#include <optional>
#include <sys/stat.h>
#include <unistd.h>

namespace {
constexpr qsizetype responseLimit = 32 * 1024 * 1024;
// Match the daemon's migrated-snapshot budget. Other bounded reads retain the
// original limit and history explicitly retries oversized pages below it.
constexpr qsizetype snapshotResponseLimit = 64 * 1024 * 1024;
constexpr qsizetype frameLimit = 64 * 1024;
constexpr qsizetype invalidationLimit = 1024;
bool integer(const QJsonValue &value) {
  const double number = value.toDouble(-1);
  return value.isDouble() && number >= 0 && number <= 9007199254740991.0 &&
         std::floor(number) == number;
}
bool uuid(const QString &value) { return !QUuid(value).isNull(); }
bool oneOf(const QString &value, const QStringList &options) {
  return options.contains(value);
}
bool validEpicSummary(const QJsonValue &value) {
  if (!value.isObject())
    return false;
  const auto summary = value.toObject();
  for (const auto *key :
       {"backlog", "queued", "in_progress", "done", "cancelled"})
    if (!integer(summary.value(key)))
      return false;
  return true;
}
bool validWorkType(const QJsonObject &object) {
  const auto kind = object.value("kind");
  if (!kind.isString() || !oneOf(kind.toString(), {"epic", "task", "bug"}))
    return false;
  const auto state = object.value("state").toString();
  const bool epic = kind == "epic";
  if (epic ? !oneOf(state, {"open", "in_progress", "closed"})
           : !oneOf(state,
                    {"backlog", "queued", "in_progress", "done", "cancelled"}))
    return false;
  const auto parent = object.value("parent_id");
  if (epic ? !parent.isUndefined()
           : !(parent.isUndefined() ||
               (parent.isString() && uuid(parent.toString()))))
    return false;
  const bool bug = kind == "bug";
  for (const auto *key :
       {"expected_behavior", "actual_behavior", "fix_evidence"}) {
    const auto field = object.value(key);
    if (bug ? !(field.isUndefined() || field.isString()) : !field.isUndefined())
      return false;
  }
  const auto summary = object.value("summary");
  return epic ? validEpicSummary(summary) : summary.isUndefined();
}
bool validSnapshot(const QJsonObject &obj) {
  if (!uuid(obj.value("instance_id").toString()) ||
      !integer(obj.value("event_seq")))
    return false;
  const auto advertised = obj.value("work_types_version");
  const bool workTypes = !advertised.isUndefined();
  if (workTypes && (!integer(advertised) || advertised.toInt() != 1))
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
        if (!project && !uuid(object.value("project_id").toString()))
          return false;
        if (!project && workTypes && !validWorkType(object))
          return false;
        if (!project && !workTypes) {
          if (!oneOf(object.value("state").toString(),
                     {"backlog", "queued", "in_progress", "done", "cancelled"}))
            return false;
          // A markerless response is the v6 contract. Do not let unvalidated
          // v7 fields reach Panel and enable Epic/Bug behavior accidentally.
          for (const auto *key : {"kind", "parent_id", "expected_behavior",
                                  "actual_behavior", "fix_evidence", "summary"})
            if (!object.value(key).isUndefined())
              return false;
        }
      }
    }
  }
  return true;
}
bool fullSHA(const QString &value) {
  static const QRegularExpression format(QStringLiteral("^[0-9a-f]{40}$"));
  return format.match(value).hasMatch();
}
bool validResult(const QJsonObject &row, const QString &task) {
  return uuid(row.value("id").toString()) &&
         row.value("task_id").toString() == task &&
         !row.value("repo").toString().isEmpty() &&
         fullSHA(row.value("base").toString()) &&
         fullSHA(row.value("head").toString()) &&
         row.value("summary").isString() && row.value("created_at").isString();
}
bool validReview(const QJsonObject &row, const QString &task) {
  if (!uuid(row.value("id").toString()) ||
      row.value("task_id").toString() != task ||
      !uuid(row.value("result_id").toString()) ||
      row.value("repo").toString().isEmpty() ||
      !fullSHA(row.value("base").toString()) ||
      !fullSHA(row.value("head").toString()) ||
      row.value("revmux_task").toString().isEmpty() ||
      row.value("run").toString().isEmpty() ||
      row.value("range_source").toString() != "operator" ||
      row.value("report_ref").toString().isEmpty() ||
      !row.value("complete").isBool() ||
      !row.value("evidence_consistent").isBool() ||
      !row.value("required_sources").isArray() ||
      !row.value("sources").isArray() ||
      !row.value("incomplete_reasons").isArray() ||
      !row.value("created_at").isString())
    return false;
  for (const auto *key : {"findings", "pre_existing", "open_questions"})
    if (!integer(row.value(key)))
      return false;
  for (const auto &value : row.value("required_sources").toArray()) {
    const auto source = value.toObject();
    if (!value.isObject() || source.value("name").toString().isEmpty() ||
        source.value("executor").toString().isEmpty())
      return false;
  }
  for (const auto &value : row.value("sources").toArray()) {
    const auto source = value.toObject();
    if (!value.isObject() || source.value("name").toString().isEmpty() ||
        source.value("executor").toString().isEmpty() ||
        !source.value("required").isBool() ||
        !source.value("responded").isBool() ||
        !source.value("degraded").isBool())
      return false;
    for (const auto *key : {"findings", "pre_existing", "open_questions"})
      if (!integer(source.value(key)))
        return false;
  }
  for (const auto &reason : row.value("incomplete_reasons").toArray())
    if (!reason.isString() || reason.toString().isEmpty())
      return false;
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
void ObjectModel::update(int row, const QVariantMap &record) {
  const auto value = QJsonObject::fromVariantMap(record);
  if (m_rows[row] == value)
    return;
  m_rows[row] = value;
  emit dataChanged(index(row, 0), index(row, 0), {Qt::UserRole + 1});
}
void ObjectModel::append(const QVariantMap &record) {
  const int row = m_rows.size();
  beginInsertRows({}, row, row);
  m_rows.append(QJsonObject::fromVariantMap(record));
  endInsertRows();
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
  m_resultsError.clear();
  m_messagesError.clear();
  m_projects.replace({});
  m_tasks.replace({});
  m_queues.replace({});
  m_instance.clear();
  m_workTypesVersion = 0;
  m_sequence = m_cursor = 0;
  m_pendingInvalidations.clear();
  m_snapshotInvalidations.clear();
  m_snapshotScopeKnown = false;
  m_invalidationsOverflowed = false;
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
  cancelResults();
  cancelMessages();
  cancelQuestions();
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
  reply->setProperty("snapshotRequestSerial", ++m_snapshotRequestSerial);
  m_snapshot = reply;
  const auto generation = m_generation;
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray();
    bytes += reply->readAll();
    if (bytes.size() > snapshotResponseLimit) {
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
  const auto previousSequence = m_sequence;
  if (instance == m_instance && sequence < m_sequence) {
    fail(tr("Outdated snapshot rejected"));
    m_reconnect.start();
    return;
  }
  const bool sameInstance = instance == m_instance;
  const bool hadStream = m_stream != nullptr;
  m_snapshotInvalidations.clear();
  m_snapshotScopeKnown = sameInstance && hadStream &&
                         !m_invalidationsOverflowed && sequence <= m_cursor;
  if (sameInstance && hadStream) {
    for (const auto &event : m_pendingInvalidations)
      if (event.sequence > previousSequence && event.sequence <= sequence)
        m_snapshotInvalidations.append(event);
  }
  if (!sameInstance || !hadStream) {
    m_pendingInvalidations.clear();
    m_invalidationsOverflowed = false;
  } else {
    m_pendingInvalidations.erase(std::remove_if(m_pendingInvalidations.begin(),
                                                m_pendingInvalidations.end(),
                                                [sequence](const auto &event) {
                                                  return event.sequence <=
                                                         sequence;
                                                }),
                                 m_pendingInvalidations.end());
    if (m_pendingInvalidations.isEmpty() && m_cursor <= sequence)
      m_invalidationsOverflowed = false;
  }
  if (instance != m_instance) {
    cancel(m_stream);
    m_cursor = 0;
  }
  m_instance = instance;
  m_workTypesVersion = obj.value("work_types_version").toInt();
  if (instance == m_mutationInstance &&
      !reply->property("beforeAnswerRetryCheck").toBool())
    m_answerRetryNeedsSnapshot = false;
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
  m_appliedSnapshotRequestSerial =
      reply->property("snapshotRequestSerial").toULongLong();
  emit changed();
}
bool ApiClient::snapshotAffectsTask(const QString &project,
                                    const QString &task) const {
  for (const auto &event : m_snapshotInvalidations)
    if (event.task == task ||
        (event.task.isEmpty() && event.project == project))
      return true;
  return false;
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
    const auto object = doc.object();
    const auto task = object.value("task_id");
    if (!ok || seq != m_cursor + 1 || event != "invalidation" ||
        !doc.isObject() || !object.value("kind").isString() ||
        !uuid(object.value("project_id").toString()) ||
        (!task.isUndefined() && !uuid(task.toString()))) {
      reply->abort();
      return;
    }
    if (m_pendingInvalidations.size() < invalidationLimit)
      m_pendingInvalidations.append(
          {seq, object.value("project_id").toString(), task.toString()});
    else
      m_invalidationsOverflowed = true;
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
                            qint64 after, int limit) {
  cancelHistory();
  if (!m_online || m_stopped || !uuid(project) || limit < 1 || limit > 20 ||
      (!task.isEmpty() && !uuid(task)))
    return;
  const auto instance = m_instance;
  auto *reply =
      request("GET", "/v1/events?instance_id=" + instance +
                         "&after=" + QString::number(after) + "&limit=" +
                         QString::number(limit) + "&project_id=" + project +
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
  connect(reply, &QNetworkReply::finished, this,
          [this, reply, instance, project, task, after, limit] {
            reply->deleteLater();
            if (reply != m_historyReply)
              return;
            m_historyReply = nullptr;
            const auto obj =
                QJsonDocument::fromJson(reply->property("bytes").toByteArray())
                    .object();
            const auto events = obj.value("events");
            bool valid =
                reply->error() == QNetworkReply::NoError &&
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
                events.toArray().size() <= limit;
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
            } else if (instance == m_instance &&
                       reply->property("oversized").toBool() && limit > 1) {
              // A page can be oversized even though every individual audit
              // value is legal. Halve only the encoded page, preserving
              // cursor/filters.
              loadHistory(project, task, after, limit / 2);
              return;
            } else {
              m_historyError = tr("History unavailable or invalid");
            }
            emit changed();
          });
  emit changed();
}
void ApiClient::loadCurrentResult(const QString &task) {
  cancel(m_currentResultReply);
  cancel(m_currentReviewsReply);
  m_currentReviewIDs.clear();
  m_currentResultRecord = {};
  m_currentResultTask.clear();
  m_currentResultError.clear();
  if (!m_online || m_stopped || !uuid(task))
    return;
  const auto instance = m_instance;
  auto *reply = request("GET", "/v1/tasks/" + task + "/results/current");
  m_currentResultReply = reply;
  m_currentResultTask = task;
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray() + reply->readAll();
    if (bytes.size() > responseLimit) {
      reply->setProperty("oversized", true);
      reply->abort();
    } else {
      reply->setProperty("bytes", bytes);
    }
  });
  connect(reply, &QNetworkReply::finished, this, [this, reply, task, instance] {
    reply->deleteLater();
    if (reply != m_currentResultReply)
      return;
    m_currentResultReply = nullptr;
    if (instance != m_instance || task != m_currentResultTask) {
      emit changed();
      return;
    }
    const auto obj =
        QJsonDocument::fromJson(reply->property("bytes").toByteArray())
            .object();
    const auto resultValue = obj.value("result");
    const auto result = resultValue.toObject();
    const bool valid =
        reply->error() == QNetworkReply::NoError &&
        reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() ==
            200 &&
        reply->header(QNetworkRequest::ContentTypeHeader)
            .toString()
            .startsWith("application/json") &&
        !reply->property("oversized").toBool() &&
        (resultValue.isNull() ||
         (resultValue.isObject() && validResult(result, task))) &&
        obj.value("reviews").isArray() &&
        obj.value("reviews").toArray().isEmpty();
    if (valid) {
      m_currentResultError.clear();
      m_currentResultRecord = result;
      emit currentResultReceived(resultValue.isNull() ? QVariantMap{}
                                                      : result.toVariantMap());
      if (!resultValue.isNull())
        loadCurrentReviews(task, result.value("id").toString(), {});
    } else {
      m_currentResultError = tr("Submitted result unavailable or invalid");
    }
    emit changed();
  });
  emit changed();
}
void ApiClient::loadCurrentReviews(const QString &task, const QString &result,
                                   const QString &after) {
  cancel(m_currentReviewsReply);
  if (!m_online || m_stopped || task != m_currentResultTask || !uuid(result) ||
      (!after.isEmpty() && !uuid(after)))
    return;
  const auto instance = m_instance;
  auto *reply = request("GET", "/v1/tasks/" + task +
                                   "/results/current/reviews?after=" + after +
                                   "&limit=20");
  m_currentReviewsReply = reply;
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
      reply, &QNetworkReply::finished, this,
      [this, reply, task, result, after, instance] {
        reply->deleteLater();
        if (reply != m_currentReviewsReply)
          return;
        m_currentReviewsReply = nullptr;
        if (instance != m_instance || task != m_currentResultTask) {
          emit changed();
          return;
        }
        const auto obj =
            QJsonDocument::fromJson(reply->property("bytes").toByteArray())
                .object();
        const auto reviews = obj.value("reviews").toArray();
        const auto next = obj.value("next_after").toString();
        const auto more = obj.value("has_more");
        bool valid =
            reply->error() == QNetworkReply::NoError &&
            reply->attribute(QNetworkRequest::HttpStatusCodeAttribute)
                    .toInt() == 200 &&
            reply->header(QNetworkRequest::ContentTypeHeader)
                .toString()
                .startsWith("application/json") &&
            !reply->property("oversized").toBool() &&
            obj.value("result_id").toString() == result &&
            obj.value("reviews").isArray() && reviews.size() <= 20 &&
            obj.value("next_after").isString() &&
            (next.isEmpty() || uuid(next)) && more.isBool() &&
            (!more.toBool() || reviews.size() == 20) &&
            (reviews.isEmpty()
                 ? next == after
                 : next != after &&
                       next ==
                           reviews.last().toObject().value("id").toString());
        // The current read supplied this candidate; do not trust a page
        // that advanced to another result between requests.
        valid = valid && m_currentResultRecord.value("id").toString() == result;
        QSet<QString> pageIDs;
        for (const auto &value : reviews) {
          const auto review = value.toObject();
          const auto id = review.value("id").toString();
          valid = valid && value.isObject() && validReview(review, task) &&
                  !m_currentReviewIDs.contains(id) && !pageIDs.contains(id) &&
                  review.value("result_id").toString() == result &&
                  review.value("repo") == m_currentResultRecord.value("repo") &&
                  review.value("base") == m_currentResultRecord.value("base") &&
                  review.value("head") == m_currentResultRecord.value("head");
          pageIDs.insert(id);
        }
        if (valid) {
          m_currentReviewIDs.unite(pageIDs);
          m_currentResultError.clear();
          emit currentReviewsReceived(result, reviews.toVariantList(), after,
                                      next, more.toBool());
        } else {
          m_currentResultError =
              tr("Submitted result reviews unavailable or invalid");
        }
        emit changed();
      });
  emit changed();
}
void ApiClient::cancelResults() {
  cancel(m_currentResultReply);
  cancel(m_currentReviewsReply);
  m_currentReviewIDs.clear();
  m_currentResultRecord = {};
  m_currentResultTask.clear();
  m_currentResultError.clear();
  cancel(m_resultsReply);
  m_resultsTask.clear();
  m_resultsError.clear();
}
void ApiClient::loadResults(const QString &task, qint64 after) {
  cancel(m_resultsReply);
  m_resultsTask.clear();
  m_resultsError.clear();
  if (!m_online || m_stopped || !uuid(task) || after < 0)
    return;
  const auto instance = m_instance;
  auto *reply = request("GET", "/v1/tasks/" + task + "/results?after=" +
                                   QString::number(after) + "&limit=50");
  m_resultsReply = reply;
  m_resultsTask = task;
  m_resultsError.clear();
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray() + reply->readAll();
    if (bytes.size() > responseLimit) {
      reply->setProperty("oversized", true);
      reply->abort();
    } else {
      reply->setProperty("bytes", bytes);
    }
  });
  connect(reply, &QNetworkReply::finished, this,
          [this, reply, task, after, instance] {
            reply->deleteLater();
            if (reply != m_resultsReply)
              return;
            m_resultsReply = nullptr;
            if (instance != m_instance || task != m_resultsTask) {
              emit changed();
              return;
            }
            const auto obj =
                QJsonDocument::fromJson(reply->property("bytes").toByteArray())
                    .object();
            const auto entries = obj.value("entries").toArray();
            const auto current = obj.value("current_result_id").toString();
            const auto next = obj.value("next_after");
            const auto more = obj.value("has_more");
            bool valid =
                reply->error() == QNetworkReply::NoError &&
                reply->attribute(QNetworkRequest::HttpStatusCodeAttribute)
                        .toInt() == 200 &&
                reply->header(QNetworkRequest::ContentTypeHeader)
                    .toString()
                    .startsWith("application/json") &&
                !reply->property("oversized").toBool() &&
                obj.value("current_result_id").isString() &&
                (current.isEmpty() || uuid(current)) &&
                obj.value("entries").isArray() && entries.size() <= 50 &&
                integer(next) && next.toInteger() >= after && more.isBool() &&
                (!more.toBool() || entries.size() == 50);
            qint64 previous = after;
            for (const auto &value : entries) {
              const auto entry = value.toObject();
              const auto kind = entry.value("kind").toString();
              const auto seq = entry.value("seq");
              valid = valid && value.isObject() && integer(seq) &&
                      seq.toInteger() > previous &&
                      seq.toInteger() <= next.toInteger() &&
                      ((kind == "result" && entry.value("result").isObject() &&
                        validResult(entry.value("result").toObject(), task)) ||
                       (kind == "review" && entry.value("review").isObject() &&
                        validReview(entry.value("review").toObject(), task)));
              previous = seq.toInteger();
            }
            if (valid && ((entries.isEmpty() && next.toInteger() == after) ||
                          (!entries.isEmpty() && next.toInteger() > after))) {
              m_resultsError.clear();
              emit resultsReceived(current, entries.toVariantList(), after,
                                   next.toInteger(), more.toBool());
            } else {
              m_resultsError = tr("Submitted results unavailable or invalid");
            }
            emit changed();
          });
  emit changed();
}
bool ApiClient::validMessage(const QJsonObject &obj) const {
  return uuid(obj.value("id").toString()) &&
         uuid(obj.value("task_id").toString()) &&
         uuid(obj.value("delivery_binding_id").toString()) &&
         obj.value("text").isString() &&
         oneOf(obj.value("status").toString(),
               {QStringLiteral("stored"), QStringLiteral("unknown"),
                QStringLiteral("observed")}) &&
         obj.value("blocked").isBool() && obj.value("created_at").isString();
}
void ApiClient::cancelMessages() {
  cancel(m_messagesReply);
  cancel(m_statusReply);
  cancel(m_deliveryReply);
  m_messagesPageLoaded = false;
}
void ApiClient::cancelQuestions() {
  cancel(m_questionsReply);
  cancel(m_questionStatusReply);
}
void ApiClient::loadMessages(const QString &task, qint64 after) {
  cancel(m_messagesReply);
  if (!m_online || m_stopped || !uuid(task) || after < 0)
    return;
  const auto instance = m_instance;
  auto *reply = request("GET", "/v1/tasks/" + task + "/messages?after=" +
                                   QString::number(after) + "&limit=20");
  m_messagesReply = reply;
  m_messagesTask = task;
  if (after == 0)
    m_messagesPageLoaded = false;
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray() + reply->readAll();
    if (bytes.size() > responseLimit) {
      reply->setProperty("oversized", true);
      reply->abort();
      return;
    }
    reply->setProperty("bytes", bytes);
  });
  connect(reply, &QNetworkReply::finished, this,
          [this, reply, task, after, instance] {
            reply->deleteLater();
            if (reply != m_messagesReply)
              return;
            m_messagesReply = nullptr;
            const auto obj =
                QJsonDocument::fromJson(reply->property("bytes").toByteArray())
                    .object();
            const auto rows = obj.value("messages").toArray();
            bool valid =
                reply->error() == QNetworkReply::NoError &&
                reply->attribute(QNetworkRequest::HttpStatusCodeAttribute)
                        .toInt() == 200 &&
                reply->header(QNetworkRequest::ContentTypeHeader)
                    .toString()
                    .startsWith("application/json") &&
                !reply->property("oversized").toBool() &&
                instance == m_instance && task == m_messagesTask &&
                rows.size() <= 20 && integer(obj.value("next_after")) &&
                obj.value("next_after").toInteger() >= after;
            for (const auto &row : rows) {
              const auto message = row.toObject();
              valid = valid && validMessage(message) &&
                      message.value("task_id").toString() == task;
            }
            if (valid) {
              m_messagesError.clear();
              emit messagesReceived(rows.toVariantList(),
                                    obj.value("next_after").toInteger(),
                                    rows.size() == 20);
            } else {
              m_messagesError = tr("Commissions unavailable or invalid");
              // A failed first page must not hide history until the task is
              // reselected: retry the same page once shortly, if still current.
              if (after == 0 && instance == m_instance &&
                  task == m_messagesTask && m_online && !m_stopped) {
                const auto retryTask = task;
                QTimer::singleShot(2000, this, [this, retryTask] {
                  if (m_online && !m_stopped && m_messagesTask == retryTask &&
                      m_messagesReply == nullptr)
                    loadMessages(retryTask, 0);
                });
              }
            }
            emit changed();
          });
  emit changed();
}
void ApiClient::refreshMessageStatuses(const QString &task,
                                       const QStringList &ids) {
  cancel(m_statusReply);
  if (!m_online || m_stopped || !uuid(task) || ids.isEmpty())
    return;
  QStringList checked;
  for (const auto &id : ids) {
    if (!uuid(id))
      return;
    checked.append(id);
  }
  // Status polling stays bounded: refresh the newest commissions first so a
  // long history never freezes the visible rows.
  if (checked.size() > 50)
    checked = checked.sliced(checked.size() - 50);
  const auto instance = m_instance;
  auto *reply = request("GET", "/v1/tasks/" + task +
                                   "/messages?ids=" + checked.join(','));
  m_statusReply = reply;
  m_statusTask = task;
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray() + reply->readAll();
    if (bytes.size() > responseLimit) {
      reply->setProperty("oversized", true);
      reply->abort();
      return;
    }
    reply->setProperty("bytes", bytes);
  });
  connect(reply, &QNetworkReply::finished, this, [this, reply, task, instance] {
    reply->deleteLater();
    if (reply != m_statusReply)
      return;
    m_statusReply = nullptr;
    const auto obj =
        QJsonDocument::fromJson(reply->property("bytes").toByteArray())
            .object();
    const auto rows = obj.value("messages").toArray();
    bool valid =
        reply->error() == QNetworkReply::NoError &&
        reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() ==
            200 &&
        reply->header(QNetworkRequest::ContentTypeHeader)
            .toString()
            .startsWith("application/json") &&
        !reply->property("oversized").toBool() && instance == m_instance &&
        task == m_statusTask && rows.size() <= 50;
    for (const auto &row : rows)
      valid = valid && validMessage(row.toObject()) &&
              row.toObject().value("task_id").toString() == task;
    if (valid) {
      m_messagesError.clear();
      emit messageStatusesReceived(rows.toVariantList());
    } else {
      m_messagesError = tr("Commission status unavailable or invalid");
      // Keep the polling loop alive: one failed read must not freeze statuses.
      emit messageStatusesReceived({});
    }
    emit changed();
  });
  emit changed();
}
void ApiClient::loadDelivery(const QString &task) {
  cancel(m_deliveryReply);
  if (!m_online || m_stopped || !uuid(task))
    return;
  const auto instance = m_instance;
  auto *reply = request("GET", "/v1/tasks/" + task + "/delivery");
  m_deliveryReply = reply;
  m_deliveryTask = task;
  connect(reply, &QIODevice::readyRead, this, [reply] {
    auto bytes = reply->property("bytes").toByteArray() + reply->readAll();
    if (bytes.size() > responseLimit) {
      reply->setProperty("oversized", true);
      reply->abort();
      return;
    }
    reply->setProperty("bytes", bytes);
  });
  connect(reply, &QNetworkReply::finished, this, [this, reply, task, instance] {
    reply->deleteLater();
    if (reply != m_deliveryReply)
      return;
    m_deliveryReply = nullptr;
    const auto obj =
        QJsonDocument::fromJson(reply->property("bytes").toByteArray())
            .object();
    const auto binding = obj.value("binding");
    bool valid =
        reply->error() == QNetworkReply::NoError &&
        reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() ==
            200 &&
        reply->header(QNetworkRequest::ContentTypeHeader)
            .toString()
            .startsWith("application/json") &&
        !reply->property("oversized").toBool() && instance == m_instance &&
        task == m_deliveryTask && obj.value("task_id").toString() == task &&
        uuid(obj.value("project_id").toString()) &&
        (binding.isNull() ||
         (binding.isObject() &&
          uuid(binding.toObject().value("delivery_binding_id").toString()) &&
          uuid(binding.toObject().value("observer_id").toString()) &&
          uuid(binding.toObject().value("runtime_id").toString()) &&
          uuid(binding.toObject().value("project_id").toString()) &&
          uuid(binding.toObject().value("pi_session_id").toString()) &&
          uuid(binding.toObject().value("journal_id").toString()) &&
          integer(binding.toObject().value("generation")) &&
          oneOf(binding.toObject().value("status").toString(),
                {QStringLiteral("pending"), QStringLiteral("active"),
                 QStringLiteral("revoked")}) &&
          binding.toObject().value("created_at").isString() &&
          binding.toObject().value("writer_id").toString().isEmpty() &&
          (!binding.toObject().contains("allow_owner_questions") ||
           binding.toObject().value("allow_owner_questions").isBool()) &&
          obj.value("ready").isBool()));
    if (valid) {
      m_messagesError.clear();
      emit deliveryReceived(obj.toVariantMap());
    } else {
      m_messagesError = tr("Assignment unavailable or invalid");
    }
    emit changed();
  });
  emit changed();
}
namespace {
bool validQuestion(const QJsonObject &obj) {
  if (!uuid(obj.value("id").toString()) ||
      !uuid(obj.value("task_id").toString()) ||
      !uuid(obj.value("delivery_binding_id").toString()) ||
      !uuid(obj.value("pi_session_id").toString()) ||
      !obj.value("text").isString() ||
      !oneOf(obj.value("status").toString(),
             {QStringLiteral("open"), QStringLiteral("answered"),
              QStringLiteral("closed")}) ||
      !obj.value("created_at").isString())
    return false;
  if (obj.value("status").toString() != "answered")
    return true;
  return uuid(obj.value("answer_message_id").toString()) &&
         obj.value("answer_text").isString() &&
         oneOf(obj.value("answer_status").toString(),
               {QStringLiteral("stored"), QStringLiteral("unknown"),
                QStringLiteral("observed")}) &&
         obj.value("answered_at").isString();
}

void bufferQuestionResponse(QNetworkReply *reply, QObject *context) {
  QObject::connect(reply, &QIODevice::readyRead, context, [reply] {
    auto bytes = reply->property("bytes").toByteArray() + reply->readAll();
    if (bytes.size() > 320 * 1024) {
      reply->setProperty("oversized", true);
      reply->abort();
      return;
    }
    reply->setProperty("bytes", bytes);
  });
}

struct QuestionResponse {
  QJsonArray rows;
  qint64 next;
  bool more;
};

std::optional<QuestionResponse> readQuestionResponse(QNetworkReply *reply,
                                                     const QString &task) {
  if (reply->error() != QNetworkReply::NoError ||
      reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() !=
          200 ||
      !reply->header(QNetworkRequest::ContentTypeHeader)
           .toString()
           .startsWith("application/json") ||
      reply->property("oversized").toBool())
    return std::nullopt;
  const auto obj =
      QJsonDocument::fromJson(reply->property("bytes").toByteArray()).object();
  const auto questions = obj.value("questions");
  const auto rows = questions.toArray();
  const auto next = obj.value("next_after");
  const auto more = obj.value("has_more");
  if (!questions.isArray() || rows.size() > 5 || !integer(next) ||
      !more.isBool() || (more.toBool() && rows.size() != 5))
    return std::nullopt;
  for (const auto &row : rows) {
    const auto question = row.toObject();
    if (!validQuestion(question) ||
        question.value("task_id").toString() != task)
      return std::nullopt;
  }
  return QuestionResponse{rows, next.toInteger(), more.toBool()};
}
} // namespace

void ApiClient::loadQuestions(const QString &task, qint64 after) {
  cancel(m_questionsReply);
  if (!m_online || m_stopped || !uuid(task) || after < 0)
    return;
  const auto instance = m_instance;
  auto *reply = request(
      "GET", "/v1/tasks/" + task + "/questions?instance_id=" + instance +
                 "&after=" + QString::number(after) + "&limit=5");
  m_questionsReply = reply;
  m_questionsTask = task;
  bufferQuestionResponse(reply, this);
  connect(reply, &QNetworkReply::finished, this,
          [this, reply, task, after, instance] {
            reply->deleteLater();
            if (reply != m_questionsReply)
              return;
            m_questionsReply = nullptr;
            if (instance == m_instance && task == m_questionsTask) {
              const auto response = readQuestionResponse(reply, task);
              if (response &&
                  (response->rows.isEmpty() ? response->next == after
                                            : response->next > after)) {
                emit questionsReceived(response->rows.toVariantList(), after,
                                       response->next, response->more);
              }
            }
            emit changed();
          });
}
void ApiClient::refreshQuestionStatuses(const QString &task,
                                        const QStringList &ids) {
  cancel(m_questionStatusReply);
  if (!m_online || m_stopped || !uuid(task) || ids.isEmpty() || ids.size() > 5)
    return;
  for (const auto &id : ids)
    if (!uuid(id))
      return;
  const auto instance = m_instance;
  auto *reply = request("GET", "/v1/tasks/" + task + "/questions?instance_id=" +
                                   instance + "&ids=" + ids.join(','));
  m_questionStatusReply = reply;
  m_questionStatusTask = task;
  bufferQuestionResponse(reply, this);
  connect(reply, &QNetworkReply::finished, this, [this, reply, task, instance] {
    reply->deleteLater();
    if (reply != m_questionStatusReply)
      return;
    m_questionStatusReply = nullptr;
    if (instance == m_instance && task == m_questionStatusTask) {
      const auto response = readQuestionResponse(reply, task);
      if (response && !response->more)
        emit questionStatusesReceived(response->rows.toVariantList());
    }
    emit changed();
  });
}
bool ApiClient::validAnswerResult(const QJsonObject &obj) const {
  return uuid(obj.value("question_id").toString()) &&
         uuid(obj.value("message_id").toString()) &&
         obj.value("status").toString() == "stored";
}
ApiClient::AnswerValidation ApiClient::validateAnswer(const QString &text) {
  if (!text.isValidUtf16() || text.contains(QChar::Null))
    return InvalidAnswerText;
  if (text.toUtf8().size() > 4096)
    return AnswerTooLong;
  return text.trimmed().isEmpty() ? EmptyAnswer : ValidAnswer;
}
bool ApiClient::answerQuestion(const QString &task, const QString &question,
                               const QString &text) {
  if (!m_online || m_stopped || pending() || m_uncertain || !uuid(task) ||
      !uuid(question) || validateAnswer(text) != ValidAnswer)
    return false;
  m_method = "POST";
  m_path = "/v1/tasks/" + task + "/questions/" + question + "/answer";
  m_body =
      QJsonDocument(QJsonObject{{"instance_id", m_instance}, {"text", text}})
          .toJson(QJsonDocument::Compact);
  m_key = QUuid::createUuid().toString(QUuid::WithoutBraces).toLatin1();
  m_mutationInstance = m_instance;
  sendMutation();
  return true;
}
bool ApiClient::sendMessage(const QString &task, const QString &text,
                            const QString &binding) {
  if (!m_online || m_stopped || pending() || m_uncertain || !uuid(task) ||
      !uuid(binding))
    return false;
  const QByteArray body = text.toUtf8();
  if (body.isEmpty() || body.size() > 8192 || body.contains('\0'))
    return false;
  m_method = "POST";
  m_path = "/v1/tasks/" + task + "/messages";
  m_body = QJsonDocument(QJsonObject{{"text", QString::fromUtf8(body)},
                                     {"delivery_binding_id", binding},
                                     {"instance_id", m_instance}})
               .toJson(QJsonDocument::Compact);
  m_key = QUuid::createUuid().toString(QUuid::WithoutBraces).toLatin1();
  m_mutationInstance = m_instance;
  m_conflict = false;
  sendMutation();
  return true;
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
  if (body.size() > 256 * 1024) {
    m_error = tr("Save request is too large; shorten the changed fields");
    emit changed();
    return false;
  }
  m_error.clear();
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
bool ApiClient::canRetry() const {
  return m_uncertain && m_online && !pending() && !m_stopped &&
         !m_answerRetryNeedsSnapshot && m_mutationInstance == m_instance;
}
bool ApiClient::retry() {
  if (!canRetry())
    return false;
  sendMutation();
  return true;
}
bool ApiClient::abandonUncertain() {
  if (!canAbandon())
    return false;
  m_uncertain = false;
  if (!m_path.endsWith("/answer"))
    m_conflict = false;
  m_method.clear();
  m_path.clear();
  m_body.clear();
  m_key.clear();
  m_mutationInstance.clear();
  m_answerRetryNeedsSnapshot = false;
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
    const bool messageRoute =
        m_method == "POST" && m_path.endsWith("/messages");
    const bool answerRoute = m_method == "POST" && m_path.endsWith("/answer");
    const bool deliveryMutation = messageRoute || answerRoute;
    // A reply for an older database must not confirm or clear delivery text.
    const bool staleInstance =
        deliveryMutation && m_mutationInstance != m_instance;
    const bool validResult =
        answerRoute    ? validAnswerResult(obj)
        : messageRoute ? validMessage(obj)
        : m_path.endsWith("/queue")
            ? uuid(obj.value("project_id").toString()) &&
                  integer(obj.value("queue_version")) &&
                  obj.value("task_ids").isArray()
            : uuid(obj.value("id").toString()) && integer(obj.value("version"));
    if (!staleInstance && code >= 200 && code < 300 &&
        reply->error() == QNetworkReply::NoError &&
        reply->header(QNetworkRequest::ContentTypeHeader)
            .toString()
            .startsWith("application/json") &&
        !reply->property("oversized").toBool() && validResult &&
        (!answerRoute ||
         obj.value("question_id").toString() == m_path.section('/', 5, 5))) {
      m_uncertain = false;
      if (!answerRoute)
        m_conflict = false;
      if (!deliveryMutation && m_draftRevision == m_sentRevision)
        m_draft.clear();
      m_error.clear();
      if (answerRoute) {
        emit answerAccepted(obj.toVariantMap());
        refreshQuestionStatuses(m_path.section('/', 3, 3),
                                {m_path.section('/', 5, 5)});
      } else if (messageRoute) {
        emit commissionAccepted(obj.toVariantMap());
        const auto task =
            m_path.section('/', 3, 3); // /v1/tasks/{task}/messages
        loadMessages(task, 0);
        loadDelivery(task);
        reload();
      } else {
        emit mutationSucceeded(obj.toVariantMap(), m_mutationInstance);
        reload();
      }
    } else {
      // A transport error, 5xx, redirect or unusable success can follow commit.
      const auto detail = obj.value("code").toString();
      const bool answerMismatch =
          answerRoute && code == 409 && detail == "instance_mismatch";
      // The owner answer route checks the instance and the exact receipt
      // before returning closed. Other rejections may not even come from that
      // route (e.g. a v3 replacement), so keep the original request locked.
      const bool answerClosed =
          answerRoute && !staleInstance && code == 409 &&
          reply->error() == QNetworkReply::ContentConflictError &&
          !reply->property("oversized").toBool() &&
          reply->header(QNetworkRequest::ContentTypeHeader)
                  .toString()
                  .section(';', 0, 0)
                  .trimmed()
                  .compare("application/json", Qt::CaseInsensitive) == 0 &&
          detail == "closed" && obj.size() == 2 &&
          !obj.value("message").toString().isEmpty();
      const bool unresolvedRetry = answerRoute && m_uncertain && !answerClosed;
      m_uncertain = unresolvedRetry || staleInstance || code == 0 ||
                    code >= 500 || (code >= 200 && code < 400) ||
                    reply->error() == QNetworkReply::OperationCanceledError;
      // Answer outcomes are independent of manual editor version conflicts.
      if (!answerRoute)
        m_conflict = code == 409;
      if (answerClosed) {
        m_error =
            tr("Question is closed; answer not recorded, draft preserved");
      } else if (answerRoute && !m_uncertain && !answerMismatch) {
        m_error = tr("Answer rejected; draft preserved");
      } else if (deliveryMutation) {
        m_error =
            m_uncertain ? tr("Send outcome unknown; retry the same request "
                             "with the same key")
            : detail == "closed"
                ? tr("Task or project is closed; commission not recorded")
            : detail == "not_ready"
                ? tr("Pi writer is not ready; commission not recorded")
            : detail == "assignment_mismatch"
                ? tr("The enabled assignment belongs to a different project")
            : detail == "instance_mismatch"
                ? tr("Database changed; retry after reloading")
                : tr("Send rejected; text preserved");
      } else {
        m_error =
            m_uncertain ? tr("Save outcome unknown; retry the same request")
            : m_conflict
                ? (dirty()
                       ? tr("Conflict; draft preserved. Reload before editing "
                            "again")
                       : tr("Conflict; reload before trying the action again"))
                : tr("Save rejected; draft preserved");
      }
      if (answerMismatch || unresolvedRetry) {
        m_answerRetryNeedsSnapshot = unresolvedRetry;
        if (m_snapshot)
          m_snapshot->setProperty("beforeAnswerRetryCheck", true);
        reload();
      }
      if (code == 401) {
        fail(deliveryMutation
                 ? tr("Owner authorization failed; text preserved")
                 : tr("Owner authorization failed; draft preserved"));
        m_reconnect.start();
      }
    }
    emit changed();
  });
  emit changed();
}
