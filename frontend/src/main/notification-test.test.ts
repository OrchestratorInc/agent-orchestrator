import { describe, expect, it, vi } from "vitest";
import { sendTestNotification, type ProbeNotification } from "./notification-test";

type Outcome = "show" | "failed" | "silent" | "throw";

function fakeToast(outcome: Outcome) {
	const listeners: Record<string, (...args: unknown[]) => void> = {};
	const toast: ProbeNotification = {
		once: (event: string, listener: (...args: unknown[]) => void) => {
			listeners[event] = listener;
		},
		on: (event: string, listener: () => void) => {
			listeners[event] = listener;
		},
		show: () => {
			if (outcome === "throw") throw new Error("boom");
			if (outcome === "show") listeners.show?.();
			if (outcome === "failed") listeners.failed?.({}, "Notifications are not allowed");
		},
	} as ProbeNotification;
	return { toast, listeners };
}

describe("sendTestNotification", () => {
	const copy = { title: "Notifications are on", body: "You will hear from your agents here." };

	it.each<[Outcome, string]>([
		["show", "granted"],
		["failed", "denied"],
		["throw", "denied"],
	])("maps a toast that %s to %s", async (outcome, state) => {
		const { toast } = fakeToast(outcome);
		const create = vi.fn(() => toast);
		await expect(sendTestNotification(copy, { isSupported: () => true, create, onClick: () => undefined })).resolves.toBe(state);
		expect(create).toHaveBeenCalledWith(copy);
	});

	it("reports unknown when the OS neither shows nor rejects the toast in time", async () => {
		const { toast } = fakeToast("silent");
		await expect(
			sendTestNotification(copy, { isSupported: () => true, create: () => toast, onClick: () => undefined, timeoutMs: 5 }),
		).resolves.toBe("unknown");
	});

	it("reports unsupported without creating a toast", async () => {
		const create = vi.fn();
		await expect(sendTestNotification(copy, { isSupported: () => false, create, onClick: () => undefined })).resolves.toBe("unsupported");
		expect(create).not.toHaveBeenCalled();
	});

	it("ignores a request without a title and trims oversized copy", async () => {
		const create = vi.fn(() => fakeToast("show").toast);
		await expect(sendTestNotification({ title: " " }, { isSupported: () => true, create, onClick: () => undefined })).resolves.toBe("unknown");
		expect(create).not.toHaveBeenCalled();
		await sendTestNotification({ title: "x".repeat(500), body: 7 as unknown as string }, { isSupported: () => true, create, onClick: () => undefined });
		expect(create).toHaveBeenLastCalledWith({ title: "x".repeat(200), body: "" });
	});

	it("focuses the app when the test toast is clicked", async () => {
		const { toast, listeners } = fakeToast("show");
		const onClick = vi.fn();
		await sendTestNotification(copy, { isSupported: () => true, create: () => toast, onClick });
		listeners.click?.();
		expect(onClick).toHaveBeenCalledOnce();
	});
});
