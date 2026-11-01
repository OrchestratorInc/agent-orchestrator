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

// The title, back button and Clear all are the native header's: Settings'
// sheet bar on iOS, a top app bar on Android, the system back on both.
describe("notifications header", () => {
	const page = source("../app/notifications/index.tsx");

	// A large title in the sheet left the page blank beneath it.
	it("takes an inline title from its own stack instead of drawing a header row", () => {
		expect(source("../app/notifications/_layout.tsx")).toContain('<Stack screenOptions={{ ...stackScreenOptions(t), ...(SYSTEM_GLASS_HEADER ? { headerTransparent: true, headerStyle: undefined } : null) }}>');
		expect(source("../app/notifications/_layout.tsx")).not.toContain("largeTitleScreenOptions");
		expect(source("../app/notifications/_layout.tsx")).toContain('<Stack.Screen name="index" options={{ title: "Notifications" }} />');
		expect(page).not.toContain("ScreenHeader");
		expect(page).not.toContain("HeaderIconButton");
		expect(page).not.toContain("insets.top");
	});

	// Flattened away, the root leaves the list a direct child of the sheet, which
	// react-native-screens resizes wrongly: the page showed blank under its bar.
	it("keeps its root view so the sheet lays the list out", () => {
		expect(page).toContain("<View style={styles.screen} collapsable={false}>");
	});

	// The clear bar blurs whatever scrolls under it, so the page's first view is
	// the one inset for it.
	it("keeps the list first under the bar", () => {
		expect(page).toContain('contentInsetAdjustmentBehavior="automatic"');
		expect(page).toMatch(/\{headerItems\}[\s\S]*?\*\/\}\s*<SectionList/);
	});

	// A stand-in scroll view swapped for the list while the sheet came up left
	// iOS 26's floating bar over the sheet taking every touch: the close button
	// and the rows stopped responding.
	it("loads inside the list rather than in a scroll view standing in for it", () => {
		expect(page).not.toContain("TabPageScroll");
		expect(page).toMatch(/ListEmptyComponent=\{\s*loading \? \(\s*<View style=\{styles\.center\}>\s*<ActivityIndicator/);
	});

	// A pushed page on both platforms: the bar's back button leaves it, with
	// no close item of its own, and Clear all takes the trailing end.
	it("leaves by the back button and clears all from the trailing end", () => {
		expect(page).not.toContain("Close notifications");
		expect(page).toContain('<Stack.Toolbar placement="right">');
		expect(page).toContain('<Stack.Toolbar.Button icon={toolbarIcon("delete")} accessibilityLabel="Clear all" onPress={() => confirmClearAllRef.current()} />');
		expect(page).toContain("[clearable]);");
	});
});

// The page is built from the shared pieces rather than local copies of them.
describe("notifications body", () => {
	const page = source("../app/notifications/index.tsx");

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

	// Pull Requests is a sheet of its own on iOS: pushed from this one, it would
	// be presented inside it.
	it("closes the sheet before opening Pull Requests", () => {
		expect(page).toContain('openPage({ pathname: "/prs", params: { hostId: notification.hostId } })');
		expect(page).not.toMatch(/router\.(dismissTo|navigate|push)\(\{ pathname: "\/prs"/);
	});
});
