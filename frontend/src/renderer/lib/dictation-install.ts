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

/**
 * What the mic button shows: the model being downloaded and how far along it is (0 to 1), or, after the
 * download, that the chosen model is still loading into memory.
 */
type Install = { model: string; fraction: number; preparing: boolean };
const idle: Install = { model: "", fraction: 0, preparing: false };
let install = idle;
let preparing = false;
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
	setInstall(status.installing ? { model: status.installing, fraction, preparing } : { ...idle, preparing });
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

function setInstall(next: Install) {
	if (next.model === install.model && next.fraction === install.fraction && next.preparing === install.preparing)
		return;
	install = next;
	for (const listener of listeners) listener();
}

function stopTracking(failure: string) {
	preparing = false;
	setInstall(idle);
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
		await untilModelLoaded();
	});
	const tracked = finished.finally(() => installs.delete(model));
	installs.set(model, tracked);
	return tracked;
}

/**
 * A freshly chosen model takes several seconds to load the first time. Dictation works meanwhile, but its first
 * result waits for the load, so "ready" is only announced once the daemon says the model is loaded. Gives up
 * after two minutes rather than keeping a download looking unfinished forever.
 */
async function untilModelLoaded() {
	preparing = true;
	setInstall({ ...install, preparing });
	try {
		const deadline = Date.now() + 120_000;
		while (Date.now() < deadline) {
			const { data, error } = await apiClient.GET("/api/v1/dictation");
			if (error || !data || !data.preparing) return;
			await new Promise((resolve) => setTimeout(resolve, 500));
		}
	} finally {
		preparing = false;
		setInstall({ ...install, preparing });
	}
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

export function useDictationInstall(): Install {
	return useSyncExternalStore(subscribe, () => install);
}

/** Forgets everything. For tests. */
export function resetDictationInstall() {
	clearTimeout(timer);
	timer = undefined;
	install = idle;
	preparing = false;
	waiters.clear();
	installs.clear();
	cancelling = false;
}
