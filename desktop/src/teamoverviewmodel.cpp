#include "teamoverviewmodel.h"

namespace {
QString status(const QVariantMap &row, bool current) {
  if (row.value("inconsistent").toBool())
    return "inconsistent";
  if (!current)
    return "unknown";
  if (row.value("lifecycle") == "stopped" ||
      row.value("freshness") == "stopped")
    return "stopped";
  if (row.value("freshness") != "fresh")
    return "unknown";
  return row.value("activity").toString();
}
QString label(const QString &state) {
  if (state == "working")
    return TeamOverviewModel::tr("Working");
  if (state == "idle")
    return TeamOverviewModel::tr("Available");
  if (state == "waiting_user")
    return TeamOverviewModel::tr("Waiting for input in Pi");
  if (state == "stopped")
    return TeamOverviewModel::tr("Observation stopped");
  if (state == "inconsistent")
    return TeamOverviewModel::tr("Inconsistent observation");
  return TeamOverviewModel::tr("Current activity unknown");
}
} // namespace

TeamOverviewModel::TeamOverviewModel(Panel *panel, QObject *parent, Clock clock)
    : QAbstractListModel(parent), m_panel(panel),
      m_observers(panel->observers()), m_clock(std::move(clock)) {
  m_timer.setInterval(1000);
  connect(&m_timer, &QTimer::timeout, this, &TeamOverviewModel::tick);
  connect(m_observers, &ObserverClient::changed, this,
          &TeamOverviewModel::rebuild);
  connect(panel, &Panel::changed, this, &TeamOverviewModel::rebuild);
  rebuild();
}
int TeamOverviewModel::rowCount(const QModelIndex &parent) const {
  return parent.isValid() ? 0 : m_rows.size();
}
QHash<int, QByteArray> TeamOverviewModel::roleNames() const {
  return {{Qt::UserRole, "record"}};
}
QVariant TeamOverviewModel::data(const QModelIndex &index, int role) const {
  if (!index.isValid() || index.row() < 0 || index.row() >= m_rows.size() ||
      role != Qt::UserRole)
    return {};
  return presented(m_rows.at(index.row()).toMap());
}
QVariantMap TeamOverviewModel::presented(QVariantMap row) const {
  const auto state = status(row, m_current);
  row.insert("status", state);
  row.insert("status_text", label(state));
  row.insert("cached", !m_current);
  row.insert("last_freshness", row.value("freshness"));
  if (!m_current)
    row.insert("freshness", "unknown");
  // Saved last activity remains explicit; it is never advertised as current.
  row.insert("last_activity", row.value("activity"));
  if (state == "unknown" || state == "inconsistent" || state == "stopped")
    row.insert("activity", "unknown");
  const auto seen =
      QDateTime::fromString(row.value("last_seen").toString(), Qt::ISODate);
  const auto seconds =
      seen.isValid() ? qMax<qint64>(0, seen.secsTo(m_clock())) : -1;
  row.insert("age_seconds", seconds);
  row.insert("age_text", seconds < 0      ? tr("Time unavailable")
                         : seconds < 60   ? tr("Just now")
                         : seconds < 3600 ? tr("%1 min ago").arg(seconds / 60)
                         : seconds < 86400
                             ? tr("%1 h ago").arg(seconds / 3600)
                             : tr("%1 d ago").arg(seconds / 86400));
  return row;
}
void TeamOverviewModel::rebuild() {
  const bool current = m_observers->cacheCurrent();
  QVariantList all, rows;
  QVariantMap counts{{"working", 0}, {"idle", 0},    {"waiting_user", 0},
                     {"stopped", 0}, {"unknown", 0}, {"inconsistent", 0}};
  int unbound = 0;
  for (const auto &value : m_observers->projection()) {
    const auto observer = value.toMap();
    if (observer.value("revoked").toBool())
      continue;
    const auto binding = observer.value("binding").toMap();
    if (binding.isEmpty()) {
      ++unbound;
      continue;
    }
    const auto runtime = binding.value("runtime_id").toString();
    QVariantMap row;
    for (const auto &candidate : observer.value("runtimes").toList()) {
      const auto r = candidate.toMap();
      if (r.value("runtime_id") == runtime) {
        row = r;
        break;
      }
    }
    row.insert("inconsistent",
               row.isEmpty() || row.value("disposition") != "bound");
    row.insert("runtime_id", runtime);
    for (const auto *key : {"observer_id", "label", "version", "generation"})
      row.insert(key, observer.value(key));
    const auto project = binding.value("project_id").toString();
    row.insert("project_id", project);
    row.insert("project_name", tr("Unavailable project (%1)").arg(project));
    row.insert("project_available", false);
    for (const auto &p : m_panel->projectSnapshot()) {
      const auto owner = p.toMap();
      if (owner.value("id") == project) {
        row.insert("project_name", owner.value("name"));
        row.insert("project_available", !owner.value("archived").toBool());
        break;
      }
    }
    all.append(row);
    const auto state = status(row, current);
    counts[state] = counts.value(state).toInt() + 1;
    if ((!m_project.isEmpty() && m_project != project) ||
        (!m_activity.isEmpty() && m_activity != state &&
         m_activity !=
             (current ? row.value("freshness").toString() : "unknown")))
      continue;
    const auto text = row.value("label").toString() + " " +
                      row.value("project_name").toString() + " " + project;
    if (!text.contains(m_search, Qt::CaseInsensitive))
      continue;
    rows.append(row);
  }
  const bool changedRows = rows != m_rows;
  const bool changedState = current != m_current;
  if (changedRows)
    beginResetModel();
  m_current = current;
  m_all = all;
  m_rows = rows;
  m_unbound = unbound;
  m_counts = counts;
  if (changedRows)
    endResetModel();
  else if (changedState && !m_rows.isEmpty())
    emit dataChanged(index(0), index(m_rows.size() - 1));
  emit changed();
}
void TeamOverviewModel::setSearch(const QString &value) {
  if (m_search == value)
    return;
  m_search = value;
  rebuild();
}
void TeamOverviewModel::setProjectId(const QString &value) {
  if (m_project == value)
    return;
  m_project = value;
  rebuild();
}
void TeamOverviewModel::setActivity(const QString &value) {
  if (m_activity == value)
    return;
  m_activity = value;
  rebuild();
}
void TeamOverviewModel::setVisible(bool value) {
  if (m_visible == value)
    return;
  m_visible = value;
  if (value) {
    tick();
    m_timer.start();
  } else
    m_timer.stop();
  emit changed();
}
void TeamOverviewModel::tick() {
  if (m_current != m_observers->cacheCurrent())
    rebuild();
  if (!m_rows.isEmpty())
    emit dataChanged(index(0), index(m_rows.size() - 1), {Qt::UserRole});
  emit changed(); // Inspector age/translation bindings, not a model rebuild.
}
QVariantMap TeamOverviewModel::find(const QString &observer,
                                    const QString &runtime) const {
  for (const auto &value : m_rows) {
    const auto row = value.toMap();
    if (row.value("observer_id") == observer &&
        row.value("runtime_id") == runtime)
      return presented(row);
  }
  return {};
}
