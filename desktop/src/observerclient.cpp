#include "observerclient.h"
#include <QJsonDocument>
#include <QNetworkProxy>
#include <QSet>
#include <QUuid>
#include <cmath>

namespace {
constexpr qsizetype responseLimit = 32 * 1024 * 1024;
bool uuid(const QJsonValue &value) {
  const auto s = value.toString();
  return !QUuid(s).isNull() && QUuid(s).toString(QUuid::WithoutBraces) == s;
}
bool counter(const QJsonValue &v) {
  const auto n = v.toDouble(-1);
  return v.isDouble() && n >= 0 && n <= 9007199254740991.0 &&
         std::floor(n) == n;
}
bool choice(const QJsonValue &v, const QStringList &values) {
  return v.isString() && values.contains(v.toString());
}
bool text(const QJsonValue &v, int max) {
  // OpenAPI maxLength and the Go/TS peers count Unicode code points, not
  // QString's UTF-16 code units (non-BMP characters occupy two units).
  if (!v.isString() || v.toString().toUcs4().size() > max)
    return false;
  for (const auto c : v.toString())
    if (c.category() == QChar::Other_Control)
      return false;
  return true;
}
// Copy only public, typed metadata into the model; never expose raw responses.
bool sanitizeProjection(const QJsonObject &root, QJsonArray &clean) {
  if (!uuid(root.value("database_instance_id")) ||
      !root.value("observers").isArray() ||
      root.value("observers").toArray().size() > 256)
    return false;
  QSet<QString> observers;
  for (const auto &v : root.value("observers").toArray()) {
    const auto o = v.toObject();
    const auto id = o.value("observer_id");
    if (!uuid(id) || observers.contains(id.toString()) ||
        !text(o.value("label"), 128) || !counter(o.value("version")) ||
        !counter(o.value("generation")) || !o.value("revoked").isBool() ||
        !o.value("runtimes").isArray() ||
        o.value("runtimes").toArray().size() > 8)
      return false;
    observers.insert(id.toString());
    QJsonObject row;
    for (const auto *k :
         {"observer_id", "label", "version", "generation", "revoked"})
      row.insert(k, o.value(k));
    const auto b = o.value("binding");
    if (!b.isUndefined() && !b.isNull()) {
      const auto binding = b.toObject();
      if (!uuid(binding.value("runtime_id")) ||
          !uuid(binding.value("project_id")))
        return false;
      row.insert("binding",
                 QJsonObject{{"runtime_id", binding.value("runtime_id")},
                             {"project_id", binding.value("project_id")}});
    }
    QJsonArray runtimes;
    QSet<QString> ids;
    for (const auto &r : o.value("runtimes").toArray()) {
      const auto runtime = r.toObject();
      const auto reg = runtime.value("registration").toObject();
      if (!uuid(reg.value("runtime_id")) || !uuid(reg.value("pi_session_id")) ||
          ids.contains(reg.value("runtime_id").toString()) ||
          !text(reg.value("pi_version"), 256) ||
          !text(reg.value("bridge_version"), 256) ||
          !choice(runtime.value("disposition"),
                  {"candidate", "bound", "superseded", "revoked"}) ||
          !choice(runtime.value("activity"),
                  {"unknown", "idle", "working", "waiting_user"}) ||
          !choice(runtime.value("freshness"),
                  {"unknown", "fresh", "stale", "stopped"}) ||
          !choice(runtime.value("lifecycle"), {"running", "stopped"}))
        return false;
      ids.insert(reg.value("runtime_id").toString());
      QJsonObject result;
      for (const auto *k :
           {"runtime_id", "pi_session_id", "pi_version", "bridge_version"})
        result.insert(k, reg.value(k));
      for (const auto *k :
           {"disposition", "activity", "freshness", "lifecycle"})
        result.insert(k, runtime.value(k));
      for (const auto *k : {"model", "provider", "last_seen"}) {
        if (!runtime.value(k).isUndefined() && !text(runtime.value(k), 256))
          return false;
        result.insert(k, runtime.value(k).toString());
      }
      const auto hint = reg.value("terminal_hint").toObject().toVariantMap();
      if (AgtermAdapter::validHint(hint))
        result.insert("terminal_hint", QJsonObject::fromVariantMap(hint));
      runtimes.append(result);
    }
    row.insert("runtimes", runtimes);
    clean.append(row);
  }
  return true;
}
QString stateLabel(const QString &s) {
  if (s == "candidate")
    return ObserverClient::tr("Candidate");
  if (s == "bound")
    return ObserverClient::tr("Bound");
  if (s == "superseded")
    return ObserverClient::tr("Superseded");
  if (s == "revoked")
    return ObserverClient::tr("Revoked");
  if (s == "fresh")
    return ObserverClient::tr("Fresh");
  if (s == "stale")
    return ObserverClient::tr("Stale");
  if (s == "stopped")
    return ObserverClient::tr("Stopped");
  if (s == "idle")
    return ObserverClient::tr("Idle");
  if (s == "working")
    return ObserverClient::tr("Working");
  if (s == "waiting_user")
    return ObserverClient::tr("Waiting for user");
  return ObserverClient::tr("Unknown");
}
} // namespace

