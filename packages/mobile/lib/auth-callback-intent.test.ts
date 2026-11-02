import { describe, expect, it } from "vitest";
import { redirectSystemPath } from "./auth-callback-intent";

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
});
