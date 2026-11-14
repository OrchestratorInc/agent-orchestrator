// agent-orchestrator: managed Strands Node preload
//
// Strands CLI 0.2.x patches Ink with synchronous module hooks when Node has
// module.registerHooks (Node 22.15+). Loading any external plugin also
// registers asynchronous hooks, and the synchronous hook then receives no
// source, so every plugin fails with ERR_INVALID_RETURN_PROPERTY_VALUE.
// Hiding registerHooks makes Strands use its documented module.register
// fallback, which composes with the plugin loaders. Consume the launch-only
// flag before Strands starts tools: inherited NODE_OPTIONS must leave project
// Node children and subsequent loader workers with their native module API.
import { createRequire, syncBuiltinESMExports } from "node:module";

const enabled = process.env.AO_STRANDS_NODE_HOOK_WORKAROUND === "1";
delete process.env.AO_STRANDS_NODE_HOOK_WORKAROUND;
// Drop this preload from NODE_OPTIONS too, so project Node children keep their
// own options; a Node without --import would otherwise refuse to start.
const ownPreload = /(?:^|\s+)--import\s+"file:[^"]*\/agents\/strands\/ao-node-hooks\.mjs"(?=\s|$)/g;
if (process.env.NODE_OPTIONS) {
  const rest = process.env.NODE_OPTIONS.replace(ownPreload, "").trim();
  if (rest) process.env.NODE_OPTIONS = rest;
  else delete process.env.NODE_OPTIONS;
}
const nodeModule = createRequire(import.meta.url)("node:module");
if (enabled && typeof nodeModule.registerHooks === "function") {
  nodeModule.registerHooks = undefined;
  syncBuiltinESMExports();
}
