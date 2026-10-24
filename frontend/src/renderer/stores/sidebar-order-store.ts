import { create } from "zustand";

export const PROJECT_ORDER_STORAGE_KEY = "ao.sidebar.project-order";

function readStoredProjectOrder(): string[] {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(PROJECT_ORDER_STORAGE_KEY) ?? "null");
		if (Array.isArray(saved)) return [...new Set(saved.filter((id): id is string => typeof id === "string"))];
	} catch {}
	return [];
}

type SidebarOrderState = {
	/** Persisted project order shared by creation, sidebar drag, and navigation. */
	projectOrder: string[];
	/** Display order of each mounted project's sessions, keyed by `sessionUiKey(projectId, hostId)`. */
	sessionOrders: Record<string, string[]>;
	setProjectOrder: (order: readonly string[]) => void;
	moveProjectToTop: (projectId: string) => void;
	setSessionOrder: (projectKey: string, order: readonly string[]) => void;
	clearSessionOrder: (projectKey: string) => void;
};

/**
 * Project order survives renderer restarts. The Sidebar also publishes its
 * mounted session lists here so keyboard navigation follows the visible order.
 */
export const useSidebarOrderStore = create<SidebarOrderState>((set, get) => ({
	projectOrder: readStoredProjectOrder(),
	sessionOrders: {},
	setProjectOrder: (order) => {
		try {
			window.localStorage.setItem(PROJECT_ORDER_STORAGE_KEY, JSON.stringify(order));
		} catch {}
		set((state) =>
			state.projectOrder.length === order.length && state.projectOrder.every((id, index) => id === order[index])
				? state
				: { projectOrder: [...order] },
		);
	},
	moveProjectToTop: (projectId) => {
		const { projectOrder, setProjectOrder } = get();
		setProjectOrder([projectId, ...projectOrder.filter((id) => id !== projectId)]);
	},
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
