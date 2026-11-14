import type { DaemonFailureCode, DaemonStatus } from "../shared/daemon-status";
import type { IpcMain, IpcMainInvokeEvent } from "electron";

export const MAIN_ERROR_CATEGORIES = ["daemon", "update", "ipc", "window", "acp_runtime", "side_chat"] as const;
export type MainErrorCategory = (typeof MAIN_ERROR_CATEGORIES)[number];

export const MAIN_ERROR_REASONS = [
	"binary_missing", "spawn_failed", "exited", "port_unconfirmed", "identity_mismatch", "datadir_unwritable",
	"check", "download", "stage", "restart_prep", "install", "return",
	"handler",
	"unresponsive", "did_fail_load",
	"rebuild",
	"recovery",
] as const;
export type MainErrorReason = (typeof MAIN_ERROR_REASONS)[number];

export type MainErrorCapture = {
	category: MainErrorCategory;
	reason: MainErrorReason;
	error?: unknown;
	detail?: string;
};

const CATEGORIES = new Set<string>(MAIN_ERROR_CATEGORIES);
const REASONS = new Set<string>(MAIN_ERROR_REASONS);
const SAFE_DETAIL = /^(?:signal:SIG[A-Z0-9]{1,10}|code:-?\d{1,5}|channel:[a-z][A-Za-z0-9:_-]{0,63}|kind:[a-z-]{1,24})$/;
const PENDING_LIMIT = 20;

type Sink = (capture: MainErrorCapture) => void;

export class ExpectedIpcError extends Error {}

let sink: Sink | null = null;
let pending: MainErrorCapture[] = [];
let reported = new WeakSet<object>();
const MAX_BUDGET_KEYS = 1024;
let budgetDay = -1;
const budgets = new Map<string, { minute: number; minuteCount: number; dayCount: number }>();

export function isSafeMainErrorDetail(value: unknown): value is string {
	return typeof value === "string" && SAFE_DETAIL.test(value);
}

export function sanitizeMainErrorCapture(capture: MainErrorCapture): MainErrorCapture | null {
	if (!CATEGORIES.has(capture.category) || !REASONS.has(capture.reason)) return null;
	return {
		category: capture.category,
		reason: capture.reason,
		...(capture.error === undefined ? {} : { error: capture.error }),
		...(isSafeMainErrorDetail(capture.detail) ? { detail: capture.detail } : {}),
	};
}

export function setMainErrorSink(next: Sink | null): void {
	sink = next;
	if (!next) return;
	const held = pending;
	pending = [];
	for (const capture of held) {
		try { next(capture); } catch { }
	}
}

export function captureMainError(capture: MainErrorCapture): void {
	const safe = sanitizeMainErrorCapture(capture);
	if (!safe) return;
	if (safe.error instanceof ExpectedIpcError) return;
	if (safe.error && typeof safe.error === "object") {
		if (reported.has(safe.error)) return;
		reported.add(safe.error);
	}
	const now = Date.now();
	const minute = Math.floor(now / 60_000), day = Math.floor(now / 86_400_000);
	if (budgetDay !== day) { budgets.clear(); budgetDay = day; }
	const key = `${safe.category}|${safe.reason}|${safe.detail ?? ""}`;
	let budget = budgets.get(key);
	if (!budget) {
		if (budgets.size >= MAX_BUDGET_KEYS) return;
		budget = { minute, minuteCount: 0, dayCount: 0 };
		budgets.set(key, budget);
	}
	if (budget.minute !== minute) { budget.minute = minute; budget.minuteCount = 0; }
	if (budget.minuteCount >= 5 || budget.dayCount >= 200) return;
	budget.minuteCount++; budget.dayCount++;
	if (sink) {
		try { sink(safe); } catch { }
		return;
	}
	if (pending.length < PENDING_LIMIT) pending.push(safe);
}




let ipcCaptureInstalled = false;

export function installIpcErrorCapture(ipcMain: Pick<IpcMain, "handle">): void {
	if (ipcCaptureInstalled) return;
	ipcCaptureInstalled = true;
	const original = ipcMain.handle.bind(ipcMain);
	ipcMain.handle = (channel, listener) => {
		if (channel.startsWith("telemetry:")) return original(channel, listener);
		const wrapped = async (event: IpcMainInvokeEvent, ...args: unknown[]) => {
			try {
				return await listener(event, ...args);
			} catch (error) {
				if (!(error instanceof ExpectedIpcError)) captureMainError({ category: "ipc", reason: "handler", error, detail: `channel:${channel}` });
				throw error;
			}
		};
		return original(channel, wrapped);
	};
}

export function resetMainErrorCaptureForTests(): void {
	sink = null;
	pending = [];
	reported = new WeakSet<object>();
	budgets.clear();
	budgetDay = -1;
	ipcCaptureInstalled = false;
}

const REPORTED_DAEMON_FAILURES = new Set<DaemonFailureCode>(["binary_missing", "spawn_failed", "exited", "port_unconfirmed", "identity_mismatch", "datadir_unwritable"]);

export function reportDaemonFailure(previous: DaemonStatus, next: DaemonStatus): void {
	const code = next.code;
	if (!code || !REPORTED_DAEMON_FAILURES.has(code)) return;
	if (previous.code === code && previous.state === next.state) return;
	const detail = next.signal ? `signal:${next.signal}` : typeof next.exitCode === "number" ? `code:${next.exitCode}` : undefined;
	captureMainError({ category: "daemon", reason: code as MainErrorReason, ...(detail ? { detail } : {}) });
}
