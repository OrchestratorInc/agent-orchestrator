import { afterEach, describe, expect, it, vi } from "vitest";
import { createEngine } from "./code-highlight-engine";

class FakeWorker {
	static instances: FakeWorker[] = [];
	onmessage: ((event: MessageEvent) => void) | null = null;
	onerror: (() => void) | null = null;
	onmessageerror: (() => void) | null = null;
	postMessage = vi.fn();
	terminate = vi.fn();
	constructor() { FakeWorker.instances.push(this); }
}
const engine = await createEngine();
const code = 'const message = "hello 👋";\n'.repeat(900);
async function setup() {
	vi.resetModules();
	FakeWorker.instances = [];
	vi.stubGlobal("Worker", FakeWorker);
	return import("./code-highlight");
}
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("large code worker highlighting", () => {
	it("starts lazily, shares requests and caches exactly the engine's tree", async () => {
		const api = await setup();
		expect(api.canonicalLanguage("text")).toBeUndefined();
		await api.highlight("const x = 1", "javascript");
		expect(api.highlightSync("const y = 2", "javascript")).toBeDefined();
		expect(FakeWorker.instances).toHaveLength(0);
		const first = api.highlight(code, "javascript");
		const duplicate = api.highlight(code, "javascript");
		const worker = FakeWorker.instances[0];
		expect(worker).toBeDefined();
		expect(worker.postMessage).toHaveBeenCalledTimes(1);
		const request = worker.postMessage.mock.calls[0][0];
		const tree = engine.highlight(request.language, request.code);
		worker.onmessage!(new MessageEvent("message", { data: { kind: "highlight", id: request.id, tree } }));
		expect(await first).toEqual(tree);
		expect(await duplicate).toBe(await first);
		expect(api.highlightSync(code, "javascript")).toBe(await first);
	});

	it("sends only appended lines, retains completed tokens and avoids the settled cache", async () => {
		const api = await setup();
		const stream = api.createCodeHighlightStream();
		const first = stream.update(code, "javascript");
		const worker = FakeWorker.instances[0];
		const request = worker.postMessage.mock.calls[0][0];
		const append = engine.createStream("javascript");
		const lines = await append(code);
		worker.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: request.id, lines } }));
		const initial = await first;
		const chunk = 'const next = "new";\n';
		const second = stream.update(code + chunk, "javascript");
		const next = worker.postMessage.mock.calls[1][0];
		expect(next).toMatchObject({ kind: "stream", code: chunk, offset: code.length, reset: false, streamId: request.streamId });
		worker.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: next.id, lines: await append(chunk) } }));
		expect((await second)?.lines[0]).toBe(initial?.lines[0]);
		expect(api.highlightSync(code, "javascript")).toBeUndefined();
		expect(await stream.update("x".repeat(100_001), "javascript")).toBeUndefined();
		stream.dispose();
		expect(worker.postMessage).toHaveBeenLastCalledWith({ kind: "release", streamId: request.streamId });
	});

	it("replays the full prefix once if the worker evicted its stream", async () => {
		const api = await setup();
		const stream = api.createCodeHighlightStream();
		const first = stream.update("const a = 1;\n", "javascript");
		const worker = FakeWorker.instances[0];
		const request = worker.postMessage.mock.calls[0][0];
		worker.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: request.id, lines: [] } }));
		await first;
		const source = "const a = 1;\nconst b = 2;\n";
		const next = stream.update(source, "javascript");
		const delta = worker.postMessage.mock.calls[1][0];
		worker.onmessage!(new MessageEvent("message", { data: { kind: "reset", id: delta.id } }));
		await vi.waitFor(() => expect(worker.postMessage).toHaveBeenCalledTimes(3));
		const replay = worker.postMessage.mock.calls[2][0];
		expect(replay).toMatchObject({ code: source, offset: 0, reset: true });
		worker.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: replay.id, lines: await engine.createStream("javascript")(source) } }));
		expect((await next)?.source).toBe(source);
		stream.dispose();
	});

	it("does not recreate a disposed stream when an in-flight append requests a replay", async () => {
		const api = await setup();
		const stream = api.createCodeHighlightStream();
		const first = stream.update("const a = 1;\n", "javascript");
		const worker = FakeWorker.instances[0];
		const initial = worker.postMessage.mock.calls[0][0];
		worker.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: initial.id, lines: [] } }));
		await first;
		const pending = stream.update("const a = 1;\nconst b = 2;\n", "javascript");
		const append = worker.postMessage.mock.calls[1][0];
		stream.dispose();
		expect(worker.postMessage).toHaveBeenLastCalledWith({ kind: "release", streamId: initial.streamId });
		worker.onmessage!(new MessageEvent("message", { data: { kind: "reset", id: append.id } }));
		expect(await pending).toBeUndefined();
		expect(worker.postMessage).toHaveBeenCalledTimes(3);
	});

	it("resets stream state on edits and language changes and ignores results after disposal", async () => {
		const api = await setup();
		const stream = api.createCodeHighlightStream();
		const first = stream.update("const old = 1;\n", "javascript");
		const worker = FakeWorker.instances[0];
		let request = worker.postMessage.mock.calls[0][0];
		worker.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: request.id, lines: [] } }));
		await first;
		const edited = stream.update("const replacement = 2;\n", "javascript");
		request = worker.postMessage.mock.calls[1][0];
		expect(request.reset).toBe(true);
		worker.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: request.id, lines: [] } }));
		await edited;
		const language = stream.update("const replacement = 2;\n", "python");
		request = worker.postMessage.mock.calls[2][0];
		expect(request).toMatchObject({ reset: true, offset: 0, language: "python" });
		stream.dispose();
		worker.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: request.id, lines: [] } }));
		expect(await language).toBeUndefined();
	});

	it("keeps text readable when workers are unsupported", async () => {
		const api = await setup();
		vi.stubGlobal("Worker", undefined);
		expect(await api.highlight(code, "javascript")).toBeUndefined();
	});

	it.each(["onerror", "onmessageerror"] as const)("settles all queued requests on %s and avoids restart loops", async (event) => {
		const api = await setup();
		const first = api.highlight(code, "javascript");
		const second = api.highlight(code + "\n", "javascript");
		const worker = FakeWorker.instances[0];
		worker[event]!();
		expect(await first).toBeUndefined();
		expect(await second).toBeUndefined();
		expect(worker.terminate).toHaveBeenCalledOnce();
		expect(await api.highlight(code, "javascript")).toBeUndefined();
		expect(FakeWorker.instances).toHaveLength(1);
	});

	it("bounds queued requests and recovers capacity as results arrive", async () => {
		const api = await setup();
		const requests = Array.from({ length: 32 }, (_, i) => api.highlight(code + i, "javascript"));
		const worker = FakeWorker.instances[0];
		expect(await api.highlight(code + "overflow", "javascript")).toBeUndefined();
		expect(worker.postMessage).toHaveBeenCalledTimes(32);
		const { id } = worker.postMessage.mock.calls[0][0];
		worker.onmessage!(new MessageEvent("message", { data: { kind: "unavailable", id } }));
		const replacement = api.highlight(code + "replacement", "javascript");
		expect(worker.postMessage).toHaveBeenCalledTimes(33);
		worker.onerror!();
		await Promise.all([...requests, replacement]);
	});

	it("cleans up a failed postMessage without rejecting the caller", async () => {
		const api = await setup();
		const pending = api.highlight(code, "javascript");
		const worker = FakeWorker.instances[0];
		worker.postMessage.mockImplementationOnce(() => { throw new Error("clone failed"); });
		expect(await api.highlight(code + "second", "javascript")).toBeUndefined();
		expect(await pending).toBeUndefined();
		expect(worker.terminate).toHaveBeenCalledOnce();
	});

	it("times out stuck parsing and bounds source queued for the worker", async () => {
		vi.useFakeTimers();
		const api = await setup();
		expect(await api.highlight("x".repeat(1_000_001), "javascript")).toBeUndefined();
		expect(FakeWorker.instances).toHaveLength(0);
		const pending = api.highlight(code, "javascript");
		await vi.advanceTimersByTimeAsync(10_000);
		expect(await pending).toBeUndefined();
		expect(FakeWorker.instances[0].terminate).toHaveBeenCalledOnce();
	});
});

