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
	const route = source("../../app/conversation-actions.tsx");

	it("opens the conversation actions as a bottom sheet on both platforms, never a menu or a page", () => {
		expect(session).toContain('<Stack.Toolbar.Button icon={toolbarIcon("more")} accessibilityLabel="Conversation actions" onPress={openActions} />');
		expect(session).toContain('kind: "conversation-actions"');
		expect(session).not.toContain("Stack.Toolbar.Menu");
		expect(source("./chatSheetRegistry.ts")).toContain('"conversation-actions": "/conversation-actions",');
		const layout = source("../../app/_layout.tsx");
		expect(layout).toMatch(/name="conversation-actions"\s*options=\{\{\s*presentation: "formSheet",/);
		// iOS opens at full height so Archive is whole; Android keeps one detent so a swipe down closes it.
		expect(layout).toMatch(/name="conversation-actions"[\s\S]*?sheetAllowedDetents: Platform\.OS === "android" \? \[0\.9\] : \[1\],/);
		// No title bar: the actions start under the grabber.
		expect(layout).toMatch(/name="conversation-actions"[\s\S]*?headerShown: false,/);
		expect(route).not.toContain("Stack.Screen");
		// Back to the chat first, then the action, with no wait for an animation.
		expect(route).toMatch(/backOr\(router\);\s*actions\.onSelect\(action\);/);
		expect(session).not.toMatch(/setTimeout\([^)]*220/);
	});

	// The PR lives in the composer's chips, not the header.
	it("keeps the PR out of the header", () => {
		expect(session).not.toContain("Show PR card");
		expect(session).not.toContain("PULL_REQUEST_ICON");
	});

	it("opens the session's files from the composer's changes strip", () => {
		expect(session).toContain("changes={changeSummary?.files ? { ...changeSummary, onOpen: filesNavigation.openChanges } : undefined}");
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
	});
});
