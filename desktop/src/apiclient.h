#pragma once

#include <QAbstractListModel>
#include <QDateTime>
#include <QJsonArray>
#include <QJsonObject>
#include <QNetworkAccessManager>
#include <QNetworkReply>
#include <QTimer>
#include <QUrl>

// A read-only model; drafts never share storage with snapshot rows.
class ObjectModel : public QAbstractListModel {
  Q_OBJECT
public:
  explicit ObjectModel(QObject *parent = nullptr)
      : QAbstractListModel(parent) {}
  int rowCount(const QModelIndex &parent = {}) const override;
  QVariant data(const QModelIndex &index, int role) const override;
  QHash<int, QByteArray> roleNames() const override;
  Q_INVOKABLE QVariantMap get(int row) const;
  void replace(const QJsonArray &rows);

private:
  QJsonArray m_rows;
};

class ApiClient : public QObject {
  Q_OBJECT
  Q_PROPERTY(ObjectModel *projects READ projects CONSTANT)
  Q_PROPERTY(ObjectModel *tasks READ tasks CONSTANT)
  Q_PROPERTY(ObjectModel *queues READ queues CONSTANT)
  Q_PROPERTY(QString status READ status NOTIFY changed)
  Q_PROPERTY(QString error READ error NOTIFY changed)
  Q_PROPERTY(bool online READ online NOTIFY changed)
  Q_PROPERTY(bool loading READ loading NOTIFY changed)
  Q_PROPERTY(bool pending READ pending NOTIFY changed)
  Q_PROPERTY(bool uncertain READ uncertain NOTIFY changed)
  Q_PROPERTY(bool conflict READ conflict NOTIFY changed)
  Q_PROPERTY(bool dirty READ dirty NOTIFY changed)
  Q_PROPERTY(QVariantMap draft READ draft WRITE setDraft NOTIFY changed)
  Q_PROPERTY(QDateTime lastUpdated READ lastUpdated NOTIFY changed)
public:
  explicit ApiClient(QObject *parent = nullptr);
  ~ApiClient() override;
  static QString defaultTokenPath();
  static bool validEndpoint(const QUrl &url);
  bool configure(const QUrl &endpoint, const QString &tokenFile);
  void setTimeout(int milliseconds);
  bool historyLoading() const { return m_historyReply; }
  void loadHistory(const QString &project, const QString &task, qint64 after);
  void cancelHistory();
  ObjectModel *projects() { return &m_projects; }
  ObjectModel *tasks() { return &m_tasks; }
  ObjectModel *queues() { return &m_queues; }
  QString status() const { return m_status; }
  QString error() const { return m_error.isEmpty() ? m_historyError : m_error; }
  bool online() const { return m_online; }
  bool loading() const { return m_snapshot; }
  bool pending() const { return m_mutation; }
  bool uncertain() const { return m_uncertain; }
  bool canAbandon() const {
    return m_uncertain && m_online && !pending() && !m_stopped &&
           !m_instance.isEmpty() && m_mutationInstance != m_instance;
  }
  bool conflict() const { return m_conflict; }
  bool dirty() const { return !m_draft.isEmpty(); }
  QVariantMap draft() const { return m_draft; }
  void setDraft(const QVariantMap &draft);
  QDateTime lastUpdated() const { return m_updated; }
  QString instance() const { return m_instance; }
  qint64 sequence() const { return m_sequence; }
  Q_INVOKABLE void reload();
  Q_INVOKABLE void stop();
  // Payload includes expected_version/expected_queue_version from the editor's
  // original snapshot. Never silently replace these with a newer version.
  Q_INVOKABLE bool save(const QString &method, const QString &path,
                        const QVariantMap &payload);
  Q_INVOKABLE bool retry();
  // Explicit acknowledgement only after database replacement. Keeps the draft.
  bool abandonUncertain();
  Q_INVOKABLE void discardDraft();
signals:
  void changed();
  void mutationSucceeded(const QVariantMap &result, const QString &instance);
  void historyReceived(const QVariantList &events, qint64 cursor, bool more);

private:
  QNetworkReply *request(const QByteArray &method, const QString &path,
                         const QByteArray &body = {},
                         const QByteArray &key = {});
  void snapshotFinished(QNetworkReply *reply, quint64 generation);
  void subscribe();
  void streamReady(QNetworkReply *reply, quint64 generation);
  void fail(const QString &message);
  void sendMutation();
  void cancel(QNetworkReply *&reply);
  QNetworkAccessManager m_network;
  ObjectModel m_projects, m_tasks, m_queues;
  QUrl m_endpoint;
  QByteArray m_token;
  QNetworkReply *m_snapshot = nullptr, *m_stream = nullptr,
                *m_mutation = nullptr, *m_historyReply = nullptr;
  QTimer m_reconnect, m_coalesce;
  quint64 m_generation = 0, m_draftRevision = 0, m_sentRevision = 0;
  QString m_instance, m_status = QStringLiteral("disconnected"), m_error,
                      m_historyError;
  qint64 m_sequence = 0, m_cursor = 0;
  bool m_online = false, m_uncertain = false, m_conflict = false,
       m_reloadAgain = false, m_stopped = true;
  int m_timeout = 5000;
  QVariantMap m_draft;
  QByteArray m_method, m_body, m_key;
  QString m_path, m_mutationInstance;
  QDateTime m_updated;
};
