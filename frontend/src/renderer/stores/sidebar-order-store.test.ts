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
