import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanUpLegacyPreparedCloudSessions } from "./cloud-session-prestart";
import { CloudCpError } from "./cloud-cp/errors";

const KEY = "ao.cloudPreparedSessions.v1";

function record(entries: { orgId: string; sessionId: string; discarded?: boolean }[]) {
	window.localStorage.setItem(KEY, JSON.stringify(entries));
}

function client(messages: Record<string, number>, deleteSession = vi.fn().mockResolvedValue(undefined)) {
	return {
		listChatEvents: vi.fn(async (_org: string, sessionId: string) => {
			if (!(sessionId in messages)) throw new CloudCpError("not found", { status: 404 });
			return { events: Array.from({ length: messages[sessionId] }, (_, sequence) => ({ sequence, type: "chat.user_message", payload: {} })), nextAfter: 0, hasMore: false };
		}),
		deleteSession,
	};
}

describe("cleanUpLegacyPreparedCloudSessions", () => {
	beforeEach(() => window.localStorage.clear());

	it("deletes a recorded session that never received a message, then forgets the record", async () => {
		record([{ orgId: "org-1", sessionId: "pre-1" }]);
		const cp = client({ "pre-1": 0 });
		await cleanUpLegacyPreparedCloudSessions(cp as never, "org-1");
		expect(cp.deleteSession).toHaveBeenCalledWith("org-1", "pre-1");
		expect(window.localStorage.getItem(KEY)).toBeNull();
	});

	it("keeps a recorded session a send made into a task, and forgets only its record", async () => {
		record([{ orgId: "org-1", sessionId: "pre-2" }]);
		const cp = client({ "pre-2": 1 });
		await cleanUpLegacyPreparedCloudSessions(cp as never, "org-1");
		expect(cp.deleteSession).not.toHaveBeenCalled();
		expect(window.localStorage.getItem(KEY)).toBeNull();
	});

	it("forgets a record already deleted, and one the control plane no longer knows, without deleting anything", async () => {
		record([{ orgId: "org-1", sessionId: "gone-1", discarded: true }, { orgId: "org-1", sessionId: "gone-2" }]);
		const cp = client({});
		await cleanUpLegacyPreparedCloudSessions(cp as never, "org-1");
		expect(cp.listChatEvents).toHaveBeenCalledOnce();
		expect(cp.deleteSession).not.toHaveBeenCalled();
		expect(window.localStorage.getItem(KEY)).toBeNull();
	});

	it("keeps the record for the next run when deleting fails for another reason", async () => {
		record([{ orgId: "org-1", sessionId: "pre-3" }]);
		const cp = client({ "pre-3": 0 }, vi.fn().mockRejectedValue(new CloudCpError("unavailable", { status: 503 })));
		await cleanUpLegacyPreparedCloudSessions(cp as never, "org-1");
		expect(JSON.parse(window.localStorage.getItem(KEY) ?? "[]")).toEqual([{ orgId: "org-1", sessionId: "pre-3" }]);
	});

	it("touches only the org it was asked about", async () => {
		record([{ orgId: "org-2", sessionId: "other" }]);
		const cp = client({ other: 0 });
		await cleanUpLegacyPreparedCloudSessions(cp as never, "org-1");
		expect(cp.listChatEvents).not.toHaveBeenCalled();
		expect(JSON.parse(window.localStorage.getItem(KEY) ?? "[]")).toHaveLength(1);
	});
});
