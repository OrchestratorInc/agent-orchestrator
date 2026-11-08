import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MoveError, moveSession, type MoveInput, type MoveResult } from "./move-session";
import { cancelWaitingMove, moveKey, runMove, useMoveStore } from "./move-runner";

vi.mock("./move-session", async (importOriginal) => ({ ...(await importOriginal<typeof import("./move-session")>()), moveSession: vi.fn() }));

const result: MoveResult = { sessionId: "web-9", warnings: [], committed: true, transferId: "tr1", destinationLabel: "MacBook B", resumed: true };

function input(states: string[]): MoveInput {
	let i = 0;
	const client = { GET: async () => ({ data: { session: { activity: { state: states[Math.min(i++, states.length - 1)] } } } }) } as never;
	return {
		source: { client, hostId: "local", sessionId: "web-3", label: "This computer" },
		target: { client, hostId: "host-b", label: "MacBook B", projectId: "web" },
	};
}

describe("runMove", () => {
	const key = moveKey(undefined, "web-3");
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => {
		vi.useRealTimers();
		vi.mocked(moveSession).mockReset();
		useMoveStore.setState({ moves: {} });
	});

	it("waits for the turn to end before moving", async () => {
		vi.mocked(moveSession).mockResolvedValue(result);
		const onDone = vi.fn();
		const run = runMove(key, input(["active", "active", "idle"]), { afterTurn: true, onDone });
		await vi.advanceTimersByTimeAsync(0);
		expect(useMoveStore.getState().moves[key].phase).toBe("waiting");
		expect(moveSession).not.toHaveBeenCalled();

		await vi.advanceTimersByTimeAsync(4000);
		await run;
		expect(moveSession).toHaveBeenCalledTimes(1);
		expect(onDone).toHaveBeenCalledWith(result);
		expect(useMoveStore.getState().moves[key].phase).toBe("done");
	});

	it("goes back to waiting when a new turn starts before the move", async () => {
		vi.mocked(moveSession)
			.mockRejectedValueOnce(new MoveError("the agent is in the middle of a turn", "prepare", true, "TRANSFER_AGENT_BUSY"))
			.mockResolvedValueOnce(result);
		const run = runMove(key, input(["idle", "active", "idle"]), { afterTurn: true });
		await vi.advanceTimersByTimeAsync(4000);
		await run;
		expect(moveSession).toHaveBeenCalledTimes(2);
		expect(useMoveStore.getState().moves[key].phase).toBe("done");
	});

	it("passes stopTurn through and records a failure", async () => {
		vi.mocked(moveSession).mockRejectedValue(new MoveError("offline", "receive", true));
		const onFail = vi.fn();
		await runMove(key, input(["active"]), { stopTurn: true, onFail });
		expect(vi.mocked(moveSession).mock.calls[0][0]).toMatchObject({ stopTurn: true });
		expect(onFail).toHaveBeenCalled();
		expect(useMoveStore.getState().moves[key]).toMatchObject({ phase: "failed", error: { message: "offline" } });
	});

	it("drops a queued move when it is cancelled", async () => {
		const run = runMove(key, input(["active"]), { afterTurn: true });
		await vi.advanceTimersByTimeAsync(0);
		cancelWaitingMove(key);
		await vi.advanceTimersByTimeAsync(2000);
		await run;
		expect(moveSession).not.toHaveBeenCalled();
		expect(useMoveStore.getState().moves[key]).toBeUndefined();
	});
});
