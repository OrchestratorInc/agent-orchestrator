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
	const buttons = source("../session/sessionHeaderButtons.tsx");
	const route = source("../../app/conversation-actions.tsx");

	it("declares the header items as a native toolbar from the page", () => {
		expect(session).toContain('<Stack.Toolbar placement="right">');
		expect(session).toContain("{headerItems}");
		for (const drawn of ["glassHeaderControl", "NativeHeaderButton", "resetHeaderRightForSwap", "headerRight:"]) {
			expect(session).not.toContain(drawn);
		}
	});

	// The same buttons, in the same order, with the same glyphs, as the
	// terminal's header: the switch to the other interface, the preview, the
	// files, then "⋯".
	it("leads with the terminal's buttons, built by the same function", () => {
		expect(session).toContain("{sessionHeaderButtons({");
		expect(session).toContain('switchTo: "terminal",');
		expect(source("../session/TerminalSessionScreen.tsx")).toContain('switchTo: "chat",');
		const order = ['key="switch"', 'key="preview"', 'key="files"'].map((key) => buttons.lastIndexOf(key));
		expect(order).toEqual([...order].sort((a, b) => a - b));
		expect(buttons).toContain("icon={toolbarIcon(switchTo)}");
		expect(buttons).toContain('icon={toolbarIcon(previewReady && ios ? PREVIEW_READY_GLYPH : "globe")}');
		expect(buttons).toContain('icon={toolbarIcon("fileSearch")}');
		// Both globes read the same detector, so they agree on what is ready.
		expect(session).toContain("const previewReady = useSessionPreview(config, session.id);");
	});

	it("opens the conversation actions as a pushed page, not a menu or a sheet", () => {
		expect(session).toContain('<Stack.Toolbar.Button icon={toolbarIcon("more")} accessibilityLabel="Conversation actions" onPress={openActions} />');
		expect(session).toContain('kind: "conversation-actions"');
		expect(session).not.toContain("Stack.Toolbar.Menu");
		expect(source("./chatSheetRegistry.ts")).toContain('"conversation-actions": "/conversation-actions",');
		expect(source("../../app/_layout.tsx")).toContain('<Stack.Screen name="conversation-actions" options={{ title: "Actions" }} />');
		// Back to the chat first, then the action, with no wait for an animation.
		expect(route).toMatch(/backOr\(router\);\s*actions\.onSelect\(action\);/);
		expect(session).not.toMatch(/setTimeout\([^)]*220/);
	});

	// The page follows what it shows while it is up; the header is rebuilt only
	// when its own buttons change, not on every streamed event.
	it("keeps the header still while the conversation streams", () => {
		expect(session).toContain("const menuGroups = useMemo(() => menuReady ? conversationMenuGroups({");
		expect(session).toContain("const runMenuAction = useCallback((action: ConversationMenuAction) => menuActionsRef.current[action](), []);");
		expect(session).toContain("const headerItems = useMemo(() => menuReady ? (");
		expect(route).toContain("useEffect(() => actions?.subscribeGroups(setGroups), [actions]);");
		// The conversation refreshes itself after every streamed event; only a
		// refresh asked for from the page shows in it.
		expect(session).toContain("const menuRefreshing = manualRefreshing;");
	});

	// The PR lives in the composer's chips, not the header.
	it("keeps the PR out of the header", () => {
		expect(session).not.toContain("Show PR card");
		expect(session).not.toContain("PULL_REQUEST_ICON");
		expect(readFileSync(new URL("./ChatComposer.tsx", import.meta.url), "utf8")).toContain("<PRChipContent pr={activeReviewPR} summary={reviewPRSummary} />");
	});

	it("opens the session's files from the header", () => {
		expect(session).toContain("openFiles: filesNavigation.openChanges,");
		expect(session).toContain("onFiles: () => headerPress.current.openFiles(),");
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
