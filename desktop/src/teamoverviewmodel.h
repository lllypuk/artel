#pragma once
#include "panel.h"
#include <QAbstractListModel>
#include <QDateTime>
#include <functional>

// Presentation-only roster. One row per non-revoked binding, not per runtime.
class TeamOverviewModel : public QAbstractListModel {
  Q_OBJECT
  QML_ELEMENT
  QML_UNCREATABLE("Provided by the application")
  Q_PROPERTY(int total READ total NOTIFY changed)
  Q_PROPERTY(int shown READ shown NOTIFY changed)
  Q_PROPERTY(int unbound READ unbound NOTIFY changed)
  Q_PROPERTY(bool cached READ cached NOTIFY changed)
  Q_PROPERTY(QString search READ search WRITE setSearch NOTIFY changed)
  Q_PROPERTY(QString projectId READ projectId WRITE setProjectId NOTIFY changed)
  Q_PROPERTY(QString activity READ activity WRITE setActivity NOTIFY changed)
  Q_PROPERTY(bool visible READ visible WRITE setVisible NOTIFY changed)
  Q_PROPERTY(QVariantMap counts READ counts NOTIFY changed)
public:
  using Clock = std::function<QDateTime()>;
  explicit TeamOverviewModel(Panel *panel, QObject *parent = nullptr,
                             Clock clock = QDateTime::currentDateTimeUtc);
  int rowCount(const QModelIndex &parent = {}) const override;
  QVariant data(const QModelIndex &index, int role) const override;
  QHash<int, QByteArray> roleNames() const override;
  int total() const { return m_all.size(); }
  int shown() const { return m_rows.size(); }
  int unbound() const { return m_unbound; }
  bool cached() const { return !m_current; }
  QString search() const { return m_search; }
  QString projectId() const { return m_project; }
  QString activity() const { return m_activity; }
  bool visible() const { return m_visible; }
  QVariantMap counts() const { return m_counts; }
  void setSearch(const QString &value);
  void setProjectId(const QString &value);
  void setActivity(const QString &value);
  void setVisible(bool value);
  // Update ages and apply cache expiry (which can change filters and counts).
  // Never poll or extend server freshness.
  void tick();
  Q_INVOKABLE QVariantMap find(const QString &observer,
                               const QString &runtime) const;
signals:
  void changed();

private:
  void rebuild();
  QVariantMap presented(QVariantMap row) const;
  Panel *m_panel;
  ObserverClient *m_observers;
  Clock m_clock;
  QTimer m_timer;
  QVariantList m_all, m_rows;
  QVariantMap m_counts;
  QString m_search, m_project, m_activity;
  int m_unbound = 0;
  bool m_current = false, m_visible = false;
};
