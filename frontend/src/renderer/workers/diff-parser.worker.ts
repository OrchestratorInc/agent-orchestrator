import { parseUnifiedDiff, type DiffRow } from "../lib/diff-parser";

export type DiffParserRequest = {
	id: number;
	diff: string;
};

export type DiffParserResponse = { id: number; ok: true; rows: DiffRow[] } | { id: number; ok: false; error: string };

const worker = self as unknown as Worker;

worker.addEventListener("message", (event: MessageEvent<DiffParserRequest>) => {
	const { id, diff } = event.data;
	try {
		const rows = parseUnifiedDiff(diff);
		const response: DiffParserResponse = { id, ok: true, rows };
		worker.postMessage(response);
	} catch (error) {
		const response: DiffParserResponse = {
			id,
			ok: false,
			error: error instanceof Error ? error.message : String(error),
		};
		worker.postMessage(response);
	}
});
