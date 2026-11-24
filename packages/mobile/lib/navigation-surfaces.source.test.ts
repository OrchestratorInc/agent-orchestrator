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

	// Every stack shares one set of defaults, and with them one push.
	it("pushes sub-pages with the shared stack options", () => {
		expect(layout).toContain("<Stack screenOptions={stackScreenOptions(t)}>");
	});

	// Home is the desktop sidebar's Sessions and Projects; the side bar holds
	// Pull requests, All workers, Notifications and Settings, each opened the
	// same way: navigated to, so a second tap during the push doesn't stack a copy.
	it("opens the app's pages from the side bar's rows", () => {
		const home = source("../app/(tabs)/(workers)/index.tsx");
		expect(home).toContain('return <Sidebar part="home" />;');
		expect(home).toContain("useSidebarEdgeSwipe(useIsFocused());");
		expect(source("../app/_layout.tsx")).toContain('<Sidebar part="drawer" close={close} />');
		const sidebar = source("./sidebar/Sidebar.tsx");
		expect(sidebar).toContain('label="Pull requests" onPress={() => go("/prs")}');
		expect(sidebar).toContain('label="Notifications" onPress={() => go("/notifications")}');
		expect(sidebar).toContain('label="Artifacts" onPress={() => go("/artifacts")}');
		expect(sidebar).toContain('onSettings={() => go("/settings")}');
		expect(sidebar).toContain("router.navigate(href)");
		expect(source("./BoardHeaderItems.tsx")).not.toContain("router.");
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
		expect(session).toContain("headerTitle: () => {");
		expect(session).toContain('<Stack.Toolbar placement="right">');
		// Android floats the "⋯" on a pill of its own, set as the bar's item.
		expect(session).toContain('{menuReady ? <HeaderPillButton icon="more" label="Conversation actions"');
		expect(session).not.toContain("resetHeaderRightForSwap");
	});
});
