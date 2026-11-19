// Isolated Pi 1.5.2 SDK delivery/crash matrix. No real providers, tools or sessions.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import { once } from 'node:events';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { pinnedPi } from './pi-package.mjs';

const projectRoot = fs.realpathSync(path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..'));
const parentRoot = process.env.PROBE_ROOT;
process.umask(0o077);
assert(parentRoot?.startsWith('/tmp/artel-pi-delivery-'), 'PROBE_ROOT must be an absolute private /tmp/artel-pi-delivery-* path');
assert(path.isAbsolute(parentRoot) && path.relative(projectRoot, parentRoot).startsWith('..'), 'PROBE_ROOT must be outside the worktree');
const rootStat = fs.lstatSync(parentRoot);
assert(rootStat.isDirectory() && rootStat.uid === process.getuid() && (rootStat.mode & 0o777) === 0o700);
const realRoot = fs.realpathSync(parentRoot);
assert(realRoot.startsWith('/tmp/artel-pi-delivery-') && !realRoot.startsWith(`${projectRoot}/`) && realRoot !== projectRoot);
for (const key of ['HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME', 'XDG_DATA_HOME', 'PI_CODING_AGENT_DIR']) {
  const value = process.env[key];
  assert(value?.startsWith('/'), `${key} must be an absolute private path`);
  const directory = fs.realpathSync(value);
  assert.equal(directory, value, `${key} must not use symlinks`);
  const stat = fs.statSync(directory);
  assert(stat.isDirectory() && stat.uid === process.getuid() && (stat.mode & 0o777) === 0o700);
  const relative = path.relative(realRoot, directory);
  assert(relative && !relative.startsWith('..') && !path.isAbsolute(relative), `${key} must be inside PROBE_ROOT`);
}
assert.equal(process.env.PI_OFFLINE, '1', 'PI_OFFLINE=1 is required');
const allowedEnvironment = new Set([
  'PATH', 'HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME', 'XDG_DATA_HOME',
  'PI_CODING_AGENT_DIR', 'PI_OFFLINE', 'PROBE_ROOT', 'PI_TEST_PACKAGE', 'LANG', 'LC_ALL', 'TMPDIR',
]);
for (const key of Object.keys(process.env)) {
  assert(allowedEnvironment.has(key), `unapproved inherited environment variable: ${key}`);
}
const root = fs.mkdtempSync(path.join(realRoot, 'sdk-'));
const env = { ...process.env, PROBE_ROOT: root };
// Never import Pi or spawn children before the isolation checks. Use a fresh
// tree even when the caller reuses an otherwise valid private parent directory.
for (const key of ['HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME', 'XDG_DATA_HOME', 'PI_CODING_AGENT_DIR', 'TMPDIR']) {
  const directory = path.join(root, key.toLowerCase());
  fs.mkdirSync(directory, { mode: 0o700 });
  process.env[key] = env[key] = directory;
}
process.env.PROBE_ROOT = root;
const sdk = await import(pinnedPi(process.env.PI_TEST_PACKAGE).sdk);
assert.equal(sdk.VERSION, '1.5.2');
for (const api of ['createAgentSession', 'DefaultResourceLoader', 'ModelRuntime', 'SessionManager', 'SettingsManager']) {
  assert.equal(typeof sdk[api], 'function', `missing public Pi API ${api}`);
}
const workerPath = fileURLToPath(new URL('./sdk-worker.mjs', import.meta.url));
const children = new Set();
const results = [];
const providers = new Set();
const oracleErrors = [];
const cancellation = { error: null };

function checkCancelled(scenario) {
  const error = cancellation.error ?? scenario?.cancellation.error ?? scenario?.watchdogError;
  if (error) throw error;
}

function diagnostics(errors, append = fs.appendFileSync) {
  const write = (file, data) => {
    try {
      append(file, data, { mode: 0o600 });
    } catch (cause) {
      errors.push(new Error(`diagnostic write failed (${path.basename(file)}): ${cause.message}`, { cause }));
    }
  };
  return {
    write,
    report(error) {
      errors.push(error);
      write(path.join(root, 'runner-errors.log'), `${error.stack ?? error}\n`);
    },
  };
}

const runnerDiagnostics = diagnostics(oracleErrors);
const entryError = runnerDiagnostics.report;

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

class OwnedChild {
  constructor(scenario, config, { executable = process.execPath, reportError = entryError, writeDiagnostic = runnerDiagnostics.write } = {}) {
    checkCancelled(scenario);
    this.scenario = scenario;
    this.config = config;
    this.messages = [];
    this.waiters = new Set();
    fs.writeFileSync(config.configPath, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
    this.child = spawn(executable, [workerPath, config.configPath], {
      cwd: config.scenarioRoot,
      env,
      stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
    });
    this.child.stdout.on('data', chunk => {
      writeDiagnostic(path.join(config.scenarioRoot, `${config.mode}-${this.child.pid}-stdout.log`), chunk);
    });
    this.child.stderr.on('data', chunk => {
      writeDiagnostic(path.join(config.scenarioRoot, `${config.mode}-${this.child.pid}-stderr.log`), chunk);
    });
    this.child.on('message', message => {
      this.messages.push(message);
      if (message.type === 'token-event') {
        if (scenario.tokens.has(message.token)) entryError(new Error('duplicate before_agent_start token'));
        scenario.tokens.set(message.token, message);
      }
      if (message.type === 'provider-intake') {
        try {
          scenario.provider.expectIntake(message.id);
          if (this.child.connected) this.send({ type: 'provider-intake-ready', id: message.id });
        } catch (error) {
          entryError(error);
        }
      }
      if (message.type === 'pi-send') scenario.piSends += 1;
      if (message.type === 'pi-transmit') scenario.transmitCommands += 1;
      if (['oracle-error', 'extension-error'].includes(message.type)) entryError(new Error(message.message));
      for (const waiter of [...this.waiters]) waiter.check();
    });
    this.exited = deferred();
    const terminal = (code, signal, error) => {
      if (this.exit) return;
      children.delete(this);
      this.exit = { code, signal, ...(error && { error }) };
      this.exited.resolve(this.exit);
      for (const waiter of [...this.waiters]) waiter.check();
    };
    this.child.once('close', (code, signal) => terminal(code, signal));
    this.child.once('error', error => {
      reportError(error);
      if (!this.child.pid) terminal(null, null, error);
    });
    children.add(this);
  }

  async wait(type, predicate = () => true, timeout = 6000) {
    const found = this.messages.find(message => message.type === type && predicate(message));
    if (found) return found;
    const waiter = deferred();
    const entry = {
      check: () => {
        const match = this.messages.find(message => message.type === type && predicate(message));
        if (match) {
          clearTimeout(timer);
          this.waiters.delete(entry);
          waiter.resolve(match);
        } else if (this.exit) {
          clearTimeout(timer);
          this.waiters.delete(entry);
          waiter.reject(new Error(`${this.scenario.name}: child exited before ${type}; private log ${this.config.scenarioRoot}`));
        }
      },
    };
    const timer = setTimeout(() => {
      this.waiters.delete(entry);
      waiter.reject(new Error(`${this.scenario.name}: barrier/event timeout ${type}; private log ${this.config.scenarioRoot}`));
    }, timeout);
    this.waiters.add(entry);
    entry.check();
    return waiter.promise;
  }

  send(message) {
    assert(this.child.connected && !this.exit, `cannot send ${message.type} to exited ${this.scenario.name} child`);
    this.child.send(message, error => { if (error) entryError(error); });
  }

  release(name) { this.send({ type: 'release', name }); }

  async kill() {
    if (!this.exit) this.child.kill('SIGKILL');
    const outcome = await this.exited.promise;
    if (!this.exit) this.exit = outcome;
    assert.equal(outcome.signal, 'SIGKILL');
    return outcome;
  }

  async stop() {
    assert(!this.exit, 'worker exited before shutdown');
    this.send({ type: 'shutdown' });
    const timer = setTimeout(() => {
      reportAndKill([this], entryError, new Error(`${this.scenario.name}: shutdown watchdog expired`));
    }, 2000);
    await this.exited.promise;
    clearTimeout(timer);
    assert.deepEqual(this.exit, { code: 0, signal: null }, `${this.scenario.name}: clean worker shutdown`);
    assert(this.messages.some(message => message.type === 'shutdown'));
  }
}

class NumericProvider {
  constructor(scenario, reportError) {
    this.scenario = scenario;
    this.reportError = reportError;
    this.requests = [];
    this.waiters = new Set();
    this.policy = 'respond';
    this.intake = new Map();
    this.handlers = new Set();
    this.server = http.createServer((req, res) => {
      // This listener belongs to one scenario for its entire lifetime. Snapshot
      // the policy at headers too; a slow body cannot migrate to another case.
      const handler = this.handle(req, res, this.policy);
      this.handlers.add(handler);
      void handler.then(() => this.handlers.delete(handler), error => {
        this.handlers.delete(handler);
        this.reportError(error);
      });
    });
    this.server.on('clientError', (error, socket) => { this.reportError(error); socket.destroy(); });
    this.server.on('error', this.reportError);
  }

  async listen() {
    await new Promise((resolve, reject) => {
      this.server.once('error', reject);
      this.server.listen(0, '127.0.0.1', resolve);
    });
    providers.add(this);
    this.port = this.server.address().port;
    assert(Number.isInteger(this.port));
  }

  expectIntake(id) {
    assert(!this.closing, 'provider transport registered after intake was sealed');
    assert(typeof id === 'string' && id && !this.intake.has(id), 'invalid or duplicate provider transport ID');
    this.intake.set(id, deferred());
  }

  async handle(req, res, policy) {
    try {
      const intake = this.intake.get(req.headers['x-artel-probe-intake']);
      assert(intake && !intake.started, 'unregistered or reused provider transport ID');
      intake.started = true;
      assert.equal(req.method, 'POST');
      assert.equal(req.url, '/v1/chat/completions');
      let raw = '';
      for await (const chunk of req) {
        raw += chunk;
        assert(raw.length < 200000, 'provider fixture body limit');
      }
      assert(req.complete, 'provider request body was truncated');
      intake.resolve();
      const entry = {
        sequence: this.requests.length + 1,
        scenario: this.scenario.name,
        token: req.headers['x-artel-probe-token'],
        headers: { ...req.headers },
        rawBody: raw,
        body: JSON.parse(raw),
        held: policy === 'hold',
        response: policy,
      };
      assert(typeof entry.token === 'string' && entry.token, 'provider request is missing its independent token');
      this.requests.push(entry);
      fs.appendFileSync(path.join(this.scenario.dir, 'provider-requests.jsonl'), `${JSON.stringify(entry)}\n`, { mode: 0o600 });
      if (this.closing || policy === 'disconnect') {
        req.socket.destroy();
      } else if (policy !== 'hold') {
        this.respond(entry, req, res);
      } else {
        entry.req = req;
        entry.res = res;
      }
      for (const waiter of [...this.waiters]) waiter.check();
    } catch (error) {
      this.reportError(error);
      if (!res.headersSent && !res.destroyed) res.writeHead(500);
      res.end();
    }
  }

  respond(entry, req = entry.req, res = entry.res) {
    // IncomingMessage may be marked destroyed after its body is consumed even
    // though the ServerResponse is still writable. Only the response matters.
    if (!res || res.destroyed || !res.writable) return;
    entry.response = 'responded';
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const base = { id: `numeric-${entry.sequence}`, object: 'chat.completion.chunk', created: 1, model: 'fixture' };
    res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: { role: 'assistant', content: '42' }, finish_reason: null }] })}\n\n`);
    res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`);
    res.end('data: [DONE]\n\n');
  }

  release(entry) {
    assert(entry?.held, 'only an explicitly held request can be released');
    entry.held = false;
    this.respond(entry);
  }

  drop(entry) {
    if (entry?.res && !entry.res.destroyed) entry.res.destroy();
    if (entry) entry.held = false;
  }

  async wait(predicate = () => true, timeout = 6000) {
    const found = this.requests.find(predicate);
    if (found) return found;
    const waiter = deferred();
    const entry = {
      check: () => {
        const match = this.requests.find(predicate);
        if (match) {
          clearTimeout(timer);
          this.waiters.delete(entry);
          waiter.resolve(match);
        }
      },
    };
    const timer = setTimeout(() => {
      this.waiters.delete(entry);
      waiter.reject(new Error(`${this.scenario.name}: provider request barrier timeout`));
    }, timeout);
    this.waiters.add(entry);
    entry.check();
    return waiter.promise;
  }

  async close(timeout = 3000) {
    this.closing = true;
    for (const request of this.requests) if (request.held) this.drop(request);
    let timer;
    const expired = new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${this.scenario.name}: provider intake drain timed out`)), timeout);
    });
    const closeListener = () => new Promise((resolve, reject) =>
      this.server.close(error => error ? reject(error) : resolve()));
    try {
      // Producers have been reaped. Each one-shot write needed a registration
      // ACK; keep accepting/parsing until every registered full body is seen.
      // Neither child exit nor absence of HTTP callbacks proves that boundary.
      await Promise.race([Promise.all([...this.intake.values()].map(item => item.promise)), expired]);
      await Promise.race([closeListener().then(() => Promise.all([...this.handlers])), expired]);
    } finally {
      clearTimeout(timer);
      if (this.server.listening) this.server.close();
      this.server.closeAllConnections();
      providers.delete(this);
    }
  }
}

class Scenario {
  constructor(name, reportError = entryError, signalState = cancellation) {
    this.name = name;
    this.cancellation = signalState;
    this.watchdogError = null;
    this.dir = path.join(root, name);
    fs.mkdirSync(this.dir, { recursive: true, mode: 0o700 });
    this.identity = { messageId: `message-${name}`, generation: 7, sessionId: randomUUID() };
    this.payload = {
      content: `Fictional numeric consultation ${name}; literal marker ${`MARKER_${name.toUpperCase().replaceAll('-', '_')}`}`,
      details: {
        message_id: `message-${name}`,
        marker: `MARKER_${name.toUpperCase().replaceAll('-', '_')}`,
        author: 'developer',
        recipient: 'product',
      },
    };
    this.serverPath = path.join(this.dir, 'server.json');
    this.journalPath = path.join(this.dir, 'journal.json');
    this.tokens = new Map();
    this.piSends = 0;
    this.transmitCommands = 0;
    this.journalIds = new Map();
    this.children = [];
    this.sequence = 0;
    this.provider = new NumericProvider(this, reportError);
  }

  async start(mode = 'combined', options = {}) {
    const number = ++this.sequence;
    const config = {
      configPath: path.join(this.dir, `config-${number}.json`),
      root,
      scenarioRoot: this.dir,
      mode,
      providerPort: this.provider.port,
      identity: this.identity,
      payload: this.payload,
      serverPath: this.serverPath,
      journalPath: this.journalPath,
      createServer: options.createServer ?? number === 1,
      createJournal: options.createJournal ?? number === 1,
      autoDeliver: options.autoDeliver ?? mode !== 'pi',
      ...options,
    };
    if (!config.createJournal && mode !== 'pi') {
      config.expectedJournalId = this.journalIds.get(config.journalPath);
      assert(config.expectedJournalId, 'recovery requires an original journal ID');
    }
    const child = new OwnedChild(this, config);
    this.children.push(child);
    if (!options.expectSetupError) {
      const ready = await child.wait('ready');
      if (ready.journalId) this.journalIds.set(config.journalPath, ready.journalId);
    }
    return child;
  }

  state() {
    const server = JSON.parse(fs.readFileSync(this.serverPath, 'utf8'));
    const journal = fs.existsSync(this.journalPath)
      ? JSON.parse(fs.readFileSync(this.journalPath, 'utf8'))
      : undefined;
    return { server: server.messages[this.identity.messageId], activeGeneration: server.activeGeneration, journal: journal?.messages[this.identity.messageId], journalId: journal?.journalId };
  }

  providerRequests() { return this.provider.requests; }

  async finishSuccessful(child) {
    await child.wait('receipt-result', message => message.result.status === 'observed');
    const settled = await child.wait('agent-settled');
    assert.equal(settled.idle, true);
    assert.equal(settled.assistantText, '42');
    assert.equal(this.state().server.receipt.messageType, 'custom');
  }

  correlate({ manual = false } = {}) {
    const requests = this.providerRequests();
    const perToken = new Map();
    for (const request of requests) {
      const event = this.tokens.get(request.token);
      assert(event, `${this.name}: provider request has no before_agent_start token`);
      assert(!perToken.has(request.token), `${this.name}: retries/duplicate request for one token`);
      perToken.set(request.token, request);
      assert.equal(request.body.model, 'fixture');
      assert(!request.body.tools || request.body.tools.length === 0);
      const initiatingText = event.messageType === 'custom' ? this.payload.content : event.prompt;
      assertInitiatingMessage(request.body.messages, initiatingText);
      assert.equal(request.headers.authorization, 'Bearer fake-not-a-secret');
      if (event.messageType === 'custom') {
        assert.deepEqual(event.details, this.payload.details);
        assert.equal(event.sessionId, this.identity.sessionId);
        assert.equal(event.generation, this.identity.generation);
        assert.equal(event.messageId, this.identity.messageId);
        const state = this.state();
        assert.equal(event.attemptId, state.server.spend.attemptId);
        assert.equal(event.journalId, state.server.spend.journalId);
        assert.equal(event.ownerPid, state.server.spend.ownerPid);
        assert.equal(event.ownerInstance, state.server.spend.ownerInstance);
      } else {
        assert.equal(event.messageType, 'user');
        assert.equal(event.sessionId, this.identity.sessionId);
        assert.equal(event.prompt, `MANUAL_OLD_MARKER ${this.payload.details.marker}`);
        assert.equal(event.attemptId, undefined);
      }
    }
    assert.deepEqual(new Set(perToken.keys()), new Set(this.tokens.keys()), 'missing request for a recorded start');
    assert.deepEqual([...this.tokens.values()].map(event => event.messageType),
      [...Array(this.piSends).fill('custom'), ...(manual ? ['user'] : [])]);
    if (manual) assert.equal(this.piSends, 1, 'manual old-marker turn must not be another bridge send');
    assertOracleHealthy();
  }

  summary(options = {}) {
    this.summaryOptions = options;
  }

  finalize(expectedPiSends) {
    const extra = this.summaryOptions;
    assert(extra, 'scenario did not supply summary options');
    const state = this.state();
    this.correlate(extra);
    const expectedTurns = expectedPiSends + (extra.manual ? 1 : 0);
    assert.deepEqual([this.piSends, this.providerRequests().length, this.tokens.size],
      [expectedPiSends, expectedTurns, expectedTurns], `${this.name}: unexpected final Pi/HTTP/start counts`);
    const verifiedCounts = extra.verify?.();
    const summary = {
      name: this.name,
      result: 'PASS',
      piSends: this.piSends,
      transmitCommands: this.transmitCommands,
      providerRequests: this.providerRequests().length,
      beforeAgentStart: this.tokens.size,
      customStarts: [...this.tokens.values()].filter(event => event.messageType === 'custom').length,
      userStarts: [...this.tokens.values()].filter(event => event.messageType === 'user').length,
      serverStatus: state.server ? (state.server.receipt ? 'observed' : state.server.spend ? 'unknown' : 'admitted') : 'absent',
      activeGeneration: state.activeGeneration,
      ...extra.counts,
      ...verifiedCounts,
    };
    results.push(summary);
    return summary;
  }
}

async function run(name, expectedPiSends, body) {
  checkCancelled();
  const scenario = new Scenario(name);
  const timeout = startWatchdog(scenario);
  try {
    await scenario.provider.listen();
    checkCancelled(scenario);
    await body(scenario);
  } finally {
    try {
      for (const request of scenario.providerRequests()) if (request.held) scenario.provider.drop(request);
      await reapChildren(scenario.children);
    } finally {
      try {
        if (providers.has(scenario.provider)) await scenario.provider.close();
      } finally {
        clearTimeout(timeout);
      }
    }
  }
  checkCancelled(scenario);
  scenario.finalize(expectedPiSends);
}

function reportAndKill(owned, reportError, error) {
  try {
    reportError(error);
  } finally {
    for (const child of owned) if (!child.exit) child.child.kill('SIGKILL');
  }
}

function startWatchdog(scenario, ms = 15000, reportError = entryError) {
  return setTimeout(() => {
    scenario.watchdogError = new Error(`${scenario.name}: scenario watchdog expired`);
    reportAndKill(scenario.children, reportError, scenario.watchdogError);
  }, ms);
}

async function reapChildren(owned) {
  const pending = [...owned].filter(child => !child.exit);
  const outcomes = await Promise.allSettled(pending.map(child => child.kill()));
  const failures = outcomes.filter(outcome => outcome.status === 'rejected');
  if (failures.length) throw new AggregateError(failures.map(failure => failure.reason), 'worker cleanup failed');
}

function connectPi(pi, ...bridges) {
  assert([pi, ...bridges].every(worker => worker.child.connected && !worker.exit));
  const transmit = message => {
    if (message.type === 'pi-transmit') {
      if (!pi.child.connected) entryError(new Error('transmission command has no live Pi route'));
      else pi.send({ type: 'transmit', transmission: message.transmission });
    }
  };
  const observe = message => {
    if (message.type !== 'observation') return;
    const owner = bridges.find(bridge => bridge.child.pid === message.ownerPid);
    if (owner?.child.connected) owner.send(message);
  };
  for (const bridge of bridges) bridge.child.on('message', transmit);
  pi.child.on('message', observe);
  return () => {
    for (const bridge of bridges) bridge.child.off('message', transmit);
    pi.child.off('message', observe);
  };
}

function assertOracleHealthy(errors = oracleErrors) {
  assert.equal(errors.length, 0, `oracle/runner failures: ${errors.map(error => error.message).join('; ')}`);
}

function assertInitiatingMessage(messages, expected) {
  assert(Array.isArray(messages));
  const text = message => typeof message.content === 'string' ? message.content
    : message.content?.map(part => part.type === 'text' ? part.text : '').join('');
  assert.equal(messages.at(-1)?.role, 'user');
  assert.equal(text(messages.at(-1)), expected, 'last provider message must be the exact initiator, not history');
  assert.equal(messages.filter(message => message.role === 'user' && text(message) === expected).length, 1);
}

function assertPersistedRestart(before, after) {
  assert.deepEqual(after, {
    ...before,
    server: { ...before.server, receipt: before.journal.receipt },
  }, 'restart must preserve the full spend and journal and publish only the original local receipt');
}

async function recoverUnknown(scenario) {
  const before = scenario.providerRequests().length;
  const persisted = scenario.state();
  assert(persisted.server.spend);
  assert.equal(persisted.journal.receipt, null);
  const sends = scenario.piSends;
  const recovered = await scenario.start('combined', { createServer: false, createJournal: false });
  const delivery = await recovered.wait('delivery-result', message => message.command === 'initial');
  assert.equal(delivery.result.transmitted, false);
  assert.equal(delivery.result.status, 'unknown');
  assert.equal(scenario.providerRequests().length, before);
  assert.equal(scenario.piSends, sends);
  await recovered.stop();
  assertPersistedRestart(persisted, scenario.state());
  return recovered;
}

function requestRedelivery(child) {
  const firstNewMessage = child.messages.length;
  child.send({ type: 'deliver' });
  return child.wait('delivery-result', message => message.command === 'redelivery'
    && child.messages.indexOf(message) >= firstNewMessage);
}

async function redeliverObserved(child, scenario) {
  const replay = await requestRedelivery(child);
  assert.equal(replay.result.status, 'observed');
  assert.equal(scenario.piSends, 1);
  assert.equal(scenario.providerRequests().length, 1);
}

async function positiveControl() {
  await run('positive-control', 1, async scenario => {
    const child = await scenario.start();
    await scenario.finishSuccessful(child);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    child.send({ type: 'deliver' });
    const replay = await child.wait('delivery-result', message => message.command === 'redelivery');
    assert.equal(replay.result.status, 'observed');
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    child.send({ type: 'manual' });
    await child.wait('agent-settled', message => scenario.tokens.get(message.token)?.messageType === 'user');
    assert.equal(scenario.providerRequests().length, 2);
    assert.equal(scenario.tokens.size, 2);
    assert.equal(scenario.piSends, 1);
    await child.stop();
    scenario.summary({ manual: true, verify: () => {
      // Check the complete IPC stream, even for an idempotent extra writer invocation.
      assert.equal(child.messages.filter(message => message.type === 'receipt-write-start').length, 1,
        'old-marker user turn must not start another receipt writer');
      return { oldMarkerAdditionalReceiptWrites: 0 };
    } });
  });
}

async function admissionResponseMatrix() {
  for (const phase of ['beforeCommit', 'beforeResponse']) {
    await run(`admission-response-lost-${phase}`, 1, async scenario => {
      const child = await scenario.start('combined', { failAt: [`admission:${phase}`] });
      await child.wait('delivery-error', message => message.command === 'initial');
      assert.equal(scenario.piSends, 0);
      assert.equal(scenario.providerRequests().length, 0);
      assert.equal(Boolean(scenario.state().server), phase === 'beforeResponse');
      child.send({ type: 'deliver' });
      await scenario.finishSuccessful(child);
      assert.equal(scenario.piSends, 1);
      assert.equal(scenario.providerRequests().length, 1);
      await redeliverObserved(child, scenario);
      await child.stop();
      scenario.summary();
    });
  }
}

async function crashBeforeSpend() {
  await run('crash-before-spend-safe-continuation', 1, async scenario => {
    const child = await scenario.start('combined', { pauses: ['bridge:beforeSpend'] });
    await child.wait('barrier', message => message.name === 'bridge:beforeSpend');
    assert.equal(scenario.state().server.spend, null);
    assert.equal(scenario.state().journal.attempt, null);
    await child.kill();

    const recovered = await scenario.start('combined', { createServer: false, createJournal: false });
    await scenario.finishSuccessful(recovered);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    await redeliverObserved(recovered, scenario);
    await recovered.stop();
    scenario.summary({ counts: { safeUnspentContinuations: 1 } });
  });
}

async function crashSpentBeforePi() {
  const cases = [
    ['crash-after-spend-commit-before-response', 'spend:afterCommit', false],
    ['crash-after-spend-response-before-attempt', 'bridge:afterSpendResponse', false],
    ['crash-after-attempt-before-pi', 'bridge:afterAttemptCommit', true],
  ];
  for (const [name, barrier, attemptCommitted] of cases) {
    await run(name, 0, async scenario => {
      const child = await scenario.start('combined', { pauses: [barrier] });
      await child.wait('barrier', message => message.name === barrier);
      assert(scenario.state().server.spend);
      assert.equal(Boolean(scenario.state().journal.attempt), attemptCommitted);
      assert.equal(scenario.piSends, 0);
      await child.kill();
      await recoverUnknown(scenario);
      assert.equal(scenario.providerRequests().length, 0);
      scenario.summary();
    });
  }
}

async function transmissionAndReceiptMatrix() {
  await run('whole-sdk-crash-after-transmission-before-receipt', 1, async scenario => {
    scenario.provider.policy = 'hold';
    const child = await scenario.start('combined', { pauses: ['receipt:beforeWrite'] });
    const request = await scenario.provider.wait(item => item.scenario === scenario.name);
    await child.wait('barrier', message => message.name === 'receipt:beforeWrite');
    assert.equal(scenario.state().journal.receipt, null);
    assert.equal(scenario.state().server.receipt, null);
    assert.equal(scenario.piSends, 1);
    await child.kill();
    scenario.provider.drop(request);
    await recoverUnknown(scenario);
    assert.equal(scenario.providerRequests().length, 1);
    scenario.summary();
  });

  await run('http-disconnect-after-body-before-headers', 1, async scenario => {
    scenario.provider.policy = 'disconnect';
    const child = await scenario.start();
    await child.wait('receipt-result', message => message.result.status === 'observed');
    await child.wait('agent-settled');
    assert.equal(scenario.providerRequests()[0].response, 'disconnect');
    assert.equal(scenario.providerRequests().length, 1);
    await redeliverObserved(child, scenario);
    await child.stop();
    scenario.summary();
  });

  await run('crash-after-local-receipt-before-post', 1, async scenario => {
    scenario.provider.policy = 'hold';
    const child = await scenario.start('combined', { pauses: ['bridge:afterReceiptCommit'] });
    const request = await scenario.provider.wait(item => item.scenario === scenario.name);
    await child.wait('barrier', message => message.name === 'bridge:afterReceiptCommit');
    const before = scenario.state();
    assert(before.journal.receipt);
    assert.equal(before.server.receipt, null);
    const attemptId = before.server.spend.attemptId;
    await child.kill();
    scenario.provider.drop(request);

    const recovered = await scenario.start('combined', { createServer: false, createJournal: false });
    const replay = await recovered.wait('delivery-result', message => message.command === 'initial');
    assert.equal(replay.result.status, 'observed');
    assert.equal(replay.result.attemptId, attemptId);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    await recovered.stop();
    assertPersistedRestart(before, scenario.state());
    scenario.summary();
  });

  for (const phase of ['beforeCommit', 'beforeResponse']) {
    await run(`receipt-response-lost-${phase}`, 1, async scenario => {
      const child = await scenario.start('combined', { failAt: [`receipt:${phase}`] });
      await child.wait('receipt-error');
      await child.wait('agent-settled');
      assert(scenario.state().journal.receipt);
      assert.equal(Boolean(scenario.state().server.receipt), phase === 'beforeResponse');
      const attemptId = scenario.state().server.spend.attemptId;
      await redeliverObserved(child, scenario);
      assert.equal(scenario.state().server.spend.attemptId, attemptId);
      await child.stop();
      scenario.summary();
    });
  }
}

async function writeFailureMatrix() {
  await run('attempt-write-failure-zero-calls', 0, async scenario => {
    const child = await scenario.start('combined', { failJournalWrite: 'attempt' });
    const failure = await child.wait('delivery-error', message => message.command === 'initial');
    assert.equal(failure.code, 'ATTEMPT_WRITE_FAILED');
    assert.equal(scenario.piSends, 0);
    assert.equal(scenario.providerRequests().length, 0);
    assert(scenario.state().server.spend);
    assert.equal(scenario.state().journal.attempt, null);
    const replay = await requestRedelivery(child);
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    await child.stop();
    scenario.summary();
  });

  await run('local-receipt-write-failure-remains-unknown', 1, async scenario => {
    const child = await scenario.start('combined', { failJournalWrite: 'receipt' });
    const failure = await child.wait('receipt-error');
    assert.equal(failure.code, 'INJECTED_WRITE_FAILURE');
    await child.wait('agent-settled');
    assert.equal(scenario.state().journal.receipt, null);
    assert.equal(scenario.state().server.receipt, null);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    const replay = await requestRedelivery(child);
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    assert.equal(scenario.piSends, 1);
    await child.stop();
    scenario.summary();
  });
}

async function splitProcessMatrix() {
  await run('pi-crash-after-transmission-bridge-alive', 1, async scenario => {
    scenario.provider.policy = 'hold';
    const pi = await scenario.start('pi', { autoDeliver: false });
    const bridge = await scenario.start('bridge', {
      createServer: true,
      createJournal: true,
      autoDeliver: false,
      pauses: ['receipt:beforeWrite'],
    });
    const disconnect = connectPi(pi, bridge);
    const initialPi = await pi.wait('ready');
    bridge.send({ type: 'deliver' });
    const request = await scenario.provider.wait(item => item.scenario === scenario.name);
    await bridge.wait('barrier', message => message.name === 'receipt:beforeWrite');
    const persistedFiles = () => [scenario.serverPath, scenario.journalPath].map(file => fs.readFileSync(file));
    const before = persistedFiles();
    const state = scenario.state();
    assert.equal(state.server.spend.ownerPid, bridge.child.pid);
    assert.deepEqual(state.journal.attempt, state.server.spend);
    assert.equal(state.journal.receipt, null);
    assert.equal(state.server.receipt, null);
    const counts = () => [scenario.transmitCommands, scenario.piSends, scenario.providerRequests().length];
    const countsBefore = counts();
    assert.deepEqual(countsBefore, [1, 1, 1]);
    await pi.kill();
    disconnect();
    scenario.provider.drop(request);

    // Reopen the test session if Pi had already written it; otherwise create
    // a fresh Pi runtime with the same fixture identity (not recovered history).
    const restartedPi = await scenario.start('pi', { sessionFile: initialPi.sessionFile });
    assert.equal(initialPi.sessionId, scenario.identity.sessionId);
    assert.equal((await restartedPi.wait('ready')).sessionId, initialPi.sessionId);
    connectPi(restartedPi, bridge);
    const replay = await requestRedelivery(bridge);
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    assert.equal(replay.result.attemptId, state.server.spend.attemptId);
    await bridge.stop();
    await restartedPi.stop();
    scenario.summary({ verify() {
      const deltas = counts().map((count, index) => count - countsBefore[index]);
      assert.deepEqual(deltas, [0, 0, 0], 'redelivery to the live replacement must not add IPC/Pi/HTTP');
      assert.deepEqual(persistedFiles(), before, 'full persisted server and journal must match after shutdown');
      assert.equal(scenario.state().server.receipt, null);
      const [postRestartTransmitCommands, postRestartPiSends, postRestartProviderRequests] = deltas;
      return { sameSessionPiRestart: true, postRestartTransmitCommands, postRestartPiSends,
        postRestartProviderRequests, persistedStateUnchanged: true };
    } });
  });

  await run('bridge-crash-pi-alive-late-completion-and-restart', 1, async scenario => {
    scenario.provider.policy = 'hold';
    const oldPi = await scenario.start('pi', { autoDeliver: false });
    const oldPiReady = oldPi.messages.find(message => message.type === 'ready');
    const bridge = await scenario.start('bridge', {
      createServer: true,
      createJournal: true,
      autoDeliver: false,
      pauses: ['receipt:beforeWrite'],
    });
    connectPi(oldPi, bridge);
    bridge.send({ type: 'deliver' });
    const request = await scenario.provider.wait(item => item.scenario === scenario.name);
    await bridge.wait('barrier', message => message.name === 'receipt:beforeWrite');
    const before = scenario.state();
    assert(before.server.spend);
    assert.equal(before.journal.receipt, null);
    await bridge.kill();
    scenario.provider.release(request);
    const late = await oldPi.wait('agent-settled');
    assert.equal(late.assistantText, '42');
    assert.equal(scenario.state().server.receipt, null);
    assert.equal(scenario.piSends, 1);
    await oldPi.kill();

    assert(oldPiReady.sessionFile && fs.existsSync(oldPiReady.sessionFile));
    const restartedPi = await scenario.start('pi', {
      autoDeliver: false,
      createServer: false,
      createJournal: false,
      sessionFile: oldPiReady.sessionFile,
    });
    assert.equal(restartedPi.messages.find(message => message.type === 'ready').sessionId, scenario.identity.sessionId);
    const recoveredBridge = await scenario.start('bridge', { autoDeliver: false });
    connectPi(restartedPi, recoveredBridge);
    const commandsBefore = scenario.transmitCommands;
    assert.equal(commandsBefore, 1);
    recoveredBridge.send({ type: 'deliver' });
    const replay = await recoveredBridge.wait('delivery-result', message => message.command === 'redelivery');
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    assert.equal(replay.result.attemptId, before.server.spend.attemptId);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    await recoveredBridge.stop();
    await restartedPi.stop();
    assertPersistedRestart(before, scenario.state());
    const postRestartTransmitCommands = scenario.transmitCommands - commandsBefore;
    assert.equal(postRestartTransmitCommands, 0);
    scenario.summary({ counts: { latePiCompletions: 1, postRestartTransmitCommands } });
  });
}

async function journalRedeliveryMatrix() {
  await run('redelivery-with-missing-local-journal', 1, async scenario => {
    const child = await scenario.start();
    await scenario.finishSuccessful(child);
    await child.stop();
    const before = scenario.state();
    fs.rmSync(scenario.journalPath);
    const recovery = await scenario.start('combined', {
      createServer: false,
      createJournal: false,
      expectSetupError: true,
    });
    const failure = await recovery.wait('setup-error');
    assert.equal(failure.code, 'ENOENT');
    assert.deepEqual(await recovery.exited.promise, { code: 1, signal: null });
    assert.deepEqual(scenario.state(), { ...before, journal: undefined, journalId: undefined });
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    scenario.summary();
  });

  await run('redelivery-with-restored-pre-attempt-copy', 0, async scenario => {
    const child = await scenario.start('combined', {
      pauses: ['bridge:afterSpendResponse', 'bridge:afterAttemptCommit'],
    });
    await child.wait('barrier', message => message.name === 'bridge:afterSpendResponse');
    const originalJournalId = scenario.state().journalId;
    const snapshot = path.join(scenario.dir, 'pre-attempt-journal-copy.json');
    fs.copyFileSync(scenario.journalPath, snapshot);
    fs.chmodSync(snapshot, 0o600);
    assert.equal(scenario.state().journal.attempt, null);
    child.release('bridge:afterSpendResponse');
    await child.wait('barrier', message => message.name === 'bridge:afterAttemptCommit');
    assert(scenario.state().journal.attempt);
    await child.kill();
    fs.copyFileSync(snapshot, scenario.journalPath);
    fs.chmodSync(scenario.journalPath, 0o600);
    assert.equal(scenario.state().journal.attempt, null);
    assert.equal(scenario.state().journalId, originalJournalId);
    await recoverUnknown(scenario);
    assert.equal(scenario.piSends, 0);
    assert.equal(scenario.providerRequests().length, 0);
    scenario.summary();
  });

  await run('redelivery-with-invalid-persisted-receipt', 1, async scenario => {
    const child = await scenario.start('combined', { pauses: ['bridge:afterReceiptCommit'] });
    await scenario.provider.wait(item => item.scenario === scenario.name);
    await child.wait('barrier', message => message.name === 'bridge:afterReceiptCommit');
    await child.kill();
    const document = JSON.parse(fs.readFileSync(scenario.journalPath, 'utf8'));
    document.messages[scenario.identity.messageId].receipt.messageType = 'user';
    fs.writeFileSync(scenario.journalPath, JSON.stringify(document));
    const before = scenario.state().server;
    const recovered = await scenario.start('combined', { expectSetupError: true });
    const failure = await recovered.wait('setup-error');
    assert.equal(failure.code, 'CORRUPT_STATE');
    assert.deepEqual(await recovered.exited.promise, { code: 1, signal: null });
    assert.deepEqual(scenario.state().server, before);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    scenario.summary();
  });

  await run('two-live-competing-senders', 1, async scenario => {
    const pi = await scenario.start('pi');
    const startSender = options => scenario.start('bridge', {
      autoDeliver: false, createJournal: true, pauses: ['bridge:beforeSpend'], ...options,
    });
    const first = await startSender({ createServer: true });
    const second = await startSender({ journalPath: path.join(scenario.dir, 'competitor-journal.json') });
    const senders = [first, second];
    connectPi(pi, ...senders);
    for (const sender of senders) sender.send({ type: 'deliver' });
    await Promise.all(senders.map(sender => sender.wait('barrier', message => message.name === 'bridge:beforeSpend')));
    assert(senders.every(sender => sender.child.connected && !sender.exit));
    assert.equal(scenario.state().server.spend, null);
    assert.equal(scenario.transmitCommands, 0);
    assert.notEqual(...scenario.journalIds.values());
    for (const sender of senders) sender.release('bridge:beforeSpend');
    const deliveries = await Promise.all(senders.map(sender => sender.wait('delivery-result')));
    const grants = senders.flatMap(sender => sender.messages.filter(message => message.type === 'right-granted'));
    assert.equal(grants.length, 1);
    const spend = scenario.state().server.spend;
    assert.equal(grants[0].right.attemptId, spend.attemptId);
    assert(deliveries.every(item => item.result.attemptId === spend.attemptId));
    assert.equal(deliveries.filter(item => item.result.transmitted).length, 1);
    const winner = senders.find(sender => sender.child.pid === spend.ownerPid);
    assert(winner);
    assert.equal(scenario.journalIds.get(winner.config.journalPath), spend.journalId);
    for (const sender of senders) {
      const local = JSON.parse(fs.readFileSync(sender.config.journalPath, 'utf8')).messages[scenario.identity.messageId];
      assert.deepEqual(local.attempt, sender === winner ? spend : null);
    }
    await winner.wait('receipt-result', message => message.result.status === 'observed');
    assert.equal((await pi.wait('agent-settled')).assistantText, '42');
    await pi.stop();
    await Promise.all(senders.map(sender => sender.stop()));
    assert.equal(scenario.transmitCommands, 1);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    scenario.summary({ counts: { liveSendersBeforeSpend: 2, spendGrants: grants.length } });
  });
}

async function revokeAtBarrier(scenario, child, barrier) {
  await child.wait('barrier', message => message.name === barrier);
  child.send({ type: 'revoke' });
  const revoked = await child.wait('revoked');
  assert.equal(revoked.activeGeneration, scenario.identity.generation + 1);
  child.release(barrier);
}

async function revocationMatrix() {
  await run('revocation-before-admission', 0, async scenario => {
    const child = await scenario.start('combined', { revokeBeforeDeliver: true });
    const failure = await child.wait('delivery-error', message => message.command === 'initial');
    assert.equal(failure.code, 'REVOKED_GENERATION');
    assert.equal(scenario.state().server, undefined);
    assert.equal(scenario.piSends, 0);
    assert.equal(scenario.providerRequests().length, 0);
    await child.stop();
    scenario.summary();
  });

  await run('revocation-after-admission-before-spend', 0, async scenario => {
    const child = await scenario.start('combined', { pauses: ['bridge:beforeSpend'] });
    await revokeAtBarrier(scenario, child, 'bridge:beforeSpend');
    const failure = await child.wait('delivery-error', message => message.command === 'initial');
    assert.equal(failure.code, 'REVOKED_GENERATION');
    assert.equal(scenario.state().server.spend, null);
    assert.equal(scenario.piSends, 0);
    assert.equal(scenario.providerRequests().length, 0);
    await child.stop();
    scenario.summary();
  });

  await run('revocation-after-spend-before-final-verification', 0, async scenario => {
    const child = await scenario.start('combined', { pauses: ['bridge:afterAttemptCommit'] });
    await revokeAtBarrier(scenario, child, 'bridge:afterAttemptCommit');
    const result = await child.wait('delivery-result', message => message.command === 'initial');
    assert.equal(result.result.status, 'unknown');
    assert.equal(result.result.transmitted, false);
    assert.equal(scenario.piSends, 0);
    assert.equal(scenario.providerRequests().length, 0);
    await child.stop();
    scenario.summary();
  });

  await run('revocation-after-transmission-historical-receipt', 1, async scenario => {
    scenario.provider.policy = 'hold';
    const child = await scenario.start('combined', { pauses: ['receipt:beforeWrite'] });
    const request = await scenario.provider.wait(item => item.scenario === scenario.name);
    await child.wait('barrier', message => message.name === 'receipt:beforeWrite');
    child.send({ type: 'revoke' });
    const revoked = await child.wait('revoked');
    const activeGeneration = revoked.activeGeneration;
    child.release('receipt:beforeWrite');
    scenario.provider.release(request);
    await scenario.finishSuccessful(child);
    assert.equal(scenario.state().activeGeneration, activeGeneration);
    assert.equal(scenario.state().server.identity.generation, scenario.identity.generation);
    assert(scenario.state().server.receipt);
    await redeliverObserved(child, scenario);
    await child.stop();
    scenario.summary();
  });

  await run('residual-final-check-to-send-revocation-race', 1, async scenario => {
    const child = await scenario.start('combined', { pauses: ['bridge:afterFinalVerification'] });
    await revokeAtBarrier(scenario, child, 'bridge:afterFinalVerification');
    await scenario.finishSuccessful(child);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    assert.equal(scenario.state().activeGeneration, scenario.identity.generation + 1);
    await child.stop();
    scenario.summary({ counts: { residualRaceCalls: 1 } });
  });
}

async function assertLateDuplicateDrained() {
  let sampled;
  const count = results.length;
  await assert.rejects(run('late-provider-duplicate-check', 1, async scenario => {
    sampled = scenario;
    const oracle = scenario.provider;
    const child = await scenario.start();
    await scenario.finishSuccessful(child);
    await child.stop();
    const token = randomUUID();
    const prompt = `MANUAL_OLD_MARKER ${scenario.payload.details.marker}`;
    scenario.tokens.set(token, { token, messageType: 'user', prompt, sessionId: scenario.identity.sessionId });
    const body = JSON.stringify({ model: 'fixture', messages: [{ role: 'user', content: prompt }] });
    const headers = { 'x-artel-probe-token': token, authorization: 'Bearer fake-not-a-secret',
      'content-length': Buffer.byteLength(body) };
    const url = `http://127.0.0.1:${oracle.port}/v1/chat/completions`;
    const signal = AbortSignal.timeout(3000);
    // Deliberately use a keep-alive client here (not used by the SDK worker)
    // so the old close-before-drain path would discard the idle socket.
    const agent = new http.Agent({ keepAlive: true });
    try {
      const firstId = randomUUID();
      oracle.expectIntake(firstId);
      const accepted = once(oracle.server, 'connection', { signal });
      const idle = once(agent, 'free', { signal });
      const first = http.request(url, { method: 'POST', agent, signal, headers: { ...headers, 'x-artel-probe-intake': firstId } });
      const response = once(first, 'response', { signal }).then(async ([res]) => {
        assert.equal(res.statusCode, 200);
        for await (const _ of res) { /* consume the response to idle the socket */ }
      });
      first.end(body);
      const [[socket], [duplicate]] = await Promise.all([accepted, idle, response]);
      const duplicateId = randomUUID();
      oracle.expectIntake(duplicateId);
      const wire = Buffer.from([
        'POST /v1/chat/completions HTTP/1.1', `Host: 127.0.0.1:${oracle.port}`,
        'Connection: close', `x-artel-probe-intake: ${duplicateId}`,
        ...Object.entries(headers).map(([key, value]) => `${key}: ${value}`), '', body,
      ].join('\r\n'));
      const clientClosed = new Promise(resolve => duplicate.once('close', resolve));
      const buffered = deferred();
      // A readable listener buffers TCP bytes without forwarding them to the
      // HTTP parser's data listener. Verify arrival, not just client write().
      const onReadable = () => {
        if (socket.readableLength >= wire.length) buffered.resolve();
      };
      socket.on('readable', onReadable);
      signal.addEventListener('abort', () => buffered.reject(signal.reason), { once: true });
      duplicate.write(wire);
      await buffered.promise;
      assert.equal(oracle.intake.get(duplicateId).started, undefined, 'HTTP callback ran before shutdown');
      assert.equal(scenario.providerRequests().length, 2);
      scenario.correlate({ manual: true }); // The sample would pass without the buffered duplicate.
      scenario.summary({ manual: true });
      const close = oracle.close.bind(oracle);
      oracle.close = async () => {
        const draining = close();
        socket.off('readable', onReadable);
        socket.resume();
        try { await draining; } finally {
          agent.destroy();
          await clientClosed;
        }
      };
    } catch (error) {
      agent.destroy();
      throw error;
    }
  }), /duplicate request for one token/);
  assert.deepEqual(sampled.providerRequests().map(item => item.scenario), Array(3).fill(sampled.name));
  assert.equal(results.length, count);
}

