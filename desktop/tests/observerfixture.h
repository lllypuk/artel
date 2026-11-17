#pragma once
#include "uifixture.h"
#include <QJsonArray>

// Fictional, local-only projection. Task 7 supplies the real Pi/daemon oracle.
class ObserverFixture : public UiFixture {
public:
  const QString observer = "44444444-4444-4444-8444-444444444444";
  const QString runtime = "55555555-5555-4555-8555-555555555555";
  const QString otherRuntime = "66666666-6666-4666-8666-666666666666";
  QJsonObject registry;
  int reads = 0;
  ObserverFixture() {
    const auto registration = [this](const QString &id) {
      return QJsonObject{{"runtime_id", id},
                         {"pi_session_id", task},
                         {"pi_version", "1.5.2"},
                         {"bridge_version", "0.1.0"}};
    };
    QJsonArray runtimes;
    for (const auto &id : {runtime, otherRuntime})
      runtimes.append(QJsonObject{{"registration", registration(id)},
                                  {"disposition", "candidate"},
                                  {"activity", "working"},
                                  {"freshness", "fresh"},
                                  {"lifecycle", "running"},
                                  {"model", "fictional-model"},
                                  {"provider", "fixture"},
                                  {"last_seen", "2026-11-17T12:00:00Z"}});
    registry = {
        {"database_instance_id", instance},
        {"server_time", "2026-11-17T12:00:00Z"},
        {"observers", QJsonArray{QJsonObject{{"observer_id", observer},
                                             {"label", "Example observer"},
                                             {"version", 1},
                                             {"generation", 0},
                                             {"revoked", false},
                                             {"runtimes", runtimes}}}}};
    handler = [this](QTcpSocket *socket, const QByteArray &request) {
      observation(socket, request);
    };
  }
  QJsonObject record() const {
    return registry.value("observers").toArray().first().toObject();
  }
  void record(const QJsonObject &o) {
    registry.insert("observers", QJsonArray{o});
  }
  QByteArray bytes() const {
    return QJsonDocument(registry).toJson(QJsonDocument::Compact);
  }
  void observation(QTcpSocket *socket, const QByteArray &request) {
    if (request.startsWith("GET /v1/observers ")) {
      ++reads;
      respond(socket, 200, bytes());
    } else if (!request.startsWith("GET") &&
               request.contains("/v1/observers/")) {
      auto o = record();
      const auto body =
          QJsonDocument::fromJson(request.mid(request.indexOf("\r\n\r\n") + 4))
              .object();
      if (body.value("expected_version") != o.value("version")) {
        respond(socket, 409, "{\"error\":\"version_conflict\"}");
        return;
      }
      o.insert("version", o.value("version").toInt() + 1);
      if (request.contains("/binding ")) {
        if (body.value("runtime_id").isNull())
          o.remove("binding");
        else
          o.insert("binding",
                   QJsonObject{{"runtime_id", body.value("runtime_id")},
                               {"project_id", body.value("project_id")}});
        auto runtimes = o.value("runtimes").toArray();
        for (int i = 0; i < runtimes.size(); ++i) {
          auto r = runtimes[i].toObject();
          const bool bound =
              r.value("registration").toObject().value("runtime_id") ==
              body.value("runtime_id");
          if (bound || r.value("disposition") == "bound")
            r.insert("disposition", bound ? "bound" : "superseded");
          runtimes[i] = r;
        }
        o.insert("runtimes", runtimes);
      } else if (request.contains("/revoke "))
        o.insert("revoked", true);
      else if (request.contains("/prune ")) {
        QJsonArray kept;
        for (const auto &r : o.value("runtimes").toArray())
          if (r.toObject().value("disposition") == "bound")
            kept.append(r);
        o.insert("runtimes", kept);
      }
      record(o);
      respond(socket, 200,
              "{\"database_instance_id\":\"" + instance.toUtf8() + "\"}");
    } else
      normal(socket, request);
  }
};
