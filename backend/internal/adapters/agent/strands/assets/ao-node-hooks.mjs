// agent-orchestrator: managed Strands Node preload
//
// Strands CLI 0.2.x patches Ink with synchronous module hooks when Node has
// module.registerHooks (Node 22.15+). Loading any external plugin also
// registers asynchronous hooks, and the synchronous hook then receives no
// source, so every plugin fails with ERR_INVALID_RETURN_PROPERTY_VALUE.
// Hiding registerHooks makes Strands use its documented module.register
// fallback, which composes with the plugin loaders.
import { createRequire, syncBuiltinESMExports } from "node:module";

const nodeModule = createRequire(import.meta.url)("node:module");
if (typeof nodeModule.registerHooks === "function") {
  nodeModule.registerHooks = undefined;
  syncBuiltinESMExports();
}