int ConnectionRows::rowCount(const QModelIndex &parent) const {
  return parent.isValid() ? 0 : m_rows.size();
}
QVariant ConnectionRows::data(const QModelIndex &index, int role) const {
  if (!index.isValid() || index.row() >= m_rows.size() || role != Qt::UserRole)
    return {};
  return m_rows.at(index.row());
}
QHash<int, QByteArray> ConnectionRows::roleNames() const {
  return {{Qt::UserRole, "record"}};
}
void ConnectionRows::update(const QVariantList &rows) {
  if (rows == m_rows)
    return;
  const auto key = [](const QVariant &record) {
    const auto row = record.toMap();
    return row.value("observer_id").toString() + '/' +
           row.value("runtime_id").toString();
  };
  QSet<QString> wanted;
  for (const auto &record : rows)
    wanted.insert(key(record));
  for (int i = m_rows.size() - 1; i >= 0; --i) {
    if (wanted.contains(key(m_rows.at(i))))
      continue;
    beginRemoveRows({}, i, i);
    m_rows.removeAt(i);
    endRemoveRows();
  }
  for (int i = 0; i < rows.size(); ++i) {
    const auto target = key(rows.at(i));
    int found = i;
    while (found < m_rows.size() && key(m_rows.at(found)) != target)
      ++found;
    if (found == m_rows.size()) {
      beginInsertRows({}, i, i);
      m_rows.insert(i, rows.at(i));
      endInsertRows();
    } else {
      if (found != i) {
        beginMoveRows({}, found, found, {}, i);
        m_rows.move(found, i);
        endMoveRows();
      }
      if (m_rows.at(i) != rows.at(i)) {
        m_rows[i] = rows.at(i);
        emit dataChanged(index(i), index(i), {Qt::UserRole});
      }
    }
  }
}