async function assertLateFreshDeliveryRejected() {
  let sampled;
  const count = results.length;
  await assert.rejects(run('late-fresh-delivery-check', 1, async scenario => {
    sampled = scenario;
    const child = await scenario.start();
    await scenario.finishSuccessful(child);
    await redeliverObserved(child, scenario);
    await child.stop();
    scenario.correlate();
    scenario.summary();

    const oracle = scenario.provider;
    const [initial] = scenario.providerRequests();
    const id = randomUUID();
    const token = randomUUID();
    oracle.expectIntake(id);
    const close = oracle.close.bind(oracle);
    oracle.close = async () => {
      // Simulate a send and fresh start captured at shutdown, with the matching
      // complete POST still in flight at drain. Correlation alone must pass.
      scenario.piSends += 1;
      scenario.tokens.set(token, { ...scenario.tokens.get(initial.token), token });
      const post = http.request(`http://127.0.0.1:${oracle.port}/v1/chat/completions`, {
        method: 'POST', signal: AbortSignal.timeout(3000),
        headers: { connection: 'close', 'x-artel-probe-intake': id,
          'x-artel-probe-token': token, authorization: initial.headers.authorization,
          'content-length': Buffer.byteLength(initial.rawBody) },
      });
      const closed = new Promise(resolve => post.once('close', resolve));
      post.on('error', error => {
        // The provider destroys the socket rather than responding during drain.
        if (error.code !== 'ECONNRESET') entryError(error);
      });
      post.end(initial.rawBody);
      await Promise.all([close(), closed]);
      scenario.correlate();
    };
  }), /unexpected final Pi\/HTTP\/start counts/);
  assert.equal(sampled.providerRequests().length, 2);
  assert.equal(results.length, count);
}

