import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { CloudGitHubInstallation } from "./types";

const browser = vi.hoisted(() => ({
	os: "ios" as "ios" | "android",
	openAuthSessionAsync: vi.fn(),
	openBrowserAsync: vi.fn(),
}));
vi.mock("expo-application", () => ({ applicationId: "aoagents.dev" }));
vi.mock("expo-web-browser", () => ({ openAuthSessionAsync: browser.openAuthSessionAsync, openBrowserAsync: browser.openBrowserAsync, dismissAuthSession: vi.fn() }));
vi.mock("react-native", () => ({ NativeModules: {}, Platform: { get OS() { return browser.os; } } }));

import { INSTALL_POLL_MS, INSTALL_SETTLE_MS, INSTALL_WAIT_MS, installGitHubApp } from "./githubInstall";

const INSTALL_URL = "https://github.com/apps/orchestrator/installations/new?state=s1";

function installation(id: string, updatedAt: string, status = "active"): CloudGitHubInstallation {
	return { id, githubInstallationId: `gh-${id}`, accountLogin: "acme", accountType: "Organization", status, repositorySelection: "selected", syncStatus: "synced", createdAt: "2026-01-01T00:00:00Z", updatedAt };
}

function client(sequence: CloudGitHubInstallation[][]) {
	let call = 0;
	return {
		startGitHubInstallation: vi.fn(async () => ({ installationUrl: INSTALL_URL, expiresAt: "" })),
		listGitHubInstallations: vi.fn(async () => ({ installations: sequence[Math.min(call++, sequence.length - 1)] })),
		syncGitHubInstallation: vi.fn(async () => ({ installation: installation("i1", "x") })),
	};
}

beforeEach(() => {
	vi.useFakeTimers();
	browser.os = "ios";
	browser.openAuthSessionAsync.mockReset();
	browser.openBrowserAsync.mockReset();
});
afterEach(() => { vi.useRealTimers(); });

// Safari's session, not SFSafariViewController's private one: the person is
// already signed in to GitHub there.
it("opens GitHub's install page in the auth session on iOS, which shares Safari's sign-ins", async () => {
	browser.openAuthSessionAsync.mockReturnValue(new Promise(() => {}));
	const cloud = client([[], [installation("i1", "t1")]]);
	const done = installGitHubApp(cloud as never, "org-1", []);
	await vi.advanceTimersByTimeAsync(INSTALL_POLL_MS * 2);
	await expect(done).resolves.toMatchObject({ id: "i1" });
	expect(browser.openAuthSessionAsync).toHaveBeenCalledWith(INSTALL_URL, "ao-app://agent-login");
	expect(browser.openBrowserAsync).not.toHaveBeenCalled();
	expect(cloud.syncGitHubInstallation).toHaveBeenCalledWith("org-1", "i1");
});

it("opens it in a Custom Tab on Android, which runs in the person's Chrome profile", async () => {
	browser.os = "android";
	browser.openBrowserAsync.mockResolvedValue({ type: "opened" });
	const cloud = client([[installation("i1", "t0")], [installation("i1", "t1")]]);
	const done = installGitHubApp(cloud as never, "org-1", [installation("i1", "t0")]);
	await vi.advanceTimersByTimeAsync(INSTALL_POLL_MS * 2);
	await expect(done).resolves.toMatchObject({ id: "i1", updatedAt: "t1" });
	expect(browser.openBrowserAsync).toHaveBeenCalledWith(INSTALL_URL, { showInRecents: true });
});

// The Custom Tab returns at once, so the page waits for the person to finish.
it("waits out an install still in progress on Android, then gives up", async () => {
	browser.os = "android";
	browser.openBrowserAsync.mockResolvedValue({ type: "opened" });
	const cloud = client([[]]);
	let settled = false;
	const done = installGitHubApp(cloud as never, "org-1", []).then((found) => { settled = true; return found; });
	await vi.advanceTimersByTimeAsync(INSTALL_SETTLE_MS * 2);
	expect(settled).toBe(false);
	await vi.advanceTimersByTimeAsync(INSTALL_WAIT_MS);
	await expect(done).resolves.toBeUndefined();
});

// The person closed the sheet without installing: stop soon after, not five minutes on.
it("stops looking a short while after the iOS sheet closes with nothing new", async () => {
	browser.openAuthSessionAsync.mockResolvedValue({ type: "cancel" });
	const cloud = client([[installation("i1", "t0")]]);
	const done = installGitHubApp(cloud as never, "org-1", [installation("i1", "t0")]);
	await vi.advanceTimersByTimeAsync(INSTALL_SETTLE_MS + INSTALL_POLL_MS * 2);
	await expect(done).resolves.toBeUndefined();
	expect(cloud.syncGitHubInstallation).not.toHaveBeenCalled();
});

it("ignores an installation that is not active", async () => {
	browser.openAuthSessionAsync.mockResolvedValue({ type: "cancel" });
	const cloud = client([[installation("i1", "t1", "suspended")]]);
	const done = installGitHubApp(cloud as never, "org-1", []);
	await vi.advanceTimersByTimeAsync(INSTALL_SETTLE_MS + INSTALL_POLL_MS * 2);
	await expect(done).resolves.toBeUndefined();
});

it("reports a browser that would not open", async () => {
	browser.openAuthSessionAsync.mockRejectedValue(new Error("Another web browser is already open"));
	const cloud = client([[]]);
	const done = installGitHubApp(cloud as never, "org-1", []);
	const failure = expect(done).rejects.toThrow("Another web browser is already open");
	await vi.advanceTimersByTimeAsync(INSTALL_POLL_MS);
	await failure;
});

it("stops polling once the screen is gone", async () => {
	browser.openAuthSessionAsync.mockReturnValue(new Promise(() => {}));
	const cloud = client([[]]);
	let alive = true;
	const done = installGitHubApp(cloud as never, "org-1", [], () => alive);
	await vi.advanceTimersByTimeAsync(INSTALL_POLL_MS);
	alive = false;
	await vi.advanceTimersByTimeAsync(INSTALL_POLL_MS * 3);
	await expect(done).resolves.toBeUndefined();
	expect(cloud.listGitHubInstallations).toHaveBeenCalledTimes(1);
});
