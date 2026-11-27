// This process emits only an authored verdict. Provider text, keys and native
// account paths remain inside the native SDK boundary.
import { createRequire } from 'node:module';
import { readFileSync, realpathSync } from 'node:fs';
import { dirname, join, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

for (const method of ['log', 'info', 'warn', 'error', 'debug']) console[method] = () => {};
let status = 'unknown';
let code = 'mastra_sdk_unsupported';
let provider = '';
let httpStatus = 0;
const emit = () => process.stdout.write(JSON.stringify({ status, code, provider, httpStatus }));
function packageRoot(entry) {
  const executable = realpathSync(entry);
  const candidates = [dirname(executable),
    join(dirname(executable), 'node_modules', 'mastracode'),
    join(dirname(executable), '..', 'lib', 'node_modules', 'mastracode')];
  for (const candidate of candidates) {
    let root = resolve(candidate);
    for (;;) {
      let metadata;
      try { metadata = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')); } catch {}
      if (metadata?.name === 'mastracode') {
        if (metadata.version !== '0.45.0') throw new Error('unsupported native version');
        return root;
      }
      const parent = dirname(root);
      if (root === parent) break;
      root = parent;
    }
  }
  throw new Error('native package unavailable');
}
function exportedPackage(req, name, version) {
  const metadataPath = req.resolve(name + '/package.json');
  const metadata = JSON.parse(readFileSync(metadataPath, 'utf8'));
  if (metadata.name !== name || metadata.version !== version) throw new Error('unsupported package');
  const root = dirname(metadataPath);
  const target = subpath => {
    let value = metadata.exports?.[subpath] ?? metadata.exports?.['./*'];
    while (value && typeof value === 'object') value = value.import ?? value.default;
    if (typeof value !== 'string' || !value.startsWith('./')) throw new Error('unsupported export');
    const file = resolve(root, value.replace('*', subpath.slice(2)));
    if (!file.startsWith(root + sep)) throw new Error('invalid export');
    return pathToFileURL(file).href;
  };
  return { target, req: createRequire(metadataPath) };
}
try {
  const native = packageRoot(process.argv[1]);
  process.env.MASTRA_APP_DATA_DIR = process.argv[2];
  const sdk = exportedPackage(createRequire(join(native, 'package.json')), '@mastra/code-sdk', '1.11.0');
  const core = exportedPackage(sdk.req, '@mastra/core', '1.75.0');
  const { AuthStorage } = await import(sdk.target('./auth/storage'));
  const settingsModule = await import(sdk.target('./onboarding/settings'));
  const { PROVIDER_REGISTRY } = await import(core.target('./llm'));
  const storage = new AuthStorage();
  const settings = settingsModule.loadSettings();
  const envNames = {};
  for (const [id, config] of Object.entries(PROVIDER_REGISTRY)) {
    envNames[id] = Array.isArray(config.apiKeyEnvVar) ? config.apiKeyEnvVar[0] : config.apiKeyEnvVar;
  }
  storage.loadStoredApiKeysIntoEnv(envNames);
  const custom = settings.customProviders ?? [];
  const configured = storage.list().some(id => id.startsWith('apikey:')
    ? Boolean(storage.getStoredApiKey(id.slice(7))?.trim())
    : ['oauth', 'api_key'].includes(storage.get(id)?.type)) ||
    Object.values(PROVIDER_REGISTRY).some(config => (Array.isArray(config.apiKeyEnvVar)
      ? config.apiKeyEnvVar : [config.apiKeyEnvVar]).some(name => name && process.env[name]?.trim())) ||
    custom.some(item => item.apiKey?.trim());
  if (!configured) { code = 'credential_missing'; }
  else {
    status = 'configured';
    code = 'mastra_model_unselected';
    // Match native headless/build startup, including active built-in/custom
    // packs and per-pack overrides. Raw modeDefaults can be empty or stale.
    // AO replaces inherited MASTRACODE_MODEL_ID per session.
    code = 'mastra_sdk_unsupported';
    const { listBuiltinModePacks } = await import(sdk.target('./onboarding/packs'));
    const defaults = settingsModule.resolveModelDefaults(settings, listBuiltinModePacks());
    code = 'mastra_model_unselected';
    const modelId = defaults?.build;
    if (typeof modelId === 'string' && modelId.includes('/')) {
      code = 'provider_unsupported';
      const normalized = settingsModule.stripMastraCodeCustomProviderPrefix(modelId, custom);
      const selected = normalized.split('/')[0];
      provider = /^[a-zA-Z0-9._-]{1,64}$/.test(selected) ? selected : '';
      const model = await import(sdk.target('./agents/model'));
      const { generateText } = await import(pathToFileURL(sdk.req.resolve('ai')).href);
      try {
        const result = await generateText({ model: model.resolveModel(modelId), prompt: 'Reply OK.',
          maxOutputTokens: 16, maxRetries: 0, abortSignal: AbortSignal.timeout(15000) });
        // A bounded reasoning-only response still proves provider access.
        // A missing/malformed SDK result does not.
        // The native V2 router can retain V3/V4 provider result fields through
        // AI SDK 6's compatibility wrapper. Read only their documented values.
        const finishReason = typeof result?.finishReason === 'string'
          ? result.finishReason : result?.finishReason?.unified;
        const outputTokens = typeof result?.usage?.outputTokens === 'number'
          ? result.usage.outputTokens : result?.usage?.outputTokens?.total;
        const inference = (typeof result?.text === 'string' && result.text.trim() !== '') ||
          (Number.isFinite(outputTokens) && outputTokens > 0);
        if (typeof result?.response?.modelId === 'string' && result.response.modelId.trim() &&
            result.usage && typeof result.usage === 'object' &&
            ['stop', 'length', 'content-filter', 'tool-calls', 'other'].includes(finishReason) && inference) {
          status = 'authorized'; code = 'provider_accepted';
        }
      } catch (error) {
        // Read only typed status fields, never classify or return raw messages.
        const responseStatus = error?.statusCode ?? error?.cause?.statusCode;
        httpStatus = Number.isInteger(responseStatus) && responseStatus >= 100 && responseStatus <= 599 ? responseStatus : 0;
        const providerCode = error?.data?.error?.code ?? error?.data?.error?.type;
        const quota = ['insufficient_quota', 'quota_exceeded', 'billing_hard_limit_reached', 'insufficient_balance'].includes(providerCode);
        if (quota || httpStatus === 402) code = 'provider_quota_exhausted';
        else if (httpStatus === 401 || httpStatus === 403) {status = 'unauthorized';code = 'provider_rejected';}
        else if (httpStatus === 429) code = 'provider_rate_limited';
        else code = 'provider_unreachable';
      }
    }
  }
} catch { /* Missing/incompatible SDK stays unknown or configured, never signed out. */ }
emit();
