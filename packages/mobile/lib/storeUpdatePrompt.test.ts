import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ alert: vi.fn(), platform: { OS: "ios" } }));
vi.mock("react-native", () => ({ Alert: { alert: fixtures.alert }, Platform: fixtures.platform }));

import { promptStoreUpdate, storeName } from "./storeUpdatePrompt";

type Button = { text: string; style?: string; isPreferred?: boolean; onPress?: () => void };

function lastAlert() {
	const [title, message, buttons, options] = fixtures.alert.mock.calls.at(-1)!;
	return { title, message, buttons: buttons as Button[], options: options as { cancelable?: boolean; onDismiss?: () => void } };
}

beforeEach(() => {
	fixtures.alert.mockClear();
	fixtures.platform.OS = "ios";
});

describe("the store-update prompt", () => {
	it("names each platform's store", () => {
		expect(storeName("ios")).toBe("App Store");
		expect(storeName("android")).toBe("Play Store");
	});

	it.each([
		["ios", "App Store"],
		["android", "Play Store"],
	])("is the system alert on %s, with Not now as its cancel button", (os, store) => {
		fixtures.platform.OS = os;
		promptStoreUpdate({ version: "2.1.0", storeConfirmed: true, onUpdate: vi.fn() });

		const { title, message, buttons } = lastAlert();
		expect(title).toBe("A newer version of Orchestrator is ready");
		expect(message).toBe(`Version 2.1.0 is on the ${store}. Update the native app for the latest compatibility, fixes, and system integrations.`);
		expect(buttons.map((button) => button.text)).toEqual(["Not now", `Open ${store}`]);
		expect(buttons[0].style).toBe("cancel");
		expect(buttons[1].isPreferred).toBe(true);
	});

	it("says only that a version is available when the store has not confirmed one", () => {
		promptStoreUpdate({ storeConfirmed: false, onUpdate: vi.fn() });
		expect(lastAlert().message).toMatch(/^A newer version is available\. /);
	});

	it("opens the store from its button and snoozes from Not now", () => {
		const onUpdate = vi.fn();
		const onDismiss = vi.fn();
		promptStoreUpdate({ storeConfirmed: true, onUpdate, onDismiss });
		const { buttons } = lastAlert();

		buttons[1].onPress?.();
		expect(onUpdate).toHaveBeenCalledOnce();
		expect(onDismiss).not.toHaveBeenCalled();
		buttons[0].onPress?.();
		expect(onDismiss).toHaveBeenCalledOnce();
	});

	// Back or a tap outside the Android dialog is a "Not now" as well.
	it("counts closing the dialog without a button as Not now", () => {
		const onDismiss = vi.fn();
		promptStoreUpdate({ storeConfirmed: true, onUpdate: vi.fn(), onDismiss });
		const { options } = lastAlert();

		expect(options.cancelable).toBe(true);
		options.onDismiss?.();
		expect(onDismiss).toHaveBeenCalledOnce();
	});
});

// There used to be three: a sheet route, an Android copy of it and an inline
// panel in Settings.
describe("one store-update prompt", () => {
	const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

	it("is raised by the launch check and by Settings alike", () => {
		expect(source("./StoreUpdateManager.tsx")).toContain("promptStoreUpdate({");
		expect(source("./StoreUpdateManager.tsx")).not.toContain("router.push");
		expect(source("../app/settings/index.tsx")).toContain("promptStoreUpdate({");
		expect(source("../app/settings/index.tsx")).not.toContain("InlinePanel");
	});

	it("has no sheet of its own", () => {
		expect(source("../app/_layout.tsx")).not.toContain("sheets/store-update");
		for (const path of ["../app/sheets/store-update.tsx", "./StoreUpdateSheet.tsx", "./StoreUpdateSheet.android.tsx"]) {
			expect(existsSync(fileURLToPath(new URL(path, import.meta.url)))).toBe(false);
		}
	});
});
