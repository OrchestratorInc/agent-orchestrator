import { useCallback, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";

type PreparedClone = components["schemas"]["ClonePreparationResult"];
export type CloneProgress = components["schemas"]["CloneProgress"];

// git throttles its own progress display to roughly one update per second, so
// a sub-second poll captures every phase change without busy-looping the daemon.
const CLONE_PROGRESS_POLL_MS = 500;

export function usePreparedClone(hostId?: string) {
	const currentRef = useRef<PreparedClone | null>(null);
	const [isCleaning, setIsCleaning] = useState(false);
	const [progress, setProgress] = useState<CloneProgress | null>(null);

	const prepare = useCallback(async (remoteUrl: string, destinationParent: string) => {
		const client = hostId ? clientForSessionHost(hostId) : apiClient;
		// The prepare call blocks until the clone finishes; poll its progress
		// side-channel while it runs so the dialog can render live git output.
		setProgress(null);
		const timer = window.setInterval(() => {
			void client.GET("/api/v1/projects/clone/progress", {
				params: { query: { remoteUrl, destinationParent } },
			}).then(({ data }) => {
				if (data && typeof data.phase === "string") setProgress(data);
			}).catch(() => {
				// Progress is best-effort: the route 404s before the clone
				// registers and older daemons do not serve it at all.
			});
		}, CLONE_PROGRESS_POLL_MS);
		try {
			const { data, error } = await client.POST("/api/v1/projects/clone/prepare", {
				body: { remoteUrl, destinationParent },
			});
			if (error) throw error;
			if (!data) throw new Error("Could not clone repository");
			currentRef.current = data;
			return data;
		} finally {
			window.clearInterval(timer);
			setProgress(null);
		}
	}, [hostId]);

	const cleanup = useCallback(async () => {
		const current = currentRef.current;
		if (!current) return;
		setIsCleaning(true);
		try {
			const { error } = await (hostId ? clientForSessionHost(hostId) : apiClient).POST("/api/v1/projects/clone/cleanup", {
				body: { path: current.path, preparationId: current.preparationId },
			});
			if (error) throw new Error(apiErrorMessage(error, "Could not clean up prepared clone"));
			currentRef.current = null;
		} finally {
			setIsCleaning(false);
		}
	}, [hostId]);

	const complete = useCallback(() => {
		currentRef.current = null;
	}, []);

	const current = useCallback(() => currentRef.current, []);

	return { cleanup, complete, current, isCleaning, prepare, progress };
}
