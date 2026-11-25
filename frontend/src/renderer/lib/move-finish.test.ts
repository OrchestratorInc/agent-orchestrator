import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { CloudCpError } from "./cloud-cp";
import { MAX_FINISH_AGE_MS, MAX_FINISH_ATTEMPTS, finishExpired, finishMove, useFinishUnfinishedMoves, useUnfinishedMoveStore, type UnfinishedMove } from "./move-finish";

const { completeReply, cloudCp } = vi.hoisted(() => ({
	completeReply: { current: {} as { error?: unknown; response?: { status: number } } },
	cloudCp: { current: { client: null as unknown, userId: "" } },
}));
vi.mock("./host-clients", () => ({ clientForSessionHost: () => ({ POST: vi.fn(async () => completeReply.current) }) }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => cloudCp.current }));

const complete: UnfinishedMove = { kind: "complete", sessionId: "app-1", moveId: "m-1", orgId: "org-1", cloudSessionId: "cloud-9" };
const deleteCloud: UnfinishedMove = { kind: "delete-cloud", orgId: "org-1", cloudSessionId: "cloud-9" };

function daemon(reply: { error?: unknown; response?: { status: number } }) {
	const POST = vi.fn(async () => reply);
	return { POST, factory: () => ({ POST }) as never };
}

describe("finishMove", () => {
	it("records a move on this computer, and is done when there is nothing left to record", async () => {
		const ok = daemon({});
		expect(await finishMove(complete, { daemon: ok.factory, cloud: null })).toBe("done");
		expect(ok.POST).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/move/complete", {
			params: { path: { sessionId: "app-1" } },
			body: { moveId: "m-1", orgId: "org-1", sessionId: "cloud-9" },
		});
		expect(await finishMove(complete, { daemon: daemon({ error: { code: "SESSION_MOVED" } }).factory, cloud: null })).toBe("done");
		expect(await finishMove(complete, { daemon: daemon({ error: { code: "INTERNAL" }, response: { status: 500 } }).factory, cloud: null })).toBe("retry");
	});

	it("stops on a refusal retrying cannot change", async () => {
		const refused = daemon({ error: { code: "MOVE_DESTINATION_INVALID", message: "moveId, orgId and sessionId are required" }, response: { status: 400 } });
		expect(await finishMove(complete, { daemon: refused.factory, cloud: null })).toEqual({ failed: "moveId, orgId and sessionId are required" });
		for (const status of [403, 409, 422]) {
			const refuse = vi.fn(async () => { throw new CloudCpError("refused", { status }); });
			expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: refuse } })).toEqual({ failed: "refused" });
		}
		// A refusal that says a retry is safe, and a timeout, are retried.
		const notCaptured = vi.fn(async () => { throw new CloudCpError("not captured", { status: 422, code: "MOVE_TRANSCRIPT_NOT_CAPTURED" }); });
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: notCaptured } })).toBe("retry");
		const timeout = vi.fn(async () => { throw new CloudCpError("timeout", { status: 504, code: "WORKER_TIMEOUT" }); });
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: timeout } })).toBe("retry");
	});

	it("leaves a Cloud copy alone under another account, never reading its 404 as deleted", async () => {
		const gone = vi.fn(async () => { throw new CloudCpError("gone", { status: 404 }); });
		const mine = { ...deleteCloud, accountId: "user-a" };
		expect(await finishMove(mine, { daemon: daemon({}).factory, cloud: { deleteSession: gone }, accountId: "user-b" })).toBe("other-account");
		expect(await finishMove(mine, { daemon: daemon({}).factory, cloud: null, accountId: "" })).toBe("other-account");
		expect(gone).not.toHaveBeenCalled();
		expect(await finishMove(mine, { daemon: daemon({}).factory, cloud: { deleteSession: gone }, accountId: "user-a" })).toBe("done");
	});

	it("deletes the Cloud session a move left, and is done once Cloud no longer has it", async () => {
		const deleted = vi.fn(async () => ({}) as never);
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: deleted } })).toBe("done");
		expect(deleted).toHaveBeenCalledWith("org-1", "cloud-9");
		const gone = vi.fn(async () => { throw new CloudCpError("gone", { status: 404 }); });
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: gone } })).toBe("done");
		const down = vi.fn(async () => { throw new CloudCpError("down", { status: 503 }); });
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: { deleteSession: down } })).toBe("retry");
		expect(await finishMove(deleteCloud, { daemon: daemon({}).factory, cloud: null })).toBe("retry");
	});
});

