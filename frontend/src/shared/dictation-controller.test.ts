import { describe, expect, it, vi } from "vitest";
import type { DictationEvent, DictationMessage } from "./dictation";
import { createDictationController } from "./dictation-controller";
import type { DictationTransport } from "./dictation-ipc";

function setup() {
	const listeners = new Set<(event: DictationMessage) => void>();
	const transport: DictationTransport = {
		start: vi.fn(async (_run: number) => true),
		stop: vi.fn(async () => undefined),
		cancel: vi.fn(async () => undefined),
		onEvent: (listener) => {
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
	};
	const surface = { onPhase: vi.fn(), onText: vi.fn(), onEnd: vi.fn(), onError: vi.fn() };
	const controller = createDictationController(transport, surface);
	const run = () => vi.mocked(transport.start).mock.calls.at(-1)?.[0] as number;
	const emit = (event: DictationEvent, to = run()) => {
		for (const listener of [...listeners]) listener({ ...event, run: to });
	};
	return { controller, emit, listeners, run, surface, transport };
}

describe("createDictationController", () => {
	it("streams a run's text and ends it with the final wording", () => {
		const { controller, emit, listeners, surface } = setup();
		controller.start();
		expect(controller.phase).toBe("starting");
		emit({ type: "listening" });
		emit({ type: "text", text: "hello" });
		emit({ type: "end", text: "hello there" });

		expect(surface.onText).toHaveBeenLastCalledWith("hello there");
		expect(surface.onEnd).toHaveBeenCalledWith(true);
		expect(controller.phase).toBe("idle");
		expect(listeners.size).toBe(0);
	});

	it("numbers each run and ignores a late event from an earlier one", () => {
		const { controller, emit, run, surface } = setup();
		controller.start();
		const first = run();
		controller.halt();
		controller.start();
		expect(run()).not.toBe(first);

		emit({ type: "superseded" }, first);
		emit({ type: "end", text: "stale" }, first);

		expect(controller.phase).toBe("starting");
		expect(surface.onText).not.toHaveBeenCalledWith("stale");
	});

	it.each([
		[{ type: "error", message: "No microphone." } as DictationEvent, "No microphone."],
		[{ type: "exit" } as DictationEvent, "Dictation stopped unexpectedly."],
	])("ends the run and reports %j", (event, message) => {
		const { controller, emit, surface } = setup();
		controller.start();
		emit(event);
		expect(controller.phase).toBe("idle");
		expect(surface.onError).toHaveBeenCalledWith(message);
	});

	it.each([
		["refuses", async () => false],
		["fails", async () => { throw new Error("no handler"); }],
	])("ends the run when main %s to start it", async (_name, answer) => {
		const { controller, surface, transport } = setup();
		vi.mocked(transport.start).mockImplementationOnce(answer);
		controller.start();
		await vi.waitFor(() => expect(controller.phase).toBe("idle"));
		expect(surface.onEnd).toHaveBeenCalledWith(true);
	});

	it("does not let a late refusal end the run started after it", async () => {
		const { controller, transport } = setup();
		let refuse!: (ok: boolean) => void;
		vi.mocked(transport.start).mockImplementationOnce(() => new Promise((resolve) => { refuse = resolve; }));
		controller.start();
		controller.halt();
		controller.start();
		refuse(false);
		await Promise.resolve();
		expect(controller.phase).toBe("starting");
	});

	it("ends quietly when another surface takes the mic", () => {
		const { controller, emit, surface, transport } = setup();
		controller.start();
		emit({ type: "superseded" });
		expect(surface.onEnd).toHaveBeenCalledWith(true);
		expect(surface.onError).not.toHaveBeenCalled();
		expect(transport.cancel).not.toHaveBeenCalled();
	});

	it("stops once, halts with a cancel, and disposes without ending the surface", () => {
		const { controller, surface, transport } = setup();
		controller.start();
		controller.stop();
		controller.stop();
		expect(transport.stop).toHaveBeenCalledTimes(1);
		expect(controller.phase).toBe("finishing");

		controller.halt();
		expect(transport.cancel).toHaveBeenCalledTimes(1);
		expect(surface.onEnd).toHaveBeenCalledTimes(1);

		controller.start();
		controller.dispose();
		expect(transport.cancel).toHaveBeenCalledTimes(2);
		expect(surface.onEnd).toHaveBeenCalledTimes(1);
		expect(controller.phase).toBe("idle");
	});
});
