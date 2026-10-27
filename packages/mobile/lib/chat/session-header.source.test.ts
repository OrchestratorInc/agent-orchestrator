import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

// The conversation-actions control used to be a bare Pressable next to a glass
// back button. It read as a different, brighter control in the same row — the
// reason this is pinned is that "the header looks unlike the home page" is a
// regression you only catch on device.
describe("session header controls", () => {
	const session = source("./ChatSessionScreen.tsx");

	it("renders conversation actions through the shared glass header button", () => {
		expect(session).toContain('import { NativeHeaderButton } from "../native-header-button";');
		expect(session).toContain('icon="more" label="Conversation actions"');
	});

	it("uses the PR restore control beside the shared actions button", () => {
		const start = session.indexOf('glassHeaderControl("right", (');
		expect(start).toBeGreaterThan(-1);
		const actions = session.slice(start, start + 900);
		expect(actions).toContain('testID="header-pullRequest"');
		expect(actions).toContain('accessibilityLabel="Show PR card"');
		expect(actions).toContain('<NativeHeaderButton icon="more" label="Conversation actions"');
	});

	// Files are reached from the actions menu and the composer's changes pill;
	// the header keeps only the PR restore control and the actions button.
	it("keeps session files out of the header", () => {
		const start = session.indexOf('glassHeaderControl("right", (');
		const end = session.indexOf("glassHeaderControl(\"right\"),", start);
		expect(session.slice(start, end)).not.toContain("file-diff");
		expect(session).toContain("onFiles: filesNavigation.openChanges");
	});

	it("gives every platform an icon for the more control", () => {
		expect(source("../native-header-button.tsx")).toContain('"more"');
		expect(source("../native-header-button.ios.tsx")).toContain('icon === "more"');
		expect(source("../native-header-button.android.tsx")).toContain('more: "more-horizontal"');
	});
});
