#pragma once
#include <QPointer>
#include <QProcess>
#include <QTimer>
#include <QVariantMap>
#include <QtQml/qqmlregistration.h>
#include <functional>

// Owner-local process boundary; observer metadata never selects a
// program/socket.
class AgtermAdapter : public QObject {
  Q_OBJECT
  QML_ELEMENT
  QML_UNCREATABLE("Provided by the application")
  Q_PROPERTY(bool busy READ busy NOTIFY changed)
  Q_PROPERTY(QString message READ message NOTIFY changed)
public:
  using Done = std::function<void(bool, QByteArray)>;
  using Runner = std::function<void(const QStringList &, Done)>;
  explicit AgtermAdapter(QObject *parent = nullptr);
  ~AgtermAdapter() override;
  void setSocket(const QString &socket) { m_socket = socket; }
  void setRunner(Runner runner) { m_runner = std::move(runner); }
  bool busy() const { return m_busy; }
  QString message() const { return m_message; }
  static bool validHint(const QVariantMap &hint);
  Q_INVOKABLE void open(const QVariantMap &hint);
  void cancel();
signals:
  void changed();

private:
  void run(QStringList args, Done done);
  void process(const QStringList &args, Done done);
  void discover();
  void trees();
  void finish(bool success);
  Runner m_runner;
  QPointer<QProcess> m_process;
  QTimer m_deadline;
  QString m_socket, m_session, m_pane, m_message, m_found;
  QStringList m_windows;
  bool m_busy = false;
  int m_index = 0, m_matches = 0, m_pass = 0;
  qsizetype m_bytes = 0;
  quint64 m_generation = 0;
};
