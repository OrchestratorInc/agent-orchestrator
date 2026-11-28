import { describe, expect, it } from "vitest";
import { APP_RETURN_URL, authCallbackReturnUrl, renderAuthCallbackPage } from "./auth-callback-page";

describe("renderAuthCallbackPage", () => {
	it("names the service on success and failure", () => {
		expect(renderAuthCallbackPage({ provider: "cloud", ok: true })).toContain("<title>Signed in to Orchestrator</title>");
		expect(renderAuthCallbackPage({ provider: "claude-code", ok: true })).toContain("<h1>Signed in to Claude Code</h1>");
		expect(renderAuthCallbackPage({ provider: "github", ok: false })).toContain("<h1>Couldn’t sign in to GitHub</h1>");
	});

	it("confirms the sign-in only, not the save that follows it", () => {
		for (const provider of ["cloud", "claude-code", "github"] as const) {
			const page = renderAuthCallbackPage({ provider, ok: true, returnUrl: APP_RETURN_URL });
			expect(page).not.toMatch(/saved|connected/i);
		}
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
		expect(failure).toContain("Try signing in again from Orchestrator.");
	});

	it("tells a dev build to close the tab instead of linking to the app", () => {
		const page = renderAuthCallbackPage({ provider: "cloud", ok: true, returnUrl: authCallbackReturnUrl(false) });
		expect(page).not.toContain("<script");
		expect(page).not.toContain("ao-app://");
		expect(page).toContain("<p>You can close this tab.</p>");
	});
});

describe("authCallbackReturnUrl", () => {
	it("is the app link in a packaged build only", () => {
		expect(authCallbackReturnUrl(true)).toBe(APP_RETURN_URL);
		expect(authCallbackReturnUrl(false)).toBeUndefined();
	});
});