describe("finishExpired", () => {
	it("gives up after a day or too many tries", () => {
		const now = 1_000_000_000;
		expect(finishExpired({ ...complete, addedAt: now - 1000, attempts: 3 }, now)).toBe(false);
		expect(finishExpired({ ...complete, addedAt: now - MAX_FINISH_AGE_MS, attempts: 3 }, now)).toBe(true);
		expect(finishExpired({ ...complete, addedAt: now, attempts: MAX_FINISH_ATTEMPTS }, now)).toBe(true);
		// An entry saved before these fields existed starts counting now.
		expect(finishExpired(complete, now)).toBe(false);
	});
});

describe("useFinishUnfinishedMoves", () => {
	it("says once that it stopped, and drops a move refused for good", async () => {
		completeReply.current = { error: { code: "MOVE_DESTINATION_INVALID", message: "moveId, orgId and sessionId are required" }, response: { status: 400 } };
		useUiStore.setState({ globalToast: null });
		useUnfinishedMoveStore.setState({ moves: [] });
		useUnfinishedMoveStore.getState().add(complete);
		const { unmount } = renderHook(() => useFinishUnfinishedMoves());
		await waitFor(() => expect(useUnfinishedMoveStore.getState().moves).toEqual([]));
		expect(useUiStore.getState().globalToast).toMatchObject({
			title: "Orchestrator stopped trying to archive a session that moved to Orchestrator Cloud. Archive it on this computer.",
			body: "moveId, orgId and sessionId are required",
		});
		unmount();
	});

	it("drops a move that kept failing for a day", async () => {
		completeReply.current = { error: { code: "INTERNAL" }, response: { status: 500 } };
		useUiStore.setState({ globalToast: null });
		useUnfinishedMoveStore.setState({ moves: [] });
		useUnfinishedMoveStore.getState().add({ ...complete, addedAt: Date.now() - MAX_FINISH_AGE_MS });
		const { unmount } = renderHook(() => useFinishUnfinishedMoves());
		await waitFor(() => expect(useUnfinishedMoveStore.getState().moves).toEqual([]));
		expect(useUiStore.getState().globalToast?.body).toBeUndefined();
		unmount();
	});

	it("keeps a move for another account, says so once, and does not count tries", async () => {
		const deleteSession = vi.fn(async () => ({}));
		cloudCp.current = { client: { deleteSession }, userId: "user-b" };
		useUiStore.setState({ globalToast: null });
		useUnfinishedMoveStore.setState({ moves: [] });
		useUnfinishedMoveStore.getState().add({ ...deleteCloud, accountId: "user-a" });
		const { unmount } = renderHook(() => useFinishUnfinishedMoves());
		await waitFor(() => expect(useUiStore.getState().globalToast?.title).toBe(
			"A move to this computer is unfinished for another Orchestrator Cloud account. Sign in to that account to delete its Cloud copy.",
		));
		expect(deleteSession).not.toHaveBeenCalled();
		expect(useUnfinishedMoveStore.getState().moves).toMatchObject([{ accountId: "user-a", attempts: 0 }]);
		unmount();
		cloudCp.current = { client: null, userId: "" };
	});

	it("keeps retrying a move that may still work, counting each try", async () => {
		completeReply.current = { error: { code: "INTERNAL" }, response: { status: 500 } };
		useUnfinishedMoveStore.setState({ moves: [] });
		useUnfinishedMoveStore.getState().add(complete);
		const { unmount } = renderHook(() => useFinishUnfinishedMoves());
		await waitFor(() => expect(useUnfinishedMoveStore.getState().moves[0]?.attempts).toBe(1));
		unmount();
	});
});
