import { describe, expect, it } from "vitest";
import { redirectSystemPath } from "../app/+native-intent";

describe("native sign-in callback routing", () => {
	it.each(["ao-app", "aoagents.ao", "aomobile"])("keeps a warm %s callback out of screen navigation", async (scheme) => {
		expect(redirectSystemPath({ path: `${scheme}://callback?code=test-code&state=test-state`, initial: false })).toBeNull();
	});
	it.each(["ao-app", "aoagents.ao", "aomobile"])("opens the root for a cold %s callback without routing auth parameters", async (scheme) => {
		expect(redirectSystemPath({ path: `${scheme}://callback?code=test-code&state=test-state`, initial: true })).toBe("/");
	});
	it("keeps an OAuth error callback on the originating screen", async () => {
		expect(redirectSystemPath({ path: "ao-app://callback?error=access_denied&state=test-state", initial: false })).toBeNull();
	});
	it.each([
		"aomobile://connect?url=http%3A%2F%2F192.168.1.2%3A8080",
		"aoagents.ao://session/worker-1",
		"/session/worker-1",
		"https://other.test/callback?code=test-code",
		"ao-app://callback.evil.test?code=test-code",
		"ao-app://callback/unrelated?code=test-code",
		"ao-app://user@callback?code=test-code",
		"ao-app://callback:123?code=test-code",
		"not a URL",
	])("preserves unrelated system link %s", async (path) => {
		expect(redirectSystemPath({ path, initial: false })).toBe(path);
	});
});
