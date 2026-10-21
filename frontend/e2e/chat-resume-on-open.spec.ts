import { expect, test, type Page } from "@playwright/test";
import type { components } from "../src/api/schema";
import { installFakeAgent } from "./support/fake-bridge";

// Renderer-boundary regression: no reboot, daemon, or provider runs here. The
// fake preload/SSE supplies a ready desktop; intercepted HTTP responses model
// the stopped Chat controller and startup failures observed after a reboot.
// dev:web normally disables automatic resume, so replace only that preview
// flag and supply wire DTOs through the real API client/workspace mapping.
const projectId = "resume-on-open";
const untouchedId = "untouched-stopped-chat";
const now = "2026-10-21T12:00:00Z";
type ResumeFailure = { status: number; code: string; message: string };
test.setTimeout(60_000);

function session(id: string, resumed: boolean): components["schemas"]["ControllersSessionView"] {
	return {
		id, projectId, displayName: id, harness: "codex", kind: "worker", mode: "chat",
		branch: `session/${id}`, status: resumed ? "idle" : "exited", statusReadiness: "ready",
		displayStatus: resumed ? "Working" : "Exited", kanbanColumn: resumed ? "building" : "needs_review",
		activity: { state: resumed ? "idle" : "exited", lastActivityAt: now },
		createdAt: now, updatedAt: now, lastEventAt: now, isTerminated: false, isPinned: false,
		autoInjectCI: true, autoInjectReview: true, autoReviewEnabled: false,
		chatProviderPreserved: false, terminateOnPrMerge: false, outputType: "none", prs: [],
	};
}

function conversation(id: string, resumed: boolean) {
	return {
		conversationId: `conversation-${id}`, sessionId: id, harness: "codex", mode: "chat",
		controller: resumed ? "ready" : "stopped", latestSequence: 1, oldestSequence: 1, hasMoreBefore: false,
		turns: [{ id: "saved-turn", state: "completed", requestedAt: now, startedAt: now, completedAt: now }],
		messages: [{
			kind: "message", id: "saved-message", turnId: "saved-turn", sequence: 1, revision: 0,
			role: "assistant", origin: "agent", text: "Saved conversation remains available.", streaming: false, createdAt: now,
		}],
		activities: [], settings: {},
	};
}

async function installStoppedChats(page: Page, failures: Record<string, ResumeFailure[]>) {
	const ids = [...Object.keys(failures), untouchedId];
	const attempts: Record<string, number[]> = Object.fromEntries(ids.map((id) => [id, []]));
	const resumed = new Set<string>();
	const submittedMessages: string[] = [];
	await installFakeAgent(page);
	await page.route("**/src/renderer/lib/preview-mode.ts*", (route) => route.fulfill({
		contentType: "text/javascript", body: "export const usesPreviewWorkspaceData = false;",
	}));
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const request = route.request();
		const path = new URL(request.url()).pathname;
		if (path === "/api/v1/settings") return route.fulfill({ json: {
			defaultSessionMode: "chat", chatHarnesses: ["codex"], localEnabled: true,
			cloudOffering: false, cloudEnabled: false,
		} });
		if (path === "/api/v1/settings/chat-hibernation") return route.fulfill({ json: { enabled: false } });
		if (path === "/api/v1/notifications") return route.fulfill({ json: { notifications: [] } });
		if (path === "/api/v1/projects") return route.fulfill({ json: { projects: [{
			id: projectId, name: projectId, kind: "single_repo", path: `/repos/${projectId}`,
			sessionPrefix: "resume", folderMissing: false,
		}] } });
		if (path === "/api/v1/sessions") {
			return route.fulfill({ json: { sessions: ids.map((id) => session(id, resumed.has(id))) } });
		}
		const id = path.split("/")[4];
		if (id && ids.includes(id)) {
			if (path.endsWith("/resume-agent") && request.method() === "POST") {
				attempts[id].push(Date.now());
				const failure = failures[id]?.[attempts[id].length - 1];
				if (failure) return route.fulfill({ status: failure.status, json: {
					error: "resume failed", code: failure.code, message: failure.message, requestId: "resume-regression",
				} });
				resumed.add(id);
				return route.fulfill({ json: { ok: true, resumeMode: "native", sessionId: id, session: session(id, true) } });
			}
			if (path.endsWith("/conversation") && request.method() === "GET") {
				return route.fulfill({ json: conversation(id, resumed.has(id)) });
			}
			if (path.endsWith("/conversation/messages") && request.method() === "POST") {
				submittedMessages.push(id);
				return route.fulfill({ status: 500, json: { code: "UNEXPECTED_MESSAGE", message: "Resume must not submit a turn" } });
			}
			if (path.endsWith("/conversation/models")) return route.fulfill({ json: { models: [], selected: {} } });
			if (path.endsWith("/conversation/skills")) return route.fulfill({ json: { skills: [] } });
			if (path.endsWith("/workspace/files")) return route.fulfill({ json: { files: [], truncated: false } });
			if (path.endsWith("/interface-transition")) return route.fulfill({ json: { supported: true, targetMode: "tui" } });
		}
		return route.fulfill({ status: 404, json: { code: "NOT_FOUND", message: "Not part of the renderer fixture" } });
	});
	return { attempts, submittedMessages };
}

