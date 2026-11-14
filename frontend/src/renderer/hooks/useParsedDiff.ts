import { useEffect, useMemo, useState } from "react";
import { parseUnifiedDiff, type DiffRow } from "../lib/diff-parser";
import DiffParserWorker from "../workers/diff-parser.worker?worker";
import type { DiffParserRequest, DiffParserResponse } from "../workers/diff-parser.worker";

const WORKER_DIFF_THRESHOLD_CHARS = 50_000;

let sharedWorker: Worker | null = null;
let sharedWorkerFailed = false;
let nextRequestId = 0;
const pendingRequests = new Map<number, { resolve: (rows: DiffRow[]) => void; reject: (error: Error) => void }>();

function getSharedWorker(): Worker | null {
	if (sharedWorkerFailed || typeof Worker === "undefined") return null;
	if (sharedWorker) return sharedWorker;
	try {
		const worker = new DiffParserWorker();
		worker.addEventListener("message", (event: MessageEvent<DiffParserResponse>) => {
			const entry = pendingRequests.get(event.data.id);
			if (!entry) return;
			pendingRequests.delete(event.data.id);
			if (event.data.ok) entry.resolve(event.data.rows);
			else entry.reject(new Error(event.data.error));
		});
		worker.addEventListener("error", () => {
			sharedWorkerFailed = true;
			for (const [id, entry] of pendingRequests) {
				entry.reject(new Error("diff parser worker failed"));
				pendingRequests.delete(id);
			}
		});
		sharedWorker = worker;
	} catch {
		sharedWorkerFailed = true;
	}
	return sharedWorker;
}

function parseInWorker(diff: string): Promise<DiffRow[]> {
	const worker = getSharedWorker();
	if (!worker) return Promise.reject(new Error("diff parser worker unavailable"));
	return new Promise((resolve, reject) => {
		const id = nextRequestId++;
		pendingRequests.set(id, { resolve, reject });
		const request: DiffParserRequest = { id, diff };
		worker.postMessage(request);
	});
}

export type ParsedDiffResult = {
	rows: DiffRow[];
	pending: boolean;
};

export function useParsedDiff(diff: string): ParsedDiffResult {
	const isSmall = diff.length < WORKER_DIFF_THRESHOLD_CHARS;
	const syncRows = useMemo(() => (isSmall ? parseUnifiedDiff(diff) : null), [diff, isSmall]);
	const [asyncResult, setAsyncResult] = useState<{ diff: string; rows: DiffRow[] } | null>(null);

	useEffect(() => {
		if (isSmall) return;
		let cancelled = false;
		parseInWorker(diff)
			.catch(() => parseUnifiedDiff(diff))
			.then((rows) => {
				if (cancelled) return;
				setAsyncResult({ diff, rows });
			});
		return () => {
			cancelled = true;
		};
	}, [diff, isSmall]);

	if (isSmall) return { rows: syncRows ?? [], pending: false };
	if (asyncResult?.diff === diff) return { rows: asyncResult.rows, pending: false };
	return { rows: [], pending: true };
}
