// AO preloads this into pi-acp and its daemon (BUN_OPTIONS=--preload). pi-acp's
// bundled Pi predates the models providers serve now; this hands the models a
// newer Pi lists in <agent dir>/models-store.json to it as custom models
// (written to AO_PI_MODELS_JSON) and hides built-ins of a provider the store
// no longer lists. Pi's own files are only read; any failure changes nothing.
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

const out = process.env.AO_PI_MODELS_JSON;
const entry = process.env.AO_PI_ACP_ENTRY;
// Only pi-acp and its daemon take the preload. The commands Pi runs for the
// agent get the user's own BUN_OPTIONS back, and a bun inheriting it anyway
// (an older daemon's child) skips it.
const own = process.argv[1] && entry && (() => {
	try {
		return realpathSync(process.argv[1]) === entry;
	} catch {
		return false;
	}
})();
if (own && process.argv.includes("--daemon")) {
	if (process.env.AO_PI_BUN_OPTIONS) process.env.BUN_OPTIONS = process.env.AO_PI_BUN_OPTIONS;
	else delete process.env.BUN_OPTIONS;
	for (const name of ["AO_PI_BUN_OPTIONS", "AO_PI_ACP_ENTRY", "AO_PI_MODELS_JSON"]) delete process.env[name];
}
const warn = (error) => console.error("ao: pi-acp models preload:", error instanceof Error ? error.message : error);

// Only the fields the old runtime's models.json schema knows: a file it
// rejects is ignored whole, the user's own custom models with it.
const thinkingLevels = ["off", "minimal", "low", "medium", "high", "xhigh"];
function definition(model) {
	const kept = { id: model.id };
	for (const key of ["name", "api", "baseUrl", "reasoning", "contextWindow", "maxTokens"]) {
		if (model[key] !== undefined) kept[key] = model[key];
	}
	if (Array.isArray(model.input)) kept.input = model.input.filter((kind) => kind === "text" || kind === "image");
	const costKeys = ["input", "output", "cacheRead", "cacheWrite"];
	if (model.cost && costKeys.every((key) => typeof model.cost[key] === "number")) {
		kept.cost = Object.fromEntries(costKeys.map((key) => [key, model.cost[key]]));
	}
	if (model.thinkingLevelMap && typeof model.thinkingLevelMap === "object") {
		kept.thinkingLevelMap = Object.fromEntries(
			Object.entries(model.thinkingLevelMap).filter(([level]) => thinkingLevels.includes(level)),
		);
	}
	return kept;
}

// listed holds, per provider in the store, the model ids it serves now.
const listed = new Map();

function mergedModelsPath(modelsJsonPath) {
	const store = join(dirname(modelsJsonPath), "models-store.json");
	if (!existsSync(store)) return undefined;
	const current = JSON.parse(readFileSync(store, "utf-8"));
	// A models.json Pi cannot read is left for Pi to report.
	const config = existsSync(modelsJsonPath) ? JSON.parse(readFileSync(modelsJsonPath, "utf-8")) : { providers: {} };
	config.providers ??= {};
	for (const [provider, entry] of Object.entries(current)) {
		const models = Array.isArray(entry?.models) ? entry.models.filter((model) => model?.id) : [];
		if (models.length === 0) continue;
		const declared = config.providers[provider] ?? {};
		const own = Array.isArray(declared.models) ? declared.models : [];
		const ids = new Set(own.map((model) => model.id));
		declared.models = [...own, ...models.filter((model) => !ids.has(model.id)).map(definition)];
		config.providers[provider] = declared;
		listed.set(provider, new Set(declared.models.map((model) => model.id)));
	}
	mkdirSync(dirname(out), { recursive: true });
	// It holds the user's provider keys.
	writeFileSync(out, JSON.stringify(config), { mode: 0o600 });
	return out;
}

if (out && own) {
	try {
		const { ModelRegistry, getAgentDir } = await import(Bun.resolveSync("@earendil-works/pi-coding-agent", dirname(entry)));
		if (ModelRegistry && typeof ModelRegistry.create === "function" && typeof getAgentDir === "function") {
			const create = ModelRegistry.create;
			ModelRegistry.create = function (authStorage, modelsJsonPath = join(getAgentDir(), "models.json")) {
				let path = modelsJsonPath;
				try {
					path = mergedModelsPath(modelsJsonPath) ?? modelsJsonPath;
				} catch (error) {
					warn(error);
				}
				return create.call(this, authStorage, path);
			};
			for (const name of ["getAll", "getAvailable"]) {
				const original = ModelRegistry.prototype[name];
				if (typeof original !== "function") continue;
				ModelRegistry.prototype[name] = function (...args) {
					const models = original.apply(this, args);
					if (!Array.isArray(models) || listed.size === 0) return models;
					return models.filter((model) => !listed.has(model.provider) || listed.get(model.provider).has(model.id));
				};
			}
		}
	} catch (error) {
		warn(error);
	}
}
