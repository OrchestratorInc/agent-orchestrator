import { useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiClient } from "./api-client";

export type DictationStatus = components["schemas"]["DictationStatus"];

/** Set once the user has made a first-use choice, so the chooser is not shown again. */
export const dictationChooserKey = "ao.dictation.chooser.v2";
export function acknowledgeDictationChoice() {
	try {
		localStorage.setItem(dictationChooserKey, "seen");
	} catch {
		/* Storage can be unavailable in previews. */
	}
}

/** The user stopped the download themselves; nothing went wrong. */
export class DictationInstallCancelled extends Error {}

type Install = { model: string; fraction: number };
const idle: Install = { model: "", fraction: 0 };
let install = idle;
const listeners = new Set<() => void>();
type Waiter = {
	model: string;
	loadFailed: string;
	updateFailed: string;
	resolve(status: DictationStatus): void;
	reject(error: Error): void;
};
const waiters = new Set<Waiter>();
let timer: ReturnType<typeof setTimeout> | undefined;
let cancelling = false;

/**
 * Records the daemon's latest status. While a model is downloading it keeps
 * asking for the next one, so the mic button and the chooser stay current
 * without either of them owning the download.
 */
export function publishDictationStatus(status: DictationStatus) {
	const fraction = status.progressTotal > 0 ? Math.min(1, Math.max(0, status.progressDone / status.progressTotal)) : 0;
	const next = status.installing ? { model: status.installing, fraction } : idle;
	if (next.model !== install.model || next.fraction !== install.fraction) {
		install = next;
		for (const listener of listeners) listener();
	}
	for (const waiter of [...waiters]) {
		if (status.installing && status.installing !== waiter.model) {
			waiters.delete(waiter);
			waiter.reject(new Error(waiter.updateFailed));
		} else if (!status.installing) {
			waiters.delete(waiter);
			if (cancelling) waiter.reject(new DictationInstallCancelled());
			else waiter.resolve(status);
		}
	}
	if (waiters.size === 0) cancelling = false;
	if (status.installing) schedule();
}

function stopTracking(failure: string) {
	install = idle;
	for (const listener of listeners) listener();
	for (const waiter of [...waiters]) {
		waiters.delete(waiter);
		waiter.reject(new Error(failure || waiter.loadFailed));
	}
	cancelling = false;
}

function schedule() {
	if (timer) return;
	timer = setTimeout(() => {
		timer = undefined;
		void apiClient.GET("/api/v1/dictation").then(
			({ data, error }) => (data && !error ? publishDictationStatus(data) : stopTracking("")),
			() => stopTracking(""),
		);
	}, 500);
}

/** Asks the daemon how far a download is, once, so a reload or another window's download shows up. */
export async function refreshDictationStatus() {
	try {
		const { data, error } = await apiClient.GET("/api/v1/dictation");
		if (data && !error) publishDictationStatus(data);
	} catch {
		/* No daemon to ask: there is nothing to show. */
	}
}

const installs = new Map<string, Promise<void>>();

/**
 * Follows a download until the daemon is done with it, then selects the model.
 * The work belongs to the app, not to the dialog that asked for it: closing the
 * chooser or switching chats does not interrupt it, and asking again for the
 * same model joins the download that is already running.
 */
export function installDictationModel(
	model: string,
	initial: DictationStatus,
	texts: { loadFailed: string; updateFailed: string },
): Promise<void> {
	const running = installs.get(model);
	if (running) return running;
	const finished = new Promise<DictationStatus>((resolve, reject) => {
		waiters.add({ model, ...texts, resolve, reject });
		publishDictationStatus(initial);
	}).then(async (status) => {
		if (!status.models.some((entry) => entry.id === model && entry.installed)) {
			throw new Error(status.error || texts.updateFailed);
		}
		const { data, error } = await apiClient.POST("/api/v1/dictation", { body: { action: "select", model } });
		if (error || !data) throw new Error(error?.message ?? texts.updateFailed);
		publishDictationStatus(data);
		acknowledgeDictationChoice();
	});
	const tracked = finished.finally(() => installs.delete(model));
	installs.set(model, tracked);
	return tracked;
}

/** Stops the download in progress. Anyone waiting on it is told it was cancelled, not that it failed. */
export async function cancelDictationInstall(): Promise<DictationStatus> {
	cancelling = true;
	try {
		const { data, error } = await apiClient.POST("/api/v1/dictation", { body: { action: "cancel", model: "" } });
		if (error || !data) throw new Error(error?.message ?? "Could not cancel the download.");
		publishDictationStatus(data);
		return data;
	} finally {
		if (waiters.size === 0) cancelling = false;
	}
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

/** The model being downloaded, if any, and how far along the whole download is (0 to 1). */
export function useDictationInstall(): Install {
	return useSyncExternalStore(subscribe, () => install);
}

/** Forgets everything. For tests. */
export function resetDictationInstall() {
	clearTimeout(timer);
	timer = undefined;
	install = idle;
	waiters.clear();
	installs.clear();
	cancelling = false;
}
