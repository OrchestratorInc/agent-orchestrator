import { useCallback, useEffect, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";

type PreparedClone = components["schemas"]["ClonePreparationResult"];

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

	const prepare = useCallback(async (remoteUrl: string, destinationParent: string) => {
		const controller = new AbortController();
		activeRequest.current = controller;
		try {
			const { data, error } = await (hostId ? clientForSessionHost(hostId) : apiClient).POST("/api/v1/projects/clone/prepare", {
				body: { remoteUrl, destinationParent },
				signal: controller.signal,
			});
			if (error) throw error;
			if (!data) throw new Error("Could not clone repository");
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
			if (error) throw new Error(apiErrorMessage(error, "Could not keep the cloned checkout"));
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
