import type { Page } from "@playwright/test";
import { installFakeAgent } from "../support/fake-bridge";

const paths = Array.from({ length: 6000 }, (_, i) => `src/feature${i}/file${i}.ts`);
const workers = Array.from({ length: 10 }, (_, i) => ({
	id: `bench-${i}`, title: `Bench ${i}`, mode: "chat" as const, provider: "codex",
	status: "idle", activity: "idle" as const,
}));

/** Real renderer routes and chat surfaces; daemon and native browser are fakes. */
export async function installSessionSwitchFixture(page: Page) {
	await installFakeAgent(page, { workers, expandProject: true });
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		const id = pathname.match(/sessions\/(bench-\d+)/)?.[1];
		if (pathname.includes("/agents/readiness")) return route.fulfill({ json: { agents: [] } });
		if (pathname.endsWith("/conversation")) {
			const stamp = "2026-11-01T10:00:00Z";
			const turns = Array.from({ length: 18 }, (_, i) => ({
				id: `${id}-turn-${i}`, state: "completed", requestedAt: stamp, startedAt: stamp, completedAt: stamp,
			}));
			const messages = turns.flatMap((turn, i) => [
				{ kind: "message", id: `${id}-user-${i}`, turnId: turn.id, sequence: i * 2 + 1, revision: 0,
					role: "user", origin: "human", text: `Inspect ${id} change ${i}`, streaming: false, createdAt: stamp },
				{ kind: "message", id: `${id}-reply-${i}`, turnId: turn.id, sequence: i * 2 + 2, revision: 0,
					role: "assistant", origin: "agent", streaming: false, createdAt: stamp,
					text: `Reviewed ${[paths[i * 7], `file${i * 7 + 1}.ts:12`, `/repo/${paths[i * 7 + 2]}`,
						`feature${i * 7 + 3}/file${i * 7 + 3}.ts`, "not_a_file", "missing.ts"]
						.map((path) => `\`${path}\``).join(", ")}. Complete ${id} ${i}.` },
			]);
			return route.fulfill({ json: { conversationId: `conversation-${id}`, sessionId: id, harness: "codex",
				mode: "chat", controller: "ready", latestSequence: 36, oldestSequence: 1, hasMoreBefore: false,
				turns, messages, activities: [], settings: {} } });
		}
		if (pathname.endsWith("/workspace/files")) return route.fulfill({
			json: { files: paths.map((path) => ({ path, status: "untracked" })), truncated: false },
		});
		if (pathname.endsWith("/conversation/models")) return route.fulfill({ json: { models: [], selected: {} } });
		if (pathname.endsWith("/conversation/skills")) return route.fulfill({ json: { skills: [] } });
		if (pathname.endsWith("/interface-transition")) return route.fulfill({ json: { supported: true, targetMode: "tui" } });
		if (pathname.endsWith("/shell-terminals")) return route.fulfill({ json: { terminals: [] } });
		return route.fulfill({ json: { status: "ok" } });
	});
}

/** Click dispatch to the task after rAF with exact destination content and links. */
export async function measureSessionSwitch(page: Page, id: number) {
	return page.evaluate(async (id) => {
		const button = document.querySelector<HTMLButtonElement>(`button[aria-label="Open Bench ${id}"]`);
		if (!button) throw new Error(`Missing sidebar session ${id}`);
		const started = performance.now();
		button.click();
		await new Promise<void>((resolve, reject) => {
			function poll() {
				const log = document.querySelector('[role="log"][aria-label="Conversation"]');
				const links = log?.querySelectorAll('[aria-label^="Open src/"]').length ?? 0;
				if (log?.textContent?.includes(`Complete bench-${id} 17.`) && links === 54) return resolve();
				if (performance.now() - started > 15_000) return reject(new Error(`Destination ${id} not ready; ${links} file links`));
				requestAnimationFrame(poll);
			}
			poll();
		});
		await new Promise<void>((resolve) => requestAnimationFrame(() => {
			const channel = new MessageChannel();
			channel.port1.onmessage = () => { channel.port1.close(); channel.port2.close(); resolve(); };
			channel.port2.postMessage(0);
		}));
		return { id, started, finished: performance.now(),
			turns: document.querySelectorAll("[data-chat-scroll-anchor]").length,
			logs: document.querySelectorAll('[role="log"][aria-label="Conversation"]').length };
	}, id);
}
