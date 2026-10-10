// Opt-in real SDK/CLI boundary tests; no provider calls or credentials.
// STRANDS_NODE_MODULES=/absolute/pinned/node_modules node --test this-file
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";

const modules = process.env.STRANDS_NODE_MODULES;
test("native cancellation reaps the shell and prevents a late write", { skip: !modules, timeout: 15000 }, async () => {
  const root = resolve(modules, "@strands-agents");
  const { Agent, Model } = await import(pathToFileURL(join(root, "sdk/dist/src/index.node.js")));
  const { defineHarnessAgentConfig, harnessAgentOptionsFromConfig } =
    await import(pathToFileURL(join(root, "harness/dist/src/index.js")));
  const { WorkspaceSandbox } =
    await import(pathToFileURL(join(root, "cli/dist/src/tui/workspace/sandbox.js")));
  const workspace = await mkdtemp(join(tmpdir(), "strands-cancel-"));
  try {
    const options = await harnessAgentOptionsFromConfig(defineHarnessAgentConfig({
      plugins: [{kind: "plugin", module: new URL("../assets/ao-activity.mjs", import.meta.url).pathname, export: "shellGuard"}, {kind: "plugin", module: new URL("../assets/ao-activity.mjs", import.meta.url).pathname}],
    }));
    const agent = new Agent({ model: new class extends Model {}, sandbox: new WorkspaceSandbox(workspace),
      tools: [(await import(pathToFileURL(join(root, "sdk/dist/src/vended-tools/shell/make-shell.js")))).makeShell()], plugins: options.plugins, printer: false });
    await agent.initialize();
    const shell = agent.toolRegistry.get("shell");
    assert.ok(shell, "AO plugin must supply a cancellation-aware native shell");
    const controller = new AbortController();
    const command = "printf '%s' $$ > owned-pid; sleep 3; printf late > forbidden";
    const context = { agent, cancelSignal: controller.signal, invocationState: {},
      toolUse: { name: "shell", toolUseId: "cancel-boundary", input: { command } } };
    const run = (async () => {
      const stream = shell.stream(context);
      for (;;) { const next = await stream.next(); if (next.done) return next.value; }
    })();
    let pid;
    const started = Date.now();
    while (!pid && Date.now() - started < 2000) {
      pid = await readFile(join(workspace, "owned-pid"), "utf8").catch(() => null);
      if (!pid) await delay(10);
    }
    assert.ok(pid, "shell must actually start before cancellation");
    const cancelled = Date.now();
    controller.abort();
    const result = await run;
    assert.ok(Date.now() - cancelled < 2500, "bounded cancellation must settle before the delayed write");
    assert.equal(result.status, "error");
    assert.throws(() => process.kill(Number(pid), 0), {code: "ESRCH"}, "owned shell must be reaped");
    await delay(Math.max(0, 3300 - (Date.now() - started)));
    assert.equal(await readFile(join(workspace, "forbidden"), "utf8").catch(() => null), null);
  } finally {
    await rm(workspace, {recursive: true, force: true});
  }
});


test("public replacement preserves shell selection and rejects user replacement", { skip: !modules }, async () => {
  const root = resolve(modules, "@strands-agents");
  const { Agent, Model, tool } = await import(pathToFileURL(join(root, "sdk/dist/src/index.node.js")));
  const { makeShell } = await import(pathToFileURL(join(root, "sdk/dist/src/vended-tools/shell/make-shell.js")));
  const { harnessAgentOptionsFromConfig } = await import(pathToFileURL(join(root, "harness/dist/src/index.js")));
  const options = await harnessAgentOptionsFromConfig({ plugins: [
    {kind: "plugin", module: new URL("../assets/ao-activity.mjs", import.meta.url).pathname, export: "shellGuard"},
    {kind: "plugin", module: new URL("../assets/ao-activity.mjs", import.meta.url).pathname},
  ] });
  const make = (tools, plugins = options.plugins) => new Agent({model: new class extends Model {}, tools, plugins, printer: false});
  const original = makeShell();
  const configTool = tool({name:"strands_config", description:"native configuration", callback: () => "reloaded"});
  const parent = make([original, configTool]);
  await parent.initialize();
  assert.equal(parent.toolRegistry.get("strands_config"), undefined, "model-driven native reload must be unavailable");
  assert.notEqual(parent.toolRegistry.get("shell"), original);
  assert.deepEqual(parent.toolRegistry.get("shell").toolSpec, original.toolSpec);
  const child = make([]);
  await child.initialize();
  assert.equal(child.toolRegistry.get("shell"), undefined, "narrowed child must not acquire shell");
  const custom = tool({name: "shell", description: "custom", callback: () => "custom"});
  const replacing = {name: "custom-replacement", initAgent(agent) { agent.toolRegistry.addOrReplace([custom]); }};
  const changed = make([makeShell()], [options.plugins[0], replacing, options.plugins[1]]);
  await assert.rejects(changed.initialize(), /custom shell replacement/);
  assert.equal(changed.toolRegistry.get("shell"), custom, "custom shell must not be overwritten");
});