async function exerciseRunnerGuards() {
  const faults = [];
  const report = error => faults.push(error);
  const signalState = { error: null };
  const scenario = new Scenario('runner-guard-checks', report, signalState);
  const oracle = scenario.provider;
  try {
    await oracle.listen();
    const url = `http://127.0.0.1:${oracle.port}/v1/chat/completions`;
    const body = JSON.stringify({ model: 'fixture', messages: [] });
    const unregistered = await fetch(url, { method: 'POST', headers: { connection: 'close' }, body });
    assert.equal(unregistered.status, 500);
    await unregistered.text();
    assert.throws(() => assertOracleHealthy(faults), /unregistered or reused provider transport ID/);
    faults.length = 0;

    const id = randomUUID();
    oracle.expectIntake(id);
    const res = await fetch(url, {
      method: 'POST', headers: { connection: 'close', 'x-artel-probe-intake': id }, body,
    });
    assert.equal(res.status, 500);
    await res.text();
    assert.equal(oracle.requests.length, 0);
    assert.throws(() => assertOracleHealthy(faults), /missing its independent token/);
    assert.throws(() => assertInitiatingMessage([
      { role: 'user', content: 'expected' }, { role: 'user', content: 'different' },
    ], 'expected'), /not history/);

    faults.length = 0;
    const failed = new OwnedChild(scenario, {
      scenarioRoot: scenario.dir, configPath: path.join(scenario.dir, 'spawn-rejection.json'),
    }, { executable: path.join(scenario.dir, 'does-not-exist'), reportError: report });
    await assert.rejects(failed.wait('ready'), /exited before ready/);
    assert.equal((await failed.exited.promise).error.code, 'ENOENT');
    assert(!children.has(failed));
    assert.throws(() => assertOracleHealthy(faults), /ENOENT/);

    faults.length = 0;
    const worker = await scenario.start('bridge', { autoDeliver: false });
    const watchdog = startWatchdog(scenario, 1, report);
    try {
      assert.deepEqual(await worker.exited.promise, { code: null, signal: 'SIGKILL' });
      await worker.kill(); // A crash path awaiting SIGKILL also accepts the watchdog's exit.
      assert.throws(() => assertOracleHealthy(faults), /scenario watchdog expired/);
      const configCount = scenario.children.length;
      await assert.rejects(scenario.start('bridge', { autoDeliver: false }), /scenario watchdog expired/);
      assert.equal(scenario.children.length, configCount, 'watchdog must prevent recovery worker spawn');
    } finally {
      clearTimeout(watchdog);
    }
    faults.length = 0;
    const broken = diagnostics(faults, () => { throw Object.assign(new Error('ENOSPC'), { code: 'ENOSPC' }); });
    const diagnosticScenario = { ...scenario, watchdogError: null };
    for (const terminate of [false, true]) {
      const config = { ...worker.config, createServer: false, createJournal: false,
        expectedJournalId: scenario.journalIds.get(worker.config.journalPath) };
      const owned = new OwnedChild(diagnosticScenario, config, { reportError: broken.report, writeDiagnostic: broken.write });
      scenario.children.push(owned);
      await owned.wait('ready');
      owned.child.stdout.emit('data', Buffer.from('fictional stdout'));
      owned.child.stderr.emit('data', Buffer.from('fictional stderr'));
      assert.throws(() => assertOracleHealthy(faults), /diagnostic write failed.*stdout.*diagnostic write failed.*stderr/);
      if (terminate) terminateOwned([owned], [], broken.report, signalState);
      else startWatchdog({ ...diagnosticScenario, children: [owned] }, 1, broken.report);
      assert.deepEqual(await owned.exited.promise, { code: null, signal: 'SIGKILL' });
      assert(!children.has(owned));
      assert.throws(() => assertOracleHealthy(faults), /diagnostic write failed \(runner-errors.log\)/);
      faults.length = 0;
    }

    const configPath = path.join(scenario.dir, 'cancelled-guard.json');
    const assertSpawnBlocked = guard => {
      assert.throws(() => new OwnedChild(guard, { ...worker.config, configPath }), /runner interrupted by SIGTERM/);
      assert(!fs.existsSync(configPath), 'SIGTERM must block worker launch before writing its config');
    };
    assertSpawnBlocked(diagnosticScenario); // Local latch also blocks recovery.
    checkCancelled(); // Do not mask a real SIGTERM.
    try {
      // Inject the global latch synchronously, as if SIGTERM arrived while
      // the guard listener was starting, with no workers yet to kill.
      terminateOwned([], [], () => {});
      assertSpawnBlocked({ ...diagnosticScenario, cancellation: { error: null } });
    } finally {
      cancellation.error = null;
    }

    const before = { server: { spend: { attemptId: 'original', ownerPid: 123 }, receipt: null },
      journal: { receipt: { type: 'before_agent_start' } } };
    assertPersistedRestart(before, { ...before, server: { ...before.server, receipt: before.journal.receipt } });
    assert.throws(() => assertPersistedRestart(before, before), /publish only the original local receipt/);
    const unknown = { ...before, journal: { receipt: null } };
    assert.throws(() => assertPersistedRestart(unknown, { ...unknown, server: { ...unknown.server, receipt: before.journal.receipt } }), /publish only the original local receipt/);
    assert.throws(() => assertPersistedRestart(unknown, { ...unknown, server: { spend: { attemptId: 'original', ownerPid: 456 }, receipt: null } }), /preserve the full spend/);

    await assertLateDuplicateDrained();
    await assertLateFreshDeliveryRejected();
    oracle.expectIntake(randomUUID()); // Acknowledged, but no measurable HTTP body will arrive.
    await assert.rejects(oracle.close(50), /provider intake drain timed out/);
    assert(!oracle.server.listening);
    assertOracleHealthy(faults);
    return { untaggedRequestRejected: true, historicalMessageRejected: true, spawnFailureReaped: true,
      watchdogFatal: true, watchdogPreventsRecoveryLaunch: true, diagnosticWriteFailureFatalAndReaped: true,
      sigtermPreventsWorkerLaunch: true, globalSigtermPreventsGuardLaunch: true,
      persistedRestartMismatchRejected: true, preCallbackDuplicateRejectedAfterDrain: true,
      lateFreshDeliveryRejectedAfterDrain: true, unregisteredProviderTransportRejected: true, unresolvedProviderIntakeRejected: true };
  } finally {
    try { await reapChildren(scenario.children); } finally {
      if (providers.has(oracle)) await oracle.close();
    }
  }
}

