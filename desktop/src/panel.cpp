#include "panel.h"
#include <QFileInfo>
#include <QJsonDocument>
#include <QLocale>
#include <algorithm>

namespace {
QVariantMap find(ObjectModel *model, const QString &id) {
  for (int i = 0; i < model->rowCount(); ++i)
    if (model->get(i).value("id").toString() == id)
      return model->get(i);
  return {};
}
} // namespace
Panel::Panel(ApiClient *client, QObject *parent)
    : QObject(parent), m_client(client), m_observers(client, this) {
  connect(this, &Panel::changed, this,
          [this] { m_observers.setProject(project()); });
  connect(client, &ApiClient::changed, this, [this] {
    if (m_seenSequence != m_client->sequence() ||
        m_historyInstance != m_client->instance()) {
      m_seenSequence = m_client->sequence();
      m_historyInstance = m_client->instance();
      m_client->cancelHistory();
      m_history.clear();
      m_historyCursor = 0;
      m_historyMore = true;
      resetCommissions();
      if (!m_task.isEmpty()) {
        moreHistory();
        loadCommissionPage();
      }
    }
    if (!m_client->dirty() && !m_client->pending() && !m_client->uncertain())
      m_kind.clear();
    emit changed();
  });
  connect(client, &ApiClient::mutationSucceeded, this,
          [this](const QVariantMap &result, const QString &instance) {
            // A newer draft survives the reply, but its base must advance to
            // the confirmed save (not to a possibly newer SSE snapshot).
            if (!m_client->dirty() || m_kind.isEmpty() ||
                m_editorInstance != instance)
              return;
            m_editorId = result.value("id").toString();
            m_version = result.value("version").toLongLong();
            m_adopted = false;
            if (m_kind == "project") {
              if (m_project != m_editorId)
                setProjectSelection(m_editorId);
            } else {
              m_task = m_editorId;
            }
          });
  connect(client, &ApiClient::historyReceived, this,
          [this](const QVariantList &events, qint64 cursor, bool more) {
            m_history += events;
            m_historyCursor = cursor;
            m_historyMore = more;
            emit changed();
          });
  connect(client, &ApiClient::messagesReceived, this,
          [this](const QVariantList &rows, qint64 after, bool more) {
            if (m_messagesTask.isEmpty())
              return;
            if (after == 0)
              m_messages.clear();
            m_messages += rows;
            m_messagesCursor = after;
            m_messagesMore = more;
            armStatusPoll();
            emit changed();
          });
  connect(client, &ApiClient::messageStatusesReceived, this,
          [this](const QVariantList &rows) {
            for (const auto &value : rows) {
              const auto row = value.toMap();
              for (auto &existing : m_messages) {
                if (existing.toMap().value("id").toString() ==
                    row.value("id").toString())
                  existing = row;
              }
            }
            armStatusPoll();
            emit changed();
          });
  connect(client, &ApiClient::deliveryReceived, this,
          [this](const QVariantMap &projection) {
            if (!m_deliveryTask.isEmpty())
              m_delivery = projection;
            emit changed();
          });
  connect(client, &ApiClient::commissionAccepted, this,
          [this](const QVariantMap &message) {
            // One explicit send records exactly one commission; clear the
            // commission text only when it still holds the sent text, so a
            // retry confirmation never discards newer typing.
            if (m_commission == message.value("text").toString())
              m_commission.clear();
            m_messagesCursor = 0;
            m_messages.clear();
            m_messagesMore = true;
            loadCommissionPage();
            emit changed();
          });
  m_statusPoll.setSingleShot(true);
  m_statusPoll.setInterval(2000);
  connect(&m_statusPoll, &QTimer::timeout, this, [this] {
    if (m_task.isEmpty() || m_messages.isEmpty())
      return;
    QStringList ids;
    for (const auto &value : m_messages)
      ids.append(value.toMap().value("id").toString());
    // Rotate a bounded window so long histories keep refreshing everywhere.
    if (ids.size() > 50) {
      m_statusOffset = (m_statusOffset + 50) % ids.size();
      const auto count = std::min<qsizetype>(50, ids.size() - m_statusOffset);
      ids = ids.sliced(m_statusOffset, count);
    }
    m_client->refreshMessageStatuses(m_task, ids);
    // An offline tick arms no reply; rearm so reconnect resumes polling.
    if (!m_client->messagesLoading())
      armStatusPoll();
  });
  m_deliveryPoll.setSingleShot(true);
  m_deliveryPoll.setInterval(3000);
  connect(&m_deliveryPoll, &QTimer::timeout, this, [this] {
    if (m_task.isEmpty())
      return;
    m_deliveryTask = m_task;
    m_client->loadDelivery(m_task);
    m_deliveryPoll.start(); // Bounded: only while this task stays selected.
  });
}
void Panel::setCommissionText(const QString &text) {
  if (m_commission == text)
    return;
  m_commission = text;
  emit changed();
}
void Panel::armStatusPoll() {
  if (m_task.isEmpty())
    return;
  for (const auto &value : m_messages) {
    if (value.toMap().value("status").toString() != "observed") {
      if (!m_statusPoll.isActive())
        m_statusPoll.start();
      return;
    }
  }
  m_statusPoll.stop();
}
QVariantMap Panel::messageRow(const QVariantMap &row) const {
  auto result = row;
  const auto timestamp =
      QDateTime::fromString(row.value("created_at").toString(), Qt::ISODate);
  result.insert(
      "displayTimestamp",
      timestamp.isValid()
          ? QLocale().toString(timestamp.toLocalTime(), QLocale::ShortFormat)
          : row.value("created_at").toString());
  const auto status = row.value("status").toString();
  result.insert("statusLabel", status == "observed"
                                   ? tr("Pi started preparing the turn")
                               : status == "unknown" ? tr("Outcome unknown")
                                                     : tr("Stored"));
  const auto blockReason = row.value("block_reason").toString();
  result.insert("blockReason", blockReason == "task_closed"
                                   ? tr("The task is closed")
                               : blockReason == "project_archived"
                                   ? tr("The project is archived")
                                   : QString());
  return result;
}
QVariantList Panel::messages() const {
  QVariantList result;
  for (const auto &value : m_messages)
    result.append(messageRow(value.toMap()));
  return result;
}
QVariantMap Panel::assignment() const {
  QVariantMap result{{"enabled", false},
                     {"available", false},
                     {"canSend", false},
                     {"reason", QString()}};
  const auto task = this->task();
  if (task.isEmpty())
    return result;
  result.insert("taskId", m_task);
  const auto state = task.value("state").toString();
  const auto terminal = QStringList{"done", "cancelled"}.contains(state);
  const auto archived = project().value("archived").toBool();
  const auto binding = m_delivery.value("binding").toMap();
  if (binding.isEmpty()) {
    result.insert("reason",
                  tr("Delivery to Pi is not enabled for this installation"));
    return result;
  }
  result.insert("enabled", true);
  result.insert("bindingId", binding.value("delivery_binding_id").toString());
  result.insert("piSession", binding.value("pi_session_id").toString());
  result.insert("status", binding.value("status").toString());
  result.insert("ready", m_delivery.value("ready").toBool());
  QString reason;
  if (archived)
    reason = tr("The project is archived");
  else if (terminal)
    reason = tr("The task is closed");
  else if (binding.value("status").toString() != "active")
    reason = tr("The Pi assignment is not active");
  else if (binding.value("project_id").toString() !=
           task.value("project_id").toString())
    reason = tr("The enabled assignment belongs to a different project");
  else if (!m_delivery.value("ready").toBool())
    reason = tr("The Pi writer is not ready right now");
  result.insert("reason", reason);
  result.insert("available", reason.isEmpty());
  result.insert("canSend", reason.isEmpty() && writable() &&
                               !m_commission.trimmed().isEmpty());
  return result;
}
void Panel::resetCommissions() {
  m_statusPoll.stop();
  m_deliveryPoll.stop();
  m_messagesTask.clear();
  m_deliveryTask.clear();
  m_messages.clear();
  m_messagesCursor = 0;
  m_messagesMore = false;
  m_delivery = {};
  m_client->cancelMessages();
}

