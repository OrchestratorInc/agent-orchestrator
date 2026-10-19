import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, realpath } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createAgent, replayMessages, reasoningUpdates } from './bridge.mjs';

const row = (id = 'user-1', role = 'user', parts = [{ type: 'text', text: 'ordinary task' }]) =>
  ({ id, role, threadId: 'native-1', content: { format: 2, parts } });

async function fixture({ mapEvent } = {}) {
  const workspace = await realpath(await mkdtemp(join(tmpdir(), 'mastra-sdk-contract-')));
  const calls = [];
  const updates = [];
  const threads = new Map();
  const messages = new Map();
  let current;
  let state = { yolo: false, permissionRules: { categories: {}, tools: { ask_user: 'deny' } } };
  let listener;
  let receivedConfig;
  let model = 'native/default';
  const session = {
    identity: { getResourceId: () => 'resource-1' },
    state: { get: () => state },
    thread: {
      getId: () => current,
      getById: async ({ threadId }) => { calls.push(['get', threadId]); return threads.get(threadId); },
      listMessages: async ({ threadId }) => { calls.push(['messages', threadId]); return messages.get(threadId); },
      create: async () => {
        calls.push(['create']); current = 'native-1';
        const thread = { id: current, resourceId: 'resource-1', metadata: {} }; threads.set(current, thread); return thread;
      },
      setSetting: async ({ key, value }) => { threads.get(current).metadata[key] = value; },
      switch: async ({ threadId }) => { calls.push(['switch', threadId]); current = threadId; },
    },
    model: { get: () => model, switch: async id => { model = id; calls.push(['model', id]); } },
    subscribe: callback => { listener = callback; return () => { listener = undefined; }; },
    sendMessage: async ({ content }) => {
      calls.push(['send', content]);
      listener({ type: 'agent_end', reason: 'complete' });
    },
    abort: () => { calls.push(['abort']); listener?.({ type: 'agent_end', reason: 'aborted' }); },
    completeDeferredAbort: () => calls.push(['deferred-abort']),
  };
  const runtime = { session, controller: { listAvailableModels: async () => [{ id: model, hasApiKey: true }] }, cleanup: async () => calls.push(['cleanup']) };
  const agent = createAgent({ connection: { sessionUpdate: async update => updates.push(update) },
    createRuntime: async config => { receivedConfig = config; return runtime; },
    mapEvent: mapEvent ?? ((event, state) => { if (event.type === 'agent_end') state.resolve(event.reason); }),
    workspace, owner: 'ao-owner', hostInstructions: 'private additive instruction',
    storage: { backend: 'libsql', url: 'file:owned-history.db', vectorUrl: 'file:owned-vector.db', isRemote: false } });
  const request = { cwd: workspace, mcpServers: [] };
  return { agent, request, calls, threads, messages, updates, session, runtime,
    emit: event => listener(event),
    config: () => receivedConfig, setState: value => { state = value; } };
}

test('fresh identity and private host context use native SDK APIs, never visible text', async () => {
  const f = await fixture();
  const created = await f.agent.newSession(f.request);
  assert.equal(created.sessionId, 'native-1');
  assert.equal(f.config().hostInstructions, 'private additive instruction');
  assert.equal(f.config().createInitialThread, false);
  assert.equal(f.config().storage.url, 'file:owned-history.db');
  assert.equal(f.config().storage.vectorUrl, 'file:owned-vector.db');
  assert.equal(f.config().inputProcessors, undefined);
  assert.equal(f.config().modes, undefined);
  const result = await f.agent.prompt({ sessionId: created.sessionId, prompt: [{ type: 'text', text: 'ordinary task' }] });
  assert.equal(result.stopReason, 'end_turn');
  assert.deepEqual(f.calls.find(([method]) => method === 'send'), ['send', 'ordinary task']);
  await f.agent.dispose();
  assert.equal(f.calls.filter(([method]) => method === 'cleanup').length, 1);
});

