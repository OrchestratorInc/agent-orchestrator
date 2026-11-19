import { expect, it, vi } from "vitest";

vi.mock("expo-application", () => ({ applicationId: "aoagents.dev" }));
vi.mock("expo-web-browser", () => ({ openAuthSessionAsync: vi.fn(), openBrowserAsync: vi.fn(), dismissAuthSession: vi.fn() }));
vi.mock("react-native", () => ({ NativeModules: {}, Platform: { OS: "ios" } }));

import { agentLoginReturnUrl, BrowserSignInUnavailable, canSignInWithBrowser, prepareBrowserSignIn } from "./browserSignIn";

it("returns to this app: the auth session's scheme on iOS, an explicit intent on Android", () => {
	expect(agentLoginReturnUrl("ios", "aoagents.ao")).toBe("ao-app://agent-login");
	expect(agentLoginReturnUrl("android", "aoagents.dev.cloudtest")).toBe("intent://agent-login#Intent;scheme=ao-app;package=aoagents.dev.cloudtest;end");
});

it("reports a build without the listener so the code fallback runs", async () => {
	expect(canSignInWithBrowser()).toBe(false);
	await expect(prepareBrowserSignIn(0, "/callback")).rejects.toBeInstanceOf(BrowserSignInUnavailable);
});
