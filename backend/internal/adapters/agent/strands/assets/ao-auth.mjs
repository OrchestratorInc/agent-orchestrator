// Read-only bridge to the installed Strands setup wizard's provider discovery.
// No authored agent, model module, plugin or CLI setup is loaded by this probe.
import { readFile, realpath } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

async function installedRoot(binary) {
  const target = await realpath(binary);
  // Unix npm bin symlink, then Windows npm .cmd shim beside node_modules.
  for (const root of [resolve(dirname(target), ".."), join(dirname(target), "node_modules", "@strands-agents", "cli")]) {
    try {
      const manifest = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
      if (manifest.name === "@strands-agents/cli" && manifest.version === "0.2.0") return root;
    } catch {}
  }
  throw new Error("unsupported native package");
}

async function probe(root) {
  let config = {};
  try {
    config = JSON.parse(await readFile(join(homedir(), ".strands", "cli", "config.json"), "utf8"));
  } catch (error) {
    if (error.code !== "ENOENT") return "unknown";
  }
  if (!config || typeof config !== "object" || Array.isArray(config)) return "unknown";
  if (config.agentProject || config.profile?.modelModule) return "unknown";
  const model = config.profile?.model;
  const provider = typeof model === "string" && model.includes("/") ? model.split("/")[0] : "bedrock";
  let fallback = "unknown";
  try {
    const {CliConfigStore} = await import(pathToFileURL(join(root,"dist","src","tui","config.js")).href);
    // memory(permissions, settings, setup) normalizes through native APIs
    // without load()'s credential cleanup writes. The native normalizer ignores
    // legacy credential strings in providers.environment, just as the CLI does.
    // Pass only the safe model string, never modules or consumer plugins.
    const store = CliConfigStore.memory({}, {}, {
      profile: typeof model === "string" ? {model} : {},
      providerEnvironment: config.providers?.environment ?? {},
    });
    const environment = store.providerEnvironment();
    const key = {anthropic:"ANTHROPIC_API_KEY",openai:"OPENAI_API_KEY",google:"GEMINI_API_KEY",litellm:"LITELLM_API_KEY",bedrock:"AWS_BEARER_TOKEN_BEDROCK","bedrock-mantle":"AWS_BEARER_TOKEN_BEDROCK"}[provider];
    const configured = key && environment[key]?.value?.trim()
      || provider.startsWith("bedrock") && environment.AWS_ACCESS_KEY_ID?.value && environment.AWS_SECRET_ACCESS_KEY?.value;
    fallback = configured ? "configured" : "unknown";
    // Native Bedrock discovery can substitute static catalogs; local/custom
    // routes can expose unauthenticated inventories. Neither proves auth.
    if (!["anthropic","openai","google"].includes(provider) || !configured) return fallback;
    // A custom inference endpoint is not verified by a first-party model list.
    if (provider === "openai" && (environment.OPENAI_BASE_URL?.value || process.env.OPENAI_BASE_URL)
        || provider === "anthropic" && (environment.ANTHROPIC_BASE_URL?.value || process.env.ANTHROPIC_BASE_URL)) return fallback;
    const {discoverProviderModels} = await import(pathToFileURL(join(root,"dist","src","tui","provider","discovery.js")).href);
    const result = await discoverProviderModels(provider, environment);
    if (result?.available === false && result.credentialRejected === true) return "unauthorized";
    if (result?.available === true && Array.isArray(result.models)
        && result.models.some(model => typeof model?.id === "string" && model.id.trim())) return "authorized";
  } catch {}
  return fallback;
}

try {
  const status = await probe(await installedRoot(process.argv[1]));
  process.stdout.write(JSON.stringify({status}));
} catch {
  process.stdout.write(JSON.stringify({status:"unknown"}));
  process.exitCode = 1;
}