test('exact load validates native owner, workspace and complete replay before switching', async () => {
  const f = await fixture();
  f.threads.set('native-1', { id: 'native-1', resourceId: 'resource-1', metadata: { aoChatOwner: 'ao-owner', aoChatWorkspace: f.request.cwd } });
  f.messages.set('native-1', [row(), row('assistant-1', 'assistant', [{ type: 'text', text: 'native answer' }])]);
  await f.agent.loadSession({ ...f.request, sessionId: 'native-1' });
  assert.equal(f.calls.some(([method]) => method === 'create'), false);
  assert.deepEqual(f.calls.slice(0, 3), [['get', 'native-1'], ['messages', 'native-1'], ['switch', 'native-1']]);
  assert.deepEqual(f.updates.map(({ update }) => update.messageId), ['user-1', 'assistant-1']);
  await f.agent.loadSession({ ...f.request, sessionId: 'native-1' });
  assert.deepEqual(f.updates.slice(0, 2), f.updates.slice(2));
  assert.equal(f.config().hostInstructions, 'private additive instruction');
  await f.agent.dispose();
});

for (const failure of ['missing', 'wrong-owner', 'wrong-workspace', 'wrong-resource', 'corrupt', 'unsettled-tool', 'unknown-part']) {
  test(`load rejects ${failure} without fresh fallback, switch, or partial replay`, async () => {
    const f = await fixture();
    const metadata = { aoChatOwner: 'ao-owner', aoChatWorkspace: f.request.cwd };
    const thread = { id: 'native-1', resourceId: 'resource-1', metadata };
    if (failure === 'wrong-owner') metadata.aoChatOwner = 'other';
    if (failure === 'wrong-workspace') metadata.aoChatWorkspace = '/other';
    if (failure === 'wrong-resource') thread.resourceId = 'other';
    if (failure !== 'missing') f.threads.set(thread.id, thread);
    const history = [row()];
    if (failure === 'corrupt') history.push(row());
    if (failure === 'unsettled-tool') history.push(row('tool', 'assistant', [{ type: 'tool-invocation', toolInvocation: { toolCallId: 'call', toolName: 'shell', state: 'call' } }]));
    if (failure === 'unknown-part') history.push(row('unknown', 'assistant', [{ type: 'image', data: 'x' }]));
    f.messages.set(thread.id, history);
    await assert.rejects(f.agent.loadSession({ ...f.request, sessionId: thread.id }));
    assert.equal(f.calls.some(([method]) => ['create', 'switch', 'send'].includes(method)), false);
    assert.deepEqual(f.updates, []);
    await f.agent.dispose();
  });
}

test('candidate refuses saved bypass and unsupported input/model without an execution', async () => {
  const f = await fixture();
  await f.agent.newSession(f.request);
  await assert.rejects(f.agent.prompt({ sessionId: 'native-1', prompt: [{ type: 'resource', resource: { text: 'hidden' } }] }));
  await assert.rejects(f.agent.unstable_setSessionModel({ sessionId: 'native-1', modelId: 'invented' }));
  f.setState({ yolo: true, permissionRules: {} });
  await assert.rejects(f.agent.prompt({ sessionId: 'native-1', prompt: [{ type: 'text', text: 'task' }] }));
  assert.equal(f.calls.some(([method]) => ['send', 'model'].includes(method)), false);
  await f.agent.dispose();
});

test('native reasoning and completed tool replay preserve native identities and results', () => {
  const replay = replayMessages([row(), row('assistant-1', 'assistant', [
    { type: 'reasoning', reasoning: 'native thought' },
    { type: 'tool-invocation', toolInvocation: { toolCallId: 'native-call', toolName: 'shell', state: 'result', args: { command: 'pwd' }, result: 'native output' } },
  ])], 'native-1');
  assert.equal(replay[1].sessionUpdate, 'agent_thought_chunk');
  assert.equal(replay[1].messageId, 'assistant-1:reasoning');
  assert.equal(replay[2].toolCallId, 'native-call');
  assert.equal(replay[2].rawOutput, 'native output');
});

test('cancel interrupts the actual native turn and permits immediate continuation', async () => {
  const f = await fixture();
  await f.agent.newSession(f.request);
  let started;
  const startedPromise = new Promise(resolve => { started = resolve; });
  const normalSend = f.session.sendMessage;
  f.session.sendMessage = async ({ content }) => { f.calls.push(['foreground', content]); started(); };
  const turn = f.agent.prompt({ sessionId: 'native-1', prompt: [{ type: 'text', text: 'ordinary long task' }] });
  await startedPromise;
  await f.agent.cancel({ sessionId: 'native-1' });
  assert.equal((await turn).stopReason, 'cancelled');
  assert.equal(f.calls.some(([method]) => method === 'abort'), true);
  f.session.sendMessage = normalSend;
  assert.equal((await f.agent.prompt({ sessionId: 'native-1', prompt: [{ type: 'text', text: 'continue' }] })).stopReason, 'end_turn');
  await f.agent.dispose();
});

