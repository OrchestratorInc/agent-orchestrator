// Provider-owned candidate bridge over public @mastra/code-sdk APIs. This is
// deliberately separate from stock mastracode --acp, which cannot load history.
import { realpath } from 'node:fs/promises';

export function replayMessages(messages, threadId) {
  if (!Array.isArray(messages) || messages.length === 0) throw new Error('Native history is missing');
  const updates = [];
  const ids = new Set();
  for (const message of messages) {
    if (!message || typeof message.id !== 'string' || !message.id || ids.has(message.id) ||
        (message.threadId && message.threadId !== threadId)) throw new Error('Invalid native message identity');
    ids.add(message.id);
    // Trusted system context remains native context, never a visible task.
    if (message.role === 'system') continue;
    if (!['user', 'assistant'].includes(message.role) || message.content?.format !== 2 ||
        !Array.isArray(message.content.parts) || message.content.experimental_attachments?.length) {
      throw new Error('Unsupported native history content');
    }
    for (const part of message.content.parts) {
      if (part.type === 'step-start') continue;
      if (part.type === 'text' && typeof part.text === 'string') {
        updates.push({ sessionUpdate: message.role === 'user' ? 'user_message_chunk' : 'agent_message_chunk',
          messageId: message.id, content: { type: 'text', text: part.text } });
      } else if (part.type === 'reasoning' && message.role === 'assistant' && typeof part.reasoning === 'string') {
        updates.push({ sessionUpdate: 'agent_thought_chunk', messageId: message.id + ':reasoning',
          content: { type: 'text', text: part.reasoning } });
      } else if (part.type === 'tool-invocation' && message.role === 'assistant') {
        const tool = part.toolInvocation;
        if (!tool || typeof tool.toolCallId !== 'string' || !tool.toolCallId || typeof tool.toolName !== 'string' ||
            !['result', 'output-error', 'output-denied'].includes(tool.state)) throw new Error('Unsettled or invalid native tool history');
        updates.push({ sessionUpdate: 'tool_call', toolCallId: tool.toolCallId, title: tool.toolName,
          status: tool.isError || tool.state !== 'result' ? 'failed' : 'completed',
          rawInput: tool.args, rawOutput: tool.errorText ?? tool.result });
      } else throw new Error('Unsupported native history part');
    }
  }
  if (!updates.some(update => update.sessionUpdate === 'user_message_chunk')) throw new Error('Native user history is missing');
  return updates;
}

// Native reasoning updates contain both deltas and full-part snapshots. Keep
// each native message/part prefix so a final snapshot cannot duplicate deltas.
export function reasoningUpdates(event, activeMessageId, parts) {
  const updates = [];
  const push = (id, index, text, delta) => {
    if (id !== activeMessageId || !Number.isInteger(index) || index < 0 || typeof text !== 'string') return;
    const key = JSON.stringify([id, index]);
    const previous = parts.get(key) ?? '';
    const full = delta ? previous + text : text;
    if (!full.startsWith(previous)) throw new Error('Native reasoning changed an emitted prefix');
    parts.set(key, full);
    if (full.length > previous.length) updates.push({ sessionUpdate: 'agent_thought_chunk', messageId: id + ':reasoning',
      content: { type: 'text', text: full.slice(previous.length) } });
  };
  if (event.type === 'message_start' && event.message?.role === 'assistant') {
    event.message.content?.parts?.forEach((part, index) => { if (part.type === 'reasoning') push(event.message.id, index, part.reasoning, false); });
  } else if (event.type === 'message_update') {
    if (event.event.type === 'reasoning-delta') push(event.id, event.event.index, event.event.delta, true);
    else if (event.event.type === 'part' && event.event.part?.type === 'reasoning') push(event.id, event.event.index, event.event.part.reasoning, false);
  }
  return updates;
}

export function stdioMCPServers(servers, workspace) {
  if (!Array.isArray(servers)) throw new Error('Invalid client MCP servers');
  const result = Object.create(null);
  for (const server of servers) {
    if (!server || typeof server.name !== 'string' || !server.name || Object.hasOwn(result, server.name) ||
        server.type || typeof server.command !== 'string' || !server.command ||
        !Array.isArray(server.args) || server.args.some(arg => typeof arg !== 'string') ||
        !Array.isArray(server.env) || server.env.some(item => typeof item.name !== 'string' || typeof item.value !== 'string')) {
      throw new Error('Only distinct, valid stdio MCP servers are supported');
    }
    result[server.name] = { command: server.command, args: [...server.args],
      env: Object.fromEntries(server.env.map(item => [item.name, item.value])), cwd: workspace };
  }
  return result;
}