test("activity rebinds same native root and fences session/workspace changes", { skip: !modules }, async () => {
  const root = resolve(modules, "@strands-agents");
  const { harnessAgentOptionsFromConfig } = await import(pathToFileURL(join(root, "harness/dist/src/index.js")));
  const options = await harnessAgentOptionsFromConfig({plugins: [
    {kind: "plugin", module: new URL("../assets/ao-activity.mjs", import.meta.url).pathname, export: "shellGuard"},
    {kind: "plugin", module: new URL("../assets/ao-activity.mjs", import.meta.url).pathname},
  ]});
  const saved = [...process.argv];
  process.argv.push("--session-id", "root-identity");
  try {
    const initialize = (id, cwd = process.cwd(), durable = true) => {
      const hooks = [];
      const agent = {sessionId: id, sandbox: {cwd}, sessionManager: durable ? {} : undefined,
        toolRegistry: {get: () => undefined, remove: () => {}}, addHook: (event, callback) => hooks.push([event, callback])};
      options.plugins[0].initAgent(agent);
      options.plugins[1].initAgent(agent);
      return hooks;
    };
    assert.equal(initialize("root-identity").length, 3);
    assert.equal(initialize("root-identity").length, 3, "new same-session root must receive hooks");
    assert.equal(initialize("child", process.cwd(), false).length, 0);
    assert.throws(() => initialize("other-root"), /changing native session/);
    assert.throws(() => initialize("root-identity", "/different"), /changing native session/);
  } finally { process.argv.splice(0, process.argv.length, ...saved); }
});


test("native configuration rebuild cannot reenable background dispatch", { skip: !modules }, async () => {
  const root = resolve(modules, "@strands-agents");
  const { Agent, Model, SessionManager, FileStorage } = await import(pathToFileURL(join(root, "sdk/dist/src/index.node.js")));
  const { harnessAgentOptionsFromConfig } = await import(pathToFileURL(join(root, "harness/dist/src/index.js")));
  const options = await harnessAgentOptionsFromConfig({plugins: [
    {kind: "plugin", module: new URL("../assets/ao-activity.mjs", import.meta.url).pathname, export: "shellGuard"},
    {kind: "plugin", module: new URL("../assets/ao-activity.mjs", import.meta.url).pathname},
  ]});
  const saved=[...process.argv];process.argv.push("--session-id", "managed-root");
  const workspace=await mkdtemp(join(tmpdir(), "strands-background-"));
  try {
    const { WorkspaceSandbox } = await import(pathToFileURL(join(root, "cli/dist/src/tui/workspace/sandbox.js")));
    const sandbox=new WorkspaceSandbox(process.cwd());
    const make = backgroundTasks => new Agent({model:new class extends Model {},plugins:options.plugins,
      sandbox,backgroundTasks,sessionManager:new SessionManager({sessionId:"managed-root",storage:{snapshot:new FileStorage(workspace)}}),printer:false});
    await make(false).initialize();
    await assert.rejects(make(true).initialize(), /background dispatch inside AO/);
    await assert.rejects(make({waitForCompletion:false}).initialize(), /background dispatch inside AO/);
  } finally {process.argv.splice(0,process.argv.length,...saved);await rm(workspace,{recursive:true,force:true});}
});