ObserverClient::ObserverClient(ApiClient *owner, QObject *parent)
    : QObject(parent), m_owner(owner) {
  m_network.setProxy(QNetworkProxy::NoProxy);
  connect(this, &ObserverClient::changed, this,
          [this] { m_connectionRows.update(rows()); });
  m_poll.setSingleShot(true);
  connect(&m_poll, &QTimer::timeout, this, &ObserverClient::reload);
  connect(owner, &ApiClient::changed, this, [this] {
    if (m_instance != m_owner->instance()) {
      resetRead();
      m_instance = m_owner->instance();
      m_age.invalidate();
      m_observers = {};
      m_online = false;
      m_confirmation.clear();
      emit changed();
      if (m_active)
        reload();
    }
  });
  m_instance = owner->instance();
}
ObserverClient::~ObserverClient() {
  resetRead();
  if (m_mutation) {
    m_mutation->disconnect(this);
    m_mutation->abort();
  }
}
void ObserverClient::resetRead() {
  m_navigation.cancel();
  ++m_generation;
  m_poll.stop();
  if (m_read) {
    auto *reply = m_read.data();
    m_read = nullptr;
    reply->abort();
  }
}
void ObserverClient::setActive(bool value) {
  if (value == m_active)
    return;
  m_active = value;
  resetRead();
  m_online = false;
  m_confirmation.clear();
  emit changed();
  if (value)
    reload();
}
void ObserverClient::setProject(const QVariantMap &project) {
  if (m_project == project)
    return;
  m_project = project;
  m_online = false;
  // Unbind targets the observer's captured binding, not the manual project.
  if (m_confirmation.value("action") != "unbind")
    m_confirmation.clear();
  resetRead();
  emit changed();
  if (m_active)
    reload();
}
void ObserverClient::setOnlyProject(bool value) {
  if (m_onlyProject == value)
    return;
  m_onlyProject = value;
  emit changed();
}
bool ObserverClient::canBind() const {
  return !m_project.value("id").toString().isEmpty() &&
         !m_project.value("archived").toBool();
}
void ObserverClient::setViewFilter(const QString &value) {
  if (m_viewFilter == value ||
      !QStringList{"", "bound", "candidate", "history"}.contains(value))
    return;
  m_viewFilter = value;
  emit changed();
}
QVariantList ObserverClient::rows() const {
  QVariantList result;
  for (const auto &value : records(m_onlyProject)) {
    const auto row = value.toMap();
    if (m_viewFilter.isEmpty() || row.value("category") == m_viewFilter)
      result.append(row);
  }
  return result;
}
QVariantList ObserverClient::records(bool onlyProject) const {
  QVariantList result;
  for (const auto &value : m_observers) {
    const auto o = value.toObject();
    auto runtimes = o.value("runtimes").toArray();
    if (runtimes.isEmpty())
      runtimes.append(QJsonObject{});
    for (const auto &r : runtimes) {
      auto row = r.toObject().toVariantMap();
      for (const auto *k : {"observer_id", "label", "version", "revoked"})
        row.insert(k, o.value(k).toVariant());
      const auto b = o.value("binding").toObject();
      row.insert("selected_project",
                 !b.isEmpty() && b.value("project_id").toString() ==
                                     m_project.value("id").toString());
      row.insert("bound_runtime", b.value("runtime_id").toString() ==
                                          row.value("runtime_id").toString() &&
                                      !b.isEmpty());
      row.insert("has_binding", !b.isEmpty());
      row.insert("binding_runtime_id", b.value("runtime_id").toString());
      row.insert("binding_project_id", b.value("project_id").toString());
      row.insert("generation", o.value("generation").toVariant());
      const bool registrationOnly =
          row.value("runtime_id").toString().isEmpty();
      const QString category =
          o.value("revoked").toBool()           ? "history"
          : row.value("bound_runtime").toBool() ? "bound"
          : registrationOnly || row.value("disposition") == "candidate"
              ? "candidate"
              : "history";
      row.insert("category", category);
      row.insert("category_text",
                 category == "bound" ? tr("Current binding")
                 : category == "candidate"
                     ? (registrationOnly
                            ? tr("Unbound observer — no runtime yet")
                            : tr("Candidate runtime"))
                     : tr("Connection history"));
      const bool offline =
          !m_online || !m_age.isValid() || m_age.elapsed() >= 4000;
      const auto freshness =
          offline ? QString("unknown") : row.value("freshness").toString();
      row.insert("freshness", freshness);
      row.insert(
          "navigable",
          !offline && freshness == "fresh" && !o.value("revoked").toBool() &&
              AgtermAdapter::validHint(row.value("terminal_hint").toMap()));
      const bool historical = offline || o.value("revoked").toBool() ||
                              row.value("disposition") == "superseded";
      const QString activity = historical                            ? "unknown"
                               : row.value("lifecycle") == "stopped" ? "stopped"
                               : freshness != "fresh"
                                   ? "unknown"
                                   : row.value("activity").toString();
      row.insert("status_text",
                 tr("%1 · %2 · %3")
                     .arg(stateLabel(o.value("revoked").toBool()
                                         ? "revoked"
                                         : row.value("disposition").toString()),
                          stateLabel(activity), stateLabel(freshness)));
      row.insert("bindable", canBind() && !o.value("revoked").toBool() &&
                                 (row.value("disposition") == "candidate" ||
                                  row.value("disposition") == "bound"));
      if (!onlyProject || (row.value("selected_project").toBool() &&
                           row.value("bound_runtime").toBool()))
        result.append(row);
    }
  }
  return result;
}
QNetworkReply *ObserverClient::request(const QByteArray &method,
                                       const QString &path,
                                       const QByteArray &body,
                                       const QByteArray &key) {
  auto *reply = m_owner->ownerRequest(m_network, method, path, body, key);
  if (!reply)
    return nullptr;
  reply->setReadBufferSize(responseLimit + 1);
  QTimer::singleShot(m_timeout, reply, [reply] {
    if (reply->isRunning())
      reply->abort();
  });
  connect(reply, &QNetworkReply::readyRead, reply, [reply] {
    if (reply->bytesAvailable() > responseLimit)
      reply->abort();
  });
  return reply;
}
void ObserverClient::fail(const QString &message) {
  m_online = false;
  m_message = message;
  emit changed();
  if (m_active) {
    m_poll.start(m_backoff);
    m_backoff = qMin(30000, m_backoff * 2);
  }
}
void ObserverClient::reload() {
  if (!m_active || m_read || m_mutation)
    return;
  m_poll.stop();
  if (m_instance.isEmpty()) {
    fail(tr("Observation offline. Reload the owner connection."));
    return;
  }
  auto *reply = request("GET", "/v1/observers");
  if (!reply) {
    fail(tr("Observation offline. Reload the owner connection."));
    return;
  }
  m_read = reply;
  const auto generation = m_generation;
  connect(reply, &QNetworkReply::finished, this, [this, reply, generation] {
    reply->deleteLater();
    if (m_read != reply || generation != m_generation)
      return;
    m_read = nullptr;
    const auto bytes = reply->isOpen() ? reply->readAll() : QByteArray{};
    QJsonParseError error;
    const auto doc = QJsonDocument::fromJson(bytes, &error);
    QJsonArray clean;
    if (reply->error() != QNetworkReply::NoError ||
        reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() !=
            200 ||
        bytes.size() > responseLimit ||
        error.error != QJsonParseError::NoError || !doc.isObject() ||
        !sanitizeProjection(doc.object(), clean)) {
      fail(tr(
          "Observation unavailable. Retry after refreshing the connection."));
      return;
    }
    if (doc.object().value("database_instance_id").toString() != m_instance) {
      m_age.invalidate();
      m_observers = {};
      m_confirmation.clear();
      fail(tr("Observation database changed. Reload the owner connection."));
      return;
    }
    m_observers = clean;
    m_online = true;
    m_age.restart();
    m_message.clear();
    m_backoff = 2000;
    emit changed();
    m_poll.start(2000);
  });
  emit changed();
}
QString ObserverClient::confirmation() const {
  const auto action = m_confirmation.value("action").toString();
  if (action.isEmpty())
    return {};
  QString question;
  if (action == "bind")
    question = tr("Bind this runtime to the selected project? This replaces "
                  "the observer's previous binding.");
  else if (action == "unbind")
    return tr("Remove this binding? Observation of the bound project will "
              "stop.\n\nObserver: %1\nBound runtime: %2\nBound project ID: %3")
        .arg(m_confirmation.value("observer_id").toString(),
             m_confirmation.value("binding_runtime_id").toString(),
             m_confirmation.value("binding_project_id").toString());
  else if (action == "revoke")
    question = tr("Revoke this observer credential permanently?");
  else
    question = tr("Prune inactive runtimes? Their metadata will be removed "
                  "permanently; retired IDs cannot reconnect.");
  return question + "\n\n" +
         tr("Observer: %1\nRuntime: %2\nProject: %3")
             .arg(m_confirmation.value("observer_id").toString(),
                  m_confirmation.value("runtime_id").toString(), projectName());
}
void ObserverClient::cancelConfirmation() {
  m_confirmation.clear();
  emit changed();
}
bool ObserverClient::canManage() const {
  return m_active && m_online && !busy() && m_age.isValid() &&
         m_age.elapsed() < 4000;
}
bool ObserverClient::prepare(const QString &action, const QString &observer,
                             const QString &runtime) {
  if (!canManage() ||
      !QStringList{"bind", "unbind", "revoke", "prune"}.contains(action))
    return false;
  for (const auto &value : records(false)) {
    const auto row = value.toMap();
    if (row.value("observer_id") != observer ||
        row.value("runtime_id").toString() != runtime)
      continue;
    if ((action == "bind" && !row.value("bindable").toBool()) ||
        (action == "unbind" && !row.value("has_binding").toBool()) ||
        (action == "revoke" && row.value("revoked").toBool()))
      return false;
    m_actionMessage.clear();
    m_confirmation = {{"action", action},
                      {"observer_id", observer},
                      {"version", row.value("version")},
                      {"generation", row.value("generation")},
                      {"binding_runtime_id", row.value("binding_runtime_id")},
                      {"binding_project_id", row.value("binding_project_id")},
                      {"instance", m_instance}};
    if (action != "unbind")
      m_confirmation.insert("runtime_id", runtime);
    if (action == "bind")
      m_confirmation.insert("project_id", m_project.value("id"));
    emit changed();
    return true;
  }
  return false;
}
bool ObserverClient::confirm() {
  const auto c = m_confirmation;
  m_confirmation.clear();
  emit changed();
  const auto action = c.value("action").toString();
  if (c.isEmpty() || c.value("instance") != m_instance || !canManage() ||
      (action == "bind" && c.value("project_id") != m_project.value("id")))
    return false;
  bool same = false;
  if (action == "unbind") {
    // Revalidate against the observer itself. The clicked row is not the
    // target, and it need not still exist.
    for (const auto &value : m_observers) {
      const auto observer = value.toObject();
      if (observer.value("observer_id").toString() != c.value("observer_id"))
        continue;
      const auto binding = observer.value("binding").toObject();
      same =
          !binding.isEmpty() &&
          observer.value("version").toVariant() == c.value("version") &&
          observer.value("generation").toVariant() == c.value("generation") &&
          binding.value("runtime_id").toString() ==
              c.value("binding_runtime_id") &&
          binding.value("project_id").toString() ==
              c.value("binding_project_id");
      break;
    }
  } else {
    // Recheck the current row and CAS version, not a stale QML delegate.
    if (!prepare(action, c.value("observer_id").toString(),
                 c.value("runtime_id").toString()))
      return false;
    same = m_confirmation.value("version") == c.value("version") &&
           m_confirmation.value("generation") == c.value("generation") &&
           m_confirmation.value("binding_runtime_id") ==
               c.value("binding_runtime_id") &&
           m_confirmation.value("binding_project_id") ==
               c.value("binding_project_id");
    m_confirmation.clear();
  }
  if (!same) {
    fail(tr("Observation changed. Refresh and confirm again."));
    return false;
  }
  QJsonObject payload{
      {"expected_version", QJsonValue::fromVariant(c.value("version"))}};
  QString path = "/v1/observers/" + c.value("observer_id").toString();
  QByteArray method = "POST";
  if (action == "bind" || action == "unbind") {
    method = "PATCH";
    path += "/binding";
    payload.insert("runtime_id",
                   action == "bind"
                       ? QJsonValue(c.value("runtime_id").toString())
                       : QJsonValue(QJsonValue::Null));
    payload.insert("project_id",
                   action == "bind"
                       ? QJsonValue(c.value("project_id").toString())
                       : QJsonValue(QJsonValue::Null));
  } else
    path += "/" + action;
  resetRead();
  auto *reply = request(
      method, path, QJsonDocument(payload).toJson(QJsonDocument::Compact),
      QUuid::createUuid().toString(QUuid::WithoutBraces).toUtf8());
  if (!reply) {
    fail(tr("Observation offline. Reload the owner connection."));
    return false;
  }
  m_mutation = reply;
  emit changed();
  const auto instance = m_instance;
  connect(reply, &QNetworkReply::finished, this, [this, reply, instance] {
    reply->deleteLater();
    if (m_mutation != reply)
      return;
    m_mutation = nullptr;
    m_online = false;
    // Never apply mutation responses to rows. Fetch a fresh projection, even on
    // lost ACK/CAS/archive failure; no automatic mutation retry or draft reset.
    const auto status =
        reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
    m_actionMessage = status >= 200 && status < 300 && instance == m_instance
                          ? QString{}
                      : (status == 409 || status == 404 || status == 400)
                          ? tr("Observation changed or project unavailable. "
                               "Refresh and confirm again.")
                          : tr("Observation action unconfirmed. Refresh before "
                               "trying again.");
    emit changed();
    if (m_active)
      reload();
  });
  return true;
}

void ObserverClient::openSession(const QString &observer,
                                 const QString &runtime) {
  for (const auto &v : records(false)) {
    const auto row = v.toMap();
    if (row.value("observer_id") == observer &&
        row.value("runtime_id") == runtime && row.value("navigable").toBool()) {
      m_navigation.open(row.value("terminal_hint").toMap());
      return;
    }
  }
}
