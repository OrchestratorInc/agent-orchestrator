import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { notificationIdentity } from "./notificationView";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

describe("notifications host routing", () => {
	it("keeps same-id actions on different machines independently addressable", () => {
		expect(notificationIdentity("host-a", "same-id")).toBe("host-a:same-id");
		expect(notificationIdentity("host-b", "same-id")).toBe("host-b:same-id");
		expect(notificationIdentity("host-a", "same-id")).not.toBe(notificationIdentity("host-b", "same-id"));
	});
});

// The title, back button and Clear all are the native header's: a large title
// on iOS, a top app bar on Android, the system back on both.
describe("notifications header", () => {
	const page = source("../app/notifications.tsx");

	it("takes a large title from the root stack instead of drawing a header row", () => {
		expect(source("../app/_layout.tsx")).toContain('<Stack.Screen name="notifications" options={{ title: "Notifications", ...largeTitleScreenOptions() }} />');
		expect(page).not.toContain("ScreenHeader");
		expect(page).not.toContain("HeaderIconButton");
		expect(page).not.toContain("insets.top");
		expect(page).not.toContain("backOr(");
	});

	// The large title collapses with the page's first view.
	it("keeps the list, or a scroll view in its place, first under the bar", () => {
		expect(page).toContain('contentInsetAdjustmentBehavior="automatic"');
		expect(page).toMatch(/\{headerItems\}[\s\S]*?\{loading \? \(\s*<TabPageScroll>/);
	});

	it("clears all from a trailing header item", () => {
		expect(page).toContain('<Stack.Toolbar.Button icon={toolbarIcon("delete")} accessibilityLabel="Clear all" onPress={() => confirmClearAllRef.current()} />');
		expect(page).toContain("[clearable]);");
	});
});

// The page is built from the shared pieces rather than local copies of them.
describe("notifications body", () => {
	const page = source("../app/notifications.tsx");

	it("says what went wrong through the shared, announced toast", () => {
		expect(page).toContain("<ToastHost toast={toast} />");
		expect(page).not.toContain('pointerEvents="none"');
	});

	it("draws its dividers at the shared width and heads groups with the shared header", () => {
		expect(page).toContain("borderBottomWidth: rowDividerWidth");
		expect(page).toContain("<ListSectionHeader label={section.title} count={section.data.length} />");
		expect(page).not.toMatch(/hairlineWidth|NotificationSectionHeader|Feather/);
	});

	// EmptyState picks the lone action's style per platform: prominent on iOS,
	// tonal on Android. Naming a variant would override that on both.
	it("leaves the offline retry to each platform's empty-state default", () => {
		expect(page).toContain('action={<Button title="Retry" icon="refresh-cw" onPress={() => void load("refresh")} />}');
	});

	// From a pushed page, navigate would put a second board on the stack.
	it("returns to the PRs tab by popping to the board", () => {
		expect(page).toContain('router.dismissTo({ pathname: "/prs", params: { hostId: notification.hostId } })');
		expect(page).not.toContain('router.navigate({ pathname: "/prs"');
	});
});
