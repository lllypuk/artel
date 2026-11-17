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
      if (!m_task.isEmpty())
        moreHistory();
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
  moreHistory();
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
