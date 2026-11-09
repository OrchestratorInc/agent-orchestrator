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

	// The tab roots' titles and actions are the native bar's, each item named
	// for screen readers and opening its page the same way.
	it("leaves the tab roots' titles and actions to the native header", () => {
		const items = source("./BoardHeaderItems.tsx");
		expect(items).toContain('accessibilityLabel="Pull Requests" onPress={() => router.navigate("/prs")}');
		expect(items).toContain('accessibilityLabel="Settings" onPress={() => router.navigate("/settings")} />');
		expect(items).toContain('onPress={() => router.navigate("/notifications")}');
		expect(items).not.toContain("router.push(");
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
		expect(session).not.toContain("resetHeaderRightForSwap");
	});
});
