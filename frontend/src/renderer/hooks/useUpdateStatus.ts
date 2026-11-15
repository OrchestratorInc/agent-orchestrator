import { useEffect, useRef, useState } from "react";
import type { UpdateStatus } from "../../main/update-settings";
import { displayableUpdateError } from "../../shared/update-error-message";
import { aoBridge } from "../lib/bridge";

let current: UpdateStatus = { state: "idle" };
const listeners = new Set<(status: UpdateStatus) => void>();
let reconcileUsers = 0;
let revision = 0;
let stop: (() => void) | undefined;
let downloadPending = false;

const ACTIVE_STATES = ["downloading", "preparing"];
const WAITING_STATES = ["available", "downloaded", "error"];
const POLL_MS = 3_000;
const WAITING_POLL_EVERY = 10;

function withBareVersion(status: UpdateStatus): UpdateStatus {
	return /^v\d/i.test(status.version ?? "") ? { ...status, version: status.version!.slice(1) } : status;
}

function receive(raw: UpdateStatus) {
	const status = withBareVersion(raw);
	current = status;
	revision += 1;
	for (const listener of listeners) listener(status);
}

function sameUpdateStatus(a: UpdateStatus, b: UpdateStatus): boolean {
	return a === b || JSON.stringify(a) === JSON.stringify(b);
}

function connect() {
	let live = true;
	let pending = false;
	let ticks = 0;
	const off = aoBridge.updates.onStatus(receive);
	const refresh = async () => {
		if (pending) return;
		pending = true;
		const requestedRevision = revision;
		try {
			const next = withBareVersion(await aoBridge.updates.getStatus());
			if (live && requestedRevision === revision && !sameUpdateStatus(next, current)) receive(next);
		} catch {
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

export async function requestUpdateDownload(requestId?: string): Promise<void> {
	if (downloadPending || current.state === "downloading" || current.state === "preparing") return;
	downloadPending = true;
	receive({ ...current, state: "downloading", percent: undefined, transferred: undefined, total: undefined, requestId });
	try {
		await aoBridge.updates.download(requestId);
	} catch (error) {
		if (["downloading"].includes(current.state)) {
			receive({ ...current, state: "error", message: displayableUpdateError(error instanceof Error ? error.message : undefined, "Download failed. Try again.") });
		}
	} finally {
		downloadPending = false;
	}
}
