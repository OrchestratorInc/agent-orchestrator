import { expect, test, type Page } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

// Evidence rig for steering a hermes session: the hermes ACP driver does not
// advertise the steer capability, yet the queued card must still offer Steer
// (cut the running turn and dispatch the queued message now), and activating it
// must call the queued-turn steer route and land the interrupted marker in the
// timeline. Mirrors the fork's queued-card steer behavior carried upstream.
//
// Run with AO_STEER_EVIDENCE_DIR set to a directory to capture screenshots;
// `video: "on"` leaves a screencast of the flow in test-results.

test.use({ video: "on" });

const sessionId = "hermes-steer";
const now = "2026-10-30T12:00:00Z";

function snapshot(state: "before" | "after") {
	return {
		conversationId: `conversation-${sessionId}`,
		sessionId,
		harness: "hermes",
		mode: "chat",
		controller: "busy",
		// Deliberately WITHOUT "steer": the hermes driver has no steering
		// extension, so the mid-turn composer steer stays hidden while the
		// queued-card affordance must remain available.
		capabilities: ["images"],
		latestSequence: 4,
		oldestSequence: 1,
		hasMoreBefore: false,
		turns:
			state === "before"
				? [
						{
							id: "running-1",
							state: "running",
							providerTurnId: "provider-1",
							requestedAt: now,
							startedAt: now,
						},
						{ id: "queued-1", state: "queued", requestedAt: now },
					]
				: [
						{
							id: "running-1",
							state: "interrupted",
							providerTurnId: "provider-1",
							requestedAt: now,
							startedAt: now,
							completedAt: now,
						},
						{
							id: "promoted-1",
							state: "running",
							providerTurnId: "provider-2",
							requestedAt: now,
							startedAt: now,
						},
					],
		messages: [
			{
				id: "message-running",
				turnId: "running-1",
				sequence: 1,
				revision: 0,
				role: "user",
				origin: "human",
				text: "Count from 1 to 400, one per line",
				streaming: false,
				createdAt: now,
				content: [],
			},
			{
				id: "message-queued",
				turnId: "queued-1",
				sequence: 2,
				revision: 0,
				role: "user",
				origin: "human",
				text: "STEER-MARKER: stop counting and summarize instead",
				streaming: false,
				createdAt: now,
				content: [],
			},
		],
		activities: [],
		settings: {},
	};
}

async function installConversationRoutes(page: Page, evidenceDir: string) {
	let state: "before" | "after" = "before";
	const steerCalls: unknown[] = [];
	await page.route(`**/api/v1/sessions/${sessionId}/**`, async (route) => {
		const url = new URL(route.request().url());
		if (url.pathname.endsWith("/conversation") && route.request().method() === "GET") {
			await route.fulfill({ json: snapshot(state) });
			return;
		}
		if (url.pathname.endsWith("/conversation/turns/queued-1/steer")) {
			steerCalls.push(route.request().postDataJSON());
			state = "after";
			await route.fulfill({
				status: 202,
				json: {
					sourceTurnId: "queued-1",
					providerTurnId: "provider-2",
					interruptedTurnId: "running-1",
				},
			});
			return;
		}
		if (url.pathname.endsWith("/conversation/models")) {
			await route.fulfill({ json: { models: [], selected: {} } });
			return;
		}
		if (url.pathname.endsWith("/conversation/skills")) {
			await route.fulfill({ json: { skills: [] } });
			return;
		}
		if (url.pathname.endsWith("/workspace/files")) {
			await route.fulfill({ json: { files: [], truncated: false } });
			return;
		}
		if (url.pathname.endsWith("/interface-transition")) {
			await route.fulfill({ json: { supported: true, targetMode: "tui" } });
			return;
		}
		await route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND", message: "not found" } } });
	});
	return { evidenceDir, steerCalls };
}

test("queued-card Steer renders and cuts the running turn on a hermes session @T0", async ({ page }) => {
	const evidenceDir = process.env.AO_STEER_EVIDENCE_DIR ?? "e2e/artifacts/hermes-steer";
	await installFakeAgent(page, { workers: [{ id: sessionId, title: "Hermes steer", mode: "chat" }] });
	const rig = await installConversationRoutes(page, evidenceDir);

	await page.goto(`/#/projects/fake-proj/sessions/${sessionId}`);
	const queuedRow = page.getByText("STEER-MARKER: stop counting and summarize instead");
	await expect(queuedRow).toBeVisible();

	// The mid-turn composer steer is capability-gated and hermes has no steer
	// capability: no affordance may offer "Steer message".
	await expect(page.getByRole("button", { name: "Steer message" })).toHaveCount(0);

	// The queued card still offers the cut-and-send affordance.
	const steerButton = page.getByRole("button", {
		name: "Send this queued message now, interrupting the running turn",
	});
	await expect(steerButton).toHaveCount(1);
	// The affordance is hover-revealed on the queued row.
	await queuedRow.hover();
	await expect(page.getByTitle("Send now (interrupts)")).toBeVisible();
	await page.screenshot({ path: `${evidenceDir}/hermes-queued-card-steer-before.png`, fullPage: true });

	await steerButton.click();

	// The activate went to the queued-turn steer route, which cuts the running
	// turn and promotes the queued message.
	await expect
		.poll(() => rig.steerCalls.length, { timeout: 5_000 })
		.toBe(1);

	// The timeline reflects the cut: the interrupted marker lands on the cut
	// counting turn.
	await expect(page.getByText("The agent was interrupted by you").first()).toBeVisible({ timeout: 10_000 });
	await page.screenshot({ path: `${evidenceDir}/hermes-queued-card-steer-after.png`, fullPage: true });
});
