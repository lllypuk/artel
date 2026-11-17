#pragma once
#include "agtermadapter.h"
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>

class AgtermFixture {
public:
  const QString window = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  const QString otherWindow = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  const QString session = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
  const QString pane = "dddddddd-dddd-4ddd-8ddd-dddddddddddd";
  QString mode;
  QList<QStringList> calls;
  int lists = 0;
  QVariantMap hint() const {
    return {{"window_id", window}, {"session_id", session}, {"pane_id", pane}};
  }
  static QByteArray response(const QJsonObject &r) {
    return QJsonDocument(QJsonObject{{"ok", true}, {"result", r}}).toJson();
  }
  QByteArray reply(const QStringList &args) {
    calls << args;
    if (mode == "oversize")
      return QByteArray(1024 * 1024 + 1, 'x');
    if (mode == "malformed")
      return "broken";
    if (args.first() == "version")
      return response(
          {{"app", QJsonObject{
                       {"version", mode == "version" ? "99.0.0" : "0.40.0"}}}});
    if (args.contains("--help"))
      return mode == "capability" ? "unsupported"
                                  : "--target --window --socket --json";
    if (args.first() == "window") {
      ++lists;
      if (mode == "window-format")
        return response({{"windows", "unknown"}});
      QJsonArray windows;
      for (const auto &id : {window, otherWindow})
        windows.append(QJsonObject{{"id", id}, {"open", true}});
      if (mode == "windows")
        for (int i = 0; i < 15; ++i)
          windows.append(windows.first());
      return response({{"windows", windows}});
    }
    if (args.first() == "tree") {
      if (mode == "tree-format")
        return response({{"tree", QJsonObject{{"workspaces", "unknown"}}}});
      if (mode == "tree-oversize")
        return QByteArray(1024 * 1024, 'x');
      const auto id = args.at(args.indexOf("--window") + 1);
      QJsonArray sessions;
      const auto targetWindow =
          mode == "move" && lists == 1 ? window : otherWindow;
      if ((id == targetWindow || mode == "ambiguous") && mode != "missing" &&
          !(mode == "disappeared" && lists == 2)) {
        sessions.append(QJsonObject{
            {"id", session},
            {"realized", mode != "unrealized"},
            {"surfaces",
             QJsonArray{QJsonObject{
                 {"id", mode == "swap" && lists == 2 ? "left" : "right"},
                 {"paneID", mode == "pane" ? window : pane}}}}});
      }
      return response(
          {{"tree",
            QJsonObject{{"workspaces",
                         QJsonArray{QJsonObject{{"sessions", sessions}}}}}}});
    }
    return mode == "select-failure"
               ? QByteArray("{\"ok\":false,\"error\":\"SECRET\"}")
               : response({{"id", session}});
  }
  void install(AgtermAdapter *adapter) {
    adapter->setSocket("/fictional/owner.sock");
    adapter->setRunner(
        [this](const QStringList &args, AgtermAdapter::Done done) {
          if (mode == "timeout")
            return;
          done(mode != "killed", reply(args));
        });
  }
};
