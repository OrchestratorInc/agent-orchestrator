import { useEffect, useRef, useState } from "react";
import type { UpdateStatus } from "../../main/update-settings";
import { aoBridge } from "../lib/bridge";

let current: UpdateStatus = { state: "idle" };
const listeners = new Set<(status: UpdateStatus) => void>();
let reconcileUsers = 0;
let revision = 0;
let stop: (() => void) | undefined;
let downloadPending = false;

// Fast reconciliation only while progress is moving or a surface asked for it.
// The waiting states can last all day (an update sitting downloaded), and main
// already pushes every change, so they only need an occasional safety read.
const ACTIVE_STATES = ["downloading", "preparing"];
const WAITING_STATES = ["available", "downloaded", "error"];
const POLL_MS = 3_000;
const WAITING_POLL_EVERY = 10; // every 30s

function receive(status: UpdateStatus) {
	current = status;
	revision += 1;
	for (const listener of listeners) listener(status);
}

/** Small plain IPC object built by one producer; JSON equality is exact enough. */
function sameUpdateStatus(a: UpdateStatus, b: UpdateStatus): boolean {
	return a === b || JSON.stringify(a) === JSON.stringify(b);
}

function connect() {
	let live = true;
	let pending = false;
	let ticks = 0;
	// Subscribe before reading. A late local snapshot must never replace a push.
	const off = aoBridge.updates.onStatus(receive);
	const refresh = async () => {
		if (pending) return;
		pending = true;
		const requestedRevision = revision;
		try {
			const next = await aoBridge.updates.getStatus();
			// An unchanged poll result would re-render every subscriber (the Sidebar).
			if (live && requestedRevision === revision && !sameUpdateStatus(next, current)) receive(next);
		} catch {
			// Retain the last known status and retry the local read next time.
		} finally {
			pending = false;
		}
	};
	void refresh();
	const timer = setInterval(() => {
		ticks += 1;
		if (reconcileUsers > 0 || ACTIVE_STATES.includes(current.state)) void refresh();
		else if (WAITING_STATES.includes(current.state) && ticks % WAITING_POLL_EVERY === 0) void refresh();
	}, POLL_MS);
	return () => {
		live = false;
		clearInterval(timer);
		off?.();
	};
}

/** One local status stream shared by the sidebar, Settings and confirmation. */
export function useUpdateStatus(onStatusEvent?: (status: UpdateStatus) => void, reconcile = false): UpdateStatus {
	const [status, setStatus] = useState(current);
	const callback = useRef(onStatusEvent);
	callback.current = onStatusEvent;
	useEffect(() => {
		const listener = (next: UpdateStatus) => {
			callback.current?.(next);
			setStatus(next);
		};
		listeners.add(listener);
		if (reconcile) reconcileUsers += 1;
		listener(current);
		if (!stop) stop = connect();
		return () => {
			listeners.delete(listener);
			if (reconcile) reconcileUsers -= 1;
			if (listeners.size === 0) {
				stop?.();
				stop = undefined;
				current = { state: "idle" };
			}
		};
	}, [reconcile]);
	return status;
}

/** Show acknowledgment in every surface before the main process receives IPC. */
export async function requestUpdateDownload(requestId?: string): Promise<void> {
	if (downloadPending || current.state === "downloading" || current.state === "preparing") return;
	downloadPending = true;
	receive({ ...current, state: "downloading", percent: undefined, transferred: undefined, total: undefined, requestId });
	try {
		await aoBridge.updates.download(requestId);
	} catch (error) {
		if (["downloading"].includes(current.state)) {
			receive({ ...current, state: "error", message: error instanceof Error ? error.message : "Download failed. Try again." });
		}
	} finally {
		downloadPending = false;
	}
}
