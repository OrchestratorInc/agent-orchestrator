import { createEngine, type CodeEngine } from "./code-highlight-engine";
import type { HighlightWorkerRequest, HighlightWorkerResponse } from "./code-highlight-protocol";

let engine: Promise<CodeEngine> | undefined;

self.onmessage = async (event: MessageEvent<HighlightWorkerRequest>) => {
	const request = event.data;
	let response: HighlightWorkerResponse;
	try {
		const ready = await (engine ??= createEngine());
		response = { kind: "highlight", id: request.id, tree: ready.highlight(request.language, request.code) };
	} catch {
		response = { kind: "unavailable", id: request.id };
	}
	self.postMessage(response);
};
