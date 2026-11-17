#include "agtermadapter.h"
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QRegularExpression>
#include <QSet>
#include <memory>

namespace {
bool uuid(const QString &s) {
  static const QRegularExpression pattern(
      "^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]"
      "{12}$");
  return pattern.match(s).hasMatch();
}
QJsonObject result(const QByteArray &bytes) {
  const auto doc = QJsonDocument::fromJson(bytes);
  const auto o = doc.object();
  return o.value("ok") == true ? o.value("result").toObject() : QJsonObject{};
}
} // namespace
AgtermAdapter::AgtermAdapter(QObject *parent) : QObject(parent) {
  m_deadline.setSingleShot(true);
  connect(&m_deadline, &QTimer::timeout, this, [this] { finish(false); });
}
AgtermAdapter::~AgtermAdapter() {
  if (m_process) {
    m_process->disconnect(this);
    m_process->kill();
    m_process->waitForFinished(100);
  }
}
bool AgtermAdapter::validHint(const QVariantMap &hint) {
  return hint.size() == 3 && uuid(hint.value("window_id").toString()) &&
         uuid(hint.value("session_id").toString()) &&
         uuid(hint.value("pane_id").toString());
}
void AgtermAdapter::cancel() {
  if (m_busy)
    finish(false);
  m_message.clear();
  emit changed();
}
void AgtermAdapter::finish(bool success) {
  ++m_generation;
  m_deadline.stop();
  if (m_process) {
    m_process->disconnect(this);
    m_process->kill();
    m_process->waitForFinished(100);
    m_process->deleteLater();
    m_process = nullptr;
  }
  m_busy = false;
  m_message =
      success ? tr("Declared agterm session opened; exact pane focus is not "
                   "guaranteed.")
              : tr("agterm navigation unavailable. Observation is unaffected.");
  emit changed();
}
void AgtermAdapter::run(QStringList args, Done done) {
  args << "--socket" << m_socket;
  const auto generation = m_generation;
  QPointer<AgtermAdapter> guard(this);
  auto callback = [guard, generation, done](bool ok, QByteArray bytes) {
    if (!guard || !guard->m_busy || generation != guard->m_generation)
      return;
    guard->m_bytes += bytes.size();
    if (!ok || guard->m_bytes > 1024 * 1024) {
      guard->finish(false);
      return;
    }
    done(true, bytes);
  };
  if (m_runner)
    m_runner(args, callback);
  else
    process(args, callback);
}
void AgtermAdapter::process(const QStringList &args, Done done) {
  auto *p = new QProcess(this);
  m_process = p;
  auto output = std::make_shared<QByteArray>();
  connect(p, &QProcess::readyReadStandardOutput, this, [this, p, output] {
    output->append(p->readAllStandardOutput());
    if (output->size() + m_bytes > 1024 * 1024)
      finish(false);
  });
  // Drain but never display untrusted stderr (it may contain credentials).
  connect(p, &QProcess::readyReadStandardError, this, [this, p] {
    m_bytes += p->readAllStandardError().size();
    if (m_bytes > 1024 * 1024)
      finish(false);
  });
  connect(p, &QProcess::errorOccurred, this,
          [this](QProcess::ProcessError) { finish(false); });
  connect(p, &QProcess::finished, this,
          [this, p, output, done](int code, QProcess::ExitStatus status) {
            output->append(p->readAllStandardOutput());
            m_bytes += p->readAllStandardError().size();
            m_process = nullptr;
            p->deleteLater();
            done(code == 0 && status == QProcess::NormalExit, *output);
          });
  p->setProgram("agtermctl");
  p->setArguments(args);
  p->start();
}
void AgtermAdapter::open(const QVariantMap &hint) {
  if (m_busy)
    return;
  if (!validHint(hint) || !m_socket.startsWith('/') || m_socket.size() > 4096 ||
      m_socket.contains(QChar::Null)) {
    finish(false);
    return;
  }
  m_session = hint.value("session_id").toString().toLower();
  m_pane = hint.value("pane_id").toString().toLower();
  m_busy = true;
  m_message.clear();
  m_bytes = 0;
  m_pass = 0;
  ++m_generation;
  m_deadline.start(2000);
  emit changed();
  run({"version", "--json"}, [this](bool, QByteArray bytes) {
    // Tested public CLI dialect only; future formats fail closed.
    if (result(bytes).value("app").toObject().value("version") != "0.40.0") {
      finish(false);
      return;
    }
    run({"session", "select", "--help"}, [this](bool, QByteArray help) {
      if (!help.contains("--target") || !help.contains("--window") ||
          !help.contains("--socket") || !help.contains("--json")) {
        finish(false);
        return;
      }
      discover();
    });
  });
}
void AgtermAdapter::discover() {
  m_windows.clear();
  m_matches = 0;
  m_index = 0;
  m_found.clear();
  run({"window", "list", "--json"}, [this](bool, QByteArray bytes) {
    const auto value = result(bytes).value("windows");
    if (!value.isArray() || value.toArray().size() > 16) {
      finish(false);
      return;
    }
    QSet<QString> ids;
    for (const auto &v : value.toArray()) {
      const auto w = v.toObject();
      const auto id = w.value("id").toString().toLower();
      if (!uuid(id) || ids.contains(id) || !w.value("open").isBool()) {
        finish(false);
        return;
      }
      ids.insert(id);
      if (w.value("open").toBool())
        m_windows << id;
    }
    trees();
  });
}
void AgtermAdapter::trees() {
  if (m_index == m_windows.size()) {
    if (m_matches != 1) {
      finish(false);
      return;
    }
    // Re-enumerate once immediately before selection to cover a moved session.
    if (m_pass++ == 0) {
      discover();
      return;
    }
    run({"session", "select", "--target", m_session, "--window", m_found,
         "--json"},
        [this](bool, QByteArray bytes) {
          const auto o = QJsonDocument::fromJson(bytes).object();
          finish(o.value("ok") == true &&
                 o.value("result").toObject().value("id").toString().compare(
                     m_session, Qt::CaseInsensitive) == 0);
        });
    return;
  }
  const auto window = m_windows.at(m_index++);
  run({"tree", "--window", window, "--json"}, [this, window](bool,
                                                             QByteArray bytes) {
    const auto tree = result(bytes).value("tree").toObject();
    if (!tree.value("workspaces").isArray()) {
      finish(false);
      return;
    }
    for (const auto &wv : tree.value("workspaces").toArray()) {
      const auto w = wv.toObject();
      if (!w.value("sessions").isArray()) {
        finish(false);
        return;
      }
      for (const auto &sv : w.value("sessions").toArray()) {
        const auto s = sv.toObject();
        if (!uuid(s.value("id").toString()) || !s.value("surfaces").isArray()) {
          finish(false);
          return;
        }
        if (s.value("id").toString().compare(m_session, Qt::CaseInsensitive) !=
            0)
          continue;
        for (const auto &pv : s.value("surfaces").toArray()) {
          const auto p = pv.toObject();
          if (p.value("paneID").toString().compare(m_pane,
                                                   Qt::CaseInsensitive) == 0) {
            if (s.value("realized") != true) {
              finish(false);
              return;
            }
            ++m_matches;
            m_found = window;
          }
        }
      }
    }
    trees();
  });
}