async function openChat(page: Page, id: string) {
	await page.goto(`/#/projects/${projectId}/sessions/${id}`);
	await expect(page.getByRole("region", { name: "Chat", exact: true })).toBeVisible({ timeout: 15000 });
	await expect(page.getByText("Saved conversation remains available.")).toBeVisible();
}

async function capture(page: Page, name: string) {
	const directory = process.env.AO_RESUME_EVIDENCE_DIR;
	if (directory) await page.screenshot({ path: `${directory}/${name}.png`, fullPage: true });
}

test("renderer: opening stopped Chat recovers from one startup timeout without a click @T0", async ({ page }) => {
	const id = "transient-startup";
	const { attempts, submittedMessages } = await installStoppedChats(page, {
		[id]: [{ status: 503, code: "SERVICE_UNAVAILABLE", message: "Codex initialization timed out" }],
	});
	await openChat(page, id);
	await expect.poll(() => attempts[id].length).toBe(1);
	await expect(page.getByText("The agent controller stopped", { exact: true })).toHaveCount(0);
	await expect(page.getByRole("button", { name: "Resume agent", exact: true })).toHaveCount(0);
	await capture(page, "resume-transient-pending");

	await expect.poll(() => attempts[id].length, { timeout: 8000 }).toBe(2);
	expect(attempts[id][1] - attempts[id][0]).toBeGreaterThanOrEqual(1800);
	const composer = page.getByRole("combobox", { name: "Message the agent" });
	await expect(composer).toBeEditable();
	await composer.fill("A draft after automatic recovery");
	await expect(page.getByRole("button", { name: "Send message", exact: true })).toBeEnabled();
	await expect(page.getByText("The agent controller stopped", { exact: true })).toHaveCount(0);
	await capture(page, "resume-transient-recovered");
	expect(attempts[untouchedId]).toEqual([]);
	expect(submittedMessages).toEqual([]);
});

test("renderer: automatic recovery stops for permanent errors and after three transient attempts @T0", async ({ page }) => {
	const auth = { status: 409, code: "CHAT_AUTH_REQUIRED", message: "The agent is installed but not authenticated" };
	const incompatible = { status: 409, code: "CHAT_DRIVER_INCOMPATIBLE", message: "The installed chat driver is incompatible" };
	const timeout = { status: 504, code: "GATEWAY_TIMEOUT", message: "Agent initialization deadline exceeded" };
	const { attempts, submittedMessages } = await installStoppedChats(page, {
		"auth-required": [auth], "incompatible-driver": [incompatible], "persistent-timeout": [timeout, timeout, timeout],
	});
	for (const id of ["auth-required", "incompatible-driver"]) {
		await openChat(page, id);
		await expect(page.getByRole("button", { name: "Resume agent", exact: true })).toBeVisible();
		await expect(page.getByText(id === "auth-required" ? auth.message : incompatible.message, { exact: true })).toBeVisible();
		await page.waitForTimeout(2300); // Beyond the first retry delay: permanent failures stay stopped.
		expect(attempts[id]).toHaveLength(1);
	}
	await openChat(page, "persistent-timeout");
	await expect.poll(() => attempts["persistent-timeout"].length, { timeout: 12000 }).toBe(3);
	await expect(page.getByText("The agent controller stopped", { exact: true })).toBeVisible();
	await expect(page.getByRole("button", { name: "Resume agent", exact: true })).toBeEnabled();
	await expect(page.getByText(timeout.message, { exact: true })).toBeVisible();
	await page.waitForTimeout(5500); // A fourth attempt must not run after the final backoff.
	expect(attempts["persistent-timeout"]).toHaveLength(3);
	await capture(page, "resume-retries-exhausted");
	expect(attempts[untouchedId]).toEqual([]);
	expect(submittedMessages).toEqual([]);
});
