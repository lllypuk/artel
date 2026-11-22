#include "panel.h"
#include <QFileInfo>
#include <QHash>
#include <QJsonDocument>
#include <QLocale>
#include <algorithm>

namespace {
QVariantMap find(ObjectModel *model, const QString &id) {
  for (int i = 0; i < model->rowCount(); ++i)
    if (model->get(i).value("id").toString() == id)
      return model->get(i);
  return {};
}
QVariantMap editableFields(const QString &editorKind,
                           const QVariantMap &value) {
  if (editorKind == "project")
    return {{"name", value.value("name", "")},
            {"root_path", value.value("root_path", "")},
            {"description", value.value("description", "")}};
  const auto workKind = value.value("kind", "task").toString();
  QVariantMap fields = {
      {"title", value.value("title", "")},
      {"description", value.value("description", "")},
      {"acceptance_criteria", value.value("acceptance_criteria", "")},
      {"kind", workKind}};
  if (workKind != "epic")
    fields.insert("parent_id", value.value("parent_id", ""));
  if (workKind == "bug") {
    fields.insert("expected_behavior", value.value("expected_behavior", ""));
    fields.insert("actual_behavior", value.value("actual_behavior", ""));
    fields.insert("fix_evidence", value.value("fix_evidence", ""));
  }
  return fields;
}
QVariantMap mergeQuestion(const QVariantMap &current, QVariantMap incoming) {
  if (current.value("status").toString() != "open" &&
      incoming.value("status").toString() == "open")
    return current;
  const auto known = current.value("answer_status").toString();
  const auto reported = incoming.value("answer_status").toString();
  if (known == "observed" || (known == "unknown" && reported == "stored"))
    incoming.insert("answer_status", known);
  if (incoming.value("answer_status").toString() == "observed")
    incoming.remove("answer_block_reason");
  else if (!current.value("answer_block_reason").toString().isEmpty())
    incoming.insert("answer_block_reason",
                    current.value("answer_block_reason"));
  return incoming;
}
} // namespace
Panel::Panel(ApiClient *client, QObject *parent)
    : QObject(parent), m_client(client), m_observers(client, this) {
  connect(this, &Panel::changed, this, [this] {
    m_observers.setProject(project());
    for (int i = 0; i < m_questions.rowCount(); ++i)
      m_questions.update(i, questionRow(m_questions.get(i)));
  });
  connect(client, &ApiClient::changed, this, [this] {
    if (m_client->conflict()) {
      if (!m_conflictTracked) {
        m_conflictTracked = true;
        m_conflictInstance = m_client->instance();
        m_conflictEditorId = m_editorId;
        m_conflictEditorKind = m_kind;
        m_conflictSnapshotRequestSerial = m_client->snapshotRequestSerial();
      }
    } else {
      m_conflictTracked = false;
    }
    const bool reconnected = m_client->online() && !m_wasOnline;
    m_wasOnline = m_client->online();
    if (m_seenSequence != m_client->sequence() ||
        m_historyInstance != m_client->instance()) {
      const bool sameInstance = m_historyInstance == m_client->instance();
      const bool preserveTask =
          sameInstance && !m_task.isEmpty() &&
          m_seenSequence != m_client->sequence() &&
          m_client->snapshotScopeKnown() &&
          !m_client->snapshotAffectsTask(m_project, m_task);
      m_seenSequence = m_client->sequence();
      m_historyInstance = m_client->instance();
      if (!preserveTask) {
        m_client->cancelHistory();
        m_history.clear();
        m_historyCursor = 0;
        m_historyMore = true;
        resetCommissions();
        resetResults(sameInstance);
        if (!sameInstance) {
          resetQuestions();
          emit detachedAnswerDraftsChanged();
        }
        if (!m_task.isEmpty()) {
          moreHistory();
          loadCommissionPage();
          loadResults();
        }
      }
    } else if (reconnected && !m_task.isEmpty()) {
      // A selection or reload made offline still needs its first reads on a
      // same-snapshot reconnect. Result recovery is independent of the
      // delivery timer, which may already be active for this selected task.
      if (!m_deliveryPoll.isActive()) {
        moreHistory();
        loadCommissionPage();
      }
      if (!m_resultsLoaded)
        loadResults();
    }
    if (!m_client->dirty() && !m_client->pending() && !m_client->uncertain())
      m_kind.clear();
    emit changed();
  });
  connect(client, &ApiClient::mutationSucceeded, this,
          [this](const QVariantMap &result, const QString &instance) {
            // A newer draft survives the reply, but its base must advance to
            // the confirmed save (not to a possibly newer SSE snapshot).
            if (!m_client->dirty() || m_kind.isEmpty() ||
                m_editorInstance != instance)
              return;
            m_editorBase = m_sentEditorBase;
            m_editorId = result.value("id").toString();
            m_version = result.value("version").toLongLong();
            m_adopted = false;
            if (m_kind == "project") {
              if (m_project != m_editorId)
                setProjectSelection(m_editorId);
            } else if (m_task != m_editorId) {
              setTaskSelection(m_editorId);
              // If SSE already loaded the new task, the ACK's reload may not
              // advance the snapshot again. Start its reads here in that case.
              if (!task().isEmpty()) {
                moreHistory();
                loadCommissionPage();
                loadResults();
              }
            }
          });
  connect(client, &ApiClient::historyReceived, this,
          [this](const QVariantList &events, qint64 cursor, bool more) {
            m_history += events;
            m_historyCursor = cursor;
            m_historyMore = more;
            emit changed();
          });
  connect(client, &ApiClient::currentResultReceived, this,
          [this](const QVariantMap &result) {
            if (m_task.isEmpty())
              return;
            m_currentResultData = result;
            m_currentResult = result.value("id").toString();
            m_currentResultData.insert("reviews", QVariantList{});
            m_currentReviewsCursor.clear();
            m_currentResultLoaded = true;
            // Confirmed absence needs no review read. A present candidate is
            // visible now, but decisions wait for every bounded review page.
            m_resultsLoaded = result.isEmpty();
            emit changed();
          });
  connect(client, &ApiClient::currentReviewsReceived, this,
          [this](const QString &result, const QVariantList &reviews,
                 const QString &after, const QString &cursor, bool more) {
            if (m_task.isEmpty() || result != m_currentResult ||
                after != m_currentReviewsCursor)
              return;
            auto all = m_currentResultData.value("reviews").toList();
            all += reviews;
            m_currentResultData.insert("reviews", all);
            m_currentReviewsCursor = cursor;
            m_resultsLoaded = !more;
            emit changed();
            if (more)
              m_client->loadCurrentReviews(m_task, result, cursor);
          });
  connect(client, &ApiClient::resultsReceived, this,
          [this](const QString &, const QVariantList &entries, qint64 after,
                 qint64 cursor, bool more) {
            if (m_task.isEmpty() || (after != 0 && after != m_resultsCursor))
              return;
            if (after == 0)
              m_resultEntries.clear();
            m_resultEntries += entries;
            m_resultsCursor = cursor;
            m_resultsMore = more;
            emit changed();
          });
  connect(client, &ApiClient::messagesReceived, this,
          [this](const QVariantList &rows, qint64 after, bool more) {
            if (m_messagesTask.isEmpty())
              return;
            if (after == 0)
              m_messages.clear();
            m_messages += rows;
            m_messagesCursor = after;
            m_messagesMore = more;
            armStatusPoll();
            emit changed();
          });
  connect(client, &ApiClient::messageStatusesReceived, this,
          [this](const QVariantList &rows) {
            for (const auto &value : rows) {
              const auto row = value.toMap();
              for (auto &existing : m_messages) {
                if (existing.toMap().value("id").toString() ==
                    row.value("id").toString())
                  existing = row;
              }
            }
            armStatusPoll();
            emit changed();
          });
  connect(client, &ApiClient::deliveryReceived, this,
          [this](const QVariantMap &projection) {
            if (!m_deliveryTask.isEmpty())
              m_delivery = projection;
            emit changed();
          });
  connect(
      client, &ApiClient::questionsReceived, this,
      [this](const QVariantList &rows, qint64 after, qint64 cursor, bool more) {
        if (m_task.isEmpty() || (after != 0 && after != m_questionsCursor))
          return;
        updateQuestions(rows, true);
        if (cursor >= m_questionsCursor) {
          m_questionsCursor = cursor;
          m_questionsMore = more;
        }
        emit changed();
      });
  connect(client, &ApiClient::questionStatusesReceived, this,
          [this](const QVariantList &rows) {
            updateQuestions(rows, false);
            emit changed();
          });
  connect(client, &ApiClient::answerAccepted, this,
          [this](const QVariantMap &answer) {
            const auto question = answer.value("question_id").toString();
            const auto key = answerKey(question);
            const auto sent = m_sentAnswers.value(key);
            for (int i = 0; i < m_questions.rowCount(); ++i) {
              auto row = m_questions.get(i);
              if (row.value("id").toString() != question)
                continue;
              // The ACK proves storage, not a delivery state or a server time.
              if (row.value("status").toString() != "answered") {
                row.insert("status", QString("answered"));
                row.insert("answer_message_id", answer.value("message_id"));
                row.insert("answer_text", sent);
                m_questions.update(i, questionRow(row));
              }
              break;
            }
            if (sent == m_answers.value(key))
              setAnswerText(question, {});
            m_sentAnswers.remove(key);
            emit changed();
          });
  connect(client, &ApiClient::commissionAccepted, this,
          [this](const QVariantMap &message) {
            // One explicit send records exactly one commission; clear the
            // commission text only when it still holds the sent text, so a
            // retry confirmation never discards newer typing.
            if (m_commission == message.value("text").toString())
              m_commission.clear();
            m_messagesCursor = 0;
            m_messages.clear();
            m_messagesMore = true;
            loadCommissionPage();
            emit changed();
          });
  m_statusPoll.setSingleShot(true);
  m_statusPoll.setInterval(2000);
  connect(&m_statusPoll, &QTimer::timeout, this, [this] {
    if (m_task.isEmpty() || m_messages.isEmpty())
      return;
    QStringList ids;
    for (const auto &value : m_messages)
      ids.append(value.toMap().value("id").toString());
    // Rotate a bounded window so long histories keep refreshing everywhere.
    if (ids.size() > 50) {
      m_statusOffset = (m_statusOffset + 50) % ids.size();
      const auto count = std::min<qsizetype>(50, ids.size() - m_statusOffset);
      ids = ids.sliced(m_statusOffset, count);
    }
    m_client->refreshMessageStatuses(m_task, ids);
    // An offline tick arms no reply; rearm so reconnect resumes polling.
    if (!m_client->messagesLoading())
      armStatusPoll();
  });
  m_deliveryPoll.setSingleShot(true);
  m_deliveryPoll.setInterval(3000);
  connect(&m_deliveryPoll, &QTimer::timeout, this, [this] {
    if (m_task.isEmpty())
      return;
    m_deliveryTask = m_task;
    m_client->loadDelivery(m_task);
    pollQuestions();
    m_deliveryPoll.start(); // Bounded: only while this task stays selected.
  });
}
void Panel::setCommissionText(const QString &text) {
  if (m_commission == text)
    return;
  m_commission = text;
  emit changed();
}
QString Panel::answerKey(const QString &question) const {
  return m_client->instance() + "/" + m_task + "/" + question;
}
QString Panel::answerText(const QString &question) const {
  return m_answers.value(answerKey(question)).toString();
}
void Panel::setAnswerText(const QString &question, const QString &text) {
  const auto key = answerKey(question);
  if (m_answers.value(key).toString() == text)
    return;
  const bool hadDrafts = hasAnswerDrafts();
  if (text.isEmpty())
    m_answers.remove(key);
  else
    m_answers.insert(key, text);
  if (hadDrafts != hasAnswerDrafts())
    emit answerDraftsChanged();
}
QVariantList Panel::detachedAnswerDrafts() const {
  QVariantList drafts;
  const auto prefix = m_client->instance() + "/";
  for (auto it = m_answers.cbegin(); it != m_answers.cend(); ++it) {
    if (!it.key().startsWith(prefix))
      drafts.append(QVariantMap{{"key", it.key()},
                                {"instance", it.key().section('/', 0, 0)},
                                {"task", it.key().section('/', 1, 1)},
                                {"question", it.key().section('/', 2, 2)},
                                {"text", it.value()}});
  }
  return drafts;
}
void Panel::discardAnswerDraft(const QString &key) {
  if (m_answers.remove(key) && m_answers.isEmpty())
    emit answerDraftsChanged();
  m_sentAnswers.remove(key);
}
void Panel::discardDetachedAnswerDraft(const QString &key) {
  if (busy() || uncertain() || key.startsWith(m_client->instance() + "/") ||
      !m_answers.contains(key))
    return;
  discardAnswerDraft(key);
  emit detachedAnswerDraftsChanged();
}
void Panel::discardTerminalAnswerDraft(const QString &question) {
  const auto row = find(&m_questions, question);
  const auto status = row.value("status").toString();
  if (busy() || uncertain() || (status != "closed" && status != "answered"))
    return;
  discardAnswerDraft(answerKey(question));
  emit changed();
}
void Panel::armStatusPoll() {
  if (m_task.isEmpty())
    return;
  for (const auto &value : m_messages) {
    if (value.toMap().value("status").toString() != "observed") {
      if (!m_statusPoll.isActive())
        m_statusPoll.start();
      return;
    }
  }
  m_statusPoll.stop();
}
QVariantMap Panel::messageRow(const QVariantMap &row) const {
  auto result = row;
  const auto timestamp =
      QDateTime::fromString(row.value("created_at").toString(), Qt::ISODate);
  result.insert(
      "displayTimestamp",
      timestamp.isValid()
          ? QLocale().toString(timestamp.toLocalTime(), QLocale::ShortFormat)
          : row.value("created_at").toString());
  const auto status = row.value("status").toString();
  result.insert("statusLabel", status == "observed"
                                   ? tr("Pi started preparing the turn")
                               : status == "unknown" ? tr("Outcome unknown")
                                                     : tr("Stored"));
  const auto blockReason = row.value("block_reason").toString();
  result.insert("blockReason", blockReason == "task_closed"
                                   ? tr("The task is closed")
                               : blockReason == "project_archived"
                                   ? tr("The project is archived")
                                   : QString());
  return result;
}
QVariantList Panel::messages() const {
  QVariantList result;
  for (const auto &value : m_messages)
    result.append(messageRow(value.toMap()));
  return result;
}
QVariantList Panel::questions() const {
  QVariantList result;
  for (int i = 0; i < m_questions.rowCount(); ++i)
    result.append(m_questions.get(i));
  return result;
}
QVariantMap Panel::questionRow(QVariantMap row) const {
  const auto binding = m_delivery.value("binding").toMap();
  const auto selected = task();
  const auto state = selected.value("state").toString();
  const bool openTask = !selected.isEmpty() && executable(selected) &&
                        !project().value("archived").toBool() &&
                        state != "done" && state != "cancelled";
  const bool openQuestion = row.value("status").toString() == "open";
  row.insert("answerDraft",
             openQuestion ? QString() : answerText(row.value("id").toString()));
  const auto answerStatus = row.value("answer_status").toString();
  row.insert("answerStatusLabel",
             answerStatus == "observed"
                 ? tr("Pi started preparing the answer turn")
             : answerStatus == "unknown" ? tr("Answer outcome unknown")
             : row.value("status").toString() == "answered" ? tr("Answer saved")
                                                            : QString());
  const auto blockReason = row.value("answer_block_reason").toString();
  row.insert("answerBlockReason",
             blockReason == "task_closed"        ? tr("The task is closed")
             : blockReason == "project_archived" ? tr("The project is archived")
             : blockReason == "delivery_revoked"
                 ? tr("The Pi assignment is not active")
                 : QString());
  row.insert("canAnswer", openQuestion && openTask &&
                              binding.value("allow_owner_questions").toBool() &&
                              binding.value("status").toString() == "active" &&
                              binding.value("delivery_binding_id").toString() ==
                                  row.value("delivery_binding_id").toString() &&
                              binding.value("project_id").toString() ==
                                  selected.value("project_id").toString() &&
                              writable());
  return row;
}
void Panel::updateQuestions(const QVariantList &rows, bool append) {
  for (const auto &value : rows) {
    const auto row = value.toMap();
    const auto id = row.value("id");
    int index = 0;
    for (; index < m_questions.rowCount(); ++index) {
      const auto current = m_questions.get(index);
      if (current.value("id") == id) {
        m_questions.update(index, questionRow(mergeQuestion(current, row)));
        break;
      }
    }
    if (append && index == m_questions.rowCount())
      m_questions.append(questionRow(row));
  }
}
void Panel::pollQuestions() {
  if (!m_client->online())
    return;
  // History belongs to the task, even with a revoked or replacement grant.
  if (!questionsLoading() && !m_questionsMore)
    m_client->loadQuestions(m_task, m_questionsCursor);
  const qsizetype size = m_questions.rowCount();
  if (size == 0 || m_client->questionStatusesLoading())
    return;
  const auto count = std::min<qsizetype>(5, size - m_questionStatusOffset);
  QStringList ids;
  for (qsizetype i = 0; i < count; ++i)
    ids.append(
        m_questions.get(m_questionStatusOffset + i).value("id").toString());
  m_questionStatusOffset = (m_questionStatusOffset + count) % size;
  m_client->refreshQuestionStatuses(m_task, ids);
}
void Panel::resetQuestions() {
  m_client->cancelQuestions();
  m_questions.replace({});
  m_questionsCursor = 0;
  m_questionsMore = false;
  m_questionStatusOffset = 0;
}
void Panel::resetResults(bool preserveCurrent) {
  m_client->cancelResults();
  m_resultEntries.clear();
  m_currentResultData.clear();
  m_currentReviewsCursor.clear();
  m_currentResultLoaded = false;
  m_resultsCursor = 0;
  m_resultsMore = false;
  m_resultsLoaded = false;
  if (!preserveCurrent)
    m_currentResult.clear();
}
void Panel::loadResults() {
  if (!m_task.isEmpty() && executable(task()) && m_client->online()) {
    m_client->loadResults(m_task, 0);
    m_client->loadCurrentResult(m_task);
  }
}
QString Panel::resultsError() const {
  return m_task.isEmpty() ? QString{} : m_client->currentResultError();
}
QString Panel::resultHistoryError() const {
  return m_task.isEmpty() ? QString{} : m_client->resultsError();
}
QVariant Panel::currentResult() const {
  return m_currentResultLoaded ? QVariant(m_currentResultData) : QVariantMap{};
}
QVariant Panel::resultHistory() const {
  QVariantList rows;
  for (const auto &value : m_resultEntries) {
    const auto entry = value.toMap();
    if (entry.value("kind").toString() == "result") {
      rows.append(QVariantMap{{"seq", entry.value("seq")},
                              {"result", entry.value("result")},
                              {"reviews", QVariantList{}}});
      continue;
    }
    const auto review = entry.value("review").toMap();
    for (auto &row : rows) {
      auto candidate = row.toMap();
      if (candidate.value("result").toMap().value("id").toString() !=
          review.value("result_id").toString())
        continue;
      auto reviews = candidate.value("reviews").toList();
      reviews.append(review);
      candidate.insert("reviews", reviews);
      row = candidate;
      break;
    }
  }
  return rows;
}
QVariantMap Panel::assignment() const {
  QVariantMap result{{"enabled", false},
                     {"available", false},
                     {"canSend", false},
                     {"reason", QString()}};
  const auto task = this->task();
  if (task.isEmpty() || !executable(task))
    return result;
  result.insert("taskId", m_task);
  const auto state = task.value("state").toString();
  const auto terminal = QStringList{"done", "cancelled"}.contains(state);
  const auto archived = project().value("archived").toBool();
  const auto binding = m_delivery.value("binding").toMap();
  if (binding.isEmpty()) {
    result.insert("reason",
                  tr("Delivery to Pi is not enabled for this installation"));
    return result;
  }
  result.insert("enabled", true);
  result.insert("bindingId", binding.value("delivery_binding_id").toString());
  result.insert("piSession", binding.value("pi_session_id").toString());
  result.insert("status", binding.value("status").toString());
  result.insert("ready", m_delivery.value("ready").toBool());
  QString reason;
  if (archived)
    reason = tr("The project is archived");
  else if (terminal)
    reason = tr("The task is closed");
  else if (binding.value("status").toString() != "active")
    reason = tr("The Pi assignment is not active");
  else if (binding.value("project_id").toString() !=
           task.value("project_id").toString())
    reason = tr("The enabled assignment belongs to a different project");
  else if (!m_delivery.value("ready").toBool())
    reason = tr("The Pi writer is not ready right now");
  result.insert("reason", reason);
  result.insert("available", reason.isEmpty());
  result.insert("canSend", reason.isEmpty() && writable() &&
                               !m_commission.trimmed().isEmpty());
  return result;
}
void Panel::resetCommissions() {
  m_statusPoll.stop();
  m_deliveryPoll.stop();
  m_messagesTask.clear();
  m_deliveryTask.clear();
  m_messages.clear();
  m_messagesCursor = 0;
  m_messagesMore = false;
  m_delivery = {};
  m_client->cancelMessages();
}

