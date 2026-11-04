import { useFocusEffect } from "expo-router";
import { useCallback, useRef, useState } from "react";
import { AppState } from "react-native";
import { shouldPoll } from "../appStatePoll";
import type { ServerConfig } from "../config";
import { userFacingError } from "../connectionError";
import { getWorkspaceManifest, type WorkspaceManifest } from "./api";

/** How often an open Files screen asks whether the agent changed anything. */
export const WORKSPACE_POLL_MS = 5_000;

export type ManifestState = {
	manifest: WorkspaceManifest | null;
	loading: boolean;
	error?: string;
	refreshing: boolean;
	refresh: () => Promise<void>;
};

/**
 * The session's changed-file manifest, kept fresh while the screen is focused
 * and the app is in front. A poll whose `workspaceVersion` matches what is
 * already shown keeps the old object, so an idle session re-renders nothing.
 */
export function useWorkspaceManifest(config: ServerConfig | null, sessionId: string, enabled = true): ManifestState {
	const [manifest, setManifest] = useState<WorkspaceManifest | null>(null);
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	const [error, setError] = useState<string>();
	const current = useRef({ config, sessionId });
	current.current = { config, sessionId };

	const load = useCallback(async () => {
		if (!config || !enabled) return;
		try {
			const next = await getWorkspaceManifest(config, sessionId);
			if (current.current.config !== config || current.current.sessionId !== sessionId) return;
			setManifest((previous) => previous && previous.workspaceVersion && previous.workspaceVersion === next.workspaceVersion ? previous : next);
			setError(undefined);
		} catch (cause) {
			if (current.current.config === config && current.current.sessionId === sessionId) setError(userFacingError(cause, "Couldn't load this session's files."));
		} finally {
			if (current.current.config === config && current.current.sessionId === sessionId) setLoading(false);
		}
	}, [config, enabled, sessionId]);

	useFocusEffect(useCallback(() => {
		void load();
		const timer = setInterval(() => {
			if (shouldPoll(AppState.currentState)) void load();
		}, WORKSPACE_POLL_MS);
		return () => clearInterval(timer);
	}, [load]));

	const refresh = useCallback(async () => {
		setRefreshing(true);
		try { await load(); } finally { setRefreshing(false); }
	}, [load]);

	return { manifest, loading: loading && !manifest, error, refreshing, refresh };
}
