import { describe, expect, it, vi } from "vitest";
import { CloudCpError } from "./cloud-cp";
import { finishMove, type UnfinishedMove } from "./move-finish";

const complete: UnfinishedMove = { kind: "complete", sessionId: "app-1", moveId: "m-1", orgId: "org-1", cloudSessionId: "cloud-9" };
const deleteCloud: UnfinishedMove = { kind: "delete-cloud", orgId: "org-1", cloudSessionId: "cloud-9" };

function daemon(reply: { error?: unknown }) {
	const POST = vi.fn(async () => reply);
	return { POST, factory: () => ({ POST }) as never };
}

describe("finishMove", () => {
	it("records a move on this computer, and is done when there is nothing left to record", async () => {
		const ok = daemon({});
		expect(await finishMove(complete, { daemon: ok.factory, cloud: null })).toBe(true);
		expect(ok.POST).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/move/complete", {
			params: { path: { sessionId: "app-1" } },
			body: { moveId: "m-1", orgId: "org-1", sessionId: "cloud-9" },
		});
		expect(await finishMove(complete, { daemon: daemon({ error: { code: "SESSION_MOVED" } }).factory, cloud: null })).toBe(true);
		expect(await finishMove(complete, { daemon: daemon({ error: { code: "INTERNAL" } }).factory, cloud: null })).toBe(false);
	});

	it("deletes the Cloud session a move left, and is done once Cloud no longer has it", async () => {
		const deleted = vi.fn(async () => ({}) as never);
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: deleted } })).toBe(true);
		expect(deleted).toHaveBeenCalledWith("org-1", "cloud-9");
		const gone = vi.fn(async () => { throw new CloudCpError("gone", { status: 404 }); });
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: gone } })).toBe(true);
		const down = vi.fn(async () => { throw new CloudCpError("down", { status: 503 }); });
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: down } })).toBe(false);
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: null })).toBe(false);
	});
});
