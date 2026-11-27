import { create } from "zustand";

/** The model last picked for each agent, in any chat: agent id → model id. */
type LastModels = Record<string, string>;

const STORAGE_KEY = "ao.chat.lastModels";

function readStored(): LastModels {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? "{}");
		if (saved && typeof saved === "object" && !Array.isArray(saved)) {
			return Object.fromEntries(Object.entries(saved).filter(([, model]) => typeof model === "string" && model));
		}
	} catch {}
	return {};
}

const useStore = create<{ models: LastModels }>(() => ({ models: readStored() }));

/** Remembers the model just picked for an agent, so its picker lists it first next time. */
export function rememberLastModel(harness: string, model: string) {
	if (!harness || !model || useStore.getState().models[harness] === model) return;
	const models = { ...useStore.getState().models, [harness]: model };
	useStore.setState({ models });
	try {
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify(models));
	} catch {
		// A full or blocked store only forgets the pick across restarts.
	}
}

/** The model last picked for each agent. */
export function useLastModels(): LastModels {
	return useStore((state) => state.models);
}

/** Forgets every remembered model (tests start from none). */
export function forgetLastModels() {
	useStore.setState({ models: {} });
	try {
		window.localStorage.removeItem(STORAGE_KEY);
	} catch {}
}
