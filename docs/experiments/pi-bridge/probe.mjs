// Isolated Pi 1.5.2 integration probe. No real LLM, tools or project resources.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import { spawn } from 'node:child_process';
import { pathToFileURL } from 'node:url';
const root = process.env.PROBE_ROOT;
const pkg = process.env.PI_TEST_PACKAGE;
assert(root?.startsWith('/tmp/artel-pi-bridge-'));
process.umask(0o077);
const { createAgentSession, DefaultResourceLoader, ModelRuntime, SessionManager, SettingsManager } =
  await import(pathToFileURL(path.join(pkg, 'dist/index.js')));
const version = JSON.parse(fs.readFileSync(path.join(pkg, 'package.json'), 'utf8')).version;
const sleep = ms => new Promise(r => setTimeout(r, ms));
async function until(fn, label) {
  const deadline = Date.now() + 8000;
  while (!fn()) { assert(Date.now() < deadline, `timeout: ${label}`); await sleep(10); }
}
const checks = [];
function check(name, evidence) { checks.push({ name, evidence }); console.log(`PASS ${name}`); }
function entries(file) {
  return fs.existsSync(file) ? fs.readFileSync(file, 'utf8').trim().split('\n').map(JSON.parse) : [];
}
let holdNext = false;
const requests = [], held = [];
const server = http.createServer(async (req, res) => {
  try {
    assert.equal(req.method, 'POST'); assert.equal(req.url, '/v1/chat/completions');
    let raw = ''; for await (const chunk of req) { raw += chunk; assert(raw.length < 200000); }
    const body = JSON.parse(raw); requests.push(body);
    const reply = () => {
      if (res.destroyed) return;
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      const base = { id: `fixture-${requests.length}`, object: 'chat.completion.chunk', created: 1, model: 'fixture' };
      res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: { role: 'assistant', content: 'FIXTURE_OK' }, finish_reason: null }] })}\n\n`);
      res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] })}\n\n`);
      res.end('data: [DONE]\n\n');
    };
    if (holdNext) { holdNext = false; held.push(reply); } else reply();
  } catch (e) { res.writeHead(500); res.end(String(e)); }
});
await new Promise(r => server.listen(0, '127.0.0.1', r));
const port = server.address().port;
const sessions = [];
async function setup(name, openFile) {
  const dir = path.join(root, name); fs.mkdirSync(dir, { recursive: true });
  const cwd = path.join(dir, 'project'), agentDir = path.join(dir, 'agent');
  fs.mkdirSync(cwd, { recursive: true }); fs.mkdirSync(agentDir, { recursive: true });
  const sm = openFile ? SessionManager.open(openFile, path.join(dir, 'sessions')) : SessionManager.create(cwd, path.join(dir, 'sessions'));
  const state = { name, events: [], errors: [], sm, file: sm.getSessionFile() };
  const settingsManager = SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, packages: [], extensions: [], defaultTools: [] });
  const modelRuntime = await ModelRuntime.create({ authPath: path.join(agentDir, 'auth.json'), modelsPath: path.join(agentDir, 'models.json'), modelsStorePath: path.join(agentDir, 'model-store.json'), allowModelNetwork: false, refreshOnCreate: false });
  modelRuntime.registerProvider('artel-fixture', {
    baseUrl: `http://127.0.0.1:${port}/v1`, apiKey: 'fake-not-a-secret', api: 'openai-completions',
    models: [{ id: 'fixture', name: 'Deterministic fixture', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 32000, maxTokens: 128 }],
  });
  const loader = new DefaultResourceLoader({
    cwd, agentDir, settingsManager, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true,
    agentsFilesOverride: () => ({ agentsFiles: [] }), systemPromptOverride: () => 'Isolated transport fixture. No tools. Reply FIXTURE_OK.',
    appendSystemPromptOverride: () => [],
    extensionFactories: [pi => {
      state.api = pi;
      for (const type of ['session_start', 'before_agent_start', 'agent_start', 'message_start', 'message_end', 'agent_end', 'agent_settled']) {
        pi.on(type, (event, ctx) => {
          state.ctx = ctx;
          state.events.push({ event, idle: ctx.isIdle(), fileExists: fs.existsSync(state.file) });
        });
      }
      pi.on('input', event => {
        state.events.push({ event });
        if (event.text === 'DROP') return { action: 'handled' };
      });
    }],
  });
  await loader.reload();
  assert.equal(loader.getExtensions().errors.length, 0);
  assert.equal(loader.getExtensions().extensions.length, 1);
  assert.equal(loader.getAgentsFiles().agentsFiles.length, 0);
  const { session } = await createAgentSession({ cwd, agentDir, modelRuntime, model: modelRuntime.getModel('artel-fixture', 'fixture'), thinkingLevel: 'off', settingsManager, resourceLoader: loader, sessionManager: sm, tools: [], noTools: true });
  state.session = session; sessions.push(state);
  await session.bindExtensions({ mode: 'json', onError: error => state.errors.push(error) });
  assert.deepEqual(session.getActiveToolNames(), []);
  return state;
}
function settled(s) { return s.events.filter(x => x.event.type === 'agent_settled').length; }
function custom(id) { return { customType: 'artel-probe', content: `Agent-origin consultation; message_id=${id}; from=developer; to=product. Test only, no owner authority.`, display: true, details: { message_id: id, author: 'developer', recipient: 'product', kind: 'consultation', sentinel: 'METADATA_NOT_FOR_MODEL' } }; }
async function sendCustom(s, id) {
  const n = settled(s); const result = s.api.sendMessage(custom(id), { triggerTurn: true });
  await until(() => settled(s) > n, `custom ${id}`);
  assert.equal(s.session.getLastAssistantText(), 'FIXTURE_OK');
  return result;
}
try {
  // Child cases are killed by the parent at exact observable boundaries.
  if (process.argv[2] === '--crash-child') {
    const kind = process.argv[3];
    const s = await setup(`crash-${kind}`);
    if (kind === 'queued-followup') await sendCustom(s, 'warmup-before-crash');
    s.api.appendEntry('receipt-probe', { message_id: kind });
    if (kind === 'append-only') {
      console.log(JSON.stringify({ ready: true, file: s.file, kind, exists: fs.existsSync(s.file) }));
    } else {
      holdNext = true;
      s.api.sendMessage(custom(kind), { triggerTurn: true });
      await until(() => held.length === 1, 'child provider request');
      if (kind === 'queued-followup') {
        s.api.sendUserMessage('QUEUED_ONLY_MARKER', { deliverAs: 'followUp' });
        await until(() => s.ctx.hasPendingMessages(), 'followup queued');
      }
      console.log(JSON.stringify({ ready: true, file: s.file, kind, exists: fs.existsSync(s.file), providerReceived: true, pending: s.ctx.hasPendingMessages() }));
    }
    await new Promise(() => {});
  }
  assert.equal(version, '1.5.2');
  const s = await setup('main');
  s.api.appendEntry('receipt-probe', { id: 'before-conversation' });
  assert(s.sm.getEntries().some(e => e.type === 'custom' && e.customType === 'receipt-probe'));
  assert.equal(fs.existsSync(s.file), false);
  check('appendEntry before conversation stays memory-only', { fileExists: false });

  const r = await sendCustom(s, 'custom-1');
  assert.equal(r, undefined);
  assert.equal(s.events.filter(x => x.event.type === 'input').length, 0);
  assert(s.events.some(x => x.event.type === 'before_agent_start' && x.event.messageType === 'custom' && x.event.message.details.message_id === 'custom-1'));
  assert(s.events.some(x => x.event.type === 'message_end' && x.event.message.role === 'custom' && x.event.message.details.message_id === 'custom-1'));
  assert(entries(s.file).some(e => e.type === 'custom_message' && e.details?.message_id === 'custom-1'));
  const payload = JSON.stringify(requests[0]);
  assert(payload.includes('message_id=custom-1')); assert(!payload.includes('METADATA_NOT_FOR_MODEL'));
  check('idle custom trigger: correlation in events/history; metadata excluded from model payload', { returnValue: 'undefined', requests: requests.length });

  const before = requests.length;
  assert.equal(s.api.sendUserMessage('DROP'), undefined);
  await until(() => s.events.some(x => x.event.type === 'input' && x.event.text === 'DROP'), 'handled input');
  await sleep(100);
  assert.equal(requests.length, before);
  assert(!entries(s.file).some(e => JSON.stringify(e).includes('DROP')));
  check('sendUserMessage can be handled without model run or persisted message', { returnValue: 'undefined', additionalRequests: 0 });

  const started = requests.length;
  holdNext = true;
  s.api.sendUserMessage('BUSY_BASE');
  await until(() => held.length === 1, 'held busy request');
  assert.equal(s.ctx.isIdle(), false);
  assert.equal(s.api.sendUserMessage('REJECT_WHILE_BUSY'), undefined);
  await until(() => s.errors.length === 1, 'asynchronous busy error');
  assert(!entries(s.file).some(e => JSON.stringify(e).includes('REJECT_WHILE_BUSY')));
  const n = settled(s); held.shift()();
  await until(() => settled(s) > n, 'busy run settle');
  assert.equal(requests.length, started + 1);
  check('busy sendUserMessage returns before asynchronous rejection', { error: s.errors[0] });

  // A minimal caller-held inbox: no Pi follow-up while busy; dispatch from settled.
  holdNext = true;
  const n2 = settled(s), rq2 = requests.length;
  s.api.sendUserMessage('WAIT_BASE');
  await until(() => held.length === 1, 'wait base');
  const pending = custom('idle-delivery');
  assert.equal(s.ctx.isIdle(), false);
  assert.equal(s.ctx.hasPendingMessages(), false);
  assert(!entries(s.file).some(e => e.details?.message_id === 'idle-delivery'));
  let delivered = false;
  const off = s.api.on('agent_settled', (_event, ctx) => {
    if (!delivered) { assert(ctx.isIdle()); delivered = true; s.api.sendMessage(pending, { triggerTurn: true }); }
  });
  held.shift()();
  await until(() => settled(s) >= n2 + 2, 'deferred custom after settle'); off();
  assert.equal(requests.length, rq2 + 2);
  assert.equal(entries(s.file).filter(e => e.details?.message_id === 'idle-delivery').length, 1);
  check('caller-held message dispatched from agent_settled once', { requests: 2, note: 'in-memory fixture, NOT durable Artel queue' });

  await sendCustom(s, 'duplicate-id'); await sendCustom(s, 'duplicate-id');
  const duplicateCount = entries(s.file).filter(e => e.details?.message_id === 'duplicate-id').length;
  assert.equal(duplicateCount, 2);
  check('same custom message ID is not deduplicated by Pi', { duplicateCount });

  s.session.dispose();
  const reopened = await setup('reopen', s.file);
  assert(reopened.sm.getEntries().some(e => e.type === 'custom_message' && e.details?.message_id === 'custom-1'));
  assert.equal(reopened.sm.getSessionId(), s.sm.getSessionId());
  check('normal reopen preserves custom details and session identity', { sessionId: reopened.sm.getSessionId() });

  for (const kind of ['append-only', 'custom-inflight', 'queued-followup']) {
    const child = spawn(process.execPath, [import.meta.filename, '--crash-child', kind], { env: process.env, stdio: ['ignore', 'pipe', 'pipe'] });
    let text = '', stderr = '', ready;
    child.stdout.on('data', chunk => {
      text += chunk;
      for (const line of text.split('\n')) {
        try { const obj = JSON.parse(line); if (obj.ready) ready = obj; } catch {}
      }
    });
    child.stderr.on('data', c => { stderr += c; });
    const exit = new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })));
    try { await until(() => ready, `child ${kind}: ${stderr}`); }
    finally { child.kill('SIGKILL'); }
    const outcome = await exit;
    assert.equal(outcome.signal, 'SIGKILL');
    if (kind === 'queued-followup') {
      assert.equal(ready.pending, true);
      assert.equal(fs.existsSync(ready.file), true);
      assert(entries(ready.file).some(e => e.details?.message_id === 'warmup-before-crash'));
      assert(!entries(ready.file).some(e => JSON.stringify(e).includes('QUEUED_ONLY_MARKER')));
      const recovered = SessionManager.open(ready.file, path.dirname(ready.file));
      assert(!recovered.getEntries().some(e => JSON.stringify(e).includes('QUEUED_ONLY_MARKER')));
      check('SIGKILL queued followup: persisted conversation survives but queued input is absent', { ...ready, outcome });
    } else {
      assert.equal(fs.existsSync(ready.file), false);
      check(`SIGKILL ${kind}: no session file at this boundary`, { ...ready, outcome });
    }
  }
  fs.writeFileSync(path.join(root, 'result.json'), JSON.stringify({ version, checks, limitations: ['SDK/json binding, not interactive TUI', 'No fake Artel API/auth or durable receipt journal', 'No reload/fork/compaction', 'No power-loss guarantees', 'No real LLM semantic/task acceptance validation'] }, null, 2));
  console.log(`RESULT ${checks.length} checks passed; ${root}/result.json`);
} finally {
  for (const s of sessions) {
    fs.writeFileSync(path.join(root, `trace-${s.name}.json`), JSON.stringify({ events: s.events, errors: s.errors }, null, 2));
    s.session.dispose();
  }
  fs.writeFileSync(path.join(root, 'provider-requests.json'), JSON.stringify(requests, null, 2));
  server.closeAllConnections(); await new Promise(r => server.close(r));
}
