#include "workspacecontroller.h"

WorkspaceController::WorkspaceController(Panel *panel, QObject *parent)
    : QObject(parent), m_panel(panel), m_team(panel, this) {
  connect(panel, &Panel::changed, this, [this] {
    const auto task = m_panel->task().value("id").toString();
    const auto editor = m_panel->editorKind();
    if (task != m_task || editor != m_editor)
      m_manualDetails = !task.isEmpty() || !editor.isEmpty();
    m_task = task;
    m_editor = editor;
    emit changed();
  });
  connect(panel, &Panel::answerDraftsChanged, this,
          &WorkspaceController::changed);
  connect(&m_team, &TeamOverviewModel::changed, this, [this] {
    if (selectedObserver().isEmpty()) {
      m_observer.clear();
      m_runtime.clear();
    }
    emit changed();
  });
}
QVariantMap WorkspaceController::selectedObserver() const {
  return m_team.find(m_observer, m_runtime);
}
bool WorkspaceController::draftHidden() const {
  return (!m_panel->editorKind().isEmpty() && !editorVisible()) ||
         (m_route == "team" && m_panel->uncertain());
}
bool WorkspaceController::editorVisible() const {
  return m_route != "team" && !m_panel->editorKind().isEmpty();
}
bool WorkspaceController::openTask(const QString &id) {
  if (!m_panel->selectTask(id))
    return false;
  showManualDetails();
  return true;
}
bool WorkspaceController::closeAllowed() const {
  return !m_panel->dirty() && !m_panel->hasAnswerDrafts() && !m_panel->busy() &&
         !m_panel->uncertain();
}
void WorkspaceController::updateActivation() {
  const bool visible = m_visible && !m_minimized;
  m_panel->observers()->setActive(visible &&
                                  (m_route == "team" || m_connections));
  m_team.setVisible(visible && m_route == "team");
}
void WorkspaceController::setRoute(const QString &value) {
  if (m_route == value)
    return;
  m_route = value;
  updateActivation();
  emit changed();
}
void WorkspaceController::showTeam() { setRoute("team"); }
void WorkspaceController::showProjects() { setRoute("projects"); }
void WorkspaceController::returnToEditor() {
  m_manualDetails = true;
  setRoute("workspace");
  emit changed();
}
void WorkspaceController::showManualList() {
  m_manualDetails = false;
  emit changed();
}
void WorkspaceController::showManualDetails() {
  m_manualDetails = true;
  emit changed();
}
void WorkspaceController::clearObserver() {
  m_observer.clear();
  m_runtime.clear();
  emit changed();
}
bool WorkspaceController::openProject(const QString &id) {
  // Do not route away or silently discard when the existing manual guard
  // refuses.
  bool exists = false;
  for (const auto &p : m_panel->projectSnapshot())
    exists |= p.toMap().value("id") == id;
  if (!exists || !m_panel->selectProject(id))
    return false;
  setRoute("workspace");
  return true;
}
void WorkspaceController::setConnectionsOpen(bool value) {
  if (m_connections == value)
    return;
  m_connections = value;
  updateActivation();
  emit changed();
}
void WorkspaceController::setWindowVisible(bool value) {
  if (m_visible == value)
    return;
  m_visible = value;
  updateActivation();
  emit changed();
}
void WorkspaceController::setMinimized(bool value) {
  if (m_minimized == value)
    return;
  m_minimized = value;
  updateActivation();
  emit changed();
}
bool WorkspaceController::selectObserver(const QString &observer,
                                         const QString &runtime) {
  if (m_team.find(observer, runtime).isEmpty())
    return false;
  m_observer = observer;
  m_runtime = runtime;
  emit changed();
  return true;
}
bool WorkspaceController::openObserverProject(const QString &observer,
                                              const QString &runtime,
                                              const QString &project,
                                              qint64 version) {
  const auto row = selectedObserver();
  if (row.value("observer_id") != observer ||
      row.value("runtime_id") != runtime ||
      row.value("project_id") != project ||
      row.value("version").toLongLong() != version ||
      row.value("inconsistent").toBool() ||
      !row.value("project_available").toBool() ||
      !m_panel->observers()->cacheCurrent())
    return false;
  return openProject(project);
}
bool WorkspaceController::openObserverSession(const QString &observer,
                                              const QString &runtime,
                                              qint64 version) {
  const auto row = selectedObserver();
  if (row.value("observer_id") != observer ||
      row.value("runtime_id") != runtime ||
      row.value("version").toLongLong() != version ||
      !row.value("navigable").toBool())
    return false;
  return m_panel->observers()->openSession(observer, runtime);
}
bool WorkspaceController::saveShortcut() {
  return editorVisible() && m_panel->saveable() && m_panel->save();
}
bool WorkspaceController::escapeShortcut() {
  if (!editorVisible() || m_panel->busy() || m_panel->uncertain() ||
      m_panel->dirty())
    return false;
  m_panel->discard();
  return true;
}
