#pragma once
#include "agtermadapter.h"
#include "apiclient.h"
#include <QElapsedTimer>
#include <QPointer>
#include <QtQml/qqmlregistration.h>

// Independent latest-state projection. No manual draft/mutation slots are used.
class ObserverClient : public QObject {
  Q_OBJECT
  QML_ELEMENT
  QML_UNCREATABLE("Provided by the application")
  Q_PROPERTY(QVariantList rows READ rows NOTIFY changed)
  Q_PROPERTY(AgtermAdapter *navigation READ navigation CONSTANT)
  Q_PROPERTY(bool active READ active WRITE setActive NOTIFY changed)
  Q_PROPERTY(bool online READ online NOTIFY changed)
  Q_PROPERTY(bool busy READ busy NOTIFY changed)
  Q_PROPERTY(bool canBind READ canBind NOTIFY changed)
  Q_PROPERTY(QString message READ message NOTIFY changed)
  Q_PROPERTY(QString confirmation READ confirmation NOTIFY changed)
  Q_PROPERTY(QString projectName READ projectName NOTIFY changed)
  Q_PROPERTY(
      bool onlyProject READ onlyProject WRITE setOnlyProject NOTIFY changed)
public:
  explicit ObserverClient(ApiClient *owner, QObject *parent = nullptr);
  ~ObserverClient() override;
  QVariantList rows() const;
  AgtermAdapter *navigation() { return &m_navigation; }
  Q_INVOKABLE void openSession(const QString &observer, const QString &runtime);
  bool active() const { return m_active; }
  void setActive(bool value);
  void setProject(const QVariantMap &project);
  bool online() const { return m_online; }
  bool busy() const { return m_mutation; }
  bool canBind() const;
  QString projectName() const { return m_project.value("name").toString(); }
  bool onlyProject() const { return m_onlyProject; }
  void setOnlyProject(bool value);
  QString message() const {
    return m_message.isEmpty() ? m_actionMessage : m_message;
  }
  QString confirmation() const;
  Q_INVOKABLE void reload();
  Q_INVOKABLE bool prepare(const QString &action, const QString &observer,
                           const QString &runtime);
  Q_INVOKABLE bool confirm();
  Q_INVOKABLE void cancelConfirmation();
  void setTimeout(int value) { m_timeout = value; }
signals:
  void changed();

private:
  QVariantList records(bool onlyProject) const;
  void resetRead();
  void fail(const QString &message);
  QNetworkReply *request(const QByteArray &method, const QString &path,
                         const QByteArray &body = {},
                         const QByteArray &key = {});
  AgtermAdapter m_navigation;
  ApiClient *m_owner;
  QNetworkAccessManager m_network;
  QPointer<QNetworkReply> m_read, m_mutation;
  QTimer m_poll;
  QElapsedTimer m_age;
  QJsonArray m_observers;
  QVariantMap m_project, m_confirmation;
  QString m_instance, m_message, m_actionMessage;
  quint64 m_generation = 0;
  bool m_active = false, m_online = false, m_onlyProject = false;
  int m_timeout = 2000, m_backoff = 2000;
};