test('dispose fences delayed native lookup before switch or replay', async () => {
  const f = await fixture();
  let release;
  let entered;
  const enteredPromise = new Promise(resolve => { entered = resolve; });
  const blocked = new Promise(resolve => { release = resolve; });
  f.session.thread.getById = async () => { entered(); await blocked; return { id: 'native-1', resourceId: 'resource-1', metadata: { aoChatOwner: 'ao-owner', aoChatWorkspace: f.request.cwd } }; };
  const load = f.agent.loadSession({ ...f.request, sessionId: 'native-1' });
  const rejection = assert.rejects(load, /closed/);
  await enteredPromise;
  const disposal = f.agent.dispose();
  assert.equal(f.calls.some(([method]) => method === 'cleanup'), false);
  release();
  await rejection;
  await disposal;
  assert.equal(f.calls.some(([method]) => method === 'switch'), false);
  assert.deepEqual(f.updates, []);
  assert.equal(f.calls.filter(([method]) => method === 'cleanup').length, 1);
});

test('native error terminal event is a failure even without a separate error event', async () => {
  const f = await fixture();
  await f.agent.newSession(f.request);
  f.session.sendMessage = async () => f.emit({ type: 'agent_end', reason: 'error' });
  await assert.rejects(f.agent.prompt({ sessionId: 'native-1', prompt: [{ type: 'text', text: 'ordinary task' }] }), /failed/);
  await f.agent.dispose();
});

test('a rejected suspension denial does not suppress the owning native abort', async () => {
  let denied = false;
  const f = await fixture({ mapEvent: (event, state) => {
    if (event.type === 'install-denial') state.cancelSuspensions = new Map([['call', async () => { denied = true; throw new Error('denial failed'); }]]);
    if (event.type === 'agent_end') state.resolve(event.reason);
  } });
  await f.agent.newSession(f.request);
  let started;
  const running = new Promise(resolve => { started = resolve; });
  f.session.sendMessage = async () => { started(); };
  const turn = f.agent.prompt({ sessionId: 'native-1', prompt: [{ type: 'text', text: 'ordinary task' }] });
  await running;
  f.emit({ type: 'install-denial' });
  await f.agent.cancel({ sessionId: 'native-1' });
  assert.equal((await turn).stopReason, 'cancelled');
  assert.equal(denied, true);
  assert.equal(f.calls.some(([method]) => method === 'abort'), true);
  await f.agent.dispose();
});

test('native error accompanying cancellation remains cancelled', async () => {
  const f = await fixture({ mapEvent: (event, state) => {
    if (event.type === 'agent_end') { state.error = new Error('native interrupt'); state.resolve('error'); }
  } });
  await f.agent.newSession(f.request);
  let started;
  const running = new Promise(resolve => { started = resolve; });
  f.session.sendMessage = async () => { started(); };
  const turn = f.agent.prompt({ sessionId: 'native-1', prompt: [{ type: 'text', text: 'ordinary task' }] });
  await running;
  await f.agent.cancel({ sessionId: 'native-1' });
  assert.equal((await turn).stopReason, 'cancelled');
  await f.agent.dispose();
});

test('native reasoning delta plus full snapshots emits each suffix once', () => {
  const parts = new Map();
  const start = { type: 'message_start', message: { id: 'native-assistant', role: 'assistant', content: { parts: [{ type: 'reasoning', reasoning: 'first' }] } } };
  const output = reasoningUpdates(start, 'native-assistant', parts);
  output.push(...reasoningUpdates({ type: 'message_update', id: 'native-assistant', event: { type: 'reasoning-delta', index: 0, delta: ' next' } }, 'native-assistant', parts));
  output.push(...reasoningUpdates({ type: 'message_update', id: 'native-assistant', event: { type: 'part', index: 0, part: { type: 'reasoning', reasoning: 'first next' } } }, 'native-assistant', parts));
  assert.equal(output.map(update => update.content.text).join(''), 'first next');
  assert.equal(output.every(update => update.messageId === 'native-assistant:reasoning'), true);
  assert.deepEqual(reasoningUpdates(start, 'unrelated', parts), []);
  assert.throws(() => reasoningUpdates({ type: 'message_update', id: 'native-assistant', event: { type: 'part', index: 0, part: { type: 'reasoning', reasoning: 'rewritten' } } }, 'native-assistant', parts), /prefix/);
});
