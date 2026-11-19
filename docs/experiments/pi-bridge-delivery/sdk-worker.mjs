// Isolated SDK worker for sdk-matrix.mjs. Not a production Artel/Pi bridge.
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { FakeArtel, LocalJournal, TestBridge } from './protocol.mjs';

process.umask(0o077);
const configPath = process.argv[2];
assert(configPath?.startsWith('/'), 'worker requires an absolute private config path');
const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
assert(['combined', 'pi', 'bridge'].includes(config.mode));
assert(config.scenarioRoot.startsWith(`${config.root}/`));
fs.mkdirSync(config.scenarioRoot, { recursive: true, mode: 0o700 });
const tracePath = path.join(config.scenarioRoot, `${config.mode}-${process.pid}-trace.jsonl`);

function emit(message) {
  const row = { ...message, workerPid: process.pid };
  fs.appendFileSync(tracePath, `${JSON.stringify(row)}\n`, { mode: 0o600 });
  if (process.connected) process.send(row);
}

const releases = new Map();
const pauses = new Set(config.pauses ?? []);
const failures = new Set(config.failAt ?? []);
process.on('message', message => {
  if (message?.type !== 'release') return;
  const release = releases.get(message.name);
  if (release) {
    releases.delete(message.name);
    release();
  }
});

async function barrier(name) {
  if (!pauses.has(name)) return;
  emit({ type: 'barrier', name });
  await new Promise(resolve => releases.set(name, resolve));
  emit({ type: 'barrier-released', name });
}

async function serverFault(operation, phase) {
  const name = `${operation}:${phase}`;
  await barrier(name);
  if (failures.delete(name)) throw new Error(`injected ${name} failure`);
}

const serverHooks = {
  beforeCommit: (operation) => serverFault(operation, 'beforeCommit'),
  afterCommit: (operation) => barrier(`${operation}:afterCommit`),
  beforeResponse: (operation) => serverFault(operation, 'beforeResponse'),
};
const bridgeHooks = {
  beforeSpend: () => barrier('bridge:beforeSpend'),
  afterSpendResponse: async () => {
    await barrier('bridge:afterSpendResponse');
    if (config.failJournalWrite === 'attempt') journal.failNextWrite();
  },
  afterAttemptCommit: () => barrier('bridge:afterAttemptCommit'),
  afterFinalVerification: () => barrier('bridge:afterFinalVerification'),
  afterReceiptCommit: () => barrier('bridge:afterReceiptCommit'),
};

let artel;
let journal;
let bridge;
let api;
let session;
let sessionManager;
let piSends = 0;
let currentToken;
const activeAttempts = new Map();
const customMessage = transmission => ({
  customType: 'artel-delivery-fixture',
  content: transmission.payload.content,
  display: true,
  details: transmission.payload.details,
});

function transmitCombined(transmission) {
  if (config.mode === 'combined') {
    assert.equal(transmission.ownerPid, process.pid, 'only the original owner may transmit');
  } else {
    assert.equal(typeof transmission.ownerPid, 'number');
  }
  activeAttempts.set(transmission.messageId, transmission);
  piSends += 1;
  emit({
    type: 'pi-send',
    messageId: transmission.messageId,
    generation: transmission.generation,
    sessionId: transmission.sessionId,
    attemptId: transmission.attemptId,
    marker: transmission.payload.details.marker,
    piSends,
  });
  api.sendMessage(customMessage(transmission), { triggerTurn: true });
}

async function writeObservation(messageId, observation) {
  try {
    await barrier('receipt:beforeWrite');
    if (config.failJournalWrite === 'receipt') journal.failNextWrite();
    const result = await bridge.recordObservation(messageId, observation);
    emit({ type: 'receipt-result', messageId, result });
  } catch (error) {
    emit({ type: 'receipt-error', messageId, code: error.code, message: error.message });
  }
}

