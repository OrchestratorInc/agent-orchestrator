import { afterEach, describe, expect, it, vi } from "vitest";
import {
	applyUpdate,
	dismissUpdateScreen,
	getUpdateScreen,
	RESTART_HOLD_MS,
	showUpdateReady,
	subscribeUpdateScreen,
} from "./updateScreenState";

const reset = () => applyUpdate({ reloadAsync: async () => Promise.reject(new Error("reset")) }, 0, async () => {}).catch(() => {});

afterEach(async () => {
	await reset();
});

describe("update screen", () => {
	it("starts hidden, offers an update, and goes away on Not now", () => {
		expect(getUpdateScreen()).toEqual({ visible: false });
		const listener = vi.fn();
		const unsubscribe = subscribeUpdateScreen(listener);
		showUpdateReady();
		expect(getUpdateScreen()).toEqual({ visible: true, phase: "ready" });
		dismissUpdateScreen();
		expect(getUpdateScreen()).toEqual({ visible: false });
		expect(listener).toHaveBeenCalledTimes(2);
		unsubscribe();
		showUpdateReady();
		expect(listener).toHaveBeenCalledTimes(2);
	});

	it("never reloads on its own: offering an update leaves the app running", () => {
		const reloadAsync = vi.fn(async () => {});
		showUpdateReady();
		dismissUpdateScreen();
		expect(reloadAsync).not.toHaveBeenCalled();
	});

	it("shows the restarting screen for the hold before reloading", async () => {
		const events: string[] = [];
		const sleep = vi.fn(async (ms: number) => {
			events.push(`hold ${ms} while ${JSON.stringify(getUpdateScreen())}`);
		});
		showUpdateReady();
		await applyUpdate({ reloadAsync: async () => void events.push("reload") }, RESTART_HOLD_MS, sleep);
		expect(events).toEqual([`hold ${RESTART_HOLD_MS} while {"visible":true,"phase":"restarting"}`, "reload"]);
	});

	it("does not let a late offer or Not now interrupt a restart under way", async () => {
		let release: () => void = () => {};
		const held = new Promise<void>((resolve) => (release = resolve));
		const restart = applyUpdate({ reloadAsync: async () => {} }, 0, () => held);
		showUpdateReady();
		dismissUpdateScreen();
		expect(getUpdateScreen()).toEqual({ visible: true, phase: "restarting" });
		release();
		await restart;
	});

	it("takes the screen down and rethrows when the reload fails", async () => {
		const failure = new Error("reload failed");
		await expect(
			applyUpdate({ reloadAsync: async () => Promise.reject(failure) }, 0, async () => {}),
		).rejects.toBe(failure);
		expect(getUpdateScreen()).toEqual({ visible: false });
	});
});
