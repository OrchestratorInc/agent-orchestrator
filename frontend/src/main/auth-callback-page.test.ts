import { describe, expect, it } from "vitest";
import { APP_RETURN_URL, authCallbackReturnUrl, renderAuthCallbackPage } from "./auth-callback-page";

describe("renderAuthCallbackPage", () => {
	it("names the service on success and failure", () => {
		expect(renderAuthCallbackPage({ provider: "cloud", ok: true })).toContain("<title>Signed in to Orchestrator Cloud</title>");
		expect(renderAuthCallbackPage({ provider: "claude-code", ok: true })).toContain("<h1>Claude is connected</h1>");
		expect(renderAuthCallbackPage({ provider: "github", ok: false })).toContain("<h1>Couldn’t sign in to GitHub</h1>");
	});

	it("shows the GitHub login, escaped", () => {
		const page = renderAuthCallbackPage({ provider: "github", ok: true, login: "<img src=x onerror=alert(1)>" });
		expect(page).toContain("Authenticated as <strong>&lt;img src=x onerror=alert(1)&gt;</strong>");
		expect(page).not.toContain("<img src=x");
	});

	it("is a self-contained document with no external request", () => {
		const page = renderAuthCallbackPage({ provider: "cloud", ok: true, returnUrl: APP_RETURN_URL });
		expect(page.startsWith("<!doctype html>")).toBe(true);
		expect(page).toContain('<meta charset="utf-8">');
		expect(page).not.toMatch(/<link|https?:\/\//);
	});

	it("returns to the app on its own only after a successful sign-in", () => {
		const success = renderAuthCallbackPage({ provider: "claude-code", ok: true, returnUrl: APP_RETURN_URL });
		expect(success).toContain(`location.href=${JSON.stringify(APP_RETURN_URL)}`);
		expect(success).toContain(`href="${APP_RETURN_URL}"`);

		const failure = renderAuthCallbackPage({ provider: "claude-code", ok: false, returnUrl: APP_RETURN_URL });
		expect(failure).not.toContain("<script");
		expect(failure).toContain(`href="${APP_RETURN_URL}"`);
	});

	it("has no script or return link when the build doesn't own the app link", () => {
		const page = renderAuthCallbackPage({ provider: "cloud", ok: true, returnUrl: authCallbackReturnUrl(false) });
		expect(page).not.toContain("<script");
		expect(page).not.toContain("ao-app://");
		expect(page).toContain("You can close this window and return to Orchestrator.");
	});
});

describe("authCallbackReturnUrl", () => {
	it("is the app link in a packaged build only", () => {
		expect(authCallbackReturnUrl(true)).toBe(APP_RETURN_URL);
		expect(authCallbackReturnUrl(false)).toBeUndefined();
	});
});
