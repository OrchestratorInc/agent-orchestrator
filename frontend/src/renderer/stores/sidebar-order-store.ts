import { create } from "zustand";

export const PROJECT_ORDER_STORAGE_KEY = "ao.sidebar.project-order";
const PENDING_CREATED_PROJECTS_STORAGE_KEY = "ao.sidebar.pending-created-projects";
export const PINNED_PROJECTS_STORAGE_KEY = "ao.sidebar.pinned-projects";

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
	/** Projects pinned above the rest of the list. Order within comes from `projectOrder`. */
	pinnedProjectIds: string[];
	/** Display order of each mounted project's sessions, keyed by `sessionUiKey(projectId, hostId)`. */
	sessionOrders: Record<string, string[]>;
	setProjectOrder: (order: readonly string[], pendingCreatedProjectIds?: readonly string[]) => void;
	moveProjectToTop: (projectId: string) => void;
	setProjectPinned: (projectId: string, pinned: boolean) => void;
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
	pinnedProjectIds: readStoredOrder(PINNED_PROJECTS_STORAGE_KEY),
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
	setProjectPinned: (projectId, pinned) => {
		const { pinnedProjectIds, projectOrder, setProjectOrder } = get();
		if (pinnedProjectIds.includes(projectId) === pinned) return;
		const next = pinned ? [...pinnedProjectIds, projectId] : pinnedProjectIds.filter((id) => id !== projectId);
		try {
			window.localStorage.setItem(PINNED_PROJECTS_STORAGE_KEY, JSON.stringify(next));
		} catch {}
		set({ pinnedProjectIds: next });
		// Store the order as displayed (pinned group first), then put the
		// project at the group boundary: a new pin lands at the bottom of the
		// pinned group, an unpinned project at the top of the rest.
		const others = new Set(next.filter((id) => id !== projectId));
		const rest = projectOrder.filter((id) => id !== projectId);
		const pinnedRest = rest.filter((id) => others.has(id));
		const unpinnedRest = rest.filter((id) => !others.has(id));
		setProjectOrder([...pinnedRest, projectId, ...unpinnedRest], get().pendingCreatedProjectIds);
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
