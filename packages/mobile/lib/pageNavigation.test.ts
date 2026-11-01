import { describe, expect, it, vi } from "vitest";

vi.mock("expo-router", () => ({ useNavigationContainerRef: vi.fn(), useRouter: vi.fn() }));

import { isPresentedRoute, presentedRouteDepth } from "./pageNavigation";

describe("opening pages outside sheets", () => {
	it("recognises every sheet and modal the root stack presents", () => {
		for (const name of ["sheets/review-actions", "sheets/conversation-usage", "pair"]) expect(isPresentedRoute(name)).toBe(true);
		for (const name of ["(tabs)", "review/[sessionId]", "reviewer/[reviewId]", "shell/[handleId]", "settings/environments"]) expect(isPresentedRoute(name)).toBe(false);
	});

	// Settings, Notifications, Pull Requests and New task are pages pushed over
	// the board on both platforms, so a page opened from one is pushed on top
	// instead of dismissing it first.
	it("treats Settings, Notifications, Pull Requests and New task as pages", () => {
		for (const name of ["settings", "notifications", "prs", "spawn"]) expect(isPresentedRoute(name)).toBe(false);
		expect(presentedRouteDepth(["(tabs)", "settings", "notifications", "review/[sessionId]"])).toBe(0);
	});

	// The rendered-page viewer is a modal, so a review opened from a push tap
	// while it is up replaces it rather than landing inside it.
	it("dismisses the rendered-page viewer before pushing", () => {
		expect(isPresentedRoute("render")).toBe(true);
		expect(presentedRouteDepth(["(tabs)", "session/[id]", "render"])).toBe(1);
	});

	it("dismisses the first presented route and everything pushed into it", () => {
		expect(presentedRouteDepth(["(tabs)", "review/[sessionId]"])).toBe(0);
		expect(presentedRouteDepth(["(tabs)", "review/[sessionId]", "sheets/review-actions"])).toBe(1);
		// A page already pushed into a modal goes with it.
		expect(presentedRouteDepth(["(tabs)", "render", "review/[sessionId]"])).toBe(2);
	});
});
