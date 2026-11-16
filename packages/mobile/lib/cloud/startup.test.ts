import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { queuedConversationMessages } from "../chat/timelineModel";
import { startupVisible, stepStatus } from "../session/sessionStartup";
import { cloudStartupIndex, cloudStartupView } from "./startup";
import type { CloudSession } from "./types";

const session = (overrides: Partial<CloudSession> = {}): CloudSession => ({
	id: "s1", orgId: "o", projectId: "p", kind: "worker", harness: "codex", displayName: "Task", branch: "ao/task",
	mode: "trusted", interfaceMode: "chat", activityState: "active", status: "working", runtimeConnected: false,
	isTerminated: false, createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z",
	desiredState: "running", observedState: "provisioning", ...overrides,
});

type Turn = { id: string; state: "queued" | "running" | "completed" | "failed" | "cancelled" };
const turns = (...list: Turn[]) => ({ turns: list }) as never;
const statuses = (view: ReturnType<typeof cloudStartupView>) => view?.steps.map((step) => stepStatus(step, view.failed));

describe("cloud startup checklist", () => {
	it("walks the sandbox, the worker and the agent as the desktop's startup does", () => {
		expect(cloudStartupIndex(session({ observedState: "requested" }))).toBe(0);
		expect(cloudStartupIndex(session({ observedState: "provisioning" }))).toBe(0);
		expect(cloudStartupIndex(session({ observedState: "bootstrapping" }))).toBe(1);
		expect(cloudStartupIndex(session({ observedState: "bootstrapping", runtimeConnected: true }))).toBe(2);
		expect(cloudStartupIndex(session({ observedState: "running", runtimeConnected: true }))).toBe(3);
	});

	it("is the local checklist, in cloud words, while a new session starts", () => {
		const view = cloudStartupView(session({ observedState: "bootstrapping" }), turns());
		expect(statuses(view)).toEqual(["done", "running", "pending"]);
		expect(view).toMatchObject({ failed: false, agentName: "Codex", branch: "ao/task", labels: { worktree: "Create the workspace", setup: "Connect to the worker" } });
		expect(startupVisible(view, turns())).toBe(true);
	});

	it("holds the agent step until the opening brief is picked up, then hands off", () => {
		const up = session({ observedState: "running", runtimeConnected: true });
		const queued = cloudStartupView(up, turns({ id: "t1", state: "queued" }));
		expect(statuses(queued)).toEqual(["done", "done", "running"]);
		expect(queued?.openingTurnId).toBe("t1");
		expect(cloudStartupView(up, turns({ id: "t1", state: "running" }))).toBeUndefined();
		expect(cloudStartupView(up, turns())).toBeUndefined();
	});

	it("leaves a session that has already worked to its banners when it wakes or reconnects", () => {
		expect(cloudStartupView(session({ observedState: "bootstrapping" }), turns({ id: "t1", state: "completed" }))).toBeUndefined();
		expect(cloudStartupView(session({ desiredState: "paused", observedState: "stopped" }), turns())).toBeUndefined();
		expect(cloudStartupView(session({ observedState: "stopped" }), turns())).toBeUndefined();
		expect(cloudStartupView(session({ isTerminated: true }), turns())).toBeUndefined();
		// Read newest first, a long chat's loaded page may hold only a queued
		// message; the older history it lacks means the session has worked.
		const reconnecting = session({ observedState: "running", runtimeConnected: true });
		expect(cloudStartupView(reconnecting, { turns: [{ id: "t9", state: "queued" }], hasMoreBefore: true } as never)).toBeUndefined();
	});

	it("reports a failed start on the step that failed, with the reason", () => {
		const failed = cloudStartupView(session({
			observedState: "running", runtimeConnected: true,
			startupError: { code: "repository_checkout_failed", at: "2026-11-01T00:01:00Z", message: "Couldn't check out the repository.", detail: "fatal: not found" },
		}), turns({ id: "t1", state: "queued" }));
		expect(failed).toMatchObject({ failed: true, error: "Couldn't check out the repository.\nfatal: not found" });
		expect(statuses(failed)).toEqual(["done", "done", "failed"]);
		expect(startupVisible(failed, turns({ id: "t1", state: "queued" }))).toBe(true);
	});
});

describe("a starting cloud session's opening brief", () => {
	// Seen on the Pixel: the brief sat in the timeline as sent and again in the
	// composer's queue, because only the timeline was told which turn it was.
	it("reads as sent, so the composer does not also list it as queued", () => {
		const up = session({ observedState: "running", runtimeConnected: true });
		const snapshot = {
			turns: [{ id: "t1", state: "queued" }],
			items: [{ kind: "message", id: "m1", role: "user", origin: "human", turnId: "t1", text: "Open a PR", sequence: 1, revision: 1 }],
		} as never;
		const view = cloudStartupView(up, snapshot);
		expect(queuedConversationMessages(snapshot)).toHaveLength(1);
		expect(queuedConversationMessages(snapshot, view?.openingTurnId)).toEqual([]);
		const screen = readFileSync(new URL("./CloudSessionScreen.tsx", import.meta.url), "utf8");
		expect(screen).toMatch(/<ChatComposer[\s\S]*?openingTurnId=\{startup\?\.openingTurnId\}[\s\S]*?\/>/);
	});
});

