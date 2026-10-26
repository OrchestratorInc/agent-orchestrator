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

	// Each board tab is a route group with a stack of its own: the stack gives the
	// tab's page a native header, the group keeps the page's URL.
	it("gives each board tab a native large-title header", () => {
		for (const [group, screen] of [["(workers)", "index"], ["(projects)", "projects"], ["(prs)", "prs"]]) {
			expect(source(`../app/(tabs)/${group}/_layout.tsx`)).toContain(`<TabStack screen="${screen}" title={boardTitle("${group}")} />`);
		}
		expect(source("./TabStack.tsx")).toContain("<Stack screenOptions={{ ...stackScreenOptions(t), ...largeTitleScreenOptions() }}>");
	});

	// The header row each page used to draw (a safe-area spacer, a title and a bell
	// with a hand-drawn dot) is the native bar now, with its actions as bar items.
	it("leaves the tab roots' titles and actions to the native header", () => {
		for (const page of ["../app/(tabs)/(workers)/index.tsx", "../app/(tabs)/(projects)/projects.tsx", "../app/(tabs)/(prs)/prs.tsx"]) {
			const text = source(page);
			expect(text).not.toContain("ScreenHeader");
			expect(text).not.toContain("HeaderIconButton");
			expect(text).not.toContain("insets.top");
			expect(text).toContain("<BoardHeaderItems unread={");
			expect(text).toContain('contentInsetAdjustmentBehavior="automatic"');
		}
		const items = source("./BoardHeaderItems.tsx");
		expect(items).toContain('<Stack.Toolbar placement="right">');
		expect(items).toContain('icon={toolbarIcon("settings")}');
		expect(items).toContain('icon={toolbarIcon("notifications")}');
		expect(items).toContain("<Stack.Toolbar.Badge>");
	});

	// The session used to render a spinner until the push animation finished, so
	// the page assembled itself after it had already landed.
	it("renders a session's content while it is still sliding in", () => {
		expect(session).not.toContain("Preparing conversation");
		expect(session).not.toContain("deferRouteContent");
	});

	// The title is native header state, so it is set on the first commit. Deferring
	// it with the trailing control made iOS animate the header in from the top.
	it("sets the session header title without waiting for the control swap", () => {
		const titleEffect = session.indexOf("headerTitle: () => (");
		const gatedControl = session.indexOf("headerRightReady\n\t\t\t\t? glassHeaderControl");
		expect(titleEffect).toBeGreaterThan(-1);
		expect(gatedControl).toBeGreaterThan(-1);
		expect(gatedControl).toBeGreaterThan(titleEffect);
	});
});
