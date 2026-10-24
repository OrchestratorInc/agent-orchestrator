import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import NativeAbortController from "abort-controller";
import { createCloudSessionSource } from "./source";
import { cloudConversationReplayCache } from "./conversationReplay";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async () => null, setItem: async () => {}, removeItem: async () => {},
} }));

const event = (sequence: number, type: string, payload: Record<string, unknown>, sessionId = "s1") => ({ sessionId, sequence, type, payload, createdAt: "now" });
const acknowledgement = (sequence = 8) => event(sequence, "chat.turn_steered", { turnId: "t1", text: "Guidance", clientMessageId: "steer-one" });
function fixture() {
	const poll = vi.fn().mockResolvedValue({ events: [], nextAfter: 7, hasMore: false });
	const steerTurn = vi.fn().mockResolvedValue({ event: event(7, "chat.turn_steer_requested", { turnId: "t1", clientMessageId: "steer-one", text: "Guidance" }) });
	const sendMessage = vi.fn();
	const history = [event(1, "chat.turn_capabilities", { turnId: "t1", attempt: 1, steering: true })];
	const session = { id: "s1", harness: "codex", interfaceMode: "chat", runtimeConnected: true, isTerminated: false, activeTurn: { id: "t1", state: "running", attemptCount: 1 } as { id: string; state: string; attemptCount: number } | undefined };
	const replayEvents = vi.fn(async (_org: string, _id: string, options: { after?: number; limit?: number; signal?: AbortSignal } = {}) => options.limit === 100
		? poll(options)
		: { events: history.filter((item) => item.sequence > (options.after ?? 0)), nextAfter: history.at(-1)!.sequence, hasMore: false });
	let current = true;
	const source = createCloudSessionSource({ scopeKey: "steering-scope", isCurrent: () => current, orgId: "o1", client: {
		getSession: vi.fn(async () => ({ session })),
		replayEvents, steerTurn, sendMessage,
	} as never });
	return { source, poll, replayEvents, steerTurn, sendMessage, session, history, changeAccount: () => { current = false; cloudConversationReplayCache.clearScope("steering-scope"); } };
}
const guidance = { text: "Guidance", clientMessageId: "steer-one" };

