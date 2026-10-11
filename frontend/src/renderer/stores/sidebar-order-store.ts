import { create } from "zustand";

export const PROJECT_ORDER_STORAGE_KEY = "ao.sidebar.project-order";

function readStoredProjectOrder(): string[] {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(PROJECT_ORDER_STORAGE_KEY) ?? "null");
		if (Array.isArray(saved)) return saved.filter((id): id is string => typeof id === "string");
	} catch {}
	return [];
}

type SidebarOrderState = {
	/** Drag order of the project list, mirrored from the Sidebar. */
	projectOrder: string[];
	/** Display order of each mounted project's sessions, keyed by `sessionUiKey(projectId, hostId)`. */
	sessionOrders: Record<string, string[]>;
	setProjectOrder: (order: readonly string[]) => void;
	setSessionOrder: (projectKey: string, order: readonly string[]) => void;
	clearSessionOrder: (projectKey: string) => void;
};

/**
 * The Sidebar owns project/session ordering as local drag state. Keyboard
 * navigation lives elsewhere (the shell), so the Sidebar publishes the order it
 * is currently showing here and navigation reads it back.
 */
export const useSidebarOrderStore = create<SidebarOrderState>((set) => ({
	projectOrder: readStoredProjectOrder(),
	sessionOrders: {},
	setProjectOrder: (order) =>
		set((state) =>
			state.projectOrder.length === order.length && state.projectOrder.every((id, index) => id === order[index])
				? state
				: { projectOrder: [...order] },
		),
	setSessionOrder: (projectKey, order) =>
		set((state) => {
			const current = state.sessionOrders[projectKey];
			if (current && current.length === order.length && current.every((id, index) => id === order[index])) return state;
			return { sessionOrders: { ...state.sessionOrders, [projectKey]: [...order] } };
		}),
	clearSessionOrder: (projectKey) =>
		set((state) => {
			if (!(projectKey in state.sessionOrders)) return state;
			const { [projectKey]: _removed, ...rest } = state.sessionOrders;
			return { sessionOrders: rest };
		}),
}));