// One native runtime per owned AO session, using an isolated native database.
// Dependencies are explicit so CI can exercise this boundary without inference.
export function createAgent({ connection, createRuntime, mapEvent, workspace, owner, hostInstructions, storage }) {
  let runtime;
  let runtimePromise;
  let requestedMCP;
  let sessionId;
  let active;
  let opening = false;
  let disposed = false;
  let disposePromise;
  const pending = new Set();
  const track = operation => {
    // Start the async operation synchronously through its first await. A cancel
    // arriving immediately after prompt must see its reserved native turn.
    let promise;
    try { promise = Promise.resolve(operation()); }
    catch (error) { promise = Promise.reject(error); }
    pending.add(promise);
    promise.then(() => pending.delete(promise), () => pending.delete(promise));
    return promise;
  };
  const assertOpen = () => { if (disposed) throw new Error('Bridge is closed'); };
  const invalid = message => { throw new Error(message); };
  const checkRequest = async request => {
    if (disposed) invalid('Bridge is closed');
    if (await realpath(request.cwd) !== workspace) invalid('Workspace mismatch');
    assertOpen();
    const servers = stdioMCPServers(request.mcpServers ?? [], workspace);
    const signature = JSON.stringify(servers);
    if (requestedMCP && requestedMCP.signature !== signature) invalid('Cannot change client MCP configuration on a live native runtime');
    requestedMCP ??= { signature, servers };
  };
  const getRuntime = async () => {
    runtimePromise ??= createRuntime({ cwd: workspace, createInitialThread: false,
      hostInstructions, storage, mcpServers: requestedMCP.servers, unixSocketPubSub: false, disableEnvFile: true,
      initialState: { projectPath: workspace, yolo: false, permissionRules: { categories: {}, tools: { ask_user: 'deny' } } },
      disabledTools: ['ask_user'] });
    runtime = await runtimePromise;
    if (disposed) invalid('Bridge is closed');
    return runtime;
  };
  const checkSafety = async session => {
    // A persisted bypass choice must never override this candidate's manual
    // approval policy. Refuse it, preserving the user's saved native settings.
    const state = session.state.get();
    if (state.yolo !== false) invalid('Native history enables unqualified automatic approval');
    if (typeof state.projectPath !== 'string' || await realpath(state.projectPath) !== workspace) invalid('Native state belongs to another workspace');
    if ((state.pluginInstructions ?? []).some(text => runtime.managedInstructions?.has(text))) invalid('Native history contains legacy AO TUI instruction context');
    const rules = state.permissionRules;
    if (!rules || Object.entries(rules.categories ?? {}).some(([, value]) => !['ask', 'deny'].includes(value)) ||
        Object.entries(rules.tools ?? {}).some(([, value]) => !['ask', 'deny'].includes(value))) invalid('Native automatic permission rules are unqualified');
  };
  const info = async session => {
    const models = (await runtime.controller.listAvailableModels()).filter(model => model.hasApiKey);
    const current = session.model.get();
    const available = models.map(model => ({ modelId: model.id, name: model.id }));
    if (current && !available.some(model => model.modelId === current)) available.push({ modelId: current, name: current });
    return { ...(current ? { models: { currentModelId: current, availableModels: available } } : {}) };
  };
  const checkSession = id => {
    if (!runtime || !sessionId || id !== sessionId || runtime.session.thread.getId() !== sessionId) invalid('Unknown native session');
    return runtime.session;
  };
  const open = async (request, load) => {
    await checkRequest(request);
    if (opening || active) invalid('Native session is busy');
    if (sessionId && (!load || sessionId !== request.sessionId)) invalid('Bridge already owns a native session');
    opening = true;
    try {
      const { session } = await getRuntime();
      let updates;
      if (load) {
        if (typeof request.sessionId !== 'string' || !request.sessionId) invalid('Exact native session ID is required');
        const thread = await session.thread.getById({ threadId: request.sessionId });
        assertOpen();
        if (!thread || thread.id !== request.sessionId || thread.resourceId !== session.identity.getResourceId() ||
            thread.metadata?.aoChatOwner !== owner || thread.metadata?.aoChatWorkspace !== workspace) invalid('Native history is missing or belongs to another workspace/owner');
        // Validate the entire snapshot before switching or emitting any replay.
        updates = replayMessages(await session.thread.listMessages({ threadId: thread.id }), thread.id);
        assertOpen();
        await session.thread.switch({ threadId: thread.id });
        if (session.thread.getId() !== request.sessionId) invalid('Native restore changed session identity');
      } else {
        if (sessionId) invalid('Fresh creation cannot replace native history');
        const thread = await session.thread.create();
        assertOpen();
        if (!thread?.id || session.thread.getId() !== thread.id) invalid('Native creation returned no bound identity');
        await session.thread.setSetting({ key: 'aoChatOwner', value: owner });
        await session.thread.setSetting({ key: 'aoChatWorkspace', value: workspace });
      }
      assertOpen();
      await checkSafety(session);
      const result = await info(session);
      assertOpen();
      sessionId = session.thread.getId();
      if (updates) for (const update of updates) {
        assertOpen();
        await connection.sessionUpdate({ sessionId, update });
      }
      return load ? result : { sessionId, ...result };
    } finally { opening = false; }
  };
  const cancelActive = async () => {
    if (!active || active.finished || active.cancelled) return;
    const state = active;
    state.cancelled = true;
    for (const cancel of state.cancelSuspensions?.values() ?? []) {
      // Match the first-party ACP path: a failed suspension denial must not
      // prevent the native interrupt of the owning run.
      try { await cancel(); } catch { /* native abort below remains required */ }
    }
    state.cancelSuspensions?.clear();
    if (active !== state) return;
    runtime.session.abort();
    if (state.suspended) {
      runtime.session.stream.detach();
      state.resolve('aborted');
    }
  };
  return {
    async initialize() {
      return { protocolVersion: 1, agentInfo: { name: 'ao-mastra-sdk-candidate', version: '1.11.0' },
        agentCapabilities: { loadSession: true }, authMethods: [] };
    },
    // Native SDK auth remains authoritative. Never run an interactive login.
    async authenticate() { invalid('Configure native Mastra authentication before launching this candidate'); },
    newSession(request) { return track(() => open(request, false)); },
    loadSession(request) { return track(() => open(request, true)); },
    prompt(request) { return track(async () => {
      const session = checkSession(request.sessionId);
      if (disposed || active || opening) invalid('Native session is busy or closed');
      if (!Array.isArray(request.prompt) || request.prompt.some(part => part.type !== 'text' || typeof part.text !== 'string')) invalid('Only text input is supported');
      let resolveTurn;
      const settled = new Promise(resolve => { resolveTurn = resolve; });
      const state = { sessionId, usage: { promptTokens: 0, completionTokens: 0, totalTokens: 0 },
        resolve: reason => { state.finished = true; resolveTurn(reason); } };
      active = state;
      let unsubscribe = () => {};
      try {
        await checkSafety(session);
        if (state.cancelled) return { stopReason: 'cancelled' };
        const reasoning = new Map();
        const updates = new Set();
        const liveConnection = {
          requestPermission: request => connection.requestPermission(request),
          sessionUpdate: notification => {
            if (notification.update.sessionUpdate === 'agent_message_chunk' && state.activeAssistantMessageId) {
              notification = { ...notification, update: { ...notification.update, messageId: state.activeAssistantMessageId } };
            }
            const sent = connection.sessionUpdate(notification);
            updates.add(sent);
            sent.then(() => updates.delete(sent), error => { updates.delete(sent); state.bridgeError ??= error; });
            return sent;
          },
        };
        unsubscribe = session.subscribe(event => {
          try {
            mapEvent(event, state, liveConnection, session);
            for (const update of reasoningUpdates(event, state.activeAssistantMessageId, reasoning)) {
              void liveConnection.sessionUpdate({ sessionId, update });
            }
          } catch (error) { state.bridgeError = error; session.abort(); }
        });
        await session.sendMessage({ content: request.prompt.map(part => part.text).join('\n') });
        const reason = await settled;
        await Promise.all([...updates]);
        if (state.bridgeError && !state.cancelled) throw state.bridgeError;
        if (reason === 'error' && !state.cancelled && !state.stopReason) throw state.error ?? new Error('Native Mastra turn failed');
        return { stopReason: state.cancelled || reason === 'aborted' ? 'cancelled' : state.stopReason ?? 'end_turn' };
      } catch (error) {
        if (state.cancelled) return { stopReason: 'cancelled' };
        throw error;
      } finally { state.finished = true; unsubscribe(); if (active === state) active = undefined; }
    }); },
    async cancel(request) { checkSession(request.sessionId); await cancelActive(); },
    unstable_setSessionModel(request) { return track(async () => {
      assertOpen();
      const session = checkSession(request.sessionId);
      if (active || opening) invalid('Native session is busy');
      opening = true;
      try {
        const { models } = await info(session);
        assertOpen();
        if (!models?.availableModels.some(model => model.modelId === request.modelId)) invalid('Native model was not advertised');
        await session.model.switch(request.modelId);
        return {};
      } finally { opening = false; }
    }); },
    dispose() {
      disposed = true;
      return disposePromise ??= (async () => {
        try { await cancelActive(); }
        finally {
          await Promise.allSettled([...pending]);
          if (runtimePromise) {
            const owned = await runtimePromise;
            await owned.cleanup();
          }
        }
      })();
    },
  };
}
