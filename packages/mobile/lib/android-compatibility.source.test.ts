import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

describe("Android native compatibility boundaries", () => {
	// React Native registers its back callback only when both the device and
	// the target SDK are Android 16. Opted in to predictive back, Android 13–15
	// never call onBackPressed(), so Back skips JavaScript, no stack pops, and the
	// app goes to the launcher from any pushed page.
	it("leaves Android Back to React Native rather than opting in to predictive back", () => {
		const android = JSON.parse(source("../app.json")).expo.android;
		expect(android.predictiveBackGestureEnabled).toBe(false);
	});

	it("keeps SwiftUI and percentage native widths out of the cross-platform Spawn route", () => {
		const spawn = source("../app/spawn.tsx");
		expect(spawn).not.toContain("@expo/ui/swift-ui");
		expect(spawn).not.toContain("NativeTextInput");
		// A page on both platforms, so nothing needs a sheet of its own.
		expect(spawn).not.toContain("@expo/ui/community/bottom-sheet");
	});

	// Spawn's choices open on its own page. They used to be a second sheet over
	// Spawn's: two grabbers, and only the top one answered a swipe down.
	it("shows Spawn's choices inside Spawn's own sheet", () => {
		const android = source("./spawn-composer-controls.android.tsx");
		expect(android).not.toMatch(/\bModal\b/);
		expect(android).not.toContain("@expo/ui/community/bottom-sheet");
		expect(android).not.toContain("Picker");
	});

	it("dismisses the Android keyboard before presenting chat sheets", () => {
		const screen = source("./chat/ChatSessionScreen.tsx");
		expect(screen).toContain("dismissKeyboardBeforeSheet");
		expect(screen).toMatch(/const presentSheet = useCallback\(async \(entry: ChatSheetEntry\) => \{\s*await dismissKeyboardBeforeSheet/);
	});

	// With two detents Material only hid the sheet after a drag past half its
	// height, so a normal swipe down snapped back and closing felt random.
	it("pins Android review actions to one 60% detent so a swipe down closes it", () => {
		const layout = source("../app/_layout.tsx");
		const actions = source("./review-actions-sheet.tsx");
		expect(layout).toContain('{ name: "sheets/review-actions", detents: [0.6, 0.95], androidDetents: [0.6] }');
		expect(layout).toContain("sheetInitialDetentIndex: 0");
		expect(actions).toMatch(/<ScrollView[^>]*nestedScrollEnabled/);
	});

	it("does not register the built-in Android sound as a missing custom asset", () => {
		expect(source("./push.ts")).not.toContain('sound: "default"');
	});

	// A full-screen page over the board on Android, with a top app bar and the
	// system back; a sheet on iOS.
	it("presents Settings from the root stack over the board", () => {
		const rootSettingsPath = fileURLToPath(new URL("../app/settings/index.tsx", import.meta.url));
		const nestedSettingsPath = fileURLToPath(new URL("../app/(tabs)/settings.tsx", import.meta.url));
		expect(existsSync(rootSettingsPath)).toBe(true);
		expect(existsSync(nestedSettingsPath)).toBe(false);
	});
});
