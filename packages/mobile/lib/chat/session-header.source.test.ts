import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

function source(relativePath: string): string {
	return readFileSync(fileURLToPath(new URL(relativePath, import.meta.url)), "utf8");
}

// The session's header actions are the platform's own bar items: UIKit bar
// buttons and a pull-down menu on iOS, the top app bar's icon buttons and
// overflow menu on Android. A drawn control in that row reads as a different,
// brighter thing than the system back button beside it, which is a regression
// you only catch on device.
describe("session header controls", () => {
	const session = source("./ChatSessionScreen.tsx");
	const menu = source("./conversationActionsMenu.tsx");

	it("declares the header items as a native toolbar from the page", () => {
		expect(session).toContain('<Stack.Toolbar placement="right">');
		expect(session).toContain("{conversationActionsMenu(menuGroups, runMenuAction)}");
		expect(session).toContain("{headerItems}");
		for (const drawn of ["glassHeaderControl", "NativeHeaderButton", "resetHeaderRightForSwap", "headerRight:"]) {
			expect(session).not.toContain(drawn);
		}
	});

	it("opens the conversation actions as a pull-down menu, not a sheet", () => {
		expect(menu).toContain('<Stack.Toolbar.Menu icon={toolbarIcon("more")} accessibilityLabel="Conversation actions">');
		expect(menu).toContain("destructive={item.destructive}");
		expect(session).not.toContain("conversation-actions");
		// An item runs when it is chosen, not after a sheet has had time to close.
		expect(session).not.toMatch(/setTimeout\([^)]*220/);
	});

	// The menu is rebuilt from what it shows, not on every streamed event, which
	// would re-register the header and could close the menu under the finger.
	it("keeps the menu still while the conversation streams", () => {
		expect(session).toContain("const menuGroups = useMemo(() => menuReady ? conversationMenuGroups({");
		expect(session).toContain("const runMenuAction = useCallback((action: ConversationMenuAction) => menuActionsRef.current[action](), []);");
		expect(session).toContain("const headerItems = useMemo(() => menuGroups ? (");
		// The conversation refreshes itself after every streamed event; only a
		// refresh asked for from the menu shows in it.
		expect(session).toContain("const menuRefreshing = manualRefreshing;");
	});

	// The PR lives in the composer's chips, so the header holds the actions menu alone.
	it("keeps the PR out of the header", () => {
		expect(session).not.toContain("Show PR card");
		expect(session).not.toContain("PULL_REQUEST_ICON");
		expect(readFileSync(new URL("./ChatComposer.tsx", import.meta.url), "utf8")).toContain("<PRChipContent pr={activeReviewPR} summary={reviewPRSummary} />");
	});

	// Files are reached from the actions menu and the composer's changes pill;
	// the header keeps only the actions menu.
	it("keeps session files out of the header", () => {
		const start = session.indexOf('<Stack.Toolbar placement="right">');
		const end = session.indexOf("</Stack.Toolbar>", start);
		expect(session.slice(start, end)).not.toContain("file-diff");
		expect(session).toContain("files: filesNavigation.openChanges");
	});

	// Pull Requests is a page of its own, opened over the session so back
	// returns to it.
	it("opens the Pull Requests page over the session for several PRs", () => {
		expect(session).toContain('else openPage("/prs");');
		expect(session).not.toContain("/(tabs)/prs");
		expect(session).not.toMatch(/dismissTo\(\{\s*pathname: "\/prs"/);
	});

	it("goes back down to the board from the session route's empty states", () => {
		const route = source("../../app/session/[id].tsx");
		expect(route).toContain('const openBoard = useCallback(() => router.dismissTo("/"), [router]);');
		expect(route).not.toContain('router.navigate("/")');
		expect(route).toContain('<Button title="Scan pairing code" icon="qrScanner"');
	});
});
