import { beforeEach, expect, it, vi } from "vitest";

beforeEach(() => {
	window.localStorage.clear();
	vi.resetModules();
});

it("restores the saved drag and creation order after a renderer restart", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	useSidebarOrderStore.getState().setProjectOrder(["beta", "alpha"]);
	useSidebarOrderStore.getState().moveProjectToTop("zulu");

	vi.resetModules();
	const restarted = await import("./sidebar-order-store");
	expect(restarted.useSidebarOrderStore.getState().projectOrder).toEqual(["zulu", "beta", "alpha"]);
	restarted.useSidebarOrderStore.getState().reconcileProjectOrder(["alpha", "beta", "older-cloud", "zulu"]);
	expect(restarted.useSidebarOrderStore.getState().projectOrder).toEqual(["zulu", "older-cloud", "beta", "alpha"]);
	expect(restarted.useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual([]);
});

it("keeps creation priority through partial snapshots until the new project arrives", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectOrder(["beta", "alpha"]);
	store.moveProjectToTop("zulu");
	store.reconcileProjectOrder(["alpha", "beta", "older-cloud"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["zulu", "older-cloud", "beta", "alpha"]);
	expect(useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual(["zulu"]);
	store.reconcileProjectOrder(["alpha", "beta", "older-cloud", "zulu"]);
	expect(useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual([]);
	store.reconcileProjectOrder(["newer-project", "alpha", "beta", "older-cloud", "zulu"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["newer-project", "zulu", "older-cloud", "beta", "alpha"]);
});

it("lets a manual reorder replace pending creation priority", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectOrder(["beta", "alpha"]);
	store.moveProjectToTop("zulu");
	store.setProjectOrder(["beta", "zulu", "alpha"]);
	store.reconcileProjectOrder(["alpha", "beta", "zulu"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["beta", "zulu", "alpha"]);
	expect(useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual([]);
	expect(window.localStorage.getItem("ao.sidebar.pending-created-projects")).toBeNull();
});

it("keeps creation order when newer projects appear before older pending projects", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectOrder(["alpha"]);
	store.moveProjectToTop("first-created");
	store.moveProjectToTop("second-created");
	store.reconcileProjectOrder(["alpha", "second-created"]);
	store.reconcileProjectOrder(["alpha", "second-created", "older-cloud"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["second-created", "first-created", "older-cloud", "alpha"]);
	store.reconcileProjectOrder(["alpha", "second-created", "first-created", "older-cloud"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["second-created", "first-created", "older-cloud", "alpha"]);
	expect(useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual([]);
});

it.each([
	{ saved: "invalid JSON", expected: [] },
	{ saved: '{"unexpected":true}', expected: [] },
	{ saved: '[4,null,"beta","alpha","beta"]', expected: ["beta", "alpha"] },
])("validates the saved project order at startup: $saved", async ({ saved, expected }) => {
	window.localStorage.setItem("ao.sidebar.project-order", saved);
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(expected);
});