// resetCommissionView drops the typed commission text as well; use it only
// when the recipient context actually changed (task/project switch), never
// for snapshot refreshes of the same selection.
void Panel::resetCommissionView() {
  resetCommissions();
  m_commission.clear();
}
void Panel::loadCommissionPage() {
  if (m_task.isEmpty() || !m_client->online())
    return;
  m_messagesTask = m_task;
  m_deliveryTask = m_task;
  m_client->loadMessages(m_task, 0);
  m_client->loadDelivery(m_task);
  m_deliveryPoll.start();
}
bool Panel::sendCommission() {
  const auto target = assignment();
  if (!target.value("canSend").toBool())
    return false;
  return m_client->sendMessage(m_task, m_commission,
                               target.value("bindingId").toString());
}
void Panel::moreMessages() {
  if (!m_task.isEmpty() && m_messagesMore && !messagesLoading())
    m_client->loadMessages(m_task, m_messagesCursor);
}
QVariantList Panel::history() const {
  const QMap<QString, QString> labels{
      {"project.create", tr("Project created")},
      {"project.edit", tr("Project edited")},
      {"task.create", tr("Task created")},
      {"task.edit", tr("Task edited")},
      {"task.transition", tr("Manual state changed")},
      {"queue.replace", tr("Queue changed")}};
  QVariantList result;
  for (const auto &value : m_history) {
    auto row = value.toMap();
    const auto timestamp =
        QDateTime::fromString(row.value("timestamp").toString(), Qt::ISODate);
    row.insert("displayTimestamp",
               timestamp.isValid() ? QLocale().toString(timestamp.toLocalTime(),
                                                        QLocale::ShortFormat)
                                   : row.value("timestamp").toString());
    row.insert("label", labels.value(row.value("action").toString(),
                                     tr("Manual change")));
    row.insert("details",
               tr("Before:\n%1\nAfter:\n%2")
                   .arg(QString::fromUtf8(
                            QJsonDocument::fromVariant(row.value("before"))
                                .toJson(QJsonDocument::Indented)),
                        QString::fromUtf8(
                            QJsonDocument::fromVariant(row.value("after"))
                                .toJson(QJsonDocument::Indented))));
    result.append(row);
  }
  return result;
}
QVariantList Panel::projectSnapshot() const {
  QVariantList rows;
  for (int i = 0; i < m_client->projects()->rowCount(); ++i)
    rows.append(m_client->projects()->get(i));
  return rows;
}
QVariantList Panel::projects() const {
  QVariantList rows;
  for (int i = 0; i < m_client->projects()->rowCount(); ++i) {
    auto row = m_client->projects()->get(i);
    if (!m_archived && row.value("archived").toBool())
      continue;
    QMap<QString, int> counts;
    for (int j = 0; j < m_client->tasks()->rowCount(); ++j) {
      const auto task = m_client->tasks()->get(j);
      if (task.value("project_id") == row.value("id"))
        ++counts[task.value("state").toString()];
    }
    row.insert("counts", tr("Backlog %1 · Queued %2 · Current %3 · Done %4")
                             .arg(counts["backlog"])
                             .arg(counts["queued"])
                             .arg(counts["in_progress"])
                             .arg(counts["done"]));
    rows.append(row);
  }
  return rows;
}
QVariantMap Panel::project() const {
  auto row = find(m_client->projects(), m_project);
  if (!row.isEmpty())
    row.insert("pathAvailable",
               QFileInfo(row.value("root_path").toString()).isDir());
  return row;
}
QVariantMap Panel::task() const { return find(m_client->tasks(), m_task); }
QVariantList Panel::queueIds() const {
  for (int i = 0; i < m_client->queues()->rowCount(); ++i) {
    const auto row = m_client->queues()->get(i);
    if (row.value("project_id").toString() == m_project)
      return row.value("task_ids").toList();
  }
  return {};
}
QVariantList Panel::tasks() const {
  QVariantList rows;
  const auto ids = queueIds();
  for (int i = 0; i < m_client->tasks()->rowCount(); ++i) {
    auto row = m_client->tasks()->get(i);
    if (row.value("project_id").toString() != m_project)
      continue;
    const int position = ids.indexOf(row.value("id"));
    row.insert("position", position);
    row.insert("canDown", position >= 0 && position + 1 < ids.size());
    row.insert("stateLabel", stateLabel(row.value("state").toString()));
    rows.append(row);
  }
  const QStringList states{"backlog", "queued", "in_progress", "done",
                           "cancelled"};
  std::stable_sort(
      rows.begin(), rows.end(), [&](const QVariant &a, const QVariant &b) {
        const auto x = a.toMap(), y = b.toMap();
        if (x.value("state") == y.value("state"))
          return x.value("position").toInt() < y.value("position").toInt();
        return states.indexOf(x.value("state").toString()) <
               states.indexOf(y.value("state").toString());
      });
  QString previousState;
  for (auto &value : rows) {
    auto row = value.toMap();
    const auto state = row.value("state").toString();
    row.insert("sectionStart", state != previousState);
    previousState = state;
    value = row;
  }
  return rows;
}
QString Panel::stateLabel(const QString &state) const {
  if (state == "backlog")
    return tr("Backlog");
  if (state == "queued")
    return tr("Queue");
  if (state == "in_progress")
    return tr("In progress (manual)");
  if (state == "done")
    return tr("Completed manually");
  if (state == "cancelled")
    return tr("Cancelled");
  return {};
}
QString Panel::currentText() const {
  if (m_kind == "project") {
    const auto row = project();
    return tr("Name: %1\nDirectory: %2\nDescription: %3")
        .arg(row.value("name").toString(), row.value("root_path").toString(),
             row.value("description").toString());
  }
  const auto row = task();
  return tr("Title: %1\nDescription: %2\nAcceptance criteria: %3")
      .arg(row.value("title").toString(), row.value("description").toString(),
           row.value("acceptance_criteria").toString());
}
QString Panel::message() const {
  if (canAbandon())
    return tr("Database changed; the old save outcome is unknown. Acknowledge "
              "abandonment before continuing; the draft will be kept.");
  if (dirty() && m_editorInstance != m_client->instance())
    return tr("Database changed; copy or discard the draft before continuing");
  return m_client->error();
}
QString Panel::connection() const {
  if (m_client->loading())
    return tr("Loading");
  return m_client->online() ? tr("Online")
                            : tr("Offline — cached data is read-only");
}
QString Panel::updated() const {
  return m_client->lastUpdated().isValid()
             ? QLocale().toString(m_client->lastUpdated().toLocalTime(),
                                  QLocale::ShortFormat)
             : tr("Never");
}
bool Panel::writable() const {
  return m_client->online() && !m_client->pending() && !m_client->uncertain();
}
bool Panel::saveable() const {
  const bool newProject = m_kind == "project" && m_editorId.isEmpty();
  return writable() && !conflict() && !m_kind.isEmpty() &&
         m_editorInstance == m_client->instance() &&
         (newProject || !project().value("archived").toBool());
}
void Panel::setShowArchived(bool value) {
  m_archived = value;
  emit changed();
}
void Panel::setProjectSelection(const QString &id) {
  // Keep dependent selection/history consistent for both owner selection and
  // confirmed project creation, without touching an in-flight editor's draft.
  m_project = id;
  m_task.clear();
  m_client->cancelHistory();
  m_history.clear();
  m_historyCursor = 0;
  m_historyMore = true;
  resetCommissionView();
}
bool Panel::selectProject(const QString &id) {
  if (dirty() || m_client->pending() || uncertain())
    return false;
  setProjectSelection(id);
  emit changed();
  return true;
}
bool Panel::selectTask(const QString &id) {
  if (dirty() || m_client->pending() || uncertain())
    return false;
  const auto row = find(m_client->tasks(), id);
  if (row.value("project_id").toString() != m_project)
    return false;
  m_task = id;
  m_client->cancelHistory();
  m_history.clear();
  m_historyCursor = 0;
  m_historyMore = true;
  // The commission draft survives re-selection and snapshot refreshes: the
  // view resets, the typed text is cleared only on a real recipient change
  // (project/task switch via resetCommissionView).
  resetCommissions();
  moreHistory();
  loadCommissionPage();
  emit changed();
  return true;
}
bool Panel::begin(const QString &kind, const QVariantMap &value) {
  if (!writable() || dirty())
    return false;
  m_kind = kind;
  m_version = value.value("version").toLongLong();
  m_editorId = value.value("id").toString();
  m_editorProject = m_project;
  m_editorInstance = m_client->instance();
  m_adopted = false;
  QVariantMap fields;
  if (kind == "project") {
    fields = {{"name", value.value("name", "")},
              {"root_path", value.value("root_path", "")},
              {"description", value.value("description", "")}};
  } else {
    fields = {{"title", value.value("title", "")},
              {"description", value.value("description", "")},
              {"acceptance_criteria", value.value("acceptance_criteria", "")}};
  }
  m_client->setDraft(fields);
  return true;
}
bool Panel::editProject(bool create) {
  return (create ||
          (!project().isEmpty() && !project().value("archived").toBool())) &&
         begin("project", create ? QVariantMap{} : project());
}
bool Panel::editTask(bool create) {
  return !project().isEmpty() && !project().value("archived").toBool() &&
         (create || !task().isEmpty()) &&
         begin("task", create ? QVariantMap{} : task());
}
void Panel::field(const QString &name, const QString &value) {
  auto draft = editor();
  if (!draft.contains(name))
    return;
  draft[name] = value;
  m_client->setDraft(draft);
}
bool Panel::save() {
  if (!saveable())
    return false;
  m_adopted = false;
  auto payload = editor();
  if (!m_editorId.isEmpty())
    payload.insert("expected_version", m_version);
  const auto path = m_kind == "project"
                        ? "/v1/projects"
                        : "/v1/projects/" + m_editorProject + "/tasks";
  return m_client->save(
      m_editorId.isEmpty() ? "POST" : "PATCH",
      m_editorId.isEmpty()
          ? path
          : QStringLiteral("/v1/") +
                (m_kind == "project" ? "projects/" : "tasks/") + m_editorId,
      payload);
}
void Panel::discard() { m_client->discardDraft(); }
void Panel::reload() { m_client->reload(); }
bool Panel::rebase() {
  // Explicit owner action: retain text but adopt the freshly loaded version.
  if (!writable() || m_client->loading() || m_editorId.isEmpty() ||
      m_editorInstance != m_client->instance())
    return false;
  const auto row =
      find(m_kind == "project" ? m_client->projects() : m_client->tasks(),
           m_editorId);
  if (row.isEmpty())
    return false;
  if (row.value("version").toLongLong() <= m_version)
    return false;
  m_version = row.value("version").toLongLong();
  m_adopted = true;
  emit changed();
  return true;
}
bool Panel::retry() { return m_client->retry(); }
bool Panel::canArchive() const {
  if (!writable() || dirty() || project().isEmpty())
    return false;
  if (project().value("archived").toBool())
    return true;
  for (const auto &row : tasks()) {
    const auto state = row.toMap().value("state").toString();
    if (state == "queued" || state == "in_progress")
      return false;
  }
  return true;
}
bool Panel::archive() {
  if (!canArchive())
    return false;
  return m_client->save("PATCH", "/v1/projects/" + m_project,
                        {{"expected_version", project().value("version")},
                         {"archived", !project().value("archived").toBool()}});
}
bool Panel::queue(const QString &id, int direction) {
  if (!writable() || dirty() || project().value("archived").toBool() ||
      direction < -1 || direction > 1)
    return false;
  const auto row = find(m_client->tasks(), id);
  if (row.value("project_id").toString() != m_project)
    return false;
  auto ids = queueIds();
  const int position = ids.indexOf(id);
  if (direction == 0) {
    if (position >= 0)
      ids.removeAt(position);
    else if (row.value("state") == "backlog")
      ids.append(id);
    else
      return false;
  } else {
    if (position < 0 || position + direction < 0 ||
        position + direction >= ids.size())
      return false;
    ids.swapItemsAt(position, position + direction);
  }
  return m_client->save(
      "PUT", "/v1/projects/" + m_project + "/queue",
      {{"expected_queue_version", project().value("queue_version")},
       {"task_ids", ids}});
}
bool Panel::allowed(const QString &target) const {
  if (!writable() || dirty() || project().value("archived").toBool() ||
      task().isEmpty())
    return false;
  const auto state = task().value("state").toString();
  if (target == "in_progress") {
    const auto ids = queueIds();
    if (state != "queued" || ids.isEmpty() || ids.first().toString() != m_task)
      return false;
    for (const auto &row : tasks())
      if (row.toMap().value("state") == "in_progress")
        return false;
    return true;
  }
  if (target == "done")
    return state == "in_progress";
  if (target == "cancelled")
    return QStringList{"backlog", "queued", "in_progress"}.contains(state);
  if (target == "backlog")
    return QStringList{"in_progress", "done", "cancelled"}.contains(state);
  return false;
}
bool Panel::transition(const QString &state) {
  if (!allowed(state))
    return false;
  QVariantMap payload{{"state", state},
                      {"expected_version", task().value("version")}};
  if (task().value("state") == "queued")
    payload.insert("expected_queue_version", project().value("queue_version"));
  return m_client->save("POST", "/v1/tasks/" + m_task + "/transition", payload);
}
void Panel::moreHistory() {
  if (!m_task.isEmpty() && m_historyMore && !historyLoading())
    m_client->loadHistory(m_project, m_task, m_historyCursor);
}
