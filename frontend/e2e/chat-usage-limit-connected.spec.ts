import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

// Opt-in full daemon integration. Start TestUsageLimitDaemonBrowserFixture in
// backend/e2e, then supply its coordinator URL. No conversation routes or SSE
// are mocked; only the native desktop bridge and provider processes are controlled.
const fixtureBaseUrl = process.env.AO_USAGE_LIMIT_FIXTURE_URL;

interface FixtureMetadata {
	baseUrl: string;
	sessions: Record<string, string>;
}

test.describe("usage-limit recovery through the real daemon @connected", () => {
	test.skip(!fixtureBaseUrl, "Set AO_USAGE_LIMIT_FIXTURE_URL to a running full-daemon usage-limit fixture; this test does not simulate scheduling.");

	for (const { harness, providerLabel } of [
		{ harness: "claude-code", providerLabel: "Claude Code" },
		{ harness: "codex", providerLabel: "Codex" },
	]) {
		test(`${providerLabel} resumes automatically after a real two-second reset`, async ({ page, request }) => {
			const metadataResponse = await request.get(`${fixtureBaseUrl}/test/usage-limit`);
			expect(metadataResponse.ok()).toBe(true);
			const metadata: FixtureMetadata = await metadataResponse.json();
			const sessionId = metadata.sessions[harness];
			const fixtureSessionUrl = `${fixtureBaseUrl}/test/usage-limit/${encodeURIComponent(sessionId)}`;
			await installFakeBridge(page, { daemonPort: Number(new URL(metadata.baseUrl).port) });
			const retryRequests: string[] = [];
			page.on("request", (outgoing) => {
				if (outgoing.method() === "POST" && /\/conversation\/turns\/[^/]+\/retry$/.test(new URL(outgoing.url()).pathname)) {
					retryRequests.push(outgoing.url());
				}
			});
			const query = new URLSearchParams({ fixtureBaseUrl: fixtureBaseUrl!, harness, delay: "2000" });
			await page.goto(`/e2e/usage-limit/connected.html?${query}`);
			await expect(page.getByRole("tab", { name: `Connected usage-limit recovery · ${providerLabel}`, exact: true })).toBeVisible();
			await expect(page.getByTestId("connected-events-state")).toHaveText("Events: connected.");
			const completedMessages = page.getByText("AUTOMATIC-RESUME-COMPLETED", { exact: true });
			const completedBefore = await completedMessages.count();
			const quotaResponse = page.waitForResponse((response) => response.url() === fixtureSessionUrl && response.request().method() === "POST");
			await page.getByRole("button", { name: "Start quota test", exact: true }).click();
			const triggered = await quotaResponse;
			expect(triggered.ok()).toBe(true);
			const { turnId } = await triggered.json() as { turnId: string };
			const recoveryNotice = page.getByTestId("usage-limit-notice").filter({ has: page.locator("time") });
			await expect(recoveryNotice).toBeVisible();
			await expect(page.getByRole("button", { name: "Resume", exact: true })).toHaveCount(0);
			const consentResponse = page.waitForResponse((response) => response.url() === `${metadata.baseUrl}/api/v1/sessions/${sessionId}/conversation/turns/${turnId}/auto-resume` && response.request().method() === "PATCH");
			await page.getByRole("button", { name: "Auto-resume at reset", exact: true }).click();
			expect((await consentResponse).status()).toBe(204);
			await expect(page.getByText("Auto-resume is scheduled.", { exact: true })).toBeVisible();

			// Real wall-clock time and the production daemon ticker dispatch the retry.
			// Consent stays cancellable until the daemon records the retry.
			await expect(completedMessages).toHaveCount(completedBefore + 1, { timeout: 15_000 });
			await expect(completedMessages.last()).toBeVisible();
			await expect(recoveryNotice).toHaveCount(0);
			await expect(page.getByRole("button", { name: /^(Resume|Auto-resume at reset|Cancel auto-resume)$/ })).toHaveCount(0);
			expect(retryRequests).toEqual([]);

			const statsResponse = await request.get(fixtureSessionUrl);
			expect(statsResponse.ok()).toBe(true);
			const stats = await statsResponse.json() as { deliveries: number; retryDeliveries: number; resetsAt: string; retryDeliveredAt: string };
			expect(stats.deliveries).toBe(2);
			expect(stats.retryDeliveries).toBe(1);
			expect(Date.parse(stats.retryDeliveredAt)).toBeGreaterThanOrEqual(Date.parse(stats.resetsAt));
			const conversationResponse = await request.get(`${metadata.baseUrl}/api/v1/sessions/${sessionId}/conversation`);
			expect(conversationResponse.ok()).toBe(true);
			const conversation = await conversationResponse.json() as { turns: { retryOfTurnId?: string; state: string }[] };
			expect(conversation.turns.filter((turn) => turn.retryOfTurnId === turnId)).toEqual([expect.objectContaining({ state: "completed" })]);
		});
	}
});
