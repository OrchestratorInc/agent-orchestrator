import { expect, it, vi } from "vitest";

it.each([false, true])("reports unavailable close confirmation without Electron (already closed: %s)", async alreadyClosed => {
	const desktop = Object.getOwnPropertyDescriptor(window, "ao");
	vi.resetModules();
	try {
		Object.defineProperty(window, "ao", { configurable: true, value: undefined });
		const { aoBridge } = await import("./bridge");
		// An unavailable bridge is a bookkeeping failure after successful DELETE,
		// and cannot independently prove shutdown after a 404.
		await expect(aoBridge.sideChats.confirmClose("side", alreadyClosed)).rejects.toThrow(/confirmation is unavailable/);
		expect(await aoBridge.sideChats.getCloseState("session")).toEqual({ pending: [], closedIds: [] });
	} finally {
		if (desktop) Object.defineProperty(window, "ao", desktop);
		else Reflect.deleteProperty(window, "ao");
		vi.resetModules();
	}
});