async function setupPi() {
  const pkg = process.env.PI_TEST_PACKAGE;
  assert(pkg, 'PI_TEST_PACKAGE is required');
  const sdk = await import(pathToFileURL(path.join(pkg, 'dist/index.js')));
  assert.equal(sdk.VERSION, '1.5.2');
  const cwd = path.join(config.scenarioRoot, 'project');
  const agentDir = path.join(config.scenarioRoot, 'agent');
  const sessionsDir = path.join(config.scenarioRoot, 'sessions');
  fs.mkdirSync(cwd, { recursive: true, mode: 0o700 });
  fs.mkdirSync(agentDir, { recursive: true, mode: 0o700 });
  fs.mkdirSync(sessionsDir, { recursive: true, mode: 0o700 });
  const existingSession = config.sessionFile && fs.existsSync(config.sessionFile);
  sessionManager = existingSession
    ? sdk.SessionManager.open(config.sessionFile, sessionsDir)
    : sdk.SessionManager.create(cwd, sessionsDir, { id: config.identity.sessionId });
  assert.equal(sessionManager.getSessionId(), config.identity.sessionId);

  const settingsManager = sdk.SettingsManager.inMemory({
    defaultProvider: 'artel-fixture',
    defaultModel: 'fixture',
    compaction: { enabled: false },
    retry: { enabled: false },
    packages: [],
    extensions: [],
    defaultTools: [],
  });
  assert.equal(settingsManager.getRetryEnabled(), false);
  const modelRuntime = await sdk.ModelRuntime.create({
    authPath: path.join(agentDir, 'auth.json'),
    modelsPath: path.join(agentDir, 'models.json'),
    modelsStorePath: path.join(agentDir, 'model-store.json'),
    allowModelNetwork: false,
    refreshOnCreate: false,
  });
  modelRuntime.registerProvider('artel-fixture', {
    baseUrl: `http://127.0.0.1:${config.providerPort}/v1`,
    apiKey: 'fake-not-a-secret',
    api: 'openai-completions',
    models: [{
      id: 'fixture',
      name: 'Deterministic numeric fixture',
      reasoning: false,
      input: ['text'],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 32000,
      maxTokens: 128,
    }],
  });
  const loader = new sdk.DefaultResourceLoader({
    cwd,
    agentDir,
    settingsManager,
    noExtensions: true,
    noSkills: true,
    noPromptTemplates: true,
    noContextFiles: true,
    noThemes: true,
    noMcp: true,
    agentsFilesOverride: () => ({ agentsFiles: [] }),
    systemPromptOverride: () => 'Isolated numeric transport fixture. No tools. Reply only 42.',
    appendSystemPromptOverride: () => [],
    extensionFactories: [pi => {
      api = pi;
      pi.on('before_agent_start', (event, ctx) => {
        const token = randomUUID();
        currentToken = token;
        const sessionId = ctx.sessionManager.getSessionId();
        const messageId = event.messageType === 'custom' ? event.message.details?.message_id : undefined;
        const attempt = messageId ? activeAttempts.get(messageId) : undefined;
        emit({
          type: 'token-event',
          token,
          messageType: event.messageType,
          sessionId,
          details: event.messageType === 'custom' ? event.message.details : undefined,
          prompt: event.messageType === 'user' ? event.prompt : undefined,
          messageId,
          generation: attempt?.generation,
          attemptId: attempt?.attemptId,
          journalId: attempt?.journalId,
          ownerPid: attempt?.ownerPid,
        });
        if (event.messageType !== 'custom' || !attempt) return;
        const observation = {
          type: 'before_agent_start',
          messageType: 'custom',
          sessionId,
          details: structuredClone(event.message.details),
        };
        // Do not await the writer in Pi's hook. The sole positive receipt is still
        // the exact custom observation above, never provider or completion events.
        if (config.mode === 'pi') {
          emit({ type: 'observation', messageId, observation });
        } else {
          queueMicrotask(() => { void writeObservation(messageId, observation); });
        }
      });
      pi.on('before_provider_headers', (event, ctx) => {
        if (!currentToken || ctx.model?.provider !== 'artel-fixture' || ctx.model?.id !== 'fixture') {
          emit({ type: 'oracle-error', message: 'missing token or unexpected provider/model' });
          throw new Error('provider oracle refused an unmarked or unexpected request');
        }
        event.headers['x-artel-probe-token'] = currentToken;
        emit({ type: 'provider-headers', token: currentToken, provider: ctx.model.provider, model: ctx.model.id });
      });
      pi.on('after_provider_response', event => emit({ type: 'provider-response', token: currentToken, status: event.status }));
      pi.on('provider_stream_event', event => emit({ type: 'provider-stream', token: currentToken, data: event.data }));
      pi.on('agent_end', () => emit({ type: 'agent-end', token: currentToken }));
      pi.on('agent_settled', (_event, ctx) => emit({
        type: 'agent-settled',
        token: currentToken,
        idle: ctx.isIdle(),
        assistantText: session.getLastAssistantText(),
      }));
    }],
  });
  await loader.reload();
  assert.equal(loader.getExtensions().errors.length, 0);
  assert.equal(loader.getExtensions().extensions.length, 1);
  assert.equal(loader.getAgentsFiles().agentsFiles.length, 0);
  assert.equal(loader.getSkills().skills.length, 0);
  const model = modelRuntime.getModel('artel-fixture', 'fixture');
  assert(model);
  const created = await sdk.createAgentSession({
    cwd,
    agentDir,
    modelRuntime,
    model,
    thinkingLevel: 'off',
    settingsManager,
    resourceLoader: loader,
    sessionManager,
    tools: [],
    noTools: 'all',
  });
  session = created.session;
  await session.bindExtensions({
    mode: 'json',
    onError: error => emit({ type: 'extension-error', message: error.message }),
  });
  assert.deepEqual(session.getActiveToolNames(), []);
}

