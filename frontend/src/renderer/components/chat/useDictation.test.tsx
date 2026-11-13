import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DictationEvent } from "../../../shared/dictation";
import { useDictation } from "./useDictation";

const sound = vi.hoisted(() => ({ play: vi.fn() }));
vi.mock("../../lib/dictation-sound", () => ({ playDictationSound: sound.play }));

const bridge = vi.hoisted(() => ({
	listeners: new Set<(event: DictationEvent) => void>(),
	start: vi.fn(async () => undefined),
	stop: vi.fn(async () => undefined),
	cancel: vi.fn(async () => undefined),
}));
vi.mock("../../lib/dictation-client", () => ({
	dictationClient: {
		start: bridge.start,
		stop: bridge.stop,
		cancel: bridge.cancel,
		onEvent: (listener: (event: DictationEvent) => void) => {
			bridge.listeners.add(listener);
			return () => bridge.listeners.delete(listener);
		},
	},
}));

function emit(event: DictationEvent) {
	act(() => {
		for (const listener of bridge.listeners) listener(event);
	});
}

beforeEach(() => {
	vi.clearAllMocks();
});
afterEach(() => {
	cleanup();
	bridge.listeners.clear();
});

function record() {
	const target = { onText: vi.fn(), onEnd: vi.fn() };
	const hook = renderHook(({ ownerKey }) => useDictation({ ...target, ownerKey }), {
		initialProps: { ownerKey: "chat-a" },
	});
	act(() => hook.result.current.toggle());
	emit({ type: "listening" });
	return { ...hook, target };
}

function finish(dictation: ReturnType<typeof useDictation>): Promise<boolean> | null {
	let completion: Promise<boolean> | null = null;
	act(() => {
		completion = dictation.finish();
	});
	return completion;
}

describe("dictation completion before submission", () => {
	it("leaves ordinary submission synchronous when no recording is active", () => {
		const { result } = renderHook(() => useDictation({ onText: vi.fn(), onEnd: vi.fn() }));
		expect(result.current.finish()).toBeNull();
		expect(bridge.stop).not.toHaveBeenCalled();
	});

	it("never shows interim words: the text arrives once, when recognition finishes", () => {
		const { target } = record();
		emit({ type: "text", text: "change for" });
		emit({ type: "text", text: "change forty" });
		expect(target.onText).not.toHaveBeenCalled();
		emit({ type: "end", text: "Change 42." });
		expect(target.onText).toHaveBeenCalledExactlyOnceWith("Change 42.");
	});

	it("waits through interim results and delivers the final text before allowing send", async () => {
		const { result, target } = record();
		emit({ type: "text", text: "change forty" });
		const submit = vi.fn();
		const completion = finish(result.current);
		const sent = completion?.then((completed) => {
			if (completed) submit(target.onText.mock.lastCall?.[0]);
		});
		await act(async () => {
			await Promise.resolve();
		});
		expect(submit).not.toHaveBeenCalled();
		emit({ type: "text", text: "change forty two" });
		expect(submit).not.toHaveBeenCalled();
		emit({ type: "end", text: "Change 42." });
		await sent;
		expect(submit).toHaveBeenCalledExactlyOnceWith("Change 42.");
		expect(target.onEnd).toHaveBeenCalledWith(true);
	});

	it("requests native stop once when several callers await finalization", async () => {
		const { result } = record();
		const completions = [finish(result.current), finish(result.current)];
		expect(bridge.stop).toHaveBeenCalledTimes(1);
		emit({ type: "end", text: "Done." });
		await expect(Promise.all(completions)).resolves.toEqual([true, true]);
	});

	it.each<DictationEvent>([{ type: "error", message: "Microphone disconnected." }, { type: "exit" }])(
		"preserves draft text but cancels send after $type",
		async (event) => {
			const { result, target } = record();
			const completion = finish(result.current);
			emit(event);
			await expect(completion).resolves.toBe(false);
			expect(target.onEnd).toHaveBeenCalledWith(true);
		},
	);

	it("cancels a queued send when typing takes over", async () => {
		const { result } = record();
		const completion = finish(result.current);
		act(() => result.current.detach());
		emit({ type: "end", text: "Late result" });
		await expect(completion).resolves.toBe(false);
	});

	it("cancels a queued send when the composer unmounts", async () => {
		const { result, unmount } = record();
		const completion = finish(result.current);
		unmount();
		await expect(completion).resolves.toBe(false);
		expect(bridge.cancel).toHaveBeenCalledTimes(1);
	});

	it("cancels a queued send when the same composer changes chats", async () => {
		const { result, rerender, target } = record();
		const completion = finish(result.current);
		rerender({ ownerKey: "chat-b" });
		await expect(completion).resolves.toBe(false);
		expect(result.current.phase).toBe("idle");
		expect(target.onEnd).toHaveBeenCalledWith(true);
	});

	it("cancels the old send when another composer starts recording", async () => {
		const first = record();
		const completion = finish(first.result.current);
		record();
		await expect(completion).resolves.toBe(false);
	});

	it("settles a queued send when stopping through IPC fails", async () => {
		const { result } = record();
		bridge.stop.mockRejectedValueOnce(new Error("IPC disconnected"));
		const completion = finish(result.current);
		await act(async () => {
			await completion;
		});
		await expect(completion).resolves.toBe(false);
		expect(result.current.error).toBe("Could not finish dictation.");
	});
});