describe("Cloud provider steering acknowledgement", () => {
	beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal("AbortController", NativeAbortController); cloudConversationReplayCache.clear(); });
	afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

	it("keeps guidance pending after HTTP acceptance until the provider acknowledges it", async () => {
		const { source, poll, steerTurn } = fixture();
		poll.mockResolvedValueOnce({ events: [], nextAfter: 7, hasMore: false }).mockResolvedValue({ events: [acknowledgement()], nextAfter: 8, hasMore: false });
		let settled = false;
		const sending = source.steerMessage!("s1", guidance, "t1").then((value) => { settled = true; return value; }, (error) => error);
		await vi.advanceTimersByTimeAsync(0);
		expect(settled).toBe(false);
		expect(poll).toHaveBeenCalledWith(expect.objectContaining({ after: 7, limit: 100 }));
		await vi.advanceTimersByTimeAsync(500);
		await expect(sending).resolves.toEqual({ duplicate: false, turnId: "t1" });
		expect(steerTurn.mock.calls[0][4]).toMatchObject({ idempotencyKey: "steer-one" });
		expect(poll.mock.calls[0][0].signal.throwIfAborted).toBeUndefined();
	});

	it("rejects a provider failure with its reason instead of reporting a sent message", async () => {
		const { source, poll, sendMessage } = fixture();
		poll.mockResolvedValue({ events: [event(8, "chat.turn_steer_failed", { turnId: "t1", clientMessageId: "steer-one", error: "The provider no longer accepts guidance." })], nextAfter: 8, hasMore: false });
		await expect(source.steerMessage!("s1", guidance, "t1")).rejects.toThrow("The provider no longer accepts guidance.");
		expect(sendMessage).not.toHaveBeenCalled();
	});

	it("bounds an unknown result even when the event request never settles", async () => {
		const { source, poll } = fixture();
		poll.mockImplementation(() => new Promise(() => {}));
		const sending = source.steerMessage!("s1", guidance, "t1").then(() => undefined, (error: unknown) => error);
		await vi.advanceTimersByTimeAsync(30_000);
		expect(await sending).toMatchObject({ message: expect.stringMatching(/provider.*unknown/i) });
		expect(poll.mock.calls[0][0].signal.aborted).toBe(true);
	});

	it("rejects after the acknowledgement deadline when polling only returns empty pages", async () => {
		const { source } = fixture();
		const sending = source.steerMessage!("s1", guidance, "t1").then(() => undefined, (error: unknown) => error);
		await vi.advanceTimersByTimeAsync(30_000);
		expect(await sending).toMatchObject({ message: expect.stringMatching(/provider.*unknown/i) });
	});

	it("ignores unrelated, stale, and other-session events while following every event page", async () => {
		const { source, poll } = fixture();
		poll.mockResolvedValueOnce({ events: [
			acknowledgement(6),
			event(8, "chat.turn_steered", { turnId: "t1", clientMessageId: "another-message" }),
			event(9, "chat.turn_steer_failed", { turnId: "another-turn", clientMessageId: "steer-one", error: "Unrelated" }),
			event(10, "chat.turn_steered", { turnId: "t1", clientMessageId: "steer-one" }, "other-session"),
			event(11, "chat.assistant_delta", { turnId: "t1", clientMessageId: "steer-one", text: "Not an acknowledgement" }),
		], nextAfter: 11, hasMore: true }).mockResolvedValueOnce({ events: [], nextAfter: 11, hasMore: false }).mockResolvedValue({ events: [acknowledgement(12)], nextAfter: 12, hasMore: false });
		let settled = false;
		const sending = source.steerMessage!("s1", guidance, "t1").then((value) => { settled = true; return value; }, (error) => error);
		await vi.advanceTimersByTimeAsync(0);
		expect(settled).toBe(false);
		expect(poll.mock.calls.map(([options]) => options.after)).toEqual([7, 11]);
		await vi.advanceTimersByTimeAsync(500);
		await expect(sending).resolves.toMatchObject({ turnId: "t1" });
	});

	it("retries an uncertain delivery using its original identity and acceptance cursor", async () => {
		const { source, poll, steerTurn, sendMessage } = fixture();
		poll.mockRejectedValueOnce(new TypeError("Connection lost")).mockResolvedValue({ events: [acknowledgement()], nextAfter: 8, hasMore: false });
		await expect(source.steerMessage!("s1", guidance, "t1")).rejects.toThrow("Connection lost");
		await expect(source.steerMessage!("s1", guidance, "t1")).resolves.toMatchObject({ turnId: "t1" });
		expect(steerTurn.mock.calls.map((call) => [call[2], call[3], call[4].idempotencyKey])).toEqual([["t1", "Guidance", "steer-one"], ["t1", "Guidance", "steer-one"]]);
		expect(poll.mock.calls.map(([options]) => options.after)).toEqual([7, 7]);
		expect(sendMessage).not.toHaveBeenCalled();
	});

	it("recovers an unknown delivery after its original turn finishes", async () => {
		const { source, poll, steerTurn, session, history } = fixture();
		const sending = source.steerMessage!("s1", guidance, "t1").then(() => undefined, (error: unknown) => error);
		await vi.advanceTimersByTimeAsync(30_000);
		expect(await sending).toMatchObject({ message: expect.stringMatching(/provider.*unknown/i) });
		session.activeTurn = undefined;
		history.push(event(7, "chat.turn_steer_requested", { turnId: "t1", clientMessageId: "steer-one", text: "Guidance" }), acknowledgement(), event(9, "chat.turn_completed", { turnId: "t1" }));
		poll.mockResolvedValue({ events: [acknowledgement(), event(9, "chat.turn_completed", { turnId: "t1" })], nextAfter: 9, hasMore: false });
		await expect(source.steerMessage!("s1", guidance, "t1")).resolves.toMatchObject({ turnId: "t1" });
		expect(steerTurn.mock.calls.map((call) => call[4].idempotencyKey)).toEqual(["steer-one", "steer-one"]);
	});

	it("rejects an account change while waiting without accepting a late provider result", async () => {
		const { source, poll, changeAccount } = fixture();
		let finish!: (page: unknown) => void;
		poll.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
		const sending = source.steerMessage!("s1", guidance, "t1").then(() => undefined, (error: unknown) => error);
		await vi.advanceTimersByTimeAsync(0);
		changeAccount();
		expect(await sending).toMatchObject({ message: expect.stringMatching(/account changed/i) });
		finish({ events: [acknowledgement()], nextAfter: 8, hasMore: false });
		expect(cloudConversationReplayCache.get("steering-scope", "s1")).toBeUndefined();
	});

	it("rejects account changes after HTTP acceptance before starting acknowledgement reads", async () => {
		const { source, poll, steerTurn, changeAccount } = fixture();
		steerTurn.mockImplementation(async () => { changeAccount(); return { event: event(7, "chat.turn_steer_requested", { turnId: "t1", clientMessageId: "steer-one" }) }; });
		await expect(source.steerMessage!("s1", guidance, "t1")).rejects.toThrow(/account changed/i);
		expect(poll).not.toHaveBeenCalled();
	});

	it("rejects a response without an authoritative acceptance cursor", async () => {
		const { source, steerTurn, poll } = fixture();
		steerTurn.mockResolvedValue({ event: { type: "chat.turn_steer_requested" } });
		await expect(source.steerMessage!("s1", guidance, "t1")).rejects.toThrow(/provider.*unknown/i);
		expect(poll).not.toHaveBeenCalled();
	});
});