async function setupBridge() {
  artel = config.createServer
    ? await FakeArtel.create(config.serverPath, {
      sessionId: config.identity.sessionId,
      generation: config.identity.generation,
    }, serverHooks)
    : await FakeArtel.open(config.serverPath, serverHooks);
  journal = config.createJournal
    ? await LocalJournal.create(config.journalPath)
    : await LocalJournal.open(config.journalPath);
  bridge = new TestBridge({
    artel,
    journal,
    transmit: config.mode === 'bridge'
      ? async transmission => emit({ type: 'pi-transmit', transmission })
      : async transmission => transmitCombined(transmission),
    hooks: bridgeHooks,
  });
}

async function deliver(command) {
  try {
    if (config.revokeBeforeDeliver) {
      config.revokeBeforeDeliver = false;
      await artel.revokeGeneration();
    }
    const result = await bridge.deliver(config.identity, config.payload);
    emit({ type: 'delivery-result', command, result, piSends });
  } catch (error) {
    emit({ type: 'delivery-error', command, code: error.code, message: error.message, piSends });
  }
}

async function shutdown() {
  session?.dispose();
  emit({ type: 'shutdown', piSends });
  process.disconnect();
}

process.on('message', message => {
  if (message?.type === 'deliver') void deliver('redelivery');
  if (message?.type === 'manual') {
    assert(api, 'manual turn requires Pi');
    api.sendUserMessage(`MANUAL_OLD_MARKER ${config.payload.details.marker}`);
    emit({ type: 'manual-sent' });
  }
  if (message?.type === 'revoke') {
    assert(artel, 'revocation requires the fake Artel owner');
    void artel.revokeGeneration().then(activeGeneration => emit({ type: 'revoked', activeGeneration }));
  }
  if (message?.type === 'transmit') {
    assert.equal(config.mode, 'pi');
    transmitCombined(message.transmission);
  }
  if (message?.type === 'observation') {
    assert.equal(config.mode, 'bridge');
    queueMicrotask(() => { void writeObservation(message.messageId, message.observation); });
  }
  if (message?.type === 'shutdown') void shutdown();
});

try {
  if (config.mode !== 'bridge') await setupPi();
  if (config.mode !== 'pi') await setupBridge();
  emit({
    type: 'ready',
    mode: config.mode,
    sessionId: sessionManager?.getSessionId() ?? config.identity.sessionId,
    sessionFile: sessionManager?.getSessionFile(),
    journalId: journal?.journalId,
  });
  if (config.autoDeliver && config.mode !== 'pi') queueMicrotask(() => { void deliver('initial'); });
} catch (error) {
  emit({ type: 'setup-error', code: error.code, message: error.message, stack: error.stack });
  process.exitCode = 1;
}