describe("dictation sounds", () => {
	it("plays the opening click as dictation starts, before the microphone opens", () => {
		const target = { onText: vi.fn(), onEnd: vi.fn() };
		const { result } = renderHook(() => useDictation(target));
		const order: string[] = [];
		sound.play.mockImplementation((kind: string) => order.push(`sound:${kind}`));
		bridge.start.mockImplementationOnce(async () => void order.push("start"));
		act(() => result.current.toggle());
		expect(order).toEqual(["sound:on", "start"]);
	});

	it("plays the closing click only after the microphone has been released", async () => {
		const { result } = record();
		sound.play.mockClear();
		let release!: () => void;
		bridge.stop.mockImplementationOnce(() => new Promise<undefined>((resolve) => (release = () => resolve(undefined))));
		act(() => result.current.toggle());
		expect(sound.play).not.toHaveBeenCalled();
		await act(async () => release());
		expect(sound.play).toHaveBeenCalledExactlyOnceWith("off");
	});

	it("plays the closing click when Escape cancels, and stays silent if the microphone fails to start", async () => {
		record();
		sound.play.mockClear();
		act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
		expect(sound.play).toHaveBeenCalledExactlyOnceWith("off");
		cleanup();
		bridge.listeners.clear();
		sound.play.mockClear();
		bridge.start.mockRejectedValueOnce(new Error("No microphone"));
		const failing = renderHook(() => useDictation({ onText: vi.fn(), onEnd: vi.fn() }));
		await act(async () => failing.result.current.toggle());
		expect(sound.play.mock.calls.map(([kind]) => kind)).toEqual(["on"]);
	});
});

it("Escape discards dictation and cancels a queued send", async () => {
	const { result, target } = record();
	emit({ type: "text", text: "Discard this" });
	const completion = finish(result.current);
	act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
	await expect(completion).resolves.toBe(false);
	expect(target.onEnd).toHaveBeenLastCalledWith(false);
	expect(bridge.cancel).toHaveBeenCalled();
});

describe("handing dictation from one composer to another", () => {
	it("lets the recording finish and deliver its words before the next composer starts", async () => {
		const first = record();
		const secondTarget = { onText: vi.fn(), onEnd: vi.fn() };
		const second = renderHook(() => useDictation({ ...secondTarget, ownerKey: "chat-b" }));
		bridge.start.mockClear();
		act(() => second.result.current.toggle());
		// The first recording is told to finish; the second waits for it instead of cutting it off.
		expect(bridge.stop).toHaveBeenCalledTimes(1);
		expect(bridge.start).not.toHaveBeenCalled();
		await act(async () => {
			for (const listener of [...bridge.listeners]) listener({ type: "end", text: "First composer's words." });
		});
		expect(first.target.onText).toHaveBeenCalledExactlyOnceWith("First composer's words.");
		expect(first.target.onEnd).toHaveBeenCalledWith(true);
		expect(bridge.start).toHaveBeenCalledTimes(1);
	});

	it("does not wait forever for a recording that never finishes", async () => {
		vi.useFakeTimers();
		try {
			const first = record();
			const second = renderHook(() => useDictation({ onText: vi.fn(), onEnd: vi.fn(), ownerKey: "chat-b" }));
			bridge.start.mockClear();
			act(() => second.result.current.toggle());
			expect(bridge.start).not.toHaveBeenCalled();
			await act(async () => {
				await vi.advanceTimersByTimeAsync(8000);
			});
			expect(bridge.start).toHaveBeenCalledTimes(1);
			expect(first.target.onEnd).toHaveBeenCalledWith(true);
		} finally {
			vi.useRealTimers();
		}
	});
});
