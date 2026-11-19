// Test-only provider and independent token oracle for the real Pi 1.5.2 TUI.
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';

const endpoint = new URL(process.env.ARTEL_PTY_PROVIDER_URL);
assert.equal(endpoint.protocol, 'http:');
assert.equal(endpoint.hostname, '127.0.0.1');
assert.equal(endpoint.pathname, '/v1');
assert.equal(endpoint.username, '');
assert.equal(endpoint.password, '');
assert.equal(endpoint.search, '');
assert.equal(endpoint.hash, '');
const eventPath = process.env.ARTEL_PTY_ORACLE_EVENTS;
assert(eventPath?.startsWith('/tmp/artel-pi-delivery-'));

const lookupAttempt = Symbol.for('artel.pi-delivery-02.pty-bridge.lookup-attempt');
let currentToken;

function emit(row) {
  fs.appendFileSync(eventPath, `${JSON.stringify({ ...row, processPid: process.pid })}\n`, { mode: 0o600 });
}

export default function providerFixture(pi) {
  pi.registerProvider('artel-fixture', {
    baseUrl: endpoint.href.replace(/\/$/, ''),
    api: 'openai-completions',
    apiKey: 'fake-not-a-secret',
    models: [{
      id: 'fixture',
      name: 'Deterministic numeric PTY fixture',
      reasoning: false,
      input: ['text'],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 32000,
      maxTokens: 128,
    }],
  });

  pi.on('session_start', (_event, ctx) => {
    assert.equal(ctx.mode, 'tui', 'the probe must exercise the real interactive TUI');
    assert.deepEqual(pi.getActiveTools(), [], 'no built-in, extension or MCP tools may be active');
    emit({
      type: 'session-start',
      sessionId: ctx.sessionManager.getSessionId(),
      sessionFile: ctx.sessionManager.getSessionFile(),
      mode: ctx.mode,
      activeTools: pi.getActiveTools(),
    });
  });

  pi.on('before_agent_start', (event, ctx) => {
    const token = randomUUID();
    currentToken = token;
    const messageId = event.messageType === 'custom' ? event.message.details?.message_id : undefined;
    const attempt = messageId ? globalThis[lookupAttempt]?.(messageId) : undefined;
    emit({
      type: 'token-event',
      token,
      messageType: event.messageType,
      sessionId: ctx.sessionManager.getSessionId(),
      details: event.messageType === 'custom' ? event.message.details : undefined,
      prompt: event.messageType === 'user' ? event.prompt : undefined,
      messageId,
      generation: attempt?.generation,
      attemptId: attempt?.attemptId,
      journalId: attempt?.journalId,
      ownerPid: attempt?.ownerPid,
      ownerInstance: attempt?.ownerInstance,
    });
  });

  pi.on('before_provider_headers', (event, ctx) => {
    if (!currentToken || ctx.model?.provider !== 'artel-fixture' || ctx.model?.id !== 'fixture') {
      emit({ type: 'oracle-error', message: 'missing token or unexpected provider/model' });
      throw new Error('provider oracle refused an unmarked or unexpected request');
    }
    event.headers['x-artel-probe-token'] = currentToken;
    emit({
      type: 'provider-headers',
      token: currentToken,
      provider: ctx.model.provider,
      model: ctx.model.id,
    });
  });

  pi.on('after_provider_response', event => {
    emit({ type: 'provider-response', token: currentToken, status: event.status });
  });

  pi.on('agent_end', () => emit({ type: 'agent-end', token: currentToken }));
  pi.on('agent_settled', (_event, ctx) => emit({
    type: 'agent-settled',
    token: currentToken,
    idle: ctx.isIdle(),
    pending: ctx.hasPendingMessages(),
  }));
}