// resetCommissionView drops the typed commission text as well; use it only
// when the recipient context actually changed (task/project switch), never
// for snapshot refreshes of the same selection.
void Panel::resetCommissionView() {
  resetCommissions();
  resetQuestions();
  m_commission.clear();
  // Answer drafts stay keyed to their instance/task/question across navigation.
  m_sentAnswers.clear();
}
void Panel::loadCommissionPage() {
  if (m_task.isEmpty() || !executable(task()) || !m_client->online())
    return;
  m_messagesTask = m_task;
  m_deliveryTask = m_task;
  m_client->loadMessages(m_task, 0);
  m_client->loadDelivery(m_task);
  pollQuestions();
  m_deliveryPoll.start();
}
bool Panel::sendCommission() {
  const auto target = assignment();
  if (!target.value("canSend").toBool())
    return false;
  return m_client->sendMessage(m_task, m_commission,
                               target.value("bindingId").toString());
}
bool Panel::answerQuestion(const QString &question) {
  for (const auto &value : questions()) {
    const auto row = value.toMap();
    if (row.value("id").toString() == question &&
        questionRow(row).value("canAnswer").toBool()) {
      const auto answer = answerText(question);
      if (!m_client->answerQuestion(m_task, question, answer))
        return false;
      m_sentAnswers.insert(answerKey(question), answer);
      return true;
    }
  }
  return false;
}
void Panel::moreQuestions() {
  if (!m_task.isEmpty() && m_questionsMore && !questionsLoading())
    m_client->loadQuestions(m_task, m_questionsCursor);
}
void Panel::moreMessages() {
  if (!m_task.isEmpty() && m_messagesMore && !messagesLoading())
    m_client->loadMessages(m_task, m_messagesCursor);
}
QVariantList Panel::history() const {
  const QMap<QString, QString> labels{
      {"project.create", tr("Project created")},
      {"project.edit", tr("Project edited")},
      {"task.create", tr("Task created")},
      {"task.edit", tr("Task edited")},
      {"task.transition", tr("Manual state changed")},
      {"queue.replace", tr("Queue changed")}};
  QVariantList result;
  for (const auto &value : m_history) {
    auto row = value.toMap();
    const auto timestamp =
        QDateTime::fromString(row.value("timestamp").toString(), Qt::ISODate);
    row.insert("displayTimestamp",
               timestamp.isValid() ? QLocale().toString(timestamp.toLocalTime(),
                                                        QLocale::ShortFormat)
                                   : row.value("timestamp").toString());
    row.insert("label", labels.value(row.value("action").toString(),
                                     tr("Manual change")));
    row.insert("details",
               tr("Before:\n%1\nAfter:\n%2")
                   .arg(QString::fromUtf8(
                            QJsonDocument::fromVariant(row.value("before"))
                                .toJson(QJsonDocument::Indented)),
                        QString::fromUtf8(
                            QJsonDocument::fromVariant(row.value("after"))
                                .toJson(QJsonDocument::Indented))));
    result.append(row);
  }
  return result;
}
QVariantList Panel::projectSnapshot() const {
  QVariantList rows;
  for (int i = 0; i < m_client->projects()->rowCount(); ++i)
    rows.append(m_client->projects()->get(i));
  return rows;
}
QVariantList Panel::projects() const {
  QVariantList rows;
  for (int i = 0; i < m_client->projects()->rowCount(); ++i) {
    auto row = m_client->projects()->get(i);
    if (!m_archived && row.value("archived").toBool())
      continue;
    QMap<QString, int> counts;
    for (int j = 0; j < m_client->tasks()->rowCount(); ++j) {
      const auto task = m_client->tasks()->get(j);
      if (task.value("project_id") == row.value("id") &&
          task.value("kind", "task").toString() != "epic")
        ++counts[task.value("state").toString()];
    }
    row.insert("counts", tr("Backlog %1 · Queued %2 · Current %3 · Done %4")
                             .arg(counts["backlog"])
                             .arg(counts["queued"])
                             .arg(counts["in_progress"])
                             .arg(counts["done"]));
    rows.append(row);
  }
  return rows;
}
QVariantMap Panel::project() const {
  auto row = find(m_client->projects(), m_project);
  if (!row.isEmpty())
    row.insert("pathAvailable",
               QFileInfo(row.value("root_path").toString()).isDir());
  return row;
}
QVariantMap Panel::task() const {
  return workRow(find(m_client->tasks(), m_task));
}
bool Panel::workTypesUpgradeRequired() const {
  return m_client->lastUpdated().isValid() && !workTypesAvailable();
}
QString Panel::kindLabel(const QString &kind) const {
  if (kind == "epic")
    return tr("Epic");
  if (kind == "bug")
    return tr("Bug");
  return tr("Task");
}
Panel::EpicTitleCounts Panel::epicTitleCounts() const {
  EpicTitleCounts counts;
  for (int i = 0; i < m_client->tasks()->rowCount(); ++i) {
    const auto row = m_client->tasks()->get(i);
    if (row.value("kind").toString() == "epic")
      ++counts[row.value("project_id").toString()]
              [row.value("title").toString()];
  }
  return counts;
}
QString Panel::parentTitle(const QVariantMap &parent,
                           const EpicTitleCounts &titleCounts) const {
  if (parent.isEmpty())
    return {};
  const auto title = parent.value("title").toString();
  const int matchingTitles =
      titleCounts.value(parent.value("project_id").toString()).value(title);
  // The UUID comes first because compact combo delegates elide on the right.
  return matchingTitles > 1 ? QStringLiteral("(%1) %2").arg(
                                  parent.value("id").toString(), title)
                            : title;
}
QString Panel::parentTitle(const QVariantMap &parent) const {
  return parentTitle(parent, epicTitleCounts());
}
QString Panel::parentOptionTitle(const QVariantMap &parent) const {
  if (parent.isEmpty())
    return {};
  // Combo delegates elide on the right, so every option starts with its UUID.
  return QStringLiteral("(%1) %2").arg(parent.value("id").toString(),
                                       parent.value("title").toString());
}
QVariantMap Panel::workRow(QVariantMap row, const QVariantMap *knownParent,
                           const EpicTitleCounts *titleCounts) const {
  if (row.isEmpty())
    return row;
  EpicTitleCounts computedTitleCounts;
  if (!titleCounts) {
    computedTitleCounts = epicTitleCounts();
    titleCounts = &computedTitleCounts;
  }
  const auto kind = row.value("kind", "task").toString();
  row.insert("kind", kind);
  row.insert("kindLabel", kindLabel(kind));
  row.insert("stateLabel", stateLabel(row.value("state").toString()));
  const auto parent =
      knownParent ? *knownParent
                  : find(m_client->tasks(), row.value("parent_id").toString());
  row.insert("parentTitle", parentTitle(parent, *titleCounts));
  row.insert("parentState", parent.value("state"));
  const auto summary = row.value("summary").toMap();
  if (kind == "epic")
    row.insert("summaryText",
               tr("Backlog %1 · Queued %2 · In progress %3 · Done %4 · "
                  "Cancelled %5")
                   .arg(summary.value("backlog").toInt())
                   .arg(summary.value("queued").toInt())
                   .arg(summary.value("in_progress").toInt())
                   .arg(summary.value("done").toInt())
                   .arg(summary.value("cancelled").toInt()));
  return row;
}
bool Panel::executable(const QVariantMap &row) const {
  const auto kind = row.value("kind", "task").toString();
  return kind == "task" || kind == "bug";
}
QVariantList Panel::creationKinds() const {
  QVariantList rows{QVariantMap{{"id", "task"}, {"label", kindLabel("task")}}};
  if (workTypesAvailable()) {
    rows.append(QVariantMap{{"id", "epic"}, {"label", kindLabel("epic")}});
    rows.append(QVariantMap{{"id", "bug"}, {"label", kindLabel("bug")}});
  }
  return rows;
}
QVariantList Panel::parentCandidates() const {
  QVariantList rows;
  if (!workTypesAvailable())
    return rows;
  for (int i = 0; i < m_client->tasks()->rowCount(); ++i) {
    const auto row = m_client->tasks()->get(i);
    const auto state = row.value("state").toString();
    if (row.value("project_id").toString() == m_editorProject &&
        row.value("kind").toString() == "epic" &&
        (state == "open" || state == "in_progress"))
      rows.append(QVariantMap{{"id", row.value("id")},
                              {"title", parentOptionTitle(row)},
                              {"state", state}});
  }
  return rows;
}
QVariantList Panel::parentOptions() const {
  QVariantList rows{QVariantMap{{"id", ""}, {"title", tr("No Epic parent")}}};
  const auto candidates = parentCandidates();
  if (m_kind == "task" && workTypesAvailable() &&
      editor().value("kind").toString() != "epic") {
    const auto isCandidate = [&](const QString &id) {
      return std::any_of(candidates.begin(), candidates.end(),
                         [&](const QVariant &value) {
                           return value.toMap().value("id").toString() == id;
                         });
    };
    const auto original = !m_editorId.isEmpty()
                              ? m_editorBase.value("parent_id").toString()
                              : QString();
    if (!original.isEmpty() && !isCandidate(original)) {
      const auto parent = find(m_client->tasks(), original);
      auto title = parentOptionTitle(parent);
      if (title.isEmpty())
        title = original;
      rows.append(QVariantMap{
          {"id", original},
          {"title", tr("Current Epic parent (unchanged): %1").arg(title)},
          {"state", parent.value("state")}});
    }
    const auto selected = editor().value("parent_id").toString();
    if (!selected.isEmpty() && selected != original && !isCandidate(selected)) {
      const auto parent = find(m_client->tasks(), selected);
      auto title = parentOptionTitle(parent);
      if (title.isEmpty())
        title = selected;
      rows.append(
          QVariantMap{{"id", selected},
                      {"title", tr("Unavailable Epic parent: %1").arg(title)},
                      {"state", parent.value("state")}});
    }
  }
  return rows + candidates;
}
QString Panel::editorParentTitle() const {
  if (m_kind != "task")
    return {};
  const auto parent =
      find(m_client->tasks(), editor().value("parent_id").toString());
  return parentTitle(parent);
}
QString Panel::editorParentState() const {
  if (m_kind != "task")
    return {};
  const auto parent =
      find(m_client->tasks(), editor().value("parent_id").toString());
  return parent.value("state").toString();
}
QVariantList Panel::epicChildren() const {
  QVariantList rows;
  const auto titleCounts = epicTitleCounts();
  const auto selected =
      workRow(find(m_client->tasks(), m_task), nullptr, &titleCounts);
  if (selected.value("kind").toString() != "epic")
    return rows;
  for (int i = 0; i < m_client->tasks()->rowCount(); ++i) {
    auto row = m_client->tasks()->get(i);
    if (row.value("parent_id").toString() == m_task)
      rows.append(workRow(row, &selected, &titleCounts));
  }
  return rows;
}
QVariantList Panel::queueIds() const {
  for (int i = 0; i < m_client->queues()->rowCount(); ++i) {
    const auto row = m_client->queues()->get(i);
    if (row.value("project_id").toString() == m_project)
      return row.value("task_ids").toList();
  }
  return {};
}
QVariantList Panel::tasks() const {
  QVariantList rows;
  const auto ids = queueIds();
  const auto titleCounts = epicTitleCounts();
  QHash<QString, QVariantMap> parents;
  parents.reserve(m_client->tasks()->rowCount());
  for (int i = 0; i < m_client->tasks()->rowCount(); ++i) {
    const auto row = m_client->tasks()->get(i);
    parents.insert(row.value("id").toString(), row);
  }
  for (int i = 0; i < m_client->tasks()->rowCount(); ++i) {
    auto row = m_client->tasks()->get(i);
    if (row.value("project_id").toString() != m_project)
      continue;
    const auto kind = row.value("kind", "task").toString();
    row.insert("kind", kind);
    row.insert("kindLabel", kindLabel(kind));
    const auto parentID = row.value("parent_id").toString();
    const auto parent =
        parentID.isEmpty() ? QVariantMap{} : parents.value(parentID);
    row.insert("parentTitle", parentTitle(parent, titleCounts));
    row.insert("parentState", parent.value("state"));
    const int position = ids.indexOf(row.value("id"));
    row.insert("position", position);
    row.insert("canDown", position >= 0 && position + 1 < ids.size());
    row.insert("stateLabel", stateLabel(row.value("state").toString()));
    rows.append(row);
  }
  const QStringList states{"open", "backlog",   "queued", "in_progress",
                           "done", "cancelled", "closed"};
  std::stable_sort(
      rows.begin(), rows.end(), [&](const QVariant &a, const QVariant &b) {
        const auto x = a.toMap(), y = b.toMap();
        if (x.value("state") == y.value("state"))
          return x.value("position").toInt() < y.value("position").toInt();
        return states.indexOf(x.value("state").toString()) <
               states.indexOf(y.value("state").toString());
      });
  QString previousState;
  for (auto &value : rows) {
    auto row = value.toMap();
    const auto state = row.value("state").toString();
    row.insert("sectionStart", state != previousState);
    previousState = state;
    value = row;
  }
  return rows;
}
QString Panel::stateLabel(const QString &state) const {
  if (state == "backlog")
    return tr("Backlog");
  if (state == "queued")
    return tr("Queue");
  if (state == "in_progress")
    return tr("In progress (manual)");
  if (state == "done")
    return tr("Completed manually");
  if (state == "cancelled")
    return tr("Cancelled");
  if (state == "open")
    return tr("Open");
  if (state == "closed")
    return tr("Closed");
  return {};
}
QString Panel::currentText() const {
  if (m_kind == "project") {
    const auto row = project();
    return tr("Name: %1\nDirectory: %2\nDescription: %3")
        .arg(row.value("name").toString(), row.value("root_path").toString(),
             row.value("description").toString());
  }
  if (m_editorId.isEmpty()) {
    auto text = tr("No stored work item (create draft).");
    const auto parent =
        find(m_client->tasks(), editor().value("parent_id").toString());
    if (!parent.isEmpty())
      text += tr("\nSelected Epic parent: %1 (%2)")
                  .arg(parentTitle(parent),
                       stateLabel(parent.value("state").toString()));
    return text;
  }
  const auto row = workRow(find(m_client->tasks(), m_editorId));
  auto text =
      tr("Type: %1\nTitle: %2\nDescription: %3\nAcceptance criteria: %4")
          .arg(row.value("kindLabel").toString(), row.value("title").toString(),
               row.value("description").toString(),
               row.value("acceptance_criteria").toString());
  if (row.value("kind").toString() != "epic")
    text += tr("\nEpic parent: %1")
                .arg(row.value("parentTitle").toString().isEmpty()
                         ? tr("None")
                         : row.value("parentTitle").toString());
  if (row.value("kind").toString() == "bug")
    text += tr("\nExpected behavior: %1\nActual behavior: %2\nFix evidence: %3")
                .arg(row.value("expected_behavior").toString(),
                     row.value("actual_behavior").toString(),
                     row.value("fix_evidence").toString());
  return text;
}
QString Panel::message() const {
  if (canAbandon())
    return tr("Database changed; the old save outcome is unknown. Acknowledge "
              "abandonment before continuing; the draft will be kept.");
  if (dirty() && m_editorInstance != m_client->instance())
    return tr("Database changed; copy or discard the draft before continuing");
  if (unsupportedWorkTypeDraft())
    return tr("Daemon upgrade required for Epic, Bug and Epic parent actions. "
              "Existing tasks remain available.");
  if (invalidParentAttachment())
    return tr("Selected Epic parent is unavailable; choose an eligible Epic "
              "or clear the parent");
  return m_client->error();
}
QString Panel::connection() const {
  if (m_client->loading())
    return tr("Loading");
  return m_client->online() ? tr("Online")
                            : tr("Offline — cached data is read-only");
}
QString Panel::updated() const {
  return m_client->lastUpdated().isValid()
             ? QLocale().toString(m_client->lastUpdated().toLocalTime(),
                                  QLocale::ShortFormat)
             : tr("Never");
}
bool Panel::writable() const {
  return m_client->online() && !m_client->pending() && !m_client->uncertain();
}
bool Panel::unsupportedWorkTypeDraft() const {
  if (m_kind != "task" || workTypesAvailable())
    return false;
  const auto draft = editor();
  return draft.value("kind", "task").toString() != "task" ||
         !draft.value("parent_id").toString().isEmpty();
}
bool Panel::invalidParentAttachment() const {
  if (m_kind != "task" || !workTypesAvailable() ||
      editor().value("kind").toString() == "epic")
    return false;
  const auto selected = editor().value("parent_id").toString();
  if (selected.isEmpty() ||
      (!m_editorId.isEmpty() &&
       selected == m_editorBase.value("parent_id").toString()))
    return false;
  for (int i = 0; i < m_client->tasks()->rowCount(); ++i) {
    const auto row = m_client->tasks()->get(i);
    const auto state = row.value("state").toString();
    if (row.value("id").toString() == selected &&
        row.value("project_id").toString() == m_editorProject &&
        row.value("kind").toString() == "epic" &&
        (state == "open" || state == "in_progress"))
      return false;
  }
  return true;
}
bool Panel::saveable() const {
  const bool newProject = m_kind == "project" && m_editorId.isEmpty();
  return writable() && !conflict() && !m_kind.isEmpty() &&
         !unsupportedWorkTypeDraft() && !invalidParentAttachment() &&
         m_editorInstance == m_client->instance() &&
         (newProject || !project().value("archived").toBool());
}
void Panel::setShowArchived(bool value) {
  m_archived = value;
  emit changed();
}
void Panel::setProjectSelection(const QString &id) {
  // Keep dependent selection/history consistent for both owner selection and
  // confirmed project creation, without touching an in-flight editor's draft.
  m_project = id;
  m_task.clear();
  m_client->cancelHistory();
  m_history.clear();
  m_historyCursor = 0;
  m_historyMore = true;
  resetCommissionView();
  resetResults();
}
bool Panel::selectProject(const QString &id) {
  if (dirty() || m_client->pending() || uncertain())
    return false;
  setProjectSelection(id);
  emit changed();
  return true;
}
void Panel::setTaskSelection(const QString &id) {
  const bool sameTask = m_task == id;
  m_task = id;
  m_client->cancelHistory();
  m_history.clear();
  m_historyCursor = 0;
  m_historyMore = true;
  // Same-task reselection and snapshot refreshes keep the typed commission;
  // only a real recipient change (another task or project) clears it.
  if (sameTask)
    resetCommissions();
  else
    resetCommissionView();
  resetResults();
}
bool Panel::selectTask(const QString &id) {
  if (dirty() || m_client->pending() || uncertain())
    return false;
  const auto row = find(m_client->tasks(), id);
  if (row.value("project_id").toString() != m_project)
    return false;
  setTaskSelection(id);
  moreHistory();
  loadCommissionPage();
  loadResults();
  emit changed();
  return true;
}
bool Panel::begin(const QString &kind, const QVariantMap &value) {
  if (!writable() || dirty())
    return false;
  m_kind = kind;
  m_version = value.value("version").toLongLong();
  m_editorId = value.value("id").toString();
  m_editorProject = m_project;
  m_editorInstance = m_client->instance();
  m_adopted = false;
  const auto fields = editableFields(kind, value);
  m_editorBase = fields;
  m_sentEditorBase.clear();
  m_client->setDraft(fields);
  return true;
}
bool Panel::editProject(bool create) {
  return (create ||
          (!project().isEmpty() && !project().value("archived").toBool())) &&
         begin("project", create ? QVariantMap{} : project());
}
bool Panel::editTask(bool create, const QString &kind) {
  const auto selectedKind =
      create ? kind : task().value("kind", "task").toString();
  if (!QStringList{"epic", "task", "bug"}.contains(selectedKind) ||
      (create && !workTypesAvailable() && selectedKind != "task"))
    return false;
  QVariantMap value;
  if (create)
    value.insert("kind", selectedKind);
  else
    value = task();
  return !project().isEmpty() && !project().value("archived").toBool() &&
         (create || !task().isEmpty()) && begin("task", value);
}
void Panel::field(const QString &name, const QString &value) {
  auto draft = editor();
  if (!draft.contains(name))
    return;
  if (name == "kind") {
    if (draft.value("kind").toString() == value)
      return;
    if (!m_editorId.isEmpty() || m_client->pending() || m_client->uncertain() ||
        !workTypesAvailable() ||
        !QStringList{"epic", "task", "bug"}.contains(value))
      return;
    draft[name] = value;
    // Hidden fields stay in the local draft so a type round trip is lossless;
    // save() independently filters fields inapplicable to the selected kind.
    if (value != "epic" && !draft.contains("parent_id"))
      draft.insert("parent_id", QString());
    if (value == "bug")
      for (const auto *key :
           {"expected_behavior", "actual_behavior", "fix_evidence"})
        if (!draft.contains(key))
          draft.insert(key, QString());
  } else if (name == "parent_id") {
    if (!workTypesAvailable() || draft.value("kind").toString() == "epic")
      return;
    bool valid = value.isEmpty();
    if (!m_editorId.isEmpty() &&
        value == m_editorBase.value("parent_id").toString())
      valid = true;
    for (const auto &candidate : parentCandidates())
      valid = valid || candidate.toMap().value("id").toString() == value;
    if (!valid)
      return;
    draft[name] = value;
  } else {
    draft[name] = value;
  }
  m_client->setDraft(draft);
}
bool Panel::save() {
  if (!saveable())
    return false;
  const auto sentEditorBase = editor();
  auto payload = sentEditorBase;
  if (m_kind == "task") {
    const auto kind = payload.value("kind", "task").toString();
    if (!m_editorId.isEmpty())
      payload.remove("kind");
    if (!workTypesAvailable() || kind == "epic") {
      payload.remove("parent_id");
      payload.remove("expected_behavior");
      payload.remove("actual_behavior");
      payload.remove("fix_evidence");
    } else if (kind != "bug") {
      payload.remove("expected_behavior");
      payload.remove("actual_behavior");
      payload.remove("fix_evidence");
    }
    if (!workTypesAvailable())
      payload.remove("kind");
  }
  if (!m_editorId.isEmpty()) {
    // PATCH is a delta from the editor's confirmed base. In particular, a
    // title-only Bug edit must not resend several individually valid 64 KiB
    // fields and exceed the whole-request limit.
    for (auto it = payload.begin(); it != payload.end();) {
      if (m_editorBase.contains(it.key()) &&
          m_editorBase.value(it.key()) == it.value())
        it = payload.erase(it);
      else
        ++it;
    }
    payload.insert("expected_version", m_version);
  }
  const auto path = m_kind == "project"
                        ? "/v1/projects"
                        : "/v1/projects/" + m_editorProject + "/tasks";
  const bool sent = m_client->save(
      m_editorId.isEmpty() ? "POST" : "PATCH",
      m_editorId.isEmpty()
          ? path
          : QStringLiteral("/v1/") +
                (m_kind == "project" ? "projects/" : "tasks/") + m_editorId,
      payload);
  if (sent) {
    m_adopted = false;
    m_sentEditorBase = sentEditorBase;
  }
  return sent;
}
void Panel::discard() { m_client->discardDraft(); }
void Panel::reload() {
  m_client->reload();
  resetResults(true);
  loadResults();
}
bool Panel::rebaseEligible() const {
  if (!writable() || m_client->loading() || !m_client->conflict() ||
      !m_conflictTracked || m_conflictInstance != m_client->instance() ||
      m_editorInstance != m_client->instance() ||
      m_conflictEditorId != m_editorId || m_conflictEditorKind != m_kind ||
      m_client->appliedSnapshotRequestSerial() <=
          m_conflictSnapshotRequestSerial ||
      (m_editorId.isEmpty() && m_kind != "task"))
    return false;
  if (m_editorId.isEmpty())
    return true;
  const auto row =
      find(m_kind == "project" ? m_client->projects() : m_client->tasks(),
           m_editorId);
  if (row.isEmpty())
    return false;
  const auto version = row.value("version").toLongLong();
  return version >= m_version && (m_kind != "project" || version != m_version);
}
bool Panel::rebase() {
  // Explicit owner action: retain text but adopt the freshly loaded editable
  // base. A parent-state conflict need not advance (or create) the work row.
  if (!rebaseEligible())
    return false;
  if (!m_editorId.isEmpty()) {
    const auto row =
        find(m_kind == "project" ? m_client->projects() : m_client->tasks(),
             m_editorId);
    m_version = row.value("version").toLongLong();
    m_editorBase = editableFields(m_kind, row);
  }
  m_adopted = true;
  emit changed();
  return true;
}
bool Panel::retry() { return m_client->retry(); }
bool Panel::canArchive() const {
  if (!writable() || dirty() || project().isEmpty())
    return false;
  if (project().value("archived").toBool())
    return true;
  for (const auto &row : tasks()) {
    const auto task = row.toMap();
    const auto state = task.value("state").toString();
    if (executable(task) && (state == "queued" || state == "in_progress"))
      return false;
  }
  return true;
}
bool Panel::archive() {
  if (!canArchive())
    return false;
  return m_client->save("PATCH", "/v1/projects/" + m_project,
                        {{"expected_version", project().value("version")},
                         {"archived", !project().value("archived").toBool()}});
}
bool Panel::queue(const QString &id, int direction) {
  if (!writable() || dirty() || project().value("archived").toBool() ||
      direction < -1 || direction > 1)
    return false;
  const auto row = find(m_client->tasks(), id);
  if (row.value("project_id").toString() != m_project || !executable(row))
    return false;
  auto ids = queueIds();
  const int position = ids.indexOf(id);
  if (direction == 0) {
    if (position >= 0)
      ids.removeAt(position);
    else if (row.value("state") == "backlog")
      ids.append(id);
    else
      return false;
  } else {
    if (position < 0 || position + direction < 0 ||
        position + direction >= ids.size())
      return false;
    ids.swapItemsAt(position, position + direction);
  }
  return m_client->save(
      "PUT", "/v1/projects/" + m_project + "/queue",
      {{"expected_queue_version", project().value("queue_version")},
       {"task_ids", ids}});
}
bool Panel::queueStartEligible() const {
  const auto state = task().value("state").toString();
  if (!executable(task()))
    return false;
  const auto ids = queueIds();
  if (state != "queued" || ids.isEmpty() || ids.first().toString() != m_task)
    return false;
  for (const auto &row : tasks())
    if (executable(row.toMap()) &&
        row.toMap().value("state").toString() == "in_progress")
      return false;
  return true;
}
bool Panel::allowed(const QString &target) const {
  if (!writable() || dirty() || project().value("archived").toBool() ||
      task().isEmpty())
    return false;
  const auto selected = task();
  const auto state = selected.value("state").toString();
  if (selected.value("kind").toString() == "epic") {
    if (target == "in_progress")
      return state == "open";
    if (target == "open")
      return state == "in_progress" || state == "closed";
    if (target != "closed" || state != "in_progress")
      return false;
    const auto summary = selected.value("summary").toMap();
    return summary.value("backlog").toInt() == 0 &&
           summary.value("queued").toInt() == 0 &&
           summary.value("in_progress").toInt() == 0;
  }
  if (!m_resultsLoaded)
    return false;
  if (target == "in_progress" && !queueStartEligible())
    return false;
  if (target == "in_progress")
    return true;
  if (target == "done")
    return state == "in_progress";
  if (target == "cancelled")
    return QStringList{"backlog", "queued", "in_progress"}.contains(state);
  if (target == "backlog")
    return QStringList{"in_progress", "done", "cancelled"}.contains(state) &&
           selected.value("parentState").toString() != "closed";
  return false;
}
bool Panel::transition(const QString &state) {
  if (!allowed(state))
    return false;
  QVariantMap payload{{"state", state},
                      {"expected_version", task().value("version")}};
  if (executable(task()))
    payload.insert("expected_result_id", m_currentResult);
  if (task().value("state") == "queued")
    payload.insert("expected_queue_version", project().value("queue_version"));
  return m_client->save("POST", "/v1/tasks/" + m_task + "/transition", payload);
}
void Panel::moreHistory() {
  if (!m_task.isEmpty() && m_historyMore && !historyLoading())
    m_client->loadHistory(m_project, m_task, m_historyCursor);
}
void Panel::moreResults() {
  if (!m_task.isEmpty() && m_resultsMore && !resultHistoryLoading())
    m_client->loadResults(m_task, m_resultsCursor);
}
