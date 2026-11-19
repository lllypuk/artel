// Isolated Pi 1.5.2 SDK delivery/crash matrix. No real providers, tools or sessions.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import fsPromises from 'node:fs/promises';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const EXPECTED_NODE = '/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/bin/node';
const EXPECTED_PACKAGE = '/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/lib/node_modules/@earendil-works/pi-coding-agent';
const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const root = process.env.PROBE_ROOT;
const pkg = process.env.PI_TEST_PACKAGE;
process.umask(0o077);
assert.equal(process.execPath, EXPECTED_NODE, 'run with the absolute pinned Node 22.23.2 binary');
assert.equal(process.versions.node, '22.23.2');
assert.equal(process.platform, 'linux');
assert(pkg, 'PI_TEST_PACKAGE is required');
assert.equal(fs.realpathSync(pkg), EXPECTED_PACKAGE, 'PI_TEST_PACKAGE must be the expected installed package');
assert.equal(JSON.parse(fs.readFileSync(path.join(pkg, 'package.json'), 'utf8')).version, '1.5.2');
const sdk = await import(new URL(`file://${path.join(pkg, 'dist/index.js')}`));
assert.equal(sdk.VERSION, '1.5.2');
for (const api of ['createAgentSession', 'DefaultResourceLoader', 'ModelRuntime', 'SessionManager', 'SettingsManager']) {
  assert.equal(typeof sdk[api], 'function', `missing public Pi API ${api}`);
}
assert(root?.startsWith('/tmp/artel-pi-delivery-'), 'PROBE_ROOT must be an absolute private /tmp/artel-pi-delivery-* path');
assert(path.isAbsolute(root) && path.relative(projectRoot, root).startsWith('..'), 'PROBE_ROOT must be outside the worktree');
const rootStat = fs.statSync(root);
assert(rootStat.isDirectory() && rootStat.uid === process.getuid() && (rootStat.mode & 0o777) === 0o700);
const realRoot = fs.realpathSync(root);
assert(realRoot.startsWith('/tmp/artel-pi-delivery-') && path.relative(projectRoot, realRoot).startsWith('..'));
for (const key of ['HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME', 'XDG_DATA_HOME', 'PI_CODING_AGENT_DIR']) {
  const value = process.env[key];
  assert(value?.startsWith('/'), `${key} must be an absolute private path`);
  const relative = path.relative(realRoot, fs.realpathSync(value));
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
const workerPath = fileURLToPath(new URL('./sdk-worker.mjs', import.meta.url));
const children = new Set();
const results = [];
let currentScenario;

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

class OwnedChild {
  constructor(scenario, config) {
    this.scenario = scenario;
    this.config = config;
    this.messages = [];
    this.waiters = new Set();
    this.stdout = '';
    this.stderr = '';
    fs.writeFileSync(config.configPath, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
    this.child = spawn(process.execPath, [workerPath, config.configPath], {
      cwd: config.scenarioRoot,
      env: process.env,
      stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
    });
    this.child.stdout.on('data', chunk => {
      this.stdout += chunk;
      fs.appendFileSync(path.join(config.scenarioRoot, `${config.mode}-${this.child.pid}-stdout.log`), chunk, { mode: 0o600 });
    });
    this.child.stderr.on('data', chunk => {
      this.stderr += chunk;
      fs.appendFileSync(path.join(config.scenarioRoot, `${config.mode}-${this.child.pid}-stderr.log`), chunk, { mode: 0o600 });
    });
    this.child.on('message', message => {
      this.messages.push(message);
      if (message.type === 'token-event') {
        assert(!scenario.tokens.has(message.token), 'before_agent_start must allocate a fresh token');
        scenario.tokens.set(message.token, message);
      }
      if (message.type === 'pi-send') scenario.piSends += 1;
      for (const waiter of [...this.waiters]) waiter.check();
    });
    this.exited = deferred();
    this.child.once('exit', (code, signal) => {
      children.delete(this);
      this.exit = { code, signal };
      this.exited.resolve(this.exit);
      for (const waiter of [...this.waiters]) waiter.check();
    });
    this.child.once('error', error => {
      this.spawnError = error;
      for (const waiter of [...this.waiters]) waiter.check();
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
        } else if (this.exit || this.spawnError) {
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
    return waiter.promise;
  }

  send(message) {
    assert(this.child.connected && !this.exit, `cannot send ${message.type} to exited ${this.scenario.name} child`);
    this.child.send(message);
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
    if (this.exit) return;
    this.send({ type: 'shutdown' });
    const timer = setTimeout(() => { if (!this.exit) this.child.kill('SIGKILL'); }, 2000);
    await this.exited.promise;
    clearTimeout(timer);
    assert.equal(this.exit.code, 0, `${this.scenario.name}: clean worker shutdown`);
  }
}

class NumericProvider {
  constructor() {
    this.requests = [];
    this.waiters = new Set();
    this.policy = 'respond';
    this.server = http.createServer((req, res) => { void this.handle(req, res); });
  }

  async listen() {
    await new Promise((resolve, reject) => {
      this.server.once('error', reject);
      this.server.listen(0, '127.0.0.1', resolve);
    });
    this.port = this.server.address().port;
    assert(Number.isInteger(this.port));
  }

  async handle(req, res) {
    try {
      assert.equal(req.method, 'POST');
      assert.equal(req.url, '/v1/chat/completions');
      let raw = '';
      for await (const chunk of req) {
        raw += chunk;
        assert(raw.length < 200000, 'provider fixture body limit');
      }
      const entry = {
        sequence: this.requests.length + 1,
        scenario: currentScenario?.name,
        token: req.headers['x-artel-probe-token'],
        headers: { ...req.headers },
        rawBody: raw,
        body: JSON.parse(raw),
        held: this.policy === 'hold',
        response: this.policy,
      };
      assert.equal(typeof entry.token, 'string', 'provider request is missing its independent token');
      this.requests.push(entry);
      fs.appendFileSync(path.join(root, 'provider-requests.jsonl'), `${JSON.stringify(entry)}\n`, { mode: 0o600 });
      if (this.policy === 'disconnect') {
        req.socket.destroy();
      } else if (this.policy !== 'hold') {
        this.respond(entry, req, res);
      } else {
        entry.req = req;
        entry.res = res;
      }
      for (const waiter of [...this.waiters]) waiter.check();
    } catch (error) {
      entryError(error);
      res.writeHead(500);
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
    if (entry?.req && !entry.req.destroyed) entry.req.socket.destroy();
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
      waiter.reject(new Error(`${currentScenario?.name}: provider request barrier timeout`));
    }, timeout);
    this.waiters.add(entry);
    return waiter.promise;
  }

  async close() {
    for (const request of this.requests) this.drop(request);
    this.server.closeAllConnections();
    await new Promise(resolve => this.server.close(resolve));
  }
}

function entryError(error) {
  fs.appendFileSync(path.join(root, 'runner-errors.log'), `${error.stack ?? error}\n`, { mode: 0o600 });
}

const provider = new NumericProvider();
await provider.listen();

class Scenario {
  constructor(name) {
    this.name = name;
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
    this.children = [];
    this.sequence = 0;
  }

  async start(mode = 'combined', options = {}) {
    const number = ++this.sequence;
    const config = {
      configPath: path.join(this.dir, `config-${number}.json`),
      root,
      scenarioRoot: this.dir,
      mode,
      providerPort: provider.port,
      identity: this.identity,
      payload: this.payload,
      serverPath: this.serverPath,
      journalPath: this.journalPath,
      createServer: options.createServer ?? number === 1,
      createJournal: options.createJournal ?? number === 1,
      autoDeliver: options.autoDeliver ?? mode !== 'pi',
      ...options,
    };
    delete config.configPath;
    config.configPath = path.join(this.dir, `config-${number}.json`);
    const child = new OwnedChild(this, config);
    this.children.push(child);
    if (!options.expectSetupError) await child.wait('ready');
    return child;
  }

  state() {
    const server = JSON.parse(fs.readFileSync(this.serverPath, 'utf8'));
    const journal = fs.existsSync(this.journalPath)
      ? JSON.parse(fs.readFileSync(this.journalPath, 'utf8'))
      : undefined;
    return { server: server.messages[this.identity.messageId], activeGeneration: server.activeGeneration, journal: journal?.messages[this.identity.messageId], journalId: journal?.journalId };
  }

  providerRequests() { return provider.requests.filter(request => request.scenario === this.name); }

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
      assert(request.rawBody.includes(this.payload.details.marker), 'unique literal marker absent from actual request body');
      assert(request.rawBody.includes(this.payload.content), 'custom content absent from first correlated request');
      assert.equal(request.headers.authorization, 'Bearer fake-not-a-secret');
      if (event.messageType === 'custom') {
        assert.deepEqual(event.details, this.payload.details);
        assert.equal(event.sessionId, this.identity.sessionId);
        assert.equal(event.generation, this.identity.generation);
        assert.equal(event.messageId, this.identity.messageId);
        const state = this.state();
        assert.equal(event.attemptId, state.server.spend.attemptId);
        assert.equal(event.journalId, state.server.spend.journalId);
      } else {
        assert.equal(event.messageType, 'user');
        assert(event.prompt.includes(this.payload.details.marker));
        assert.equal(event.attemptId, undefined);
      }
    }
    if (manual) {
      const users = [...this.tokens.values()].filter(event => event.messageType === 'user');
      assert.equal(users.length, 1);
      assert.equal(requests.filter(request => this.tokens.get(request.token)?.messageType === 'user').length, 1);
      assert.equal(this.piSends, 1, 'manual old-marker turn must not be another bridge send');
    }
  }

  summary(extra = {}) {
    const state = this.state();
    this.correlate(extra);
    const summary = {
      name: this.name,
      result: 'PASS',
      piSends: this.piSends,
      providerRequests: this.providerRequests().length,
      beforeAgentStart: this.tokens.size,
      customStarts: [...this.tokens.values()].filter(event => event.messageType === 'custom').length,
      userStarts: [...this.tokens.values()].filter(event => event.messageType === 'user').length,
      serverStatus: state.server ? (state.server.receipt ? 'observed' : state.server.spend ? 'unknown' : 'admitted') : 'absent',
      activeGeneration: state.activeGeneration,
      ...extra.counts,
    };
    results.push(summary);
    console.log(`PASS ${this.name}: pi=${summary.piSends} provider=${summary.providerRequests} before_agent_start=${summary.beforeAgentStart} server=${summary.serverStatus}`);
    return summary;
  }
}

async function run(name, body) {
  const scenario = new Scenario(name);
  currentScenario = scenario;
  provider.policy = 'respond';
  const timeout = setTimeout(() => {
    for (const child of scenario.children) if (!child.exit) child.child.kill('SIGKILL');
  }, 15000);
  try {
    await body(scenario);
  } finally {
    clearTimeout(timeout);
    for (const request of scenario.providerRequests()) if (request.held) provider.drop(request);
    for (const child of [...scenario.children].reverse()) {
      if (!child.exit) await child.kill();
    }
  }
}

async function recoverUnknown(scenario) {
  const before = scenario.providerRequests().length;
  const sends = scenario.piSends;
  const recovered = await scenario.start('combined', { createServer: false, createJournal: false });
  const delivery = await recovered.wait('delivery-result', message => message.command === 'initial');
  assert.equal(delivery.result.transmitted, false);
  assert.equal(delivery.result.status, 'unknown');
  assert.equal(scenario.providerRequests().length, before);
  assert.equal(scenario.piSends, sends);
  await recovered.stop();
  return recovered;
}

async function redeliverObserved(child, scenario) {
  const firstNewMessage = child.messages.length;
  child.send({ type: 'deliver' });
  const replay = await child.wait('delivery-result', message => message.command === 'redelivery'
    && child.messages.indexOf(message) >= firstNewMessage);
  assert.equal(replay.result.status, 'observed');
  assert.equal(scenario.piSends, 1);
  assert.equal(scenario.providerRequests().length, 1);
}

async function positiveControl() {
  await run('positive-control', async scenario => {
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
    scenario.correlate({ manual: true });
    await child.stop();
    scenario.summary({ manual: true });
  });
}

async function admissionResponseMatrix() {
  for (const phase of ['beforeCommit', 'beforeResponse']) {
    await run(`admission-response-lost-${phase}`, async scenario => {
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
  await run('crash-before-spend-safe-continuation', async scenario => {
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
    await run(name, async scenario => {
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
  await run('whole-sdk-crash-after-transmission-before-receipt', async scenario => {
    provider.policy = 'hold';
    const child = await scenario.start('combined', { pauses: ['receipt:beforeWrite'] });
    const request = await provider.wait(item => item.scenario === scenario.name);
    await child.wait('barrier', message => message.name === 'receipt:beforeWrite');
    assert.equal(scenario.state().journal.receipt, null);
    assert.equal(scenario.state().server.receipt, null);
    assert.equal(scenario.piSends, 1);
    await child.kill();
    provider.drop(request);
    await recoverUnknown(scenario);
    assert.equal(scenario.providerRequests().length, 1);
    scenario.summary();
  });

  await run('http-disconnect-after-body-before-headers', async scenario => {
    provider.policy = 'disconnect';
    const child = await scenario.start();
    await child.wait('receipt-result', message => message.result.status === 'observed');
    await child.wait('agent-settled');
    assert.equal(scenario.providerRequests()[0].response, 'disconnect');
    assert.equal(scenario.providerRequests().length, 1);
    await redeliverObserved(child, scenario);
    await child.stop();
    scenario.summary();
  });

  await run('crash-after-local-receipt-before-post', async scenario => {
    provider.policy = 'hold';
    const child = await scenario.start('combined', { pauses: ['bridge:afterReceiptCommit'] });
    const request = await provider.wait(item => item.scenario === scenario.name);
    await child.wait('barrier', message => message.name === 'bridge:afterReceiptCommit');
    const before = scenario.state();
    assert(before.journal.receipt);
    assert.equal(before.server.receipt, null);
    const attemptId = before.server.spend.attemptId;
    await child.kill();
    provider.drop(request);

    const recovered = await scenario.start('combined', { createServer: false, createJournal: false });
    const replay = await recovered.wait('delivery-result', message => message.command === 'initial');
    assert.equal(replay.result.status, 'observed');
    assert.equal(replay.result.attemptId, attemptId);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    assert.deepEqual(scenario.state().server.receipt, before.journal.receipt);
    await recovered.stop();
    scenario.summary();
  });

  for (const phase of ['beforeCommit', 'beforeResponse']) {
    await run(`receipt-response-lost-${phase}`, async scenario => {
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
  await run('attempt-write-failure-zero-calls', async scenario => {
    const child = await scenario.start('combined', { failJournalWrite: 'attempt' });
    const failure = await child.wait('delivery-error', message => message.command === 'initial');
    assert.equal(failure.code, 'ATTEMPT_WRITE_FAILED');
    assert.equal(scenario.piSends, 0);
    assert.equal(scenario.providerRequests().length, 0);
    assert(scenario.state().server.spend);
    assert.equal(scenario.state().journal.attempt, null);
    const firstNewMessage = child.messages.length;
    child.send({ type: 'deliver' });
    const replay = await child.wait('delivery-result', message => message.command === 'redelivery'
      && child.messages.indexOf(message) >= firstNewMessage);
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    await child.stop();
    scenario.summary();
  });

  await run('local-receipt-write-failure-remains-unknown', async scenario => {
    const child = await scenario.start('combined', { failJournalWrite: 'receipt' });
    const failure = await child.wait('receipt-error');
    assert.equal(failure.code, 'INJECTED_WRITE_FAILURE');
    await child.wait('agent-settled');
    assert.equal(scenario.state().journal.receipt, null);
    assert.equal(scenario.state().server.receipt, null);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    const firstNewMessage = child.messages.length;
    child.send({ type: 'deliver' });
    const replay = await child.wait('delivery-result', message => message.command === 'redelivery'
      && child.messages.indexOf(message) >= firstNewMessage);
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    assert.equal(scenario.piSends, 1);
    await child.stop();
    scenario.summary();
  });
}

async function splitProcessMatrix() {
  await run('pi-crash-after-transmission-bridge-alive', async scenario => {
    provider.policy = 'hold';
    const pi = await scenario.start('pi', { autoDeliver: false });
    const bridge = await scenario.start('bridge', {
      createServer: true,
      createJournal: true,
      autoDeliver: false,
      pauses: ['receipt:beforeWrite'],
    });
    bridge.child.on('message', message => {
      if (message.type === 'pi-transmit' && !pi.exit) pi.send({ type: 'transmit', transmission: message.transmission });
    });
    pi.child.on('message', message => {
      if (message.type === 'observation' && !bridge.exit) bridge.send({ type: 'observation', ...message });
    });
    bridge.send({ type: 'deliver' });
    const request = await provider.wait(item => item.scenario === scenario.name);
    await bridge.wait('barrier', message => message.name === 'receipt:beforeWrite');
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.state().server.receipt, null);
    await pi.kill();
    provider.drop(request);

    const firstNewMessage = bridge.messages.length;
    bridge.send({ type: 'deliver' });
    const replay = await bridge.wait('delivery-result', message => message.command === 'redelivery'
      && bridge.messages.indexOf(message) >= firstNewMessage);
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    await bridge.stop();
    scenario.summary();
  });

  await run('bridge-crash-pi-alive-late-completion-and-restart', async scenario => {
    provider.policy = 'hold';
    const oldPi = await scenario.start('pi', { autoDeliver: false });
    const oldPiReady = oldPi.messages.find(message => message.type === 'ready');
    const bridge = await scenario.start('bridge', {
      createServer: true,
      createJournal: true,
      autoDeliver: false,
      pauses: ['receipt:beforeWrite'],
    });
    bridge.child.on('message', message => {
      if (message.type === 'pi-transmit' && !oldPi.exit) oldPi.send({ type: 'transmit', transmission: message.transmission });
    });
    oldPi.child.on('message', message => {
      if (message.type === 'observation' && !bridge.exit) bridge.send({ type: 'observation', ...message });
    });
    bridge.send({ type: 'deliver' });
    const request = await provider.wait(item => item.scenario === scenario.name);
    await bridge.wait('barrier', message => message.name === 'receipt:beforeWrite');
    const attemptId = scenario.state().server.spend.attemptId;
    await bridge.kill();
    provider.release(request);
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
    const recoveredBridge = await scenario.start('bridge', { createServer: false, createJournal: false });
    const replay = await recoveredBridge.wait('delivery-result', message => message.command === 'initial');
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    assert.equal(scenario.state().server.spend.attemptId, attemptId);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    await recoveredBridge.stop();
    await restartedPi.stop();
    scenario.summary({ counts: { latePiCompletions: 1, postRestartAttempts: 0 } });
  });
}

async function journalRedeliveryMatrix() {
  await run('redelivery-with-missing-local-journal', async scenario => {
    const child = await scenario.start();
    await scenario.finishSuccessful(child);
    await child.stop();
    fs.rmSync(scenario.journalPath);
    const recovery = await scenario.start('combined', {
      createServer: false,
      createJournal: false,
      expectSetupError: true,
    });
    const failure = await recovery.wait('setup-error');
    assert.equal(failure.code, 'ENOENT');
    await recovery.exited.promise;
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    scenario.summary();
  });

  await run('redelivery-with-restored-pre-attempt-copy', async scenario => {
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

  await run('redelivery-with-competing-sender', async scenario => {
    provider.policy = 'hold';
    const original = await scenario.start('combined', { pauses: ['receipt:beforeWrite'] });
    const ready = original.messages.find(message => message.type === 'ready');
    const request = await provider.wait(item => item.scenario === scenario.name);
    await original.wait('barrier', message => message.name === 'receipt:beforeWrite');
    const attemptId = scenario.state().server.spend.attemptId;
    await original.kill();
    provider.drop(request);

    const competitor = await scenario.start('combined', {
      createServer: false,
      createJournal: true,
      journalPath: path.join(scenario.dir, 'competitor-journal.json'),
      sessionFile: ready.sessionFile,
    });
    const replay = await competitor.wait('delivery-result', message => message.command === 'initial');
    assert.equal(replay.result.status, 'unknown');
    assert.equal(replay.result.transmitted, false);
    assert.equal(scenario.state().server.spend.attemptId, attemptId);
    assert.notEqual(scenario.state().server.spend.ownerPid, competitor.child.pid);
    assert.equal(scenario.piSends, 1);
    assert.equal(scenario.providerRequests().length, 1);
    await competitor.stop();
    scenario.summary();
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
  await run('revocation-before-admission', async scenario => {
    const child = await scenario.start('combined', { revokeBeforeDeliver: true });
    const failure = await child.wait('delivery-error', message => message.command === 'initial');
    assert.equal(failure.code, 'REVOKED_GENERATION');
    assert.equal(scenario.state().server, undefined);
    assert.equal(scenario.piSends, 0);
    assert.equal(scenario.providerRequests().length, 0);
    await child.stop();
    scenario.summary();
  });

  await run('revocation-after-admission-before-spend', async scenario => {
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

  await run('revocation-after-spend-before-final-verification', async scenario => {
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

  await run('revocation-after-transmission-historical-receipt', async scenario => {
    provider.policy = 'hold';
    const child = await scenario.start('combined', { pauses: ['receipt:beforeWrite'] });
    const request = await provider.wait(item => item.scenario === scenario.name);
    await child.wait('barrier', message => message.name === 'receipt:beforeWrite');
    child.send({ type: 'revoke' });
    const revoked = await child.wait('revoked');
    const activeGeneration = revoked.activeGeneration;
    child.release('receipt:beforeWrite');
    provider.release(request);
    await scenario.finishSuccessful(child);
    assert.equal(scenario.state().activeGeneration, activeGeneration);
    assert.equal(scenario.state().server.identity.generation, scenario.identity.generation);
    assert(scenario.state().server.receipt);
    await redeliverObserved(child, scenario);
    await child.stop();
    scenario.summary();
  });

  await run('residual-final-check-to-send-revocation-race', async scenario => {
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
  const output = { piVersion: sdk.VERSION, nodeVersion: process.versions.node, scenarios: results };
  fs.writeFileSync(path.join(root, 'result.json'), `${JSON.stringify(output, null, 2)}\n`, { mode: 0o600 });
  console.log(`RESULT ${results.length} SDK scenarios passed; private traces: ${root}`);
} finally {
  for (const child of [...children]) if (!child.exit) await child.kill();
  await provider.close();
}
