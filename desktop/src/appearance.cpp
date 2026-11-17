#include "appearance.h"
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QGuiApplication>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLocale>
#include <QRegularExpression>
#include <QStandardPaths>

Appearance::Appearance(const QString &theme, const QString &language,
                       const QString &command, QObject *parent)
    : QTranslator(parent),
      m_theme(theme.isEmpty() ? QStandardPaths::writableLocation(
                                    QStandardPaths::GenericStateLocation) +
                                    "/omarchy/current"
                              : theme),
      m_languageFile(language.isEmpty()
                         ? QStandardPaths::writableLocation(
                               QStandardPaths::GenericConfigLocation) +
                               "/omarchy/language"
                         : language),
      m_command(command) {
  if (m_command.isEmpty() && theme.isEmpty() &&
      !qEnvironmentVariableIsEmpty("HYPRLAND_INSTANCE_SIGNATURE"))
    m_command = QStandardPaths::findExecutable("hyprctl");
  m_debounce.setSingleShot(true);
  m_debounce.setInterval(80);
  connect(&m_watcher, &QFileSystemWatcher::fileChanged, this,
          [this] { m_debounce.start(); });
  connect(&m_watcher, &QFileSystemWatcher::directoryChanged, this,
          [this] { m_debounce.start(); });
  connect(&m_debounce, &QTimer::timeout, this, &Appearance::refresh);
  m_deadline.setSingleShot(true);
  m_deadline.setInterval(500);
  connect(&m_deadline, &QTimer::timeout, &m_process, &QProcess::kill);
  connect(&m_process, &QProcess::errorOccurred, this,
          [this] { m_deadline.stop(); });
  connect(&m_process, &QProcess::finished, this,
          [this](int code, QProcess::ExitStatus status) {
            m_deadline.stop();
            const auto value =
                QJsonDocument::fromJson(m_process.readAllStandardOutput())
                    .object()
                    .value("int");
            if (code == 0 && status == QProcess::NormalExit &&
                value.isDouble() && value.toInt(-1) >= 0 &&
                value.toInt() <= 100 && m_radius != value.toInt()) {
              m_radius = value.toInt();
              emit changed();
            }
          });
  m_poll.setInterval(1000);
  connect(&m_poll, &QTimer::timeout, this, [this] {
    if (!m_command.isEmpty() && m_process.state() == QProcess::NotRunning) {
      m_process.start(m_command, {"-j", "getoption", "decoration:rounding"});
      m_deadline.start();
    }
  });
  if (!m_command.isEmpty())
    m_poll.start();
  refresh();
}
Appearance::~Appearance() {
  if (m_process.state() != QProcess::NotRunning) {
    m_process.kill();
    m_process.waitForFinished(1000);
  }
}
QString Appearance::translate(const char *, const char *source, const char *,
                              int) const {
  return m_strings.value(QString::fromUtf8(source));
}
void Appearance::refresh() {
  const auto previousAccent = m_accent;
  const auto previousLanguage = m_language;
  const auto paths = m_watcher.files() + m_watcher.directories();
  if (!paths.isEmpty())
    m_watcher.removePaths(paths);
  QStringList candidates{m_theme, m_theme + "/theme",
                         m_theme + "/theme/colors.toml", m_languageFile};
  QStringList watches;
  for (const auto &path : candidates) {
    QString ancestor = path;
    while (!QFileInfo::exists(ancestor) && ancestor != "/")
      ancestor = QFileInfo(ancestor).absolutePath();
    watches << ancestor << QFileInfo(ancestor).absolutePath();
  }
  watches.removeDuplicates();
  for (const auto &path : watches)
    if (QFileInfo::exists(path))
      m_watcher.addPath(path);
  QFile colors(m_theme + "/theme/colors.toml");
  m_accent = "#FFD60A";
  if (colors.open(QIODevice::ReadOnly)) {
    static const QRegularExpression re(
        R"re(^\s*accent\s*=\s*["'](#[0-9a-fA-F]{6})["'])re",
        QRegularExpression::MultilineOption);
    const auto match = re.match(QString::fromUtf8(colors.read(65536)));
    if (match.hasMatch())
      m_accent = match.captured(1);
  }
  QFile language(m_languageFile);
  QString code = "en";
  if (language.open(QIODevice::ReadOnly))
    code =
        QString::fromUtf8(language.readLine(64)).trimmed().section('-', 0, 0);
  m_language = code == "ru" ? "ru" : "en";
  m_strings.clear();
  QFile catalog(":/i18n/" + m_language + ".json");
  if (catalog.open(QIODevice::ReadOnly)) {
    const auto obj = QJsonDocument::fromJson(catalog.readAll()).object();
    for (auto it = obj.begin(); it != obj.end(); ++it)
      m_strings.insert(it.key(), it.value().toString());
  }
  QLocale::setDefault(QLocale(m_language));
  QGuiApplication::setLayoutDirection(Qt::LeftToRight);
  if (previousAccent != m_accent || previousLanguage != m_language)
    emit changed();
}
