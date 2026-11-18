#pragma once
#include "teamoverviewmodel.h"

// Small presentation-only router. Manual selection/drafts remain owned by
// Panel.
class WorkspaceController : public QObject {
  Q_OBJECT
  QML_ELEMENT
  QML_UNCREATABLE("Provided by the application")
  Q_PROPERTY(QString route READ route NOTIFY changed)
  Q_PROPERTY(TeamOverviewModel *team READ team CONSTANT)
  Q_PROPERTY(QVariantMap selectedObserver READ selectedObserver NOTIFY changed)
  Q_PROPERTY(bool connectionsOpen READ connectionsOpen WRITE setConnectionsOpen
                 NOTIFY changed)
  Q_PROPERTY(bool windowVisible READ windowVisible WRITE setWindowVisible NOTIFY
                 changed)
  Q_PROPERTY(bool minimized READ minimized WRITE setMinimized NOTIFY changed)
  Q_PROPERTY(bool draftHidden READ draftHidden NOTIFY changed)
  Q_PROPERTY(bool editorVisible READ editorVisible NOTIFY changed)
  Q_PROPERTY(bool closeAllowed READ closeAllowed NOTIFY changed)
  Q_PROPERTY(bool manualDetails READ manualDetails NOTIFY changed)
public:
  explicit WorkspaceController(Panel *panel, QObject *parent = nullptr);
  QString route() const { return m_route; }
  TeamOverviewModel *team() { return &m_team; }
  QVariantMap selectedObserver() const;
  bool connectionsOpen() const { return m_connections; }
  bool windowVisible() const { return m_visible; }
  bool minimized() const { return m_minimized; }
  bool draftHidden() const;
  bool editorVisible() const;
  Q_INVOKABLE bool openTask(const QString &id);
  bool closeAllowed() const;
  bool manualDetails() const {
    return m_manualDetails || !m_panel->editorKind().isEmpty();
  }
  Q_INVOKABLE void showManualList();
  Q_INVOKABLE void showManualDetails();
  Q_INVOKABLE void clearObserver();
  void setConnectionsOpen(bool value);
  void setWindowVisible(bool value);
  void setMinimized(bool value);
  Q_INVOKABLE void showTeam();
  Q_INVOKABLE void showProjects();
  Q_INVOKABLE void returnToEditor();
  Q_INVOKABLE bool openProject(const QString &id);
  Q_INVOKABLE bool selectObserver(const QString &observer,
                                  const QString &runtime);
  Q_INVOKABLE bool openObserverProject(const QString &observer,
                                       const QString &runtime,
                                       const QString &project, qint64 version);
  Q_INVOKABLE bool openObserverSession(const QString &observer,
                                       const QString &runtime, qint64 version);
  Q_INVOKABLE bool saveShortcut();
  Q_INVOKABLE bool escapeShortcut();
signals:
  void changed();

private:
  void setRoute(const QString &value);
  void updateActivation();
  Panel *m_panel;
  TeamOverviewModel m_team;
  QString m_route = "team", m_observer, m_runtime, m_task, m_editor;
  bool m_manualDetails = false;
  bool m_connections = false, m_visible = false, m_minimized = false;
};
