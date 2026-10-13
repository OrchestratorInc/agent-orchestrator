import type { AgentModelCatalog } from "../hooks/useAgentModelsQuery";

type Model = AgentModelCatalog["models"][number];

/** Group only explicit provider metadata; model namespaces aren't providers. */
export function groupModelProviders(models: Model[], configuredProviders: string[] = [], reportedProviders: string[] = []) {
	const buckets = new Map<string, Model[]>();
	for (const provider of [...reportedProviders, ...configuredProviders]) {
		if (provider.trim()) buckets.set(provider.trim(), []);
	}
	for (const model of models) {
		const provider = model.provider?.trim() ?? "";
		const bucket = buckets.get(provider) ?? [];
		bucket.push(model);
		buckets.set(provider, bucket);
	}
	return [...buckets].map(([id, models]) => ({ id, models, configured: configuredProviders.includes(id) }))
		.sort((a, b) => Number(!a.id) - Number(!b.id) || Number(b.configured) - Number(a.configured) || a.id.localeCompare(b.id));
}
