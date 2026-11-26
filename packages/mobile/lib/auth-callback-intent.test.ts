import { beforeEach, describe, expect, it, vi } from "vitest";

const openedBy = vi.hoisted(() => vi.fn());
vi.mock("./telemetry/uiUsageRegistry", async (importOriginal) => ({ ...(await importOriginal<object>()), reportOpenedBy: openedBy }));

import { redirectSystemPath } from "./auth-callback-intent";

beforeEach(() => openedBy.mockClear());

describe("redirectSystemPath", () => {
	it("drops the Android sign-in redirect while the app is running", () => {
		for (const path of [
			"ao-app://callback?code=01M6&state=4ed2",
			"aomobile://callback?code=01M6&state=4ed2",
			"ao-app:///callback?code=01M6",
			"ao-app://callback",
		]) {
			expect(redirectSystemPath({ path, initial: false })).toBeNull();
		}
	});

	it("opens the app normally when the redirect cold-starts it", () => {
		expect(redirectSystemPath({ path: "ao-app://callback?code=01M6&state=4ed2", initial: true })).toBe("/");
	});

	it("leaves every other link untouched", () => {
		for (const path of ["aomobile://session/abc", "aomobile://callbacks", "aomobile://pair?code=x", "/notifications"]) {
			expect(redirectSystemPath({ path, initial: false })).toBe(path);
			expect(redirectSystemPath({ path, initial: true })).toBe(path);
		}
	});

	// A session a link opens counts as opened from a link; a sign-in return opens nothing.
	it("marks a link the app opens, and not a sign-in return", () => {
		redirectSystemPath({ path: "ao-app://callback?code=01M6", initial: true });
		expect(openedBy).not.toHaveBeenCalled();
		redirectSystemPath({ path: "aomobile://session/abc", initial: true });
		expect(openedBy).toHaveBeenCalledWith("deep_link", "abc");
	});

	// expo-router passes the root URL for a plain launch from the home screen.
	it("does not mark a launch from the home screen or a link to another screen", () => {
		for (const path of ["aomobile:///", "aomobile://", "/", "aomobile://pair?code=x", "/notifications"]) {
			redirectSystemPath({ path, initial: true });
		}
		expect(openedBy).not.toHaveBeenCalled();
		redirectSystemPath({ path: "aomobile:///session/abc?hostId=h", initial: false });
		redirectSystemPath({ path: "/session/abc", initial: false });
		expect(openedBy).toHaveBeenCalledTimes(2);
	});

	it("drops a coding-agent sign-in's return link the same way", () => {
		expect(redirectSystemPath({ path: "ao-app://agent-login", initial: false })).toBeNull();
		expect(redirectSystemPath({ path: "ao-app://agent-login", initial: true })).toBe("/");
	});
});


describe("shared artifact intent", () => {
	it("preserves the published link on cold and warm launches", () => {
		const viewer = "https://orchestrator.inc/artifact/abcdefghijklmnopqrstuvwxyz";
		for (const initial of [true, false]) {
			expect(redirectSystemPath({ path: `ao-app://shared-artifact?url=${encodeURIComponent(viewer)}`, initial }))
				.toBe(`/shared-artifact?url=${encodeURIComponent(viewer)}`);
		}
	});
});
