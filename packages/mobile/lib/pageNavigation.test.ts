import { describe, expect, it, vi } from "vitest";

vi.mock("expo-router", () => ({ useNavigationContainerRef: vi.fn(), useRouter: vi.fn() }));
vi.mock("react-native", () => ({ Platform: { OS: "ios" } }));

import { isPresentedRoute, presentedRouteDepth } from "./pageNavigation";

describe("opening pages outside sheets", () => {
	it("recognises every sheet and modal the root stack presents", () => {
		for (const name of ["sheets/review-actions", "sheets/conversation-usage", "settings", "spawn", "pair"]) expect(isPresentedRoute(name, "ios")).toBe(true);
		expect(isPresentedRoute("settings/environments", "ios")).toBe(false);
		for (const name of ["(tabs)", "review/[sessionId]", "reviewer/[reviewId]", "shell/[handleId]", "notifications"]) expect(isPresentedRoute(name, "ios")).toBe(false);
	});

	// Android pushes Settings as a page with its own top app bar, so a page
	// opened from it is pushed on top instead of dismissing Settings first.
	it("treats Settings as presented on iOS only", () => {
		expect(isPresentedRoute("settings", "ios")).toBe(true);
		expect(isPresentedRoute("settings", "android")).toBe(false);
		for (const name of ["sheets/review-actions", "spawn", "pair"]) expect(isPresentedRoute(name, "android")).toBe(true);
	});

	it("reads the running platform by default", () => {
		expect(isPresentedRoute("settings")).toBe(true);
		expect(presentedRouteDepth(["(tabs)", "settings", "notifications"])).toBe(2);
	});

	it("dismisses the first presented route and everything pushed into it", () => {
		expect(presentedRouteDepth(["(tabs)", "review/[sessionId]"], "ios")).toBe(0);
		expect(presentedRouteDepth(["(tabs)", "review/[sessionId]", "sheets/review-actions"], "ios")).toBe(1);
		// A page already pushed into a sheet goes with it.
		expect(presentedRouteDepth(["(tabs)", "settings", "notifications", "review/[sessionId]"], "ios")).toBe(3);
		expect(presentedRouteDepth(["(tabs)", "settings", "notifications", "review/[sessionId]"], "android")).toBe(0);
	});
});
