#pragma once
#include "apiclient.h"
#include <QObject>
#include <QtQml/qqmlregistration.h>

// Presentation state and actions live here, not in QML. Editor base versions
// are captured once and survive snapshot replacement and conflicts.
class Panel : public QObject {
  Q_OBJECT
  QML_ELEMENT
  QML_UNCREATABLE("Provided by the application")
  Q_PROPERTY(QVariantList projects READ projects NOTIFY changed)
  Q_PROPERTY(QVariantList tasks READ tasks NOTIFY changed)
  Q_PROPERTY(QVariantList history READ history NOTIFY changed)
  Q_PROPERTY(QVariant project READ project NOTIFY changed)
  Q_PROPERTY(QVariant task READ task NOTIFY changed)
  Q_PROPERTY(QVariant editor READ editor NOTIFY changed)
  Q_PROPERTY(QString editorKind READ editorKind NOTIFY changed)
  Q_PROPERTY(QString message READ message NOTIFY changed)
  Q_PROPERTY(QString currentText READ currentText NOTIFY changed)
  Q_PROPERTY(QString connection READ connection NOTIFY changed)
  Q_PROPERTY(QString updated READ updated NOTIFY changed)
  Q_PROPERTY(bool writable READ writable NOTIFY changed)
  Q_PROPERTY(bool busy READ busy NOTIFY changed)
  Q_PROPERTY(bool saveable READ saveable NOTIFY changed)
  Q_PROPERTY(bool canArchive READ canArchive NOTIFY changed)
  Q_PROPERTY(bool retryable READ retryable NOTIFY changed)
  Q_PROPERTY(bool dirty READ dirty NOTIFY changed)
  Q_PROPERTY(bool uncertain READ uncertain NOTIFY changed)
  Q_PROPERTY(bool conflict READ conflict NOTIFY changed)
  Q_PROPERTY(bool historyMore READ historyMore NOTIFY changed)
  Q_PROPERTY(bool historyLoading READ historyLoading NOTIFY changed)
  Q_PROPERTY(
      bool showArchived READ showArchived WRITE setShowArchived NOTIFY changed)
public:
  explicit Panel(ApiClient *client, QObject *parent = nullptr);
  QVariantList projects() const;
  QVariantList tasks() const;
  QVariantList history() const;
  QVariantMap project() const;
  QVariantMap task() const;
  QVariantMap editor() const { return m_client->draft(); }
  QString editorKind() const { return m_kind; }
  QString message() const;
  QString currentText() const;
  QString connection() const;
  QString updated() const;
  bool writable() const;
  bool canArchive() const;
  bool saveable() const;
  bool dirty() const { return m_client->dirty(); }
  bool uncertain() const { return m_client->uncertain(); }
  bool conflict() const { return m_client->conflict() && !m_adopted; }
  bool busy() const { return m_client->pending(); }
  bool retryable() const {
    return m_client->online() && !busy() && uncertain();
  }
  bool historyMore() const { return m_historyMore; }
  bool historyLoading() const { return m_client->historyLoading(); }
  bool showArchived() const { return m_archived; }
  void setShowArchived(bool value);
  Q_INVOKABLE bool selectProject(const QString &id);
  Q_INVOKABLE bool selectTask(const QString &id);
  Q_INVOKABLE bool editProject(bool create = false);
  Q_INVOKABLE bool editTask(bool create = false);
  Q_INVOKABLE void field(const QString &name, const QString &value);
  Q_INVOKABLE bool save();
  Q_INVOKABLE void discard();
  Q_INVOKABLE void reload();
  Q_INVOKABLE bool rebase();
  Q_INVOKABLE bool retry();
  Q_INVOKABLE bool archive();
  Q_INVOKABLE bool queue(const QString &id, int direction);
  Q_INVOKABLE bool transition(const QString &state);
  Q_INVOKABLE bool allowed(const QString &state) const;
  Q_INVOKABLE void moreHistory();
  Q_INVOKABLE QString stateLabel(const QString &state) const;
signals:
  void changed();

private:
  QVariantList queueIds() const;
  bool begin(const QString &kind, const QVariantMap &value);
  ApiClient *m_client;
  QString m_project, m_task, m_kind, m_editorId, m_editorProject,
      m_historyInstance, m_editorInstance;
  qint64 m_seenSequence = -1;
  qint64 m_version = 0, m_historyCursor = 0;
  QVariantList m_history;
  bool m_archived = false, m_historyMore = false, m_adopted = false;
};
