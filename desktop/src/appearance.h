#pragma once
#include <QFileSystemWatcher>
#include <QHash>
#include <QProcess>
#include <QTimer>
#include <QTranslator>
#include <QtQml/qqmlregistration.h>

// Read-only Omarchy adapters. Inject paths in tests; no settings are written.
// Watching/re-arming follows Omacut/Monologue (MIT, D. H. Hansson).
class Appearance : public QTranslator {
  Q_OBJECT
  QML_ELEMENT
  QML_UNCREATABLE("Provided by the application")
  Q_PROPERTY(QString accent READ accent NOTIFY changed)
  Q_PROPERTY(int radius READ radius NOTIFY changed)
  Q_PROPERTY(QString language READ language NOTIFY changed)
public:
  explicit Appearance(const QString &themeDirectory = {},
                      const QString &languageFile = {},
                      const QString &roundingCommand = {},
                      QObject *parent = nullptr);
  ~Appearance() override;
  QString accent() const { return m_accent; }
  int radius() const { return m_radius; }
  QString language() const { return m_language; }
  bool isEmpty() const override { return false; }
  QString translate(const char *, const char *source, const char * = nullptr,
                    int = -1) const override;
  Q_INVOKABLE void refresh();
signals:
  void changed();

private:
  QString m_theme, m_languageFile, m_command, m_accent = "#FFD60A",
                                              m_language = "en";
  int m_radius = 0;
  QHash<QString, QString> m_strings;
  QFileSystemWatcher m_watcher;
  QTimer m_debounce, m_poll, m_deadline;
  QProcess m_process;
};