it("the worker serializes initialization, releases grammar state and preserves source", async () => {
	vi.resetModules();
	const scope = { onmessage: null as ((event: MessageEvent) => Promise<void>) | null, postMessage: vi.fn() };
	vi.stubGlobal("self", scope);
	await import("./code-highlight.worker");
	await scope.onmessage!(new MessageEvent("message", { data: { kind: "highlight", id: 7, code, language: "javascript" } }));
	expect(scope.postMessage.mock.calls[0][0]).toEqual({ kind: "highlight", id: 7, tree: engine.highlight("javascript", code) });
	await scope.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: 8, streamId: 1, offset: 0, reset: true, code: "/* open\n", language: "javascript" } }));
	await scope.onmessage!(new MessageEvent("message", { data: { kind: "release", streamId: 1 } }));
	await scope.onmessage!(new MessageEvent("message", { data: { kind: "stream", id: 9, streamId: 1, offset: 8, reset: false, code: "continued */\n", language: "javascript" } }));
	expect(scope.postMessage.mock.calls.at(-1)?.[0]).toEqual({ kind: "reset", id: 9 });
	await scope.onmessage!(new MessageEvent("message", { data: { kind: "highlight", id: 10, code, language: "unknown" } }));
	expect(scope.postMessage.mock.calls.at(-1)?.[0]).toEqual({ kind: "unavailable", id: 10 });
});
