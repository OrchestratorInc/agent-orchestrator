import { createEngine, type CodeEngine } from "./code-highlight-engine";
import type { GrammarName } from "./code-highlight";
import type { HighlightWorkerRequest, HighlightWorkerResponse } from "./code-highlight-protocol";

const MAX_STREAMS = 16;
const MAX_STREAM_CHARS = 100_000;
const MAX_TOTAL_CHARS = 1_000_000;
const streams = new Map<number, {
	language: GrammarName;
	length: number;
	append: ReturnType<CodeEngine["createStream"]>;
}>();
let engine: Promise<CodeEngine> | undefined;
let queue = Promise.resolve();

async function handle(request: HighlightWorkerRequest): Promise<void> {
	if (request.kind === "release") {
		streams.delete(request.streamId);
		return;
	}
	let response: HighlightWorkerResponse;
	try {
		const ready = await (engine ??= createEngine());
		if (request.kind === "highlight") {
			response = { kind: "highlight", id: request.id, tree: ready.highlight(request.language, request.code) };
		} else {
			if (request.reset) streams.delete(request.streamId);
			let state = streams.get(request.streamId);
			if ((!state && request.offset !== 0) || (state && (state.length !== request.offset || state.language !== request.language))) {
				response = { kind: "reset", id: request.id };
			} else if (request.offset + request.code.length > MAX_STREAM_CHARS || !request.code.endsWith("\n")) {
				response = { kind: "unavailable", id: request.id };
			} else {
				state ??= { language: request.language, length: 0, append: ready.createStream(request.language) };
				streams.delete(request.streamId);
				let retained = state.length + request.code.length;
				for (const item of streams.values()) retained += item.length;
				while (streams.size >= MAX_STREAMS || retained > MAX_TOTAL_CHARS) {
					const oldest = streams.keys().next().value;
					if (oldest === undefined) break;
					retained -= streams.get(oldest)?.length ?? 0;
					streams.delete(oldest);
				}
				const lines = await state.append(request.code);
				state.length += request.code.length;
				streams.set(request.streamId, state);
				response = { kind: "stream", id: request.id, lines };
			}
		}
	} catch {
		if (request.kind === "stream") streams.delete(request.streamId);
		response = { kind: "unavailable", id: request.id };
	}
	self.postMessage(response);
}

self.onmessage = (event: MessageEvent<HighlightWorkerRequest>) => {
	// Serialize append/reset/release across asynchronous grammar initialization.
	// Each renderer fence coalesces independently before it reaches this queue.
	queue = queue.then(() => handle(event.data));
	return queue;
};
