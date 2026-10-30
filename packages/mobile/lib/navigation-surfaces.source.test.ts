import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

const layout = source("../app/_layout.tsx");
const session = source("./chat/ChatSessionScreen.tsx");

describe("navigation surfaces", () => {
	// The navigator paints two backgrounds of its own, both visible only while a
	// page moves: the stack container (the seam between the outgoing and incoming
	// screens) and the content view under a screen. Its defaults are white, which
	// is what put a white edge around every transition.
	it("paints the navigator's own surfaces from the palette", () => {
		expect(layout).toContain("NavigationThemeProvider");
		expect(layout).toContain("background: t.bgBase");
		expect(layout).toContain("card: t.bgSurface");
	});

	// Left to native-stack's default, Android runs the system's own push
	// transition instead of a slide picked for it.
	it("pushes sub-pages with the system transition", () => {
		expect(layout).toContain("<Stack screenOptions={stackScreenOptions(t)}>");
		expect(source("./stackScreenOptions.ts")).not.toMatch(/animation:/);
	});

	// The board's destinations are the platform's tab bar. The push drawer it
	// replaced ran its drag on the JS thread and left the page behind it reachable
	// by screen readers.
	it("navigates the board with native tabs, not a drawer", () => {
		const tabs = source("../app/(tabs)/_layout.tsx");
		expect(tabs).toContain('import { NativeTabs } from "expo-router/unstable-native-tabs";');
		expect(tabs).not.toContain("tabBar={() => null}");
		expect(tabs).not.toMatch(/Sidebar|Drawer|PanResponder/);
	});

	// Each board tab is a route group with a stack of its own: the stack gives the
	// tab's page a native header, the group keeps the page's URL.
	it("gives each board tab a native large-title header", () => {
		for (const [group, screen] of [["(workers)", "index"], ["(projects)", "projects"]]) {
			expect(source(`../app/(tabs)/${group}/_layout.tsx`)).toContain(`<TabStack screen="${screen}" title={boardTitle("${group}")} />`);
		}
		expect(source("./TabStack.tsx")).toContain("<Stack screenOptions={{ ...stackScreenOptions(t), ...largeTitleScreenOptions() }}>");
	});

	// The header row each page used to draw (a safe-area spacer, a title and a bell
	// with a hand-drawn dot) is the native bar now, with its actions as bar items.
	it("leaves the tab roots' titles and actions to the native header", () => {
		for (const page of ["../app/(tabs)/(workers)/index.tsx", "../app/(tabs)/(projects)/projects.tsx", "../app/prs/index.tsx"]) {
			const text = source(page);
			expect(text).not.toContain("ScreenHeader");
			expect(text).not.toContain("HeaderIconButton");
			expect(text).not.toContain("insets.top");
			expect(text).toContain('contentInsetAdjustmentBehavior="automatic"');
			// Pull Requests is a sheet over the board, so it closes from its bar
			// rather than carrying the board's own items.
			if (page.endsWith("prs/index.tsx")) expect(text).not.toContain("BoardHeaderItems");
			else expect(text).toContain("<BoardHeaderItems unread={");
		}
		const items = source("./BoardHeaderItems.tsx");
		expect(items).toContain('<Stack.Toolbar placement="right">');
		expect(items).toContain("icon={SETTINGS_ICON} hidesSharedBackground");
		expect(items).toContain("icon={NOTIFICATIONS_ICON}");
		expect(items).toContain('const PULL_REQUESTS_ICON = Platform.OS === "ios" ? "arrow.triangle.pull" : PULL_REQUEST_GLYPH;');
		expect(items).toContain('<Stack.Toolbar.Button icon={PULL_REQUESTS_ICON} iconRenderingMode="template" hidesSharedBackground accessibilityLabel="Pull Requests" onPress={() => router.push("/prs")} />');
		expect(items).toContain("<Stack.Toolbar.Badge>");
	});

	// The session used to render a spinner until the push animation finished, so
	// the page assembled itself after it had already landed.
	it("renders a session's content while it is still sliding in", () => {
		expect(session).not.toContain("Preparing conversation");
		expect(session).not.toContain("deferRouteContent");
	});

	// The title is native header state, set on the first commit. The trailing
	// items are native bar items too, declared from the page, so nothing waits a
	// frame to be measured and swapped in.
	it("sets the session header title and items natively", () => {
		expect(session).toContain("headerTitle: () => (");
		expect(session).toContain('<Stack.Toolbar placement="right">');
		expect(session).not.toContain("glassHeaderControl");
		expect(session).not.toContain("resetHeaderRightForSwap");
	});
});
