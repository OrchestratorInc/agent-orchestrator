import { apiErrorException } from "../lib/api-error-exception";
import { useCallback, useEffect, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";

type PreparedClone = components["schemas"]["ClonePreparationResult"];
export type CloneProgress = components["schemas"]["CloneProgress"];
type ClonePrepareStreamEvent = components["schemas"]["ClonePrepareStreamEvent"];
type DaemonClient = ReturnType<typeof clientForSessionHost>;

export function usePreparedClone(hostId?: string) {
	const currentRef = useRef<PreparedClone | null>(null);
	const inputRef = useRef<{ remoteUrl: string; destinationParent: string } | null>(null);
	const [isCleaning, setIsCleaning] = useState(false);
	const activeRequest = useRef<AbortController | null>(null);
	const supportsCheckoutRelease = useRef(!hostId);

	useEffect(() => {
		supportsCheckoutRelease.current = !hostId;
		return () => {
			activeRequest.current?.abort();
			currentRef.current = null;
			inputRef.current = null;
		};
	}, [hostId]);

	const checkDestination = useCallback(async (input: { remoteUrl: string; destinationParent: string }, signal: AbortSignal) => {
		const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/projects/clone/check", { body: input, signal });
		if (signal.aborted) throw new DOMException("Cancelled", "AbortError");
		if (response?.status === 404 || response?.status === 405) {
			supportsCheckoutRelease.current = false;
			// Older daemons lack this optional preflight. Keep the dialog's path
			// preview and let prepare enforce destination conflicts as before.
			return { remoteUrl: input.remoteUrl, path: "", exists: false, canUseExisting: false };
		}
		if (error) throw error;
		if (!data) throw new Error("Could not check the destination. Retry or choose another folder.");
		supportsCheckoutRelease.current = true;
		const owned = currentRef.current;
		if (owned && owned.path === data.path && owned.remoteUrl === data.remoteUrl && inputRef.current?.destinationParent === input.destinationParent) {
			return { ...data, exists: false, canUseExisting: false };
		}
		return data;
	}, [hostId]);

	const prepare = useCallback(async (remoteUrl: string, destinationParent: string, onProgress?: (progress: CloneProgress) => void) => {
		const controller = new AbortController();
		activeRequest.current = controller;
		try {
			const client = hostId ? clientForSessionHost(hostId) : apiClient;
			const body = { remoteUrl, destinationParent };
			const data = (onProgress ? await prepareCloneStream(client, body, controller.signal, onProgress) : null)
				?? await prepareCloneOnce(client, body, controller.signal);
			if (controller.signal.aborted) throw new DOMException("Cancelled", "AbortError");
			currentRef.current = data;
			inputRef.current = { remoteUrl, destinationParent };
			return data;
		} finally {
			if (activeRequest.current === controller) activeRequest.current = null;
		}
	}, [hostId]);

	const matches = useCallback((input: { remoteUrl: string; destinationParent: string }) =>
		inputRef.current?.remoteUrl === input.remoteUrl && inputRef.current.destinationParent === input.destinationParent, []);

	const cancel = useCallback(() => activeRequest.current?.abort(), []);

	const cleanup = useCallback(async () => {
		const current = currentRef.current;
		if (!current) return;
		if (!supportsCheckoutRelease.current) {
			// Legacy daemons cannot release ownership without deleting files.
			// Preserve the checkout rather than send an unsupported keep flag.
			currentRef.current = null;
			inputRef.current = null;
			return;
		}
		setIsCleaning(true);
		try {
			const { error } = await (hostId ? clientForSessionHost(hostId) : apiClient).POST("/api/v1/projects/clone/cleanup", {
				body: { path: current.path, preparationId: current.preparationId, keepCheckout: true },
			});
			if (error) throw apiErrorException(error, "Could not keep the cloned checkout");
			currentRef.current = null;
			inputRef.current = null;
		} finally {
			setIsCleaning(false);
		}
	}, [hostId]);

	const complete = useCallback(() => {
		currentRef.current = null;
		inputRef.current = null;
	}, []);

	const current = useCallback(() => currentRef.current, []);

	return { cancel, checkDestination, cleanup, complete, current, isCleaning, matches, prepare };
}

async function prepareCloneOnce(client: DaemonClient, body: { remoteUrl: string; destinationParent: string }, signal: AbortSignal): Promise<PreparedClone> {
	const { data, error } = await client.POST("/api/v1/projects/clone/prepare", { body, signal });
	if (error) throw error;
	if (!data) throw new Error("Could not clone repository");
	return data;
}

/**
 * Clones through the streaming route, reporting each git step as it starts.
 * Resolves null when the daemon predates the route, so the caller can clone
 * through the JSON route instead.
 */
async function prepareCloneStream(
	client: DaemonClient,
	body: { remoteUrl: string; destinationParent: string },
	signal: AbortSignal,
	onProgress: (progress: CloneProgress) => void,
): Promise<PreparedClone | null> {
	const { data: stream, error, response } = await client.POST("/api/v1/projects/clone/prepare/stream", { body, signal, parseAs: "stream" });
	if (!response || response.status === 404 || response.status === 405) return null;
	if (error) throw error;
	if (!(stream instanceof ReadableStream)) return null;
	const reader = stream.pipeThrough(new TextDecoderStream()).getReader();
	let buffered = "";
	try {
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			buffered += value;
			let boundary = buffered.indexOf("\n\n");
			while (boundary >= 0) {
				const frame = buffered.slice(0, boundary);
				buffered = buffered.slice(boundary + 2);
				boundary = buffered.indexOf("\n\n");
				const data = frame.split("\n").filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trimStart()).join("\n");
				if (!data) continue;
				const event = JSON.parse(data) as ClonePrepareStreamEvent;
				if (event.error) throw event.error;
				if (event.result) return event.result;
				if (event.progress) onProgress(event.progress);
			}
		}
	} catch (err) {
		if (signal.aborted) throw new DOMException("Cancelled", "AbortError");
		throw err;
	} finally {
		// The daemon closes after its last event; this only drops a stream we stop reading early.
		void reader.cancel().catch(() => undefined);
	}
	if (signal.aborted) throw new DOMException("Cancelled", "AbortError");
	throw new Error("Could not clone repository");
}
