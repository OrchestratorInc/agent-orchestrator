import { create } from "zustand";

export const PROJECT_ORDER_STORAGE_KEY = "ao.sidebar.project-order";
const PENDING_CREATED_PROJECTS_STORAGE_KEY = "ao.sidebar.pending-created-projects";

function readStoredOrder(storageKey: string): string[] {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(storageKey) ?? "null");
		if (Array.isArray(saved)) return [...new Set(saved.filter((id): id is string => typeof id === "string"))];
	} catch {}
	return [];
}

type SidebarOrderState = {
	/** Persisted project order shared by creation, sidebar drag, and navigation. */
	projectOrder: string[];
	/** Creation priority retained until a snapshot includes the new project. */
	pendingCreatedProjectIds: string[];
	/** Display order of each mounted project's sessions, keyed by `sessionUiKey(projectId, hostId)`. */
	sessionOrders: Record<string, string[]>;
	setProjectOrder: (order: readonly string[], pendingCreatedProjectIds?: readonly string[]) => void;
	moveProjectToTop: (projectId: string) => void;
	reconcileProjectOrder: (projectIds: readonly string[]) => void;
	setSessionOrder: (projectKey: string, order: readonly string[]) => void;
	clearSessionOrder: (projectKey: string) => void;
};

/**
 * Project order survives renderer restarts. The Sidebar also publishes its
 * mounted session lists here so keyboard navigation follows the visible order.
 */
export const useSidebarOrderStore = create<SidebarOrderState>((set, get) => ({
	projectOrder: readStoredOrder(PROJECT_ORDER_STORAGE_KEY),
	pendingCreatedProjectIds: readStoredOrder(PENDING_CREATED_PROJECTS_STORAGE_KEY),
	sessionOrders: {},
	setProjectOrder: (order, pendingCreatedProjectIds = []) => {
		const current = get();
		const orderChanged = current.projectOrder.length !== order.length || current.projectOrder.some((id, index) => id !== order[index]);
		const pendingChanged = current.pendingCreatedProjectIds.length !== pendingCreatedProjectIds.length || current.pendingCreatedProjectIds.some((id, index) => id !== pendingCreatedProjectIds[index]);
		if (!orderChanged && !pendingChanged) return;
		try {
			if (orderChanged) window.localStorage.setItem(PROJECT_ORDER_STORAGE_KEY, JSON.stringify(order));
			if (pendingChanged) {
				if (pendingCreatedProjectIds.length > 0) {
					window.localStorage.setItem(PENDING_CREATED_PROJECTS_STORAGE_KEY, JSON.stringify(pendingCreatedProjectIds));
				} else {
					window.localStorage.removeItem(PENDING_CREATED_PROJECTS_STORAGE_KEY);
				}
			}
		} catch {}
		set({
			projectOrder: orderChanged ? [...order] : current.projectOrder,
			pendingCreatedProjectIds: pendingChanged ? [...pendingCreatedProjectIds] : current.pendingCreatedProjectIds,
		});
	},
	moveProjectToTop: (projectId) => {
		const { projectOrder, pendingCreatedProjectIds, setProjectOrder } = get();
		setProjectOrder(
			[projectId, ...projectOrder.filter((id) => id !== projectId)],
			[projectId, ...pendingCreatedProjectIds.filter((id) => id !== projectId)],
		);
	},
	reconcileProjectOrder: (projectIds) => {
		const { projectOrder, pendingCreatedProjectIds, setProjectOrder } = get();
		const knownIds = new Set(projectOrder);
		const visibleIds = new Set(projectIds);
		const pendingIds = new Set(pendingCreatedProjectIds);
		const newIds = projectIds.filter((id) => !knownIds.has(id));
		const pendingBoundary = projectOrder.reduce((boundary, id, index) => pendingIds.has(id) ? index + 1 : boundary, 0);
		setProjectOrder(
			[
				...projectOrder.slice(0, pendingBoundary),
				...newIds,
				...projectOrder.slice(pendingBoundary),
			],
			pendingCreatedProjectIds.filter((id) => !visibleIds.has(id)),
		);
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