function terminateOwned(owned = children, activeProviders = providers, reportError = entryError, state = cancellation) {
  state.error ??= new Error('runner interrupted by SIGTERM');
  try {
    reportAndKill(owned, reportError, state.error);
  } finally {
    for (const provider of activeProviders) provider.server.closeAllConnections();
  }
}
const terminate = () => terminateOwned();
process.on('SIGTERM', terminate);
try {
  await positiveControl();
  await admissionResponseMatrix();
  await crashBeforeSpend();
  await crashSpentBeforePi();
  await transmissionAndReceiptMatrix();
  await writeFailureMatrix();
  await splitProcessMatrix();
  await journalRedeliveryMatrix();
  await revocationMatrix();
  const runnerGuards = await exerciseRunnerGuards();
  assertOracleHealthy();
  const output = { piVersion: sdk.VERSION, nodeVersion: process.versions.node, scenarios: results, runnerGuards };
  fs.writeFileSync(path.join(root, 'result.json'), `${JSON.stringify(output, null, 2)}\n`, { mode: 0o600 });
  for (const summary of results) {
    console.log(`PASS ${summary.name}: pi=${summary.piSends} provider=${summary.providerRequests} before_agent_start=${summary.beforeAgentStart} server=${summary.serverStatus}`);
  }
  console.log(`RESULT ${results.length} SDK scenarios passed; private traces: ${root}`);
} finally {
  try { await reapChildren(children); } finally {
    await Promise.all([...providers].map(provider => provider.close()));
    process.off('SIGTERM', terminate);
  }
}
